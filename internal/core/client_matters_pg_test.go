package core

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// TestClientMatterReadsAndAccessWindow covers CORE-11: client list/get allowlist,
// days_after_close window, reopen restore, and access_ended_matters on getMe.
func TestClientMatterReadsAndAccessWindow(t *testing.T) {
	f := newFin(t)
	mine, contactID := f.matter("Mine v. Acme", "cora@client.example", nil)
	other, _ := f.matter("Other v. Beta", "other@client.example", nil)
	older, _ := f.matter("Older v. Acme", "older@client.example", nil)
	f.admin.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{
		"matter_id": older, "contact_id": contactID, "role": "client", "primary": false,
	})

	cli, _ := testkit.EnrollUser(t, f.app.Identity, f.srv.URL, f.admin.kit(), "Cora Client", "cora@client.example", domain.ID(contactID), domain.RoleClient)
	client := wrapClient(cli)

	got := f.admin.must(http.StatusOK, "GET", "/api/v1/settings/client_access.days_after_close", nil)
	if str(t, got.json, "value") != "14" || str(t, got.json, "tab") != "security" {
		t.Fatalf("setting: %s", got.body)
	}

	list := client.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	rows := items(t, list)
	if len(rows) != 2 {
		t.Fatalf("client list want 2 got %d: %s", len(rows), list.body)
	}
	ids := map[string]bool{}
	for _, row := range rows {
		for _, leak := range []string{"practice_area_id", "billing_type", "responsible_attorney_user_id", "custom_fields", "glance"} {
			if _, ok := row[leak]; ok {
				t.Fatalf("client allowlist leaked %s: %v", leak, row)
			}
		}
		for _, need := range []string{"id", "number", "name", "status", "practice_area_name", "opened_on", "responsible_attorney_name"} {
			if row[need] == nil || row[need] == "" {
				t.Fatalf("missing %s: %v", need, row)
			}
		}
		ids[str(t, row, "id")] = true
		if row["practice_area_name"] != "Probate" {
			t.Fatalf("practice_area_name: %v", row)
		}
	}
	if !ids[mine] || !ids[older] || ids[other] {
		t.Fatalf("client saw wrong matters: %v", ids)
	}
	// Newest opened_on first; same day falls back to higher matter number.
	if str(t, rows[0], "opened_on") < str(t, rows[1], "opened_on") ||
		(str(t, rows[0], "opened_on") == str(t, rows[1], "opened_on") && str(t, rows[0], "number") < str(t, rows[1], "number")) {
		t.Fatalf("order not newest first: %s", list.body)
	}

	one := client.must(http.StatusOK, "GET", "/api/v1/matters/"+mine, nil)
	if str(t, one.json, "id") != mine || one.json["billing_type"] != nil {
		t.Fatalf("get allowlist: %s", one.body)
	}
	if r := client.do("GET", "/api/v1/matters/"+other, nil); r.code != http.StatusNotFound {
		t.Fatalf("foreign matter: %d %s", r.code, r.body)
	}

	me := client.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if me.json["access_ended_matters"] != float64(0) {
		t.Fatalf("access_ended before close: %s", me.body)
	}
	if str(t, me.json, "linked_contact_id") != contactID {
		t.Fatalf("linked_contact_id: %s", me.body)
	}
	staffMe := f.staff.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if _, ok := staffMe.json["access_ended_matters"]; ok {
		t.Fatalf("staff should not see access_ended_matters: %s", staffMe.body)
	}

	f.admin.must(http.StatusOK, "POST", "/api/v1/matters/"+older+"/close", map[string]any{
		"reason": "done", "acknowledge": true,
	})
	if r := client.do("GET", "/api/v1/matters/"+older, nil); r.code != http.StatusOK {
		t.Fatalf("closed within default window: %d %s", r.code, r.body)
	}

	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/client_access.days_after_close", map[string]any{"value": 0})
	// practice_records is the durable source of truth; the matters table is a projection.
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	res, err := f.app.Pool.Exec(`
UPDATE practice_records
SET body = jsonb_set(body::jsonb, '{ClosedOn}', to_jsonb($1::text))::text
WHERE kind = 'matter' AND id = $2`, yesterday, older)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("practice_records update rows=%d id=%s", n, older)
	}
	f.restart()

	if r := client.do("GET", "/api/v1/matters/"+older, nil); r.code != http.StatusNotFound {
		t.Fatalf("access ended get: %d %s", r.code, r.body)
	}
	after := items(t, client.must(http.StatusOK, "GET", "/api/v1/matters", nil))
	if len(after) != 1 || str(t, after[0], "id") != mine {
		t.Fatalf("access ended list: %v", after)
	}
	me = client.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if me.json["access_ended_matters"] != float64(1) {
		t.Fatalf("access_ended count: %s", me.body)
	}

	f.admin.must(http.StatusOK, "POST", "/api/v1/matters/"+older+"/reopen", map[string]any{"reason": "still active"})
	if r := client.do("GET", "/api/v1/matters/"+older, nil); r.code != http.StatusOK {
		t.Fatalf("reopened: %d %s", r.code, r.body)
	}
	me = client.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if me.json["access_ended_matters"] != float64(0) {
		t.Fatalf("access_ended after reopen: %s", me.body)
	}

	tool := toolName(t, "GET", "/api/v1/matters")
	mcp := mcpCall(client, tool, map[string]any{})
	if mcp.code != http.StatusOK || !strings.Contains(mcp.body, mine) || strings.Contains(mcp.body, other) {
		t.Fatalf("mcp list: %s", mcp.body)
	}
}
