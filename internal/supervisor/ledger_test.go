package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLedgerTornTailSurvivesRestartAndCorruptLineFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l := OpenLedger(path)
	l.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	if _, err := l.Append("backup", "supervisor", map[string]string{"id": "kept"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"kind":"torn"`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	reopened := OpenLedger(path)
	got, err := reopened.List("")
	if err != nil || len(got) != 1 || got[0].Fields["id"] != "kept" {
		t.Fatalf("restart read %+v %v", got, err)
	}
	if _, err := reopened.Append("update", "supervisor", map[string]string{"id": "next"}); err != nil {
		t.Fatal(err)
	}
	got, err = OpenLedger(path).List("")
	if err != nil || len(got) != 2 || got[1].Kind != "update" {
		t.Fatalf("after repair %+v %v", got, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), "torn") {
		t.Fatalf("torn tail remained: %s %v", raw, err)
	}
	if err := os.WriteFile(path, append(raw, []byte("not-json\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLedger(path).List(""); !errors.Is(err, errLedgerCorrupt) {
		t.Fatalf("corrupt committed record: %v", err)
	}
	if _, err := OpenLedger(path).Append("backup", "supervisor", nil); !errors.Is(err, errLedgerCorrupt) {
		t.Fatalf("append hid corruption: %v", err)
	}
}

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
