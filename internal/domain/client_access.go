package domain

import "time"

// ClientAccessActive reports whether a client still may read a matter after close.
// Open and other non-closed statuses stay readable. A closed matter stays readable
// through closed_on plus days calendar days (inclusive); the next day ends access.
// Reopening clears closed status, so this returns true again.
func ClientAccessActive(status, closedOn string, days int, now time.Time) bool {
	if status != "closed" {
		return true
	}
	if closedOn == "" {
		return false
	}
	closed, err := time.Parse("2006-01-02", closedOn)
	if err != nil {
		return false
	}
	last := closed.AddDate(0, 0, days)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return !today.After(last)
}
