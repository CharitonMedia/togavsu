package core

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestMeAppointments covers CORE-15 / MOD-POR-15: a client lists upcoming
// client_meeting and court_date events on readable matters with the field allowlist.
func TestMeAppointments(t *testing.T) {
	f := newFin(t)
	mine, contact := f.matter("Casey Estate", "casey.appointments@client.example", nil)
	other, _ := f.matter("Other Estate", "other.appointments@client.example", nil)

	cli, clientID := enrollUser(t, f.app, f.srv.URL, f.admin, "Casey Client", "casey.appointments.signin@client.example", domain.RoleClient)
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/users/"+string(clientID), map[string]any{"linked_contact_id": contact})
	me := cli.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if str(t, me.json, "linked_contact_id") != contact {
		t.Fatalf("linked_contact_id: %s", me.body)
	}

	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	end := start.Add(time.Hour)
	pastStart := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	pastEnd := pastStart.Add(time.Hour)
	later := start.Add(72 * time.Hour)

	f.admin.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "Intake meeting", "kind": "client_meeting",
		"starts_at": start.Format(time.RFC3339), "ends_at": end.Format(time.RFC3339),
		"location": "Zoom", "matter_id": mine,
	})
	f.admin.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "Probate hearing", "kind": "court_date",
		"starts_at": later.Format(time.RFC3339), "ends_at": later.Add(90 * time.Minute).Format(time.RFC3339),
		"location": "Courtroom 3", "matter_id": mine,
	})
	f.admin.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "Past intake", "kind": "client_meeting",
		"starts_at": pastStart.Format(time.RFC3339), "ends_at": pastEnd.Format(time.RFC3339),
		"matter_id": mine,
	})
	f.admin.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "Prep block", "kind": "block",
		"starts_at": start.Add(24 * time.Hour).Format(time.RFC3339), "ends_at": start.Add(25 * time.Hour).Format(time.RFC3339),
		"matter_id": mine,
	})
	f.admin.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "Other client's meeting", "kind": "client_meeting",
		"starts_at": start.Add(2 * time.Hour).Format(time.RFC3339), "ends_at": start.Add(3 * time.Hour).Format(time.RFC3339),
		"matter_id": other,
	})

	if r := f.staff.do("GET", "/api/v1/me/appointments", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff: %d %s", r.code, r.body)
	}
	if r := cli.do("GET", "/api/v1/calendar-events", nil); r.code != http.StatusForbidden {
		t.Fatalf("client firm calendar: %d %s", r.code, r.body)
	}

	list := cli.must(http.StatusOK, "GET", "/api/v1/me/appointments", nil)
	rows := items(t, list)
	if len(rows) != 2 {
		t.Fatalf("want 2 got %d: %s", len(rows), list.body)
	}
	for _, row := range rows {
		for _, leak := range []string{"id", "notes", "attendee_user_ids", "travel_minutes", "starts_at", "ends_at", "created_at"} {
			if _, ok := row[leak]; ok {
				t.Fatalf("allowlist leaked %s: %v", leak, row)
			}
		}
		for _, need := range []string{"title", "kind", "start", "end", "all_day", "location", "matter_id"} {
			if _, ok := row[need]; !ok {
				t.Fatalf("missing %s: %v", need, row)
			}
		}
		if str(t, row, "matter_id") != mine {
			t.Fatalf("foreign matter: %v", row)
		}
	}
	if str(t, rows[0], "title") != "Intake meeting" || str(t, rows[0], "kind") != "client_meeting" ||
		str(t, rows[0], "location") != "Zoom" {
		t.Fatalf("first: %v", rows[0])
	}
	if str(t, rows[1], "title") != "Probate hearing" || str(t, rows[1], "kind") != "court_date" {
		t.Fatalf("second: %v", rows[1])
	}
	if str(t, rows[0], "start") > str(t, rows[1], "start") {
		t.Fatalf("order: %s", list.body)
	}

	tool := toolName(t, "GET", "/api/v1/me/appointments")
	if call := mcpCall(cli, tool, map[string]any{}); !strings.Contains(call.body, "Intake meeting") ||
		strings.Contains(call.body, `"isError":true`) || strings.Contains(call.body, "Other client's meeting") {
		t.Fatalf("mcp: %s", call.body)
	}
}
