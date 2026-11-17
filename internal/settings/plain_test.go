package settings_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func TestDurationPlainRoundTrip(t *testing.T) {
	cases := []struct {
		stored string
		plain  string
		amount string
		unit   string
	}{
		{"12h0m0s", "12 hours", "12", "hours"},
		{"15m0s", "15 minutes", "15", "minutes"},
		{"180ms", "180 milliseconds", "180", "milliseconds"},
		{"30s", "30 seconds", "30", "seconds"},
		{"1h0m0s", "1 hour", "1", "hours"},
		{"24h0m0s", "1 day", "1", "days"},
		{"0s", "Right away", "0", "seconds"},
	}
	for _, tc := range cases {
		got, err := settings.FormatDurationPlain(tc.stored)
		if err != nil || got != tc.plain {
			t.Fatalf("format %s: got %q (%v), want %q", tc.stored, got, err, tc.plain)
		}
		amount, unit, err := settings.DurationParts(tc.stored)
		if err != nil {
			t.Fatalf("parts %s: %v", tc.stored, err)
		}
		back, err := settings.ParseDurationPlain(tc.amount, tc.unit)
		if err != nil {
			t.Fatalf("parse %s %s: %v", tc.amount, tc.unit, err)
		}
		// Stored form is Duration.String(); parse of that must equal original length.
		want, err := time.ParseDuration(tc.stored)
		if err != nil {
			t.Fatal(err)
		}
		gotDur, err := time.ParseDuration(back)
		if err != nil || gotDur != want {
			t.Fatalf("round trip %s via %d %s → %s (%v)", tc.stored, amount, unit, back, err)
		}
		if back != want.String() {
			t.Fatalf("stored form for %s: got %q want %q", tc.stored, back, want.String())
		}
	}
}

func TestBytesPlainRoundTrip(t *testing.T) {
	cases := []struct {
		stored string
		plain  string
		amount string
		unit   string
	}{
		{"2147483648", "2 GB", "2", "GB"},
		{"8388608", "8 MB", "8", "MB"},
		{"1073741824", "1 GB", "1", "GB"},
	}
	for _, tc := range cases {
		got, err := settings.FormatBytesPlain(tc.stored)
		if err != nil || got != tc.plain {
			t.Fatalf("format %s: %q (%v)", tc.stored, got, err)
		}
		back, err := settings.ParseBytesPlain(tc.amount, tc.unit)
		if err != nil || back != tc.stored {
			t.Fatalf("parse %s %s: got %q (%v)", tc.amount, tc.unit, back, err)
		}
	}
}

func TestLinesPlainRoundTrip(t *testing.T) {
	stored := `["Correspondence","Pleadings","Discovery"]`
	plain, err := settings.FormatLinesPlain(stored)
	if err != nil || plain != "Correspondence\nPleadings\nDiscovery" {
		t.Fatalf("format: %q %v", plain, err)
	}
	back, err := settings.ParseLinesPlain("Correspondence\n\nPleadings\nDiscovery\n")
	if err != nil || back != stored {
		t.Fatalf("parse: %q %v", back, err)
	}
	empty, err := settings.ParseLinesPlain("\n\n")
	if err != nil || empty != "[]" {
		t.Fatalf("empty lines: %q %v", empty, err)
	}
}

func TestRetryPlainRoundTrip(t *testing.T) {
	stored := domain.DefaultRetrySchedule
	plain, err := settings.FormatRetryPlain(stored)
	if err != nil {
		t.Fatal(err)
	}
	wantPlain := "Right away\nRight away\n15 minutes\n2 hours\n12 hours\n1 day"
	if plain != wantPlain {
		t.Fatalf("format:\n%s\nwant:\n%s", plain, wantPlain)
	}
	back, err := settings.ParseRetryPlain(plain)
	if err != nil {
		t.Fatal(err)
	}
	if back != stored {
		t.Fatalf("parse: got %s want %s", back, stored)
	}
}

func TestNotificationsPlainRoundTrip(t *testing.T) {
	stored := domain.DefaultNotificationPrefs
	rows := settings.NotifyRows(stored)
	if len(rows) != 6 {
		t.Fatalf("rows: %d", len(rows))
	}
	form := url.Values{}
	for _, r := range rows {
		if r.InApp {
			form.Set(r.Kind+".in_app", "on")
		}
		if r.Email {
			form.Set(r.Kind+".email", "on")
		}
	}
	// Clearing trust email in the form still stores it locked on.
	form.Del("trust_flag.email")
	back, err := settings.ParseNotificationsForm(form)
	if err != nil {
		t.Fatal(err)
	}
	again := settings.NotifyRows(back)
	for _, r := range again {
		if r.Kind == "trust_flag" && (!r.Email || !r.EmailRequired) {
			t.Fatalf("trust flag email must stay on: %+v", r)
		}
	}
	summary := settings.FormatNotificationsPlain(stored)
	if summary == "" || summary == "None" {
		t.Fatalf("summary: %q", summary)
	}
}

func TestColorsPlainRoundTrip(t *testing.T) {
	form := url.Values{}
	form.Set("color_staff_sidebar_mode", "custom")
	form.Set("color_staff_sidebar", "#123456")
	form.Set("color_staff_accent_mode", "preset")
	form.Set("color_staff_accent", "#abcdef")
	back, err := settings.ParseColorsForm(form)
	if err != nil {
		t.Fatal(err)
	}
	if back != `{"staff_sidebar":"#123456"}` {
		t.Fatalf("got %s", back)
	}
	empty, err := settings.ParseColorsForm(url.Values{})
	if err != nil || empty != "{}" {
		t.Fatalf("empty: %q %v", empty, err)
	}
	if settings.FormatColorsPlain("{}") != "None" {
		t.Fatal("empty colors display")
	}
}

func TestModelPricesAndOverridesPlain(t *testing.T) {
	if settings.FormatModelPricesPlain("{}") != "None set" {
		t.Fatal("empty prices")
	}
	if settings.FormatTaskOverridesPlain("{}") != "None set" {
		t.Fatal("empty overrides")
	}
	prices := `{"gpt-test":{"input":100,"output":200}}`
	if s := settings.FormatModelPricesPlain(prices); s == "None set" || s == "" {
		t.Fatalf("prices: %q", s)
	}
	ov := `{"labeling":{"provider":"openai","model":"gpt-fast"}}`
	if s := settings.FormatTaskOverridesPlain(ov); s == "None set" {
		t.Fatalf("overrides: %q", s)
	}
}

func TestNamingSample(t *testing.T) {
	sample := settings.NamingSample(domain.NamingPattern)
	if sample == domain.NamingPattern || sample == "" {
		t.Fatalf("sample unchanged: %q", sample)
	}
	for _, part := range []string{"Smith", "2026-0001", "petition", "pdf"} {
		if !strings.Contains(sample, part) {
			t.Fatalf("sample missing %q: %q", part, sample)
		}
	}
}

func TestDisplayPlainDefaults(t *testing.T) {
	for _, d := range settings.Definitions() {
		got := settings.DisplayPlain(d, d.Default)
		switch settings.PlainOf(d) {
		case settings.PlainDuration:
			if got == d.Default && d.Default != "" {
				t.Errorf("%s still shows raw duration %q", d.Key, got)
			}
		case settings.PlainBytes:
			if got == d.Default {
				t.Errorf("%s still shows raw bytes %q", d.Key, got)
			}
		case settings.PlainLines:
			if got != "" && got[0] == '[' {
				t.Errorf("%s still shows JSON %q", d.Key, got)
			}
		}
	}
}

