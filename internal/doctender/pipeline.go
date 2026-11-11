package doctender

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// runFile takes one file job through check, extract, label, categorize, match, name
// and publish. Every unit is saved as it finishes, so a stopped job resumes at the
// first unit without a passing check.
func (s *Service) runFile(ctx context.Context, j Job) error {
	ctx = withCost(ctx, j.ID, "")
	f, err := s.d.Files.Get(ctx, s.d.System, j.FileID)
	if err != nil {
		return s.failJob(ctx, &j, err)
	}
	v, err := s.d.Files.Version(ctx, s.d.System, j.FileID, j.VersionID)
	if err != nil {
		return s.failJob(ctx, &j, err)
	}
	if f.MatterID != nil {
		j.MatterID = *f.MatterID
	}
	// Step 1: the matter's status comes before any model call.
	var matter MatterInfo
	if j.MatterID != "" {
		matter, err = s.d.Matters.Matter(ctx, j.MatterID)
		if err != nil {
			return s.failJob(ctx, &j, err)
		}
		if matter.Status == "closed" {
			j.Status, j.Stage, j.LastError = StatusMatterClosed, stageCheck, "Not processed (matter closed)"
			return s.release(ctx, j)
		}
	}
	if ready, err := s.d.Models.Ready(ctx); err != nil || !ready {
		if err != nil {
			return err
		}
		j.Status, j.NextAttempt = StatusHeldAISetup, s.now()
		return s.release(ctx, j)
	}
	j.Status = StatusRunning
	if j.Stage == stageCheck {
		j.Stage = stageExtract
	}
	if err := s.saveJob(ctx, j); err != nil {
		return err
	}
	dl, err := s.d.Files.OpenForProcessing(ctx, s.d.System, j.FileID, j.VersionID)
	if err != nil {
		return s.failJob(ctx, &j, err)
	}
	data, err := io.ReadAll(dl.Body)
	_ = dl.Close()
	if err != nil {
		// A failed integrity check: the bytes on the backend changed.
		return s.precheckException(ctx, &j, precheck{"damaged", "the stored bytes did not verify: " + err.Error()})
	}
	w := &work{job: &j, file: f, version: v, data: data, kind: fileKind(f.ArrivedAs, v.MediaType), matter: matter}
	if w.kind == typeUnsupported {
		w.kind = fileKind(f.Name, v.MediaType)
	}
	defer w.cleanup()
	units, err := s.units(ctx, j.ID)
	if err != nil {
		return err
	}
	var specs []unitSpec
	preset := len(units) > zero && units[zero].Method == methodGrouped
	if preset {
		// A grouped PDF: its pages were read and checked as photos.
		for _, u := range units {
			if strings.HasPrefix(u.Name, "page ") {
				specs = append(specs, unitSpec{Name: u.Name})
			}
		}
		w.kind = typePDF
	} else {
		var pre *precheck
		specs, pre, err = s.safePlan(ctx, w)
		if err != nil {
			return err
		}
		if pre != nil {
			return s.precheckException(ctx, &j, *pre)
		}
		// The injection screen, before any model reads the file.
		if held, err := s.screen(ctx, &j, w, func() ([]carrier, error) { return s.carriers(w) }, heldBeforeModels); held || err != nil {
			return err
		}
	}
	if len(units) == 0 {
		for k, sp := range specs {
			if err := s.insertUnit(ctx, j.ID, sp.Name, k, nil); err != nil {
				return err
			}
		}
		if units, err = s.units(ctx, j.ID); err != nil {
			return err
		}
	}
	byName := map[string]*Unit{}
	for k := range units {
		byName[units[k].Name] = &units[k]
	}
	for _, sp := range specs {
		u := byName[sp.Name]
		if u == nil || sp.Run == nil {
			continue
		}
		if err := s.runUnit(ctx, &j, u, sp); err != nil {
			return err
		}
	}
	// What a model read out of a scan, a photo or a recording is screened before any
	// other model sees it.
	if held, err := s.screen(ctx, &j, w, func() ([]carrier, error) { return unitCarriers(units), nil }, heldAfterReading); held || err != nil {
		return err
	}
	// Photos wait for the matter's quiet time and are grouped before labeling.
	if w.kind == typePhoto && j.Stage == stageExtract {
		grouped, err := s.inGroup(ctx, j.FileID)
		if err != nil {
			return err
		}
		if !grouped {
			return s.awaitGroup(ctx, &j)
		}
		if g, _ := s.groupOf(ctx, j.FileID); g.How != "single" {
			// A member of a document group: the group's PDF carries the Markdown copy.
			j.Status, j.Stage, j.FinishedAt = StatusPublished, stageGrouped, s.now()
			return s.release(ctx, j)
		}
	}
	c := s.assemble(w, units)
	tolerance, err := s.duration(ctx, "doctender.audio_max_gap")
	if err != nil {
		return err
	}
	c.Tolerance = tolerance.Seconds()
	// Steps 3 and 4.
	stepUnits := []unitSpec{s.labelUnit(w, c), s.categorizeUnit(w, c)}
	for k, sp := range stepUnits {
		if byName[sp.Name] == nil {
			if err := s.insertUnit(ctx, j.ID, sp.Name, len(specs)+k, nil); err != nil {
				return err
			}
			all, err := s.units(ctx, j.ID)
			if err != nil {
				return err
			}
			for x := range all {
				if all[x].Name == sp.Name {
					byName[sp.Name] = &all[x]
				}
			}
		}
	}
	j.Stage = stageLabel
	if err := s.saveJob(ctx, j); err != nil {
		return err
	}
	for _, sp := range stepUnits {
		if err := s.runUnit(ctx, &j, byName[sp.Name], sp); err != nil {
			return err
		}
	}
	return s.publish(ctx, w, c, byName["label"], byName["categorize"])
}

