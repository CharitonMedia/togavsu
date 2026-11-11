package doctender

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// group is one decided photo grouping.
type group struct {
	ID      domain.ID
	How     string
	Title   string
	Members []domain.ID
	Output  domain.ID
}

func (s *Service) inGroup(ctx context.Context, file domain.ID) (bool, error) {
	var n int
	err := s.row(ctx, `SELECT COUNT(*) FROM doc_tender_group_members WHERE file_id = ?`, file).Scan(&n)
	return n > zero, err
}

func (s *Service) groupOf(ctx context.Context, file domain.ID) (group, error) {
	var g group
	var out sql.NullString
	err := s.row(ctx, `SELECT g.id, g.how, g.title, g.output_file_id FROM doc_tender_groups g JOIN doc_tender_group_members m ON m.group_id = g.id WHERE m.file_id = ?`,
		file).Scan(&g.ID, &g.How, &g.Title, &out)
	if err != nil {
		return g, err
	}
	g.Output = domain.ID(out.String)
	rows, err := s.query(ctx, `SELECT file_id FROM doc_tender_group_members WHERE group_id = ? ORDER BY position`, g.ID)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			return g, err
		}
		g.Members = append(g.Members, id)
	}
	return g, rows.Err()
}

// awaitGroup parks a read photo until its matter has had no new photos for
// doctender.photo_quiet.
func (s *Service) awaitGroup(ctx context.Context, j *Job) error {
	quiet, err := s.duration(ctx, "doctender.photo_quiet")
	if err != nil {
		return err
	}
	j.Status, j.Stage, j.NextAttempt = StatusQueued, stageAwaitingGroup, s.now().Add(quiet)
	if err := s.release(ctx, *j); err != nil {
		return err
	}
	return errDeferred
}

// groupScope is the set of photos one grouping may span: one matter, or, for photos
// with no matter, one contact. Photos with neither are never grouped with anything.
type groupScope struct {
	Matter, Contact domain.ID
}

