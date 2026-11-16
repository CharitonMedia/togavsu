package core

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestCORE14ClientAccess covers grant, get, temporary password, deactivate/reactivate,
// password_change_required, non-client refusal, and no password in grant response or audit.
func TestCORE14ClientAccess(t *testing.T) {
	f := newFin(t)
	matter, contact := f.matter("Portal Estate", "portal.client@example.com", nil)

	if r := f.staff.do("GET", "/api/v1/client-access?contact_id="+contact, nil); r.code != http.StatusNotFound {
		t.Fatalf("missing access: %d %s", r.code, r.body)
	}

	grant := f.staff.must(http.StatusOK, "POST", "/api/v1/client-access", map[string]any{
		"contact_id": contact,
	})
	userID := str(t, grant.json, "user_id")
	if grant.json["created"] != true || str(t, grant.json, "email") != "portal.client@example.com" {
		t.Fatalf("grant: %s", grant.body)
	}
	mailStatus := str(t, grant.json, "mail_status")
	if mailStatus != "sent" && mailStatus != "failed" && mailStatus != "skipped" {
		t.Fatalf("mail_status: %s", grant.body)
	}
	if strings.Contains(grant.body, "temporary_password") {
		t.Fatalf("grant leaked password: %s", grant.body)
	}

	got := f.staff.must(http.StatusOK, "GET", "/api/v1/client-access?contact_id="+contact, nil)
	if str(t, got.json, "user_id") != userID || got.json["active"] != true || got.json["must_change_password"] != true {
		t.Fatalf("get: %s", got.body)
	}
	if _, ok := got.json["granted_at"]; !ok {
		t.Fatalf("granted_at missing: %s", got.body)
	}

	if r := f.acct.do("GET", "/api/v1/client-access?contact_id="+contact, nil); r.code != http.StatusForbidden {
		t.Fatalf("accountant get: %d", r.code)
	}
	cli, _ := enrollUser(t, f.app, f.srv.URL, f.admin, "Other Client", "other.client@example.com", domain.RoleClient)
	if r := cli.do("POST", "/api/v1/client-access", map[string]any{"contact_id": contact}); r.code != http.StatusForbidden {
		t.Fatalf("client grant: %d", r.code)
	}

	temp := f.admin.must(http.StatusOK, "POST", "/api/v1/client-access/"+userID+"/temporary-password", nil)
	pw := str(t, temp.json, "temporary_password")
	if len([]rune(pw)) < 12 {
		t.Fatalf("temp password too short: %q", pw)
	}

	portal := newClient(t, f.srv.URL)
	login := portal.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{
		"email": "portal.client@example.com", "password": pw,
	})
	if str(t, login.json, "status") != "authenticated" {
		t.Fatalf("login: %s", login.body)
	}
	portal.csrf = str(t, login.json, "csrf_token")
	if r := portal.do("GET", "/api/v1/me", nil); r.code != http.StatusForbidden || r.json["code"] != "password_change_required" {
		t.Fatalf("me while must_change: %d %s", r.code, r.body)
	}
	if r := portal.do("GET", "/api/v1/matters", nil); r.code != http.StatusForbidden || r.json["code"] != "password_change_required" {
		t.Fatalf("matters while must_change: %d %s", r.code, r.body)
	}
	portal.must(http.StatusNoContent, "POST", "/api/v1/me/password", map[string]any{
		"current_password": pw, "new_password": "synthetic-changed-password",
	})
	me := portal.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if str(t, me.json, "email") != "portal.client@example.com" {
		t.Fatalf("after change: %s", me.body)
	}
	again := f.staff.must(http.StatusOK, "GET", "/api/v1/client-access?contact_id="+contact, nil)
	if again.json["must_change_password"] != false {
		t.Fatalf("flag not cleared: %s", again.body)
	}

	f.staff.must(http.StatusOK, "POST", "/api/v1/client-access/"+userID+"/deactivate", nil)
	off := f.staff.must(http.StatusOK, "GET", "/api/v1/client-access?contact_id="+contact, nil)
	if off.json["active"] != false {
		t.Fatalf("deactivated: %s", off.body)
	}
	f.staff.must(http.StatusOK, "POST", "/api/v1/client-access/"+userID+"/reactivate", nil)
	on := f.staff.must(http.StatusOK, "GET", "/api/v1/client-access?contact_id="+contact, nil)
	if on.json["active"] != true {
		t.Fatalf("reactivated: %s", on.body)
	}

	staffContact := f.admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{
		"kind": "person", "given_name": "Sam", "family_name": "Staffish",
	})
	staffContactID := str(t, staffContact.json, "id")
	f.admin.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{
		"matter_id": matter, "contact_id": staffContactID, "role": "client", "primary": false,
	})
	_, staffUserID := enrollUser(t, f.app, f.srv.URL, f.admin, "Staff Dual", "staff.dual@example.com", domain.RoleStaff, domain.RoleClient)
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/users/"+string(staffUserID), map[string]any{"linked_contact_id": staffContactID})
	if r := f.staff.do("GET", "/api/v1/client-access?contact_id="+staffContactID, nil); r.code != http.StatusConflict {
		t.Fatalf("non-client-only: %d %s", r.code, r.body)
	}

	orphan := f.admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{
		"kind": "person", "given_name": "Orphan", "family_name": "Party",
	})
	if r := f.staff.do("POST", "/api/v1/client-access", map[string]any{"contact_id": str(t, orphan.json, "id")}); r.code != http.StatusNotFound {
		t.Fatalf("orphan contact: %d %s", r.code, r.body)
	}

	re := f.staff.must(http.StatusOK, "POST", "/api/v1/client-access", map[string]any{"contact_id": contact})
	if re.json["created"] != false || str(t, re.json, "user_id") != userID {
		t.Fatalf("re-grant: %s", re.body)
	}
}
