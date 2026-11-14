package calendar

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func encodeOffsets(v []int) string {
	if v == nil {
		v = []int{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// validateDeadline checks a deadline. stored is the responsible user already
// saved on the row (zero for a new deadline); keeping that user is allowed even
// after deactivation so status changes on a departed attorney's deadline still
// work. Assigning a user requires the user to be active.
func (s *Service) validateDeadline(ctx context.Context, p domain.Principal, in *DeadlineInput, stored domain.ID) error {
	in.Title = strings.TrimSpace(in.Title)
	if in.MatterID.IsZero() {
		return domain.Invalid("matter_id", "matter is required")
	}
	if in.Title == "" {
		return domain.Invalid("title", "title is required")
	}
	if _, err := parseDate(in.DueOn); err != nil {
		return domain.Invalid("due_on", "due date must be YYYY-MM-DD")
	}
	if in.Status == "" {
		in.Status = StatusOpen
	}
	if !validStatus(in.Status) {
		return domain.Invalid("status", "status must be open, done or waived")
	}
	offsets, err := normalizeOffsets(in.Offsets)
	if err != nil {
		return err
	}
	in.Offsets = offsets
	if in.ResponsibleUserID.IsZero() || in.ResponsibleUserID != stored {
		if _, err := s.activeUser(ctx, "responsible_user_id", in.ResponsibleUserID); err != nil {
			return err
		}
	}
	return s.checkMatter(ctx, p, in.MatterID)
}

// CreateDeadline adds a deadline. An empty offset list takes the firm default.
func (s *Service) CreateDeadline(ctx context.Context, p domain.Principal, in DeadlineInput) (Deadline, error) {
	if err := s.gate(ctx, p, domain.ActionWrite, ""); err != nil {
		return Deadline{}, err
	}
	if len(in.Offsets) == 0 {
		def, err := s.DefaultDeadlineOffsets(ctx)
		if err != nil {
			return Deadline{}, err
		}
		in.Offsets = def
	}
	if err := s.validateDeadline(ctx, p, &in, ""); err != nil {
		return Deadline{}, err
	}
	m, err := s.meta(p)
	if err != nil {
		return Deadline{}, err
	}
	row := Deadline{Deadline: domain.Deadline{
		Meta: m, MatterID: in.MatterID, Title: in.Title, DueOn: in.DueOn, ResponsibleUserID: in.ResponsibleUserID,
		Status: in.Status, ReminderOffsets: encodeOffsets(in.Offsets),
	}, Offsets: in.Offsets}
	s.applyDone(&row, StatusOpen, p)
	err = s.commit(ctx, p,
		func(ctx context.Context) error { return s.d.Store.PutDeadline(ctx, row) },
		func(ctx context.Context) error { return s.d.Store.DeleteDeadline(ctx, row.ID) },
		"deadline", row.ID, row.MatterID, "deadline.created", row.Title, deadlineChanges(Deadline{}, row))
	if err != nil {
		return Deadline{}, err
	}
	return row, nil
}

func (s *Service) applyDone(row *Deadline, prevStatus string, p domain.Principal) {
	switch {
	case row.Status == StatusOpen:
		row.DoneBy, row.DoneAt = "", unsetMicro
	case row.Status != prevStatus || !isSet(row.DoneAt):
		row.DoneBy = p.UserID
		if row.DoneBy.IsZero() {
			row.DoneBy = domain.SystemUserID
		}
		row.DoneAt = domain.UnixMicroUTC(s.now())
	}
}

func deadlineChanges(a, b Deadline) []change {
	offA := ""
	if !a.ID.IsZero() {
		offA = joinInts(a.Offsets)
	}
	return []change{
		{"matter_id", string(a.MatterID), string(b.MatterID)},
		{"title", a.Title, b.Title},
		{"due_on", a.DueOn, b.DueOn},
		{"responsible_user_id", string(a.ResponsibleUserID), string(b.ResponsibleUserID)},
		{"status", a.Status, b.Status},
		{"reminder_offsets", offA, joinInts(b.Offsets)},
		{"done_by", string(a.DoneBy), string(b.DoneBy)},
		{"done_at", fmtTime(a.DoneAt), fmtTime(b.DoneAt)},
	}
}

// GetDeadline returns one deadline the caller can see.
func (s *Service) GetDeadline(ctx context.Context, p domain.Principal, id domain.ID) (Deadline, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return Deadline{}, err
	}
	row, err := s.d.Store.Deadline(ctx, id)
	if err != nil {
		return Deadline{}, missing(err, "deadline")
	}
	if !s.visible(ctx, p, row.MatterID) {
		return Deadline{}, domain.NotFound("deadline")
	}
	return row, nil
}

// UpdateDeadline applies a patch. Marking done or waived records who and when;
// reopening clears both.
func (s *Service) UpdateDeadline(ctx context.Context, p domain.Principal, id domain.ID, patch DeadlinePatch) (Deadline, error) {
	prev, err := s.GetDeadline(ctx, p, id)
	if err != nil {
		return Deadline{}, err
	}
	if err := s.gate(ctx, p, domain.ActionWrite, prev.MatterID); err != nil {
		return Deadline{}, err
	}
	in := DeadlineInput{MatterID: prev.MatterID, Title: prev.Title, DueOn: prev.DueOn, ResponsibleUserID: prev.ResponsibleUserID, Status: prev.Status, Offsets: prev.Offsets}
	if patch.MatterID != nil {
		in.MatterID = *patch.MatterID
	}
	if patch.Title != nil {
		in.Title = *patch.Title
	}
	if patch.DueOn != nil {
		in.DueOn = *patch.DueOn
	}
	if patch.ResponsibleUserID != nil {
		in.ResponsibleUserID = *patch.ResponsibleUserID
	}
	if patch.Status != nil {
		in.Status = *patch.Status
		if in.Status == "" {
			return Deadline{}, domain.Invalid("status", "status must be open, done or waived")
		}
	}
	if patch.Offsets != nil {
		in.Offsets = *patch.Offsets
	}
	if err := s.validateDeadline(ctx, p, &in, prev.ResponsibleUserID); err != nil {
		return Deadline{}, err
	}
	next := prev
	next.UpdatedAt = s.now()
	next.MatterID, next.Title, next.DueOn, next.ResponsibleUserID, next.Status = in.MatterID, in.Title, in.DueOn, in.ResponsibleUserID, in.Status
	next.Offsets, next.ReminderOffsets = in.Offsets, encodeOffsets(in.Offsets)
	s.applyDone(&next, prev.Status, p)
	var before Deadline
	err = s.commit(ctx, p,
		func(ctx context.Context) error {
			var err error
			if before, err = s.d.Store.Deadline(ctx, next.ID); err != nil {
				return missing(err, "deadline")
			}
			if err := stale(prev, before, "the deadline"); err != nil {
				return err
			}
			return s.d.Store.PutDeadline(ctx, next)
		},
		func(ctx context.Context) error { return s.d.Store.PutDeadline(ctx, before) },
		"deadline", next.ID, next.MatterID, "deadline.updated", next.Title, deadlineChanges(prev, next))
	if err != nil {
		return Deadline{}, err
	}
	return next, nil
}

// ListDeadlines returns visible deadlines by due date.
func (s *Service) ListDeadlines(ctx context.Context, p domain.Principal, f DeadlineFilter) ([]Deadline, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	for field, v := range map[string]string{"from_date": f.From, "to_date": f.To} {
		if v == "" {
			continue
		}
		if _, err := parseDate(v); err != nil {
			return nil, domain.Invalid(field, "date must be YYYY-MM-DD")
		}
	}
	if f.Status != "" && !validStatus(f.Status) {
		return nil, domain.Invalid("status", "status must be open, done or waived")
	}
	if !f.MatterID.IsZero() && !s.visible(ctx, p, f.MatterID) {
		return nil, domain.NotFound("matter")
	}
	today := s.Today(ctx).Format(dateLayout)
	// With Max set, a first read of a few times Max rows usually fills it; only when walls
	// hide too many of them is the whole list read.
	first := f
	first.limit = f.Max << 2
	for _, q := range []DeadlineFilter{first, f} {
		rows, err := s.d.Store.DeadlinesFor(ctx, q, today)
		if err != nil {
			return nil, err
		}
		var out []Deadline
		for _, row := range rows {
			if !matchDeadline(row, f, today) {
				continue
			}
			if !s.visible(ctx, p, row.MatterID) {
				continue
			}
			out = append(out, row)
			if len(out) == f.Max {
				return out, nil
			}
		}
		if !isSet(int64(q.limit)) || len(rows) < q.limit {
			return out, nil
		}
	}
	return nil, nil
}

func matchDeadline(row Deadline, f DeadlineFilter, today string) bool {
	if f.Status != "" && row.Status != f.Status {
		return false
	}
	if !f.MatterID.IsZero() && row.MatterID != f.MatterID {
		return false
	}
	if !f.ResponsibleUserID.IsZero() && row.ResponsibleUserID != f.ResponsibleUserID {
		return false
	}
	if f.To != "" && row.DueOn > f.To {
		return false
	}
	if f.Before != "" && row.DueOn >= f.Before {
		return false
	}
	if f.From != "" && row.DueOn < f.From {
		return f.IncludeOverdue && row.Status == StatusOpen && row.DueOn < today
	}
	return true
}

// Overdue lists open deadlines due before today in the firm zone that the caller
// can see. They stay until done or waived.
func (s *Service) Overdue(ctx context.Context, p domain.Principal) ([]Deadline, error) {
	return s.ListDeadlines(ctx, p, DeadlineFilter{Status: StatusOpen, Before: s.Today(ctx).Format(dateLayout)})
}

// OverduePage is Overdue's first max deadlines (zero is every one) and how many there
// are; each matter is checked once, through the gate, for the count.
func (s *Service) OverduePage(ctx context.Context, p domain.Principal, max int) ([]Deadline, int, error) {
	f := DeadlineFilter{Status: StatusOpen, Before: s.Today(ctx).Format(dateLayout), Max: max}
	rows, err := s.ListDeadlines(ctx, p, f)
	if err != nil || !isSet(int64(max)) || len(rows) < max {
		return rows, len(rows), err
	}
	counts, err := s.d.Store.DeadlineCounts(ctx, f, f.Before)
	if err != nil {
		return nil, 0, err
	}
	var total int
	for m, c := range counts {
		if s.visible(ctx, p, m) {
			total += c
		}
	}
	return rows, total, nil
}

// IsOverdue reports whether row is open and past due on the firm-local today.
func (s *Service) IsOverdue(ctx context.Context, row Deadline) bool {
	return row.Status == StatusOpen && row.DueOn < s.Today(ctx).Format(dateLayout)
}