// groupDue starts a grouping job for each matter (or, with no matter, each contact)
// whose read photos have waited out the quiet time with no newer photo arriving. A
// photo with no matter and no contact is its own document at once: nothing ties it to
// any other photo, so it is never offered to the grouping model with other clients'.
// groupDueAt is when groupDue next starts a group: the earliest scope (a matter, or a
// contact for photos with no matter) holding a photo that waits with no group job, at
// its newest photo arrival plus doctender.photo_quiet. ok is false when no photo waits.
func (s *Service) groupDueAt(ctx context.Context) (time.Time, bool, error) {
	quiet, err := s.duration(ctx, "doctender.photo_quiet")
	if err != nil {
		return time.Time{}, false, err
	}
	newest, waiting, err := s.photoArrivals(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	var due int64
	found := false
	for sc := range waiting {
		if n := newest[sc]; !found || n < due {
			due, found = n, true
		}
	}
	if !found {
		return time.Time{}, false, nil
	}
	return fromUS(due).Add(quiet), true, nil
}

// photoArrivals is, per scope, the newest arrival among photos still being read or
// waiting on a group, and which scopes hold a photo that waits with no group job.
// Only photos count toward the quiet time: a PDF or a recording arriving on the
// matter does not hold its photos back. A job's kind is decided as runFile decides
// it, from the name the file arrived as, then its name, and the version's type.
func (s *Service) photoArrivals(ctx context.Context) (map[groupScope]int64, map[groupScope]bool, error) {
	rows, err := s.query(ctx, `SELECT COALESCE(j.matter_id, ''), COALESCE(f.contact_id, ''), j.created_at, j.stage, j.status, j.group_job_id, f.arrived_name, f.name, v.media_type
FROM doc_tender_jobs j JOIN files f ON f.id = j.file_id JOIN file_versions v ON v.id = j.file_version_id
WHERE j.kind = 'file' AND j.status NOT IN ('published', 'exception') AND j.stage IN ('check', 'extract', 'awaiting_group')`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	newest, waiting := map[groupScope]int64{}, map[groupScope]bool{}
	for rows.Next() {
		var matter, contact, stage, status, group, arrived, name, media string
		var at int64
		if err := rows.Scan(&matter, &contact, &at, &stage, &status, &group, &arrived, &name, &media); err != nil {
			return nil, nil, err
		}
		kind := fileKind(arrived, media)
		if kind == typeUnsupported {
			kind = fileKind(name, media)
		}
		if kind != typePhoto && stage != stageAwaitingGroup {
			continue
		}
		sc := groupScope{Matter: domain.ID(matter)}
		if matter == "" {
			sc.Contact = domain.ID(contact)
		}
		newest[sc] = max(newest[sc], at)
		if stage == stageAwaitingGroup && status == string(StatusQueued) && group == "" {
			waiting[sc] = true
		}
	}
	return newest, waiting, rows.Err()
}

func (s *Service) groupDue(ctx context.Context) error {
	quiet, err := s.duration(ctx, "doctender.photo_quiet")
	if err != nil {
		return err
	}
	waiting, err := s.jobsWhere(ctx, `stage = ? AND status = ? AND group_job_id = '' ORDER BY created_at, id`, stageAwaitingGroup, StatusQueued)
	if err != nil {
		return err
	}
	byScope := map[groupScope][]Job{}
	var order []groupScope
	for _, j := range waiting {
		sc := groupScope{Matter: j.MatterID}
		if sc.Matter == "" {
			if err := s.row(ctx, `SELECT COALESCE(contact_id, '') FROM files WHERE id = ?`, j.FileID).Scan(&sc.Contact); err != nil {
				return err
			}
			if sc.Contact == "" {
				if err := s.singlePhoto(ctx, j); err != nil {
					return err
				}
				continue
			}
		}
		if _, ok := byScope[sc]; !ok {
			order = append(order, sc)
		}
		byScope[sc] = append(byScope[sc], j)
	}
	// The newest photo arrival in each scope, including photos still being read.
	newest, _, err := s.photoArrivals(ctx)
	if err != nil {
		return err
	}
	for _, sc := range order {
		if n, ok := newest[sc]; ok && s.now().Sub(fromUS(n)) < quiet {
			continue
		}
		if err := s.startGroup(ctx, sc.Matter, byScope[sc]); err != nil {
			return err
		}
	}
	return nil
}

// singlePhoto records a photo as its own document and queues it for labeling.
func (s *Service) singlePhoto(ctx context.Context, j Job) error {
	return s.tx(ctx, func(ctx context.Context) error {
		gid, err := s.id()
		if err != nil {
			return err
		}
		at := us(s.now())
		if err := s.insertGroup(ctx, gid, at, Job{MatterID: j.MatterID}, "single", "photo", []domain.ID{j.FileID}, ""); err != nil {
			return err
		}
		_, err = s.exec(ctx, `UPDATE doc_tender_jobs SET stage = ?, status = ?, next_attempt_at = ?, updated_at = ? WHERE id = ?`,
			stageLabel, StatusQueued, at, at, j.ID)
		if err == nil {
			s.signal()
		}
		return err
	})
}

func (s *Service) startGroup(ctx context.Context, matter domain.ID, photos []Job) error {
	return s.tx(ctx, func(ctx context.Context) error {
		gid, err := s.enqueue(ctx, kindGroup, photos[zero].FileID, photos[zero].VersionID, matter, reasonArrival)
		if err != nil {
			return err
		}
		for _, p := range photos {
			if _, err := s.exec(ctx, `UPDATE doc_tender_jobs SET group_job_id = ?, updated_at = ? WHERE id = ? AND group_job_id = ''`, gid, us(s.now()), p.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

const groupPrompt = `These photos arrived together in one law firm matter. Decide which photos are pages of the same document and in what page order, using what each page shows (headings, page numbers, letterheads). A photo that is not a page of a multi-page document is its own group of kind "photo".
Answer with one JSON object: {"groups": [{"title": "short name of the document", "kind": "document" or "photo", "photos": [photo numbers in page order]}]}. Use every photo number exactly once.`

type groupAnswer struct {
	Groups []struct {
		Title  string `json:"title"`
		Kind   string `json:"kind"`
		Photos []int  `json:"photos"`
	} `json:"groups"`
	// Files are the photos the numbers name, in the order they were asked about. The
	// model does not write it; the saved answer records it so a retry that finds some
	// photos already grouped still applies each number to the right photo.
	Files []domain.ID `json:"files,omitempty"`
}

// fileGroups is the saved answer's groups as file ids in page order. A number outside
// the recorded photos is an error, never a panic.
func (g groupAnswer) fileGroups() ([][]domain.ID, error) {
	var out [][]domain.ID
	for _, gr := range g.Groups {
		var ids []domain.ID
		for _, n := range gr.Photos {
			if n < one || n > len(g.Files) {
				return nil, fmt.Errorf("the saved grouping names photo %d of %d", n, len(g.Files))
			}
			ids = append(ids, g.Files[n-one])
		}
		out = append(out, ids)
	}
	return out, nil
}

// partition is a grouping in comparable form: each group's photo numbers in order.
func (g groupAnswer) partition(n int) ([][]int, error) {
	seen := map[int]bool{}
	var out [][]int
	for _, gr := range g.Groups {
		if len(gr.Photos) == 0 {
			continue
		}
		for _, p := range gr.Photos {
			if p < one || p > n || seen[p] {
				return nil, fmt.Errorf("photo %d is out of range or repeated", p)
			}
			seen[p] = true
		}
		out = append(out, gr.Photos)
	}
	if len(seen) != n {
		return nil, fmt.Errorf("%d of %d photos placed", len(seen), n)
	}
	sortGroups(out)
	return out, nil
}

func sortGroups(gs [][]int) {
	for i := one; i < len(gs); i++ {
		for k := i; k > zero && gs[k][zero] < gs[k-one][zero]; k-- {
			gs[k], gs[k-one] = gs[k-one], gs[k]
		}
	}
}

// runGroup decides a matter's photo grouping once: the grouping model and its check
// model must give the same groups in the same page order. Each document group becomes
// one PDF (the photos as its pages, in order) with one Markdown copy, linked to the
// photos as its sources.
func (s *Service) runGroup(ctx context.Context, j Job) error {
	ctx = withCost(ctx, j.ID, "")
	// As for a single file, the matter's status comes before any model call: a matter
	// closed during the photos' quiet time holds the group until it reopens.
	if j.MatterID != "" {
		m, err := s.d.Matters.Matter(ctx, j.MatterID)
		if err != nil {
			return err
		}
		if m.Status == "closed" {
			j.Status, j.Stage, j.LastError = StatusMatterClosed, stageCheck, "Not processed (matter closed)"
			return s.release(ctx, j)
		}
	}
	photos, err := s.jobsWhere(ctx, `group_job_id = ? ORDER BY created_at, id`, j.ID)
	if err != nil {
		return err
	}
	type member struct {
		job  Job
		file files.FileView
		read photoRead
		unit Unit
		data []byte
	}
	var ms []member
	for _, pj := range photos {
		locked, err := s.inGroup(ctx, pj.FileID)
		if err != nil {
			return err
		}
		if locked {
			// Already grouped, by a person or an earlier decision: settled stays settled.
			pj.Status, pj.Stage, pj.FinishedAt = StatusPublished, stageGrouped, s.now()
			if err := s.saveJob(ctx, pj); err != nil {
				return err
			}
			continue
		}
		f, err := s.d.Files.Get(ctx, s.d.System, pj.FileID)
		if err != nil {
			return err
		}
		us_, err := s.units(ctx, pj.ID)
		if err != nil {
			return err
		}
		var u Unit
		for _, x := range us_ {
			if x.Name == "photo" {
				u = x
			}
		}
		var r photoRead
		_ = json.Unmarshal([]byte(u.Text), &r)
		dl, err := s.d.Files.OpenForProcessing(ctx, s.d.System, pj.FileID, pj.VersionID)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(dl.Body)
		_ = dl.Close()
		if err != nil {
			return err
		}
		ms = append(ms, member{pj, f, r, u, data})
	}
	if len(ms) == 0 {
		j.Status, j.Stage, j.FinishedAt = StatusPublished, stageDone, s.now()
		return s.release(ctx, j)
	}
	j.Status, j.Stage = StatusRunning, "grouping"
	if err := s.saveJob(ctx, j); err != nil {
		return err
	}
	if err := s.insertUnit(ctx, j.ID, "grouping", zero, nil); err != nil {
		return err
	}
	units, err := s.units(ctx, j.ID)
	if err != nil {
		return err
	}
	u := &units[zero]
	spec := unitSpec{Name: "grouping", Tasks: []string{TaskGrouping}, Exception: "check_disagreement", Run: func(ctx context.Context, checkMethod bool) (unitWork, error) {
		ask := func(check bool) (groupAnswer, string, error) {
			parts := []Part{}
			for k, m := range ms {
				parts = append(parts, Part{Text: fmt.Sprintf("Photo %d. Text read from it:\n%s", k+one, quoted(strings.ReplaceAll(m.read.Text, "\n", " / ")))})
				parts = append(parts, Part{MediaType: imageType(m.file.ArrivedAs), Name: m.file.ArrivedAs, Data: m.data})
			}
			resp, err := s.d.Models.Call(ctx, Request{Task: TaskGrouping, Check: check, Compared: true, System: groupPrompt, JSON: true, Parts: parts, MatterID: j.MatterID})
			model := resp.Provider + "/" + resp.Model
			if err != nil {
				return groupAnswer{}, model, err
			}
			var g groupAnswer
			if err := json.Unmarshal([]byte(jsonObject(resp.Text)), &g); err != nil {
				return groupAnswer{}, model, fmt.Errorf("unreadable grouping: %w", err)
			}
			return g, model, nil
		}
		first, model, err := ask(checkMethod)
		if err != nil {
			return unitWork{Method: "grouping", Model: model}, err
		}
		a, err := first.partition(len(ms))
		if err != nil {
			return unitWork{Method: "grouping", Model: model, CheckResult: err.Error()}, nil
		}
		second, checkModel, err := ask(!checkMethod)
		if err == nil {
			err = selfCompared(model, checkModel)
		}
		if err != nil {
			return unitWork{Method: "grouping", Model: model}, err
		}
		b, err := second.partition(len(ms))
		if err != nil {
			return unitWork{Method: "grouping", Model: model, CheckModel: checkModel, CheckResult: "check model: " + err.Error()}, nil
		}
		agree := reflect.DeepEqual(a, b)
		result := fmt.Sprintf("model %v check %v", a, b)
		for _, m := range ms {
			first.Files = append(first.Files, m.file.ID)
		}
		return unitWork{Text: jsonText(first), Method: "grouping", Model: model, CheckMethod: "check_model_agreement", CheckModel: checkModel,
			CheckResult: result, Passed: agree}, nil
	}}
	if err := s.runUnit(ctx, &j, u, spec); err != nil {
		return err
	}
	if u.Status != unitPassed {
		// The disagreement is in the review queue; each photo stays available and is
		// processed as a photo on its own until a person groups it.
		for _, m := range ms {
			pj := m.job
			if err := s.openException(ctx, &pj, "check_disagreement", "the grouping models disagree: "+u.CheckResult, map[string]any{"grouping": u.Text}); err != nil {
				return err
			}
			pj.Status, pj.Stage, pj.FinishedAt = StatusException, stageAwaitingGroup, s.now()
			if err := s.saveJob(ctx, pj); err != nil {
				return err
			}
		}
		j.Status, j.Stage, j.FinishedAt = StatusException, stageDone, s.now()
		return s.release(ctx, j)
	}
	var answer groupAnswer
	if err := json.Unmarshal([]byte(u.Text), &answer); err != nil {
		return err
	}
	groups, err := answer.fileGroups()
	if err != nil {
		return err
	}
	byFile := map[domain.ID]member{}
	for _, m := range ms {
		byFile[m.file.ID] = m
	}
	for k, gr := range answer.Groups {
		var members []domain.ID
		var reads []Unit
		var datas [][]byte
		for _, id := range groups[k] {
			m, ok := byFile[id]
			if !ok {
				// Grouped by an earlier pass of this job that stopped part way.
				members = nil
				break
			}
			members = append(members, id)
			reads = append(reads, m.unit)
			datas = append(datas, m.data)
		}
		if len(members) == 0 {
			continue
		}
		how := "model_agreement"
		if gr.Kind == "photo" && len(members) == one {
			how = "single"
		}
		if err := s.makeGroup(ctx, j, how, gr.Title, members, reads, datas); err != nil {
			return err
		}
	}
	j.Status, j.Stage, j.FinishedAt = StatusPublished, stageDone, s.now()
	return s.release(ctx, j)
}

func imageType(name string) string {
	ext := strings.ToLower(extOf(name))
	switch ext {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "tif":
		return "image/tiff"
	}
	return "image/" + ext
}

// makeGroup records a grouping and, for a document, builds its PDF, links the photos
// as its sources and queues the PDF's job with its pages already read and checked.
func (s *Service) makeGroup(ctx context.Context, j Job, how, title string, members []domain.ID, reads []Unit, datas [][]byte) error {
	gid, err := s.id()
	if err != nil {
		return err
	}
	at := us(s.now())
	if how == "single" {
		return s.tx(ctx, func(ctx context.Context) error {
			if err := s.insertGroup(ctx, gid, at, j, how, title, members, ""); err != nil {
				return err
			}
			// The photo is its own document: it is labeled and filed on its own.
			_, err := s.exec(ctx, `UPDATE doc_tender_jobs SET stage = ?, status = ?, next_attempt_at = ?, updated_at = ? WHERE file_id = ? AND group_job_id = ?`,
				stageLabel, StatusQueued, at, at, members[zero], j.ID)
			return err
		})
	}
	var views []files.FileView
	for _, m := range members {
		f, err := s.d.Files.Get(ctx, s.d.System, m)
		if err != nil {
			return err
		}
		views = append(views, f)
	}
	nf, err := groupedFile(title, views)
	if err != nil {
		return err
	}
	pdf, err := imagesToPDF(datas)
	if err != nil {
		return err
	}
	out, err := s.d.Files.Create(ctx, s.d.System, nf, bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context) error {
		if err := s.insertGroup(ctx, gid, at, j, how, title, members, out.ID); err != nil {
			return err
		}
		for _, m := range members {
			if _, err := s.d.Files.Link(ctx, s.d.System, out.ID, m, "grouped_photo_source"); err != nil {
				return err
			}
			if _, err := s.exec(ctx, `UPDATE doc_tender_jobs SET stage = ?, status = ?, finished_at = ?, updated_at = ? WHERE file_id = ? AND kind = 'file' AND (status NOT IN ('published', 'exception') OR stage = ?)`,
				stageGrouped, StatusPublished, at, at, m, stageAwaitingGroup); err != nil {
				return err
			}
		}
		matter := domain.ID("")
		if out.MatterID != nil {
			matter = *out.MatterID
		}
		jid, err := s.Enqueue(ctx, out.ID, *out.CurrentVersionID, matter, reasonArrival)
		if err != nil {
			return err
		}
		for k, r := range reads {
			preset := r
			// A page keeps its photo's result: a photo that failed its check is an
			// unresolved page of the PDF, never published as checked.
			preset.Method = methodGrouped
			if r.Status != unitPassed {
				preset.Status = unitFailed
			}
			preset.CheckResult = "photo " + strconv.Itoa(k+one) + ": " + r.CheckResult
			if err := s.insertUnit(ctx, jid, pageName(k), k, &preset); err != nil {
				return err
			}
		}
		return nil
	})
}

// groupedFile is the new PDF's record. Its pages are every member's pixels and text,
// so its access is the most restrictive of its members: attorney-only if any member
// is, shared with the client only if every member is. Its members share one matter,
// or, with no matter, one contact; anything else is refused.
func groupedFile(title string, ms []files.FileView) (files.NewFile, error) {
	first := ms[zero]
	nf := files.NewFile{Name: groupFileName(title), MediaType: "application/pdf", Source: first.Source, How: "grouping",
		SharedWithClient: true, Origin: first.Origin, Note: "grouped from " + strconv.Itoa(len(ms)) + " photos"}
	idOf := func(p *domain.ID) domain.ID {
		if p == nil {
			return ""
		}
		return *p
	}
	nf.MatterID, nf.ContactID = idOf(first.MatterID), idOf(first.ContactID)
	for _, f := range ms {
		if idOf(f.MatterID) != nf.MatterID {
			return files.NewFile{}, domain.Invalid("grouping", "a group's photos share one matter")
		}
		if nf.MatterID == "" && (nf.ContactID == "" || idOf(f.ContactID) != nf.ContactID) {
			return files.NewFile{}, domain.Invalid("grouping", "photos with no matter are grouped only within one contact")
		}
		if idOf(f.ContactID) != nf.ContactID {
			// Photos of one matter from different contacts: the PDF names none of them.
			nf.ContactID = ""
		}
		nf.AttorneyOnly = nf.AttorneyOnly || f.AttorneyOnly
		nf.SharedWithClient = nf.SharedWithClient && f.SharedWithClient
		if f.Source != nf.Source {
			nf.Source = "upload"
		}
		if f.Origin != nf.Origin {
			nf.Origin = ""
		}
	}
	return nf, nil
}

func (s *Service) insertGroup(ctx context.Context, gid domain.ID, at int64, j Job, how, title string, members []domain.ID, out domain.ID) error {
	var jobRef any = string(j.ID)
	if j.ID == "" {
		jobRef = nil
	}
	if _, err := s.exec(ctx, `INSERT INTO doc_tender_groups (id, created_at, updated_at, created_by, matter_id, how, title, output_file_id, job_id) VALUES (?,?,?,?,?,?,?,?,?)`,
		gid, at, at, string(s.d.System.UserID), nullID(j.MatterID), how, title, nullID(out), jobRef); err != nil {
		return err
	}
	for k, m := range members {
		if _, err := s.exec(ctx, `INSERT INTO doc_tender_group_members (group_id, file_id, position) VALUES (?,?,?)`, gid, m, k); err != nil {
			return err
		}
	}
	return nil
}

func groupFileName(title string) string {
	name := slug(title)
	if name == "" {
		name = "grouped-photos"
	}
	return name + ".pdf"
}

// PersonGrouping records a person's grouping of photos: groups separated by ";", photo
// file ids in page order separated by ",". It is locked: Doc Tender never regroups
// these photos.
// resolveGroupingItems closes the open grouping disagreement items on the photos a
// person just grouped: the person's grouping is the answer to them, and an item left
// open would keep each photo in the exception state after it is filed.
func (s *Service) resolveGroupingItems(ctx context.Context, p domain.Principal, photos []domain.ID) error {
	at := us(s.now())
	for _, id := range photos {
		if _, err := s.exec(ctx, `UPDATE doc_tender_exceptions SET status = 'resolved', resolved_by = ?, resolved_at = ?, resolution = ?, updated_at = ?
WHERE file_id = ? AND kind = 'check_disagreement' AND status = 'open' AND best_answer LIKE ?`,
			string(p.UserID), at, "grouped by a person", at, id, `%"grouping":%`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) PersonGrouping(ctx context.Context, p domain.Principal, spec string) error {
	if !reviewer(p) {
		return domain.Forbidden("only admins and attorneys group photos")
	}
	for _, part := range strings.Split(spec, ";") {
		var members []domain.ID
		for _, id := range strings.Split(part, ",") {
			if id = strings.TrimSpace(id); id != "" {
				members = append(members, domain.ID(id))
			}
		}
		if len(members) == 0 {
			continue
		}
		var reads []Unit
		var datas [][]byte
		var matter domain.ID
		for k, m := range members {
			f, err := s.d.Files.Get(ctx, p, m)
			if err != nil {
				return err
			}
			fm := domain.ID("")
			if f.MatterID != nil {
				fm = *f.MatterID
			}
			if k > zero && fm != matter {
				return domain.Invalid("grouping", "a group's photos share one matter")
			}
			matter = fm
			if locked, err := s.inGroup(ctx, m); err != nil || locked {
				if err == nil {
					err = domain.Conflict("photo " + string(m) + " is already grouped")
				}
				return err
			}
			r, err := s.photoUnitFor(ctx, m)
			if err != nil {
				return err
			}
			reads = append(reads, r)
			dl, err := s.d.Files.OpenForProcessing(ctx, s.d.System, m, *f.CurrentVersionID)
			if err != nil {
				return err
			}
			data, err := io.ReadAll(dl.Body)
			_ = dl.Close()
			if err != nil {
				return err
			}
			datas = append(datas, data)
		}
		how := "person"
		j := Job{MatterID: matter}
		if len(members) == one {
			// One photo on its own: still a person's decision, and locked.
			if err := s.tx(ctx, func(ctx context.Context) error {
				gid, err := s.id()
				if err != nil {
					return err
				}
				at := us(s.now())
				if err := s.insertGroup(ctx, gid, at, j, how, "photo", members, ""); err != nil {
					return err
				}
				if err := s.resolveGroupingItems(ctx, p, members); err != nil {
					return err
				}
				// The photo is its own document: a photo the grouping models could not
				// settle goes on to be labeled and filed, as singlePhoto does.
				_, err = s.exec(ctx, `UPDATE doc_tender_jobs SET stage = ?, status = ?, next_attempt_at = ?, finished_at = 0, updated_at = ? WHERE file_id = ? AND kind = 'file' AND stage = ?`,
					stageLabel, StatusQueued, at, at, members[zero], stageAwaitingGroup)
				return err
			}); err != nil {
				return err
			}
			continue
		}
		if err := s.makeGroup(ctx, j, how, "grouped photos", members, reads, datas); err != nil {
			return err
		}
		if err := s.resolveGroupingItems(ctx, p, members); err != nil {
			return err
		}
	}
	s.signal()
	return nil
}

func (s *Service) photoUnitFor(ctx context.Context, file domain.ID) (Unit, error) {
	jobs, err := s.jobsWhere(ctx, `file_id = ? AND kind = 'file' ORDER BY created_at DESC`, file)
	if err != nil {
		return Unit{}, err
	}
	for _, j := range jobs {
		us_, err := s.units(ctx, j.ID)
		if err != nil {
			return Unit{}, err
		}
		for _, u := range us_ {
			if u.Name == "photo" && u.Status == unitPassed {
				return u, nil
			}
		}
	}
	return Unit{}, errors.New("doctender: the photo has not been read yet")
}
