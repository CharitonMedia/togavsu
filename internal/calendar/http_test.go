package calendar

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const testCSRF = "0123456789abcdef0123456789abcdef"

func send(t *testing.T, srv string, user domain.Principal, method, path, body string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if !user.UserID.IsZero() {
		req.Header.Set("X-Test-User", string(user.UserID))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func postForm(t *testing.T, srv string, user domain.Principal, path string, form url.Values) (int, string) {
	t.Helper()
	form.Set("csrf", testCSRF)
	req, err := http.NewRequest(http.MethodPost, srv+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", csrfCookie+"="+testCSRF)
	req.Header.Set("X-Test-User", string(user.UserID))
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusSeeOther {
		return res.StatusCode, res.Header.Get("Location")
	}
	return res.StatusCode, string(b)
}

func TestEventAPIRoundTripAndAccess(t *testing.T) {
	f := newFixture(t)
	srv := f.server()
	m := f.matter("Open case")
	body := `{"title":"Status conference","kind":"court_date","starts_at":"2026-11-18T15:00:00Z","ends_at":"2026-11-18T16:00:00Z","matter_id":"` + string(m.ID) + `","attendee_user_ids":["` + string(f.attorney.UserID) + `"],"location":"Div. 3","travel_minutes":20}`
	status, out := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/calendar-events", body, map[string]string{"Idempotency-Key": "k1"})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, out)
	}
	var e map[string]any
	_ = json.Unmarshal([]byte(out), &e)
	id := e["id"].(string)
	if e["starts_at"] != "2026-11-18T15:00:00Z" || e["matter_id"] != string(m.ID) || e["travel_minutes"].(float64) != 20 {
		t.Fatalf("shape: %s", out)
	}
	// Same key, same body: replayed, no second event.
	status, again := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/calendar-events", body, map[string]string{"Idempotency-Key": "k1"})
	if status != http.StatusCreated || again != out {
		t.Fatalf("replay: %d", status)
	}
	if rows, _ := f.store.Events(f.ctx); len(rows) != 1 {
		t.Fatalf("replay wrote %d events", len(rows))
	}
	// Same key, different body: conflict.
	if status, _ := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/calendar-events", strings.Replace(body, "Status", "Other", 1), map[string]string{"Idempotency-Key": "k1"}); status != http.StatusConflict {
		t.Fatalf("key reuse: %d", status)
	}
	// Browser session writes need the CSRF header.
	if status, _ := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/calendar-events", body, map[string]string{"Cookie": csrfCookie + "=" + testCSRF}); status != http.StatusForbidden {
		t.Fatalf("csrf missing: %d", status)
	}
	if status, _ := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/calendar-events", body, map[string]string{"Cookie": csrfCookie + "=" + testCSRF, "X-CSRF-Token": testCSRF}); status != http.StatusCreated {
		t.Fatalf("csrf ok: %d", status)
	}
	// Patch and get.
	status, out = send(t, srv.URL, f.staff, http.MethodPatch, "/api/v1/calendar-events/"+id, `{"location":"Div. 4"}`, nil)
	if status != http.StatusOK || !strings.Contains(out, "Div. 4") {
		t.Fatalf("patch: %d %s", status, out)
	}
	// Validation failures.
	for _, bad := range []string{
		`{"title":"","kind":"other","starts_at":"2026-11-18T15:00:00Z","ends_at":"2026-11-18T16:00:00Z"}`,
		`{"title":"x","kind":"party","starts_at":"2026-11-18T15:00:00Z","ends_at":"2026-11-18T16:00:00Z"}`,
		`{"title":"x","kind":"other","starts_at":"2026-11-18T17:00:00Z","ends_at":"2026-11-18T16:00:00Z"}`,
		`{"title":"x","kind":"other","starts_at":"tomorrow","ends_at":"2026-11-18T16:00:00Z"}`,
		`{"title":"x","kind":"other","ends_at":"2026-11-18T16:00:00Z"}`,
		`{"title":"x","kind":"other","starts_at":"2026-11-18T15:00:00Z","ends_at":"2026-11-18T16:00:00Z","attendee_user_ids":["` + string(tid(999)) + `"]}`,
		`{"title":"x","kind":"other","starts_at":"2026-11-18T15:00:00Z","ends_at":"2026-11-18T16:00:00Z","travel_minutes":-5}`,
		`{"title":"x","kind":"other","starts_at":"2026-11-18T15:00:00Z","ends_at":"2026-11-18T16:00:00Z","surprise":1}`,
	} {
		if status, out := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/calendar-events", bad, nil); status != http.StatusBadRequest {
			t.Errorf("bad body %s: %d %s", bad, status, out)
		}
	}
	// Accountant and client get no calendar; signed out is 401.
	for _, p := range []domain.Principal{f.accountant, f.client} {
		if status, _ := send(t, srv.URL, p, http.MethodGet, "/api/v1/calendar-events", "", nil); status != http.StatusForbidden {
			t.Fatalf("%v list: %d", p.Roles, status)
		}
	}
	if status, _ := send(t, srv.URL, domain.Principal{}, http.MethodGet, "/api/v1/calendar-events/"+id, "", nil); status != http.StatusUnauthorized {
		t.Fatal("signed out read")
	}
	// Delete, then it is gone.
	if status, _ := send(t, srv.URL, f.staff, http.MethodDelete, "/api/v1/calendar-events/"+id, "", nil); status != http.StatusNoContent {
		t.Fatalf("delete: %d", status)
	}
	if status, _ := send(t, srv.URL, f.staff, http.MethodGet, "/api/v1/calendar-events/"+id, "", nil); status != http.StatusNotFound {
		t.Fatalf("after delete: %d", status)
	}
}

