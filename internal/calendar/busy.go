package calendar

import (
	"context"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// BusyBlock is occupancy only. It deliberately cannot carry any event, matter,
// user, author or other record identifier, including for events behind a wall.
type BusyBlock struct {
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
	AllDay   bool   `json:"all_day"`
}

// ListBusy answers availability across walls without granting event access.
// Event detail and ordinary list reads continue to apply matter walls.
func (s *Service) ListBusy(ctx context.Context, p domain.Principal, f EventFilter) ([]BusyBlock, error) {
	var zero int
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	if f.UserID.IsZero() {
		return nil, domain.Invalid("user_id", "busy view needs user_id")
	}
	if _, err := s.activeUser(ctx, "user_id", f.UserID); err != nil {
		return nil, err
	}
	if !f.MatterID.IsZero() || f.Kind != "" {
		return nil, domain.Invalid("view", "busy view cannot filter by matter or event kind")
	}
	if f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) {
		return nil, domain.Invalid("to_time", "busy view needs from_time and a later to_time")
	}
	if f.To.After(f.From.UTC().AddDate(zero, maxRangeMonths(), zero)) {
		return nil, domain.Invalid("to_time", "busy view exceeds the available dates range")
	}
	rows, err := s.d.Store.EventsBetween(ctx, f.From, f.To)
	if err != nil {
		return nil, err
	}
	out := []BusyBlock{}
	for _, row := range rows {
		matches := matchEvent(row, f)
		if !matches && !row.MatterID.IsZero() && !s.visible(ctx, p, row.MatterID) {
			active, err := s.hasActiveAttendee(ctx, row.Attendees)
			if err != nil {
				return nil, err
			}
			window := f
			window.UserID = ""
			matches = !active && matchEvent(row, window)
		}
		if matches {
			out = append(out, BusyBlock{StartsAt: fmtTime(row.StartsAt), EndsAt: fmtTime(row.EndsAt), AllDay: row.AllDay})
			if f.Max > zero && len(out) == f.Max {
				break
			}
		}
	}
	return out, nil
}

// OccupancyBlock contains only a busy interval, including unassigned walled events.
type OccupancyBlock struct {
	AllDay   bool   `json:"all_day"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

// ListOccupancy allows a calendar service principal to read firm-wide occupancy.
// Only start, end and all_day are exposed; no event details or identifiers.
func (s *Service) ListOccupancy(ctx context.Context, p domain.Principal, f EventFilter) ([]OccupancyBlock, error) {
	var zero int
	if p.Source != domain.SourceService || p.ModuleID == "" || !p.UserID.IsZero() {
		return nil, domain.Forbidden("firm occupancy requires a module service principal")
	}
	allowed := false
	for _, scope := range p.Scopes {
		if scope == "calendar:read" {
			allowed = true
		}
	}
	if !allowed {
		return nil, domain.Forbidden("calendar:read is required")
	}
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	if !f.UserID.IsZero() || !f.MatterID.IsZero() || f.Kind != "" {
		return nil, domain.Invalid("view", "occupancy cannot filter by user, matter or kind")
	}
	if f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) || f.To.After(f.From.UTC().AddDate(zero, maxRangeMonths(), zero)) {
		return nil, domain.Invalid("to_time", "occupancy needs an ordered from_time/to_time interval within the available dates range")
	}
	rows, err := s.d.Store.OccupancyCandidates(ctx, f.From, f.To)
	if err != nil {
		return nil, err
	}
	out := []OccupancyBlock{}
	for _, row := range rows {
		// Unassigned intervals never consult wall policy, regardless of placement.
		// Assigned intervals retain the same authoritative service policy as lists.
		assigned, err := s.hasActiveAttendee(ctx, row.Attendees)
		if err != nil {
			return nil, err
		}
		if !overlaps(domain.FromUnixMicro(row.StartsAt), domain.FromUnixMicro(row.EndsAt), f.From, f.To) ||
			(assigned && !row.MatterID.IsZero() && !s.visible(ctx, p, row.MatterID)) {
			continue
		}
		out = append(out, OccupancyBlock{StartsAt: fmtTime(row.StartsAt), EndsAt: fmtTime(row.EndsAt), AllDay: row.AllDay})
		if f.Max > zero && len(out) == f.Max {
			break
		}
	}
	return out, nil
}

// hasActiveAttendee reads current user state on every availability read. Lookup
// failures fail the read rather than silently exposing an assigned walled event.
func (s *Service) hasActiveAttendee(ctx context.Context, ids []domain.ID) (bool, error) {
	for _, id := range ids {
		u, err := s.d.Users.Get(ctx, id)
		if errors.Is(err, repository.ErrNotFound) || isCode(err, domain.CodeNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if u.Active {
			return true, nil
		}
	}
	return false, nil
}
