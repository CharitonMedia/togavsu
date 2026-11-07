package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLedgerAppendOnly(t *testing.T) {
	dir := t.TempDir()
	l := OpenLedger(filepath.Join(dir, "ledger.jsonl"))
	l.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	if _, err := l.Append("backup", "supervisor", map[string]string{"id": "one"}); err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC) }
	if _, err := l.Append("backup", "local-api", map[string]string{"id": "two"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "\n") != 2 {
		t.Fatalf("lines = %q", raw)
	}
	if !strings.Contains(string(raw), `"id":"one"`) || !strings.Contains(string(raw), `"id":"two"`) {
		t.Fatalf("missing events: %s", raw)
	}
	info, err := os.Stat(l.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	latest, ok, err := l.Latest("backup")
	if err != nil || !ok || latest.Fields["id"] != "two" {
		t.Fatalf("latest = %+v ok=%v err=%v", latest, ok, err)
	}
}
