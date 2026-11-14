package calendar

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// normalizeAllDay turns an all-day input into firm-local midnights. The date is read
// from each timestamp as written (its own offset), so 2026-12-01T00:00:00Z is
// December 1 in any firm zone. The end is exclusive: an end at midnight after the
// start date ends there; any other end includes its whole written date.
func normalizeAllDay(start, end time.Time, loc *time.Location) (time.Time, time.Time) {
	sd := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	ed := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	midnight := end.Equal(time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location()))
	if !(midnight && ed.After(sd)) {
		ed = nextDay(ed)
	}
	if !ed.After(sd) {
		ed = nextDay(sd)
	}
	return dayStart(sd, loc).UTC(), dayStart(ed, loc).UTC()
}

// validateEvent checks an event. stored holds attendees already saved on the
// row (nil for a new event); they may stay after deactivation so unrelated
// edits still work. Newly added attendees must be active.
func (s *Service) validateEvent(ctx context.Context, p domain.Principal, in *EventInput, stored []domain.ID) error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return domain.Invalid("title", "title is required")
	}
	if !validKind(in.Kind) {
		return domain.Invalid("kind", "kind must be court_date, client_meeting, deadline, block or other")
	}
	if in.StartsAt.IsZero() {
		return domain.Invalid("starts_at", "start is required")
	}
	if in.EndsAt.IsZero() {
		return domain.Invalid("ends_at", "end is required")
	}
	if in.AllDay {
		in.StartsAt, in.EndsAt = normalizeAllDay(in.StartsAt, in.EndsAt, s.Zone(ctx))
	}
	if in.EndsAt.Before(in.StartsAt) {
		return domain.Invalid("ends_at", "end is before start")
	}
	if in.TravelMinutes < none {
		return domain.Invalid("travel_minutes", "travel minutes cannot be negative")
	}
	seen := map[domain.ID]bool{}
	var att []domain.ID
	for _, id := range in.Attendees {
		if seen[id] {
			continue
		}
		seen[id] = true
		if id.IsZero() || !containsID(stored, id) {
			if _, err := s.activeUser(ctx, "attendee_user_ids", id); err != nil {
				return err
			}
		}
		att = append(att, id)
	}
	in.Attendees = att
	if !in.MatterID.IsZero() {
		return s.checkMatter(ctx, p, in.MatterID)
	}
	return s.gate(ctx, p, domain.ActionWrite, "")
}

// CreateEvent adds an event. Staff, attorneys and admins may write; walls apply.
func (s *Service) CreateEvent(ctx context.Context, p domain.Principal, in EventInput) (Event, error) {
	if err := s.gate(ctx, p, domain.ActionWrite, ""); err != nil {
		return Event{}, err
	}
	if err := s.validateEvent(ctx, p, &in, nil); err != nil {
		return Event{}, err
	}
	m, err := s.meta(p)
	if err != nil {
		return Event{}, err
	}
	row := Event{CalendarEvent: domain.CalendarEvent{
		Meta: m, Title: in.Title, Kind: in.Kind, StartsAt: domain.UnixMicroUTC(in.StartsAt), EndsAt: domain.UnixMicroUTC(in.EndsAt),
		AllDay: in.AllDay, MatterID: in.MatterID, Location: in.Location, TravelMinutes: in.TravelMinutes, Notes: in.Notes,
	}, Attendees: in.Attendees}
	err = s.commit(ctx, p,
		func(ctx context.Context) error { return s.d.Store.PutEvent(ctx, row) },
		func(ctx context.Context) error { return s.d.Store.DeleteEvent(ctx, row.ID) },
		"calendar_event", row.ID, row.MatterID, "calendar_event.created", row.Title, eventChanges(Event{}, row))
	if err != nil {
		return Event{}, err
	}
	return row, nil
}

