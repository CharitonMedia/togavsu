package domain

import (
	"testing"
	"time"
)

func TestClientAccessActive(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	}
	cases := []struct {
		name, status, closed string
		days                 int
		now                  time.Time
		want                 bool
	}{
		{"open", "open", "", ClientAccessDaysAfterClose, day(2026, 1, 16), true},
		{"on_hold", "on_hold", "", ClientAccessDaysAfterClose, day(2026, 1, 16), true},
		{"closed still in window", "closed", "2026-01-02", ClientAccessDaysAfterClose, day(2026, 1, 10), true},
		{"closed last day", "closed", "2026-01-02", ClientAccessDaysAfterClose, day(2026, 1, 16), true},
		{"closed day after window", "closed", "2026-01-01", ClientAccessDaysAfterClose, day(2026, 1, 16), false},
		{"closed missing date", "closed", "", ClientAccessDaysAfterClose, day(2026, 1, 16), false},
		{"closed bad date", "closed", "not-a-date", ClientAccessDaysAfterClose, day(2026, 1, 16), false},
		{"reopened shape", "open", "2026-01-01", ClientAccessDaysAfterClose, day(2026, 1, 16), true},
		{"zero days same day", "closed", "2026-01-16", 0, day(2026, 1, 16), true},
		{"zero days next day", "closed", "2026-01-15", 0, day(2026, 1, 16), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClientAccessActive(tc.status, tc.closed, tc.days, tc.now)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
