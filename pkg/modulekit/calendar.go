package modulekit

import (
	"github.com/CharitonMedia/togavsu/internal/calendar"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// CalendarOccupancyBlock contains only starts_at, ends_at and all_day. The flag
// is the sole widening of the times-only shape, including behind matter walls.
type CalendarOccupancyBlock = calendar.OccupancyBlock

// CalendarBusyBlock is the same restricted shape for view=busy.
type CalendarBusyBlock = calendar.BusyBlock

// CalendarVideo is an optional calendar event video booking object. Requested is
// required on the wire; nullable fields let the meetings consumer choose defaults.
// The event's location carries the join link.
type CalendarVideo = domain.CalendarVideo
