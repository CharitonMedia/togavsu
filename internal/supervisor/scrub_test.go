package supervisor

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestScrubDatabaseURL(t *testing.T) {
	in := "pg_dump: postgres://togavsu:synthetic-postgres-password@127.0.0.1:5432/togavsu?sslmode=disable failed"
	got := Scrub(in)
	if strings.Contains(got, "synthetic-postgres-password") {
		t.Fatalf("password leaked: %s", got)
	}
	if !strings.Contains(got, "togavsu:***@127.0.0.1") {
		t.Fatalf("got %s", got)
	}
}

func TestScrubHandlerDropsToken(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(ScrubHandler{Next: slog.NewTextHandler(&buf, nil)})
	log.Info("request", "authorization", "Bearer super-secret-token", "path", "/v1/backups")
	if strings.Contains(buf.String(), "super-secret-token") {
		t.Fatalf("token leaked: %s", buf.String())
	}
}
