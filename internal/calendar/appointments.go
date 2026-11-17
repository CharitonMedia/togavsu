package calendar

import (
	"context"
	"sort"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// clientAppointmentKinds is the CORE-15 allowlist. Other event kinds stay firm-only.
var clientAppointmentKinds = map[string]bool{
	KindClientMeeting: true,
	KindCourtDate:     true,
}

// ClientAppointment is the client portal projection of an upcoming calendar event
// (CORE-15). Fields match the contract allowlist only.
type ClientAppointment struct {
	Title    string
	Kind     string
	Start    time.Time
	End      time.Time
	AllDay   bool
	Location string
	MatterID domain.ID
}

// ListClientAppointments returns upcoming client_meeting and court_date events on
// matters the client can read (CORE-11). Staff callers are forbidden; the firm
// calendar API stays staff-only.
func (s *Service) ListClientAppointments(ctx context.Context, p domain.Principal) ([]ClientAppointment, error) {
	if !practice.ClientOnly(p) {
		return nil, domain.Forbidden("client role is required")
	}
	now := s.now()
	rows, err := s.d.Store.EventsBetween(ctx, now, time.Time{})
	if err != nil {
		return nil, err
	}
	readable := map[domain.ID]bool{}
	var out []ClientAppointment
	for _, row := range rows {
		if !clientAppointmentKinds[row.Kind] || row.MatterID.IsZero() {
			continue
		}
		if row.End().Before(now) {
			continue
		}
		ok, seen := readable[row.MatterID]
		if !seen {
			_, err := s.d.Matters.Matter(ctx, p, row.MatterID)
			ok = err == nil
			readable[row.MatterID] = ok
		}
		if !ok {
			continue
		}
		out = append(out, ClientAppointment{
			Title: row.Title, Kind: row.Kind, Start: row.Start(), End: row.End(),
			AllDay: row.AllDay, Location: row.Location, MatterID: row.MatterID,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start.Equal(out[j].Start) {
			return out[i].MatterID < out[j].MatterID
		}
		return out[i].Start.Before(out[j].Start)
	})
	return out, nil
}