const methodGrouped = "grouped_photo"

func (s *Service) failJob(ctx context.Context, j *Job, err error) error {
	j.LastError = trimErr(err)
	j.Status, j.NextAttempt = StatusQueued, s.now().Add(s.waitDelay(ctx))
	if rerr := s.release(ctx, *j); rerr != nil {
		return errors.Join(err, rerr)
	}
	return err
}

// precheckException ends a file that can't be opened: it stays listed, viewable and
// downloadable, and goes to the review queue with a flag naming what failed.
func (s *Service) precheckException(ctx context.Context, j *Job, pre precheck) error {
	return s.tx(ctx, func(ctx context.Context) error {
		if err := s.openException(ctx, j, pre.Kind, pre.Detail, map[string]any{}); err != nil {
			return err
		}
		j.Status, j.Stage, j.LastError, j.FinishedAt = StatusException, stageCheck, pre.Detail, s.now()
		j.LeaseOwner, j.LeaseUntil = "", time.Time{}
		return s.saveJob(ctx, *j)
	})
}

// assemble builds the checked Markdown body from the units, in order, marking any
// unresolved unit in place.
func (s *Service) assemble(w *work, units []Unit) *content {
	c := &content{Pages: map[int]string{}}
	var b strings.Builder
	models := map[string]bool{}
	checks := map[string]int{}
	total := zero
	var timeline []timedLine
	for _, u := range units {
		if u.Name == "label" || u.Name == "categorize" {
			continue
		}
		total++
		if u.Model != "" {
			models[u.Method+" "+u.Model] = true
		} else if u.Method != "" {
			models[u.Method] = true
		}
		if u.Status == unitPassed {
			checks[u.CheckMethod]++
		}
		if u.Status != unitPassed {
			c.Unresolved = append(c.Unresolved, u.Name)
		}
		switch {
		case strings.HasPrefix(u.Name, "page "):
			n, _ := strconv.Atoi(strings.TrimPrefix(u.Name, "page "))
			c.PageCount = max(c.PageCount, n)
			fmt.Fprintf(&b, "## Page %d\n\n", n)
			if u.Status != unitPassed {
				fmt.Fprintf(&b, "[unresolved: page %d]\n\n", n)
				continue
			}
			text := u.Text
			if u.Method == methodGrouped {
				var p photoRead
				if json.Unmarshal([]byte(u.Text), &p) == nil {
					text = p.Text
					if p.Description != "" {
						text = strings.TrimSpace(text + "\n\n_Photo: " + p.Description + "_")
					}
				}
			}
			c.Pages[n] = text
			b.WriteString(text + "\n\n")
		case u.Name == "photo":
			c.PageCount = one
			if u.Status != unitPassed {
				b.WriteString("[unresolved: photo]\n\n")
				continue
			}
			var p photoRead
			_ = json.Unmarshal([]byte(u.Text), &p)
			fmt.Fprintf(&b, "## Photo\n\n%s\n\n", p.Description)
			if usableText(p.Text) {
				fmt.Fprintf(&b, "### Text in the photo\n\n%s\n\n", p.Text)
			}
			c.Pages[one] = p.Description + "\n" + p.Text
		case u.Name == "transcript":
			if u.Status != unitPassed {
				b.WriteString("[unresolved: transcript]\n\n")
				continue
			}
			var t transcript
			_ = json.Unmarshal([]byte(u.Text), &t)
			if t.Note != "" {
				timeline = append(timeline, timedLine{At: float64(zero), Kind: "Note", Text: t.Note})
			}
			for _, sg := range t.Segments {
				line := strings.TrimSpace(sg.Text)
				if sg.Speaker != "" {
					line = sg.Speaker + ": " + line
				}
				timeline = append(timeline, timedLine{At: sg.Start, Kind: "Said", Text: line})
			}
		case strings.HasPrefix(u.Name, "frames "):
			if u.Status != unitPassed {
				timeline = append(timeline, timedLine{At: float64(zero), Kind: "Note", Text: "[unresolved: " + u.Name + "]"})
				continue
			}
			var fs []screenFrame
			_ = json.Unmarshal([]byte(u.Text), &fs)
			for _, f := range fs {
				line := f.Description
				if usableText(f.Text) {
					line += " Text on screen: " + strings.ReplaceAll(f.Text, "\n", " / ")
				}
				timeline = append(timeline, timedLine{At: f.At, Kind: "On screen", Text: line})
			}
		default:
			c.PageCount = max(c.PageCount, one)
			if u.Status != unitPassed {
				fmt.Fprintf(&b, "[unresolved: %s]\n\n", u.Name)
				continue
			}
			b.WriteString(strings.TrimSpace(u.Text) + "\n\n")
			c.Pages[one] += u.Text + "\n"
		}
	}
	if len(timeline) > zero {
		sort.SliceStable(timeline, func(a, b int) bool { return timeline[a].At < timeline[b].At })
		title := "Transcript"
		for _, t := range timeline {
			if t.Kind == "On screen" {
				title = "Timeline: transcript and what's on screen"
			}
		}
		fmt.Fprintf(&b, "## %s\n\n", title)
		for _, t := range timeline {
			fmt.Fprintf(&b, "- **%s** %s: %s\n", hms(t.At), t.Kind, t.Text)
		}
		b.WriteString("\n")
		c.Times = timeline
		c.Pages[one] += b.String()
	}
	for m := range models {
		c.Models = append(c.Models, m)
	}
	sort.Strings(c.Models)
	for k, n := range checks {
		if k != "" {
			c.Checks = append(c.Checks, fmt.Sprintf("%s passed on %d", k, n))
		}
	}
	sort.Strings(c.Checks)
	c.Checks = append(c.Checks, fmt.Sprintf("%d of %d units passed", total-len(c.Unresolved), total))
	c.Body = strings.TrimSpace(b.String()) + "\n"
	return c
}

