package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpCommandScrubsPassword(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pg_dump")
	script := "#!/bin/sh\necho \"failed $*\" >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	err := DumpCommand(bin, "postgres://togavsu:synthetic-postgres-password@127.0.0.1/togavsu")(context.Background(), ioDiscard{})
	if err == nil {
		t.Fatal("expected dump failure")
	}
	if strings.Contains(err.Error(), "synthetic-postgres-password") {
		t.Fatalf("password leaked: %v", err)
	}
}

func TestQuoteTableRejectsInjection(t *testing.T) {
	if _, err := quoteTable(`core.matters;drop`); err == nil {
		t.Fatal("accepted injected table")
	}
	got, err := quoteTable("core.matters")
	if err != nil || got != `"core"."matters"` {
		t.Fatalf("%s %v", got, err)
	}
}

func TestSwapCleanupFailureIsCommitted(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sql.log")
	script := "#!/bin/sh\ncmd=\nprev=\nfor arg in \"$@\"; do\n  if [ \"$prev\" = \"--command\" ]; then cmd=$arg; fi\n  prev=$arg\ndone\nprintf '%s\\n' \"$cmd\" >> " + logPath + "\ncase \"$cmd\" in\n  \"DROP DATABASE\"*) echo drop >&2; exit 1 ;;\nesac\nexit 0\n"
	bin := filepath.Join(dir, "psql")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	err := swapDatabase(context.Background(), bin, "postgres://togavsu@127.0.0.1/postgres", "togavsu", "togavsu_restore_ab", "ab")
	if !errors.Is(err, ErrRestoreCleanup) {
		t.Fatalf("got %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(raw)
	if !strings.Contains(log, "ALTER DATABASE togavsu RENAME TO togavsu_replaced_ab") || !strings.Contains(log, "ALTER DATABASE togavsu_restore_ab RENAME TO togavsu") {
		t.Fatalf("renames did not both commit before the drop failed: %s", log)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
