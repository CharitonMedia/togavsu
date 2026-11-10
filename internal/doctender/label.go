package doctender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Citation is where a field was read: a page, a timestamp, or both.
type Citation struct {
	Page      *int    `json:"page"`
	Timestamp *string `json:"timestamp"`
}

// CitedField is one critical field (a name, date, amount or case number) with its
// confidence and citation.
type CitedField struct {
	Name       string   `json:"name"`
	Value      string   `json:"value"`
	Confidence float64  `json:"confidence"`
	Citation   Citation `json:"citation"`
}

// Label is the labeling model's answer.
type Label struct {
	WhatItIs           string       `json:"what_it_is"`
	WhatItIsConfidence float64      `json:"what_it_is_confidence"`
	DocDate            string       `json:"doc_date"`
	DocDateConfidence  float64      `json:"doc_date_confidence"`
	Summary            string       `json:"summary"`
	Fields             []CitedField `json:"fields"`
}

// CategoryAnswer is one categorizing call's answer.
type CategoryAnswer struct {
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
}

// categoryOutcome is the categorize step's decision.
type categoryOutcome struct {
	Answer CategoryAnswer
	Check  *CategoryAnswer
	// How is model or model_agreement; empty when the check model disagreed.
	How   string
	Agree bool
}

// content is the checked Markdown body and what citations are checked against.
type content struct {
	Body  string
	Pages map[int]string
	// Times are transcript and on-screen lines by second.
	Times []timedLine
	// Unresolved names units that failed after their retries.
	Unresolved []string
	// Tolerance is how far, in seconds, a timestamp citation may be from its line.
	Tolerance float64
	PageCount int
	Models    []string
	Checks    []string
}

type timedLine struct {
	At   float64
	Kind string
	Text string
}

const labelPrompt = `You label documents for a law firm. Read the document and answer with one JSON object:
{"what_it_is": "a short plain name of what the document is, such as motion to withdraw or lease",
 "what_it_is_confidence": 0.0-1.0,
 "doc_date": "the document's own date as YYYY-MM-DD, or empty when it has none",
 "doc_date_confidence": 0.0-1.0,
 "summary": "one or two sentences a lawyer can read on hover",
 "fields": [{"name": "case_number | party | person | date | amount | other", "value": "the exact text as it appears",
             "confidence": 0.0-1.0, "citation": {"page": 1, "timestamp": null}}]}
Cite every field to the page it is on ("## Page N" headings), or to its timestamp "HH:MM:SS" in a recording, exactly as written there.`

// labelUnit runs the labeling model on the checked Markdown. Its check is that every
// critical field is found where it is cited, and that the document date is a date.
func (s *Service) labelUnit(w *work, c *content) unitSpec {
	return unitSpec{Name: "label", Tasks: []string{TaskLabeling}, Exception: "check_disagreement", Run: func(ctx context.Context, checkMethod bool) (unitWork, error) {
		resp, err := s.d.Models.Call(ctx, Request{Task: TaskLabeling, Check: checkMethod, System: labelPrompt, JSON: true,
			Parts: []Part{{Text: c.Body}}, FileID: w.file.ID, MatterID: w.job.MatterID})
		model := resp.Provider + "/" + resp.Model
		if err != nil {
			return unitWork{Method: "labeling", Model: model}, err
		}
		var l Label
		if err := json.Unmarshal([]byte(jsonObject(resp.Text)), &l); err != nil {
			return unitWork{Method: "labeling", Model: model, CheckResult: "unreadable answer: " + err.Error()}, nil
		}
		l.WhatItIs = strings.TrimSpace(l.WhatItIs)
		problems := checkLabel(l, c)
		return unitWork{Text: jsonText(l), Method: "labeling", Model: model, CheckMethod: "citations",
			CheckResult: strings.Join(append([]string{fmt.Sprintf("%d fields cited", len(l.Fields))}, problems...), "; "),
			Passed:      len(problems) == 0 && l.WhatItIs != ""}, nil
	}}
}

var reDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// checkLabel lists every citation that does not hold.
func checkLabel(l Label, c *content) []string {
	var out []string
	if l.DocDate != "" {
		if _, err := time.Parse(time.DateOnly, l.DocDate); err != nil || !reDate.MatchString(l.DocDate) {
			out = append(out, "doc_date is not a date: "+l.DocDate)
		}
	}
	for _, f := range l.Fields {
		if strings.TrimSpace(f.Value) == "" {
			continue
		}
		found := false
		if f.Citation.Page != nil {
			found = containsWords(c.Pages[*f.Citation.Page], f.Value)
		}
		if !found && f.Citation.Timestamp != nil {
			found = containsWords(c.near(*f.Citation.Timestamp), f.Value)
		}
		if !found {
			out = append(out, fmt.Sprintf("%s %q is not where it is cited", f.Name, f.Value))
		}
	}
	return out
}

