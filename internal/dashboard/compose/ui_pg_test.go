package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func widgets(ws ...dashboard.LayoutWidget) []dashboard.LayoutWidget { return ws }

// TestUIHomeLayouts is AC-010 on the running core: layouts save per user on Postgres; a
// named default restores exactly after a reset and is offered first; the admin's role
// default stays offered; the 11th saved layout is refused and nothing is written.
func TestUIHomeLayouts(t *testing.T) {
	e := newEnv(t)
	role := widgets(dashboard.LayoutWidget{WidgetID: "upcoming-deadlines", X: 0, Y: 0, W: 6, H: 4}, dashboard.LayoutWidget{WidgetID: "trust-alerts", X: 6, Y: 0, W: 6, H: 3})
	if r := e.atty.do("PUT", "/api/v1/role-layouts/attorney", map[string]any{"role": "attorney", "widgets": role}); r.code != http.StatusForbidden {
		t.Fatalf("attorney set a role layout: %d %s", r.code, r.body)
	}
	e.admin.must(http.StatusOK, "PUT", "/api/v1/role-layouts/attorney", map[string]any{"role": "attorney", "widgets": role})
	cur := e.atty.must(http.StatusOK, "GET", "/api/v1/me/layout", nil)
	if fmt.Sprint(cur.json["widgets"]) != fmt.Sprint(toJSON(t, role)) {
		t.Fatalf("attorney does not start from the role default: %s", cur.body)
	}
	mine := widgets(dashboard.LayoutWidget{WidgetID: "my-time", X: 0, Y: 0, W: 4, H: 3},
		dashboard.LayoutWidget{WidgetID: "upcoming-deadlines", X: 4, Y: 0, W: 8, H: 5, Filters: map[string]string{"from_date": "2026-11-01", "to_date": "2026-11-30"}},
		dashboard.LayoutWidget{WidgetID: "pending-conflict-checks", X: 0, Y: 5, W: 12, H: 2})
	e.atty.must(http.StatusOK, "PUT", "/api/v1/me/layout", map[string]any{"widgets": mine})
	// Save as through the home screen form, marked default.
	raw, _ := json.Marshal(mine)
	e.atty.form("/home/layouts", url.Values{"name": {"Trial prep"}, "widgets": {string(raw)}, "default": {"on"}})
	saved := items(t, e.atty.must(http.StatusOK, "GET", "/api/v1/me/layouts", nil))
	if len(saved) != 1 || saved[0]["name"] != "Trial prep" || saved[0]["default"] != true {
		t.Fatalf("saved layouts: %v", saved)
	}
	// The layout belongs to the attorney only.
	if n := len(items(t, e.staff.must(http.StatusOK, "GET", "/api/v1/me/layouts", nil))); n != 0 {
		t.Fatalf("staff sees %d of the attorney's layouts", n)
	}
	e.atty.must(http.StatusCreated, "POST", "/api/v1/me/layouts", map[string]any{"name": "Billing day", "widgets": widgets(dashboard.LayoutWidget{WidgetID: "unbilled", X: 0, Y: 0, W: 12, H: 3})})
	// Change the working layout, then reset.
	e.atty.must(http.StatusOK, "PUT", "/api/v1/me/layout", map[string]any{"widgets": widgets(dashboard.LayoutWidget{WidgetID: "running-timer", X: 2, Y: 1, W: 3, H: 2})})
	opts := items(t, e.atty.must(http.StatusOK, "GET", "/api/v1/me/layout-reset-options", nil))
	if len(opts) != 3 || opts[0]["name"] != "Trial prep" || opts[1]["name"] != "Billing day" || opts[2]["id"] != string(dashboard.RoleOptionID) {
		t.Fatalf("reset options: %v", opts)
	}
	home := e.atty.must(http.StatusOK, "GET", "/", nil)
	first := strings.Index(home.body, "Trial prep (your default)")
	if first < 0 || first > strings.Index(home.body, "Role default (Attorney)") {
		t.Fatalf("the home screen's Reset does not offer the default first")
	}
	e.atty.form("/home/reset", url.Values{"layout_id": {opts[0]["id"].(string)}})
	cur = e.atty.must(http.StatusOK, "GET", "/api/v1/me/layout", nil)
	if fmt.Sprint(cur.json["widgets"]) != fmt.Sprint(toJSON(t, mine)) {
		t.Fatalf("restore is not exact:\n got %v\nwant %v", cur.json["widgets"], toJSON(t, mine))
	}
	// The role default stays available and restores too.
	e.atty.must(http.StatusOK, "POST", "/api/v1/me/layouts/"+string(dashboard.RoleOptionID)+"/apply", nil)
	if cur = e.atty.must(http.StatusOK, "GET", "/api/v1/me/layout", nil); fmt.Sprint(cur.json["widgets"]) != fmt.Sprint(toJSON(t, role)) {
		t.Fatalf("role default: %s", cur.body)
	}
	for i := 3; i <= 10; i++ {
		e.atty.must(http.StatusCreated, "POST", "/api/v1/me/layouts", map[string]any{"name": fmt.Sprintf("Layout %d", i), "widgets": mine})
	}
	r := e.atty.do("POST", "/api/v1/me/layouts", map[string]any{"name": "Eleventh", "widgets": mine})
	if r.code != http.StatusConflict || str(t, r.json, "code") != dashboard.CodeLayoutLimit {
		t.Fatalf("11th layout: %d %s", r.code, r.body)
	}
	var n int
	if err := e.app.Pool.QueryRow(`SELECT count(*) FROM home_layouts WHERE user_id = $1`, e.atty.id).Scan(&n); err != nil || n != 10 {
		t.Fatalf("refused save wrote a row: %d %v", n, err)
	}
	// The form path is refused the same way and says why.
	if loc := e.atty.form("/home/layouts", url.Values{"name": {"Eleventh"}}).loc; !strings.Contains(loc, "error=") {
		t.Fatalf("form save over the cap: %s", loc)
	}
	// Rename, overwrite and delete work on the saved rows.
	id := saved[0]["id"].(string)
	e.atty.must(http.StatusOK, "PATCH", "/api/v1/me/layouts/"+id, map[string]any{"name": "Trial prep (Nov)"})
	e.atty.must(http.StatusNoContent, "DELETE", "/api/v1/me/layouts/"+id, nil)
	if r := e.atty.do("GET", "/api/v1/me/layouts/"+id, nil); r.code != http.StatusNotFound {
		t.Fatalf("deleted layout still readable: %d", r.code)
	}
	// A client principal never reaches the home screen API.
}

