package doctender

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// Exception kinds (the API contract's enum).
var exceptionKinds = map[string]string{
	"low_confidence":     "Low confidence",
	"check_disagreement": "Check models disagree",
	"unmatched_matter":   "Matter not matched",
	"extraction_failed":  "Extraction failed",
	"password_protected": "Password-protected",
	"damaged":            "Damaged file",
	"unsupported_format": "Unsupported format",
	"provider_down":      "AI provider down",
}

// Exception is one review-queue item (DocTenderException).
type Exception struct {
	ID         domain.ID      `json:"id"`
	CreatedAt  string         `json:"created_at"`
	CreatedBy  domain.ID      `json:"created_by"`
	UpdatedAt  string         `json:"updated_at"`
	FileID     domain.ID      `json:"file_id"`
	Kind       string         `json:"kind"`
	Detail     string         `json:"detail"`
	BestAnswer map[string]any `json:"best_answer"`
	Status     string         `json:"status"`
	ResolvedBy *domain.ID     `json:"resolved_by"`
	ResolvedAt *string        `json:"resolved_at"`
	Resolution string         `json:"resolution"`
	jobID      domain.ID
	versionID  domain.ID
	openedAt   time.Time
	emailedAt  int64
}

// KindLabel names an exception kind for people.
func KindLabel(kind string) string { return exceptionKinds[kind] }

// Grouping reports a photo grouping disagreement: it has no single answer to accept,
// only a person's grouping (Correct with a grouping).
func (e Exception) Grouping() bool {
	_, ok := e.BestAnswer["grouping"]
	return ok
}

const excCols = `id, created_at, created_by, updated_at, file_id, kind, detail, best_answer, status, COALESCE(resolved_by, ''), resolved_at, resolution,
COALESCE(job_id, ''), COALESCE(file_version_id, ''), opened_at, emailed_at`

func scanException(sc interface{ Scan(...any) error }) (Exception, error) {
	var e Exception
	var created, updated, resolvedAt, opened int64
	var by, best string
	err := sc.Scan(&e.ID, &created, &e.CreatedBy, &updated, &e.FileID, &e.Kind, &e.Detail, &best, &e.Status, &by, &resolvedAt, &e.Resolution,
		&e.jobID, &e.versionID, &opened, &e.emailedAt)
	if err != nil {
		return e, err
	}
	e.CreatedAt, e.UpdatedAt, e.openedAt = domain.FormatUTC(fromUS(created)), domain.FormatUTC(fromUS(updated)), fromUS(opened)
	if by != "" {
		id := domain.ID(by)
		e.ResolvedBy = &id
	}
	if resolvedAt != int64(zero) {
		t := domain.FormatUTC(fromUS(resolvedAt))
		e.ResolvedAt = &t
	}
	e.BestAnswer = map[string]any{}
	_ = json.Unmarshal([]byte(best), &e.BestAnswer)
	return e, nil
}

