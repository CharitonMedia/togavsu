package domain

import "testing"

func TestEffectiveOn(t *testing.T) {
	today := "2026-11-17"
	cases := []struct {
		from, to string
		want     bool
	}{
		{"", "", true},
		{"", today, false},        // effective_to == today excludes
		{"", "2026-11-16", false}, // already ended
		{"", "2026-11-18", true},  // ends tomorrow
		{"2026-11-18", "", false}, // not yet started
		{"2026-11-17", "", true},  // started today
		{"2026-11-01", today, false},
	}
	for _, c := range cases {
		if got := EffectiveOn(c.from, c.to, today); got != c.want {
			t.Fatalf("EffectiveOn(%q,%q,%q)=%v want %v", c.from, c.to, today, got, c.want)
		}
	}
}
