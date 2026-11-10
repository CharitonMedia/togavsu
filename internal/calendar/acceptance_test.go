package calendar

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

func get(t *testing.T, srv string, user domain.Principal, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !user.UserID.IsZero() {
		req.Header.Set("X-Test-User", string(user.UserID))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// AC-011 (calendar half): clicking the upcoming deadlines widget opens the deadlines
// page with the same date range; clicking one deadline opens it; a staff user's click
// never reaches a walled matter. The module-widget and manifest clauses belong to the
// modules task.
func TestAC011UpcomingDeadlinesClickThrough(t *testing.T) {
	f := newFixture(t)
	open := f.matter("Rivera v. Acme")
	secret := f.walled("Sealed guardianship")
	// Today is 2026-11-10 in Chicago. The default window is today through +7 days.
	inRange := f.deadline(f.staff, DeadlineInput{MatterID: open.ID, Title: "Answer due", DueOn: "2026-11-13", ResponsibleUserID: f.attorney.UserID})
	overdue := f.deadline(f.staff, DeadlineInput{MatterID: open.ID, Title: "Disclosures late", DueOn: "2026-11-05", ResponsibleUserID: f.attorney.UserID})
	later := f.deadline(f.staff, DeadlineInput{MatterID: open.ID, Title: "Pretrial brief", DueOn: "2026-12-20", ResponsibleUserID: f.attorney.UserID})
	done := f.deadline(f.staff, DeadlineInput{MatterID: open.ID, Title: "Filed already", DueOn: "2026-11-12", ResponsibleUserID: f.attorney.UserID, Status: StatusDone})
	hidden := f.deadline(f.attorney, DeadlineInput{MatterID: secret.ID, Title: "Sealed report", DueOn: "2026-11-12", ResponsibleUserID: f.attorney.UserID})

	srv := f.server()
	status, body := get(t, srv.URL, f.staff, "/api/v1/widgets/upcoming-deadlines")
	if status != http.StatusOK {
		t.Fatalf("widget: %d %s", status, body)
	}
	var w Widget
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatal(err)
	}
	if w.Target.Path != "/calendar/deadlines" || w.PagePath != "/calendar/deadlines" {
		t.Fatalf("target: %+v", w.Target)
	}
	if w.Target.Query["from_date"] != "2026-11-10" || w.Target.Query["to_date"] != "2026-11-17" {
		t.Fatalf("range: %+v", w.Target.Query)
	}
	ids := map[string]bool{}
	for _, row := range w.Rows {
		ids[row.ID] = true
		if row.TargetPath != "/calendar/deadlines/"+row.ID {
			t.Fatalf("row target %q", row.TargetPath)
		}
	}
	if !ids[string(inRange.ID)] || !ids[string(overdue.ID)] || ids[string(later.ID)] || ids[string(done.ID)] || ids[string(hidden.ID)] {
		t.Fatalf("widget rows wrong: %v", ids)
	}
	if strings.Contains(body, "Sealed") {
		t.Fatal("widget leaked the walled deadline")
	}

	// The title click: the page with exactly the widget's query lists the same rows.
	q := url.Values{}
	for k, v := range w.Target.Query {
		q.Set(k, v)
	}
	status, page := get(t, srv.URL, f.staff, w.Target.Path+"?"+q.Encode())
	if status != http.StatusOK {
		t.Fatalf("page: %d", status)
	}
	table := page[strings.Index(page, "<table"):]
	for _, row := range w.Rows {
		if !strings.Contains(table, `href="/calendar/deadlines/`+row.ID+`"`) {
			t.Fatalf("page lacks widget row %s", row.Title)
		}
	}
	if strings.Contains(page, string(later.ID)) || strings.Contains(page, string(hidden.ID)) || strings.Contains(page, "Sealed") {
		t.Fatal("page shows a row the widget did not")
	}
	if !strings.Contains(page, `value="2026-11-10"`) || !strings.Contains(page, `value="2026-11-17"`) {
		t.Fatal("page did not keep the widget's date range")
	}
	// The API list with the same query matches the widget row for row.
	status, list := get(t, srv.URL, f.staff, "/api/v1/deadlines?"+q.Encode())
	if status != http.StatusOK {
		t.Fatalf("list: %d", status)
	}
	var lr struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal([]byte(list), &lr)
	var apiIDs, rowIDs []string
	for _, d := range lr.Data {
		apiIDs = append(apiIDs, d["id"].(string))
	}
	for _, r := range w.Rows {
		rowIDs = append(rowIDs, r.ID)
	}
	if !reflect.DeepEqual(apiIDs, rowIDs) {
		t.Fatalf("api %v widget %v", apiIDs, rowIDs)
	}

	// A row click opens that deadline.
	status, one := get(t, srv.URL, f.staff, w.Rows[0].TargetPath)
	if status != http.StatusOK || !strings.Contains(one, "<h1>") {
		t.Fatalf("row click: %d", status)
	}
	// A staff user who follows a walled deadline's link gets not found, by page and API.
	for _, path := range []string{"/calendar/deadlines/" + string(hidden.ID), "/api/v1/deadlines/" + string(hidden.ID)} {
		status, body := get(t, srv.URL, f.staff, path)
		if status != http.StatusNotFound || strings.Contains(body, "Sealed") {
			t.Fatalf("%s for staff: %d", path, status)
		}
	}
	// The admin, not on the wall, is excluded too; the attorney on the wall sees it.
	if status, _ := get(t, srv.URL, f.admin, "/calendar/deadlines/"+string(hidden.ID)); status != http.StatusNotFound {
		t.Fatalf("admin off the wall: %d", status)
	}
	if status, _ := get(t, srv.URL, f.attorney, "/calendar/deadlines/"+string(hidden.ID)); status != http.StatusOK {
		t.Fatalf("attorney on the wall: %d", status)
	}
	// An explicit widget range is carried through to the target unchanged.
	_, body = get(t, srv.URL, f.staff, "/api/v1/widgets/upcoming-deadlines?from_date=2026-12-01&to_date=2026-12-31")
	var w2 Widget
	_ = json.Unmarshal([]byte(body), &w2)
	if w2.Target.Query["from_date"] != "2026-12-01" || w2.Target.Query["to_date"] != "2026-12-31" {
		t.Fatalf("explicit range lost: %+v", w2.Target.Query)
	}
	if len(w2.Rows) != 2 || w2.Rows[0].ID != string(overdue.ID) || w2.Rows[1].ID != string(later.ID) {
		t.Fatalf("explicit range rows: %+v", w2.Rows)
	}
	// Bad input and roles without calendar access fail closed.
	if status, _ := get(t, srv.URL, f.staff, "/api/v1/widgets/upcoming-deadlines?from_date=11/10/2026"); status != http.StatusBadRequest {
		t.Fatalf("bad date: %d", status)
	}
	for _, p := range []domain.Principal{f.accountant, f.client} {
		if status, _ := get(t, srv.URL, p, "/api/v1/widgets/upcoming-deadlines"); status != http.StatusForbidden {
			t.Fatalf("%v widget: %d", p.Roles, status)
		}
	}
	if status, _ := get(t, srv.URL, domain.Principal{}, "/api/v1/widgets/upcoming-deadlines"); status != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", status)
	}
}

