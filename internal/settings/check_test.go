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
		{"cmd/core/alias.go", "package main\nimport env \"os\"\nfunc f(){ env.Getenv(\"X\") }\n", true},
		{"cmd/core/syscall.go", "package main\nimport sys \"syscall\"\nfunc f(){ sys.Getenv(\"X\") }\n", true},
		{"internal/bootstrap/alias.go", "package bootstrap\nimport env \"os\"\nfunc f(){ env.LookupEnv(\"X\") }\n", false},
		{"internal/bootstrap/config.go", "package bootstrap\nimport \"os\"\nfunc f(){ os.Getenv(\"TOGAVSU_DATABASE_URL\") }\n", false},
		{"internal/bootstrap/extra/x.go", "package extra\nimport \"os\"\nfunc f(){ os.LookupEnv(\"X\") }\n", true},
		{"internal/practice/time.go", "package practice\nimport \"time\"\nfunc f() time.Duration { return 5 * time.Minute }\n", true},
		{"internal/settings/definitions.go", "package settings\nimport \"time\"\nfunc f() time.Duration { return time.Hour }\n", false},
		{"cmd/core/clock.go", "package main\nimport clk \"time\"\nfunc f() time.Duration { return 5 * clk.Minute }\n", true},
		{"cmd/core/parse.go", "package main\nimport clk \"time\"\nfunc f() { clk.ParseDuration(\"15m\") }\n", true},
		{"cmd/core/page.go", "package main\nconst notesPage = 25\nfunc f() int { activityPage := 100; return activityPage }\n", true},
		{"cmd/core/filter.go", "package main\ntype Filter struct{ Limit int }\nfunc f() Filter { return Filter{Limit: 25} }\n", true},
		{"cmd/core/ret.go", "package main\nfunc f() int { return 25 }\n", true},
		{"cmd/core/paren.go", "package main\nfunc f() int { return (25) }\n", true},
		{"cmd/core/call.go", "package main\nfunc g(int) {}\nfunc f() { g(25) }\n", true},
		{"cmd/core/slice.go", "package main\nfunc f() []int { return []int{25} }\n", true},
		{"cmd/core/send.go", "package main\nfunc f(ch chan int) { ch <- 25 }\n", true},
		{"internal/billing/scale.go", "package billing\nfunc f(n int) int { return n * 100 }\n", false},
		{"internal/db/ranges.go", "package db\nfunc MigrationRanges() int { return struct{ Min int }{Min: 100}.Min }\n", false},
		{"internal/db/ranges.go", "package db\nfunc Extra() int { return struct{ Min int }{Min: 100}.Min }\n", true},
		{"internal/practice/ranges.go", "package practice\nfunc MigrationRanges() int { return struct{ Min int }{Min: 100}.Min }\n", true},
		{"internal/db/other.go", "package db\nvar bands = []struct{ Min int }{{Min: 4}}\n", true},
		{"internal/domain/notdefault.go", "package domain\nimport \"time\"\nfunc f() time.Duration { return time.Hour }\n", true},
		{"internal/domain/defaults.go", "package domain\nconst title = \"AVAILABLE DATES FOR HEARING\"\n", false},
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

func TestAC030Fixtures(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, "internal", "settings", "testdata", "ac030")
	negative := []struct {
		file   string
		number string
	}{
		{"layouts.go", "10"},
		{"filter.go", "8"},
		{"lockout.go", "5"},
		{"lockout.go", "8"},
		{"calls.go", "6"},
		{"calls.go", "10"},
		{"calls.go", "15"},
		{"not_default.go", "4"},
		{"not_default.go", "11"},
		{"not_default.go", "9"},
		{"not_default.go", "7"},
		{"zero_config.go", "0"},
		{"zero_config.go", "1"},
	}
	for _, tc := range negative {
		src := readFixture(t, filepath.Join(dir, tc.file))
		msgs := settings.ScanSource(filepath.Join("internal", "practice", tc.file), src)
		joined := strings.Join(msgs, "\n")
		if !strings.Contains(joined, "tunable number "+tc.number) {
			t.Errorf("%s: expected tunable number %s, got %s", tc.file, tc.number, joined)
		}
	}
	for _, file := range []string{"scale.go", "idiom.go"} {
		src := readFixture(t, filepath.Join(dir, file))
		msgs := settings.ScanSource(filepath.Join("internal", "practice", file), src)
		if len(msgs) != 0 {
			t.Errorf("%s: %s", file, strings.Join(msgs, "; "))
		}
	}
}

