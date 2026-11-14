package core

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
)

// moduleBearer issues a module's own token (no user) with scopes, as module install does.
func moduleBearer(t *testing.T, f *fin, module string, scopes ...string) *client {
	t.Helper()
	u, _ := url.Parse(f.srv.URL)
	var session string
	for _, c := range f.admin.http.Jar.Cookies(u) {
		if c.Name == sessionCookie {
			session = c.Value
		}
	}
	ctx := context.Background()
	caller, err := f.app.Identity.Authenticate(ctx, identity.Presented{SessionToken: session})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.app.Identity.IssueToken(ctx, caller, identity.Issue{Name: "module " + module, ModuleID: module, Scopes: scopes, Approved: scopes})
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t, f.srv.URL)
	c.bearer = res.Secret
	return c
}

func want(t *testing.T, r resp, code int, contains string) {
	t.Helper()
	if r.code != code || !strings.Contains(r.body, contains) {
		t.Fatalf("want %d with %q, got %d: %s", code, contains, r.code, r.body)
	}
}

// TestPartyRolesAPI covers listPartyRole, createPartyRole, getPartyRole and
// updatePartyRole: the records follow practice.party_roles, which parties are checked
// against; only an admin writes; a key parties use does not change.
func TestPartyRolesAPI(t *testing.T) {
	f := newFin(t)
	list := f.staff.must(http.StatusOK, "GET", "/api/v1/party-roles", nil)
	if n := len(items(t, list)); n != len(domain.DefaultPartyRoles()) {
		t.Fatalf("seeded roles: %d %s", n, list.body)
	}
	if r := f.acct.do("GET", "/api/v1/party-roles", nil); r.code != http.StatusForbidden {
		t.Fatalf("accountant listed party roles: %d", r.code)
	}
	if r := f.staff.do("POST", "/api/v1/party-roles", map[string]any{"label": "Mediator"}); r.code != http.StatusForbidden {
		t.Fatalf("staff created a role: %d %s", r.code, r.body)
	}
	if r := f.atty.do("POST", "/api/v1/party-roles", map[string]any{"label": "Mediator"}); r.code != http.StatusForbidden {
		t.Fatalf("attorney created a role: %d %s", r.code, r.body)
	}
	want(t, f.admin.do("POST", "/api/v1/party-roles", map[string]any{"label": "  "}), http.StatusBadRequest, "label")
	want(t, f.admin.do("POST", "/api/v1/party-roles", map[string]any{"label": "Judge again", "key": "judge"}), http.StatusConflict, "")

	created := f.admin.do("POST", "/api/v1/party-roles", map[string]any{"label": "Mediator"}, "Idempotency-Key", "role-1")
	want(t, created, http.StatusCreated, `"key":"mediator"`)
	id := str(t, created.json, "id")
	if created.json["active"] != true {
		t.Fatal(created.body)
	}
	replay := f.admin.do("POST", "/api/v1/party-roles", map[string]any{"label": "Mediator"}, "Idempotency-Key", "role-1")
	if replay.code != http.StatusCreated || str(t, replay.json, "id") != id {
		t.Fatalf("replay: %d %s", replay.code, replay.body)
	}
	want(t, f.admin.do("POST", "/api/v1/party-roles", map[string]any{"label": "Other"}, "Idempotency-Key", "role-1"), http.StatusConflict, "")
	if got := f.staff.must(http.StatusOK, "GET", "/api/v1/party-roles/"+id, nil); str(t, got.json, "label") != "Mediator" {
		t.Fatal(got.body)
	}
	want(t, f.staff.do("GET", "/api/v1/party-roles/"+string(mustID(t)), nil), http.StatusNotFound, "not_found")
	// The setting moved with the record, so a party can now take the role.
	setting := f.admin.must(http.StatusOK, "GET", "/api/v1/settings/practice.party_roles", nil)
	if !strings.Contains(setting.body, "mediator") {
		t.Fatalf("setting not updated: %s", setting.body)
	}
	matter, _ := f.matter("Estate of Quill", "", nil)
	contact := str(t, f.admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Mia", "family_name": "Mediator"}).json, "id")
	party := f.staff.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": matter, "contact_id": contact, "role": "mediator"})

	// A key in use stays; the label changes; deactivating takes it off the list parties use.
	want(t, f.admin.do("PATCH", "/api/v1/party-roles/"+id, map[string]any{"key": "neutral"}), http.StatusConflict, "")
	off := f.admin.must(http.StatusOK, "PATCH", "/api/v1/party-roles/"+id, map[string]any{"label": "Court mediator", "active": false})
	if off.json["active"] != false || str(t, off.json, "label") != "Court mediator" {
		t.Fatal(off.body)
	}
	want(t, f.staff.do("PATCH", "/api/v1/matter-parties/"+str(t, party.json, "id"), map[string]any{"role": "mediator"}), http.StatusBadRequest, "role")
	inactive := items(t, f.staff.must(http.StatusOK, "GET", "/api/v1/party-roles?active=false", nil))
	if len(inactive) != 1 || inactive[0]["id"] != id {
		t.Fatalf("active=false: %v", inactive)
	}
	want(t, f.staff.do("GET", "/api/v1/party-roles?active=maybe", nil), http.StatusBadRequest, "active")
	// Pages follow the cursor; a limit above api.max_page_size is refused.
	first := f.staff.must(http.StatusOK, "GET", "/api/v1/party-roles?limit=5", nil)
	next, _ := first.json["page"].(map[string]any)["next_cursor"].(string)
	if len(items(t, first)) != 5 || next == "" {
		t.Fatal(first.body)
	}
	if second := f.staff.must(http.StatusOK, "GET", "/api/v1/party-roles?limit=5&cursor="+next, nil); items(t, second)[0]["id"] == items(t, first)[0]["id"] {
		t.Fatal("cursor did not advance")
	}
	want(t, f.staff.do("GET", "/api/v1/party-roles?limit=100000", nil), http.StatusBadRequest, "limit")
	audit := f.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=party_role&entity_id="+id, nil)
	if !strings.Contains(audit.body, "Court mediator") {
		t.Fatalf("no audit row: %s", audit.body)
	}
	f.restart()
	if got := f.staff.must(http.StatusOK, "GET", "/api/v1/party-roles/"+id, nil); got.json["active"] != false || str(t, got.json, "label") != "Court mediator" {
		t.Fatalf("after restart: %s", got.body)
	}
}

// TestMarkerDefinitionsAPI covers the marker-definition routes and updateMatterMarker:
// admin writes firm markers, staff reads them, labels and colors are checked, an
// inactive marker is not pinned, and a pin moves only between matters the caller writes.
func TestMarkerDefinitionsAPI(t *testing.T) {
	f := newFin(t)
	seeded := items(t, f.staff.must(http.StatusOK, "GET", "/api/v1/marker-definitions", nil))
	if len(seeded) != len(domain.DefaultMarkers()) || seeded[0]["color"] == "" || seeded[0]["created_at"] == "" {
		t.Fatalf("seeded markers: %v", seeded)
	}
	if r := f.acct.do("GET", "/api/v1/marker-definitions", nil); r.code != http.StatusForbidden {
		t.Fatalf("accountant: %d", r.code)
	}
	for _, c := range []*client{f.staff, f.atty} {
		if r := c.do("POST", "/api/v1/marker-definitions", map[string]any{"label": "VIP"}); r.code != http.StatusForbidden {
			t.Fatalf("non-admin created a marker: %d", r.code)
		}
	}
	want(t, f.admin.do("POST", "/api/v1/marker-definitions", map[string]any{"label": "<b>VIP</b>"}), http.StatusBadRequest, "label")
	want(t, f.admin.do("POST", "/api/v1/marker-definitions", map[string]any{"label": strings.Repeat("x", 41)}), http.StatusBadRequest, "label")
	want(t, f.admin.do("POST", "/api/v1/marker-definitions", map[string]any{"label": "VIP", "color": "red"}), http.StatusBadRequest, "color")
	want(t, f.admin.do("POST", "/api/v1/marker-definitions", map[string]any{"label": "Zero fee"}), http.StatusConflict, "")

	vip := f.admin.do("POST", "/api/v1/marker-definitions", map[string]any{"label": "VIP", "color": "#AA3300"}, "Idempotency-Key", "vip")
	want(t, vip, http.StatusCreated, `"color":"#AA3300"`)
	vipID := str(t, vip.json, "id")
	if vip.json["module_id"] != nil || vip.json["active"] != true {
		t.Fatal(vip.body)
	}
	if again := f.admin.do("POST", "/api/v1/marker-definitions", map[string]any{"label": "VIP", "color": "#AA3300"}, "Idempotency-Key", "vip"); str(t, again.json, "id") != vipID {
		t.Fatalf("replay made a second marker: %s", again.body)
	}
	if got := f.staff.must(http.StatusOK, "GET", "/api/v1/marker-definitions/"+vipID, nil); str(t, got.json, "label") != "VIP" {
		t.Fatal(got.body)
	}
	want(t, f.staff.do("GET", "/api/v1/marker-definitions/"+string(mustID(t)), nil), http.StatusNotFound, "not_found")
	want(t, f.staff.do("PATCH", "/api/v1/marker-definitions/"+vipID, map[string]any{"color": "#000000"}), http.StatusForbidden, "")
	zero := ""
	for _, m := range seeded {
		if m["label"] == "Zero fee" {
			zero = m["id"].(string)
		}
	}
	want(t, f.admin.do("PATCH", "/api/v1/marker-definitions/"+zero, map[string]any{"label": "Free"}), http.StatusBadRequest, "core marker")
	upd := f.admin.must(http.StatusOK, "PATCH", "/api/v1/marker-definitions/"+vipID, map[string]any{"label": "Key client", "color": "#112233"})
	if str(t, upd.json, "label") != "Key client" || str(t, upd.json, "color") != "#112233" {
		t.Fatal(upd.body)
	}

	// Pin, then move the pin to another marker and to another matter.
	m1, _ := f.matter("Estate of Arden", "", nil)
	m2, _ := f.matter("Estate of Brook", "", nil)
	walled, _ := f.matter("Estate of Crane", "", nil)
	f.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{f.adminID}})
	pin := f.staff.must(http.StatusCreated, "POST", "/api/v1/matter-markers", map[string]any{"matter_id": m1, "marker_id": vipID})
	pinID := str(t, pin.json, "id")
	want(t, f.acct.do("PATCH", "/api/v1/matter-markers/"+pinID, map[string]any{"matter_id": m2}), http.StatusForbidden, "")
	want(t, f.staff.do("PATCH", "/api/v1/matter-markers/"+pinID, map[string]any{"matter_id": walled}), http.StatusNotFound, "not_found")
	want(t, f.staff.do("PATCH", "/api/v1/matter-markers/"+string(mustID(t)), map[string]any{"matter_id": m2}), http.StatusNotFound, "not_found")
	want(t, f.staff.do("PATCH", "/api/v1/matter-markers/"+pinID, map[string]any{"marker_id": string(mustID(t))}), http.StatusBadRequest, "marker_id")
	want(t, f.staff.do("PATCH", "/api/v1/matter-markers/"+pinID, map[string]any{"practice_area_id": f.area}), http.StatusBadRequest, "practice_area_id")
	moved := f.staff.must(http.StatusOK, "PATCH", "/api/v1/matter-markers/"+pinID, map[string]any{"matter_id": m2, "marker_id": zero})
	if str(t, moved.json, "matter_id") != m2 || str(t, moved.json, "marker_id") != zero {
		t.Fatal(moved.body)
	}
	if got := f.staff.must(http.StatusOK, "GET", "/api/v1/matter-markers/"+pinID, nil); str(t, got.json, "matter_id") != m2 {
		t.Fatal(got.body)
	}
	if body := f.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=matter&entity_id="+m2, nil).body; !strings.Contains(body, "Zero fee") {
		t.Fatalf("no audit row for the move: %s", body)
	}
	// A second pin of the same marker on the same matter is a conflict.
	other := f.staff.must(http.StatusCreated, "POST", "/api/v1/matter-markers", map[string]any{"matter_id": m2, "marker_id": vipID})
	want(t, f.staff.do("PATCH", "/api/v1/matter-markers/"+str(t, other.json, "id"), map[string]any{"marker_id": zero}), http.StatusConflict, "")

	// Deactivated: listed as inactive, not pinned again, existing pins stay.
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/marker-definitions/"+vipID, map[string]any{"active": false})
	want(t, f.staff.do("POST", "/api/v1/matter-markers", map[string]any{"matter_id": m1, "marker_id": vipID}), http.StatusBadRequest, "inactive")
	f.staff.must(http.StatusOK, "GET", "/api/v1/matter-markers/"+str(t, other.json, "id"), nil)
	if act := items(t, f.staff.must(http.StatusOK, "GET", "/api/v1/marker-definitions?active=true", nil)); len(act) != len(domain.DefaultMarkers()) {
		t.Fatalf("active filter: %v", act)
	}

	// The same operation through /mcp, under the caller's own role.
	call := mcpCall(f.admin, toolName(t, "GET", "/api/v1/marker-definitions/{marker_id}"), map[string]any{"path": map[string]any{"marker_id": vipID}})
	if call.code != http.StatusOK || !strings.Contains(call.body, "Key client") || strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("mcp: %s", call.body)
	}
	denied := mcpCall(f.staff, toolName(t, "POST", "/api/v1/marker-definitions"), map[string]any{"body": map[string]any{"label": "Sneaky"}})
	if !strings.Contains(denied.body, `"isError":true`) {
		t.Fatalf("mcp let staff create a marker: %s", denied.body)
	}
	f.restart()
	if got := f.staff.must(http.StatusOK, "GET", "/api/v1/marker-definitions/"+vipID, nil); got.json["active"] != false || str(t, got.json, "label") != "Key client" {
		t.Fatalf("after restart: %s", got.body)
	}
}

