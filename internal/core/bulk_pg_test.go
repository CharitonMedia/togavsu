package core

import (
	"net/http"
	"strings"
	"testing"
)

// TestReleasePracticeBulk covers the practice :bulk endpoints (PRD 7, AC-002): a valid
// batch writes every item in one transaction, one bad item rejects the whole batch with
// its index, and each item keeps its record's access and wall rules.
func TestReleasePracticeBulk(t *testing.T) {
	app, srv := start(t, testDSN(t))
	admin := firstRun(t, srv.URL)
	staff, _ := enrollUser(t, app, srv.URL, admin, "Bo", "bo@firm.example", "staff")
	acct, _ := enrollUser(t, app, srv.URL, admin, "Ace", "ace@firm.example", "accountant")
	area := str(t, admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Probate", "stages": []string{"Open"}}).json, "id")
	me := str(t, admin.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")

	ok := staff.must(http.StatusCreated, "POST", "/api/v1/contacts:bulk", map[string]any{"items": []any{
		map[string]any{"kind": "person", "given_name": "Bulk", "family_name": "One"},
		map[string]any{"kind": "person", "given_name": "Bulk", "family_name": "Two"},
	}})
	data, _ := ok.json["data"].([]any)
	if len(data) != 2 || !strings.Contains(ok.body, "Bulk One") || !strings.Contains(ok.body, "Bulk Two") {
		t.Fatalf("bulk contacts: %s", ok.body)
	}
	oneID := data[0].(map[string]any)["id"].(string)

	// Item 2 is invalid: nothing from the batch is stored, and the error names it.
	bad := staff.do("POST", "/api/v1/contacts:bulk", map[string]any{"items": []any{
		map[string]any{"kind": "person", "given_name": "Rolled", "family_name": "Back"},
		map[string]any{"kind": "person", "given_name": "Also", "family_name": "Back"},
		map[string]any{"kind": "spaceship"},
	}})
	if bad.code != http.StatusBadRequest || !strings.Contains(bad.body, "items[2]") {
		t.Fatalf("bad batch: %d %s", bad.code, bad.body)
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/search?q=Rolled&scope=people", nil); strings.Contains(r.body, "Rolled Back") || strings.Contains(r.body, "Also Back") {
		t.Fatalf("a rejected batch left a row: %s", r.body)
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/audit-log", nil); strings.Contains(r.body, "Rolled") {
		t.Fatal("a rejected batch left audit rows")
	}
	if r := staff.do("POST", "/api/v1/contacts:bulk", map[string]any{"items": []any{}}); r.code != http.StatusBadRequest {
		t.Fatalf("empty batch: %d", r.code)
	}
	admin.must(http.StatusOK, "PATCH", "/api/v1/settings/api.bulk_batch_max", map[string]any{"value": 2})
	if r := staff.do("POST", "/api/v1/notes:bulk", map[string]any{"items": []any{map[string]any{}, map[string]any{}, map[string]any{}}}); r.code != http.StatusBadRequest || !strings.Contains(r.body, "bulk_batch_max") {
		t.Fatalf("over the batch limit: %d %s", r.code, r.body)
	}
	admin.must(http.StatusOK, "POST", "/api/v1/settings/api.bulk_batch_max/reset", nil)

	// Roles: an accountant can't bulk-write; walls: staff can't add a note to a walled
	// matter in a batch, and the rest of that batch is discarded too.
	if r := acct.do("POST", "/api/v1/contacts:bulk", map[string]any{"items": []any{map[string]any{"kind": "person", "given_name": "No", "family_name": "Way"}}}); r.code != http.StatusForbidden {
		t.Fatalf("accountant bulk write: %d %s", r.code, r.body)
	}
	matters := admin.must(http.StatusCreated, "POST", "/api/v1/matters:bulk", map[string]any{"items": []any{
		map[string]any{"name": "Bulk open matter", "practice_area_id": area, "billing_type": "hourly"},
		map[string]any{"name": "Bulk walled matter", "practice_area_id": area, "billing_type": "hourly"},
	}})
	mdata := matters.json["data"].([]any)
	openID, walledID := mdata[0].(map[string]any)["id"].(string), mdata[1].(map[string]any)["id"].(string)
	admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walledID+"/wall", map[string]any{"user_ids": []string{me}})
	walled := staff.do("POST", "/api/v1/notes:bulk", map[string]any{"items": []any{
		map[string]any{"matter_id": openID, "title": "Fine note", "body": "ok"},
		map[string]any{"matter_id": walledID, "title": "Behind the wall", "body": "no"},
	}})
	if walled.code != http.StatusNotFound && walled.code != http.StatusForbidden {
		t.Fatalf("bulk note on a walled matter: %d %s", walled.code, walled.body)
	}
	if r := staff.must(http.StatusOK, "GET", "/api/v1/notes?matter_id="+openID, nil); strings.Contains(r.body, "Fine note") {
		t.Fatal("the walled batch kept its first note")
	}
	admin.must(http.StatusCreated, "POST", "/api/v1/matter-parties:bulk", map[string]any{"items": []any{
		map[string]any{"matter_id": openID, "contact_id": oneID, "role": "client", "primary": true},
	}})
	admin.must(http.StatusCreated, "POST", "/api/v1/contact-phones:bulk", map[string]any{"items": []any{
		map[string]any{"contact_id": oneID, "label": "mobile", "phone": "+15735550142", "primary": true},
	}})
	if r := admin.must(http.StatusOK, "GET", "/api/v1/search?q=5735550142&scope=people", nil); !strings.Contains(r.body, "Bulk One") {
		t.Fatalf("bulk phone not searchable: %s", r.body)
	}
	// The same operation over /mcp.
	call := mcpCall(staff, toolName(t, "POST", "/api/v1/contacts:bulk"), map[string]any{"body": map[string]any{"items": []any{map[string]any{"kind": "person", "given_name": "Mcp", "family_name": "Bulk"}}}})
	if !strings.Contains(call.body, "Mcp Bulk") || strings.Contains(call.body, `"isError":true`) {
		t.Fatal(call.body)
	}
}