func TestFixedNumberExclusions(t *testing.T) {
	clean := []struct {
		path string
		src  string
	}{
		{"internal/db/migrate.go", "package db\nfunc LoadFS(version int) bool { return version < 1 }\n"},
		{"internal/bootstrap/config.go", "package bootstrap\nfunc RunFrom() int { return 2 }\n"},
		{"internal/domain/money.go", "package domain\nfunc combineCents(frac int) bool { return frac > 8 || frac == 4 }\n"},
		{"internal/domain/id.go", "package domain\nfunc ParseID(dec []byte) bool { return dec[8]>>6 != 2 }\n"},
		{"internal/db/goose_store.go", "package db\nfunc ensureBaseline() int { return struct{ Version int }{Version: 0}.Version }\n"},
		{"internal/db/seed.go", "package db\nfunc insertSynthetic() int { const maxSavedLayouts = 11; return maxSavedLayouts }\n"},
		{"internal/settings/registry.go", "package settings\nfunc validateSpecial(n float64) bool { return n > 1 || n < 0 || n > 365 }\n"},
		{"internal/settings/registry.go", "package settings\nimport \"strconv\"\nfunc f(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }\n"},
	}
	for _, tc := range clean {
		if msgs := settings.ScanSource(tc.path, tc.src); len(msgs) != 0 {
			t.Errorf("%s should be a fixed rule: %s", tc.path, strings.Join(msgs, "; "))
		}
	}
	flagged := []struct {
		path string
		src  string
		num  string
	}{
		{"internal/practice/version.go", "package practice\nfunc LoadFS(version int) bool { return version < 1 }\n", "1"},
		{"internal/db/migrate.go", "package db\nfunc Extra(version int) bool { return version < 1 }\n", "1"},
		{"internal/db/migrate.go", "package db\nconst maxSavedLayouts = 11\n", "11"},
		{"internal/bootstrap/config.go", "package bootstrap\nfunc f() int { return 2 }\n", "2"},
		{"internal/domain/money.go", "package domain\nfunc f(frac int) bool { return frac > 4 }\n", "4"},
		{"internal/domain/id.go", "package domain\nfunc f(dec []byte) bool { return dec[8]>>6 != 2 }\n", "2"},
		{"internal/db/seed.go", "package db\nfunc extra() int { const maxSavedLayouts = 11; return maxSavedLayouts }\n", "11"},
		{"internal/settings/registry.go", "package settings\nfunc validateSpecial() int { const maxSavedLayouts = 11; return maxSavedLayouts }\n", "11"},
		{"internal/settings/registry.go", "package settings\nfunc other(n float64) bool { return n > 1 }\n", "1"},
		{"internal/settings/check.go", "package settings\nfunc extra(failures int) bool { return failures >= 4 }\n", "4"},
		{"internal/practice/markup.go", "package practice\nconst markup = 0\n", "0"},
	}
	for _, tc := range flagged {
		joined := strings.Join(settings.ScanSource(tc.path, tc.src), "\n")
		if !strings.Contains(joined, "tunable number "+tc.num) {
			t.Errorf("%s: expected tunable number %s, got %s", tc.path, tc.num, joined)
		}
	}
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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

// AC-030 checks the core; a nested or similarly named core directory must
// not inherit the separate supervisor executable's module boundary.
func TestModuleSupervisorBoundary(t *testing.T) {
	cases := []struct {
		path string
		bad  bool
	}{
		{"cmd/supervisor/config.go", false},
		{"internal/supervisor/config.go", false},
		{"internal/supervisor/nested/config.go", false},
		{"internal/practice/internal/supervisor/config.go", true},
		{"other/cmd/supervisor/config.go", true},
		{"cmd/supervisor-extra/config.go", true},
		{"internal/supervisor-extra/config.go", true},
		{"cmd/core/config.go", true},
		{"internal/practice/config.go", true},
	}
	const source = "package sample\nimport \"os\"\nfunc config() string { return os.Getenv(\"EXTRA_CONFIG\") }\n"
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(tc.path))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := settings.CheckModule(root); (err != nil) != tc.bad {
				t.Fatalf("CheckModule error = %v; want violation %v", err, tc.bad)
			}
			// Explicit single-file scanning remains available for every package.
			if len(settings.ScanSource(path, source)) == 0 {
				t.Fatal("direct source scan lost environment violation")
			}
		})
	}
}
