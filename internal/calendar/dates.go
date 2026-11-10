package calendar

import (
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const dateLayout = "2006-01-02"

// parseDate reads YYYY-MM-DD as a calendar date at UTC midnight.
func parseDate(s string) (time.Time, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, domain.ErrTime
	}
	return t, nil
}

// dayStart is local midnight of the calendar date d in loc.
// It is correct across daylight-saving changes because it builds the wall time.
func dayStart(d time.Time, loc *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
}

// localDate is the calendar date (UTC midnight) of instant t in loc.
func localDate(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// addDays moves a calendar date (UTC midnight) by n days; time.Date normalizes
// month and year overflow.
func addDays(d time.Time, n int) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day()+n, 0, 0, 0, 0, time.UTC)
}

// addMonths moves a calendar date by n months, normalizing like time.AddDate.
func addMonths(d time.Time, n int) time.Time {
	return time.Date(d.Year(), d.Month()+time.Month(n), d.Day(), 0, 0, 0, 0, time.UTC)
}

// monthStep is day dd of the month before y-mo (back) or after it, or that month's last
// day when it is shorter, so Next from January 31 is February 28 and no month is skipped.
func monthStep(y int, mo time.Month, dd int, back bool) time.Time {
	first := time.Date(y, mo+1, 1, 0, 0, 0, 0, time.UTC)
	if back {
		first = time.Date(y, mo-1, 1, 0, 0, 0, 0, time.UTC)
	}
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(first.Year(), first.Month(), min(dd, last), 0, 0, 0, 0, time.UTC)
}

// nextDay is the following calendar date.
func nextDay(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, time.UTC)
}

// weekStart is the Monday on or before d.
func weekStart(d time.Time) time.Time {
	back := (int(d.Weekday()) + 6) % 7
	return time.Date(d.Year(), d.Month(), d.Day()-back, 0, 0, 0, 0, time.UTC)
}

// unsetMicro is the stored value of a timestamp column that has no time yet
// (deadlines.done_at). It is Go's zero value, not a setting.
var unsetMicro int64

// none is the zero count. Comparing with it is a sign check, not a setting.
var none int

func isSet(v int64) bool { return v != unsetMicro }

// overlaps reports whether [a1,a2) and [b1,b2) share time. A zero-length event
// [a,a] counts as touching the window that contains a.
func overlaps(a1, a2, b1, b2 time.Time) bool {
	if !a2.After(a1) {
		return !a1.Before(b1) && a1.Before(b2)
	}
	return a1.Before(b2) && a2.After(b1)
}
