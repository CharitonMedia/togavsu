package core

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestReleasePerfPagingKeepsWalls covers the reads AC-017 bounded: the matters API pages
// with its contract cursor, the calendar events API reads only the page it returns, the
// calendar's overdue panel shows a page and counts the rest, and the matters screen pages
// with a next link. Walking every page returns exactly the full list, a walled matter's
// rows never reach a user outside the wall on any page, and a bad cursor is refused.
func TestReleasePerfPagingKeepsWalls(t *testing.T) {
	app, srv := start(t, testDSN(t))
	admin := firstRun(t, srv.URL)
	adminID := str(t, admin.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	staff, _ := enrollUser(t, app, srv.URL, admin, "Sid", "sid@firm.example", "staff")
	_, attyID := enrollUser(t, app, srv.URL, admin, "Ann", "ann@firm.example", "attorney")
	area := str(t, admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Family", "stages": []string{"Intake"}}).json, "id")
	var matters []string
	for i := 0; i < 6; i++ {
		matters = append(matters, str(t, admin.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{"name": fmt.Sprintf("Paging %d", i),
			"practice_area_id": area, "billing_type": "hourly", "responsible_attorney_user_id": attyID}).json, "id"))
	}
	walled := matters[5]
	admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{adminID}})
	day := func(d int) time.Time { return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, d) }
	for i, m := range matters {
		for k := 0; k < 2; k++ {
			at := day(i*2 + k + 1).Add(14 * time.Hour)
			admin.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": fmt.Sprintf("Hearing %d.%d", i, k), "kind": "court_date",
				"starts_at": at.Format(time.RFC3339), "ends_at": at.Add(time.Hour).Format(time.RFC3339), "matter_id": m})
		}
		admin.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": m, "title": fmt.Sprintf("Overdue %d", i),
			"due_on": day(-i - 1).Format("2006-01-02"), "responsible_user_id": attyID})
	}

	// walk follows next_cursor at limit 2 and returns every id it saw, in order.
	walk := func(path string) []string {
		t.Helper()
		var ids []string
		next := ""
		for pages := 0; ; pages++ {
			q := "limit=2"
			if next != "" {
				q += "&cursor=" + url.QueryEscape(next)
			}
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			r := staff.must(http.StatusOK, "GET", path+sep+q, nil)
			for _, row := range r.json["data"].([]any) {
				ids = append(ids, row.(map[string]any)["id"].(string))
			}
			pg := r.json["page"].(map[string]any)
			if n, _ := pg["next_cursor"].(string); n != "" && pages < 50 {
				next = n
				continue
			}
			return ids
		}
	}
	all := func(path string) []string {
		t.Helper()
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		var ids []string
		for _, row := range staff.must(http.StatusOK, "GET", path+sep+"limit=200", nil).json["data"].([]any) {
			ids = append(ids, row.(map[string]any)["id"].(string))
		}
		return ids
	}
	from, to := url.QueryEscape(day(0).Format(time.RFC3339)), url.QueryEscape(day(9).Format(time.RFC3339))
	for _, path := range []string{"/api/v1/matters", "/api/v1/calendar-events", "/api/v1/calendar-events?from_time=" + from + "&to_time=" + to} {
		paged, full := walk(path), all(path)
		if strings.Join(paged, ",") != strings.Join(full, ",") {
			t.Fatalf("%s: pages %v, full list %v", path, paged, full)
		}
		if len(full) < 4 {
			t.Fatalf("%s: only %d rows", path, len(full))
		}
	}
	if got := all("/api/v1/matters"); len(got) != 5 || strings.Contains(strings.Join(got, ","), walled) {
		t.Fatalf("staff matters: %v", got)
	}
	for _, row := range staff.must(http.StatusOK, "GET", "/api/v1/calendar-events?limit=200", nil).json["data"].([]any) {
		if row.(map[string]any)["matter_id"] == walled {
			t.Fatal("staff listed an event on a walled matter")
		}
	}
	window := all("/api/v1/calendar-events?from_time=" + from + "&to_time=" + to)
	if len(window) != 8 { // days 1..8 of the five visible matters' ten events
		t.Fatalf("window: %d events", len(window))
	}
	if r := staff.do("GET", "/api/v1/matters?cursor=bogus", nil); r.code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d", r.code)
	}

	// Screens: a page of two, a next link, and a count of the overdue deadlines the panel
	// does not show; the walled matter's deadline is in neither.
	admin.must(http.StatusOK, "PATCH", "/api/v1/settings/api.page_size", map[string]any{"value": 2})
	cal := staff.must(http.StatusOK, "GET", "/calendar", nil).body
	if !strings.Contains(cal, "3 more overdue") || strings.Contains(cal, "Overdue 5") {
		t.Fatalf("overdue panel: want 2 shown and 3 more of 5 visible, walled one absent")
	}
	shown := 0
	for i := 0; i < 5; i++ {
		if strings.Contains(cal, fmt.Sprintf(">Overdue %d<", i)) {
			shown++
		}
	}
	if shown != 2 {
		t.Fatalf("overdue panel shows %d deadlines", shown)
	}
	var names []string
	page := staff.must(http.StatusOK, "GET", "/practice/matters", nil).body
	for i := 0; i < 6; i++ {
		if strings.Contains(page, fmt.Sprintf(">Paging %d<", i)) {
			names = append(names, fmt.Sprint(i))
		}
	}
	if len(names) != 2 || !strings.Contains(page, "Next page") {
		t.Fatalf("matters screen: %v shown, next link %v", names, strings.Contains(page, "Next page"))
	}
	seen := map[string]bool{}
	next := "/practice/matters"
	for pages := 0; next != "" && pages < 10; pages++ {
		body := staff.must(http.StatusOK, "GET", next, nil).body
		for i := 0; i < 6; i++ {
			if strings.Contains(body, fmt.Sprintf(">Paging %d<", i)) {
				seen[fmt.Sprint(i)] = true
			}
		}
		next = ""
		if at := strings.Index(body, `">Next page</a>`); at >= 0 {
			start := strings.LastIndex(body[:at], `href="`) + len(`href="`)
			next = strings.ReplaceAll(body[start:at], "&amp;", "&")
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "0,1,2,3,4" {
		t.Fatalf("matters screen pages showed %v; want every visible matter and not the walled one", keys)
	}
}