func eventChanges(a, b Event) []change {
	return []change{
		{"title", a.Title, b.Title},
		{"kind", a.Kind, b.Kind},
		{"starts_at", fmtTime(a.StartsAt), fmtTime(b.StartsAt)},
		{"ends_at", fmtTime(a.EndsAt), fmtTime(b.EndsAt)},
		{"all_day", boolText(a, b, a.AllDay), boolText(a, b, b.AllDay)},
		{"matter_id", string(a.MatterID), string(b.MatterID)},
		{"location", a.Location, b.Location},
		{"travel_minutes", intText(a.ID, a.TravelMinutes), intText(b.ID, b.TravelMinutes)},
		{"notes", a.Notes, b.Notes},
		{"attendee_user_ids", joinIDs(a.Attendees), joinIDs(b.Attendees)},
	}
}

func boolText(a, b Event, v bool) string {
	if a.ID.IsZero() && !v {
		return ""
	}
	return fmtBool(v)
}

func intText(id domain.ID, v int) string {
	if id.IsZero() && !isSet(int64(v)) {
		return ""
	}
	return strconv.Itoa(v)
}

// GetEvent returns one event the caller can see.
func (s *Service) GetEvent(ctx context.Context, p domain.Principal, id domain.ID) (Event, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return Event{}, err
	}
	row, err := s.d.Store.Event(ctx, id)
	if err != nil {
		return Event{}, missing(err, "calendar event")
	}
	if !row.MatterID.IsZero() && !s.visible(ctx, p, row.MatterID) {
		return Event{}, domain.NotFound("calendar event")
	}
	return row, nil
}

// UpdateEvent applies a patch. The current and the new matter must both be visible.
func (s *Service) UpdateEvent(ctx context.Context, p domain.Principal, id domain.ID, patch EventPatch) (Event, error) {
	prev, err := s.GetEvent(ctx, p, id)
	if err != nil {
		return Event{}, err
	}
	if !prev.MatterID.IsZero() {
		if err := s.gate(ctx, p, domain.ActionWrite, prev.MatterID); err != nil {
			return Event{}, err
		}
	}
	in := EventInput{
		Title: prev.Title, Kind: prev.Kind, StartsAt: prev.Start(), EndsAt: prev.End(), AllDay: prev.AllDay,
		MatterID: prev.MatterID, Location: prev.Location, TravelMinutes: prev.TravelMinutes, Notes: prev.Notes, Attendees: prev.Attendees,
	}
	if patch.Title != nil {
		in.Title = *patch.Title
	}
	if patch.Kind != nil {
		in.Kind = *patch.Kind
	}
	if patch.StartsAt != nil {
		in.StartsAt = *patch.StartsAt
	}
	if patch.EndsAt != nil {
		in.EndsAt = *patch.EndsAt
	}
	if patch.AllDay != nil {
		in.AllDay = *patch.AllDay
	}
	if patch.MatterID != nil {
		in.MatterID = *patch.MatterID
	}
	if patch.Location != nil {
		in.Location = *patch.Location
	}
	if patch.TravelMinutes != nil {
		in.TravelMinutes = *patch.TravelMinutes
	}
	if patch.Notes != nil {
		in.Notes = *patch.Notes
	}
	if patch.Attendees != nil {
		in.Attendees = *patch.Attendees
	}
	if in.AllDay {
		// Re-express unpatched stored instants as firm-local written dates for
		// normalizeAllDay. This covers both all-day edits and a timed event
		// turned all-day: a 7 PM Chicago start stored as 01:00Z the next day
		// must stay on its local date.
		loc := s.Zone(ctx)
		if patch.StartsAt == nil {
			in.StartsAt = localDate(prev.Start(), loc)
		}
		if patch.EndsAt == nil {
			in.EndsAt = localDate(prev.End(), loc)
			// A timed end after local midnight covers that date too; written
			// midnight dates are exclusive ends for normalizeAllDay.
			if !prev.AllDay && !prev.End().Equal(dayStart(in.EndsAt, loc)) {
				in.EndsAt = nextDay(in.EndsAt)
			}
		}
	}
	if err := s.validateEvent(ctx, p, &in, prev.Attendees); err != nil {
		return Event{}, err
	}
	next := prev
	next.UpdatedAt = s.now()
	next.Title, next.Kind, next.AllDay, next.MatterID = in.Title, in.Kind, in.AllDay, in.MatterID
	next.StartsAt, next.EndsAt = domain.UnixMicroUTC(in.StartsAt), domain.UnixMicroUTC(in.EndsAt)
	next.Location, next.TravelMinutes, next.Notes, next.Attendees = in.Location, in.TravelMinutes, in.Notes, in.Attendees
	var before Event
	err = s.commit(ctx, p,
		func(ctx context.Context) error {
			var err error
			if before, err = s.d.Store.Event(ctx, next.ID); err != nil {
				return missing(err, "calendar event")
			}
			if err := stale(prev, before, "the calendar event"); err != nil {
				return err
			}
			return s.d.Store.PutEvent(ctx, next)
		},
		func(ctx context.Context) error { return s.d.Store.PutEvent(ctx, before) },
		"calendar_event", next.ID, next.MatterID, "calendar_event.updated", next.Title, eventChanges(prev, next))
	if err != nil {
		return Event{}, err
	}
	return next, nil
}