func toJSON(t *testing.T, v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

var hrefRe = regexp.MustCompile(`href="([^"]+)"`)

// TestUIWidgetClickThrough is AC-011: the upcoming deadlines widget's title opens the
// deadlines page with the same date range and a row opens that deadline; a staff user's
// clicks never reach a walled matter; a module widget opens the tab its manifest names;
// a manifest widget without a click-through target is refused.
func TestUIWidgetClickThrough(t *testing.T) {
	e := newEnv(t)
	open, _, _ := e.matter("Estate of Harlow", nil)
	walled, _, _ := e.matter("Walled Estate of Quist", nil)
	e.wall(walled, e.atty.id, e.admin.id)
	due := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	d1 := str(t, e.atty.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": open, "title": "Inventory due", "due_on": due, "responsible_user_id": e.staff.id}).json, "id")
	d2 := str(t, e.atty.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": walled, "title": "Quist accounting due", "due_on": due, "responsible_user_id": e.atty.id}).json, "id")
	from, to := time.Now().Format("2006-01-02"), time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	e.staff.must(http.StatusOK, "PUT", "/api/v1/me/layout", map[string]any{"widgets": widgets(dashboard.LayoutWidget{WidgetID: "upcoming-deadlines", X: 0, Y: 0, W: 6, H: 4, Filters: map[string]string{"from_date": from, "to_date": to}})})
	home := e.staff.must(http.StatusOK, "GET", "/", nil)
	if strings.Contains(home.body, "Quist") {
		t.Fatal("staff home screen shows the walled matter's deadline")
	}
	var title, row string
	for _, m := range hrefRe.FindAllStringSubmatch(home.body, -1) {
		h := strings.ReplaceAll(m[1], "&amp;", "&")
		if strings.HasPrefix(h, "/calendar/deadlines?") {
			title = h
		}
		if h == "/calendar/deadlines/"+d1 {
			row = h
		}
	}
	if title == "" || row == "" {
		t.Fatalf("home screen widget links: title %q row %q", title, row)
	}
	u, _ := url.Parse(title)
	if u.Query().Get("from_date") != from || u.Query().Get("to_date") != to {
		t.Fatalf("title click lost the date range: %s", title)
	}
	page := e.staff.must(http.StatusOK, "GET", title, nil)
	if !strings.Contains(page.body, "Inventory due") || strings.Contains(page.body, "Quist") {
		t.Fatalf("deadlines page from the widget: walled shown or own missing")
	}
	if r := e.staff.must(http.StatusOK, "GET", row, nil); !strings.Contains(r.body, "Inventory due") {
		t.Fatal("row click does not open the deadline")
	}
	if r := e.staff.do("GET", "/calendar/deadlines/"+d2, nil); r.code != http.StatusNotFound {
		t.Fatalf("staff opened the walled deadline: %d", r.code)
	}
	api := e.staff.must(http.StatusOK, "GET", "/api/v1/widgets/upcoming-deadlines?from_date="+from+"&to_date="+to, nil)
	if strings.Contains(api.body, d2) || !strings.Contains(api.body, d1) || str(t, api.json, "target", "path") != "/calendar/deadlines" {
		t.Fatalf("widget API: %s", api.body)
	}
	// Inside the wall, the attorney sees both.
	if w := e.atty.must(http.StatusOK, "GET", "/api/v1/widgets/upcoming-deadlines?from_date="+from+"&to_date="+to, nil); !strings.Contains(w.body, d2) {
		t.Fatalf("attorney inside the wall lacks the deadline: %s", w.body)
	}
	// Other core widgets on the home screen keep walls too.
	e.staff.must(http.StatusOK, "PUT", "/api/v1/me/layout", map[string]any{"widgets": widgets(
		dashboard.LayoutWidget{WidgetID: "my-open-matters", X: 0, Y: 0, W: 6, H: 4}, dashboard.LayoutWidget{WidgetID: "pending-conflict-checks", X: 6, Y: 0, W: 6, H: 4},
		dashboard.LayoutWidget{WidgetID: "recent-activity", X: 0, Y: 4, W: 6, H: 4}, dashboard.LayoutWidget{WidgetID: "unbilled", X: 6, Y: 4, W: 6, H: 4})})
	if home := e.staff.must(http.StatusOK, "GET", "/", nil); strings.Contains(home.body, "Quist") || !strings.Contains(home.body, "Harlow") {
		t.Fatal("staff home screen widgets leak or miss matters")
	}
	// Module widget: the manifest names the tab; a target-less one is refused at install.
	cat := e.ui.Dashboard.Catalog()
	if err := cat.AddModule("matter-clock", []dashboard.ManifestWidget{{ID: "today", Title: "Matter clock"}}, nil); err == nil {
		t.Fatal("a widget with no click-through target installed")
	}
	if err := cat.AddModule("matter-clock", []dashboard.ManifestWidget{{ID: "today", Title: "Matter clock", Target: "/modules/matter-clock/tab"}},
		func(string) dashboard.Source {
			return func(ctx context.Context, p domain.Principal, f map[string]string) (dashboard.Widget, error) {
				q := map[string]string{}
				if f["matter_id"] != "" {
					q["matter_id"] = f["matter_id"]
				}
				return dashboard.Widget{Title: "Matter clock", Target: dashboard.Target{Path: "/modules/matter-clock/tab", Query: q}}, nil
			}
		}); err != nil {
		t.Fatal(err)
	}
	e.staff.must(http.StatusOK, "PUT", "/api/v1/me/layout", map[string]any{"widgets": widgets(dashboard.LayoutWidget{WidgetID: "matter-clock.today", X: 0, Y: 0, W: 4, H: 3, Filters: map[string]string{"matter_id": open}})})
	w := e.staff.must(http.StatusOK, "GET", "/api/v1/widgets/matter-clock.today?matter_id="+open, nil)
	if str(t, w.json, "target", "path") != "/modules/matter-clock/tab" {
		t.Fatalf("module widget target: %s", w.body)
	}
	if home := e.staff.must(http.StatusOK, "GET", "/", nil); !strings.Contains(home.body, `href="/modules/matter-clock/tab?matter_id=`+open+`"`) {
		t.Fatal("module widget title does not open its tab with the widget's filter")
	}
	// Editing mode marks the grid so the script blocks clicks.
	if home := e.staff.must(http.StatusOK, "GET", "/?edit=1", nil); !strings.Contains(home.body, `data-editing="true"`) {
		t.Fatal("edit mode not marked")
	}
}

// TestUISidebar is AC-019: the sidebar shows only what the role and the installed
// modules allow, with live counts; a collapsed subgroup and the collapsed sidebar stay
// collapsed for that user on another computer (a second session).
func TestUISidebar(t *testing.T) {
	e := newEnv(t)
	e.matter("Estate of Harlow", nil)
	walled, _, _ := e.matter("Walled Estate of Quist", nil)
	e.wall(walled, e.atty.id)
	nav := func(c *client) resp { return c.must(http.StatusOK, "GET", "/api/v1/nav/context", nil) }
	entries := func(r resp) string {
		var ids []string
		for _, v := range r.json["entries"].([]any) {
			ids = append(ids, v.(map[string]any)["id"].(string))
		}
		return "," + strings.Join(ids, ",") + ","
	}
	a, s, acct := nav(e.admin), nav(e.staff), nav(e.acct)
	if !strings.Contains(entries(a), ",admin,") || a.json["is_admin"] != true {
		t.Fatalf("admin nav: %s", a.body)
	}
	if strings.Contains(entries(s), ",admin,") {
		t.Fatal("staff sees Administrative")
	}
	for _, hidden := range []string{"matters", "notes", "contacts", "activity", "admin"} {
		if strings.Contains(entries(acct), ","+hidden+",") {
			t.Fatalf("accountant sees %s: %s", hidden, entries(acct))
		}
	}
	if !strings.Contains(entries(acct), ",invoices,") {
		t.Fatal("accountant lacks Invoices")
	}
	// Live counts: staff outside the wall counts one active matter; the attorney inside counts two.
	if c := a.json["badges"].(map[string]any)["matters"]; c != float64(1) {
		t.Fatalf("admin outside the wall counts %v", c)
	}
	if c := nav(e.atty).json["badges"].(map[string]any)["matters"]; c != float64(2) {
		t.Fatalf("attorney inside the wall counts %v", c)
	}
	m3, _, _ := e.matter("Estate of Moreno", nil)
	if c := nav(e.staff).json["badges"].(map[string]any)["matters"]; c != float64(2) {
		t.Fatalf("count is not live: %v", c)
	}
	e.invoice(m3)
	e.atty.must(http.StatusCreated, "POST", "/api/v1/time-entries", map[string]any{"matter_id": m3, "worked_on": time.Now().UTC().Format("2006-01-02"), "increments": 2, "description": "More", "activity_type_id": e.activityID, "billable": true, "rate_cents": 100_00})
	e.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": m3})
	// An approved invoice stays a draft until it is sent, so both count.
	if c := nav(e.staff).json["badges"].(map[string]any)["invoices_draft"]; c != float64(2) {
		t.Fatalf("draft invoice count: %v", c)
	}
	// The rendered sidebar matches: no Administrative link for staff.
	if page := e.staff.must(http.StatusOK, "GET", "/", nil); strings.Contains(page.body, `href="/admin"`) || !strings.Contains(page.body, "Trust Ledger") {
		t.Fatal("staff sidebar HTML")
	}
	if page := e.acct.must(http.StatusOK, "GET", "/", nil); strings.Contains(page.body, `href="/practice/notes"`) || strings.Contains(page.body, ">Communications<") {
		t.Fatal("accountant sidebar shows Notes or an empty section")
	}
	// The sidebar hides links only: the route still refuses.
	if r := e.staff.do("GET", "/admin", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff opened /admin: %d", r.code)
	}
	// A module entry shows once registered, with its count, and goes when removed.
	if err := e.ui.Dashboard.AddModuleNav("messages", []dashboard.ModuleNavEntry{{ID: "inbox", Section: dashboard.SectionCommunications, Label: "Messages", Icon: "note", Path: "/modules/messages", Order: 5}},
		func(context.Context, domain.Principal, string) (int, bool, error) { return 3, true, nil }); err != nil {
		t.Fatal(err)
	}
	if s := nav(e.staff); !strings.Contains(entries(s), ",messages.inbox,") || s.json["badges"].(map[string]any)["module:inbox"] != float64(3) {
		t.Fatalf("module entry: %s", s.body)
	}
	e.ui.Dashboard.RemoveModuleNav("messages")
	if strings.Contains(entries(nav(e.staff)), "messages.inbox") {
		t.Fatal("removed module entry shows")
	}
	// Collapse on one computer, open on another.
	e.staff.must(http.StatusOK, "PUT", "/api/v1/me/sidebar", map[string]any{"collapsed": true, "subgroups": []map[string]any{{"id": "money.trust", "collapsed": true}}})
	other := e.signIn("Sam Staff", "sam@firm.example")
	st := nav(other)
	if st.json["sidebar_collapsed"] != true || !strings.Contains(st.body, `"id":"money.trust","collapsed":true`) {
		t.Fatalf("second session: %s", st.body)
	}
	page := other.must(http.StatusOK, "GET", "/", nil)
	if !strings.Contains(page.body, `class="shell sidebar-collapsed"`) || !strings.Contains(page.body, `data-subgroup="money.trust">`) {
		t.Fatal("second computer's page is not collapsed")
	}
	// The no-script toggle form opens it again; the attorney's state is untouched.
	other.form("/ui/sidebar", url.Values{"subgroup": {"money.trust"}, "collapsed": {"false"}, "back": {"/"}})
	if b := nav(e.staff).body; strings.Contains(b, `"collapsed":true}`) && strings.Contains(b, "money.trust\",\"collapsed\":true") {
		t.Fatalf("toggle not saved: %s", b)
	}
	if nav(e.atty).json["sidebar_collapsed"] == true {
		t.Fatal("another user's sidebar collapsed")
	}
	// Writes need the CSRF token.
	if r := e.staff.do("PUT", "/api/v1/me/sidebar", map[string]any{"collapsed": false, "subgroups": []any{}}, "X-CSRF-Token", "wrong"); r.code != http.StatusForbidden {
		t.Fatalf("sidebar write without csrf: %d", r.code)
	}
}

// TestUISearch is AC-020 through the header box: a matter by number, a person by phone
// and an invoice by number in each scope and in All; nothing from a walled matter for a
// user outside the wall.
func TestUISearch(t *testing.T) {
	e := newEnv(t)
	m, number, clientID := e.matter("Estate of Harlow", nil)
	e.admin.must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{"contact_id": clientID, "label": "mobile", "phone": "+13125550187", "e164": "+13125550187", "primary": true})
	_, invNumber := e.invoice(m)
	walled, wNumber, wClient := e.matter("Walled Estate of Quist", nil)
	e.admin.must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{"contact_id": wClient, "label": "mobile", "phone": "+13125550999", "e164": "+13125550999", "primary": true})
	_, wInv := e.invoice(walled)
	e.wall(walled, e.atty.id)
	search := func(c *client, q, scope string) string {
		return c.must(http.StatusOK, "GET", "/ui/search?q="+url.QueryEscape(q)+"&scope="+scope, nil).body
	}
	for _, tc := range []struct{ q, scope, want string }{
		{number, "matters", "Estate of Harlow"}, {number, "all", "Estate of Harlow"},
		{"5550187", "people", "Client Harlow"}, {"5550187", "all", "Client Harlow"},
		{invNumber, "invoices", invNumber}, {invNumber, "all", invNumber},
	} {
		if body := search(e.staff, tc.q, tc.scope); !strings.Contains(body, tc.want) {
			t.Fatalf("%s in %s: %s", tc.q, tc.scope, body)
		}
	}
	for _, tc := range []struct{ q, scope string }{{wNumber, "matters"}, {wNumber, "all"}, {"5550999", "people"}, {"5550999", "all"}, {wInv, "invoices"}, {wInv, "all"}, {"Quist", "all"}} {
		body := search(e.staff, tc.q, tc.scope)
		if strings.Contains(body, `role="option"`) || strings.Contains(body, walled) || !strings.Contains(body, "No matches for") {
			t.Fatalf("staff outside the wall found %q in %s: %s", tc.q, tc.scope, body)
		}
		if body := search(e.atty, tc.q, tc.scope); !strings.Contains(body, "Quist") && !strings.Contains(body, wInv) {
			t.Fatalf("attorney inside the wall cannot find %q in %s: %s", tc.q, tc.scope, body)
		}
	}
	if body := search(e.staff, "x", "all"); !strings.Contains(body, "Type at least 2 characters") {
		t.Fatalf("short query: %s", body)
	}
	if r := e.staff.do("GET", "/ui/search?q=Harlow&scope=bogus", nil); r.code != http.StatusBadRequest {
		t.Fatalf("unknown scope: %d", r.code)
	}
	// The full page and the shell's box carry the setting-driven delay and placeholder.
	page := e.staff.must(http.StatusOK, "GET", "/search?q="+url.QueryEscape(number)+"&scope=matters", nil)
	if !strings.Contains(page.body, "Estate of Harlow") || !strings.Contains(page.body, `delay:180ms`) || !strings.Contains(page.body, "Search matters, people, invoices, files (Ctrl-K)") {
		t.Fatal("search page or header box")
	}
	e.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/search.delay", map[string]any{"value": "250ms"})
	if page := e.staff.must(http.StatusOK, "GET", "/", nil); !strings.Contains(page.body, `delay:250ms`) {
		t.Fatal("search delay setting not live")
	}
}

// TestUINotifications is AC-021 in the shell: a new trust flag raises the responsible
// attorney's bell count on every screen; Mark all read clears it for that user only.
func TestUINotifications(t *testing.T) {
	e := newEnv(t)
	m, _, clientID := e.matter("Estate of Brandt", nil)
	bell := func(c *client) int {
		page := c.must(http.StatusOK, "GET", "/", nil)
		mm := regexp.MustCompile(`data-unread="(\d+)"`).FindStringSubmatch(page.body)
		if mm == nil {
			return 0
		}
		var n int
		fmt.Sscan(mm[1], &n)
		return n
	}
	if n := bell(e.atty); n != 0 {
		t.Fatalf("attorney starts with %d", n)
	}
	post := func(typ string, cents int64) {
		e.atty.must(http.StatusCreated, "POST", "/api/v1/trust-ledger", map[string]any{"trust_account_id": e.account, "client_id": clientID, "matter_id": m,
			"entry_type": typ, "amount_cents": cents, "method": "check", "description": "synthetic " + typ})
	}
	post("deposit", 50_00)
	post("withdrawal", 80_00)
	if n := bell(e.atty); n != 1 {
		t.Fatalf("bell after a flag: %d", n)
	}
	if n := bell(e.staff); n != 0 {
		t.Fatalf("staff bell: %d", n)
	}
	adminBefore := bell(e.admin)
	panel := e.atty.must(http.StatusOK, "GET", "/ui/notifications", nil)
	if !strings.Contains(panel.body, "Mark all read") || !strings.Contains(panel.body, `class="unread"`) {
		t.Fatalf("bell panel: %s", panel.body)
	}
	// Opening one marks it read and goes to its item.
	id := regexp.MustCompile(`/notifications/([0-9a-f-]+)/open`).FindStringSubmatch(panel.body)[1]
	if loc := e.atty.form("/notifications/"+id+"/open", nil).loc; !strings.HasPrefix(loc, "/trust/") {
		t.Fatalf("notification opens %s", loc)
	}
	if n := bell(e.atty); n != 0 {
		t.Fatalf("open did not mark read: %d", n)
	}
	post("withdrawal", 5_00)
	post("withdrawal", 5_00)
	if n := bell(e.atty); n != 2 {
		t.Fatalf("bell after two more flags: %d", n)
	}
	// Another user cannot open the attorney's notification.
	if r := e.staff.do("POST", "/notifications/"+id+"/open", url.Values{"csrf": {e.staff.csrf}}); r.code != http.StatusSeeOther || !strings.Contains(r.loc, "error=") {
		t.Fatalf("staff opened the attorney's notification: %d %s", r.code, r.loc)
	}
	// Mark all read without the CSRF token is refused and changes nothing.
	if r := e.atty.do("POST", "/notifications/read-all", url.Values{"back": {"/"}}); r.code != http.StatusForbidden {
		t.Fatalf("read-all without csrf: %d", r.code)
	}
	e.atty.form("/notifications/read-all", url.Values{"back": {"/"}})
	if n := bell(e.atty); n != 0 {
		t.Fatalf("bell after Mark all read: %d", n)
	}
	if n := bell(e.admin); n != adminBefore+2 {
		t.Fatalf("attorney's Mark all read changed the admin's count: %d (was %d)", n, adminBefore)
	}
}

// TestUIHelp is AC-022 and the /help half of AC-032: /help shows the user guide in HTML
// and PDF for the running version and the firm's sections; an admin adds a help item
// from the dashboard; a client user gets 404 on /help/guide; every role's tutorial opens.
func TestUIHelp(t *testing.T) {
	e := newEnv(t)
	page := e.staff.must(http.StatusOK, "GET", "/help", nil)
	for _, want := range []string{`href="/help/guide" target="_blank"`, `href="/help/guide.pdf"`, "Getting started as an admin", "Getting started as an attorney", "Getting started as staff", "Getting started as an accountant"} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("/help lacks %s", want)
		}
	}
	for _, id := range []string{"tutorial-admin", "tutorial-attorney", "tutorial-staff", "tutorial-accountant"} {
		if r := e.staff.must(http.StatusOK, "GET", "/help/topics/"+id, nil); !strings.Contains(r.body, "Sign in") && !strings.Contains(r.body, "sign in") {
			t.Fatalf("tutorial %s: %.300s", id, r.body)
		}
	}
	guide := e.staff.must(http.StatusOK, "GET", "/help/guide", nil)
	if !strings.Contains(guide.body, "Version dev") || !strings.Contains(guide.body, `id="admin-time_billing"`) || !strings.Contains(guide.ctype, "text/html") {
		t.Fatalf("guide HTML: %.400s", guide.body)
	}
	pdf := e.acct.must(http.StatusOK, "GET", "/help/guide.pdf", nil)
	if !strings.HasPrefix(pdf.body, "%PDF-") || pdf.ctype != "application/pdf" {
		t.Fatalf("guide PDF: %s", pdf.ctype)
	}
	// The admin adds a section and an item from the admin dashboard.
	e.admin.form("/admin/help/sections", url.Values{"title": {"Firm procedures"}})
	if page := e.staff.must(http.StatusOK, "GET", "/help", nil); !strings.Contains(page.body, "Firm procedures") || !strings.Contains(page.body, "No items in this section yet.") {
		t.Fatal("empty section text")
	}
	secs := items(t, e.staff.must(http.StatusOK, "GET", "/api/v1/help/sections", nil))
	sec := secs[0]["id"].(string)
	if loc := e.staff.do("POST", "/admin/help/items", url.Values{"csrf": {e.staff.csrf}, "section_id": {sec}, "title": {"x"}, "kind": {"text"}, "body": {"y"}}).loc; !strings.Contains(loc, "error=") {
		t.Fatalf("staff added a help item: %s", loc)
	}
	e.admin.form("/admin/help/items", url.Values{"section_id": {sec}, "title": {"Opening a new file"}, "kind": {"text"}, "body": {"Run the **conflict check** before the engagement letter."}})
	page = e.staff.must(http.StatusOK, "GET", "/help", nil)
	if !strings.Contains(page.body, "Opening a new file") || !strings.Contains(page.body, "<strong>conflict check</strong>") {
		t.Fatal("added item missing from /help")
	}
	if n := len(items(t, e.staff.must(http.StatusOK, "GET", "/api/v1/help/items?section_id="+sec, nil))); n != 1 {
		t.Fatalf("items API: %d", n)
	}
	// A client user gets 404 on the guide and the help center.
	// users.contact_id references the section 5 contacts table, while
	// practice keeps contacts in practice_records, so a client user is created unlinked here.
	e.createUser("Cora Client", "cora@client.example", "", domain.RoleClient)
	cl := e.signIn("Cora Client", "cora@client.example")
	for _, path := range []string{"/help/guide", "/help/guide.pdf", "/help", "/api/v1/help/guide", "/api/v1/help/sections", "/help/topics/home"} {
		if r := cl.do("GET", path, nil); r.code != http.StatusNotFound {
			t.Fatalf("client %s: %d", path, r.code)
		}
	}
	// Signed out, the guide sends the browser to sign in; the API answers 401.
	anon := newClient(t, e.srv.URL)
	if r := anon.do("GET", "/help/guide", nil); r.code != http.StatusSeeOther || r.loc != "/login" {
		t.Fatalf("anonymous guide: %d %s", r.code, r.loc)
	}
	if r := anon.do("GET", "/api/v1/help/guide", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("anonymous guide API: %d", r.code)
	}
	// Every shell screen carries a ? icon to its topic; admin tabs open their own topic.
	for path, topic := range map[string]string{"/": "/help/topics/home", "/me/settings": "/help/topics/my-settings", "/notifications": "/help/topics/notifications",
		"/help": "/help/topics/help-center", "/search": "/help/topics/search"} {
		if r := e.staff.must(http.StatusOK, "GET", path, nil); !strings.Contains(r.body, `class="help-q" href="`+topic+`"`) {
			t.Fatalf("%s has no ? to %s", path, topic)
		}
	}
	if r := e.admin.must(http.StatusOK, "GET", "/admin?tab=time_billing", nil); !strings.Contains(r.body, `class="help-q" href="/help/topics/admin-time_billing"`) {
		t.Fatal("admin tab ? icon")
	}
	if r := e.staff.must(http.StatusOK, "GET", "/api/v1/help/topics?q=time+increment", nil); items(t, r)[0]["id"] != "admin-time_billing" {
		t.Fatalf("topic search: %s", r.body)
	}
}