// markdownCopy is the published Markdown: a header naming the source file and version,
// the pages, the models and check results, then the body.
func markdownCopy(name string, v files.VersionView, c *content, label, cat *Unit) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "source: %s\n", name)
	fmt.Fprintf(&b, "source_version: %d\n", v.Number)
	fmt.Fprintf(&b, "source_sha256: %s\n", v.SHA256)
	if c.PageCount > zero {
		fmt.Fprintf(&b, "pages: %d\n", c.PageCount)
	}
	b.WriteString("models:\n")
	for _, m := range c.Models {
		fmt.Fprintf(&b, "  - %s\n", m)
	}
	for _, u := range []*Unit{label, cat} {
		if u != nil && u.Model != "" {
			fmt.Fprintf(&b, "  - %s %s\n", u.Name, u.Model)
		}
	}
	b.WriteString("checks:\n")
	for _, ch := range c.Checks {
		fmt.Fprintf(&b, "  - %s\n", ch)
	}
	for _, u := range []*Unit{label, cat} {
		if u != nil {
			fmt.Fprintf(&b, "  - %s: %s (%s)\n", u.Name, u.CheckResult, u.Status)
		}
	}
	if len(c.Unresolved) > zero {
		fmt.Fprintf(&b, "unresolved: %s\n", strings.Join(c.Unresolved, ", "))
	}
	b.WriteString("---\n\n")
	b.WriteString(c.Body)
	return b.String()
}