func (s *Service) exceptionsWhere(ctx context.Context, where string, args ...any) ([]Exception, error) {
	rows, err := s.query(ctx, `SELECT `+excCols+` FROM doc_tender_exceptions WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Exception
	for rows.Next() {
		e, err := scanException(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// openException adds a review-queue item unless the same one is already open. The
// admin mail goes out bundled (FlushExceptionMail) and reviewers get a notification.
func (s *Service) openException(ctx context.Context, j *Job, kind, detail string, best map[string]any) error {
	if _, ok := exceptionKinds[kind]; !ok {
		return fmt.Errorf("doctender: unknown exception kind %q", kind)
	}
	if best == nil {
		best = map[string]any{}
	}
	at := us(s.now())
	// The same item already open (a reprocess, a requeue after a matter was set) keeps
	// its id and takes the newest job, detail and best answer, so Accept never applies
	// an answer built before the matter changed and Retry acts on the current job.
	res, err := s.exec(ctx, `UPDATE doc_tender_exceptions SET job_id = ?, detail = ?, best_answer = ?, updated_at = ? WHERE file_id = ? AND file_version_id = ? AND kind = ? AND status = 'open'`,
		j.ID, detail, jsonText(best), at, j.FileID, j.VersionID, kind)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n > int64(zero) {
		return err
	}
	id, err := s.id()
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `INSERT INTO doc_tender_exceptions (id, created_at, updated_at, created_by, file_id, kind, opened_at, detail, status, resolved_by,
resolved_at, resolution, job_id, file_version_id, best_answer, emailed_at) VALUES (?,?,?,?,?,?,?,?,'open',NULL,0,'',?,?,?,0)`,
		id, at, at, string(s.d.System.UserID), j.FileID, kind, at, detail, j.ID, j.VersionID, jsonText(best))
	if err != nil {
		return err
	}
	if err := s.appendEvent(ctx, "doc_tender.exception_opened", "file", j.FileID, j.MatterID, KindLabel(kind)+": "+detail); err != nil {
		return err
	}
	if s.d.Notify != nil {
		if err := s.d.Notify.NotifyReviewers(ctx, "doc_tender.exception", "Doc Tender review: "+KindLabel(kind), "/doc-tender/review/"+string(id), j.MatterID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) openExceptionCount(ctx context.Context, file, version domain.ID) (int, error) {
	var n int
	err := s.row(ctx, `SELECT COUNT(*) FROM doc_tender_exceptions WHERE file_id = ? AND file_version_id = ? AND status = 'open'`, file, version).Scan(&n)
	return n, err
}

// autoResolve closes open exceptions of one kind that cleared on their own (a provider
// that came back).
func (s *Service) autoResolve(ctx context.Context, file domain.ID, kind, why string) error {
	_, err := s.exec(ctx, `UPDATE doc_tender_exceptions SET status = 'resolved', resolved_at = ?, resolution = ?, updated_at = ?
WHERE file_id = ? AND kind = ? AND status = 'open'`, us(s.now()), "auto: "+why, us(s.now()), file, kind)
	return err
}

// FlushExceptionMail mails new exceptions to the review recipients: the first at once,
// then at most one bundled email every doctender.exception_email_interval.
func (s *Service) FlushExceptionMail(ctx context.Context) error {
	if s.d.Mail == nil {
		return nil
	}
	interval, err := s.duration(ctx, "doctender.exception_email_interval")
	if err != nil {
		return err
	}
	var last int64
	err = s.row(ctx, `SELECT sent_at FROM doc_tender_mail WHERE kind = 'exceptions'`).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if last != int64(zero) && s.now().Sub(fromUS(last)) < interval {
		return nil
	}
	fresh, err := s.exceptionsWhere(ctx, `emailed_at = 0 AND status = 'open' ORDER BY opened_at`)
	if err != nil || len(fresh) == 0 {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Doc Tender put %d item(s) in the review queue. Each file stays listed, viewable and downloadable with its flag.\n\n", len(fresh))
	for _, e := range fresh {
		if what := s.restrictedItem(ctx, e); what != "" {
			fmt.Fprintf(&b, "- %s: %s (open it in the review queue)\n", what, KindLabel(e.Kind))
			continue
		}
		name := string(e.FileID)
		if f, err := s.d.Files.Get(ctx, s.d.System, e.FileID); err == nil {
			name = f.Name
		}
		fmt.Fprintf(&b, "- %s: %s (%s)\n", name, KindLabel(e.Kind), e.Detail)
	}
	b.WriteString("\nOpen the review queue: /doc-tender/review\n")
	subject := fmt.Sprintf("Doc Tender: %d item(s) to review", len(fresh))
	if err := s.mailReviewers(ctx, subject, b.String()); err != nil {
		return err
	}
	now := us(s.now())
	return s.tx(ctx, func(ctx context.Context) error {
		for _, e := range fresh {
			if _, err := s.exec(ctx, `UPDATE doc_tender_exceptions SET emailed_at = ? WHERE id = ?`, now, e.ID); err != nil {
				return err
			}
		}
		_, err := s.exec(ctx, `INSERT INTO doc_tender_mail (kind, sent_at, sent_on) VALUES ('exceptions', ?, '')
ON CONFLICT (kind) DO UPDATE SET sent_at = excluded.sent_at`, now)
		return err
	})
}

// mailReviewers sends to doctender.review_recipients: "admins" and/or addresses.
func (s *Service) mailReviewers(ctx context.Context, subject, body string) error {
	raw, err := s.setting(ctx, "doctender.review_recipients")
	if err != nil {
		return err
	}
	var errs []error
	for _, to := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if strings.EqualFold(to, "admins") {
			errs = append(errs, s.d.Mail.MailAdmins(ctx, subject, body))
			continue
		}
		errs = append(errs, s.d.Mail.Send(ctx, to, subject, body))
	}
	return errors.Join(errs...)
}

// restrictedItem says why an item may not be described in mail, or "" when it may.
// Mail goes to lists that can't be checked per reader (doctender.review_recipients can
// name any address), so an item on a walled matter, the file's own or the suggested
// one, or on an attorney-only file is mailed by kind only. A file that can't be read
// counts as walled.
func (s *Service) restrictedItem(ctx context.Context, e Exception) string {
	const walled = "a file on a walled matter"
	f, err := s.d.Files.Get(ctx, s.d.System, e.FileID)
	if err != nil {
		return walled
	}
	if f.MatterID != nil && s.matterWalled(ctx, *f.MatterID) {
		return walled
	}
	if m, _ := e.BestAnswer["matter_id"].(string); s.matterWalled(ctx, domain.ID(m)) {
		return walled
	}
	if f.AttorneyOnly {
		return "an attorney-only file"
	}
	return ""
}

// forReader is the item as p may see it. A suggested matter p can't read (a wall, a
// role) is removed with what matched it. An unmatched_matter item on a file that has a
// matter is only about the suggested matter, so a reader who can't read that matter
// doesn't see the item at all (ok is false).
func (s *Service) forReader(ctx context.Context, p domain.Principal, e Exception, f files.FileView) (Exception, bool) {
	m, _ := e.BestAnswer["matter_id"].(string)
	if m == "" || s.matterReadable(ctx, p, domain.ID(m)) {
		return e, true
	}
	if e.Kind == "unmatched_matter" && f.MatterID != nil {
		return Exception{}, false
	}
	best := make(map[string]any, len(e.BestAnswer))
	for k, v := range e.BestAnswer {
		best[k] = v
	}
	delete(best, "matter_id")
	delete(best, "matter")
	delete(best, "match_reason")
	e.BestAnswer = best
	return e, true
}

// visibleFile is the file as p sees it; a walled, attorney-only or missing file is
// not_found, exactly as files decides.
func (s *Service) visibleFile(ctx context.Context, p domain.Principal, id domain.ID) (files.FileView, error) {
	if p.Source != domain.SourceSystem && p.HasRole(domain.RoleClient) && !staffLike(p) {
		return files.FileView{}, domain.NotFound("file")
	}
	return s.d.Files.Get(ctx, p, id)
}

func staffLike(p domain.Principal) bool {
	return p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney) || p.HasRole(domain.RoleStaff)
}

// ExceptionQuery filters the review queue.
type ExceptionQuery struct {
	Status string
	Kind   string
	Cursor domain.ID
	Limit  int
}

// Exceptions lists the review queue for an admin or attorney. Items on files the caller
// can't see (a wall, attorney-only) are left out.
func (s *Service) Exceptions(ctx context.Context, p domain.Principal, q ExceptionQuery) ([]Exception, domain.ID, error) {
	if !reviewer(p) {
		return nil, "", domain.Forbidden("the review queue is for admins and attorneys")
	}
	where, args := `id > ?`, []any{string(q.Cursor)}
	if q.Status != "" {
		where, args = where+` AND status = ?`, append(args, q.Status)
	}
	if q.Kind != "" {
		where, args = where+` AND kind = ?`, append(args, q.Kind)
	}
	all, err := s.exceptionsWhere(ctx, where+` ORDER BY id`, args...)
	if err != nil {
		return nil, "", err
	}
	var out []Exception
	for _, e := range all {
		f, err := s.visibleFile(ctx, p, e.FileID)
		if err != nil {
			continue
		}
		e, ok := s.forReader(ctx, p, e, f)
		if !ok {
			continue
		}
		if q.Limit > zero && len(out) == q.Limit {
			return out, out[len(out)-one].ID, nil
		}
		out = append(out, e)
	}
	return out, "", nil
}

// OpenCount is the review queue's live count for the sidebar.
func (s *Service) OpenCount(ctx context.Context, p domain.Principal) (int, error) {
	if !reviewer(p) {
		return zero, nil
	}
	items, _, err := s.Exceptions(ctx, p, ExceptionQuery{Status: "open"})
	return len(items), err
}

// Exception reads one item.
func (s *Service) Exception(ctx context.Context, p domain.Principal, id domain.ID) (Exception, error) {
	if !reviewer(p) {
		return Exception{}, domain.Forbidden("the review queue is for admins and attorneys")
	}
	items, err := s.exceptionsWhere(ctx, `id = ?`, id)
	if err != nil {
		return Exception{}, err
	}
	if len(items) == 0 {
		return Exception{}, domain.NotFound("doc_tender_exception")
	}
	f, err := s.visibleFile(ctx, p, items[zero].FileID)
	if err != nil {
		return Exception{}, domain.NotFound("doc_tender_exception")
	}
	e, ok := s.forReader(ctx, p, items[zero], f)
	if !ok {
		return Exception{}, domain.NotFound("doc_tender_exception")
	}
	return e, nil
}

// Resolve is one person's decision on a review-queue item.
type Resolve struct {
	Action     string     `json:"action"`
	Name       string     `json:"name"`
	CategoryID *domain.ID `json:"category_id"`
	MatterID   *domain.ID `json:"matter_id"`
	Grouping   string     `json:"grouping"`
	Note       string     `json:"note"`
}

// ResolveException applies Accept, Correct, Assign matter, Retry or Keep as is. A
// name, category, matter or grouping a person sets here is locked through the files
// service (Rename, UpdateMetadata, MoveToFolder) and Doc Tender never changes it.
func (s *Service) ResolveException(ctx context.Context, p domain.Principal, id domain.ID, in Resolve) (Exception, error) {
	e, err := s.Exception(ctx, p, id)
	if err != nil {
		return Exception{}, err
	}
	if e.Status != "open" {
		return Exception{}, domain.Conflict("the item is already resolved")
	}
	f, err := s.d.Files.Get(ctx, p, e.FileID)
	if err != nil {
		return Exception{}, err
	}
	var requeue bool
	switch in.Action {
	case "accept":
		if e.Grouping() {
			// The two grouping models disagree, so there is no one answer to accept.
			return Exception{}, domain.Invalid("action", "the grouping models disagree: use Correct and say which photos go together")
		}
		if e.Kind == "unmatched_matter" {
			// Accepting the suggested matter sets it and requeues the file: the name and
			// folder are then built from that matter's tokens. Nothing is renamed or
			// filed here, since the proposed name and folder belong to no settled matter.
			m, _ := e.BestAnswer["matter_id"].(string)
			if m == "" {
				return Exception{}, domain.Invalid("action", "there is no suggested matter to accept; assign a matter")
			}
			if err := s.setMatter(ctx, p, f, domain.ID(m)); err != nil {
				return Exception{}, err
			}
			requeue = true
			break
		}
		if name, _ := e.BestAnswer["name"].(string); name != "" && name != f.Name {
			if err := s.acceptName(ctx, p, f, name); err != nil {
				return Exception{}, err
			}
		}
		if key, _ := e.BestAnswer["category"].(string); key != "" {
			if err := s.setCategory(ctx, p, f, key); err != nil {
				return Exception{}, err
			}
		}
	case "correct":
		if in.Name == "" && in.CategoryID == nil && in.MatterID == nil && in.Grouping == "" {
			return Exception{}, domain.Invalid("action", "a correction names a name, category, matter or grouping")
		}
		if in.MatterID != nil {
			if err := s.setMatter(ctx, p, f, *in.MatterID); err != nil {
				return Exception{}, err
			}
			requeue = in.Name == ""
		}
		if in.Name != "" {
			if _, err := s.d.Files.Rename(ctx, p, f.ID, in.Name); err != nil {
				return Exception{}, err
			}
		}
		if in.CategoryID != nil {
			key, err := s.categoryKey(ctx, *in.CategoryID)
			if err != nil {
				return Exception{}, err
			}
			if err := s.setCategory(ctx, p, f, key); err != nil {
				return Exception{}, err
			}
		}
		if in.Grouping != "" {
			if err := s.PersonGrouping(ctx, p, in.Grouping); err != nil {
				return Exception{}, err
			}
		}
	case "assign_matter":
		if in.MatterID == nil {
			return Exception{}, domain.Invalid("matter_id", "assign_matter needs a matter")
		}
		if err := s.setMatter(ctx, p, f, *in.MatterID); err != nil {
			return Exception{}, err
		}
		requeue = true
	case "retry":
		if e.Grouping() {
			// The photo's job waits on its group; a retry would requeue it with nothing
			// left to run and leave the photo unpublished. Correct settles the grouping.
			return Exception{}, domain.Invalid("action", "the grouping models disagree: use Correct and say which photos go together")
		}
		j, err := s.job(ctx, e.jobID)
		if err != nil {
			return Exception{}, err
		}
		if _, err := s.retryJob(ctx, j); err != nil {
			return Exception{}, err
		}
	case "keep":
		if e.Kind == "unmatched_matter" && f.MatterID != nil {
			// Keep as is confirms the file's matter: requeue so the file is named and
			// filed from it, as Accept and Assign matter do (matterSettled).
			requeue = true
		}
	default:
		return Exception{}, domain.Invalid("action", "action is accept, correct, assign_matter, retry or keep")
	}
	resolution := in.Action
	if in.Note != "" {
		resolution += ": " + in.Note
	}
	err = s.tx(ctx, func(ctx context.Context) error {
		if _, err := s.exec(ctx, `UPDATE doc_tender_exceptions SET status = 'resolved', resolved_by = ?, resolved_at = ?, resolution = ?, updated_at = ? WHERE id = ? AND status = 'open'`,
			string(p.UserID), us(s.now()), resolution, us(s.now()), id); err != nil {
			return err
		}
		matter := domain.ID("")
		if f.MatterID != nil {
			matter = *f.MatterID
		}
		if err := s.appendEvent(ctx, "doc_tender.exception_resolved", "file", f.ID, matter, KindLabel(e.Kind)+" resolved: "+resolution); err != nil {
			return err
		}
		if requeue {
			return nil
		}
		return s.settleIfReviewed(ctx, f.ID, e.versionID)
	})
	if err != nil {
		return Exception{}, err
	}
	if requeue {
		// The matter is now set: name and file it from the passed steps.
		if j, err := s.job(ctx, e.jobID); err == nil {
			j.Status, j.Stage, j.NextAttempt, j.FinishedAt = StatusQueued, stageLabel, s.now(), time.Time{}
			if err := s.saveJob(ctx, j); err != nil {
				return Exception{}, err
			}
			s.signal()
		}
	}
	return s.Exception(ctx, p, id)
}

// settleIfReviewed moves the version's finished jobs out of review once a person has
// settled its last open item, so the file reads as processed (PRD 8.21: a person's
// decision settles the item). Every job of the version is looked at, since a reprocess
// adds a job while items opened by an earlier one may still be open. A job stopped at
// its first check stays as it is, and so does a job with a unit that failed its check:
// its copy still holds an unresolved part, which is not published as correct whatever
// is done with the item.
func (s *Service) settleIfReviewed(ctx context.Context, file, version domain.ID) error {
	open, err := s.openExceptionCount(ctx, file, version)
	if err != nil || open > zero {
		return err
	}
	jobs, err := s.jobsWhere(ctx, `file_id = ? AND file_version_id = ? AND status = ? AND stage = ?`, file, version, StatusException, stageDone)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		units, err := s.units(ctx, j.ID)
		if err != nil {
			return err
		}
		settled := true
		for _, u := range units {
			settled = settled && u.Status == unitPassed
		}
		if !settled {
			continue
		}
		j.Status = StatusPublished
		if err := s.saveJob(ctx, j); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) setCategory(ctx context.Context, p domain.Principal, f files.FileView, key string) error {
	if f.CategoryID != nil && *f.CategoryID == key {
		return nil
	}
	if _, err := s.d.Files.UpdateMetadata(ctx, p, f.ID, files.Metadata{CategoryID: &key}); err != nil {
		return err
	}
	cats, err := s.categories(ctx, false)
	if err != nil {
		return err
	}
	for _, c := range cats {
		if c.Key == key && f.MatterID != nil {
			folder, err := s.d.Files.FolderByName(ctx, p, *f.MatterID, c.Folder)
			if err != nil {
				return err
			}
			_, err = s.d.Files.MoveToFolder(ctx, p, f.ID, folder.ID)
			return err
		}
	}
	return nil
}

// acceptName gives the file an accepted proposed name through the matter's clash
// resolution (PRD 8.21 step 6: -2, -3), then locks it as the person's decision. A file
// whose name already is that name with a clash suffix keeps it.
func (s *Service) acceptName(ctx context.Context, p domain.Principal, f files.FileView, name string) error {
	if f.MatterID != nil {
		unique, err := s.d.Files.UniqueName(ctx, *f.MatterID, name)
		if err != nil {
			return err
		}
		if unique != name {
			ext := path.Ext(name)
			if n, ok := strings.CutPrefix(strings.TrimSuffix(f.Name, ext), strings.TrimSuffix(name, ext)+"-"); ok && strings.HasSuffix(f.Name, ext) && allDigits(n) {
				unique = f.Name
			}
		}
		name = unique
	}
	_, err := s.d.Files.Rename(ctx, p, f.ID, name)
	return err
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func (s *Service) setMatter(ctx context.Context, p domain.Principal, f files.FileView, matter domain.ID) error {
	if f.MatterID != nil && *f.MatterID == matter {
		return nil
	}
	_, err := s.d.Files.UpdateMetadata(ctx, p, f.ID, files.Metadata{MatterID: &matter})
	return err
}

func (s *Service) categoryKey(ctx context.Context, id domain.ID) (string, error) {
	var key string
	err := s.row(ctx, `SELECT name FROM doc_categories WHERE id = ?`, id).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.Invalid("category_id", "unknown category")
	}
	return key, err
}

// Flag is what a file shows beside its name while an item about it is open or kept.
type Flag struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

// FileStatus is a file's Doc Tender state for the Files tab: its flags, and Not
// processed (matter closed) while its matter is closed.
type FileStatus struct {
	Status string `json:"status"`
	Label  string `json:"label"`
	Flags  []Flag `json:"flags"`
}

// Status reads a file's Doc Tender state as p sees the file.
func (s *Service) Status(ctx context.Context, p domain.Principal, fileID domain.ID) (FileStatus, error) {
	f, err := s.visibleFile(ctx, p, fileID)
	if err != nil {
		return FileStatus{}, err
	}
	out := FileStatus{Flags: []Flag{}}
	jobs, err := s.jobsWhere(ctx, `file_id = ? AND kind = 'file' ORDER BY created_at DESC, id DESC`, f.ID)
	if err != nil {
		return out, err
	}
	if len(jobs) > zero {
		out.Status = jobs[zero].Status
	}
	switch out.Status {
	case StatusMatterClosed:
		out.Label = "Not processed (matter closed)"
	case StatusPublished:
		out.Label = "Processed"
	case StatusException:
		out.Label = "Needs review"
	case StatusHeldAISetup:
		out.Label = "Waiting for AI setup"
	case StatusHeldProvider:
		out.Label = "Waiting for the AI provider"
	case "":
		out.Label = "Not processed yet"
	default:
		out.Label = "Processing"
	}
	items, err := s.exceptionsWhere(ctx, `file_id = ? AND (status = 'open' OR resolution LIKE 'keep%') ORDER BY opened_at`, f.ID)
	if err != nil {
		return out, err
	}
	for _, e := range items {
		if _, ok := s.forReader(ctx, p, e, f); !ok {
			continue
		}
		out.Flags = append(out.Flags, Flag{Kind: e.Kind, Label: KindLabel(e.Kind), Detail: e.Detail})
	}
	return out, nil
}
