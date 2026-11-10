package calendar

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// roster lists the fixture users the way identity's user repository would, so the
// service offers the attorney picker.
type roster struct {
	f   *fixture
	ids []domain.ID
}

func (r roster) Get(ctx context.Context, id domain.ID) (domain.User, error) {
	return r.f.pm.User(ctx, id)
}

func (r roster) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.User], error) {
	// Two pages, to prove the picker follows the cursor.
	half := len(r.ids) / 2
	part := r.ids[:half]
	next := "p2"
	if filter.Cursor == "p2" {
		part, next = r.ids[half:], ""
	}
	var page repository.Page[domain.User]
	for _, id := range part {
		u, err := r.f.pm.User(ctx, id)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, u)
	}
	page.NextCursor = next
	return page, nil
}

func (f *fixture) withRoster() {
	f.t.Helper()
	d := f.svc.d
	d.Users = roster{f: f, ids: []domain.ID{f.admin.UserID, f.attorney.UserID, f.other.UserID, f.staff.UserID, f.accountant.UserID, f.client.UserID}}
	svc, err := New(d)
	if err != nil {
		f.t.Fatal(err)
	}
	f.svc = svc
}

// PRD 8.16: the matter is picked by search. Staff never see a walled matter in the
// picker, by service call or on the page, and cannot pick one by id.
func TestAvailableDatesMatterPicker(t *testing.T) {
	f := newFixture(t)
	open := f.matter("Smith v. Jones")
	walled := f.walled("Smith Trust")

	hits, err := f.svc.SearchMatters(f.ctx, f.staff, "smith")
	if err != nil || len(hits) != 1 || hits[0].ID != open.ID || !strings.Contains(hits[0].Label, "Smith v. Jones") {
		t.Fatalf("staff hits: %v %+v", err, hits)
	}
	// The admin is not on the wall either.
	if hits, _ := f.svc.SearchMatters(f.ctx, f.admin, "smith"); len(hits) != 1 {
		t.Fatalf("admin hits: %+v", hits)
	}
	// The attorney on the wall sees both.
	if hits, _ := f.svc.SearchMatters(f.ctx, f.attorney, "smith"); len(hits) != 2 {
		t.Fatalf("attorney hits: %+v", hits)
	}
	// Failure paths: too-short query, roles without calendar access.
	if _, err := f.svc.SearchMatters(f.ctx, f.staff, "s"); code(err) != domain.CodeValidation {
		t.Fatalf("short query: %v", err)
	}
	for _, p := range []domain.Principal{f.accountant, f.client} {
		if _, err := f.svc.SearchMatters(f.ctx, p, "smith"); code(err) != domain.CodeForbidden {
			t.Fatalf("role %v: %v", p.Roles, err)
		}
	}
	if _, err := f.svc.PickedMatter(f.ctx, f.staff, walled.ID); code(err) != domain.CodeNotFound {
		t.Fatalf("walled pick: %v", err)
	}

	srv := f.server()
	status, html := get(t, srv.URL, f.staff, "/calendar/available-dates?matter_q=smith")
	if status != http.StatusOK || !strings.Contains(html, "Smith v. Jones") || strings.Contains(html, "Smith Trust") || strings.Contains(html, string(walled.ID)) {
		t.Fatalf("staff page: %d %s", status, html)
	}
	if !strings.Contains(html, "/calendar/available-dates?matter_id="+string(open.ID)) {
		t.Fatal("hit does not link to pick the matter")
	}
	status, html = get(t, srv.URL, f.attorney, "/calendar/available-dates?matter_q=smith")
	if status != http.StatusOK || !strings.Contains(html, "Smith Trust") {
		t.Fatalf("attorney page: %d", status)
	}
	// Picking shows the label and carries the id into the form.
	status, html = get(t, srv.URL, f.staff, "/calendar/available-dates?matter_id="+string(open.ID))
	if status != http.StatusOK || !strings.Contains(html, "<strong>") || !strings.Contains(html, `name="matter_id" value="`+string(open.ID)+`"`) {
		t.Fatalf("picked page: %d", status)
	}
	// A walled id typed into the URL is dropped, not echoed.
	status, html = get(t, srv.URL, f.staff, "/calendar/available-dates?matter_id="+string(walled.ID))
	if status != http.StatusOK || strings.Contains(html, string(walled.ID)) || strings.Contains(html, "Smith Trust") {
		t.Fatalf("walled pick page: %d", status)
	}
	status, html = get(t, srv.URL, f.staff, "/calendar/available-dates?matter_q=s")
	if status != http.StatusOK || !strings.Contains(html, `role="alert"`) {
		t.Fatalf("short query page: %d", status)
	}
	status, html = get(t, srv.URL, f.staff, "/calendar/available-dates?matter_q=nomatch")
	if status != http.StatusOK || !strings.Contains(html, "No matters match.") {
		t.Fatalf("no match page: %d", status)
	}
	// A walled matter posted by id still fails the tool for staff.
	form := url.Values{"user_id": {string(f.attorney.UserID)}, "start_date": {"2026-11-16"}, "matter_id": {string(walled.ID)}}
	if status, html := postForm(t, srv.URL, f.staff, "/calendar/available-dates", form); status == http.StatusOK || strings.Contains(html, "Smith Trust") {
		t.Fatalf("walled post: %d", status)
	}
}

// The attorney field is a picker of active attorneys by name when users can be
// listed, and falls back to the id field when they cannot.
func TestAvailableDatesAttorneyPicker(t *testing.T) {
	f := newFixture(t)
	srv := f.server()
	if _, html := get(t, srv.URL, f.staff, "/calendar/available-dates"); !strings.Contains(html, "Attorney user id") {
		t.Fatal("no fallback without a user lister")
	}
	if _, ok, err := f.svc.Attorneys(f.ctx, f.staff); ok || err != nil {
		t.Fatalf("lister reported without one: %v %v", ok, err)
	}

	f.withRoster()
	// An inactive attorney is not offered.
	gone := f.user(9, "Gil Gone", domain.RoleAttorney)
	u, _ := f.pm.User(f.ctx, gone.UserID)
	u.Active = false
	if err := f.pm.PutUser(f.ctx, u); err != nil {
		t.Fatal(err)
	}
	r := f.svc.d.Users.(roster)
	r.ids = append(r.ids, gone.UserID)
	d := f.svc.d
	d.Users = r
	svc, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc

	list, ok, err := f.svc.Attorneys(f.ctx, f.staff)
	if err != nil || !ok {
		t.Fatalf("attorneys: %v %v", ok, err)
	}
	var names []string
	for _, a := range list {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "Ada Attorney,Avery Admin,Otto Other" {
		t.Fatalf("attorneys: %v", names)
	}
	if _, _, err := f.svc.Attorneys(f.ctx, f.client); code(err) != domain.CodeForbidden {
		t.Fatalf("client: %v", err)
	}

	srv = f.server()
	status, html := get(t, srv.URL, f.staff, "/calendar/available-dates?user_id="+string(f.attorney.UserID))
	if status != http.StatusOK || strings.Contains(html, "Attorney user id") || strings.Contains(html, "Gil Gone") || strings.Contains(html, "Sam Staff") {
		t.Fatalf("picker page: %d", status)
	}
	if !strings.Contains(html, `<option value="`+string(f.attorney.UserID)+`" selected>Ada Attorney</option>`) {
		t.Fatalf("selected attorney missing: %s", html)
	}
}
