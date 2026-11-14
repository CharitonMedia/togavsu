package db

import (
	"strings"
	"testing"
)

// TestWrapLedgerMigration: a migration that names an append-only table gets the handoff
// and the seal inside its own Up section, before any Down section; one that names none
// comes back unchanged, so it takes no lock on the ledgers.
func TestWrapLedgerMigration(t *testing.T) {
	plain := "-- +goose Up\nCREATE TABLE notes (id TEXT);\n"
	if got := wrapLedgerMigration(plain, "togavsu_app"); got != plain {
		t.Fatalf("a migration naming no ledger was wrapped:\n%s", got)
	}
	src := "-- +goose Up\nALTER TABLE audit_log ADD COLUMN x TEXT;\n-- +goose Down\nALTER TABLE audit_log DROP COLUMN x;\n"
	got := wrapLedgerMigration(src, "togavsu_app")
	up := strings.Index(got, "-- +goose Up")
	unseal := strings.Index(got, "$unseal$")
	stmt := strings.Index(got, "ADD COLUMN x")
	seal := strings.Index(got, "OWNER TO "+LedgerOwner)
	down := strings.Index(got, "-- +goose Down")
	if !(up >= 0 && up < unseal && unseal < stmt && stmt < seal && seal < down) {
		t.Fatalf("wrapped order up=%d unseal=%d stmt=%d seal=%d down=%d:\n%s", up, unseal, stmt, seal, down, got)
	}
	if !strings.Contains(got, "ARRAY['audit_log']") || strings.Contains(got, "ARRAY['audit_log', 'trust_ledger']") {
		t.Fatalf("handoff should name only audit_log:\n%s", got)
	}
	// Every role switch ends back on the application role, so goose's version row is
	// written as it, and the Down section is untouched.
	if !strings.HasSuffix(got, "-- +goose Down\nALTER TABLE audit_log DROP COLUMN x;\n") {
		t.Fatalf("down section changed:\n%s", got)
	}
	upPart := got[:down]
	if strings.LastIndex(upPart, `SET ROLE "togavsu_app"`) < strings.LastIndex(upPart, "RESET ROLE") {
		t.Fatalf("the Up section does not end as the application role:\n%s", upPart)
	}
	// A migration with no annotation is wrapped in the Up section goose adds.
	bare := wrapLedgerMigration("INSERT INTO events (id) VALUES ('x');", "togavsu_app")
	if !strings.HasPrefix(bare, "-- +goose Up\n") || !strings.Contains(bare, "ARRAY['events']") {
		t.Fatalf("bare migration:\n%s", bare)
	}
}
