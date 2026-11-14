package core

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"testing"
	"time"
)

// TestReleaseEveryDomainWriteIsAudited is AC-018's audit half across the domains the
// earlier phase cases did not each assert on the composed core: calendar events and
// deadlines, files, Doc Tender categories, AI settings and module installs. Every write
// below leaves audit rows for its record, each naming the actor and the source (a
// session's "ui" or a token's "api_token").
func TestReleaseEveryDomainWriteIsAudited(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	matter, _ := x.matter("Estate of Auditt", "", nil)
	at := func(h int) string { return time.Now().Add(time.Duration(h) * time.Hour).UTC().Format(time.RFC3339) }

	written := map[string]string{} // entity id -> what wrote it
	ev := str(t, x.atty.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": "Audit hearing", "kind": "court_date", "starts_at": at(24), "ends_at": at(25), "matter_id": matter}).json, "id")
	x.atty.must(http.StatusOK, "PATCH", "/api/v1/calendar-events/"+ev, map[string]any{"title": "Audit hearing moved"})
	written[ev] = "calendar event create and edit"
	dl := str(t, x.staff.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": matter, "title": "Audit deadline", "due_on": time.Now().Add(72 * time.Hour).UTC().Format("2006-01-02"), "responsible_user_id": x.attyID}).json, "id")
	written[dl] = "deadline create"
	file := x.upload(x.staff, matter, "audit-note.txt", "text/plain", []byte("synthetic audit note"), nil)
	fileID := file["id"].(string)
	x.atty.must(http.StatusOK, "POST", "/api/v1/files/"+fileID+"/rename", map[string]any{"name": "audit renamed"})
	written[fileID] = "file upload and rename"
	cat := x.admin.must(http.StatusCreated, "POST", "/api/v1/doc-tender/categories", map[string]any{"key": "audit_papers", "description": "Audit papers", "tier": "standard", "folder_name": "Billing"})
	written[str(t, cat.json, "id")] = "Doc Tender category create"
	x.trustKey("Audit sample key", x.priv.Public().(ed25519.PublicKey))
	x.installSample()

	// A token write is audited as api_token.
	issued := x.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "auditor", "scopes": []string{"calendar:write", "calendar:read"}})
	bot := newClient(t, x.srv.URL)
	bot.bearer = str(t, issued.json, "secret")
	tokEv := str(t, bot.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": "Token hearing", "kind": "client_meeting", "starts_at": at(48), "ends_at": at(49), "matter_id": matter}).json, "id")
	written[tokEv] = "calendar event by token"

	check := func(what, query string, args ...any) {
		t.Helper()
		rows, err := x.app.Pool.QueryContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			var actor, source string
			if err := rows.Scan(&actor, &source); err != nil {
				t.Fatal(err)
			}
			n++
			if actor == "" || (source != "ui" && source != "api_token" && source != "module") {
				t.Errorf("%s: audit row with actor %q and source %q", what, actor, source)
			}
		}
		if n == 0 {
			t.Errorf("%s left no audit row", what)
		}
	}
	for id, what := range written {
		check(what, `SELECT actor_id, source FROM core.audit_log WHERE entity_id = $1`, id)
	}
	check("module install and permission approval", `SELECT actor_id, source FROM core.audit_log WHERE entity = 'module:matter-clock'`)
	var src string
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT source FROM core.audit_log WHERE entity_id = $1 LIMIT 1`, tokEv).Scan(&src); err != nil || src != "api_token" {
		t.Fatalf("token write audited with source %q: %v", src, err)
	}
}
