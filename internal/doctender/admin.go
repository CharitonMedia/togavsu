package doctender

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// SettingsWriter stores a setting as an admin. *settings.Registry implements it; it
// validates the naming pattern (a status token is refused) and audits the change.
type SettingsWriter interface {
	Set(ctx context.Context, key, value string, who domain.Principal) (settings.Change, error)
}

// Naming is the naming pattern setting with the tokens the admin may use.
type Naming struct {
	Pattern       string   `json:"pattern"`
	AllowedTokens []string `json:"allowed_tokens"`
}

// AllowedTokens lists the pattern tokens, without brackets.
func AllowedTokens() []string {
	return []string{"clientlast", "clientfirst", "matter#", "mattername", "what-it-is", "doc-date", "received-date", "category", "ext"}
}

func staffRead(p domain.Principal) error {
	if !staffLike(p) {
		return domain.Forbidden("Doc Tender settings are for firm staff")
	}
	return nil
}

func adminOnly(p domain.Principal) error {
	if !p.HasRole(domain.RoleAdmin) {
		return domain.Forbidden("only an admin changes Doc Tender settings")
	}
	return nil
}

// GetNaming reads the pattern.
func (s *Service) GetNaming(ctx context.Context, p domain.Principal) (Naming, error) {
	if err := staffRead(p); err != nil {
		return Naming{}, err
	}
	v, err := s.setting(ctx, "doctender.naming_pattern")
	return Naming{Pattern: v, AllowedTokens: AllowedTokens()}, err
}

// PutNaming saves a pattern on the Doc Tender tab. A status token or any unknown
// token is refused and the saved pattern stays; the next file is named by the new one.
func (s *Service) PutNaming(ctx context.Context, p domain.Principal, w SettingsWriter, pattern string) (Naming, error) {
	if err := adminOnly(p); err != nil {
		return Naming{}, err
	}
	if err := domain.ValidateNamingPattern(pattern); err != nil {
		return Naming{}, domain.Invalid("pattern", err.Error())
	}
	if _, err := w.Set(ctx, "doctender.naming_pattern", pattern, p); err != nil {
		return Naming{}, domain.Invalid("pattern", err.Error())
	}
	return s.GetNaming(ctx, p)
}

// Categories lists categories for staff.
func (s *Service) Categories(ctx context.Context, p domain.Principal) ([]Category, error) {
	if err := staffRead(p); err != nil {
		return nil, err
	}
	return s.categories(ctx, false)
}