// Result is the published labels for one version (getDocTenderResult).
type Result struct {
	ID                  domain.ID      `json:"id"`
	FileID              domain.ID      `json:"file_id"`
	FileVersionID       domain.ID      `json:"file_version_id"`
	Summary             any            `json:"summary"`
	NameParts           NameParts      `json:"name_parts"`
	CategoryID          *domain.ID     `json:"category_id"`
	Category            string         `json:"-"`
	CategorySource      string         `json:"category_source,omitempty"`
	Fields              []CitedField   `json:"fields"`
	MatterID            *domain.ID     `json:"matter_id"`
	MarkdownRenditionID *domain.ID     `json:"markdown_rendition_id"`
	Confidences         map[string]any `json:"-"`
}

// NameParts is what the name was built from.
type NameParts struct {
	WhatItIs string  `json:"what_it_is"`
	DocDate  *string `json:"doc_date"`
}

// publish is step 7: the Markdown copy, the record (name, category, folder), the
// result, search and a timeline event commit in one transaction. Anything that did
// not pass its check is not published as correct: it is held for the review queue
// with Doc Tender's best answer.
func (s *Service) publish(ctx context.Context, w *work, c *content, labelU, catU *Unit) error {
	j := w.job
	threshold, err := s.threshold(ctx)
	if err != nil {
		return err
	}
	var l Label
	labelOK := labelU != nil && labelU.Status == unitPassed && json.Unmarshal([]byte(labelU.Text), &l) == nil
	var cat categoryOutcome
	catOK := catU != nil && catU.Status == unitPassed && json.Unmarshal([]byte(catU.Text), &cat) == nil
	match, err := s.matchMatter(ctx, w, c, l)
	if err != nil {
		return err
	}
	pattern, err := s.setting(ctx, "doctender.naming_pattern")
	if err != nil {
		return err
	}
	cats, err := s.categories(ctx, false)
	if err != nil {
		return err
	}
	var category *Category
	for k := range cats {
		if cats[k].Key == cat.Answer.Category {
			category = &cats[k]
		}
	}
	zone, _ := s.setting(ctx, "firm.time_zone")
	received := w.file.CreatedAt
	if t, err := domain.ParseUTC(w.file.CreatedAt); err == nil {
		if local, err := domain.InZone(t, zone); err == nil {
			received = local.Format(time.DateOnly)
		} else {
			received = t.Format(time.DateOnly)
		}
	}
	ext := extOf(w.file.Name)
	proposed := ""
	if labelOK {
		proposed, err = renderName(pattern, nameParts{Matter: w.matter, WhatItIs: l.WhatItIs, DocDate: l.DocDate, Received: received, Category: cat.Answer.Category, Ext: ext})
		if err != nil {
			return err
		}
	}
	type pending struct {
		kind, detail string
		best         map[string]any
	}
	var reviews []pending
	best := map[string]any{"what_it_is": l.WhatItIs, "doc_date": l.DocDate}
	if match.Best != "" {
		best["matter_id"], best["matter"], best["match_reason"] = string(match.Best), match.BestLabel, match.Why
	}
	// Doc Tender sets the name, category and folder once (PRD 8.21); after that only a
	// person changes them. A later version or a reprocess still gets its own result and
	// Markdown copy, but leaves a decision an earlier result already published alone.
	named, earlierCat, earlierHow, err := s.decidedBefore(ctx, w.file.ID)
	if err != nil {
		return err
	}
	categorized := earlierCat != ""
	applyName := labelOK && j.MatterID != "" && !match.Review && !named
	applyCat := catOK && cat.Agree && category != nil && !categorized
	if labelOK && !named {
		var low []string
		if l.WhatItIsConfidence < threshold {
			low = append(low, fmt.Sprintf("what it is %.3f", l.WhatItIsConfidence))
		}
		if l.DocDate != "" && l.DocDateConfidence < threshold {
			low = append(low, fmt.Sprintf("document date %.3f", l.DocDateConfidence))
		}
		if len(low) > zero {
			applyName = false
			// Accept on a name item applies the name only. The name is built from the
			// matter's tokens, so it is offered only once the matter is settled, and
			// never once Doc Tender has named the file (clash suffix included).
			nameBest := withField(best, "", nil)
			if j.MatterID != "" && !match.Review {
				nameBest = withField(best, "name", proposed)
			}
			reviews = append(reviews, pending{"low_confidence", "low confidence on the name: " + strings.Join(low, ", "), nameBest})
		}
	}
	// Accept on a category item applies the category only, and no category item opens
	// once a category is settled, so a later pass never offers a different one.
	catBest := withField(best, "category", cat.Answer.Category)
	if catOK && cat.Agree && cat.Answer.Confidence < threshold && !categorized {
		applyCat = false
		detail := fmt.Sprintf("low confidence on the category %s: %.3f", cat.Answer.Category, cat.Answer.Confidence)
		if k := len(reviews) - one; k >= zero && reviews[k].kind == "low_confidence" {
			// One low-confidence item per version: both held-back decisions share it, and
			// its Accept applies the two it names.
			reviews[k].detail += "; " + detail
			reviews[k].best["category"] = cat.Answer.Category
		} else {
			reviews = append(reviews, pending{"low_confidence", detail, catBest})
		}
	}
	if catOK && !cat.Agree && !categorized {
		reviews = append(reviews, pending{"check_disagreement", catU.CheckResult, catBest})
	}
	if match.Review {
		reviews = append(reviews, pending{"unmatched_matter", match.Reason, best})
	}
	// The header names the source by the name it is published under; the arrival name
	// stays on the record for audit and is not searchable as the file's name.
	source := w.file.Name
	if applyName && !contains(w.file.LockedFields, "name") {
		source = proposed
	}
	md := markdownCopy(source, w.version, c, labelU, catU)
	var rendition files.RenditionView
	err = s.tx(ctx, func(ctx context.Context) error {
		rendition, err = s.d.Files.AddRendition(ctx, s.d.System, w.file.ID, w.version.ID, "markdown", bytes.NewReader([]byte(md)), int64(len(md)))
		if err != nil {
			return err
		}
		upd := files.TenderUpdate{}
		if applyName {
			upd.Name = proposed
		}
		resultCat, how := "", ""
		if applyCat {
			upd.Category, upd.Folder = category.Key, category.Folder
			resultCat, how = category.Key, cat.How
		} else if categorized {
			// The category stays as it was decided; a file that had no matter then is
			// filed now into that category's folder (files keeps a settled folder).
			resultCat, how = earlierCat, earlierHow
			if w.file.CategoryID != nil {
				resultCat = *w.file.CategoryID
			}
			if contains(w.file.LockedFields, "category") {
				how = "person"
			}
			for k := range cats {
				if cats[k].Key == resultCat {
					upd.Folder = cats[k].Folder
				}
			}
		}
		var applied files.TenderResult
		if upd != (files.TenderUpdate{}) {
			applied, err = s.d.Files.ApplyTender(ctx, s.d.System, w.file.ID, upd)
			if err != nil {
				return err
			}
		}
		for _, skipped := range applied.Skipped {
			if skipped == "category" {
				how = "person"
			}
		}
		// The name counts as published when Doc Tender applied it, including when the file
		// already carried it; only a person's lock keeps it from being Doc Tender's.
		nameSet := applyName && !contains(applied.Skipped, "name")
		if err := s.insertResult(ctx, w, l, labelOK, cat, resultCat, nameSet, how, match, rendition.ID); err != nil {
			return err
		}
		for _, r := range reviews {
			if err := s.openException(ctx, j, r.kind, r.detail, r.best); err != nil {
				return err
			}
		}
		open, err := s.openExceptionCount(ctx, j.FileID, j.VersionID)
		if err != nil {
			return err
		}
		j.Status, j.Stage, j.FinishedAt, j.LastError = StatusPublished, stageDone, s.now(), ""
		if open > zero {
			j.Status = StatusException
		}
		j.LeaseOwner, j.LeaseUntil = "", time.Time{}
		if err := s.saveJob(ctx, *j); err != nil {
			return err
		}
		summary := "Doc Tender published " + w.file.Name
		if applied.File.Name != "" {
			summary = "Doc Tender published " + applied.File.Name
		}
		return s.appendEvent(ctx, "doc_tender.published", "file", w.file.ID, j.MatterID, summary)
	})
	if err != nil {
		return err
	}
	return s.autoResolve(ctx, j.FileID, "provider_down", "the file was published")
}

