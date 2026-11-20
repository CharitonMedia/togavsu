package coretest_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func narrowService2f(t *testing.T, c *coretest.Core, id string) *coretest.Client {
	t.Helper()
	r := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{
		"name": "narrow service acceptance", "module_id": id, "scopes": []string{"modules:read"},
		"approved_scopes": []string{"modules:read"},
	})
	client := c.NewClient()
	client.Bearer = str(t, r.JSON, "secret")
	return client
}

func TestCoreUpdate2FBusyAcrossWalls(t *testing.T) {
	c := coretest.New(t)
	const id = "availability-two-f"
	c.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	install2d(t, c, id, []string{"calendar:read"}, "")
	bot := c.ModuleClient(id)
	start := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	hidden := c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "PRIVATE-HEARING-TITLE", "kind": "court_date", "matter_id": c.Walled,
		"starts_at": start.Format(time.RFC3339), "ends_at": start.Add(time.Hour).Format(time.RFC3339),
		"attendee_user_ids": []string{c.Attorney.ID}, "location": "PRIVATE-LOCATION", "notes": "PRIVATE-NOTES",
	})
	hiddenID := str(t, hidden.JSON, "id")
	q := url.Values{"view": {"busy"}, "user_id": {c.Attorney.ID}, "from_time": {start.Add(-time.Hour).Format(time.RFC3339)}, "to_time": {start.Add(3 * time.Hour).Format(time.RFC3339)}}
	for _, transport := range []string{"REST", "MCP"} {
		t.Run(transport, func(t *testing.T) {
			var value map[string]any
			if transport == "MCP" {
				query := map[string]any{}
				for key := range q {
					query[key] = q.Get(key)
				}
				value = toolResult2d(t, bot, "listCalendarEvent", map[string]any{"query": query}, false)
			} else {
				value = bot.Must(http.StatusOK, "GET", "/api/v1/calendar-events?"+q.Encode(), nil).JSON
			}
			rows, ok := value["data"].([]any)
			if !ok {
				t.Fatalf("missing busy data: %v", value)
			}
			if len(rows) != 1 {
				t.Fatalf("missing walled occupancy: %v", value)
			}
			row := rows[0].(map[string]any)
			if len(row) != 3 || row["starts_at"] != start.Format(time.RFC3339) || row["ends_at"] != start.Add(time.Hour).Format(time.RFC3339) || row["all_day"] != false {
				t.Fatalf("busy block contains detail or has wrong interval: %v", row)
			}
		})
	}
	bot.Must(http.StatusNotFound, "GET", "/api/v1/calendar-events/"+hiddenID, nil)
	narrowService2f(t, c, id).Must(http.StatusForbidden, "GET", "/api/v1/calendar-events?"+q.Encode(), nil)
	ordinary := bot.Must(http.StatusOK, "GET", "/api/v1/calendar-events", nil)
	if strings.Contains(ordinary.Body, hiddenID) || strings.Contains(ordinary.Body, "PRIVATE-") || strings.Contains(ordinary.Body, c.Walled) {
		t.Fatalf("ordinary read leaked: %s", ordinary.Body)
	}
	for _, edit := range []func(url.Values){
		func(v url.Values) { v.Del("user_id") },
		func(v url.Values) { v.Set("user_id", "missing-user") },
		func(v url.Values) { v.Del("from_time") },
		func(v url.Values) { v.Set("to_time", v.Get("from_time")) },
		func(v url.Values) { v.Set("to_time", start.AddDate(10, 0, 0).Format(time.RFC3339)) },
		func(v url.Values) { v.Set("matter_id", c.Walled) },
		func(v url.Values) { v.Set("kind", "court_date") },
	} {
		v := url.Values{}
		for key, values := range q {
			v[key] = append([]string{}, values...)
		}
		edit(v)
		r := bot.Do("GET", "/api/v1/calendar-events?"+v.Encode(), nil)
		if r.Code != http.StatusBadRequest || strings.Contains(r.Body, "PRIVATE-") {
			t.Fatalf("invalid busy request: %d %s", r.Code, r.Body)
		}
	}
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/users/"+c.Staff.ID+"/deactivate", nil)
	q.Set("user_id", c.Staff.ID)
	bot.Must(http.StatusBadRequest, "GET", "/api/v1/calendar-events?"+q.Encode(), nil)
	q.Set("user_id", c.Attorney.ID)
	c.Client.Must(http.StatusForbidden, "GET", "/api/v1/calendar-events?"+q.Encode(), nil)
	c.Accountant.Must(http.StatusForbidden, "GET", "/api/v1/calendar-events?"+q.Encode(), nil)
}

