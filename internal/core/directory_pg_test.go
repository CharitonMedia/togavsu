package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestFirmProfileAndUserDirectory covers CORE-18 and CORE-19: firm profile for every
// role, the directory with filters and the client projection, and MCP parity.
func TestFirmProfileAndUserDirectory(t *testing.T) {
	f := newFin(t)
	cli, clientID := enrollUser(t, f.app, f.srv.URL, f.admin, "Cleo Client", "cleo@client.example", domain.RoleClient)
	_, inactiveID := enrollUser(t, f.app, f.srv.URL, f.admin, "Idle Intern", "idle@firm.example", domain.RoleStaff)
	f.admin.must(http.StatusOK, "POST", "/api/v1/users/"+string(inactiveID)+"/deactivate", nil)

	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/firm.name", map[string]any{"value": "Example Law LLC"})
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/firm.short_name", map[string]any{"value": "Example"})
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/firm.phone", map[string]any{"value": "+15551234567"})
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/firm.email", map[string]any{"value": "hello@firm.example"})
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/firm.time_zone", map[string]any{"value": "America/Chicago"})
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/appearance.client_portal_label", map[string]any{"value": "Client portal"})
	f.admin.must(http.StatusOK, "PUT", "/api/v1/settings/appearance", map[string]any{
		"preset": "navy_gold", "staff_sidebar": "", "staff_header": "", "staff_page": "", "staff_accent": "",
		"client_header": "#112233", "heading_font": "Lora", "body_font": "Inter", "text_size": "md", "mode": "device",
	})

	for _, c := range []*client{f.admin, f.atty, f.staff, f.acct, cli} {
		got := c.must(http.StatusOK, "GET", "/api/v1/firm-profile", nil)
		if str(t, got.json, "name") != "Example Law LLC" || str(t, got.json, "short_name") != "Example" ||
			str(t, got.json, "phone") != "+15551234567" || str(t, got.json, "email") != "hello@firm.example" ||
			str(t, got.json, "time_zone") != "America/Chicago" {
			t.Fatalf("firm profile: %s", got.body)
		}
		portal, _ := got.json["client_portal"].(map[string]any)
		if portal["client_header"] != "#112233" || portal["label"] != "Client portal" {
			t.Fatalf("client_portal: %s", got.body)
		}
		if _, ok := got.json["logo_file_id"]; !ok || got.json["logo_file_id"] != nil {
			t.Fatalf("empty logo_file_id: %s", got.body)
		}
	}

	// Accountant may read the firm profile but not the directory.
	if r := f.acct.do("GET", "/api/v1/user-directory", nil); r.code != http.StatusForbidden {
		t.Fatalf("accountant directory: %d %s", r.code, r.body)
	}

	list := f.staff.must(http.StatusOK, "GET", "/api/v1/user-directory", nil)
	rows := items(t, list)
	if len(rows) < 5 {
		t.Fatalf("directory rows: %s", list.body)
	}
	for _, row := range rows {
		if _, ok := row["email"]; ok {
			t.Fatalf("directory leaked email: %s", list.body)
		}
		if row["id"] == nil || row["name"] == nil || row["roles"] == nil || row["active"] == nil {
			t.Fatalf("directory row: %v", row)
		}
	}

	// role=attorney and active filters.
	attorneys := items(t, f.admin.must(http.StatusOK, "GET", "/api/v1/user-directory?role=attorney", nil))
	for _, row := range attorneys {
		roles, _ := row["roles"].([]any)
		found := false
		for _, r := range roles {
			if r == "attorney" {
				found = true
			}
		}
		if !found {
			t.Fatalf("role=attorney: %v", row)
		}
	}
	inactiveRows := items(t, f.admin.must(http.StatusOK, "GET", "/api/v1/user-directory?active=false", nil))
	if len(inactiveRows) != 1 || inactiveRows[0]["id"] != string(inactiveID) {
		t.Fatalf("active=false: %v", inactiveRows)
	}

	// ids filter (up to 100) and over-limit refusal.
	q := url.Values{"ids": {f.adminID, string(clientID)}}
	byID := items(t, f.admin.must(http.StatusOK, "GET", "/api/v1/user-directory?"+q.Encode(), nil))
	if len(byID) != 2 {
		t.Fatalf("ids filter: %v", byID)
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = f.adminID
	}
	want(t, f.admin.do("GET", "/api/v1/user-directory?"+url.Values{"ids": many}.Encode(), nil), http.StatusBadRequest, "ids")

	// Client projection: only id+name for active firm-facing users.
	clientList := items(t, cli.must(http.StatusOK, "GET", "/api/v1/user-directory", nil))
	seenInactive, seenClient := false, false
	for _, row := range clientList {
		if _, ok := row["roles"]; ok || row["active"] != nil || row["email"] != nil {
			t.Fatalf("client row leaked fields: %v", row)
		}
		if row["id"] == string(inactiveID) {
			seenInactive = true
		}
		if row["id"] == string(clientID) {
			seenClient = true
		}
	}
	if seenInactive || seenClient || len(clientList) == 0 {
		t.Fatalf("client directory: inactive=%v client=%v rows=%v", seenInactive, seenClient, clientList)
	}

	// Cursor pagination.
	first := f.admin.must(http.StatusOK, "GET", "/api/v1/user-directory?limit=2", nil)
	next, _ := first.json["page"].(map[string]any)["next_cursor"].(string)
	if len(items(t, first)) != 2 || next == "" {
		t.Fatal(first.body)
	}
	second := f.admin.must(http.StatusOK, "GET", "/api/v1/user-directory?limit=2&cursor="+url.QueryEscape(next), nil)
	if items(t, second)[0]["id"] == items(t, first)[0]["id"] {
		t.Fatal("cursor did not advance")
	}
	want(t, f.admin.do("GET", "/api/v1/user-directory?cursor=bogus", nil), http.StatusBadRequest, "cursor")

	// MCP tools for both operations.
	for _, tool := range []string{"getFirmProfile", "listUserDirectory"} {
		call := mcpCall(f.admin, tool, map[string]any{"query": map[string]any{}})
		if strings.Contains(call.body, `"isError":true`) {
			t.Fatalf("MCP %s: %s", tool, call.body)
		}
	}
	if call := mcpCall(f.acct, "listUserDirectory", map[string]any{"query": map[string]any{}}); !strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("accountant MCP directory: %s", call.body)
	}
}
