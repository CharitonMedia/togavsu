package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestCORE01ServicePrincipalCalls: a module token with no context token is the service
// principal (CORE-01). createEvent is marked for it and writes an audit row with source
// service; listInvoice is not and answers scope_denied. A context token still runs as the
// viewer on a marked operation.
func TestCORE01ServicePrincipalCalls(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("  - scope: events:read\n"),
			[]byte("  - scope: events:write\n    reason: Append a timeline row.\n  - scope: matters:write\n    reason: Prove unmarked writes stay closed.\n  - scope: events:read\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	matter, _ := x.matter("Estate of Service Principal", "", nil)

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]

	// Marked for service principal: append an event with no viewer.
	created := bot.must(http.StatusCreated, "POST", "/api/v1/events", map[string]any{
		"action": "matter-clock.tick", "record_type": "matter", "record_id": matter, "matter_id": matter,
		"summary": "tick",
	})
	if created.json["actor_kind"] != "module" {
		t.Fatalf("actor_kind: %v", created.json["actor_kind"])
	}
	var source string
	if err := x.app.Pool.QueryRowContext(ctx,
		`SELECT source FROM core.audit_log WHERE entity = 'event' AND entity_id = $1 ORDER BY created_at DESC LIMIT 1`,
		str(t, created.json, "id")).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "service" {
		t.Fatalf("audit source %q, want service", source)
	}

	// Unmarked: matters:read is approved, but listMatter is delegated_viewer only.
	if r := bot.do("GET", "/api/v1/matters", nil); r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("unmarked service call: %d %v", r.code, r.json)
	}
	// A junk task-authorization header must not open an unmarked write (CORE-01 fail-closed).
	junk := bot.do("POST", "/api/v1/matters", map[string]any{
		"name": "Should Fail", "opened_on": "2026-01-01", "client_contact_id": "00000000-0000-4000-8000-000000000001",
	}, modules.TaskAuthorizationHeader, "junk.not.a.grant")
	if junk.code != http.StatusForbidden || junk.json["code"] != "scope_denied" {
		t.Fatalf("junk task auth on unmarked write: %d %v", junk.code, junk.json)
	}

	// Viewer form of a marked operation still works with a context token.
	ct := x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{})
	tok := str(t, ct.json, "token")
	if r := bot.do("POST", "/api/v1/events", map[string]any{
		"action": "matter-clock.tick", "record_type": "matter", "record_id": matter, "matter_id": matter,
		"summary": "viewer tick",
	}, modules.ContextHeader, tok); r.code != http.StatusCreated {
		t.Fatalf("viewer service call: %d %s", r.code, r.body)
	}
}

// TestCORE01ServiceCallReadAudits: books profile and matchMail (service-call reads) each
// write one audit row naming the operation with source service (CORE-01 gate audit).
func TestCORE01ServiceCallReadAudits(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("permissions:\n"),
			[]byte("permissions:\n  - scope: books:read\n    reason: Read the books.\n"+
				"  - scope: contacts:read\n    reason: Match mail contacts.\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]

	countOp := func(op string) int {
		t.Helper()
		var n int
		if err := x.app.Pool.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'service_call' AND field = 'operation' AND new_value = $1 AND source = 'service' AND reason LIKE '%module=matter-clock%'`,
			op).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	beforeBooks := countOp("getBooksProfile")
	bot.must(http.StatusOK, "GET", "/api/v1/books/profile", nil)
	if got := countOp("getBooksProfile"); got != beforeBooks+1 {
		t.Fatalf("getBooksProfile audits: got %d want %d", got, beforeBooks+1)
	}

	beforeMatch := countOp("matchMail")
	bot.must(http.StatusOK, "POST", "/api/v1/mail/matches", map[string]any{"addresses": []string{"nobody@example.com"}})
	if got := countOp("matchMail"); got != beforeMatch+1 {
		t.Fatalf("matchMail audits: got %d want %d", got, beforeMatch+1)
	}
}