// snapshot is everything a read must leave unchanged.
type snapshot struct {
	events    []Event
	deadlines []Deadline
	audit     int
	stream    int
}

func (f *fixture) snap() snapshot {
	e, _ := f.store.Events(f.ctx)
	d, _ := f.store.Deadlines(f.ctx)
	return snapshot{events: e, deadlines: d, audit: f.audit.Len(), stream: len(f.store.Stream())}
}

// AC-027: available dates lists only days with no court date, block or all-day event
// between the dates given, in pleading and letter form, and never changes the calendar.
func TestAC027AvailableDates(t *testing.T) {
	f := newFixture(t)
	ada := f.attorney.UserID
	// Week of Monday 2026-11-16 in Chicago.
	f.event(f.staff, EventInput{Title: "Motion hearing", Kind: KindCourtDate, StartsAt: at(t, "2026-11-17 09:00"), EndsAt: at(t, "2026-11-17 10:30"), Attendees: []domain.ID{ada}})
	f.event(f.staff, EventInput{Title: "Trial prep", Kind: KindBlock, StartsAt: at(t, "2026-11-18 08:00"), EndsAt: at(t, "2026-11-18 17:00"), Attendees: []domain.ID{ada}})
	f.event(f.staff, EventInput{Title: "CLE", Kind: KindOther, AllDay: true, StartsAt: at(t, "2026-11-19 00:00"), EndsAt: at(t, "2026-11-19 00:00"), Attendees: []domain.ID{ada}})
	f.event(f.staff, EventInput{Title: "Client lunch", Kind: KindClientMeeting, StartsAt: at(t, "2026-11-20 12:00"), EndsAt: at(t, "2026-11-20 13:00"), Attendees: []domain.ID{ada}})
	f.event(f.staff, EventInput{Title: "Other lawyer's trial", Kind: KindCourtDate, StartsAt: at(t, "2026-11-16 09:00"), EndsAt: at(t, "2026-11-16 17:00"), Attendees: []domain.ID{f.other.UserID}})
	f.event(f.staff, EventInput{Title: "Afternoon docket", Kind: KindCourtDate, StartsAt: at(t, "2026-11-23 13:30"), EndsAt: at(t, "2026-11-23 15:00"), Attendees: []domain.ID{ada}})
	// A court date on a walled matter still makes the attorney busy for a staff caller.
	secret := f.walled("Sealed adoption")
	f.event(f.attorney, EventInput{Title: "Sealed hearing", Kind: KindCourtDate, MatterID: secret.ID, StartsAt: at(t, "2026-11-24 09:00"), EndsAt: at(t, "2026-11-24 16:00"), Attendees: []domain.ID{ada}})

	m := f.matter("Rivera dissolution")
	court, caseNo, pos := "Circuit Court of Boone County, Missouri", "26BA-DR01234", "Petitioner"
	if _, err := f.practice.UpdateMatter(f.ctx, f.admin, m.ID, practice.MatterInput{Name: m.Name, PracticeAreaID: f.area, BillingType: practice.BillingHourly, Court: court, CaseNumber: caseNo, ClientPosition: pos}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, role string }{{"Maria Rivera", "client"}, {"Jon Rivera", "opposing party"}, {"Pat Counsel", "opposing counsel"}} {
		ct, err := f.practice.CreateContact(f.ctx, f.admin, practice.ContactInput{Kind: "person", FullName: c.name})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.practice.AddParty(f.ctx, f.admin, m.ID, practice.PartyInput{ContactID: ct.ID, Role: c.role, Primary: true}); err != nil {
			t.Fatal(err)
		}
	}

	before := f.snap()
	res, err := f.svc.AvailableDates(f.ctx, f.staff, AvailableRequest{UserID: ada, StartDate: "2026-11-16", EndDate: "2026-11-24", MatterID: m.ID, ServiceDate: "2026-11-12"})
	if err != nil {
		t.Fatal(err)
	}
	want := []AvailableDay{{"2026-11-16", PortionFull}, {"2026-11-20", PortionFull}}
	if !reflect.DeepEqual(res.Dates, want) {
		t.Fatalf("full days: %+v", res.Dates)
	}
	half, err := f.svc.AvailableDates(f.ctx, f.staff, AvailableRequest{UserID: ada, StartDate: "2026-11-16", EndDate: "2026-11-24", IncludeHalfDays: true})
	if err != nil {
		t.Fatal(err)
	}
	wantHalf := []AvailableDay{{"2026-11-16", PortionFull}, {"2026-11-17", PortionAfternoon}, {"2026-11-20", PortionFull}, {"2026-11-23", PortionMorning}}
	if !reflect.DeepEqual(half.Dates, wantHalf) {
		t.Fatalf("half days: %+v", half.Dates)
	}
	// Pleading: caption from the matter, title, the dates, certificate of service.
	for _, s := range []string{strings.ToUpper(court), "Maria Rivera", "Jon Rivera", "Petitioner", "Respondent", "Case No. " + caseNo,
		"AVAILABLE DATES FOR HEARING", "one day trial", "Monday, November 16, 2026", "Friday, November 20, 2026",
		"CERTIFICATE OF SERVICE", "November 12, 2026", "Pat Counsel", "Ada Attorney"} {
		if !strings.Contains(res.Pleading, s) {
			t.Fatalf("pleading lacks %q:\n%s", s, res.Pleading)
		}
	}
	for _, s := range []string{"Tuesday, November 17", "Saturday", "Sunday", "Sealed"} {
		if strings.Contains(res.Pleading, s) || strings.Contains(res.Letter, s) {
			t.Fatalf("output lists %q", s)
		}
	}
	// Letter: recipient, Re line, salutation, dates.
	for _, s := range []string{"Pat Counsel", "Re: Rivera dissolution, Case No. " + caseNo, "Dear Pat Counsel:", "Friday, November 20, 2026", "Sincerely"} {
		if !strings.Contains(res.Letter, s) {
			t.Fatalf("letter lacks %q:\n%s", s, res.Letter)
		}
	}
	if !strings.Contains(half.Pleading, "Tuesday, November 17, 2026 (afternoon only)") || !strings.Contains(half.Pleading, "Monday, November 23, 2026 (morning only)") {
		t.Fatalf("half-day lines:\n%s", half.Pleading)
	}

	// Default end is three months after start; the admin setting changes it live.
	def, err := f.svc.AvailableDates(f.ctx, f.attorney, AvailableRequest{UserID: ada, StartDate: "2026-11-16"})
	if err != nil || def.Request.EndDate != "2027-02-16" {
		t.Fatalf("default end %q %v", def.Request.EndDate, err)
	}
	if _, err := f.reg.Set(f.ctx, settingRangeMonths, "1", f.admin); err != nil {
		t.Fatal(err)
	}
	def, _ = f.svc.AvailableDates(f.ctx, f.attorney, AvailableRequest{UserID: ada, StartDate: "2026-11-16"})
	if def.Request.EndDate != "2026-12-16" {
		t.Fatalf("setting not applied: %q", def.Request.EndDate)
	}

	// Never changes the calendar: no event, deadline, audit or activity row changed.
	if after := f.snap(); !reflect.DeepEqual(before, after) {
		t.Fatalf("available dates changed state: %d->%d events, audit %d->%d, stream %d->%d", len(before.events), len(after.events), before.audit, after.audit, before.stream, after.stream)
	}

	// The API route returns the same thing and is equally read-only.
	srv := f.server()
	before = f.snap()
	body := `{"user_id":"` + string(ada) + `","start_date":"2026-11-16","end_date":"2026-11-24","include_half_days":true,"party_labels":{"left":"Plaintiff","right":"Defendant"},"left_party_name":"Acme","right_party_name":"Widget Co"}`
	status, out := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/calendar/available-dates", body, nil)
	if status != http.StatusOK {
		t.Fatalf("api: %d %s", status, out)
	}
	var ar struct {
		Dates    []map[string]string `json:"dates"`
		Pleading string              `json:"pleading"`
		Letter   string              `json:"letter"`
	}
	_ = json.Unmarshal([]byte(out), &ar)
	if len(ar.Dates) != 4 || ar.Dates[1]["portion"] != "afternoon" || !strings.Contains(ar.Pleading, "Acme") || ar.Letter == "" {
		t.Fatalf("api result: %s", out)
	}
	if after := f.snap(); !reflect.DeepEqual(before, after) {
		t.Fatal("API call changed state")
	}

	// The page shows the panel with Copy and Download, and stays read-only.
	form := url.Values{"user_id": {string(ada)}, "start_date": {"2026-11-16"}, "end_date": {"2026-11-24"}}
	status, html := postForm(t, srv.URL, f.staff, "/calendar/available-dates", form)
	if status != http.StatusOK || !strings.Contains(html, "data-copy=\"pleading\"") || !strings.Contains(html, "Download letter") || !strings.Contains(html, "Monday, November 16, 2026") {
		t.Fatalf("page: %d", status)
	}
	form.Set("form", "letter")
	status, txt := postForm(t, srv.URL, f.staff, "/calendar/available-dates/download", form)
	if status != http.StatusOK || !strings.Contains(txt, "Sincerely") {
		t.Fatalf("download: %d", status)
	}
	if after := f.snap(); !reflect.DeepEqual(before, after) {
		t.Fatal("page changed state")
	}
}