// TestModuleMarkersAPI covers createModuleMarker, updateModuleMarker and
// revokeModuleMarker: only an enabled module's own token, in its own namespace, with
// markers:write; a duplicate active label conflicts; revoke is idempotent and keeps pins.
func TestModuleMarkersAPI(t *testing.T) {
	f := newFin(t)
	enabled := map[string]bool{"case-clock": true, "other-mod": true}
	f.app.Practice.SetModuleCheck(func(_ context.Context, module string) (bool, error) { return enabled[module], nil })
	mod := moduleBearer(t, f, "case-clock", "markers:write")
	other := moduleBearer(t, f, "other-mod", "markers:write")
	noScope := moduleBearer(t, f, "case-clock", "matters:read")
	off := moduleBearer(t, f, "sleepy", "markers:write")
	base := "/api/v1/modules/case-clock/markers"
	body := map[string]any{"label": "Clock running", "color": "#00AA55", "target": "cases/open"}

	want(t, f.admin.do("POST", base, body), http.StatusForbidden, "")
	want(t, noScope.do("POST", base, body), http.StatusForbidden, "")
	want(t, other.do("POST", base, body), http.StatusForbidden, "namespace")
	want(t, off.do("POST", "/api/v1/modules/sleepy/markers", body), http.StatusForbidden, "enabled")
	want(t, mod.do("POST", "/api/v1/marker-definitions", map[string]any{"label": "Firm"}), http.StatusForbidden, "")
	want(t, mod.do("POST", base, map[string]any{"label": "Clock", "color": "#00AA55", "target": "https://evil.example"}), http.StatusBadRequest, "target")
	want(t, mod.do("POST", base, map[string]any{"label": "Clock", "color": "green"}), http.StatusBadRequest, "color")
	want(t, mod.do("POST", base, map[string]any{"label": "Pro bono", "color": "#00AA55"}), http.StatusConflict, "core marker")

	made := mod.do("POST", base, map[string]any{"label": "Clock running", "color": "#00AA55", "target": "cases/open", "module_id": "other-mod"}, "Idempotency-Key", "mk")
	want(t, made, http.StatusCreated, `"namespace":"case-clock"`)
	id := str(t, made.json, "id")
	if made.json["module_id"] != "case-clock" || made.json["placement"] != "matter-header" || made.json["target"] != "cases/open" {
		t.Fatal(made.body)
	}
	if again := mod.do("POST", base, map[string]any{"label": "Clock running", "color": "#00AA55", "target": "cases/open", "module_id": "other-mod"}, "Idempotency-Key", "mk"); str(t, again.json, "id") != id {
		t.Fatalf("replay: %s", again.body)
	}
	want(t, mod.do("POST", base, body), http.StatusConflict, "")
	// Another module may use the same label in its own namespace.
	theirs := other.must(http.StatusCreated, "POST", "/api/v1/modules/other-mod/markers", body)
	want(t, mod.do("PATCH", base+"/"+str(t, theirs.json, "id"), map[string]any{"color": "#000000"}), http.StatusForbidden, "namespace")
	firm := ""
	for _, m := range items(t, f.staff.must(http.StatusOK, "GET", "/api/v1/marker-definitions", nil)) {
		if m["module_id"] == nil {
			firm = m["id"].(string)
		}
	}
	want(t, mod.do("PATCH", base+"/"+firm, map[string]any{"color": "#000000"}), http.StatusForbidden, "namespace")
	want(t, mod.do("POST", base+"/"+firm+"/revoke", nil), http.StatusForbidden, "namespace")
	want(t, mod.do("PATCH", base+"/"+string(mustID(t)), map[string]any{"color": "#000000"}), http.StatusNotFound, "not_found")
	want(t, f.admin.do("PATCH", "/api/v1/marker-definitions/"+id, map[string]any{"color": "#000000"}), http.StatusForbidden, "module")

	upd := mod.must(http.StatusOK, "PATCH", base+"/"+id, map[string]any{"label": "Clock stopped", "color": "#AA0000"})
	if str(t, upd.json, "label") != "Clock stopped" || upd.json["target"] != "cases/open" {
		t.Fatal(upd.body)
	}
	// Staff pin the module marker; it shows on the firm-wide definition list.
	matter, _ := f.matter("Estate of Dorn", "", nil)
	pin := f.staff.must(http.StatusCreated, "POST", "/api/v1/matter-markers", map[string]any{"matter_id": matter, "marker_id": id})
	if def := f.staff.must(http.StatusOK, "GET", "/api/v1/marker-definitions/"+id, nil); def.json["module_id"] != "case-clock" {
		t.Fatal(def.body)
	}

	revoked := mod.must(http.StatusOK, "POST", base+"/"+id+"/revoke", nil)
	if revoked.json["active"] != false {
		t.Fatal(revoked.body)
	}
	count := func() int {
		return strings.Count(f.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=marker&entity_id="+id, nil).body, `"field":"active"`)
	}
	before := count()
	if again := mod.must(http.StatusOK, "POST", base+"/"+id+"/revoke", nil); again.json["active"] != false {
		t.Fatal(again.body)
	}
	if count() != before || before != 1 {
		t.Fatalf("second revoke recorded again: %d then %d", before, count())
	}
	want(t, mod.do("PATCH", base+"/"+id, map[string]any{"color": "#00AA55"}), http.StatusConflict, "revoked")
	f.staff.must(http.StatusOK, "GET", "/api/v1/matter-markers/"+str(t, pin.json, "id"), nil)
	// After revoke the label is free again in this namespace.
	mod.must(http.StatusCreated, "POST", base, body)
	enabled["case-clock"] = false
	want(t, mod.do("POST", base, map[string]any{"label": "Later", "color": "#00AA55"}), http.StatusForbidden, "enabled")
	enabled["case-clock"] = true

	// The same operation through /mcp with the module token.
	call := mcpCall(mod, toolName(t, "POST", "/api/v1/modules/{module_id}/markers"), map[string]any{
		"path": map[string]any{"module_id": "case-clock"}, "body": map[string]any{"label": "Via MCP", "color": "#123456"}})
	if !strings.Contains(call.body, "Via MCP") || strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("mcp: %s", call.body)
	}
}