func TestListPaginationAndViews(t *testing.T) {
	f := newFixture(t)
	srv := f.server()
	for i := 0; i < 5; i++ {
		f.event(f.staff, EventInput{Title: "E" + string(rune('A'+i)), Kind: KindOther, StartsAt: at(t, "2026-11-16 09:00").AddDate(0, 0, i), EndsAt: at(t, "2026-11-16 10:00").AddDate(0, 0, i)})
	}
	var seen []string
	path := "/api/v1/calendar-events?limit=2"
	for i := 0; i < 5 && path != ""; i++ {
		status, out := send(t, srv.URL, f.staff, http.MethodGet, path, "", nil)
		if status != http.StatusOK {
			t.Fatalf("page: %d %s", status, out)
		}
		var res struct {
			Data []map[string]any `json:"data"`
			Page struct {
				Limit int     `json:"limit"`
				Next  *string `json:"next_cursor"`
			} `json:"page"`
		}
		_ = json.Unmarshal([]byte(out), &res)
		for _, d := range res.Data {
			seen = append(seen, d["title"].(string))
		}
		path = ""
		if res.Page.Next != nil {
			path = "/api/v1/calendar-events?limit=2&cursor=" + *res.Page.Next
		}
	}
	if strings.Join(seen, "") != "EAEBECEDEE" {
		t.Fatalf("pages: %v", seen)
	}
	if status, _ := send(t, srv.URL, f.staff, http.MethodGet, "/api/v1/calendar-events?cursor=garbage", "", nil); status != http.StatusBadRequest {
		t.Fatal("bad cursor accepted")
	}
	// view=week from a Wednesday covers Monday through Sunday in the firm zone.
	status, out := send(t, srv.URL, f.staff, http.MethodGet, "/api/v1/calendar-events?view=week&from_time=2026-11-18T12:00:00-06:00", "", nil)
	if status != http.StatusOK || strings.Count(out, `"title"`) != 5 {
		t.Fatalf("week: %d %s", status, out)
	}
	status, out = send(t, srv.URL, f.staff, http.MethodGet, "/api/v1/calendar-events?view=day&from_time=2026-11-18T12:00:00-06:00", "", nil)
	if status != http.StatusOK || strings.Count(out, `"title"`) != 1 || !strings.Contains(out, `"EC"`) {
		t.Fatalf("day: %d %s", status, out)
	}
	if status, _ := send(t, srv.URL, f.staff, http.MethodGet, "/api/v1/calendar-events?view=year&from_time=2026-11-18T12:00:00Z", "", nil); status != http.StatusBadRequest {
		t.Fatal("bad view accepted")
	}
}

func TestBulkRejectsWholeBatch(t *testing.T) {
	f := newFixture(t)
	srv := f.server()
	m := f.matter("Bulk case")
	good := `{"matter_id":"` + string(m.ID) + `","title":"One","due_on":"2026-12-01","responsible_user_id":"` + string(f.attorney.UserID) + `"}`
	bad := `{"matter_id":"` + string(m.ID) + `","title":"Two","due_on":"2026-13-01","responsible_user_id":"` + string(f.attorney.UserID) + `"}`
	status, out := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/deadlines:bulk", `{"items":[`+good+`,`+bad+`]}`, nil)
	if status != http.StatusBadRequest || !strings.Contains(out, "items[1]") {
		t.Fatalf("bulk bad: %d %s", status, out)
	}
	if rows, _ := f.store.Deadlines(f.ctx); len(rows) != 0 {
		t.Fatal("bulk wrote part of a failed batch")
	}
	status, out = send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/deadlines:bulk", `{"items":[`+good+`,`+good+`]}`, nil)
	if status != http.StatusCreated || strings.Count(out, `"id"`) != 2 {
		t.Fatalf("bulk good: %d %s", status, out)
	}
	ev := `{"title":"A","kind":"other","starts_at":"2026-11-18T15:00:00Z","ends_at":"2026-11-18T16:00:00Z"}`
	evBad := `{"title":"B","kind":"other","starts_at":"2026-11-18T15:00:00Z","ends_at":"2026-11-18T14:00:00Z"}`
	if status, _ := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/calendar-events:bulk", `{"items":[`+ev+`,`+evBad+`]}`, nil); status != http.StatusBadRequest {
		t.Fatalf("event bulk bad: %d", status)
	}
	if rows, _ := f.store.Events(f.ctx); len(rows) != 0 {
		t.Fatal("event bulk wrote part of a failed batch")
	}
	if status, _ := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/calendar-events:bulk", `{"items":[]}`, nil); status != http.StatusBadRequest {
		t.Fatal("empty batch accepted")
	}
}