// near is the transcript and on-screen text within a minute of a timestamp.
func (c *content) near(ts string) string {
	at, ok := parseClock(ts)
	if !ok {
		return ""
	}
	var parts []string
	for _, t := range c.Times {
		if t.At >= at-c.Tolerance && t.At <= at+c.Tolerance {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, " ")
}

func parseClock(ts string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(ts), ":")
	total := float64(zero)
	for _, p := range parts {
		n, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return float64(zero), false
		}
		total = total*minuteUnit.Seconds() + n
	}
	return total, len(parts) > zero
}

// containsWords reports whether value's words appear in order, together, in text.
func containsWords(text, value string) bool {
	hay, needle := words(text), words(value)
	if len(needle) == 0 {
		return false
	}
	for i := zero; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Category is one doc_categories row.
type Category struct {
	ID          domain.ID `json:"id"`
	CreatedAt   string    `json:"created_at"`
	CreatedBy   domain.ID `json:"created_by"`
	UpdatedAt   string    `json:"updated_at"`
	Key         string    `json:"key"`
	Description string    `json:"description"`
	Tier        string    `json:"tier"`
	Folder      string    `json:"folder_name"`
	Active      bool      `json:"active"`
}

const categoryCols = `id, created_at, created_by, updated_at, name, description, tier, folder, active`

func scanCategory(sc interface{ Scan(...any) error }) (Category, error) {
	var c Category
	var created, updated int64
	var active int
	err := sc.Scan(&c.ID, &created, &c.CreatedBy, &updated, &c.Key, &c.Description, &c.Tier, &c.Folder, &active)
	c.CreatedAt, c.UpdatedAt, c.Active = domain.FormatUTC(fromUS(created)), domain.FormatUTC(fromUS(updated)), active == one
	return c, err
}

func (s *Service) categories(ctx context.Context, activeOnly bool) ([]Category, error) {
	q := `SELECT ` + categoryCols + ` FROM doc_categories`
	if activeOnly {
		q += ` WHERE active = 1`
	}
	rows, err := s.query(ctx, q+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func categoryPrompt(cats []Category) string {
	var b strings.Builder
	b.WriteString("You file documents for a law firm. Choose exactly one category for the document from this list:\n")
	for _, c := range cats {
		fmt.Fprintf(&b, "- %s: %s\n", c.Key, c.Description)
	}
	b.WriteString(`Answer with one JSON object: {"category": "one key from the list", "confidence": 0.0-1.0}`)
	return b.String()
}

// categorizeUnit picks one category. A high-tier category is also chosen by the check
// model and set only when the two agree (PRD 8.21 step 4); a disagreement is decided
// once, as a review-queue item, and no category is published.
func (s *Service) categorizeUnit(w *work, c *content) unitSpec {
	return unitSpec{Name: "categorize", Tasks: []string{TaskCategorizing}, Exception: "check_disagreement", Run: func(ctx context.Context, checkMethod bool) (unitWork, error) {
		cats, err := s.categories(ctx, true)
		if err != nil {
			return unitWork{}, err
		}
		byKey := map[string]Category{}
		for _, cat := range cats {
			byKey[cat.Key] = cat
		}
		ask := func(check bool) (CategoryAnswer, string, error) {
			resp, err := s.d.Models.Call(ctx, Request{Task: TaskCategorizing, Check: check, System: categoryPrompt(cats), JSON: true,
				Parts: []Part{{Text: c.Body}}, FileID: w.file.ID, MatterID: w.job.MatterID})
			model := resp.Provider + "/" + resp.Model
			if err != nil {
				return CategoryAnswer{}, model, err
			}
			var a CategoryAnswer
			if err := json.Unmarshal([]byte(jsonObject(resp.Text)), &a); err != nil {
				return CategoryAnswer{}, model, fmt.Errorf("unreadable category answer: %w", err)
			}
			a.Category = strings.TrimSpace(a.Category)
			if _, ok := byKey[a.Category]; !ok {
				return a, model, fmt.Errorf("the model chose %q, which is not an active category", a.Category)
			}
			return a, model, nil
		}
		first, model, err := ask(checkMethod)
		if err != nil {
			return unitWork{Method: "categorizing", Model: model}, err
		}
		out := categoryOutcome{Answer: first, How: "model", Agree: true}
		work := unitWork{Method: "categorizing", Model: model, Passed: true, CheckMethod: "standard_tier", CheckResult: "standard tier: one model"}
		if byKey[first.Category].Tier == "high" {
			second, checkModel, err := ask(!checkMethod)
			if err != nil {
				return unitWork{Method: "categorizing", Model: model}, err
			}
			out.Check, out.Agree = &second, second.Category == first.Category
			work.CheckMethod, work.CheckModel = "check_model_agreement", checkModel
			if out.Agree {
				out.How = "model_agreement"
				work.CheckResult = "both chose " + first.Category
			} else {
				out.How = ""
				work.CheckResult = fmt.Sprintf("labeling model chose %s, check model chose %s", first.Category, second.Category)
			}
		}
		work.Text = jsonText(out)
		return work, nil
	}}
}

// matchOutcome is step 5.
type matchOutcome struct {
	// Keep is the matter the file stays on (its arrival matter).
	Keep domain.ID
	// Best is the matter the content points to, when it is not Keep or Keep is empty.
	Best      domain.ID
	BestLabel string
	// Reason is the review item's detail. It names no other matter: the item, the
	// email and the timeline are read by people who may be walled from Best.
	Reason string
	// Why is what matched Best (a case number, a party). It names Best's details, so it
	// is kept in best_answer.match_reason and redacted with matter_id for readers
	// walled from Best.
	Why string
	// Review is set when a person must confirm the matter.
	Review bool
}

// matchMatter compares the content's case numbers and parties with the firm's matters.
// A file that arrived on a matter keeps it; Doc Tender never moves a file between
// matters on its own.
func (s *Service) matchMatter(ctx context.Context, w *work, c *content, l Label) (matchOutcome, error) {
	out := matchOutcome{Keep: w.job.MatterID}
	all, err := s.d.Matters.All(ctx)
	if err != nil {
		return out, err
	}
	text := strings.ToLower(c.Body)
	var names []string
	for _, f := range l.Fields {
		names = append(names, f.Value)
	}
	type scored struct {
		m     MatterInfo
		score int
		why   string
	}
	var hits []scored
	for _, m := range all {
		sc, why := zero, ""
		if m.CaseNumber != "" && strings.Contains(text, strings.ToLower(m.CaseNumber)) {
			sc, why = sc+len(all)+one, "case number "+m.CaseNumber
		}
		for _, p := range append(append([]string{}, m.Parties...), strings.TrimSpace(m.ClientFirst+" "+m.ClientLast), m.ClientOrg) {
			if strings.TrimSpace(p) != "" && (containsWords(c.Body, p) || containsAny(names, p)) {
				sc++
				if why == "" {
					why = "party " + p
				}
			}
		}
		if sc > zero {
			hits = append(hits, scored{m, sc, why})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	if out.Keep == "" {
		out.Review = true
		out.Reason = "the file arrived with no matter"
		if len(hits) > zero {
			out.Best, out.BestLabel, out.Why = hits[zero].m.ID, hits[zero].m.Number+" "+hits[zero].m.Name, hits[zero].why
		}
		return out, nil
	}
	if len(hits) > zero && hits[zero].m.ID != out.Keep {
		keepScore := zero
		for _, h := range hits {
			if h.m.ID == out.Keep {
				keepScore = h.score
			}
		}
		if hits[zero].score > keepScore {
			out.Review = true
			out.Best, out.BestLabel, out.Why = hits[zero].m.ID, hits[zero].m.Number+" "+hits[zero].m.Name, hits[zero].why
			out.Reason = "the content may belong to another matter"
		}
	}
	return out, nil
}

func containsAny(values []string, name string) bool {
	for _, v := range values {
		if containsWords(v, name) {
			return true
		}
	}
	return false
}

// ---- naming ----

var reSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(reSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// nameParts is everything a naming pattern can use.
type nameParts struct {
	Matter   MatterInfo
	WhatItIs string
	DocDate  string
	Received string
	Category string
	Ext      string
}

// renderName fills the naming pattern, lowercase with hyphens. A token with no value
// (a document with no date) is left off with its separator.
func renderName(pattern string, p nameParts) (string, error) {
	if err := domain.ValidateNamingPattern(pattern); err != nil {
		return "", err
	}
	last := p.Matter.ClientLast
	if last == "" && p.Matter.ClientOrg != "" {
		org := slug(p.Matter.ClientOrg)
		if limit := wire().ClientLastChars; len(org) > limit {
			org = strings.Trim(org[:limit], "-")
		}
		last = org
	}
	values := map[string]string{
		domain.TokenClientLast: slug(last), domain.TokenClientFirst: slug(p.Matter.ClientFirst),
		domain.TokenMatterNumber: slug(p.Matter.Number), domain.TokenMatterName: slug(p.Matter.Name),
		domain.TokenWhat: slug(p.WhatItIs), domain.TokenDocDate: slug(p.DocDate), domain.TokenReceivedDate: slug(p.Received),
		domain.TokenCategory: slug(p.Category), domain.TokenExt: strings.ToLower(strings.TrimPrefix(p.Ext, ".")),
	}
	ext := values[domain.TokenExt]
	stem := strings.TrimSuffix(pattern, "."+domain.TokenExt)
	stem = tokenRE.ReplaceAllStringFunc(stem, func(tok string) string { return values[tok] })
	stem = strings.Trim(regexp.MustCompile(`[-_.\s]{2,}`).ReplaceAllStringFunc(stem, func(run string) string {
		return run[:one]
	}), "-_. ")
	if stem == "" {
		return "", errors.New("the naming pattern produced an empty name")
	}
	if ext == "" {
		return stem, nil
	}
	return stem + "." + ext, nil
}

var tokenRE = regexp.MustCompile(`<[^>]+>`)

func extOf(name string) string { return strings.TrimPrefix(path.Ext(name), ".") }
