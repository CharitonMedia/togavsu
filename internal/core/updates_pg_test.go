package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestReleaseUpdatesRoutes covers GET and POST /api/v1/updates (PRD 6 and the Updates tab,
// AC-002): the history comes from the supervisor's ledger as UpdateRecords with stable
// ids, Update now reaches the supervisor as a forced update and answers 202 at once, and
// both are admin only, audited, and on /mcp.
func TestReleaseUpdatesRoutes(t *testing.T) {
	var mu sync.Mutex
	forced := make(chan bool, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/restores/hold", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"hold": false})
	})
	mux.HandleFunc("GET /v1/updates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"updates": []any{
			map[string]any{"time": "2026-11-10T08:00:00Z", "kind": "update", "actor": "supervisor", "fields": map[string]string{"status": "applied", "from": "core@sha256:aa", "to": "core@sha256:bb", "migrated": "true"}},
			map[string]any{"time": "2026-11-11T08:00:00Z", "kind": "update", "actor": "supervisor", "fields": map[string]string{"status": "refused", "reason": "signature", "image": "core@sha256:cc"}},
		}})
	})
	mux.HandleFunc("POST /v1/updates", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]bool
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		forced <- body["force"]
		writeJSON(w, http.StatusOK, map[string]any{"status": "current"})
	})
	sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+supSecret {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		mux.ServeHTTP(w, r)
	}))
	defer sup.Close()
	dsn := testDSN(t)
	app, err := Open(context.Background(), Config{DatabaseURL: dsn, Key: testKey, Domain: "firm.example", DataDir: dataDir(t, dsn),
		SupervisorURL: sup.URL, SupervisorSecret: supSecret, NoBackground: true, MailEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler())
	defer func() { srv.Close(); _ = app.Close() }()
	admin := firstRun(t, srv.URL)
	staff, _ := enrollUser(t, app, srv.URL, admin, "Uma", "uma@firm.example", "staff")

	list := admin.must(http.StatusOK, "GET", "/api/v1/updates", nil)
	rows := items(t, list)
	if len(rows) != 2 || rows[0]["status"] != "aborted" || rows[0]["reason"] != "signature" || rows[0]["to_image"] != "core@sha256:cc" ||
		rows[1]["status"] != "completed" || rows[1]["from_image"] != "core@sha256:aa" || rows[1]["to_image"] != "core@sha256:bb" {
		t.Fatalf("update history: %s", list.body)
	}
	again := items(t, admin.must(http.StatusOK, "GET", "/api/v1/updates", nil))
	if again[0]["id"] != rows[0]["id"] || !strings.HasPrefix(rows[0]["id"].(string)[14:], "7") && rows[0]["id"].(string)[14] != '7' {
		t.Fatalf("update ids are not stable UUIDv7s: %v %v", rows[0]["id"], again[0]["id"])
	}
	for _, c := range []struct{ method, path string }{{"GET", "/api/v1/updates"}, {"POST", "/api/v1/updates"}} {
		if r := staff.do(c.method, c.path, map[string]any{}); r.code != http.StatusForbidden {
			t.Errorf("staff %s %s: %d", c.method, c.path, r.code)
		}
	}
	started := admin.must(http.StatusAccepted, "POST", "/api/v1/updates", map[string]any{})
	if started.json["status"] != "running" {
		t.Fatal(started.body)
	}
	select {
	case f := <-forced:
		if !f {
			t.Fatal("update now did not ask for an update outside the window")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("update now never reached the supervisor")
	}
	if r := auditFields(t, admin, "update", str(t, started.json, "id")); !strings.Contains(r, "update now") {
		t.Fatalf("update now not audited: %s", r)
	}
	if call := mcpCall(admin, toolName(t, "GET", "/api/v1/updates"), map[string]any{}); !strings.Contains(call.body, "core@sha256:bb") || strings.Contains(call.body, `"isError":true`) {
		t.Fatal(call.body)
	}
}