func TestAvailableDatesFailures(t *testing.T) {
	f := newFixture(t)
	ada := f.attorney.UserID
	secret := f.walled("Sealed adoption")
	cases := []struct {
		name string
		p    domain.Principal
		in   AvailableRequest
		code string
	}{
		{"no start", f.staff, AvailableRequest{UserID: ada}, domain.CodeValidation},
		{"bad start", f.staff, AvailableRequest{UserID: ada, StartDate: "11/16/2026"}, domain.CodeValidation},
		{"end before start", f.staff, AvailableRequest{UserID: ada, StartDate: "2026-11-16", EndDate: "2026-11-15"}, domain.CodeValidation},
		{"range too long", f.staff, AvailableRequest{UserID: ada, StartDate: "2026-01-01", EndDate: "2029-01-02"}, domain.CodeValidation},
		{"unknown user", f.staff, AvailableRequest{UserID: tid(999), StartDate: "2026-11-16"}, domain.CodeValidation},
		{"not an attorney", f.staff, AvailableRequest{UserID: f.staff.UserID, StartDate: "2026-11-16"}, domain.CodeValidation},
		{"bad side", f.staff, AvailableRequest{UserID: ada, StartDate: "2026-11-16", FirmSide: "middle"}, domain.CodeValidation},
		{"walled matter for staff", f.staff, AvailableRequest{UserID: ada, StartDate: "2026-11-16", MatterID: secret.ID}, domain.CodeNotFound},
		{"accountant", f.accountant, AvailableRequest{UserID: ada, StartDate: "2026-11-16"}, domain.CodeForbidden},
		{"client", f.client, AvailableRequest{UserID: ada, StartDate: "2026-11-16"}, domain.CodeForbidden},
		{"signed out", domain.Principal{}, AvailableRequest{UserID: ada, StartDate: "2026-11-16"}, domain.CodeUnauthenticated},
	}
	for _, c := range cases {
		if _, err := f.svc.AvailableDates(f.ctx, c.p, c.in); code(err) != c.code {
			t.Errorf("%s: got %v, want %s", c.name, err, c.code)
		}
	}
	// The attorney on the wall can fill from that matter.
	if _, err := f.svc.AvailableDates(f.ctx, f.attorney, AvailableRequest{UserID: ada, StartDate: "2026-11-16", MatterID: secret.ID}); err != nil {
		t.Fatal(err)
	}
	// No events at all: every weekday is free, weekends never listed.
	res, err := f.svc.AvailableDates(f.ctx, f.staff, AvailableRequest{UserID: ada, StartDate: "2026-11-20", EndDate: "2026-11-23"})
	if err != nil || len(res.Dates) != 2 || res.Dates[0].Date != "2026-11-20" || res.Dates[1].Date != "2026-11-23" {
		t.Fatalf("weekend skip: %+v %v", res.Dates, err)
	}
}
