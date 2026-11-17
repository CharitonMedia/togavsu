package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

// CORE-36 contributor gap tests: each closed a row that failed before the fix.

func TestCORE36ProxyForwardsContextToken(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	var saw string
	x.rt.Handle("matter-clock", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Header.Get(modules.ContextHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	pkg := x.build(nil)
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	ct := x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{})
	tok := str(t, ct.json, "token")
	req, _ := http.NewRequest("GET", x.srv.URL+"/m/matter-clock/healthz", nil)
	req.Header.Set(modules.ContextHeader, tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if saw != tok {
		t.Fatalf("proxy dropped context token: got %q", saw)
	}
}

func TestCORE36MeAcceptsContextToken(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("permissions:\n"),
			[]byte("permissions:\n  - scope: profile:read\n    reason: Read viewer profile.\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	ct := x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{})
	tok := str(t, ct.json, "token")
	me := bot.do("GET", "/api/v1/me", nil, modules.ContextHeader, tok)
	if me.code != http.StatusOK {
		t.Fatalf("me: %d %s", me.code, me.body)
	}
	want := str(t, x.staff.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	if str(t, me.json, "id") != want {
		t.Fatalf("me as viewer: %s want staff %s", me.body, want)
	}
}

func TestCORE36MissingScopeIsScopeDenied(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(nil) // matter-clock lacks contacts:read
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	ct := x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{})
	tok := str(t, ct.json, "token")
	r := bot.do("GET", "/api/v1/contacts", nil, modules.ContextHeader, tok)
	if r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("missing scope: %d %v", r.code, r.json)
	}
	if msg, _ := r.json["message"].(string); !strings.Contains(msg, "contacts:read") {
		t.Fatalf("message should name scope: %v", r.json)
	}
}

func TestCORE36NotesWriteCoreNotes(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	matter, _ := x.matter("Notes Projection Estate", "", nil)
	created := x.staff.must(http.StatusCreated, "POST", "/api/v1/notes", map[string]any{
		"matter_id": matter, "title": "Projected note", "body": "body text",
	})
	id := str(t, created.json, "id")
	var title string
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT title FROM core.notes WHERE id = $1`, id).Scan(&title); err != nil {
		t.Fatalf("core.notes empty after create: %v", err)
	}
	if title != "Projected note" {
		t.Fatalf("title %q", title)
	}
}

func TestCORE36GetConflictCheckServiceCall(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("permissions:\n"),
			[]byte("permissions:\n  - scope: conflicts:read\n    reason: Read conflict checks.\n"+
				"  - scope: conflicts:decide\n    reason: Decide.\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	matter, _ := x.matter("Conflict Service Estate", "conflict.svc@client.example", nil)
	run := x.atty.must(http.StatusCreated, "POST", "/api/v1/matters/"+matter+"/conflict-checks", map[string]any{})
	runID := str(t, run.json, "id")

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	got := bot.must(http.StatusOK, "GET", "/api/v1/conflict-checks/"+runID, nil)
	if str(t, got.json, "id") != runID {
		t.Fatalf("getConflictCheck: %s", got.body)
	}
}

func TestCORE36ConflictCheckDecidedEvent(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	matter, clientID := x.matter("Conflict Event Estate", "conflict.evt@client.example", nil)
	// Force a hit: conflict record matches the matter client's name.
	client := x.atty.must(http.StatusOK, "GET", "/api/v1/contacts/"+clientID, nil)
	name := str(t, client.json, "full_name")
	if name == "" {
		name = "Conflict Event Estate"
	}
	x.admin.must(http.StatusCreated, "POST", "/api/v1/conflict-records", map[string]any{
		"name": name, "relationship": "other", "scope": "matter", "reason": "prior",
	})
	run := x.atty.must(http.StatusCreated, "POST", "/api/v1/matters/"+matter+"/conflict-checks", map[string]any{})
	hits, _ := run.json["hits"].([]any)
	if len(hits) == 0 {
		t.Fatalf("expected hits: %s", run.body)
	}
	hit := hits[0].(map[string]any)
	hitID := str(t, hit, "id")
	runID := str(t, run.json, "id")
	x.atty.must(http.StatusOK, "POST", "/api/v1/conflict-checks/"+runID+"/decisions", map[string]any{
		"hit_id": hitID, "decision": "clear", "reason": "not a conflict",
	})
	var action string
	if err := x.app.Pool.QueryRowContext(ctx,
		`SELECT action FROM core.events WHERE matter_id = $1 AND action = 'conflict_check.decided' ORDER BY created_at DESC LIMIT 1`,
		matter).Scan(&action); err != nil {
		t.Fatalf("conflict_check.decided missing: %v", err)
	}
}
