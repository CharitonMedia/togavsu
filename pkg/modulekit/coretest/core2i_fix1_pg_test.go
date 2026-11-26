package coretest_test

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2IVideoPatchAndInactiveHost(t *testing.T) {
	c := coretest.New(t)
	install2d(t, c, "video-fix-booking", []string{"calendar:read", "calendar:write"}, "")
	bot := c.ModuleClient("video-fix-booking")
	event := bot.Must(201, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "Video", "kind": "client_meeting", "starts_at": "2026-11-24T10:00:00Z", "ends_at": "2026-11-24T11:00:00Z",
		"video": map[string]any{"requested": true, "provider": "phone-zoom", "host_user_id": c.Staff.ID},
	})
	id := str(t, event.JSON, "id")
	path := "/api/v1/calendar-events/" + id
	want := event.JSON["video"].(map[string]any)
	c.Admin.Must(200, "POST", "/api/v1/users/"+c.Staff.ID+"/deactivate", nil)
	for _, patch := range []map[string]any{
		{"title": "Renamed", "starts_at": "2026-11-24T10:15:00Z"},
		{"video": map[string]any{}},
		{"video": map[string]any{"host_user_id": c.Staff.ID}},
	} {
		got := bot.Must(200, "PATCH", path, patch).JSON
		if !reflect.DeepEqual(got["video"], want) {
			t.Fatalf("unchanged inactive host: %v", got)
		}
	}
	meeting := "meeting"
	meetingValue := &meeting
	got := toolResult2d(t, bot, "updateCalendarEvent", map[string]any{"path": map[string]any{"calendar_event_id": id}, "body": map[string]any{"video": modulekit.CalendarVideoPatch{ProviderMeetingID: &meetingValue}}}, false)
	want["provider_meeting_id"] = "meeting"
	if !reflect.DeepEqual(got["video"], want) {
		t.Fatalf("partial MCP patch: %v", got)
	}
	for _, field := range []string{"provider", "provider_meeting_id", "host_user_id", "requested"} {
		got := bot.Must(200, "PATCH", path, map[string]any{"video": map[string]any{field: nil}}).JSON
		if field == "requested" {
			want[field] = false
		} else {
			want[field] = nil
		}
		if !reflect.DeepEqual(got["video"], want) {
			t.Fatalf("clear %s: %v", field, got)
		}
	}
	// Clearing the host removes the grandfathered assignment; reassigning it fails.
	invalid := bot.Must(400, "PATCH", path, map[string]any{"video": map[string]any{"host_user_id": c.Staff.ID}})
	if invalid.JSON["code"] != "invalid_input" {
		t.Fatal(invalid.JSON)
	}
	if got := bot.Must(200, "GET", path, nil).JSON; !reflect.DeepEqual(got["video"], want) {
		t.Fatal(got)
	}
	bot.Must(200, "PATCH", path, map[string]any{"video": nil})
	got = bot.Must(200, "PATCH", path, map[string]any{"video": map[string]any{"provider_meeting_id": "new-meeting"}}).JSON
	want = map[string]any{"requested": false, "provider": nil, "host_user_id": nil, "provider_meeting_id": "new-meeting"}
	if !reflect.DeepEqual(got["video"], want) {
		t.Fatalf("patch onto absent video: %v", got)
	}
	// A newly assigned active host works after clearing the old one.
	bot.Must(200, "PATCH", path, map[string]any{"video": map[string]any{"host_user_id": c.Attorney.ID}})
}

func TestCoreUpdate2IMigrationAllocation(t *testing.T) {
	c := coretest.New(t)
	pool, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for version, task := range map[int]string{1650: "core-update-2h", 1699: "core-update-2h", 1700: "core-update-2i", 1749: "core-update-2i"} {
		if got, ok := db.TaskForVersion(version); !ok || got != task {
			t.Fatalf("version %d: %s", version, got)
		}
	}
	if err := db.ValidateMigrationVersions([]db.Migration{{Version: 1700}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM core.schema_migrations WHERE owner='core' AND version=1700`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration missing: %d %v", count, err)
	}
	if err := pool.QueryRow(`SELECT COUNT(*) FROM core.schema_migrations WHERE owner='core' AND version=1650`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old migration remains: %d %v", count, err)
	}
	var video sql.NullString
	if err := pool.QueryRow(`SELECT video FROM core.calendar_events LIMIT 1`).Scan(&video); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
}

func TestCoreUpdate2IBusyViewerIndependent(t *testing.T) {
	c := coretest.New(t)
	install2d(t, c, "busy-fix-booking", []string{"calendar:read"}, "")
	body := map[string]any{"title": "Private", "kind": "block", "matter_id": c.Walled, "starts_at": "2026-11-24T10:00:00Z", "ends_at": "2026-11-24T11:00:00Z"}
	event := c.Attorney.Must(201, "POST", "/api/v1/calendar-events", body)
	body["matter_id"] = c.Open
	c.Attorney.Must(201, "POST", "/api/v1/calendar-events", body)
	query := map[string]any{"view": "busy", "user_id": c.Admin.ID, "from_time": "2026-11-24T00:00:00Z", "to_time": "2026-11-25T00:00:00Z"}
	path := "/api/v1/calendar-events?view=busy&user_id=" + c.Admin.ID + "&from_time=2026-11-24T00:00:00Z&to_time=2026-11-25T00:00:00Z"
	want := []any{}
	for _, who := range []*coretest.Client{c.ModuleClient("busy-fix-booking"), c.Staff.Client, c.Admin.Client, c.Attorney.Client} {
		got := who.Must(200, "GET", path, nil).JSON["data"]
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("unassigned busy differs by caller: %v", got)
		}
		mcp := toolResult2d(t, who, "listCalendarEvent", map[string]any{"query": query}, false)
		if !reflect.DeepEqual(mcp["data"], want) {
			t.Fatal(mcp)
		}
	}
	bot := c.ModuleClient("busy-fix-booking")
	occupancy := []any{
		map[string]any{"starts_at": event.JSON["starts_at"], "ends_at": event.JSON["ends_at"], "all_day": false},
		map[string]any{"starts_at": event.JSON["starts_at"], "ends_at": event.JSON["ends_at"], "all_day": false},
	}
	query["view"] = "occupancy"
	delete(query, "user_id")
	got := bot.Must(200, "GET", "/api/v1/calendar-events?view=occupancy&from_time=2026-11-24T00:00:00Z&to_time=2026-11-25T00:00:00Z", nil).JSON["data"]
	mcp := toolResult2d(t, bot, "listCalendarEvent", map[string]any{"query": query}, false)
	if !reflect.DeepEqual(got, occupancy) || !reflect.DeepEqual(mcp["data"], occupancy) {
		t.Fatalf("unassigned intervals missing from occupancy: REST %v MCP %v", got, mcp)
	}
	// Busy still includes a walled interval assigned to the requested user.
	c.Attorney.Must(200, "PATCH", "/api/v1/calendar-events/"+str(t, event.JSON, "id"), map[string]any{"attendee_user_ids": []string{c.Admin.ID}})
	assigned := c.Staff.Must(200, "GET", path, nil).JSON["data"]
	if !reflect.DeepEqual(assigned, occupancy[:1]) {
		t.Fatalf("assigned busy interval missing: %v", assigned)
	}
	c.Staff.Must(404, "GET", "/api/v1/calendar-events/"+str(t, event.JSON, "id"), nil)
	c.Attorney.Must(200, "GET", "/api/v1/calendar-events/"+str(t, event.JSON, "id"), nil)
}
