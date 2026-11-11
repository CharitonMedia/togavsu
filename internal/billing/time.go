package billing

import (
	"context"
	"errors"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TimeInput creates or patches a time entry. Nil fields stay as they are on a patch.
type TimeInput struct {
	MatterID       *domain.ID
	UserID         *domain.ID
	WorkedOn       *string
	Increments     *int
	Description    *string
	ActivityTypeID *domain.ID
	Billable       *bool
	RateCents      *domain.Cents
	Source         *string
}

// TimeFilter narrows a time-entry list. Empty fields do not filter.
type TimeFilter struct {
	MatterID  domain.ID
	UserID    domain.ID
	FromDate  string
	ToDate    string
	Billable  *bool
	InvoiceID domain.ID
	Unbilled  bool
}

// overBulkMax reports a batch larger than api.bulk_batch_max (registry default 500).
func (s *Service) overBulkMax(ctx context.Context, n int) bool {
	limit, ok := s.settingIntOK(ctx, "api.bulk_batch_max")
	return ok && n > limit
}

func (s *Service) timeEntry(ctx context.Context, id domain.ID) (TimeEntry, error) {
	var row TimeEntry
	if err := s.d.Store.Get(ctx, kindTime, string(id), &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return TimeEntry{}, domain.NotFound("time_entry")
		}
		return TimeEntry{}, err
	}
	return row, nil
}

// TimeEntry returns one entry the caller may read.
func (s *Service) TimeEntry(ctx context.Context, p domain.Principal, id domain.ID) (TimeEntry, error) {
	row, err := s.timeEntry(ctx, id)
	if err != nil {
		return TimeEntry{}, err
	}
	if err := s.readBilling(ctx, p, row.MatterID); err != nil {
		return TimeEntry{}, err
	}
	return row, nil
}

