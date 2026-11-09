package supervisor

import (
	"fmt"
	"time"
)

// Window is the firm's update window in local minutes from midnight.
// The default is 02:00 inclusive through 04:00 exclusive.
type Window struct {
	Start    time.Duration
	End      time.Duration
	Location *time.Location
}

func (w Window) Contains(t time.Time) bool {
	loc := w.Location
	if loc == nil {
		loc = time.UTC
	}
	local := t.In(loc)
	mins := local.Hour()*60 + local.Minute()
	start := int(w.Start / time.Minute)
	end := int(w.End / time.Minute)
	if start == end {
		return false
	}
	if start < end {
		return mins >= start && mins < end
	}
	return mins >= start || mins < end
}

// ParseClock reads HH:MM into a duration from midnight.
func ParseClock(value string) (time.Duration, error) {
	var hour, minute int
	if _, err := fmt.Sscanf(value, "%d:%d", &hour, &minute); err != nil {
		return 0, fmt.Errorf("clock %q: %w", value, ErrBadRequest)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("clock %q: %w", value, ErrBadRequest)
	}
	return time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute, nil
}
