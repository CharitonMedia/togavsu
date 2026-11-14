package core

import (
	"net/http"
	"strings"
	"testing"
)

// TestReleaseIdentityRoutes covers the user, session, password, two-factor reset,
// recovery-code and token-detail operations the release mounts (AC-002), with their
// role and scope refusals (AC-004) and audit rows (AC-018).
func TestReleaseIdentityRoutes(t *testing.T) {
	app, srv := start(t, testDSN(t))
	admin := firstRun(t, srv.URL)

	// Admin creates, reads, edits and deactivates a user through /api/v1.
	created := admin.must(http.StatusCreated, "POST", "/api/v1/users", map[string]any{
		"name": "Sam Staff", "email": "sam@firm.example", "roles": []string{"staff"},
		"password": "synthetic-password-sam", "phone": "+1 573 555 0100", "default_hourly_rate_cents": 15000,
	})
	samID := str(t, created.json, "id")
	if created.json["active"] != true || !strings.Contains(created.body, `"staff"`) || created.json["default_hourly_rate_cents"] != float64(15000) {
		t.Fatalf("created user: %s", created.body)
	}
	if r := admin.do("POST", "/api/v1/users", map[string]any{"name": "Dup", "email": "sam@firm.example", "roles": []string{"staff"}, "password": "synthetic-password-dup"}); r.code != http.StatusConflict {
		t.Fatalf("duplicate email: %d %s", r.code, r.body)
	}
	if r := admin.do("POST", "/api/v1/users", map[string]any{"name": "Short", "email": "short@firm.example", "roles": []string{"staff"}, "password": "short"}); r.code != http.StatusBadRequest {
		t.Fatalf("short password: %d %s", r.code, r.body)
	}
	if r := admin.do("POST", "/api/v1/users", map[string]any{"name": "Bad", "email": "bad@firm.example", "roles": []string{"wizard"}, "password": "synthetic-password-bad"}); r.code != http.StatusBadRequest {
		t.Fatalf("unknown role: %d %s", r.code, r.body)
	}
	list := admin.must(http.StatusOK, "GET", "/api/v1/users", nil)
	if !strings.Contains(list.body, "sam@firm.example") || !strings.Contains(list.body, "avery@firm.example") {
		t.Fatal(list.body)
	}
	got := admin.must(http.StatusOK, "GET", "/api/v1/users/"+samID, nil)
	if str(t, got.json, "email") != "sam@firm.example" || str(t, got.json, "phone") != "+1 573 555 0100" {
		t.Fatal(got.body)
	}
	patched := admin.must(http.StatusOK, "PATCH", "/api/v1/users/"+samID, map[string]any{"name": "Samantha Staff", "roles": []string{"staff", "attorney"}})
	if str(t, patched.json, "name") != "Samantha Staff" || !strings.Contains(patched.body, `"attorney"`) {
		t.Fatal(patched.body)
	}
	hist := admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=user&entity_id="+samID, nil)
	if !strings.Contains(hist.body, "Samantha Staff") || !strings.Contains(hist.body, `"source":"ui"`) {
		t.Fatalf("user edit not audited with old and new values: %s", hist.body)
	}
	if r := admin.do("POST", "/api/v1/users/"+samID+"x/deactivate", nil); r.code != http.StatusNotFound {
		t.Fatalf("unknown user deactivated: %d", r.code)
	}

	// A staff user can't list, create or edit users; their own profile is theirs.
	staff, staffID := enrollUser(t, app, srv.URL, admin, "Tess", "tess@firm.example", "staff")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/users"}, {"GET", "/api/v1/users/" + samID}, {"POST", "/api/v1/users"},
		{"PATCH", "/api/v1/users/" + samID}, {"POST", "/api/v1/users/" + samID + "/deactivate"},
	} {
		if r := staff.do(c.method, c.path, map[string]any{"name": "x"}); r.code != http.StatusForbidden {
			t.Errorf("staff %s %s: %d %s", c.method, c.path, r.code, r.body)
		}
	}
	me := staff.must(http.StatusOK, "PATCH", "/api/v1/me", map[string]any{"name": "Tess Staff", "initials": "TS"})
	if str(t, me.json, "name") != "Tess Staff" || str(t, me.json, "initials") != "TS" {
		t.Fatal(me.body)
	}
	if r := staff.do("PATCH", "/api/v1/me", map[string]any{"initials": "T5"}); r.code != http.StatusBadRequest {
		t.Fatalf("bad initials: %d", r.code)
	}

	// Sessions: the staff user sees only their own; the admin can end one.
	sessions := staff.must(http.StatusOK, "GET", "/api/v1/sessions", nil)
	data, _ := sessions.json["data"].([]any)
	if len(data) != 1 || strings.Contains(sessions.body, "avery") {
		t.Fatalf("staff sessions: %s", sessions.body)
	}
	row := data[0].(map[string]any)
	if row["current"] != true || row["user_id"] != string(staffID) || row["created_at"] == "" {
		t.Fatal(sessions.body)
	}
	sessionID := row["id"].(string)
	adminSessions := admin.must(http.StatusOK, "GET", "/api/v1/sessions", nil)
	if !strings.Contains(adminSessions.body, sessionID) {
		t.Fatalf("admin does not see every session: %s", adminSessions.body)
	}
	if r := staff.do("DELETE", "/api/v1/sessions/"+str(t, adminSessions.json["data"].([]any)[0].(map[string]any), "id"), nil); r.code != http.StatusNotFound && r.code != http.StatusNoContent {
		t.Fatalf("staff ending a session: %d", r.code)
	}

	// Password change: a wrong current password is refused; the right one works and the
	// new password signs in.
	if r := staff.do("POST", "/api/v1/me/password", map[string]any{"current_password": "wrong-password-x", "new_password": "synthetic-new-password-tess"}); r.code < 400 || r.code == http.StatusInternalServerError {
		t.Fatalf("wrong current password: %d %s", r.code, r.body)
	}
	staff.must(http.StatusNoContent, "POST", "/api/v1/me/password", map[string]any{"current_password": "synthetic-password-Tess", "new_password": "synthetic-new-password-tess"})
	if r := newClient(t, srv.URL).do("POST", "/api/v1/auth/login", map[string]string{"email": "tess@firm.example", "password": "synthetic-new-password-tess"}); r.code != http.StatusOK {
		t.Fatalf("new password does not sign in: %d %s", r.code, r.body)
	}

	// Recovery codes and two-factor reset need a proof of an existing factor.
	if r := staff.do("POST", "/api/v1/me/recovery-codes", map[string]any{}); r.code != http.StatusBadRequest {
		t.Fatalf("no factor: %d", r.code)
	}
	if r := staff.do("POST", "/api/v1/me/recovery-codes", map[string]any{"current_password": "nope-nope-nope"}); r.code < 400 || r.code == http.StatusInternalServerError {
		t.Fatalf("wrong factor: %d", r.code)
	}
	codes := staff.must(http.StatusOK, "POST", "/api/v1/me/recovery-codes", map[string]any{"current_password": "synthetic-new-password-tess"})
	if c, _ := codes.json["codes"].([]any); len(c) == 0 {
		t.Fatal(codes.body)
	}
	reset := staff.must(http.StatusOK, "POST", "/api/v1/me/totp/reset", map[string]any{"current_password": "synthetic-new-password-tess"})
	if str(t, reset.json, "status") != "totp_reset_pending" {
		t.Fatal(reset.body)
	}
	if r := staff.do("POST", "/api/v1/me/totp/reset/confirm", map[string]any{"enrollment_token": str(t, reset.json, "enrollment_token"), "code": "000000"}); r.code < 400 || r.code == http.StatusInternalServerError {
		t.Fatalf("wrong reset code: %d", r.code)
	}

	// Token detail: the owner reads their token; a token needs tokens:read to read it.
	issued := admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "detail", "scopes": []string{"tokens:read", "users:read"}})
	tokenID := str(t, issued.json, "token", "id")
	if r := admin.must(http.StatusOK, "GET", "/api/v1/api-tokens/"+tokenID, nil); str(t, r.json, "name") != "detail" || strings.Contains(r.body, str(t, issued.json, "secret")) {
		t.Fatal(r.body)
	}
	bot := newClient(t, srv.URL)
	bot.bearer = str(t, issued.json, "secret")
	bot.must(http.StatusOK, "GET", "/api/v1/api-tokens/"+tokenID, nil)
	bot.must(http.StatusOK, "GET", "/api/v1/users", nil)
	if r := bot.do("POST", "/api/v1/users", map[string]any{"name": "Bot", "email": "bot@firm.example", "roles": []string{"staff"}, "password": "synthetic-password-bot"}); r.code != http.StatusForbidden {
		t.Fatalf("users:read token created a user: %d", r.code)
	}
	if r := bot.do("GET", "/api/v1/sessions", nil); r.code != http.StatusForbidden {
		t.Fatalf("token without sessions:read listed sessions: %d", r.code)
	}

	// The same operations over /mcp.
	call := mcpCall(admin, toolName(t, "GET", "/api/v1/users/{user_id}"), map[string]any{"path": map[string]any{"user_id": samID}})
	if !strings.Contains(call.body, "Samantha Staff") || strings.Contains(call.body, `"isError":true`) {
		t.Fatal(call.body)
	}
	if call := mcpCall(staff, toolName(t, "GET", "/api/v1/users"), map[string]any{}); !strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("staff listed users over /mcp: %s", call.body)
	}

	// Password reset answers 202 for known and unknown addresses alike. With no SMTP
	// settings the mail fails quietly; a bogus token is refused.
	anon := newClient(t, srv.URL)
	anon.must(http.StatusAccepted, "POST", "/api/v1/auth/password-resets", map[string]any{"email": "sam@firm.example"})
	anon.must(http.StatusAccepted, "POST", "/api/v1/auth/password-resets", map[string]any{"email": "nobody@firm.example"})
	if r := anon.do("POST", "/api/v1/auth/password-resets/confirm", map[string]any{"token": "bogus", "new_password": "synthetic-password-new"}); r.code < 400 || r.code == http.StatusInternalServerError {
		t.Fatalf("bogus reset token: %d %s", r.code, r.body)
	}

	// Deactivation ends the user's access on the next request; the last admin stays.
	admin.must(http.StatusOK, "POST", "/api/v1/users/"+string(staffID)+"/deactivate", nil)
	if r := staff.do("GET", "/api/v1/me", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("deactivated user still signed in: %d", r.code)
	}
	if r := admin.do("POST", "/api/v1/users/"+str(t, admin.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")+"/deactivate", nil); r.code != http.StatusBadRequest {
		t.Fatalf("admin deactivated themself: %d", r.code)
	}
}
