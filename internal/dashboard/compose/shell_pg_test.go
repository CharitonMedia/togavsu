package compose

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// shellMarks are what every staff screen carries when it draws inside the app shell: the
// shell's body, its sidebar and its top bar.
var shellMarks = []string{`<body class="shell`, `<aside class="sidebar"`, `<header class="topbar">`}

// notScreens are the GET routes that are not staff screens, each with why: the API,
// static files, sign-in and setup, downloads, iframe content and HTML fragments that
// HTMX swaps into a screen. A new GET route must be listed in staffScreens below or here,
// so a page added without the shell fails this test.
var notScreens = map[string]string{
	"/{$}":                                  "the home screen, checked as /",
	"/home":                                 "redirects to /",
	"/trust":                                "redirects to /trust/ledger",
	"/admin/settings":                       "redirects to the admin screen's settings tab",
	"/login":                                "sign-in comes before the shell",
	"/logout":                               "sign-out",
	"/setup":                                "first-run setup comes before any user",
	"/health":                               "health check",
	"/healthz":                              "health check",
	"/llms.txt":                             "plain text for agents",
	"/favicon.ico":                          "icon",
	"/ical/{file}":                          "calendar feed download",
	"/help/guide":                           "the printable user guide, a document of its own",
	"/help/guide.pdf":                       "PDF download",
	"/billing/invoices/{invoice_id}/pdf":    "PDF download",
	"/files/{file_id}/edit/document":        "the document inside the editor's iframe",
	"/files/{file_id}/edit/context":         "JSON for the editor",
	"/editor/{rest...}":                     "the editor's own build, inside its iframe",
	"/editor-host/{name}":                   "the editor page's script",
	"/files/static/{name}":                  "static script",
	"/calendar/static/calendar.js":          "static script",
	"/practice/static/practice.css":         "sign-in page styles",
	"/ui/static/{rest...}":                  "static files",
	"/ui/appearance.css":                    "firm colors",
	"/ui/pictures/{user_id}":                "profile picture",
	"/ui/search":                            "search dropdown fragment",
	"/ui/notifications":                     "bell fragment",
	"/ui/timers":                            "timer fragment",
	"/modules/{module_id}/widgets/{widget}": "home widget fragment",
	"/modules/{module_id}/tabs/{tab}":       "needs an installed module; core TestExtensionsUIModules checks the shell",
	"/modules/{module_id}/matter-items/{item}": "needs an installed module; core TestExtensionsMatterItems checks the shell",
}

// routePattern finds a GET route's pattern in Go source: "GET /path".
var routePattern = regexp.MustCompile(`"GET (/[^" ]*)"`)

