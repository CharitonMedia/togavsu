package supervisor

import (
	"testing"
	"time"
)

func TestWindowDefaultBounds(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	w := Window{Start: 2 * time.Hour, End: 4 * time.Hour, Location: loc}
	cases := []struct {
		local string
		want  bool
	}{
		{"2026-01-15T01:59:00", false},
		{"2026-01-15T02:00:00", true},
		{"2026-01-15T03:59:00", true},
		{"2026-01-15T04:00:00", false},
		{"2026-01-15T12:00:00", false},
	}
	for _, tc := range cases {
		when, err := time.ParseInLocation("2006-01-02T15:04:05", tc.local, loc)
		if err != nil {
			t.Fatal(err)
		}
		if got := w.Contains(when); got != tc.want {
			t.Fatalf("%s contains=%v want %v", tc.local, got, tc.want)
		}
	}
}

func TestWindowUsesFirmZone(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	w := Window{Start: 2 * time.Hour, End: 4 * time.Hour, Location: loc}
	// 08:30 UTC is 02:30 in Chicago during January (CST, UTC-6).
	utc := time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC)
	if !w.Contains(utc) {
		t.Fatal("expected the UTC instant to fall inside the firm window")
	}
}

func TestParseClock(t *testing.T) {
	got, err := ParseClock("02:00")
	if err != nil || got != 2*time.Hour {
		t.Fatalf("got %s %v", got, err)
	}
	if _, err := ParseClock("24:00"); err == nil {
		t.Fatal("accepted 24:00")
	}
	if _, err := ParseClock("nope"); err == nil {
		t.Fatal("accepted nope")
	}
}