// TimeEntries lists entries on matters the caller can see.
func (s *Service) TimeEntries(ctx context.Context, p domain.Principal, f TimeFilter) ([]TimeEntry, error) {
	if err := s.readBilling(ctx, p, ""); err != nil {
		return nil, err
	}
	rows, err := listOf[TimeEntry](ctx, s.d.Store, kindTime)
	if err != nil {
		return nil, err
	}
	seen := map[domain.ID]bool{}
	var out []TimeEntry
	for _, r := range rows {
		if !f.match(r) {
			continue
		}
		ok, cached := seen[r.MatterID]
		if !cached {
			if ok, err = s.visible(ctx, p, r.MatterID); err != nil {
				return nil, err
			}
			seen[r.MatterID] = ok
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f TimeFilter) match(r TimeEntry) bool {
	switch {
	case !f.MatterID.IsZero() && r.MatterID != f.MatterID,
		!f.UserID.IsZero() && r.UserID != f.UserID,
		f.FromDate != "" && r.WorkedOn < f.FromDate,
		f.ToDate != "" && r.WorkedOn > f.ToDate,
		f.Billable != nil && r.Billable != *f.Billable,
		!f.InvoiceID.IsZero() && r.InvoiceID != f.InvoiceID,
		f.Unbilled && !r.InvoiceID.IsZero():
		return false
	}
	return true
}

// defaultRate is the matter rate, then the user's default rate, then the firm default.
func (s *Service) defaultRate(ctx context.Context, m domain.Matter, user domain.ID) domain.Cents {
	if positive(int64(m.MatterRateCents)) {
		return m.MatterRateCents
	}
	if u, err := s.d.Practice.User(ctx, user); err == nil && positive(int64(u.DefaultHourlyRateCents)) {
		return u.DefaultHourlyRateCents
	}
	return s.settingCents(ctx, "billing.default_hourly_rate_cents")
}

// buildTime validates in against before (zero for a create) and returns the new entry.
func (s *Service) buildTime(ctx context.Context, p domain.Principal, before TimeEntry, in TimeInput, create bool) (TimeEntry, error) {
	row := before
	if create {
		if in.MatterID == nil || in.WorkedOn == nil || in.Increments == nil || in.Description == nil || in.ActivityTypeID == nil {
			return TimeEntry{}, domain.Invalid("time_entry", "matter_id, worked_on, increments, description and activity_type_id are required")
		}
		row.Billable = true
		row.UserID = p.UserID
		row.IncrementMinutes = s.incrementMinutes(ctx)
		row.Source = sourceOf(p, "")
	} else if !before.InvoiceID.IsZero() {
		return TimeEntry{}, domain.Conflict("a billed time entry changes only after its invoice is voided")
	}
	if in.MatterID != nil {
		row.MatterID = *in.MatterID
	}
	if in.UserID != nil {
		row.UserID = *in.UserID
	}
	if in.WorkedOn != nil {
		if _, err := parseDate("worked_on", *in.WorkedOn); err != nil {
			return TimeEntry{}, err
		}
		row.WorkedOn = *in.WorkedOn
	}
	if in.Increments != nil {
		if !positive(int64(*in.Increments)) {
			return TimeEntry{}, domain.Invalid("increments", "must be at least one increment")
		}
		row.Increments = *in.Increments
		if !create {
			row.IncrementMinutes = s.incrementMinutes(ctx)
		}
	}
	if in.Description != nil {
		row.Description = strings.TrimSpace(*in.Description)
	}
	if in.ActivityTypeID != nil {
		if err := s.activeType(ctx, false, *in.ActivityTypeID, "activity_type_id"); err != nil {
			return TimeEntry{}, err
		}
		row.ActivityType = string(*in.ActivityTypeID)
	}
	if in.Billable != nil {
		row.Billable = *in.Billable
	}
	if row.NeedsReview && row.Description != "" && row.ActivityType != "" {
		// Saving a timer draft with a description and an activity type is its review.
		row.NeedsReview = false
	}
	if in.Source != nil && *in.Source != "" {
		row.Source = *in.Source
	}
	if row.UserID.IsZero() {
		return TimeEntry{}, domain.Invalid("user_id", "is required")
	}
	if _, err := s.d.Practice.User(ctx, row.UserID); err != nil {
		return TimeEntry{}, domain.Invalid("user_id", "unknown user")
	}
	if err := s.allow(ctx, p, domain.ResTime, domain.ActionWrite, row.MatterID); err != nil {
		return TimeEntry{}, err
	}
	m, err := s.matter(ctx, row.MatterID)
	if err != nil {
		return TimeEntry{}, err
	}
	if !create && before.MatterID != row.MatterID {
		if err := s.allow(ctx, p, domain.ResTime, domain.ActionWrite, before.MatterID); err != nil {
			return TimeEntry{}, err
		}
	}
	switch {
	case in.RateCents != nil:
		if negative(int64(*in.RateCents)) {
			return TimeEntry{}, domain.Invalid("rate_cents", "must be zero or greater")
		}
		row.RateCents = *in.RateCents
	case create:
		row.RateCents = s.defaultRate(ctx, m, row.UserID)
	}
	if _, err := timeAmount(row.RateCents, row.Minutes()); err != nil {
		return TimeEntry{}, domain.Invalid("rate_cents", "amount does not fit")
	}
	return row, nil
}

// CreateTime logs one time entry (quick entry, home widget, API).
func (s *Service) CreateTime(ctx context.Context, p domain.Principal, in TimeInput) (TimeEntry, error) {
	rows, err := s.BulkTime(ctx, p, []TimeInput{in})
	if err != nil {
		return TimeEntry{}, err
	}
	return rows[0], nil
}

// BulkTime validates every entry, then writes the batch in one transaction. One failure
// rejects the whole batch and writes nothing.
func (s *Service) BulkTime(ctx context.Context, p domain.Principal, items []TimeInput) ([]TimeEntry, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("items", "at least one item is required")
	}
	if s.overBulkMax(ctx, len(items)) {
		return nil, domain.Invalid("items", "batch is larger than api.bulk_batch_max")
	}
	out := make([]TimeEntry, 0, len(items))
	for _, in := range items {
		row, err := s.buildTime(ctx, p, TimeEntry{}, in, true)
		if err != nil {
			return nil, err
		}
		meta, err := s.meta(p)
		if err != nil {
			return nil, err
		}
		row.Meta = meta
		out = append(out, row)
	}
	err := s.write(ctx, p, func(t *txn) error {
		for _, row := range out {
			if err := t.put(kindTime, row.ID, row); err != nil {
				return err
			}
			if err := t.created("time_entry", row.ID, row, ""); err != nil {
				return err
			}
			if err := t.event("time_entry.created", "time_entry", row.ID, row.MatterID, "Time logged: "+row.Description, false); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateTime patches an unbilled time entry.
func (s *Service) UpdateTime(ctx context.Context, p domain.Principal, id domain.ID, in TimeInput) (TimeEntry, error) {
	var out TimeEntry
	err := s.write(ctx, p, func(t *txn) error {
		before, err := s.timeEntry(t.ctx, id)
		if err != nil {
			return err
		}
		if err := s.readBilling(t.ctx, p, before.MatterID); err != nil {
			return err
		}
		if err := s.allow(t.ctx, p, domain.ResTime, domain.ActionWrite, before.MatterID); err != nil {
			return err
		}
		after, err := s.buildTime(t.ctx, p, before, in, false)
		if err != nil {
			return err
		}
		after.UpdatedAt = s.now()
		if err := t.put(kindTime, id, after); err != nil {
			return err
		}
		if err := t.diff("time_entry", id, before, after, ""); err != nil {
			return err
		}
		out = after
		return t.event("time_entry.updated", "time_entry", id, after.MatterID, "Time entry updated", false)
	})
	return out, err
}

func (s *Service) timer(ctx context.Context, id domain.ID) (Timer, error) {
	var row Timer
	if err := s.d.Store.Get(ctx, kindTimer, string(id), &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Timer{}, domain.NotFound("timer")
		}
		return Timer{}, err
	}
	return row, nil
}

// Timers lists the caller's active timers on matters the caller can see.
func (s *Service) Timers(ctx context.Context, p domain.Principal) ([]Timer, error) {
	if err := s.allow(ctx, p, domain.ResTime, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	rows, err := listOf[Timer](ctx, s.d.Store, kindTimer)
	if err != nil {
		return nil, err
	}
	var out []Timer
	for _, r := range rows {
		if r.UserID != p.UserID || r.StoppedAt != nil {
			continue
		}
		if err := s.allow(ctx, p, domain.ResTime, domain.ActionRead, r.MatterID); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// Timer returns one of the caller's timers.
func (s *Service) Timer(ctx context.Context, p domain.Principal, id domain.ID) (Timer, error) {
	row, err := s.timer(ctx, id)
	if err != nil {
		return Timer{}, err
	}
	if row.UserID != p.UserID {
		return Timer{}, domain.NotFound("timer")
	}
	if err := s.allow(ctx, p, domain.ResTime, domain.ActionRead, row.MatterID); err != nil {
		return Timer{}, err
	}
	return row, nil
}

// StartTimer starts the caller's timer on a matter. A paused timer on the same matter resumes
// and keeps its seconds; a running one is a conflict. One active timer per user per matter.
func (s *Service) StartTimer(ctx context.Context, p domain.Principal, matter domain.ID) (Timer, bool, error) {
	if err := s.allow(ctx, p, domain.ResTime, domain.ActionWrite, matter); err != nil {
		return Timer{}, false, err
	}
	if _, err := s.matter(ctx, matter); err != nil {
		return Timer{}, false, err
	}
	var out Timer
	created := false
	err := s.write(ctx, p, func(t *txn) error {
		rows, err := listOf[Timer](t.ctx, s.d.Store, kindTimer)
		if err != nil {
			return err
		}
		now := s.now()
		for _, r := range rows {
			if r.UserID != p.UserID || r.MatterID != matter || r.StoppedAt != nil {
				continue
			}
			if r.RunningSince != nil {
				return domain.Conflict("a timer is already running on this matter")
			}
			before := r
			r.RunningSince = &now
			r.UpdatedAt = now
			out = r
			if err := t.put(kindTimer, r.ID, r); err != nil {
				return err
			}
			if err := t.diff("timer", r.ID, before, r, ""); err != nil {
				return err
			}
			return t.event("timer.resumed", "timer", r.ID, matter, "Timer resumed", false)
		}
		meta, err := s.meta(p)
		if err != nil {
			return err
		}
		out = Timer{Meta: meta, MatterID: matter, UserID: p.UserID, StartedAt: now, RunningSince: &now}
		created = true
		if err := t.put(kindTimer, out.ID, out); err != nil {
			return err
		}
		if err := t.created("timer", out.ID, out, ""); err != nil {
			return err
		}
		return t.event("timer.started", "timer", out.ID, matter, "Timer started", false)
	})
	return out, created, err
}

// PauseTimer stores the running seconds and stops counting.
func (s *Service) PauseTimer(ctx context.Context, p domain.Principal, id domain.ID) (Timer, error) {
	return s.moveTimer(ctx, p, id, func(r *Timer) (string, error) {
		if r.RunningSince == nil {
			return "", domain.Conflict("timer is not running")
		}
		r.AccumulatedSeconds += int64(s.now().Sub(*r.RunningSince).Seconds())
		r.RunningSince = nil
		return "timer.paused", nil
	})
}

// ResumeTimer counts again from the stored seconds.
func (s *Service) ResumeTimer(ctx context.Context, p domain.Principal, id domain.ID) (Timer, error) {
	return s.moveTimer(ctx, p, id, func(r *Timer) (string, error) {
		if r.RunningSince != nil {
			return "", domain.Conflict("timer is already running")
		}
		now := s.now()
		r.RunningSince = &now
		return "timer.resumed", nil
	})
}

func (s *Service) moveTimer(ctx context.Context, p domain.Principal, id domain.ID, step func(*Timer) (string, error)) (Timer, error) {
	var out Timer
	err := s.write(ctx, p, func(t *txn) error {
		before, err := s.Timer(t.ctx, p, id)
		if err != nil {
			return err
		}
		if err := s.allow(t.ctx, p, domain.ResTime, domain.ActionWrite, before.MatterID); err != nil {
			return err
		}
		if before.StoppedAt != nil {
			return domain.Conflict("timer is stopped")
		}
		after := before
		action, err := step(&after)
		if err != nil {
			return err
		}
		after.UpdatedAt = s.now()
		if err := t.put(kindTimer, id, after); err != nil {
			return err
		}
		out = after
		if err := t.diff("timer", id, before, after, ""); err != nil {
			return err
		}
		return t.event(action, "timer", id, after.MatterID, strings.ReplaceAll(action, ".", " "), false)
	})
	return out, err
}

// StopTimer stops the timer and creates one draft time entry, rounded up to the firm increment,
// for review (PRD 8.3). The entry carries the first active activity type until the user changes
// it, and it is not invoiced until a person saves it with a description. With no active
// activity type the stop is refused and the timer keeps its time.
func (s *Service) StopTimer(ctx context.Context, p domain.Principal, id domain.ID) (Timer, TimeEntry, error) {
	var timer Timer
	var entry TimeEntry
	err := s.write(ctx, p, func(t *txn) error {
		before, err := s.Timer(t.ctx, p, id)
		if err != nil {
			return err
		}
		if err := s.allow(t.ctx, p, domain.ResTime, domain.ActionWrite, before.MatterID); err != nil {
			return err
		}
		if before.StoppedAt != nil {
			return domain.Conflict("timer is already stopped")
		}
		m, err := s.matter(t.ctx, before.MatterID)
		if err != nil {
			return err
		}
		now := s.now()
		after := before
		if after.RunningSince != nil {
			after.AccumulatedSeconds += int64(now.Sub(*after.RunningSince).Seconds())
			after.RunningSince = nil
		}
		after.StoppedAt = &now
		after.UpdatedAt = now
		meta, err := s.meta(p)
		if err != nil {
			return err
		}
		activity := s.firstActiveType(t.ctx)
		if activity.IsZero() {
			return domain.Conflict("add an active activity type before stopping a timer; the timer keeps running")
		}
		inc := s.incrementMinutes(t.ctx)
		entry = TimeEntry{IncrementMinutes: inc, NeedsReview: true}
		entry.TimeEntry = domain.TimeEntry{
			Meta: meta, MatterID: m.ID, UserID: p.UserID, WorkedOn: s.today(t.ctx),
			Increments: incrementsFor(after.AccumulatedSeconds, inc), Billable: true,
			RateCents: s.defaultRate(t.ctx, m, p.UserID), Source: "timer",
			ActivityType: string(activity),
		}
		after.TimeEntryID = entry.ID
		timer = after
		if err := t.put(kindTimer, id, after); err != nil {
			return err
		}
		if err := t.put(kindTime, entry.ID, entry); err != nil {
			return err
		}
		if err := t.diff("timer", id, before, after, ""); err != nil {
			return err
		}
		if err := t.created("time_entry", entry.ID, entry, "timer stopped"); err != nil {
			return err
		}
		return t.event("time_entry.created", "time_entry", entry.ID, m.ID, "Time logged from a timer", false)
	})
	return timer, entry, err
}

func (s *Service) firstActiveType(ctx context.Context) domain.ID {
	rows, err := listOf[NamedType](ctx, s.d.Store, kindActivity)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.Active {
			return r.ID
		}
	}
	return ""
}