// staffRoutes reads every GET route the composed core registers for the browser.
func staffRoutes(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The supervisor is its own server on the host, not the firm's app.
			if d.Name() == "supervisor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range routePattern.FindAllStringSubmatch(string(raw), -1) {
			if !strings.HasPrefix(m[1], "/api/") && !strings.HasPrefix(m[1], "/portal") {
				seen[m[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// TestEveryStaffScreenDrawsInTheShell renders every staff screen as an admin and fails if
// one comes back without the app shell (sidebar and top bar), as the Matters page once did.
// It runs in the Core tests job (scripts/ci/test-core.sh runs every Go package).
func TestEveryStaffScreenDrawsInTheShell(t *testing.T) {
	e := newEnv(t)
	matter, _, client := e.matter("Estate of Harlow", nil)
	invoice, _ := e.invoice(matter)
	timeEntry := items(t, e.atty.must(http.StatusOK, "GET", "/api/v1/time-entries?matter_id="+matter, nil))[0]["id"].(string)
	expenseType := str(t, e.admin.must(http.StatusCreated, "POST", "/api/v1/expense-types", map[string]any{"name": "Filing fees"}).json, "id")
	expense := str(t, e.atty.must(http.StatusCreated, "POST", "/api/v1/expenses", map[string]any{"matter_id": matter, "incurred_on": firmToday(),
		"amount_cents": 45_00, "description": "Filing fee", "billable": true, "expense_type_id": expenseType}).json, "id")
	deadline := str(t, e.atty.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": matter, "title": "File inventory",
		"due_on": time.Now().AddDate(0, 0, 4).Format("2006-01-02"), "responsible_user_id": e.staff.id}).json, "id")
	start := time.Now().Truncate(time.Hour).Add(time.Hour).UTC()
	event := str(t, e.atty.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": "Hearing", "kind": "court_date", "matter_id": matter,
		"starts_at": start.Format(time.RFC3339), "ends_at": start.Add(time.Hour).Format(time.RFC3339), "all_day": false, "attendee_user_ids": []string{e.atty.id}}).json, "id")
	e.atty.must(http.StatusCreated, "POST", "/api/v1/trust-ledger", map[string]any{"trust_account_id": e.account, "client_id": client, "matter_id": matter,
		"entry_type": "withdrawal", "amount_cents": 40_00, "method": "check", "description": "Filing fee"})
	flag := items(t, e.admin.must(http.StatusOK, "GET", "/api/v1/trust-flags", nil))[0]["id"].(string)
	// An id no row has: the screen's not-found page must still draw inside the shell.
	const missing = "0193a000-0000-7000-8000-000000000000"

	staffScreens := map[string]string{
		"/":                                   "/",
		"/admin":                              "/admin",
		"/admin/users/{user_id}":              "/admin/users/" + e.atty.id,
		"/admin/doc-tender":                   "/admin/doc-tender",
		"/search":                             "/search?q=Harlow",
		"/notifications":                      "/notifications",
		"/timers":                             "/timers",
		"/me/settings":                        "/me/settings",
		"/me/profile":                         "/me/profile",
		"/quick-create":                       "/quick-create",
		"/help":                               "/help",
		"/help/topics/{topic_id}":             "/help/topics/practice-matters",
		"/practice/matters":                   "/practice/matters",
		"/practice/matters/new":               "/practice/matters/new",
		"/practice/matters/needs-case-number": "/practice/matters/needs-case-number",
		"/practice/matters/{matter_id}":       "/practice/matters/" + matter,
		"/practice/matters/{matter_id}/close": "/practice/matters/" + matter + "/close",
		"/practice/matters/{matter_id}/files": "/practice/matters/" + matter + "/files",
		"/practice/contacts":                  "/practice/contacts",
		"/practice/contacts/new":              "/practice/contacts/new",
		"/practice/contacts/{contact_id}":     "/practice/contacts/" + client,
		"/practice/notes":                     "/practice/notes",
		"/practice/activity":                  "/practice/activity",
		"/practice/search":                    "/practice/search?q=Harlow",
		"/billing":                            "/billing",
		"/billing/time":                       "/billing/time",
		"/billing/time/{time_entry_id}":       "/billing/time/" + timeEntry,
		"/billing/expenses":                   "/billing/expenses",
		"/billing/expenses/{expense_id}":      "/billing/expenses/" + expense,
		"/billing/invoices":                   "/billing/invoices",
		"/billing/invoices/{invoice_id}":      "/billing/invoices/" + invoice,
		"/billing/payments":                   "/billing/payments",
		"/billing/reports/{report}":           "/billing/reports/ar-aging",
		"/trust/ledger":                       "/trust/ledger",
		"/trust/balances":                     "/trust/balances",
		"/trust/flags":                        "/trust/flags",
		"/trust/flags/{trust_flag_id}":        "/trust/flags/" + flag,
		"/trust/replenishment":                "/trust/replenishment",
		"/trust/reconciliation":               "/trust/reconciliation",
		"/trust/matters/{matter_id}":          "/trust/matters/" + matter,
		"/trust/notifications":                "/trust/notifications",
		"/calendar":                           "/calendar",
		"/calendar/events/{id}":               "/calendar/events/" + event,
		"/calendar/deadlines":                 "/calendar/deadlines",
		"/calendar/deadlines/{id}":            "/calendar/deadlines/" + deadline,
		"/calendar/available-dates":           "/calendar/available-dates",
		"/calendar/feeds":                     "/calendar/feeds",
		"/files/admin/storage":                "/files/admin/storage",
		"/files/{file_id}/edit":               "/files/" + missing + "/edit",
		"/matters/{matter_id}/pleadings/new":  "/matters/" + matter + "/pleadings/new",
		"/doc-tender/review":                  "/doc-tender/review",
		"/doc-tender/review/{exception_id}":   "/doc-tender/review/" + missing,
		"/doc-tender/guide":                   "/doc-tender/guide",
	}
	for _, route := range staffRoutes(t) {
		if _, ok := notScreens[route]; ok {
			continue
		}
		if _, ok := staffScreens[route]; !ok {
			t.Errorf("GET %s is not checked: add it to staffScreens with a path to render, or to notScreens with why it is not a screen", route)
		}
	}
	if len(staffScreens) < 50 {
		t.Fatalf("only %d screens to check; the route list is broken", len(staffScreens))
	}
	paths := make([]string, 0, len(staffScreens))
	for route := range staffScreens {
		paths = append(paths, route)
	}
	sort.Strings(paths)
	for _, route := range paths {
		path := staffScreens[route]
		r := e.admin.do("GET", path, nil)
		if r.code >= http.StatusInternalServerError || r.code == http.StatusSeeOther || r.code == http.StatusFound {
			t.Errorf("GET %s (%s): %d %s", path, route, r.code, r.loc)
			continue
		}
		for _, mark := range shellMarks {
			if !strings.Contains(r.body, mark) {
				t.Errorf("GET %s (%s, %d) draws outside the app shell: no %s", path, route, r.code, mark)
				break
			}
		}
		if strings.Count(r.body, "<body") != 1 || strings.Count(r.body, `<main id="main"`) != 1 {
			t.Errorf("GET %s (%s) nests a page inside the shell", path, route)
		}
	}
}
