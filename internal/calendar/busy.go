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
