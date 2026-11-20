package calendar

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
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
		if matchEvent(row, f) {
			out = append(out, BusyBlock{StartsAt: fmtTime(row.StartsAt), EndsAt: fmtTime(row.EndsAt), AllDay: row.AllDay})
			if f.Max > zero && len(out) == f.Max {
				break
			}
		}
	}
	return out, nil
}

// OccupancyBlock contains only the interval, including unassigned walled events.
type OccupancyBlock struct {
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

// ListOccupancy allows a calendar service principal to read firm-wide occupancy.
// No user, matter, kind, wall, title, all-day flag or event id is exposed.
func (s *Service) ListOccupancy(ctx context.Context, p domain.Principal, f EventFilter) ([]OccupancyBlock, error) {
	if p.Source != domain.SourceService || p.ModuleID == "" || !p.UserID.IsZero() {
		return nil, domain.Forbidden("firm occupancy requires a module service principal")
	}
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	if !f.UserID.IsZero() || !f.MatterID.IsZero() || f.Kind != "" {
		return nil, domain.Invalid("view", "occupancy cannot filter by user, matter or kind")
	}
	if f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) || f.To.After(f.From.UTC().AddDate(0, maxRangeMonths(), 0)) {
		return nil, domain.Invalid("to_time", "occupancy needs an ordered from_time/to_time interval within the available dates range")
	}
	rows, err := s.d.Store.EventsBetween(ctx, f.From, f.To)
	if err != nil {
		return nil, err
	}
	out := []OccupancyBlock{}
	for _, row := range rows {
		out = append(out, OccupancyBlock{StartsAt: fmtTime(row.StartsAt), EndsAt: fmtTime(row.EndsAt)})
		if f.Max > 0 && len(out) == f.Max {
			break
		}
	}
	return out, nil
}
