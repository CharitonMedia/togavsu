package settings_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func TestDeriveCasePattern(t *testing.T) {
	cases := []struct {
		example string
		want    string
	}{
		{"23AR-CV00825", domain.DefaultCasePattern},
		{" 23AR-CV00825 ", domain.DefaultCasePattern},
		{"24JE-DR01234", `^\d{2}[A-Z]{2}-[A-Z]{2}\d+$`},
		{"2026-CV-1", `^\d{4}-[A-Z]{2}-\d+$`},
		{"AB12", `^[A-Z]{2}\d+$`},
		{"12", `^\d+$`},
	}
	for _, tc := range cases {
		got, err := settings.DeriveCasePattern(tc.example)
		if err != nil {
			t.Fatalf("%q: %v", tc.example, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.example, got, tc.want)
		}
	}
}

func TestDeriveCasePatternRefuses(t *testing.T) {
	cases := []struct {
		example string
		substr  string
	}{
		{"", "empty"},
		{"   ", "empty"},
		{"ABCDEF", "no digit"},
		{strings.Repeat("1", 41), "longer than 40"},
	}
	for _, tc := range cases {
		_, err := settings.DeriveCasePattern(tc.example)
		if err == nil || !strings.Contains(err.Error(), tc.substr) {
			t.Fatalf("%q: want error containing %q, got %v", tc.example, tc.substr, err)
		}
	}
}

func TestDefaultExamplesDeriveDefaultPatterns(t *testing.T) {
	var examplesDefault, patternsDefault string
	for _, d := range settings.Definitions() {
		switch d.Key {
		case "practice.case_number_examples":
			examplesDefault = d.Default
		case "practice.case_number_patterns":
			patternsDefault = d.Default
		}
	}
	if examplesDefault == "" || patternsDefault == "" {
		t.Fatal("missing case number settings in catalog")
	}
	got, err := settings.DeriveCasePatterns(examplesDefault)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal([]string{domain.DefaultCasePattern})
	if string(want) != patternsDefault {
		t.Fatalf("patterns default %s, want %s", patternsDefault, want)
	}
	if len(got) != 1 || got[0] != domain.DefaultCasePattern {
		t.Fatalf("derived %#v", got)
	}
	if !settings.PatternsMatchExamples(examplesDefault, patternsDefault) {
		t.Fatal("default examples and patterns should match")
	}
}