// DeleteEvent removes an event. The audit log keeps every value it had.
func (s *Service) DeleteEvent(ctx context.Context, p domain.Principal, id domain.ID) error {
	prev, err := s.GetEvent(ctx, p, id)
	if err != nil {
		return err
	}
	if err := s.gate(ctx, p, domain.ActionWrite, prev.MatterID); err != nil {
		return err
	}
	changes := eventChanges(prev, Event{CalendarEvent: domain.CalendarEvent{Meta: domain.Meta{ID: prev.ID}}})
	changes = append(changes, change{"deleted", "false", "true"})
	var before Event
	return s.commit(ctx, p,
		func(ctx context.Context) error {
			var err error
			if before, err = s.d.Store.Event(ctx, id); err != nil {
				return missing(err, "calendar event")
			}
			if err := stale(prev, before, "the calendar event"); err != nil {
				return err
			}
			return missing(s.d.Store.DeleteEvent(ctx, id), "calendar event")
		},
		func(ctx context.Context) error { return s.d.Store.PutEvent(ctx, before) },
		"calendar_event", prev.ID, prev.MatterID, "calendar_event.deleted", prev.Title, changes)
}

// ListEvents returns visible events overlapping the filter window, by start.
func (s *Service) ListEvents(ctx context.Context, p domain.Principal, f EventFilter) ([]Event, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	if f.Kind != "" && !validKind(f.Kind) {
		return nil, domain.Invalid("kind", "unknown kind")
	}
	if !f.MatterID.IsZero() && !s.visible(ctx, p, f.MatterID) {
		return nil, domain.NotFound("matter")
	}
	rows, err := s.d.Store.EventsBetween(ctx, f.From, f.To)
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, row := range rows {
		if !matchEvent(row, f) {
			continue
		}
		if !row.MatterID.IsZero() && !s.visible(ctx, p, row.MatterID) {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func matchEvent(row Event, f EventFilter) bool {
	if f.Kind != "" && row.Kind != f.Kind {
		return false
	}
	if !f.MatterID.IsZero() && row.MatterID != f.MatterID {
		return false
	}
	if !f.UserID.IsZero() && !hasID(row.Attendees, f.UserID) {
		return false
	}
	if !f.From.IsZero() || !f.To.IsZero() {
		from, to := f.From, f.To
		if from.IsZero() {
			from = time.Unix(0, 0)
		}
		if to.IsZero() {
			to = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		if !overlaps(row.Start(), row.End(), from, to) {
			return false
		}
	}
	return true
}

func hasID(ids []domain.ID, id domain.ID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func containsID(ids []domain.ID, id domain.ID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