// decidedBefore says whether an earlier result for the file already published its
// name, and the category (with how it was decided) the first result published, if any.
func (s *Service) decidedBefore(ctx context.Context, fileID domain.ID) (named bool, category, how string, err error) {
	var names int
	if err = s.row(ctx, `SELECT COALESCE(SUM(CASE WHEN name_set THEN 1 ELSE 0 END), 0) FROM doc_tender_results WHERE file_id = ?`,
		string(fileID)).Scan(&names); err != nil {
		return false, "", "", err
	}
	err = s.row(ctx, `SELECT category, category_how FROM doc_tender_results WHERE file_id = ? AND category IS NOT NULL
ORDER BY created_at, id LIMIT 1`, string(fileID)).Scan(&category, &how)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return names > zero, category, how, err
}

func (s *Service) insertResult(ctx context.Context, w *work, l Label, labelOK bool, cat categoryOutcome, resultCat string, nameSet bool, how string, m matchOutcome, rendition domain.ID) error {
	id, err := s.id()
	if err != nil {
		return err
	}
	at := us(s.now())
	parts := map[string]any{"what_it_is": l.WhatItIs, "doc_date": nil}
	if l.DocDate != "" {
		parts["doc_date"] = l.DocDate
	}
	conf := map[string]any{"what_it_is": l.WhatItIsConfidence, "doc_date": l.DocDateConfidence, "category": cat.Answer.Confidence,
		"category_proposed": cat.Answer.Category, "label_published": labelOK}
	if cat.Check != nil {
		conf["category_check"] = cat.Check.Category
	}
	var category any
	if resultCat != "" {
		category = resultCat
	}
	fields := l.Fields
	if fields == nil {
		fields = []CitedField{}
	}
	_, err = s.exec(ctx, `INSERT INTO doc_tender_results (id, created_at, updated_at, created_by, file_version_id, name_parts, summary, category, category_how,
critical_fields, matter_id, file_id, job_id, confidences, markdown_rendition_id, suggested_matter_id, name_set) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, at, at, string(s.d.System.UserID), w.version.ID, jsonText(parts), l.Summary, category, how, jsonText(fields), nullID(m.Keep),
		w.file.ID, w.job.ID, jsonText(conf), nullID(rendition), nullID(m.Best), nameSet)
	return err
}

// safePlan opens the file for its units; a file that makes a reader panic is damaged.
func (s *Service) safePlan(ctx context.Context, w *work) (specs []unitSpec, pre *precheck, err error) {
	defer func() {
		if r := recover(); r != nil {
			specs, pre, err = nil, &precheck{"damaged", fmt.Sprintf("the file could not be opened: %v", r)}, nil
		}
	}()
	return s.plan(ctx, w)
}

// withField copies an item's best answer and adds one field to it; an empty key adds none.
func withField(best map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(best))
	for k, v := range best {
		out[k] = v
	}
	if key != "" {
		out[key] = value
	}
	return out
}
