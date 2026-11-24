package coretest_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2IAllDayOccupancy(t *testing.T) {
	c := coretest.New(t)
	install2d(t, c, "all-day-booking", []string{"calendar:read"}, "")
	bot := c.ModuleClient("all-day-booking")
	r := c.Attorney.Must(201, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "PRIVATE TITLE", "notes": "PRIVATE NOTES", "kind": "other", "matter_id": c.Walled,
		"all_day": true, "starts_at": "2026-11-24T00:00:00Z", "ends_at": "2026-11-25T00:00:00Z",
		"video": map[string]any{"requested": true, "provider": "private-provider"},
	})
	from, _ := time.Parse(time.RFC3339, "2026-11-24T00:00:00Z")
	to := from.AddDate(0, 0, 2)
	want := []any{map[string]any{"starts_at": r.JSON["starts_at"], "ends_at": r.JSON["ends_at"], "all_day": true}}
	q := map[string]any{"view": "occupancy", "from_time": from.Format(time.RFC3339), "to_time": to.Format(time.RFC3339)}
	for _, transport := range []string{"REST", "MCP"} {
		var got map[string]any
		if transport == "REST" {
			got = bot.Must(200, "GET", "/api/v1/calendar-events?view=occupancy&from_time="+from.Format(time.RFC3339)+"&to_time="+to.Format(time.RFC3339), nil).JSON
		} else {
			got = toolResult2d(t, bot, "listCalendarEvent", map[string]any{"query": q}, false)
		}
		if !reflect.DeepEqual(got["data"], want) {
			t.Fatalf("%s all-day shape/leak: %v", transport, got)
		}
	}
	blocks := c.CalendarOccupancy("all-day-booking", from, to)
	if len(blocks) != 1 || !blocks[0].AllDay {
		t.Fatalf("public kit all_day: %+v", blocks)
	}
	bot.Must(404, "GET", "/api/v1/calendar-events/"+str(t, r.JSON, "id"), nil)
}