func TestCalendarPagesRender(t *testing.T) {
	f := newFixture(t)
	srv := f.server()
	open := f.matter("Open case")
	secret := f.walled("Sealed case")
	f.event(f.staff, EventInput{Title: "Visible hearing", Kind: KindCourtDate, MatterID: open.ID, StartsAt: at(t, "2026-11-10 09:00"), EndsAt: at(t, "2026-11-10 10:00"), Attendees: []domain.ID{f.staff.UserID}})
	f.event(f.attorney, EventInput{Title: "Sealed hearing", Kind: KindCourtDate, MatterID: secret.ID, StartsAt: at(t, "2026-11-10 11:00"), EndsAt: at(t, "2026-11-10 12:00")})
	f.deadline(f.staff, DeadlineInput{MatterID: open.ID, Title: "Late answer", DueOn: "2026-11-02", ResponsibleUserID: f.staff.UserID})
	for _, path := range []string{"/calendar", "/calendar?view=day&date=today", "/calendar?view=month&date=2026-11-10", "/calendar?view=week&user_id=" + string(f.staff.UserID), "/calendar?kind=court_date"} {
		status, body := get(t, srv.URL, f.staff, path)
		if status != http.StatusOK || !strings.Contains(body, "Visible hearing") {
			t.Fatalf("%s: %d", path, status)
		}
		if strings.Contains(body, "Sealed") {
			t.Fatalf("%s leaked a walled event", path)
		}
		if !strings.Contains(body, "Overdue deadlines") || !strings.Contains(body, "Late answer") {
			t.Fatalf("%s lacks the overdue panel", path)
		}
	}
	if status, body := get(t, srv.URL, f.attorney, "/calendar?view=day&date=2026-11-10"); status != http.StatusOK || !strings.Contains(body, "Sealed hearing") {
		t.Fatal("attorney on the wall should see the event")
	}
	if status, body := get(t, srv.URL, f.staff, "/calendar?kind=client_meeting"); status != http.StatusOK || strings.Contains(body, "Visible hearing") {
		t.Fatal("kind filter ignored")
	}
	if status, _ := get(t, srv.URL, f.staff, "/calendar?date=tomorrow"); status != http.StatusBadRequest {
		t.Fatalf("bad date: %d", status)
	}
	if status, _ := get(t, srv.URL, f.accountant, "/calendar"); status != http.StatusForbidden {
		t.Fatalf("accountant page: %d", status)
	}
	for _, path := range []string{"/calendar/static/calendar.css", "/calendar/static/calendar.js", "/calendar/feeds", "/calendar/available-dates", "/calendar/deadlines"} {
		if status, _ := get(t, srv.URL, f.staff, path); status != http.StatusOK {
			t.Fatalf("%s: %d", path, status)
		}
	}
	// Form create of an event and a deadline, then mark it done.
	status, loc := postForm(t, srv.URL, f.staff, "/calendar/events", url.Values{"title": {"Form meeting"}, "kind": {"client_meeting"},
		"starts_at": {"2026-11-12T10:00"}, "ends_at": {"2026-11-12T11:00"}, "attend_me": {"on"}})
	if status != http.StatusSeeOther || !strings.HasPrefix(loc, "/calendar/events/") {
		t.Fatalf("form event: %d %s", status, loc)
	}
	e, err := f.svc.GetEvent(f.ctx, f.staff, domain.ID(strings.TrimPrefix(loc, "/calendar/events/")))
	if err != nil || !e.Start().Equal(at(t, "2026-11-12 10:00")) || e.Attendees[0] != f.staff.UserID {
		t.Fatalf("form event stored wrong: %+v %v", e, err)
	}
	status, loc = postForm(t, srv.URL, f.staff, "/calendar/deadlines", url.Values{"matter_id": {string(open.ID)}, "title": {"Form deadline"}, "due_on": {"2026-11-30"}})
	if status != http.StatusSeeOther {
		t.Fatalf("form deadline: %d %s", status, loc)
	}
	status, _ = postForm(t, srv.URL, f.staff, loc+"/status", url.Values{"status": {"done"}})
	if status != http.StatusSeeOther {
		t.Fatalf("mark done: %d", status)
	}
	_, page := get(t, srv.URL, f.staff, loc)
	if !strings.Contains(page, "done by Sam Staff") {
		t.Fatal("done by not shown")
	}
	// A form post without the CSRF field is refused.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/calendar/events", strings.NewReader("title=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Test-User", string(f.staff.UserID))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("form without csrf: %d", res.StatusCode)
	}
}
