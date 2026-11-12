package practice

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

var scriptBlock = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)

// CreateNote stores a matter or contact note. Agent and module bodies are sanitized.
func (s *Service) CreateNote(ctx context.Context, p domain.Principal, in NoteInput) (_ domain.Note, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.gateNote(ctx, p, in.MatterID, in.ContactID, domain.ActionWrite); err != nil {
		return domain.Note{}, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return domain.Note{}, domain.Invalid("title", "title is required")
	}
	body := sanitizeNote(in.Body, p.Source)
	status := in.Status
	if status == "" {
		status = NoteActive
	}
	if status != NoteActive && status != NoteArchived {
		return domain.Note{}, domain.Invalid("status", "unknown note status")
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.Note{}, err
	}
	row := domain.Note{
		Meta: meta, MatterID: in.MatterID, ContactID: in.ContactID, AuthorID: actorID(p),
		Title: title, Body: body, Pinned: in.Pinned, Status: status,
	}
	if err := s.db.InsertNote(ctx, row); err != nil {
		return domain.Note{}, err
	}
	if err := s.replaceNoteTags(ctx, p, row.ID, in.Tags); err != nil {
		return domain.Note{}, err
	}
	if err := s.record(ctx, p, []change{{
		MatterID: in.MatterID, ContactID: in.ContactID, Entity: "note", EntityID: row.ID, Field: "body", New: title, Kind: "create",
		Action: "note.created", Summary: "Added note " + title,
	}}); err != nil {
		return domain.Note{}, err
	}
	return row, nil
}

// UpdateNote edits a note the caller can see.
func (s *Service) UpdateNote(ctx context.Context, p domain.Principal, id domain.ID, in NoteInput) (_ domain.Note, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	prev, err := s.db.Note(ctx, id)
	if err != nil {
		return domain.Note{}, missing(err, "note")
	}
	if err := s.gateNote(ctx, p, prev.MatterID, prev.ContactID, domain.ActionWrite); err != nil {
		return domain.Note{}, err
	}
	next := prev
	if strings.TrimSpace(in.Title) != "" {
		next.Title = strings.TrimSpace(in.Title)
	}
	next.Body = sanitizeNote(in.Body, p.Source)
	next.Pinned = in.Pinned
	if in.Status != "" {
		if in.Status != NoteActive && in.Status != NoteArchived {
			return domain.Note{}, domain.Invalid("status", "unknown note status")
		}
		next.Status = in.Status
	}
	next.UpdatedAt = s.clock()
	if err := s.db.UpdateNote(ctx, next); err != nil {
		return domain.Note{}, err
	}
	oldTags := joinTags(s.db.NoteTags(prev.ID))
	if in.Tags != nil {
		if err := s.replaceNoteTags(ctx, p, next.ID, in.Tags); err != nil {
			return domain.Note{}, err
		}
	}
	if err := s.record(ctx, p, noteChanges(prev, next, oldTags, joinTags(s.db.NoteTags(next.ID)), "note.updated", "Updated note "+next.Title)); err != nil {
		return domain.Note{}, err
	}
	return next, nil
}

// GetNote returns a note. The body is sanitized again before it leaves the service.
func (s *Service) GetNote(ctx context.Context, p domain.Principal, id domain.ID) (domain.Note, error) {
	row, err := s.db.Note(ctx, id)
	if err != nil {
		return domain.Note{}, missing(err, "note")
	}
	if err := s.gateNote(ctx, p, row.MatterID, row.ContactID, domain.ActionRead); err != nil {
		return domain.Note{}, err
	}
	return row, nil
}