// GetCategory reads one category.
func (s *Service) GetCategory(ctx context.Context, p domain.Principal, id domain.ID) (Category, error) {
	if err := staffRead(p); err != nil {
		return Category{}, err
	}
	c, err := scanCategory(s.row(ctx, `SELECT `+categoryCols+` FROM doc_categories WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, domain.NotFound("doc_category")
	}
	return c, err
}

// CategoryWrite creates or changes a category. Nil fields are left alone on update.
type CategoryWrite struct {
	Key         *string `json:"key"`
	Description *string `json:"description"`
	Tier        *string `json:"tier"`
	Folder      *string `json:"folder_name"`
	Active      *bool   `json:"active"`
}

func validCategory(c Category) error {
	switch {
	case slugKey(c.Key) != c.Key || c.Key == "":
		return domain.Invalid("key", "a category key is lower case letters, digits and underscores")
	case c.Tier != "high" && c.Tier != "standard":
		return domain.Invalid("tier", "tier is high or standard")
	case strings.TrimSpace(c.Folder) == "":
		return domain.Invalid("folder_name", "a category files to a folder")
	}
	return nil
}

func slugKey(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return -1
	}, s)
}

// CreateCategory adds a category on the Doc Tender tab; the next file can use it.
func (s *Service) CreateCategory(ctx context.Context, p domain.Principal, in CategoryWrite) (Category, error) {
	if err := adminOnly(p); err != nil {
		return Category{}, err
	}
	c := Category{Active: true}
	apply(&c, in)
	if c.Description == "" && in.Description == nil {
		return Category{}, domain.Invalid("description", "a category has a description")
	}
	if err := validCategory(c); err != nil {
		return Category{}, err
	}
	id, err := s.id()
	if err != nil {
		return Category{}, err
	}
	at := us(s.now())
	active := zero
	if c.Active {
		active = one
	}
	err = s.tx(ctx, func(ctx context.Context) error {
		var n int
		if err := s.row(ctx, `SELECT COUNT(*) FROM doc_categories WHERE name = ?`, c.Key).Scan(&n); err != nil {
			return err
		}
		if n > zero {
			return domain.Conflict("a category with that key exists")
		}
		if _, err := s.exec(ctx, `INSERT INTO doc_categories (id, created_at, updated_at, created_by, name, description, tier, folder, active) VALUES (?,?,?,?,?,?,?,?,?)`,
			id, at, at, string(p.UserID), c.Key, c.Description, c.Tier, c.Folder, active); err != nil {
			return err
		}
		if err := s.audit(ctx, p, id, "created", "", c.Key+" "+c.Tier+" "+c.Folder); err != nil {
			return err
		}
		return s.appendEvent(ctx, "doc_tender.category_updated", "doc_category", id, "", "Category "+c.Key+" added")
	})
	if err != nil {
		return Category{}, err
	}
	return s.GetCategory(ctx, p, id)
}

func apply(c *Category, in CategoryWrite) {
	if in.Key != nil {
		c.Key = strings.TrimSpace(*in.Key)
	}
	if in.Description != nil {
		c.Description = strings.TrimSpace(*in.Description)
	}
	if in.Tier != nil {
		c.Tier = *in.Tier
	}
	if in.Folder != nil {
		c.Folder = strings.TrimSpace(*in.Folder)
	}
	if in.Active != nil {
		c.Active = *in.Active
	}
}

// UpdateCategory edits or retires a category. A retired category stays on the files
// that have it; a key in use by files can't be renamed.
func (s *Service) UpdateCategory(ctx context.Context, p domain.Principal, id domain.ID, in CategoryWrite) (Category, error) {
	if err := adminOnly(p); err != nil {
		return Category{}, err
	}
	old, err := s.GetCategory(ctx, p, id)
	if err != nil {
		return Category{}, err
	}
	c := old
	apply(&c, in)
	if err := validCategory(c); err != nil {
		return Category{}, err
	}
	active := zero
	if c.Active {
		active = one
	}
	err = s.tx(ctx, func(ctx context.Context) error {
		if c.Key != old.Key {
			var n int
			if err := s.row(ctx, `SELECT COUNT(*) FROM files WHERE category = ?`, old.Key).Scan(&n); err != nil {
				return err
			}
			if n > zero {
				return domain.Conflict("files use this category; retire it and add a new one instead")
			}
		}
		if _, err := s.exec(ctx, `UPDATE doc_categories SET name = ?, description = ?, tier = ?, folder = ?, active = ?, updated_at = ? WHERE id = ?`,
			c.Key, c.Description, c.Tier, c.Folder, active, us(s.now()), id); err != nil {
			return err
		}
		for _, ch := range []struct{ f, o, n string }{{"key", old.Key, c.Key}, {"description", old.Description, c.Description},
			{"tier", old.Tier, c.Tier}, {"folder_name", old.Folder, c.Folder}, {"active", fmt.Sprint(old.Active), fmt.Sprint(c.Active)}} {
			if ch.o != ch.n {
				if err := s.audit(ctx, p, id, ch.f, ch.o, ch.n); err != nil {
					return err
				}
			}
		}
		return s.appendEvent(ctx, "doc_tender.category_updated", "doc_category", id, "", "Category "+c.Key+" changed")
	})
	if err != nil {
		return Category{}, err
	}
	return s.GetCategory(ctx, p, id)
}

func (s *Service) audit(ctx context.Context, p domain.Principal, id domain.ID, field, old, new string) error {
	if s.d.Audit == nil {
		return nil
	}
	_, err := s.d.Audit.Record(ctx, p, audit.Change{Entity: "doc_category", EntityID: id, Field: field, Old: old, New: new})
	return err
}

// SpendRow is one line of the spend report: by day, matter and file type.
type SpendRow struct {
	Day              string `json:"day"`
	MatterID         string `json:"matter_id"`
	FileType         string `json:"file_type"`
	Calls            int    `json:"calls"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	CostCents        int64  `json:"cost_cents"`
}

// SpendQuery filters the spend report.
type SpendQuery struct {
	From, To time.Time
	MatterID domain.ID
}

// Spend reports Doc Tender's AI spend by day (firm time), matter and file type. Calls
// on files the caller can't see are left out.
func (s *Service) Spend(ctx context.Context, p domain.Principal, q SpendQuery) ([]SpendRow, error) {
	if !staffLike(p) && !p.HasRole(domain.RoleAccountant) {
		return nil, domain.Forbidden("reports are for firm staff")
	}
	where, args := `c.task IN ('ocr', 'photo_description', 'photo_grouping', 'transcription', 'labeling', 'categorizing')`, []any{}
	if !q.From.IsZero() {
		where, args = where+` AND c.created_at >= ?`, append(args, us(q.From))
	}
	if !q.To.IsZero() {
		where, args = where+` AND c.created_at < ?`, append(args, us(q.To))
	}
	if q.MatterID != "" {
		where, args = where+` AND c.matter_id = ?`, append(args, string(q.MatterID))
	}
	rows, err := s.query(ctx, `SELECT c.created_at, COALESCE(c.matter_id, ''), COALESCE(c.file_id, ''), COALESCE(f.extension, ''), c.prompt_tokens, c.completion_tokens, c.cost_cents
FROM ai_call_log c LEFT JOIN files f ON f.id = c.file_id WHERE `+where+` ORDER BY c.created_at`, args...)
	if err != nil {
		return nil, err
	}
	type call struct {
		at                 int64
		matter, file, ext  string
		prompt, completion int
		cost               int64
	}
	var calls []call
	for rows.Next() {
		var c call
		if err := rows.Scan(&c.at, &c.matter, &c.file, &c.ext, &c.prompt, &c.completion, &c.cost); err != nil {
			rows.Close()
			return nil, err
		}
		calls = append(calls, c)
	}
	rows.Close()
	zone, _ := s.setting(ctx, "firm.time_zone")
	visible := map[string]bool{}
	agg := map[[three]string]*SpendRow{}
	for _, c := range calls {
		if c.file != "" {
			ok, seen := visible[c.file]
			if !seen {
				_, err := s.d.Files.Get(ctx, p, domain.ID(c.file))
				ok = err == nil
				visible[c.file] = ok
			}
			if !ok {
				continue
			}
		} else if c.matter != "" && !reviewer(p) {
			continue
		}
		t := fromUS(c.at)
		if local, err := domain.InZone(t, zone); err == nil {
			t = local
		}
		ext := strings.ToLower(c.ext)
		if ext == "" {
			ext = "grouping"
		}
		key := [three]string{t.Format(time.DateOnly), c.matter, ext}
		r := agg[key]
		if r == nil {
			r = &SpendRow{Day: key[zero], MatterID: key[one], FileType: key[two]}
			agg[key] = r
		}
		r.Calls++
		r.PromptTokens += c.prompt
		r.CompletionTokens += c.completion
		r.CostCents += c.cost
	}
	out := make([]SpendRow, zero, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Day != out[b].Day {
			return out[a].Day < out[b].Day
		}
		if out[a].MatterID != out[b].MatterID {
			return out[a].MatterID < out[b].MatterID
		}
		return out[a].FileType < out[b].FileType
	})
	return out, nil
}

// dailyWork runs the nightly sweep once per firm day and the daily summary email at
// doctender.daily_summary_time.
func (s *Service) dailyWork(ctx context.Context) error {
	zone, _ := s.setting(ctx, "firm.time_zone")
	now := s.now()
	local, err := domain.InZone(now, zone)
	if err != nil {
		local = now
	}
	today := local.Format(time.DateOnly)
	if done, err := s.ranOn(ctx, "sweep", today); err != nil || !done {
		if err != nil {
			return err
		}
		if _, err := s.Sweep(ctx); err != nil {
			return err
		}
		if err := s.markRan(ctx, "sweep", today); err != nil {
			return err
		}
	}
	on, err := s.boolSetting(ctx, "doctender.daily_summary_enabled")
	if err != nil || !on || s.d.Mail == nil {
		return err
	}
	at, err := s.setting(ctx, "doctender.daily_summary_time")
	if err != nil {
		return err
	}
	if local.Format("15:04") < at {
		return nil
	}
	if done, err := s.ranOn(ctx, "summary", today); err != nil || done {
		return err
	}
	body, err := s.DailySummary(ctx, now)
	if err != nil {
		return err
	}
	if err := s.mailReviewers(ctx, "Doc Tender daily summary "+today, body); err != nil {
		return err
	}
	return s.markRan(ctx, "summary", today)
}

func (s *Service) ranOn(ctx context.Context, kind, day string) (bool, error) {
	var on string
	err := s.row(ctx, `SELECT sent_on FROM doc_tender_mail WHERE kind = ?`, kind).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return on == day, err
}

func (s *Service) markRan(ctx context.Context, kind, day string) error {
	_, err := s.exec(ctx, `INSERT INTO doc_tender_mail (kind, sent_at, sent_on) VALUES (?,?,?)
ON CONFLICT (kind) DO UPDATE SET sent_at = excluded.sent_at, sent_on = excluded.sent_on`, kind, us(s.now()), day)
	return err
}

// DailySummary is the daily email: files processed since the last summary, open exceptions
// with their ages, and spend.
func (s *Service) DailySummary(ctx context.Context, now time.Time) (string, error) {
	// The summary covers the time since the last one.
	var last int64
	if err := s.row(ctx, `SELECT sent_at FROM doc_tender_mail WHERE kind = 'summary'`).Scan(&last); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	since := last
	var processed int
	if err := s.row(ctx, `SELECT COUNT(*) FROM doc_tender_jobs WHERE kind = 'file' AND status IN ('published', 'exception') AND finished_at >= ?`, since).Scan(&processed); err != nil {
		return "", err
	}
	open, err := s.exceptionsWhere(ctx, `status = 'open' ORDER BY opened_at`)
	if err != nil {
		return "", err
	}
	var calls, tokens int
	var cost int64
	if err := s.row(ctx, `SELECT COUNT(*), COALESCE(SUM(prompt_tokens + completion_tokens), 0), COALESCE(SUM(cost_cents), 0) FROM ai_call_log
WHERE created_at >= ? AND task IN ('ocr', 'photo_description', 'photo_grouping', 'transcription', 'labeling', 'categorizing')`, since).Scan(&calls, &tokens, &cost); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Files processed since the last summary: %d\n", processed)
	fmt.Fprintf(&b, "Open review-queue items: %d\n", len(open))
	for _, e := range open {
		age := now.Sub(e.openedAt).Round(minuteUnit)
		fmt.Fprintf(&b, "  - %s, open %s: %s\n", KindLabel(e.Kind), age, e.Detail)
	}
	fmt.Fprintf(&b, "Spend since the last summary: %d model calls, %d tokens, %s\n", calls, tokens, domain.Cents(cost).String())
	return b.String(), nil
}