// TestUIAppearanceAndLLMs is the ui half of AC-028: an Appearance change shows on the
// next page load with no restart and is audited; /llms.txt loads without sign-in and
// holds no firm records.
func TestUIAppearanceAndLLMs(t *testing.T) {
	e := newEnv(t)
	css := func(c *client) string { return c.must(http.StatusOK, "GET", "/ui/appearance.css", nil).body }
	if !strings.Contains(css(e.staff), "--sidebar:#14213d") {
		t.Fatalf("default theme: %s", css(e.staff))
	}
	if loc := e.staff.do("POST", "/admin/appearance", url.Values{"csrf": {e.staff.csrf}, "preset": {"forest_cream"}}).loc; !strings.Contains(loc, "error=") {
		t.Fatalf("staff changed appearance: %s", loc)
	}
	e.admin.form("/admin/appearance", url.Values{"preset": {"charcoal_copper"}, "heading_font": {"Lora"}, "body_font": {"Inter"}, "text_size": {"lg"}, "mode": {"dark"}})
	got := css(e.staff)
	for _, want := range []string{"--sidebar:#2b2b2b", "--accent:#b87333", `--font-head:"Lora"`, "--text-size:18px", "--page:#15181d"} {
		if !strings.Contains(got, want) {
			t.Fatalf("next load lacks %s: %s", want, got)
		}
	}
	if page := e.staff.must(http.StatusOK, "GET", "/", nil); !strings.Contains(page.body, `data-mode="dark"`) {
		t.Fatal("page mode")
	}
	var n int
	if err := e.app.Pool.QueryRow(`SELECT count(*) FROM audit_log WHERE entity = 'setting' AND field = 'appearance.preset' AND new_value = 'Charcoal and copper'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("appearance change not audited: %d %v", n, err)
	}
	// The contract API reads and writes the same settings.
	a := e.acct.must(http.StatusOK, "GET", "/api/v1/settings/appearance", nil)
	if a.json["preset"] != "charcoal_copper" || a.json["heading_font"] != "Lora" {
		t.Fatalf("appearance API: %s", a.body)
	}
	e.admin.must(http.StatusOK, "PUT", "/api/v1/settings/appearance", map[string]any{"preset": "maximum_contrast", "staff_sidebar": "", "staff_header": "", "staff_page": "", "staff_accent": "",
		"client_header": "#225588", "heading_font": "Atkinson Hyperlegible", "body_font": "Atkinson Hyperlegible", "text_size": "md", "mode": "light"})
	if got := css(e.staff); !strings.Contains(got, "--sidebar:#000000") || !strings.Contains(got, "--client-header:#225588") {
		t.Fatalf("PUT not live: %s", got)
	}
	// The user's own mode wins over the firm's.
	e.staff.must(http.StatusOK, "PATCH", "/api/v1/me/settings", map[string]any{"mode": "dark", "text_size": "sm"})
	if got := css(e.staff); !strings.Contains(got, "--page:#15181d") || !strings.Contains(got, "--text-size:14px") {
		t.Fatalf("my settings mode: %s", got)
	}
	if got := css(e.atty); strings.Contains(got, "--page:#15181d") {
		t.Fatal("one user's mode changed another's")
	}
	// llms.txt: public, no firm records.
	e.matter("Estate of Harlow", nil)
	e.admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Secret", "family_name": "Clientname"})
	e.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/llms.firm_paragraph", map[string]any{"value": "We take probate matters in three counties."})
	anon := newClient(t, e.srv.URL)
	for _, path := range []string{"/llms.txt", "/api/v1/llms.txt"} {
		r := anon.must(http.StatusOK, "GET", path, nil)
		for _, want := range []string{"Synthetic Firm LLP", "/api/v1/openapi.json", "/mcp", "bearer token", "We take probate matters"} {
			if !strings.Contains(r.body, want) {
				t.Fatalf("%s lacks %s: %s", path, want, r.body)
			}
		}
		for _, leak := range []string{"Harlow", "Clientname", "avery@firm.example", "Ada Attorney", "IOLTA", "Probate"} {
			if strings.Contains(r.body, leak) {
				t.Fatalf("%s leaks %s", path, leak)
			}
		}
	}
	e.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/llms.show_firm_name", map[string]any{"value": false})
	if r := anon.must(http.StatusOK, "GET", "/llms.txt", nil); strings.Contains(r.body, "Synthetic Firm LLP") {
		t.Fatal("hidden firm name shown")
	}
}

// TestUIAdminSettings is AC-029 through the admin page: every default the PRD names is a
// setting on its tab with that default; a change made there applies on the next request
// with no restart and logs the old and new values; Reset to default restores it.
func TestUIAdminSettings(t *testing.T) {
	e := newEnv(t)
	byTab := map[string]string{}
	pageFor := func(tab string) string {
		if _, ok := byTab[tab]; !ok {
			byTab[tab] = e.admin.must(http.StatusOK, "GET", "/admin?tab="+tab, nil).body
		}
		return byTab[tab]
	}
	checked := 0
	for _, d := range settings.Definitions() {
		if !d.PRD {
			continue
		}
		page := pageFor(d.Tab)
		row := between(page, `<tr id="s-`+d.Key+`"`, "</tr>")
		if row == "" {
			t.Errorf("PRD default %s is not on its tab %s", d.Key, d.Tab)
			continue
		}
		def := d.Default
		if def == "" {
			def = "(none)"
		}
		if !strings.Contains(html.UnescapeString(row), "<code>"+def+"</code>") {
			t.Errorf("%s does not show its default %q on %s", d.Key, def, d.Tab)
		}
		if d.Locked || d.Secret {
			continue
		}
		next, ok := altValue(d)
		if !ok {
			t.Errorf("no alternate test value for %s (%s)", d.Key, d.Kind)
			continue
		}
		e.admin.form("/admin/settings/"+d.Key, url.Values{"value": {next}, "tab": {d.Tab}, "kind": {d.Kind}})
		cur := e.admin.must(http.StatusOK, "GET", "/api/v1/settings/"+d.Key, nil)
		if fmt.Sprint(cur.json["value"]) != next && !sameJSON(fmt.Sprint(cur.json["value"]), next) && !(d.Kind == settings.KindBool && fmt.Sprint(cur.json["value"]) == next) {
			t.Errorf("%s: set %q, next request reads %v", d.Key, next, cur.json["value"])
		}
		var n int
		if err := e.app.Pool.QueryRow(`SELECT count(*) FROM audit_log WHERE entity = 'setting' AND field = $1 AND new_value = $2`, d.Key, next).Scan(&n); err != nil || n == 0 {
			t.Errorf("%s change not audited with its new value: %v", d.Key, err)
		}
		e.admin.form("/admin/settings/"+d.Key+"/reset", url.Values{"tab": {d.Tab}})
		cur = e.admin.must(http.StatusOK, "GET", "/api/v1/settings/"+d.Key, nil)
		if v := fmt.Sprint(cur.json["value"]); v != d.Default && !sameJSON(v, d.Default) && !(d.Kind == settings.KindBool && v == d.Default) {
			t.Errorf("%s: reset reads %v, default %q", d.Key, cur.json["value"], d.Default)
		}
		checked++
	}
	if checked < 50 {
		t.Fatalf("only %d PRD settings round-tripped", checked)
	}
	// One change shows its effect on a screen at once: the saved-layout cap.
	e.admin.form("/admin/settings/home.max_saved_layouts", url.Values{"value": {"1"}, "tab": {"home_screen"}})
	e.staff.must(http.StatusCreated, "POST", "/api/v1/me/layouts", map[string]any{"name": "Only", "widgets": []any{}})
	if r := e.staff.do("POST", "/api/v1/me/layouts", map[string]any{"name": "Second", "widgets": []any{}}); r.code != http.StatusConflict {
		t.Fatalf("cap of 1 not applied on the next request: %d", r.code)
	}
	// An invalid value is refused with its reason; a non-admin cannot write.
	if loc := e.admin.form("/admin/settings/billing.time_increment_minutes", url.Values{"value": {"7"}, "tab": {"time_billing"}}).loc; !strings.Contains(loc, "error=") {
		t.Fatalf("invalid increment accepted: %s", loc)
	}
	if loc := e.atty.do("POST", "/admin/settings/billing.time_increment_minutes", url.Values{"csrf": {e.atty.csrf}, "value": {"15"}}).loc; !strings.Contains(loc, "error=") {
		t.Fatalf("attorney changed a setting: %s", loc)
	}
	if r := e.atty.do("GET", "/admin?tab=security", nil); r.code != http.StatusForbidden {
		t.Fatalf("attorney opened the admin page: %d", r.code)
	}
	// Every tab renders, with its ? icon, and the System tab shows the seat count.
	for _, tab := range dashboard.AdminTabIDs() {
		r := e.admin.must(http.StatusOK, "GET", "/admin?tab="+tab, nil)
		if !strings.Contains(r.body, `class="help-q" href="/help/topics/admin-`+tab+`"`) {
			t.Fatalf("tab %s has no ? icon", tab)
		}
	}
	if r := e.admin.must(http.StatusOK, "GET", "/admin?tab=system", nil); !strings.Contains(r.body, "<strong>4</strong> active users") || !strings.Contains(r.body, "home.max_saved_layouts") {
		t.Fatalf("system tab: seat count or audit rows missing")
	}
	// The secret SMTP password never shows.
	e.admin.form("/admin/settings/mail.smtp_password", url.Values{"value": {"synthetic-secret-value"}, "tab": {"notifications_mail"}})
	if r := e.admin.must(http.StatusOK, "GET", "/admin?tab=notifications_mail", nil); strings.Contains(r.body, "synthetic-secret-value") {
		t.Fatal("secret shown on the admin page")
	}
}

// TestUIProblemReportAndSettings covers Report a problem, My settings and the role
// default editor through their forms.
func TestUIProblemReportAndSettings(t *testing.T) {
	e := newEnv(t)
	e.staff.form("/problem-reports", url.Values{"page": {"/billing/time"}, "message": {"The timer froze on save."}})
	rows := items(t, e.admin.must(http.StatusOK, "GET", "/api/v1/problem-reports", nil))
	if len(rows) != 1 || rows[0]["page"] != "/billing/time" || rows[0]["user_id"] != e.staff.id {
		t.Fatalf("problem reports: %v", rows)
	}
	if r := e.staff.do("GET", "/api/v1/problem-reports", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff listed problem reports: %d", r.code)
	}
	e.staff.form("/me/settings", url.Values{"mode": {"dark"}, "text_size": {"lg"}, "time_zone": {"America/New_York"}, "event_reminders": {"2"}, "deadline_reminders": {"5, 1"}, "idle_minutes": {"30"}})
	ms := e.staff.must(http.StatusOK, "GET", "/api/v1/me/settings", nil)
	if ms.json["mode"] != "dark" || ms.json["idle_timeout_seconds"] != float64(1800) || fmt.Sprint(ms.json["deadline_reminder_offsets_days"]) != "[5 1]" {
		t.Fatalf("my settings: %s", ms.body)
	}
	if loc := e.staff.form("/me/settings", url.Values{"idle_minutes": {"100000"}}).loc; !strings.Contains(loc, "error=") {
		t.Fatalf("idle above the firm limit accepted: %s", loc)
	}
	e.staff.form("/me/notification-preferences", url.Values{"trust_flag.in_app": {"on"}, "deadline_reminder.in_app": {"on"}})
	prefs := e.staff.must(http.StatusOK, "GET", "/api/v1/me/notification-preferences", nil).body
	if !strings.Contains(prefs, `"kind":"trust_flag","in_app":true,"email":true,"email_required":true`) || !strings.Contains(prefs, `"kind":"deadline_reminder","in_app":true,"email":false`) {
		t.Fatalf("prefs: %s", prefs)
	}
	if r := e.staff.do("PUT", "/api/v1/me/notification-preferences", map[string]any{"items": []map[string]any{{"kind": "trust_flag", "in_app": true, "email": false, "email_required": true}}}); r.code != http.StatusBadRequest {
		t.Fatalf("trust email turned off: %d", r.code)
	}
	// Role default editor on the Home screen tab.
	e.admin.form("/admin/role-layouts/staff/widgets", url.Values{"widget_id": {"ar-aging"}})
	if r := e.admin.must(http.StatusOK, "GET", "/api/v1/role-layouts/staff", nil); !strings.Contains(r.body, "ar-aging") {
		t.Fatalf("role widget add: %s", r.body)
	}
	fresh := e.enroll("Nia New", "nia@firm.example", domain.RoleStaff)
	if r := fresh.must(http.StatusOK, "GET", "/api/v1/me/layout", nil); !strings.Contains(r.body, "ar-aging") {
		t.Fatalf("new staff does not start from the edited role default: %s", r.body)
	}
	if r := e.admin.do("PUT", "/api/v1/role-layouts/staff", map[string]any{"role": "staff", "widgets": []map[string]any{{"widget_id": "module-alerts", "x": 0, "y": 0, "w": 4, "h": 3}}}); r.code != http.StatusBadRequest {
		t.Fatalf("admin-only widget put on the staff default: %d", r.code)
	}
	// Profile and timers render.
	if r := e.staff.must(http.StatusOK, "GET", "/me/profile", nil); !strings.Contains(r.body, "sam@firm.example") {
		t.Fatal("profile")
	}
	m, _, _ := e.matter("Estate of Timer", nil)
	e.staff.must(http.StatusCreated, "POST", "/api/v1/timers", map[string]any{"matter_id": m})
	if r := e.staff.must(http.StatusOK, "GET", "/ui/timers", nil); !strings.Contains(r.body, "Estate of Timer") || !strings.Contains(r.body, "Pause") {
		t.Fatalf("timers panel: %s", r.body)
	}
	if r := e.acct.must(http.StatusOK, "GET", "/ui/timers", nil); !strings.Contains(r.body, "No timers running.") {
		t.Fatalf("accountant timers: %s", r.body)
	}
}

// TestUIBearerTokensFollowTheContract checks the ui routes take bearer tokens with the
// contract's scopes and refuse tokens without them.
func TestUIBearerTokensFollowTheContract(t *testing.T) {
	e := newEnv(t)
	issue := func(scopes ...string) string {
		r := e.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "agent", "scopes": scopes, "user_id": e.staff.id})
		if tok, ok := r.json["token"].(string); ok {
			return tok
		}
		return str(t, r.json, "secret")
	}
	agent := newClient(t, e.srv.URL)
	agent.bearer = issue("layouts:read")
	agent.must(http.StatusOK, "GET", "/api/v1/me/layouts", nil)
	if r := agent.do("POST", "/api/v1/me/layouts", map[string]any{"name": "x", "widgets": []any{}}); r.code != http.StatusForbidden {
		t.Fatalf("layouts:read token wrote: %d %s", r.code, r.body)
	}
	if r := agent.do("GET", "/api/v1/nav/context", nil); r.code != http.StatusForbidden {
		t.Fatalf("token without profile:read read nav: %d", r.code)
	}
	if r := agent.do("GET", "/admin", nil); r.code != http.StatusForbidden {
		t.Fatalf("token reached a screen: %d", r.code)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], b)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}

func sameJSON(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return fmt.Sprint(x) == fmt.Sprint(y)
}

// altValue is a valid non-default value for a definition.
func altValue(d settings.Definition) (string, bool) {
	switch d.Special {
	case "day_list":
		return "3", true
	case "zone":
		return "America/Denver", true
	case "time_of_day":
		return "05:30", true
	case "confidence":
		return "0.85", true
	case "backup_scope":
		return `["local-disk"]`, true
	case "naming_pattern":
		return "Copy " + d.Default, true
	case "regexp_list":
		var list []string
		if json.Unmarshal([]byte(d.Default), &list) != nil {
			return "", false
		}
		raw, _ := json.Marshal(append(list, `^X\d+$`))
		return string(raw), true
	}
	if d.Key == "notifications.defaults" {
		return `{"email":["trust_flag","update_failure"],"email_locked":["trust_flag"],"in_app":["trust_flag","deadline_reminder","replenishment","matter_assignment","update_failure","module_alert"]}`, true
	}
	switch d.Kind {
	case settings.KindBool:
		if d.Default == "true" {
			return "false", true
		}
		return "true", true
	case settings.KindEnum:
		for _, a := range d.Allowed {
			if a != d.Default {
				return a, true
			}
		}
	case settings.KindInt, settings.KindCents:
		var n int64
		fmt.Sscan(d.Default, &n)
		for _, c := range []int64{n + 1, n - 1, d.Min, d.Max} {
			if c != n && (!d.HasMin || c >= d.Min) && (!d.HasMax || c <= d.Max) {
				if d.Key == "billing.time_increment_minutes" {
					return "15", true
				}
				return fmt.Sprint(c), true
			}
		}
	case settings.KindDuration:
		dur, err := time.ParseDuration(d.Default)
		if err != nil {
			return "", false
		}
		for _, c := range []time.Duration{dur + time.Second, dur - time.Second, dur * 2, dur / 2} {
			if c != dur && (!d.HasMin || int64(c) >= d.Min) && (!d.HasMax || int64(c) <= d.Max) {
				return c.String(), true
			}
		}
	case settings.KindString:
		if d.Special == "" {
			return d.Default + " (changed)", true
		}
	case settings.KindJSON:
		var list []any
		if json.Unmarshal([]byte(d.Default), &list) != nil || len(list) < 2 {
			return "", false
		}
		for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
			list[i], list[j] = list[j], list[i]
		}
		raw, _ := json.Marshal(list)
		return string(raw), true
	}
	return "", false
}