// ListNotes returns notes the caller can see. Pinned notes come first.
func (s *Service) ListNotes(ctx context.Context, p domain.Principal, filter NoteFilter) ([]domain.Note, string, error) {
	ctx = withSettingsMemo(ctx)
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResNote, Action: domain.ActionRead}); err != nil {
		return nil, "", err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = s.intSetting(ctx, "notes.page_size", domain.NotesPageSize)
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	var out []domain.Note
	for _, row := range s.db.Notes() {
		if err := s.gateNote(ctx, p, row.MatterID, row.ContactID, domain.ActionRead); err != nil {
			continue
		}
		if !filter.MatterID.IsZero() && row.MatterID != filter.MatterID {
			continue
		}
		if !filter.AuthorID.IsZero() && row.AuthorID != filter.AuthorID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(row.Title), query) && !strings.Contains(strings.ToLower(row.Body), query) {
			continue
		}
		if filter.From != "" && row.CreatedAt.Format("2006-01-02") < filter.From {
			continue
		}
		if filter.To != "" && row.CreatedAt.Format("2006-01-02") > filter.To {
			continue
		}
		if filter.Tag != "" && !s.noteHasTag(row.ID, filter.Tag) {
			continue
		}
		out = append(out, row)
	}
	sortNotes(out)
	if filter.Cursor != "" {
		cut := 0
		for cut < len(out) && string(out[cut].ID) != filter.Cursor {
			cut++
		}
		if cut < len(out) {
			out = out[cut+1:]
		}
	}
	next := ""
	if len(out) > limit {
		next = string(out[limit-1].ID)
		out = out[:limit]
	}
	return out, next, nil
}

func (s *Service) gateNote(ctx context.Context, p domain.Principal, matter, contact domain.ID, action domain.Action) error {
	if matter.IsZero() && contact.IsZero() {
		return domain.Invalid("note", "a note needs a matter or a contact")
	}
	if !matter.IsZero() {
		if err := s.gateMatterShared(ctx, p, matter, action, false); err != nil {
			return err
		}
	}
	ref := domain.ResourceRef{Resource: domain.ResNote, Action: action, MatterID: matter, SharedWithClient: false}
	if _, err := s.gate(ctx, p, ref); err != nil {
		return err
	}
	if !contact.IsZero() {
		if err := s.readableContact(ctx, p, contact); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) replaceNoteTags(ctx context.Context, p domain.Principal, note domain.ID, tags []string) error {
	var rows []domain.NoteTag
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		meta, err := s.meta(p)
		if err != nil {
			return err
		}
		rows = append(rows, domain.NoteTag{Meta: meta, NoteID: note, Tag: tag})
	}
	return s.db.ReplaceNoteTags(ctx, note, rows)
}

func noteChanges(prev, next domain.Note, oldTags, newTags, action, summary string) []change {
	type pair struct{ field, old, neu string }
	rows := []pair{
		{"title", prev.Title, next.Title},
		{"body", prev.Body, next.Body},
		{"pinned", boolText(prev.Pinned), boolText(next.Pinned)},
		{"status", prev.Status, next.Status},
		{"tags", oldTags, newTags},
	}
	var out []change
	first := true
	for _, row := range rows {
		if row.old == row.neu {
			continue
		}
		ch := change{
			MatterID: next.MatterID, ContactID: next.ContactID, Entity: "note", EntityID: next.ID,
			Field: row.field, Old: row.old, New: row.neu, Kind: kindOf(row.old, row.neu),
		}
		if first {
			ch.Action = action
			ch.Summary = summary
			first = false
		} else {
			ch.SkipEvent = true
		}
		out = append(out, ch)
	}
	return out
}

func joinTags(rows []domain.NoteTag) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, row.Tag)
	}
	return strings.Join(parts, ",")
}

func (s *Service) noteHasTag(note domain.ID, tag string) bool {
	for _, row := range s.db.NoteTags(note) {
		if strings.EqualFold(row.Tag, tag) {
			return true
		}
	}
	return false
}

func sanitizeNote(body string, source domain.ActorSource) string {
	if source != domain.SourceModule && source != domain.SourceAPIToken {
		return body
	}
	body = scriptBlock.ReplaceAllString(body, "")
	var b strings.Builder
	in := false
	for _, r := range body {
		switch {
		case r == '<':
			in = true
		case r == '>' && in:
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// sortNotes is a stable sort, like the quadratic insertion sort it replaced.
func sortNotes(rows []domain.Note) {
	sort.SliceStable(rows, func(i, j int) bool { return noteLess(rows[i], rows[j]) })
}

func noteLess(a, b domain.Note) bool {
	if a.Pinned != b.Pinned {
		return a.Pinned
	}
	return a.CreatedAt.After(b.CreatedAt)
}

// NoteTags returns tags for a note the caller can read.
func (s *Service) NoteTags(ctx context.Context, p domain.Principal, note domain.ID) ([]domain.NoteTag, error) {
	if _, err := s.GetNote(ctx, p, note); err != nil {
		return nil, err
	}
	return s.db.NoteTags(note), nil
}
