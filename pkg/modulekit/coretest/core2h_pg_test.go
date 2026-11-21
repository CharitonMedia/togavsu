package coretest_test

import (
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

// Exercise the public transports against Postgres, including whole responses and
// cursor pages before/after changing only the wall or attendee assignment.
func TestCoreUpdate2HUnassignedOccupancy(t *testing.T) {
	for _, transport := range []string{"REST", "MCP"} {
		t.Run(transport, func(t *testing.T) {
			c := coretest.New(t)
			const module = "booking-two-h"
			install2d(t, c, module, []string{"calendar:read"}, "")
			bot := c.ModuleClient(module)
			start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
			q := url.Values{"view": {"occupancy"}, "from_time": {start.Add(-time.Hour).Format(time.RFC3339)}, "to_time": {start.Add(6 * time.Hour).Format(time.RFC3339)}}
			copyQuery := func() url.Values {
				v, _ := url.ParseQuery(q.Encode())
				return v
			}
			read := func(query url.Values, status int) map[string]any {
				t.Helper()
				if transport == "REST" {
					return bot.Must(status, "GET", "/api/v1/calendar-events?"+query.Encode(), nil).JSON
				}
				args := map[string]any{}
				for key := range query {
					args[key] = query.Get(key)
				}
				return toolResult2d(t, bot, "listCalendarEvent", map[string]any{"query": args}, status >= 400)
			}
			create := func(matter string, offset time.Duration, attendees []string) string {
				t.Helper()
				r := c.Attorney.Must(201, "POST", "/api/v1/calendar-events", map[string]any{
					"title": "PRIVATE-BOOKING", "notes": "PRIVATE-NOTES", "location": "PRIVATE-LOCATION",
					"kind": "court_date", "matter_id": matter, "attendee_user_ids": attendees,
					"starts_at": start.Add(offset).Format(time.RFC3339), "ends_at": start.Add(offset + time.Hour).Format(time.RFC3339),
				})
				return str(t, r.JSON, "id")
			}
			block := func(offset time.Duration) any {
				return map[string]any{"starts_at": start.Add(offset).Format(time.RFC3339), "ends_at": start.Add(offset + time.Hour).Format(time.RFC3339)}
			}
			open := create(c.Open, 0, nil)
			create(c.Open, 2*time.Hour, []string{c.Attorney.ID})
			// Touching the lower/upper window boundary is not overlap. Hidden
			// unassigned rows use exactly the same window as ordinary candidates.
			create(c.Walled, -2*time.Hour, nil)
			create(c.Walled, 6*time.Hour, nil)
			assigned := create(c.Walled, 4*time.Hour, []string{c.Attorney.ID, c.Staff.ID})
			// Cancellation is DELETE in the real schema/API, for both placements.
			for _, matter := range []string{c.Walled, c.Open} {
				cancelled := create(matter, 5*time.Hour, nil)
				c.Attorney.Must(204, "DELETE", "/api/v1/calendar-events/"+cancelled, nil)
			}
			baseline := read(q, 200)
			if !reflect.DeepEqual(baseline["data"], []any{block(0), block(2 * time.Hour)}) {
				t.Fatalf("assigned walled or cancelled event included: %v", baseline)
			}
			// Give the exception its own wall so lifting it cannot change the assigned event.
			matter, _ := c.Matter("Synthetic unassigned booking", "", nil)
			c.Wall(matter, c.Attorney)
			hidden := create(matter, 0, nil)
			withHidden := read(q, 200)
			want := map[string]any{"data": []any{block(0), block(0), block(2 * time.Hour)}, "page": baseline["page"]}
			if !reflect.DeepEqual(withHidden, want) {
				t.Fatalf("response differs by more than the extra bare interval: got %v want %v", withHidden, want)
			}
			pages := func() []map[string]any {
				t.Helper()
				query := copyQuery()
				query.Set("limit", "1")
				var out []map[string]any
				for i := 0; i < 3; i++ {
					v := read(query, 200)
					out = append(out, v)
					page := v["page"].(map[string]any)
					if len(page) != 2 || page["limit"] != float64(1) || !reflect.DeepEqual(v["data"], want["data"].([]any)[i:i+1]) {
						t.Fatalf("different paging/count/shape: %v", v)
					}
					if i == 2 {
						if page["next_cursor"] != nil {
							t.Fatalf("hidden assigned/cancelled events affected cursor: %v", page)
						}
					} else {
						cursor, ok := page["next_cursor"].(string)
						if !ok || cursor == "" {
							t.Fatalf("missing next cursor: %v", page)
						}
						query.Set("cursor", cursor)
					}
				}
				return out
			}
			beforePages := pages()
			bad := copyQuery()
			bad.Set("matter_id", matter)
			beforeError := read(bad, 400)
			for key, value := range map[string]string{
				"user_id": c.Attorney.ID, "kind": "court_date", "to_time": q.Get("from_time"),
				"from_time": "", "cursor": "invalid",
			} {
				invalid := copyQuery()
				invalid.Set(key, value)
				read(invalid, 400)
			}
			tooWide := copyQuery()
			tooWide.Set("to_time", start.AddDate(10, 0, 0).Format(time.RFC3339))
			read(tooWide, 400)
			// Ordinary service and user reads still enforce the wall. Missing and
			// walled gets return the same error, without membership or event detail.
			assertHidden := func(id string) {
				t.Helper()
				if transport == "REST" {
					got := bot.Must(404, "GET", "/api/v1/calendar-events/"+id, nil).JSON
					missing := bot.Must(404, "GET", "/api/v1/calendar-events/00000000-0000-7000-8000-000000000001", nil).JSON
					if !reflect.DeepEqual(got, missing) {
						t.Fatalf("wall existence oracle: %v %v", got, missing)
					}
				} else {
					got := toolResult2d(t, bot, "getCalendarEvent", map[string]any{"path": map[string]any{"calendar_event_id": id}}, true)
					missing := toolResult2d(t, bot, "getCalendarEvent", map[string]any{"path": map[string]any{"calendar_event_id": "00000000-0000-7000-8000-000000000001"}}, true)
					if !reflect.DeepEqual(got, missing) {
						t.Fatalf("MCP wall existence oracle: %v %v", got, missing)
					}
				}
			}
			assertHidden(hidden)
			assertHidden(assigned)
			c.Staff.Must(404, "GET", "/api/v1/calendar-events/"+hidden, nil)
			ordinary := copyQuery()
			ordinary.Del("view")
			listed := read(ordinary, 200)
			if rows := listed["data"].([]any); len(rows) != 2 || rows[0].(map[string]any)["id"] != open {
				t.Fatalf("ordinary list wall regression: %v", listed)
			}
			c.Wall(matter)
			if !reflect.DeepEqual(read(q, 200), withHidden) || !reflect.DeepEqual(pages(), beforePages) || !reflect.DeepEqual(read(bad, 400), beforeError) {
				t.Fatal("wall removal changed occupancy, metadata, paging or error")
			}
			bot.Must(200, "GET", "/api/v1/calendar-events/"+hidden, nil)
			if len(read(ordinary, 200)["data"].([]any)) != 3 {
				t.Fatal("ordinary service list did not restore record on next read")
			}
			c.Wall(matter, c.Attorney)
			// Any assigned user, including an inactive user, keeps the event out.
			c.Attorney.Must(200, "PATCH", "/api/v1/calendar-events/"+hidden, map[string]any{"attendee_user_ids": []string{c.Staff.ID}})
			c.Admin.Must(200, "POST", "/api/v1/users/"+c.Staff.ID+"/deactivate", nil)
			if !reflect.DeepEqual(read(q, 200), baseline) {
				t.Fatal("assigned inactive attendee treated as unassigned")
			}
			c.Attorney.Must(200, "PATCH", "/api/v1/calendar-events/"+hidden, map[string]any{"attendee_user_ids": []string{}})
			if !reflect.DeepEqual(read(q, 200), withHidden) {
				t.Fatal("clearing last attendee did not restore occupancy immediately")
			}
		})
	}
}

// Compare the transport headers when only a wall changes, guarding cache and
// count metadata independently of the test client's decoded response helper.
func TestCoreUpdate2HOccupancyHeaders(t *testing.T) {
	c := coretest.New(t)
	install2d(t, c, "headers-two-h", []string{"calendar:read"}, "")
	bot := c.ModuleClient("headers-two-h")
	listed := bot.Must(200, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	found := false
	for _, value := range listed.JSON["result"].(map[string]any)["tools"].([]any) {
		tool := value.(map[string]any)
		if tool["name"] == "listCalendarEvent" {
			found = true
			text, _ := tool["description"].(string)
			if !strings.Contains(text, "no assigned attendee user") || !strings.Contains(text, "calendar:read") {
				t.Fatal("MCP tools/list omits occupancy exception")
			}
		}
	}
	if !found {
		t.Fatal("MCP calendar tool missing")
	}
	read := func() (http.Header, string) {
		t.Helper()
		req, err := http.NewRequest("GET", c.URL+"/api/v1/calendar-events?view=occupancy&from_time=2026-11-21T10:00:00Z&to_time=2026-11-21T11:00:00Z", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bot.Bearer)
		r, err := bot.HTTP.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("occupancy response: status %d, read error %v", r.StatusCode, err)
		}
		r.Header.Del("Date")
		return r.Header, string(body)
	}
	c.Attorney.Must(201, "POST", "/api/v1/calendar-events", map[string]any{"title": "PRIVATE", "kind": "block", "matter_id": c.Walled, "starts_at": "2026-11-21T10:00:00Z", "ends_at": "2026-11-21T11:00:00Z"})
	headers, body := read()
	c.Wall(c.Walled)
	openHeaders, openBody := read()
	if !reflect.DeepEqual(headers, openHeaders) || body != openBody {
		t.Fatalf("wall-dependent headers or body: %v %v", headers, openHeaders)
	}
}
