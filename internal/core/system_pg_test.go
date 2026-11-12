package core

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// TestReleaseSystemNotificationsAndMail covers the seat count, module and admin
// notifications, the SMTP password and test send, and password-reset delivery by system
// mail (AC-002), with their refusals.
func TestReleaseSystemNotificationsAndMail(t *testing.T) {
	app, srv := start(t, testDSN(t))
	admin := firstRun(t, srv.URL)
	staff, staffID := enrollUser(t, app, srv.URL, admin, "Nia", "nia@firm.example", "staff")
	admin.must(http.StatusCreated, "POST", "/api/v1/users", map[string]any{"name": "Cal Client", "email": "cal@firm.example", "roles": []string{"client"}, "password": "synthetic-password-cal"})
	_, attorneyID := enrollUser(t, app, srv.URL, admin, "Ari", "ari@firm.example", "attorney", "admin")

	// Seats: three staff-app users, one client-only user; staff can't read it.
	seats := admin.must(http.StatusOK, "GET", "/api/v1/system/seats", nil)
	if seats.json["active_seats"] != float64(3) {
		t.Fatalf("seats: %s", seats.body)
	}
	by, _ := seats.json["by_role"].(map[string]any)
	if by["client"] != float64(1) || by["admin"] != float64(2) || by["staff"] != float64(1) {
		t.Fatalf("by_role: %s", seats.body)
	}
	if r := staff.do("GET", "/api/v1/system/seats", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff read seats: %d", r.code)
	}
	admin.must(http.StatusOK, "POST", "/api/v1/users/"+string(attorneyID)+"/deactivate", nil)
	if r := admin.must(http.StatusOK, "GET", "/api/v1/system/seats", nil); r.json["active_seats"] != float64(2) {
		t.Fatalf("deactivated user still a seat: %s", r.body)
	}

	// Notifications: an admin (or a module) sends; the recipient reads, gets and
	// deletes their own; nobody else sees it.
	if r := staff.do("POST", "/api/v1/notifications", map[string]any{"user_id": staffID, "kind": "x", "title": "self", "link": "/"}); r.code != http.StatusForbidden {
		t.Fatalf("staff sent a notification: %d %s", r.code, r.body)
	}
	for _, link := range []string{"https://evil.example/", "//evil.example", "javascript:alert(1)", ""} {
		if r := admin.do("POST", "/api/v1/notifications", map[string]any{"user_id": staffID, "kind": "module.alert", "title": "t", "link": link}); r.code != http.StatusBadRequest {
			t.Errorf("link %q: %d", link, r.code)
		}
	}
	if r := admin.do("POST", "/api/v1/notifications", map[string]any{"user_id": attorneyID, "kind": "k", "title": "t", "link": "/"}); r.code != http.StatusBadRequest {
		t.Fatalf("notification to an inactive user: %d", r.code)
	}
	sent := admin.must(http.StatusCreated, "POST", "/api/v1/notifications", map[string]any{"user_id": staffID, "kind": "module.alert", "title": "Synthetic module alert", "link": "/m/matter-clock/"})
	nid := str(t, sent.json, "id")
	list := staff.must(http.StatusOK, "GET", "/api/v1/notifications", nil)
	if !strings.Contains(list.body, "Synthetic module alert") {
		t.Fatal(list.body)
	}
	if r := admin.do("GET", "/api/v1/notifications/"+nid, nil); r.code != http.StatusNotFound {
		t.Fatalf("another user read the notification: %d", r.code)
	}
	if r := admin.do("DELETE", "/api/v1/notifications/"+nid, nil); r.code != http.StatusNotFound {
		t.Fatalf("another user deleted the notification: %d", r.code)
	}
	if r := staff.must(http.StatusOK, "GET", "/api/v1/notifications/"+nid, nil); str(t, r.json, "link") != "/m/matter-clock/" {
		t.Fatal(r.body)
	}
	staff.must(http.StatusNoContent, "DELETE", "/api/v1/notifications/"+nid, nil)
	staff.must(http.StatusNotFound, "GET", "/api/v1/notifications/"+nid, nil)
	logRows := admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=notification&entity_id="+nid, nil)
	if !strings.Contains(logRows.body, "notification.created") || !strings.Contains(logRows.body, "notification.deleted") {
		t.Fatalf("notification writes not audited: %s", logRows.body)
	}

	// Mail: the test send fails clearly before SMTP is set, then reaches the relay.
	if r := admin.do("POST", "/api/v1/mail/test", map[string]any{"to": "avery@firm.example"}); r.code != http.StatusBadRequest || !strings.Contains(r.body, "not configured") {
		t.Fatalf("unconfigured test mail: %d %s", r.code, r.body)
	}
	if r := admin.do("POST", "/api/v1/mail/test", map[string]any{"to": "a@b.example, c@d.example"}); r.code != http.StatusBadRequest {
		t.Fatalf("two recipients accepted: %d", r.code)
	}
	if r := staff.do("POST", "/api/v1/mail/test", map[string]any{"to": "nia@firm.example"}); r.code != http.StatusForbidden {
		t.Fatalf("staff sent test mail: %d", r.code)
	}
	relay := newFakeSMTP(t)
	for k, v := range map[string]any{"mail.smtp_host": "127.0.0.1", "mail.smtp_port": relay.port(), "mail.smtp_from": "system@firm.example", "mail.smtp_starttls": false} {
		admin.must(http.StatusOK, "PATCH", "/api/v1/settings/"+k, map[string]any{"value": v})
	}
	admin.must(http.StatusAccepted, "POST", "/api/v1/mail/test", map[string]any{"to": "avery@firm.example"})
	if r := staff.do("PUT", "/api/v1/mail/smtp-password", map[string]any{"password": "x"}); r.code != http.StatusForbidden {
		t.Fatalf("staff set the SMTP password: %d", r.code)
	}
	admin.must(http.StatusNoContent, "PUT", "/api/v1/mail/smtp-password", map[string]any{"password": "synthetic-smtp-secret"})
	if r := admin.must(http.StatusOK, "GET", "/api/v1/settings/mail.smtp_password", nil); strings.Contains(r.body, "synthetic-smtp-secret") || r.json["configured"] != true {
		t.Fatalf("SMTP password readable or not stored: %s", r.body)
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/audit-log", nil); strings.Contains(r.body, "synthetic-smtp-secret") {
		t.Fatal("SMTP password reached the audit log")
	}

	// Password reset: the token arrives by system mail and changes the password once.
	anon := newClient(t, srv.URL)
	anon.must(http.StatusAccepted, "POST", "/api/v1/auth/password-resets", map[string]any{"email": "nia@firm.example"})
	relay.mu.Lock()
	var body string
	for _, m := range relay.msgs {
		if len(m.To) == 1 && m.To[0] == "nia@firm.example" {
			body = m.Data
		}
	}
	relay.mu.Unlock()
	tok := regexp.MustCompile(`Reset token: (\S+)`).FindStringSubmatch(body)
	if tok == nil {
		t.Fatalf("no reset mail reached the relay: %q", body)
	}
	anon.must(http.StatusNoContent, "POST", "/api/v1/auth/password-resets/confirm", map[string]any{"token": tok[1], "new_password": "synthetic-reset-password-nia"})
	if r := anon.do("POST", "/api/v1/auth/password-resets/confirm", map[string]any{"token": tok[1], "new_password": "synthetic-reset-password-two"}); r.code < 400 || r.code == http.StatusInternalServerError {
		t.Fatalf("reset token reused: %d", r.code)
	}
	if r := newClient(t, srv.URL).do("POST", "/api/v1/auth/login", map[string]string{"email": "nia@firm.example", "password": "synthetic-reset-password-nia"}); r.code != http.StatusOK {
		t.Fatalf("reset password does not sign in: %d %s", r.code, r.body)
	}
	if r := staff.do("GET", "/api/v1/me", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("reset left the old session signed in: %d", r.code)
	}
}
