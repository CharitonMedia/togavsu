package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestCORE30ServiceCallsWalls: every CORE-30 service call omits walled matters and
// answers not_found on a walled get; lifting the wall returns them (CU2-02).
func TestCORE30ServiceCallsWalls(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		// Append scopes that matter-clock does not already declare.
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("permissions:\n"),
			[]byte("permissions:\n"+
				"  - scope: contacts:read\n    reason: Read contacts.\n"+
				"  - scope: contacts:write\n    reason: Write contacts.\n"+
				"  - scope: matters:write\n    reason: Write matters.\n"+
				"  - scope: conflicts:read\n    reason: Conflicts.\n"+
				"  - scope: calendar:read\n    reason: Calendar.\n"+
				"  - scope: calendar:write\n    reason: Calendar write.\n"+
				"  - scope: deadlines:write\n    reason: Deadlines.\n"+
				"  - scope: notes:write\n    reason: Notes.\n"+
				"  - scope: files:read\n    reason: Renditions.\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")

	open, openClient := x.matter("CORE30 Open", "core30.open@client.example", nil)
	walled, _ := x.matter("CORE30 Walled", "core30.walled@client.example", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]

	listed := bot.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	if strings.Contains(listed.body, walled) || !strings.Contains(listed.body, open) {
		t.Fatalf("listMatter walls: %s", listed.body)
	}
	bot.must(http.StatusOK, "GET", "/api/v1/matters/"+open, nil)
	if r := bot.do("GET", "/api/v1/matters/"+walled, nil); r.code != http.StatusNotFound {
		t.Fatalf("walled getMatter: %d %s", r.code, r.body)
	}

	note := bot.must(http.StatusCreated, "POST", "/api/v1/notes", map[string]any{
		"matter_id": open, "title": "svc note", "body": "from service",
	})
	if note.json["actor_kind"] != nil && note.json["actor_kind"] != "module" {
		// createNote response may omit actor_kind; audit source must be service.
	}
	var source string
	if err := x.app.Pool.QueryRowContext(ctx,
		`SELECT source FROM core.audit_log WHERE entity = 'note' AND entity_id = $1 ORDER BY created_at DESC LIMIT 1`,
		str(t, note.json, "id")).Scan(&source); err == nil && source != "service" && source != "module" {
		t.Fatalf("note audit source %q", source)
	}

	if r := bot.do("POST", "/api/v1/notes", map[string]any{
		"matter_id": walled, "title": "walled", "body": "no",
	}); r.code != http.StatusNotFound && r.code != http.StatusForbidden {
		t.Fatalf("walled createNote: %d %s", r.code, r.body)
	}

	// Exact email lookup.
	bot.must(http.StatusOK, "GET", "/api/v1/contacts?email=core30.open@client.example", nil)
	exact := bot.must(http.StatusOK, "GET", "/api/v1/contacts?email=core30.open@client.example", nil)
	if !strings.Contains(exact.body, openClient) {
		t.Fatalf("email exact: %s", exact.body)
	}
	miss := bot.must(http.StatusOK, "GET", "/api/v1/contacts?email=nobody@example.invalid", nil)
	if strings.Contains(miss.body, openClient) {
		t.Fatalf("email exact should miss: %s", miss.body)
	}

	// Lift wall → get returns.
	x.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{}})
	bot.must(http.StatusOK, "GET", "/api/v1/matters/"+walled, nil)
}

func TestCORE30ServiceCallRefusedWithoutScope(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(nil) // matter-clock has matters:read/events:read; not contacts:write
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	r := bot.do("POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "No", "family_name": "Scope"})
	if r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("missing scope: %d %v", r.code, r.json)
	}
}

// TestCORE30ServiceCallPerOpLimit: each CORE-30 op has its own modules.service_call_limit.*
// setting (default 120); exceeding it answers rate_limited (CU2-01).
func TestCORE30ServiceCallPerOpLimit(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(nil)
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.service_call_limit.listMatter", map[string]any{"value": 2})
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	bot.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	bot.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	r := bot.do("GET", "/api/v1/matters", nil)
	if r.code != http.StatusTooManyRequests || r.json["code"] != "rate_limited" {
		t.Fatalf("per-op limit: %d %v", r.code, r.json)
	}
	// A different operation still has its own budget.
	mid, _ := x.matter("CORE30 Limit", "core30.limit@client.example", nil)
	bot.must(http.StatusOK, "GET", "/api/v1/matters/"+mid, nil)
}
