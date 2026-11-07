package supervisor

import (
	"context"
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

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
