package compose

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

type qcPage struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Path   string `json:"path"`
	Expect int    `json:"expect,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Action string `json:"action,omitempty"`
	Query  string `json:"query,omitempty"`
	Full   bool   `json:"full,omitempty"`
}

// TestUIVisualQC seeds synthetic records, serves the composed core and runs
// qc/ui-qc.mjs in headless Chrome over every ui screen and admin tab for each role.
// It runs only with TOGAVSU_UI_QC_OUT (the screenshot folder) and
// TOGAVSU_UI_QC_NODE_MODULES (a node_modules holding puppeteer-core) set; CI runs the
// HTTP tests above, which cover the same behavior without a browser.
func TestUIVisualQC(t *testing.T) {
	out, mods := os.Getenv("TOGAVSU_UI_QC_OUT"), os.Getenv("TOGAVSU_UI_QC_NODE_MODULES")
	if out == "" || mods == "" {
		t.Skip("TOGAVSU_UI_QC_OUT and TOGAVSU_UI_QC_NODE_MODULES are not set")
	}
	e := newEnv(t)
	// Synthetic seed: open and walled matters, a phone, invoices, a trust flag,
	// deadlines, events, time, a timer, help content and saved layouts.
	e.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/firm.short_name", map[string]any{"value": "Synthetic Firm"})
	harlow, number, harlowClient := e.matter("Estate of Harlow", nil)
	e.admin.must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{"contact_id": harlowClient, "label": "mobile", "phone": "+13125550187", "e164": "+13125550187", "primary": true})
	moreno, _, morenoClient := e.matter("Guardianship of Moreno", nil)
	walled, _, _ := e.matter("Walled Estate of Quist", nil)
	e.wall(walled, e.atty.id, e.admin.id)
	e.invoice(harlow)
	today := firmToday()
	for _, d := range []struct{ m, title string }{{harlow, "File inventory"}, {moreno, "Annual guardianship report"}, {walled, "Quist accounting"}} {
		e.atty.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": d.m, "title": d.title, "due_on": time.Now().AddDate(0, 0, 4).Format("2006-01-02"), "responsible_user_id": e.staff.id})
	}
	start := time.Now().Truncate(time.Hour).Add(time.Hour).UTC()
	e.atty.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": "Hearing on inventory", "kind": "court_date", "matter_id": harlow,
		"starts_at": start.Format(time.RFC3339), "ends_at": start.Add(time.Hour).Format(time.RFC3339), "all_day": false, "attendee_user_ids": []string{e.atty.id}})
	for _, c := range []*client{e.atty, e.staff} {
		c.must(http.StatusCreated, "POST", "/api/v1/time-entries", map[string]any{"matter_id": moreno, "worked_on": today, "increments": 5, "description": "Review guardianship file", "activity_type_id": e.activityID, "billable": true, "rate_cents": 250_00})
	}
	e.staff.must(http.StatusCreated, "POST", "/api/v1/timers", map[string]any{"matter_id": harlow})
	e.admin.must(http.StatusCreated, "POST", "/api/v1/trust-ledger", map[string]any{"trust_account_id": e.account, "client_id": morenoClient, "matter_id": moreno, "entry_type": "deposit", "amount_cents": 100_00, "method": "check", "description": "Retainer"})
	e.atty.must(http.StatusCreated, "POST", "/api/v1/trust-ledger", map[string]any{"trust_account_id": e.account, "client_id": morenoClient, "matter_id": moreno, "entry_type": "withdrawal", "amount_cents": 140_00, "method": "check", "description": "Filing fee"})
	e.admin.form("/admin/help/sections", url.Values{"title": {"Firm procedures"}})
	sec := items(t, e.admin.must(http.StatusOK, "GET", "/api/v1/help/sections", nil))[0]["id"].(string)
	e.admin.form("/admin/help/items", url.Values{"section_id": {sec}, "title": {"Opening a new file"}, "kind": {"text"}, "body": {"Run the **conflict check** before the engagement letter goes out.\n\n- Add every party\n- Record the decision"}})
	e.admin.form("/admin/help/sections", url.Values{"title": {"Training videos"}})
	raw, _ := json.Marshal([]dashboard.LayoutWidget{{WidgetID: "my-time", X: 0, Y: 0, W: 4, H: 3}, {WidgetID: "upcoming-deadlines", X: 4, Y: 0, W: 8, H: 4}})
	e.atty.form("/home/layouts", url.Values{"name": {"Trial prep"}, "widgets": {string(raw)}, "default": {"on"}})
	e.staff.must(http.StatusOK, "PUT", "/api/v1/me/sidebar", map[string]any{"collapsed": false, "subgroups": []map[string]any{{"id": "money.trust", "collapsed": true}}})
	e.createUser("Cora Client", "cora@client.example", "", domain.RoleClient)
	cl := e.signIn("Cora Client", "cora@client.example")

	cookies := func(c *client) []map[string]string {
		u, _ := url.Parse(e.srv.URL)
		var out []map[string]string
		for _, ck := range c.http.Jar.Cookies(u) {
			if ck.Name == identity.SessionCookie("").Name || ck.Name == practice.CSRFCookie {
				out = append(out, map[string]string{"name": ck.Name, "value": ck.Value})
			}
		}
		return out
	}
	var pages []qcPage
	add := func(p qcPage) {
		p.Name = p.Role + "-" + p.Name
		pages = append(pages, p)
	}
	add(qcPage{Name: "01-home", Role: "admin", Path: "/", Full: true})
	for i, tab := range dashboard.AdminTabIDs() {
		add(qcPage{Name: "admin-" + twoDigits(i+1) + "-" + tab, Role: "admin", Path: "/admin?tab=" + tab, Full: true})
	}
	add(qcPage{Name: "02-search-dropdown", Role: "admin", Path: "/", Action: "search", Query: number})
	add(qcPage{Name: "03-ctrl-k", Role: "admin", Path: "/help", Action: "ctrlk"})
	add(qcPage{Name: "04-help", Role: "admin", Path: "/help", Full: true})
	add(qcPage{Name: "05-help-topic", Role: "admin", Path: "/help/topics/admin-time_billing"})
	add(qcPage{Name: "06-guide-html", Role: "admin", Path: "/help/guide"})
	add(qcPage{Name: "07-quick-create", Role: "admin", Path: "/quick-create"})
	add(qcPage{Name: "08-problem-reports", Role: "admin", Path: "/admin?tab=system"})
	add(qcPage{Name: "01-home", Role: "attorney", Path: "/", Full: true})
	add(qcPage{Name: "02-home-edit", Role: "attorney", Path: "/?edit=1", Action: "editclick", Full: true})
	add(qcPage{Name: "03-bell", Role: "attorney", Path: "/", Action: "bell"})
	add(qcPage{Name: "04-notifications", Role: "attorney", Path: "/notifications"})
	add(qcPage{Name: "05-search-page", Role: "attorney", Path: "/search?q=Quist&scope=all"})
	add(qcPage{Name: "06-my-settings", Role: "attorney", Path: "/me/settings", Full: true})
	add(qcPage{Name: "07-profile", Role: "attorney", Path: "/me/profile"})
	add(qcPage{Name: "01-home", Role: "staff", Path: "/", Full: true})
	add(qcPage{Name: "02-search-phone", Role: "staff", Path: "/", Action: "search", Query: "5550187"})
	add(qcPage{Name: "03-search-walled", Role: "staff", Path: "/search?q=Quist&scope=all"})
	add(qcPage{Name: "04-timers", Role: "staff", Path: "/", Action: "timers"})
	add(qcPage{Name: "05-timers-page", Role: "staff", Path: "/timers"})
	add(qcPage{Name: "06-help", Role: "staff", Path: "/help"})
	add(qcPage{Name: "07-admin-refused", Role: "staff", Path: "/admin", Expect: http.StatusForbidden})
	add(qcPage{Name: "08-phone-home", Role: "staff", Path: "/", Width: 390, Height: 844, Full: true})
	add(qcPage{Name: "09-collapsed-sidebar", Role: "staff", Path: "/", Action: "collapse"})
	add(qcPage{Name: "10-deadlines-click-through", Role: "staff", Path: "/calendar/deadlines?from_date=" + today + "&to_date=" + time.Now().AddDate(0, 0, 7).Format("2006-01-02") + "&status=open&include_overdue=true"})
	add(qcPage{Name: "01-home", Role: "accountant", Path: "/", Full: true})
	add(qcPage{Name: "02-help", Role: "accountant", Path: "/help"})
	add(qcPage{Name: "03-my-settings", Role: "accountant", Path: "/me/settings"})
	add(qcPage{Name: "01-help-404", Role: "client", Path: "/help", Expect: http.StatusNotFound})
	add(qcPage{Name: "02-guide-404", Role: "client", Path: "/help/guide", Expect: http.StatusNotFound})
	add(qcPage{Name: "01-llms", Role: "anonymous", Path: "/llms.txt"})
	plan := map[string]any{"base": e.srv.URL, "pages": pages, "sessions": map[string]any{
		"admin": cookies(e.admin), "attorney": cookies(e.atty), "staff": cookies(e.staff), "accountant": cookies(e.acct), "client": cookies(cl), "anonymous": []any{},
	}}
	dir := t.TempDir()
	planFile := filepath.Join(dir, "plan.json")
	rawPlan, _ := json.Marshal(plan)
	if err := os.WriteFile(planFile, rawPlan, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "qc/ui-qc.mjs", planFile, out)
	cmd.Env = append(os.Environ(), "QC_NODE_MODULES="+mods)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("visual QC failed: %v", err)
	}
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