func TestCoreUpdate2IDeactivatedAttendee(t *testing.T) {
	c := coretest.New(t)
	install2d(t, c, "inactive-booking", []string{"calendar:read"}, "")
	bot := c.ModuleClient("inactive-booking")
	event := c.Attorney.Must(201, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "PRIVATE", "kind": "block", "matter_id": c.Walled, "attendee_user_ids": []string{c.Staff.ID},
		"starts_at": "2026-11-24T10:00:00Z", "ends_at": "2026-11-24T11:00:00Z",
	})
	id := str(t, event.JSON, "id")
	read := func(view, transport string) []any {
		t.Helper()
		query := map[string]any{"view": view, "from_time": "2026-11-24T00:00:00Z", "to_time": "2026-11-25T00:00:00Z"}
		if view == "busy" {
			query["user_id"] = c.Attorney.ID
		}
		var result map[string]any
		if transport == "MCP" {
			result = toolResult2d(t, bot, "listCalendarEvent", map[string]any{"query": query}, false)
		} else {
			q := url.Values{}
			for k, v := range query {
				q.Set(k, v.(string))
			}
			result = bot.Must(200, "GET", "/api/v1/calendar-events?"+q.Encode(), nil).JSON
		}
		return result["data"].([]any)
	}
	assert := func(want int) {
		t.Helper()
		for _, view := range []string{"busy", "occupancy"} {
			for _, transport := range []string{"REST", "MCP"} {
				rows := read(view, transport)
				if len(rows) != want {
					t.Fatalf("%s %s got %v want %d", view, transport, rows, want)
				}
				if want == 1 && !reflect.DeepEqual(rows[0], map[string]any{"starts_at": event.JSON["starts_at"], "ends_at": event.JSON["ends_at"], "all_day": false}) {
					t.Fatalf("busy leak: %v", rows)
				}
			}
		}
		for _, who := range []*coretest.Client{bot, c.Attorney.Client, c.Admin.Client} {
			q := url.Values{"view": {"busy"}, "user_id": {c.Attorney.ID}, "from_time": {"2026-11-24T00:00:00Z"}, "to_time": {"2026-11-25T00:00:00Z"}}
			got := who.Must(200, "GET", "/api/v1/calendar-events?"+q.Encode(), nil).JSON["data"].([]any)
			if len(got) != want {
				t.Fatalf("viewer-dependent busy: %v want %d", got, want)
			}
			if want == 1 && !reflect.DeepEqual(got[0], map[string]any{"starts_at": event.JSON["starts_at"], "ends_at": event.JSON["ends_at"], "all_day": false}) {
				t.Fatal(got)
			}
			query := map[string]any{}
			for k := range q {
				query[k] = q.Get(k)
			}
			mcp := toolResult2d(t, who, "listCalendarEvent", map[string]any{"query": query}, false)
			if !reflect.DeepEqual(mcp["data"], got) {
				t.Fatalf("busy transports: %v %v", got, mcp)
			}
		}
		bot.Must(404, "GET", "/api/v1/calendar-events/"+id, nil)
		if rows := bot.Must(200, "GET", "/api/v1/calendar-events", nil).JSON["data"].([]any); len(rows) != 0 {
			t.Fatalf("ordinary list wall: %v", rows)
		}
	}
	assert(0)
	c.Admin.Must(200, "POST", "/api/v1/users/"+c.Staff.ID+"/deactivate", nil)
	assert(1)
	// There is no staff-reactivation HTTP operation. Change the synthetic
	// fixture's authoritative user row to exercise the next-read rule.
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE core.users SET active = 1 WHERE id = $1`, c.Staff.ID); err != nil {
		t.Fatal(err)
	}
	assert(0)
	// A second active attendee still counts when another attendee is deactivated.
	c.Attorney.Must(200, "PATCH", "/api/v1/calendar-events/"+id, map[string]any{"attendee_user_ids": []string{c.Staff.ID, c.Admin.ID}})
	c.Admin.Must(200, "POST", "/api/v1/users/"+c.Staff.ID+"/deactivate", nil)
	assert(0)
}

func TestCoreUpdate2IVideo(t *testing.T) {
	c := coretest.New(t)
	const module = "video-booking"
	var mu sync.Mutex
	var received []map[string]any
	c.Serve(module, func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, body, err := coretest.ReadDelivery(m, r)
			if err != nil {
				t.Error(err)
				w.WriteHeader(401)
				return
			}
			var ev map[string]any
			if err := json.Unmarshal(body, &ev); err != nil {
				t.Error(err)
			}
			if ev["record_type"] == "calendar_event" {
				// Calendar notifications identify the record. Like all other calendar
				// fields, video is fetched through the recipient's normal scoped API.
				result := c.ModuleClient(module).Do("GET", "/api/v1/calendar-events/"+ev["record_id"].(string), nil)
				mu.Lock()
				received = append(received, map[string]any{"action": ev["action"], "code": result.Code, "video": result.JSON["video"]})
				mu.Unlock()
			}
			w.WriteHeader(204)
		})
	})
	install2d(t, c, module, []string{"calendar:read", "calendar:write"}, "")
	bot := c.ModuleClient(module)
	provider := "phone-zoom"
	host := modulekit.ID(c.Attorney.ID)
	video := modulekit.CalendarVideo{Requested: true, Provider: &provider, HostUserID: &host}
	body := map[string]any{"title": "Synthetic video booking", "kind": "client_meeting", "matter_id": c.Open, "starts_at": "2026-11-24T10:00:00Z", "ends_at": "2026-11-24T11:00:00Z", "video": video}
	r := bot.Must(201, "POST", "/api/v1/calendar-events", body)
	id := str(t, r.JSON, "id")
	path := "/api/v1/calendar-events/" + id
	want := map[string]any{"requested": true, "provider": provider, "host_user_id": c.Attorney.ID, "provider_meeting_id": nil}
	if !reflect.DeepEqual(r.JSON["video"], want) {
		t.Fatalf("create video: %v", r.JSON)
	}
	c.DeliverEvents()
	mu.Lock()
	if len(received) != 1 || received[0]["action"] != "calendar_event.created" || received[0]["code"] != 200 || !reflect.DeepEqual(received[0]["video"], want) {
		t.Errorf("created delivery: %v", received)
	}
	mu.Unlock()
	meeting := "opaque-provider-meeting"
	video.ProviderMeetingID = &meeting
	got := toolResult2d(t, bot, "updateCalendarEvent", map[string]any{"path": map[string]any{"calendar_event_id": id}, "body": map[string]any{"video": map[string]any{"provider_meeting_id": meeting}, "location": "https://meet.example.test/join"}}, false)
	want["provider_meeting_id"] = meeting
	if !reflect.DeepEqual(got["video"], want) || got["location"] != "https://meet.example.test/join" {
		t.Fatalf("MCP update: %v", got)
	}
	for _, who := range []*coretest.Client{bot, c.Staff.Client} {
		got := who.Must(200, "GET", path, nil).JSON
		if !reflect.DeepEqual(got["video"], want) {
			t.Fatalf("get: %v", got)
		}
		listed := who.Must(200, "GET", "/api/v1/calendar-events", nil).JSON["data"].([]any)
		if len(listed) != 1 || !reflect.DeepEqual(listed[0].(map[string]any)["video"], want) {
			t.Fatalf("list: %v", listed)
		}
	}
	c.DeliverEvents()
	mu.Lock()
	if len(received) != 2 || received[1]["action"] != "calendar_event.updated" || !reflect.DeepEqual(received[1]["video"], want) {
		t.Errorf("updated delivery: %v", received)
	}
	mu.Unlock()
	// An unrelated staff edit preserves the object; requested is never inferred
	// from presence of the meeting id or location.
	got = c.Staff.Must(200, "PATCH", path, map[string]any{"title": "Renamed video"}).JSON
	if !reflect.DeepEqual(got["video"], want) {
		t.Fatalf("unrelated patch: %v", got)
	}
	video.Requested = false
	got = bot.Must(200, "PATCH", path, map[string]any{"video": video}).JSON
	if got["video"].(map[string]any)["requested"] != false {
		t.Fatal("requested false changed implicitly")
	}
	for _, bad := range []any{
		map[string]any{"requested": "yes"},
		map[string]any{"requested": true, "host_user_id": "not-a-user"},
		map[string]any{"requested": true, "host_user_id": "019b6968-4600-7001-8000-000000000009"},
		map[string]any{"requested": true, "host_user_id": ""},
		map[string]any{"requested": true, "provider_meeting_id": strings.Repeat("a", 201)},
		map[string]any{"requested": true, "unknown": "field"},
		map[string]any{"requested": true, "provider": "Phone-Zoom"}, map[string]any{"requested": true, "provider": "a"},
		map[string]any{"requested": true, "provider": "-phone"}, map[string]any{"requested": true, "provider": "phone-"},
		map[string]any{"requested": true, "provider": ""}, map[string]any{"requested": true, "provider": strings.Repeat("a", 43)},
		map[string]any{"requested": true, "provider": "phone_zoom"}, map[string]any{"requested": true, "provider": "phöne"},
		"video", []any{}, true,
	} {
		invalid := bot.Must(400, "PATCH", path, map[string]any{"video": bad})
		if invalid.JSON["code"] != "invalid_input" {
			t.Fatalf("invalid video: %v", invalid.JSON)
		}
		body["video"] = bad
		bot.Must(400, "POST", "/api/v1/calendar-events", body)
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"requested": nil}} {
		body["video"] = bad
		bot.Must(400, "POST", "/api/v1/calendar-events", body)
	}
	for _, provider := range []any{nil, "ab", strings.Repeat("a", 42)} {
		got := bot.Must(200, "PATCH", path, map[string]any{"video": map[string]any{"provider": provider}}).JSON
		if got["video"].(map[string]any)["provider"] != provider {
			t.Fatalf("provider boundary: %v", got)
		}
		body["video"] = map[string]any{"requested": true, "provider": provider}
		bot.Must(201, "POST", "/api/v1/calendar-events", body)
	}
	c.Admin.Must(200, "POST", "/api/v1/users/"+c.Staff.ID+"/deactivate", nil)
	if result := bot.Must(400, "PATCH", path, map[string]any{"video": map[string]any{"requested": true, "host_user_id": c.Staff.ID}}); result.JSON["code"] != "invalid_input" {
		t.Fatalf("inactive host code: %v", result.JSON)
	}
	body["video"] = map[string]any{"requested": true, "host_user_id": c.Staff.ID}
	bot.Must(400, "POST", "/api/v1/calendar-events", body)
	bot.Must(200, "PATCH", path, map[string]any{"video": map[string]any{"requested": true, "provider": nil, "host_user_id": nil, "provider_meeting_id": strings.Repeat("é", 200)}})
	cleared := bot.Must(200, "PATCH", path, map[string]any{"video": nil})
	if cleared.JSON["video"] != nil {
		t.Fatal("null video did not clear")
	}
	body["video"] = nil
	if bot.Must(201, "POST", "/api/v1/calendar-events", body).JSON["video"] != nil {
		t.Fatal("create null video")
	}
	delete(body, "video")
	if bot.Must(201, "POST", "/api/v1/calendar-events", body).JSON["video"] != nil {
		t.Fatal("create absent video")
	}
	c.Attorney.Must(201, "POST", "/api/v1/calendar-events", map[string]any{"title": "Walled video", "kind": "other", "matter_id": c.Walled, "starts_at": "2026-11-24T12:00:00Z", "ends_at": "2026-11-24T13:00:00Z", "video": video})
	c.DeliverEvents()
	mu.Lock()
	defer mu.Unlock()
	for _, ev := range received {
		if ev["code"] != 200 && (ev["code"] != 404 || ev["video"] != nil) {
			t.Fatalf("notification-driven read disclosed video behind a wall: %v", ev)
		}
	}
}