func TestCoreUpdate2FNamedConflictService(t *testing.T) {
	c := coretest.New(t)
	const id = "conflicts-two-f"
	c.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	install2d(t, c, id, []string{"conflicts:read", "conflicts:write"}, "")
	bot := c.ModuleClient(id)
	for _, name := range []string{"Riley Additional Person", "Morgan Additional Person"} {
		c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/conflict-records", map[string]any{"name": name, "reason": "PRIVATE-ATTORNEY-REASON"})
	}
	c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/conflict-records", map[string]any{"name": "Secret Walled Person", "matter_id": c.Walled, "reason": "PRIVATE-WALLED-REASON"})
	created := bot.Must(http.StatusCreated, "POST", "/api/v1/conflict-records", map[string]any{"name": "Registered Additional Person", "matter_id": c.Open, "source": "spoofed-source"})
	if created.JSON["created_by"] != id || created.JSON["source"] != "service" {
		t.Fatalf("background registration author/source: %s", created.Body)
	}
	if c.Count(`SELECT COUNT(*) FROM core.audit_log WHERE entity='conflict_record' AND entity_id=$1 AND actor_id=$2 AND source='service'`, str(t, created.JSON, "id"), id) == 0 {
		t.Fatal("conflict domain audit did not name the service module")
	}
	path := "/api/v1/matters/" + c.Open + "/conflict-checks"
	names := []string{"Riley Additional Person", "Morgan Additional Person", "Secret Walled Person"}
	checked := bot.Must(http.StatusCreated, "POST", path, map[string]any{"names": names})
	if checked.JSON["created_by"] != id {
		t.Fatalf("background check author: %s", checked.Body)
	}
	for _, name := range names[:2] {
		found := false
		for _, value := range checked.JSON["hits"].([]any) {
			if value.(map[string]any)["name"] == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("additional name %q not checked: %s", name, checked.Body)
		}
	}
	if strings.Contains(checked.Body, "PRIVATE-") || strings.Contains(checked.Body, c.Walled) {
		t.Fatalf("conflict result leaked attorney/walled detail: %s", checked.Body)
	}
	for _, value := range checked.JSON["hits"].([]any) {
		if value.(map[string]any)["name"] == "Secret Walled Person" {
			t.Fatalf("walled hit returned: %s", checked.Body)
		}
	}
	bot.Must(http.StatusNotFound, "POST", "/api/v1/matters/"+c.Walled+"/conflict-checks", map[string]any{"names": names})
	bot.Must(http.StatusNotFound, "POST", "/api/v1/conflict-records", map[string]any{"name": "Denied", "matter_id": c.Walled})
	bot.Must(http.StatusForbidden, "POST", "/api/v1/conflict-records", map[string]any{"name": "Denied", "reason": "Service cannot set attorney-only reasons"})
	bot.Must(http.StatusBadRequest, "POST", path, map[string]any{"names": []string{" "}})
	bot.Must(http.StatusBadRequest, "POST", path, map[string]any{"names": []any{12}})
	narrow := narrowService2f(t, c, id)
	narrow.Must(http.StatusForbidden, "POST", path, map[string]any{"names": names})
	narrow.Must(http.StatusForbidden, "POST", "/api/v1/conflict-records", map[string]any{"name": "Denied missing scope"})
	// Prior modules still send an empty body when checking current matter parties.
	bot.Must(http.StatusCreated, "POST", path, nil)
	toolResult2d(t, bot, "runConflictCheck", map[string]any{"path": map[string]any{"matter_id": c.Open}, "body": map[string]any{"names": names}}, false)
	toolResult2d(t, bot, "createConflictRecord", map[string]any{"body": map[string]any{"name": "MCP Additional Person", "matter_id": c.Open}}, false)
}

func TestCoreUpdate2FCountyService(t *testing.T) {
	c := coretest.New(t)
	const id = "county-two-f"
	c.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	install2d(t, c, id, []string{"settings:read"}, "")
	county := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/counties", map[string]any{"name": "Testcounty", "state": "MO"})
	idCounty := str(t, county.JSON, "id")
	bot := c.ModuleClient(id)
	got := bot.Must(http.StatusOK, "GET", "/api/v1/counties/"+idCounty, nil)
	if got.JSON["name"] != "Testcounty" {
		t.Fatalf("county read: %s", got.Body)
	}
	toolResult2d(t, bot, "getCounty", map[string]any{"path": map[string]any{"county_id": idCounty}}, false)
	bot.Must(http.StatusNotFound, "GET", "/api/v1/counties/019a0000-0000-7000-8000-000000000000", nil)
	bot.Must(http.StatusForbidden, "PATCH", "/api/v1/counties/"+idCounty, map[string]any{"name": "Unauthorized"})
	narrowService2f(t, c, id).Must(http.StatusForbidden, "GET", "/api/v1/counties/"+idCounty, nil)
}

func TestCoreUpdate2FAdditionalConflictNames(t *testing.T) {
	c := coretest.New(t)
	name := "Alex Named Additional Person"
	c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/conflict-records", map[string]any{"name": name})
	path := "/api/v1/matters/" + c.Open + "/conflict-checks"
	check := c.Admin.Must(http.StatusCreated, "POST", path, map[string]any{"names": []string{name}})
	found := false
	for _, value := range check.JSON["hits"].([]any) {
		if value.(map[string]any)["name"] == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("additional name was ignored: %s", check.Body)
	}
	saved := c.Admin.Must(http.StatusOK, "GET", "/api/v1/conflict-checks/"+str(t, check.JSON, "id"), nil)
	if names, ok := saved.JSON["names"].([]any); !ok || len(names) != 1 || names[0] != name {
		t.Fatalf("checked names not retained: %s", saved.Body)
	}
}
