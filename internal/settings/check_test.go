package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/settings"
)

func TestScanSource(t *testing.T) {
	cases := []struct {
		path string
		src  string
		bad  bool
	}{
		{"cmd/core/main.go", "package main\nimport \"os\"\nfunc f(){ os.Getenv(\"TOGAVSU_DATABASE_URL\") }\n", true},
		{"internal/bootstrap/config.go", "package bootstrap\nimport \"os\"\nfunc f(){ os.Getenv(\"TOGAVSU_DATABASE_URL\") }\n", false},
		{"internal/bootstrap/extra/x.go", "package extra\nimport \"os\"\nfunc f(){ os.LookupEnv(\"X\") }\n", true},
		{"internal/practice/time.go", "package practice\nimport \"time\"\nfunc f() time.Duration { return 5 * time.Minute }\n", true},
		{"internal/settings/definitions.go", "package settings\nimport \"time\"\nfunc f() time.Duration { return time.Hour }\n", false},
		{"internal/billing/wait.go", "package billing\nimport \"time\"\nfunc f() { time.ParseDuration(\"15m\") }\n", true},
		{"cmd/core/pin.go", "package main\nvar c = \"770a8fab12392485c0c2789fb254da7d9296df4d\"\n", true},
		{"internal/domain/defaults.go", "package domain\nvar c = \"770a8fab12392485c0c2789fb254da7d9296df4d\"\n", false},
		{"cmd/core/main.go", "package main\nimport \"os\"\nfunc f(){ _ = os.Stdout }\n", false},
	}
	for _, tc := range cases {
		msgs := settings.ScanSource(tc.path, tc.src)
		if tc.bad && len(msgs) == 0 {
			t.Errorf("%s: expected a violation", tc.path)
		}
		if !tc.bad && len(msgs) > 0 {
			t.Errorf("%s: %s", tc.path, strings.Join(msgs, "; "))
		}
	}
}

func TestModuleClean(t *testing.T) {
	root := moduleRoot(t)
	if err := settings.CheckModule(root); err != nil {
		t.Fatal(err)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
