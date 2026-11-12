package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestReleaseWallHidesMatterEverywhere is AC-003 across the whole contract: a staff user
// outside a matter's wall calls every mounted GET operation (lists, search, widgets,
// reports, feeds, direct reads with the walled records' ids) and every one over /mcp,
// and no response carries any walled record's id or the matter's name. The same user
// sees an open matter's records, so the sweep is not vacuous.
func TestReleaseWallHidesMatterEverywhere(t *testing.T) {
	f := newFin(t)
	walled, walledClient := f.matter("Zephyrine Walled", "zephyr@client.example", nil)
	open, openClient := f.matter("Openly Visible", "open@client.example", nil)
	f.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{f.adminID, f.attyID}})

	day := func(d int) string { return time.Now().Add(time.Duration(d) * 24 * time.Hour).UTC().Format("2006-01-02") }
	at := func(h int) string { return time.Now().Add(time.Duration(h) * time.Hour).UTC().Format(time.RFC3339) }
	seed := func(matter, clientID, tag string) map[string]string {
		ids := map[string]string{"matter_id": matter, "client_id": clientID, "contact_id": clientID}
		ids["note_id"] = str(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/notes", map[string]any{"matter_id": matter, "title": tag + " note", "body": tag}).json, "id")
		ids["time_entry_id"] = f.timeEntry(matter, 4, 250_00)
		ids["invoice_id"] = items(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter}))[0]["id"].(string)
		ids["trust_entry_id"] = f.post(f.atty, matter, clientID, "deposit", 500_00)
		ids["calendar_event_id"] = str(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": tag + " hearing", "kind": "court_date", "starts_at": at(30), "ends_at": at(31), "matter_id": matter, "attendee_user_ids": []string{f.attyID}}).json, "id")
		ids["event_id"] = ids["calendar_event_id"]
		ids["deadline_id"] = str(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": matter, "title": tag + " deadline", "due_on": day(3), "responsible_user_id": f.attyID}).json, "id")
		return ids
	}
	w := seed(walled, walledClient, "Zephyrine")
	o := seed(open, openClient, "Openly")
	secret := []string{"Zephyrine", walled, w["note_id"], w["time_entry_id"], w["invoice_id"], w["trust_entry_id"], w["calendar_event_id"], w["deadline_id"]}
	leak := func(body string) string {
		for _, s := range secret {
			if strings.Contains(body, s) {
				return s
			}
		}
		return ""
	}
	// Queries that make lists, search, reports and calendars cover the seeded records. The
	// search term is a prefix of the name, so an echoed query is never mistaken for a hit.
	query := url.Values{"q": {"Zephyr"}, "from": {day(-30)}, "to": {day(60)}, "start": {day(-30)}, "end": {day(60)},
		"date_from": {day(-30)}, "date_to": {day(60)}, "limit": {"200"}}.Encode()
	staffSaw := ""
	checked := 0
	for _, tl := range f.app.tools {
		if tl.Method != "GET" || !f.app.mounted(tl) {
			continue
		}
		for _, ids := range []map[string]string{w, o} {
			path := tl.Path
			for _, s := range tl.segs {
				if strings.HasPrefix(s, "{") {
					v := ids[strings.Trim(s, "{}")]
					if v == "" {
						v = "01a1c38b-0000-7000-8000-000000000000"
					}
					path = strings.ReplaceAll(path, s, v)
				}
			}
			if ids["matter_id"] == open && path == tl.Path {
				continue // a path without ids is the same call for both matters
			}
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			r := f.staff.do("GET", path+sep+query, nil)
			checked++
			if ids["matter_id"] == walled || path == tl.Path {
				if s := leak(r.body); s != "" {
					t.Errorf("staff outside the wall got %q from GET %s (%d)", s, tl.Path, r.code)
				}
			}
			if ids["matter_id"] == open && r.code == http.StatusOK && strings.Contains(r.body, o["note_id"]) {
				staffSaw = tl.Path
			}
		}
	}
	// The same reads through /mcp.
	for _, tl := range f.app.tools {
		if tl.Method != "GET" || !f.app.mounted(tl) {
			continue
		}
		args := map[string]any{"query": map[string]any{"q": "Zephyr", "from": day(-30), "to": day(60)}}
		pathArgs := map[string]any{}
		for _, s := range tl.segs {
			if strings.HasPrefix(s, "{") {
				name := strings.Trim(s, "{}")
				if v := w[name]; v != "" {
					pathArgs[name] = v
				} else {
					pathArgs[name] = "01a1c38b-0000-7000-8000-000000000000"
				}
			}
		}
		if len(pathArgs) > 0 {
			args["path"] = pathArgs
		}
		if s := leak(mcpCall(f.staff, tl.Name, args).body); s != "" {
			t.Errorf("staff outside the wall got %q from /mcp %s", s, tl.Name)
		}
	}
	if staffSaw == "" {
		t.Fatal("the sweep never showed staff the open matter's note; it proves nothing")
	}
	if r := f.atty.must(http.StatusOK, "GET", "/api/v1/notes/"+w["note_id"], nil); !strings.Contains(r.body, "Zephyrine") {
		t.Fatalf("a user inside the wall lost the note: %s", r.body)
	}
	if r := f.atty.must(http.StatusOK, "GET", "/api/v1/search?q=Zephyr", nil); !strings.Contains(r.body, "Zephyrine") {
		t.Fatalf("search for the walled matter's name finds nothing even inside the wall: %s", r.body)
	}
	t.Logf("%d GET calls and their /mcp tools leaked nothing from the walled matter", checked)
}
