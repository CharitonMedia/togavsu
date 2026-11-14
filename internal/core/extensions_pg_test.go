package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/api"
	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/testkit"
	service "github.com/CharitonMedia/togavsu/marketplace"
)

// These are the integrate-extensions phase cases: the ui, the admin page, the help
// center, the module runtime, the marketplace client side and module AI calls, all on
// the running core core.Open builds (no second composition), over HTTP with real
// sessions, CSRF and bearer tokens, on Postgres as the non-superuser core role.

// ext is a running core with the module runtime on fakes for the supervisor only:
// testkit.Runtime starts each deployed module's server in this process with the secrets
// the core handed over (matter-clock unless a test routes the module elsewhere), and
// testkit.Backups records synthetic archives.
type ext struct {
	*docs
	rt      *testkit.Runtime
	backups *testkit.Backups
	priv    ed25519.PrivateKey
}

func newExt(t *testing.T, cfg ...func(*Config)) *ext {
	t.Helper()
	x := &ext{backups: &testkit.Backups{}}
	all := append([]func(*Config){func(c *Config) {
		x.rt = testkit.NewRuntime(func() string { return x.srv.URL }, c.DatabaseURL)
		x.rt.Default = func(m testkit.Module) http.Handler {
			return matterclock.New(matterclock.Config{Core: m.CoreURL, Token: m.Token, WebhookSecret: m.WebhookSecret, DB: m.DB, SkewSeconds: 300}).Handler()
		}
		t.Cleanup(x.rt.Close)
		c.ModuleRuntime, c.ModuleBackups, c.NoBackground = x.rt, x.backups, true
		testkit.DropModuleRoles(t, c.DatabaseURL)
	}}, cfg...)
	x.docs = newDocs(t, all...)
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	x.priv = priv
	return x
}

func (x *ext) moduleDB(role, password string) *sql.DB {
	x.t.Helper()
	d, err := x.rt.OpenDB(role, password)
	if err != nil {
		x.t.Fatal(err)
	}
	return d
}

// principal is c's signed-in principal, for direct service calls in setup.
func (x *ext) principal(c *client) domain.Principal {
	x.t.Helper()
	return testkit.Caller(x.t, x.app.Identity, c.kit()).Principal
}

// trustKey adds pub as a trusted local key over the API.
func (x *ext) trustKey(name string, pub ed25519.PublicKey) {
	x.t.Helper()
	testkit.TrustKey(x.admin.kit(), name, pub)
}

func (x *ext) build(edit func(map[string][]byte)) []byte {
	x.t.Helper()
	raw, err := matterclock.Build(x.priv, edit)
	if err != nil {
		x.t.Fatal(err)
	}
	return raw
}

// upload posts a multipart form with one package file, as the browser does.
func (c *client) upload(path string, fields url.Values, pkg []byte) resp {
	c.t.Helper()
	return fromKit(c.kit().Upload(path, fields, pkg))
}

// tabForm posts an admin Modules tab form with the session's CSRF field and returns the
// redirect's notice or error.
func (x *ext) tabForm(c *client, action string, v url.Values, pkg []byte) (notice, problem string) {
	x.t.Helper()
	return testkit.TabForm(c.kit(), action, v, pkg)
}

// approveOnTab approves every undecided permission of module through the tab.
func (x *ext) approveOnTab(module string) {
	x.t.Helper()
	testkit.ApproveOnTab(x.admin.kit(), x.app.Modules, x.adminP(), module)
}

func (x *ext) installSample() {
	x.t.Helper()
	if _, problem := x.tabForm(x.admin, "install", nil, x.build(nil)); problem != "" {
		x.t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
}

func navHas(t *testing.T, c *client, id string) bool {
	t.Helper()
	r := c.must(http.StatusOK, "GET", "/api/v1/nav/context", nil)
	return strings.Contains(r.body, `"id":"`+id+`"`)
}

func widgetOffered(t *testing.T, c *client, id string) bool {
	t.Helper()
	r := c.must(http.StatusOK, "GET", "/api/v1/widgets", nil)
	return strings.Contains(r.body, `"id":"`+id+`"`)
}

// TestExtensionsModuleLifecycle is the module-lifecycle phase case (AC-012, AC-013,
// AC-014, AC-033, AC-036, AC-037, AC-038) on the running core: signature refusals, a
// local install from the admin tab with its permission decisions, the widget, tab,
// sidebar entry and guide appearing with no restart, include= on the matter screen and
// API inside walls, event webhooks, a second module's published view behind approval,
// refused migrations, calendar replacement and back, and uninstall with Delete data.
func TestExtensionsModuleLifecycle(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	open, _ := x.matter("Estate of Harlow", "", nil)
	walled, _ := x.matter("Estate of Quist", "", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}

	// AC-013: unsigned, tampered and untrusted packages are refused before anything runs.
	signed := x.build(nil)
	if r := x.admin.upload("/api/v1/modules", nil, signed); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("package signed by an untrusted key: %d %s", r.code, r.body)
	}
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	archive, err := matterclock.Archive(nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := x.admin.upload("/api/v1/modules", nil, archive); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("unsigned package: %d %s", r.code, r.body)
	}
	var file marketplace.SignedPackage
	if err := json.Unmarshal(signed, &file); err != nil {
		t.Fatal(err)
	}
	body, _ := base64.StdEncoding.DecodeString(file.PackageBase64)
	body[len(body)/2] ^= 0xff
	file.PackageBase64 = base64.StdEncoding.EncodeToString(body)
	tampered, _ := json.Marshal(file)
	if _, problem := x.tabForm(x.admin, "install", nil, tampered); problem == "" {
		t.Fatal("tampered package accepted on the admin tab")
	}
	if x.count(`SELECT COUNT(*) FROM core.modules`) != 0 || len(x.rt.Deployments()) != 0 {
		t.Fatal("a refused package left a module behind")
	}

	// AC-037: a migration that makes its own contacts table, writes outside its schema
	// or adds a trigger is refused at install.
	for name, sql := range map[string]string{
		"contacts table": "CREATE TABLE contacts (id TEXT PRIMARY KEY);",
		"outside schema": "CREATE TABLE core.module_rows (id TEXT PRIMARY KEY);",
		"trigger":        "CREATE FUNCTION f() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$; CREATE TRIGGER t BEFORE INSERT ON matter_targets FOR EACH ROW EXECUTE FUNCTION f();",
	} {
		pkg := x.build(func(f map[string][]byte) { f["migrations/0002_bad.sql"] = []byte(sql) })
		if r := x.admin.upload("/api/v1/modules", nil, pkg); r.code != http.StatusUnprocessableEntity {
			t.Fatalf("%s migration accepted: %d %s", name, r.code, r.body)
		}
	}

	// AC-012: install from a local file on the admin tab; it asks for its permissions and
	// nothing runs until the admin decides. Staff can't install.
	if r := x.staff.upload("/api/v1/modules", nil, signed); r.code != http.StatusForbidden {
		t.Fatalf("staff install: %d", r.code)
	}
	notice, problem := x.tabForm(x.admin, "install", nil, signed)
	if problem != "" || !strings.Contains(notice, "permission") {
		t.Fatalf("install on the tab: %q %q", notice, problem)
	}
	tab := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	for _, want := range []string{`id="module-matter-clock"`, "awaiting_approval", `name="scope:matters:read"`, `name="scope:time:read"`, `name="scope:events:read"`} {
		if !strings.Contains(tab.body, want) {
			t.Fatalf("modules tab lacks %q", want)
		}
	}
	if len(x.rt.Deployments()) != 0 || widgetOffered(t, x.staff, "matter-clock.hours") {
		t.Fatal("the module ran before approval")
	}
	x.approveOnTab("matter-clock")
	dep := x.rt.Last("matter-clock")
	if dep.Secrets["togavsu_api_token"] == "" || dep.Secrets["togavsu_db_role"] == "" {
		t.Fatalf("deployment: %+v", dep)
	}

	// Shown with no restart: the widget in the picker with its manifest's tab target, the
	// home tab framed with a context token, a sidebar entry, and the guide in /help.
	if !widgetOffered(t, x.staff, "matter-clock.hours") {
		t.Fatal("module widget not offered after install")
	}
	frame := x.staff.must(http.StatusOK, "GET", "/modules/matter-clock/tabs/hours", nil)
	if !strings.Contains(frame.body, `/m/matter-clock/tab?context=`) || !strings.Contains(frame.body, "sandbox") {
		t.Fatalf("home tab frame: %s", frame.body)
	}
	// A module tab is a screen: it draws inside the app shell like the core's own pages.
	if !strings.Contains(frame.body, `<body class="shell`) || !strings.Contains(frame.body, `<aside class="sidebar"`) || !strings.Contains(frame.body, `<header class="topbar">`) {
		t.Fatalf("module tab outside the app shell: %s", frame.body)
	}
	if !strings.Contains(frame.body, `data-idle-seconds="0"`) {
		t.Fatal("a module tab is worked in its iframe, so the client idle sign-out must be off")
	}
	if !navHas(t, x.staff, "matter-clock.hours") {
		t.Fatal("module tab not in the sidebar")
	}
	help := x.staff.must(http.StatusOK, "GET", "/help", nil)
	if !strings.Contains(help.body, "Matter Clock") {
		t.Fatal("module guide not under its own heading in /help")
	}
	if r := x.staff.must(http.StatusOK, "GET", "/help?q=target", nil); !strings.Contains(r.body, "matter-clock.") {
		t.Fatal("module guide not found by help search")
	}

	// AC-036: the module's own login writes its target; it can't read core contacts.
	mdb := x.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	for _, m := range []string{open, walled} {
		if err := matterclock.SetTarget(ctx, mdb, m, 120, "partner note", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mdb.ExecContext(ctx, `SELECT 1 FROM core.contacts LIMIT 1`); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("module login read core contacts: %v", err)
	}
	// include= on the API: staff sees the target, not the attorney-only note; the walled
	// matter is not found for staff and shows its row to the attorney inside the wall.
	inc := x.staff.must(http.StatusOK, "GET", "/api/v1/matters/"+open+"?include=matter-clock.targets", nil)
	if !strings.Contains(inc.body, `"monthly_target_tenths":120`) || strings.Contains(inc.body, "partner note") {
		t.Fatalf("staff include=: %s", inc.body)
	}
	if r := x.staff.do("GET", "/api/v1/matters/"+walled+"?include=matter-clock.targets", nil); r.code != http.StatusNotFound || strings.Contains(r.body, "120") {
		t.Fatalf("staff outside the wall: %d %s", r.code, r.body)
	}
	if r := x.atty.must(http.StatusOK, "GET", "/api/v1/matters/"+walled+"?include=matter-clock.targets", nil); !strings.Contains(r.body, "partner note") {
		t.Fatalf("attorney inside the wall: %s", r.body)
	}
	list := x.staff.must(http.StatusOK, "GET", "/api/v1/matters?include=matter-clock.targets", nil)
	if strings.Contains(list.body, walled) {
		t.Fatal("a walled matter's row reached staff through a list include")
	}
	// The matter screen shows the target through include= and frames the matter tab.
	screen := x.staff.must(http.StatusOK, "GET", "/practice/matters/"+open, nil)
	for _, want := range []string{`id="module-matter-clock"`, "monthly_target_tenths", "120", "/modules/matter-clock/tabs/matter?matter_id=" + open} {
		if !strings.Contains(screen.body, want) {
			t.Fatalf("matter screen lacks %q", want)
		}
	}
	if strings.Contains(screen.body, "partner note") {
		t.Fatal("matter screen showed the attorney-only note to staff")
	}
	if r := x.atty.must(http.StatusOK, "GET", "/practice/matters/"+walled, nil); !strings.Contains(r.body, "partner note") {
		t.Fatal("matter screen hid the note from the attorney inside the wall")
	}

	// The module's framed page calls the core with its token and the viewer's context: a
	// staff viewer's widget lists only the matters staff may see.
	widget := x.staff.must(http.StatusOK, "GET", "/api/v1/widgets/matter-clock.hours", nil)
	if widget.json == nil {
		t.Fatalf("module widget data: %s", widget.body)
	}

	// Events: a new time entry is pumped from core.events and posted to the module with
	// its signature.
	x.staff.must(http.StatusCreated, "POST", "/api/v1/time-entries", map[string]any{"matter_id": open, "worked_on": time.Now().UTC().Format(time.DateOnly),
		"increments": 5, "description": "Synthetic drafting", "activity_type_id": x.activity, "billable": true})
	if n, err := x.app.Modules.Pump(ctx); err != nil || n == 0 {
		t.Fatalf("pump queued %d: %v (time entry events %d)", n, err, x.count(`SELECT COUNT(*) FROM core.events WHERE action LIKE 'time_entry.%'`))
	}
	if sent, err := x.app.Modules.Deliver(ctx); err != nil || sent == 0 {
		t.Fatalf("webhook delivery: %d %v", sent, err)
	}
	gotEvent := false
	for _, p := range x.rt.Paths("matter-clock") {
		gotEvent = gotEvent || p == "/events"
	}
	if !gotEvent {
		t.Fatalf("the module got no event: %v", x.rt.Paths("matter-clock"))
	}
	if r := x.admin.must(http.StatusOK, "GET", "/api/v1/webhook-deliveries?module_id=matter-clock", nil); !strings.Contains(r.body, "time_entry.") {
		t.Fatalf("delivery log: %s", r.body)
	}

	// AC-037 on update: a changed migration that already ran is refused and 1.0.0 keeps
	// running.
	changed := x.build(func(f map[string][]byte) {
		for k, v := range f {
			if strings.HasPrefix(k, "migrations/") {
				f[k] = append(v, []byte("\n-- changed after it ran\n")...)
			}
		}
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte(`version: "1.0.0"`), []byte(`version: "1.0.1"`), 1)
	})
	if _, problem := x.tabForm(x.admin, "update", url.Values{"module_id": {"matter-clock"}}, changed); problem == "" {
		t.Fatal("an update that changes a run migration was accepted")
	}
	if v, _ := x.app.Modules.Get(ctx, x.adminP(), "matter-clock"); v.Version != "1.0.0" || !v.Enabled {
		t.Fatalf("after a refused update: %+v", v)
	}

	// AC-038: a matter module rows point to can't be hard-deleted.
	if _, err := x.app.Pool.ExecContext(ctx, `DELETE FROM core.matters WHERE id = $1`, open); err == nil || !strings.Contains(err.Error(), "foreign key") {
		t.Fatalf("hard delete of a linked matter: %v", err)
	}
	if holders, err := x.app.Modules.Schemas().Holders(ctx, "matters", domain.ID(open)); err != nil || len(holders) != 1 || holders[0] != "matter-clock" {
		t.Fatalf("the matter's module holders: %v %v", holders, err)
	}

	// AC-014: a module switched in for calendar takes the calendar API and screens on the
	// next request; switching back restores the core's.
	calendar, _ := extCalendarModule(t)
	x.rt.Handle("calendar-plus", calendar)
	if _, problem := x.tabForm(x.admin, "install", nil, x.build(onlyManifest(extCalendarManifest))); problem != "" {
		t.Fatalf("install calendar-plus: %s", problem)
	}
	x.approveOnTab("calendar-plus")
	before := x.staff.must(http.StatusOK, "GET", "/api/v1/calendar-events", nil)
	if notice, problem := x.tabForm(x.admin, "domain", url.Values{"domain": {"calendar"}, "module_id": {"calendar-plus"}}, nil); problem != "" || !strings.Contains(notice, "calendar") {
		t.Fatalf("switch calendar: %q %q", notice, problem)
	}
	for _, path := range []string{"/api/v1/calendar-events", "/calendar"} {
		if r := x.staff.must(http.StatusOK, "GET", path, nil); r.json["served_by"] != "calendar-plus" {
			t.Fatalf("%s after the switch: %s", path, r.body)
		}
	}
	if r := x.staff.must(http.StatusOK, "GET", "/api/v1/time-entries", nil); strings.Contains(r.body, "calendar-plus") {
		t.Fatal("time went to the calendar module")
	}
	// A replaced domain keeps the core's gates: signed out, a browser screen goes to
	// sign-in and an API call gets 401; a cookie write without CSRF gets 403; a token
	// without the route's scope gets 403. None of them reaches the module.
	seen := len(x.rt.Paths("calendar-plus"))
	anon := newClient(t, x.srv.URL)
	if r := anon.do("GET", "/api/v1/calendar-events", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("signed-out API call to the replaced calendar: %d %s", r.code, r.body)
	}
	if r := anon.do("GET", "/calendar", nil); r.code != http.StatusSeeOther || r.loc != "/login" {
		t.Fatalf("signed-out calendar screen: %d %q", r.code, r.loc)
	}
	if r := anon.do("POST", "/api/v1/calendar-events", map[string]any{"title": "Hearing"}); r.code != http.StatusUnauthorized {
		t.Fatalf("signed-out write to the replaced calendar: %d %s", r.code, r.body)
	}
	if r := x.staff.do("POST", "/api/v1/calendar-events", map[string]any{"title": "Hearing"}, "X-CSRF-Token", ""); r.code != http.StatusForbidden {
		t.Fatalf("cookie write without CSRF to the replaced calendar: %d %s", r.code, r.body)
	}
	scoped := x.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "matters only", "scopes": []string{"matters:read"}})
	reader := newClient(t, x.srv.URL)
	reader.bearer = str(t, scoped.json, "secret")
	if r := reader.do("GET", "/api/v1/calendar-events", nil); r.code != http.StatusForbidden {
		t.Fatalf("token without the calendar scope: %d %s", r.code, r.body)
	}
	if got := x.rt.Paths("calendar-plus"); len(got) != seen {
		t.Fatalf("a refused request reached the replacement module: %v", got[seen:])
	}
	if _, problem := x.tabForm(x.admin, "domain", url.Values{"domain": {"calendar"}, "module_id": {""}}, nil); problem != "" {
		t.Fatalf("switch back: %s", problem)
	}
	if r := x.staff.must(http.StatusOK, "GET", "/api/v1/calendar-events", nil); r.body != before.body {
		t.Fatalf("after switching back: %s (before %s)", r.body, before.body)
	}

	// Disable takes the widget, sidebar entry and tab away with no restart; enable
	// brings them back.
	x.tabForm(x.admin, "disable", url.Values{"module_id": {"matter-clock"}}, nil)
	if widgetOffered(t, x.staff, "matter-clock.hours") || navHas(t, x.staff, "matter-clock.hours") {
		t.Fatal("a disabled module still shows")
	}
	if r := x.staff.do("GET", "/modules/matter-clock/tabs/hours", nil); r.code != http.StatusNotFound {
		t.Fatalf("disabled module tab: %d", r.code)
	}
	x.tabForm(x.admin, "enable", url.Values{"module_id": {"matter-clock"}}, nil)
	if !widgetOffered(t, x.staff, "matter-clock.hours") || !navHas(t, x.staff, "matter-clock.hours") {
		t.Fatal("re-enabled module does not show")
	}

	// AC-038: Delete data needs the typed confirmation and drops the schema only after a
	// backup; the guide, widget and sidebar entry go with no restart.
	if _, problem := x.tabForm(x.admin, "uninstall", url.Values{"module_id": {"matter-clock"}, "data": {"delete"}, "confirmation": {"wrong"}}, nil); problem == "" {
		t.Fatal("delete data without the typed confirmation")
	}
	if x.count(`SELECT COUNT(*) FROM pg_namespace WHERE nspname = 'mod_matter_clock'`) != 1 {
		t.Fatal("the schema went without confirmation")
	}
	backups := len(x.backups.Made())
	if _, problem := x.tabForm(x.admin, "uninstall", url.Values{"module_id": {"matter-clock"}, "data": {"delete"}, "confirmation": {"matter-clock"}}, nil); problem != "" {
		t.Fatalf("uninstall: %s", problem)
	}
	if len(x.backups.Made()) != backups+1 || x.count(`SELECT COUNT(*) FROM pg_namespace WHERE nspname = 'mod_matter_clock'`) != 0 {
		t.Fatal("delete data without a fresh backup, or the schema stayed")
	}
	if widgetOffered(t, x.staff, "matter-clock.hours") || navHas(t, x.staff, "matter-clock.hours") {
		t.Fatal("an uninstalled module still shows")
	}
	if r := x.staff.must(http.StatusOK, "GET", "/help", nil); strings.Contains(r.body, "Matter Clock") {
		t.Fatal("the guide stayed after uninstall")
	}
	if _, err := x.app.Pool.ExecContext(ctx, `DELETE FROM core.time_entries WHERE matter_id = $1`, open); err != nil {
		t.Fatal(err)
	}
}

// TestExtensionsMatterItems: a module that declares only matter_items, with no matter
// tab and no extension rows, shows its panel framed inline and its action as a button on
// the matter screen, and each opens the module's page through the sandboxed frame with a
// context token for that matter.
func TestExtensionsMatterItems(t *testing.T) {
	x := newExt(t)
	open, _ := x.matter("Estate of Pell", "", nil)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	x.rt.Handle("matter-notes", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<p>notes</p>"))
	}))
	if _, problem := x.tabForm(x.admin, "install", nil, x.build(onlyManifest(extNotesManifest))); problem != "" {
		t.Fatalf("install matter-notes: %s", problem)
	}
	x.approveOnTab("matter-notes")

	panel := "/modules/matter-notes/matter-items/notes?matter_id=" + open
	action := "/modules/matter-notes/matter-items/letter?matter_id=" + open
	screen := x.staff.must(http.StatusOK, "GET", "/practice/matters/"+open, nil)
	for _, want := range []string{`id="module-matter-notes"`, `<iframe src="` + panel + `"`, `sandbox="allow-scripts allow-forms"`,
		`<a class="button" href="` + action + `">Draft a letter</a>`, "Matter notes"} {
		if !strings.Contains(screen.body, want) {
			t.Fatalf("matter screen lacks %q:\n%s", want, screen.body)
		}
	}
	for path, page := range map[string]string{panel: "/m/matter-notes/notes?", action: "/m/matter-notes/letter?"} {
		r := x.staff.must(http.StatusOK, "GET", path, nil)
		for _, want := range []string{page, "context=", "matter_id=" + open} {
			if !strings.Contains(r.body, want) {
				t.Fatalf("%s frame lacks %q: %s", path, want, r.body)
			}
		}
		// The action opens as its own screen inside the shell; the panel draws inside the
		// matter screen's iframe, so it carries no shell of its own.
		if shelled := strings.Contains(r.body, `<body class="shell`); shelled != (path == action) {
			t.Fatalf("%s: inside the shell = %v", path, shelled)
		}
	}
	if r := x.staff.do("GET", "/modules/matter-notes/matter-items/notes", nil); r.code == http.StatusOK {
		t.Fatal("a matter item framed without its matter")
	}
	if r := x.staff.do("GET", "/modules/matter-notes/matter-items/nope?matter_id="+open, nil); r.code != http.StatusNotFound {
		t.Fatalf("undeclared matter item: %d", r.code)
	}
	if r := newClient(t, x.srv.URL).do("GET", panel, nil); r.code == http.StatusOK {
		t.Fatal("a signed-out caller got a matter item frame")
	}
}

const extNotesManifest = `id: matter-notes
name: Matter Notes
version: "1.0.0"
author: Synthetic author
container:
  image: ghcr.io/example/matter-notes
  digest: sha256:3333333333333333333333333333333333333333333333333333333333333333
  port: 8080
permissions:
  - scope: matters:read
matter_items:
  - id: notes
    kind: panel
    title: Matter notes
    path: /notes
  - id: letter
    kind: action
    title: Draft a letter
    path: /letter
`

const extCalendarManifest = `id: calendar-plus
name: Calendar Plus
version: "1.0.0"
author: Synthetic author
container:
  image: ghcr.io/example/calendar-plus
  digest: sha256:2222222222222222222222222222222222222222222222222222222222222222
  port: 8080
permissions:
  - scope: matters:read
replaces: calendar
`

func onlyManifest(manifest string) func(map[string][]byte) {
	return func(f map[string][]byte) {
		for k := range f {
			delete(f, k)
		}
		f[modules.ManifestFile] = []byte(manifest)
	}
}

// extCalendarModule implements the core calendar contract's paths and answers every
// calendar request itself.
func extCalendarModule(t *testing.T) (http.Handler, *[]http.Header) {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	paths := map[string]map[string]json.RawMessage{}
	for p, ops := range spec.Paths {
		if modules.DomainOf(p) == modules.DomainCalendar {
			paths[p] = ops
		}
	}
	doc, _ := json.Marshal(map[string]any{"openapi": "3.1.0", "paths": paths})
	var mu sync.Mutex
	seen := &[]http.Header{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/openapi.json" {
			_, _ = w.Write(doc)
			return
		}
		mu.Lock()
		*seen = append(*seen, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "module-event", "title": "From Calendar Plus"}}, "served_by": "calendar-plus", "path": r.URL.Path})
	}), seen
}

// publishSample runs the real marketplace service in process and publishes the sample
// module there, signed by a verified author key. It returns the service's URL, the
// operator call helper and the author's private key.
func publishSample(t *testing.T) (string, func(method, path, token string, body any) (int, []byte), ed25519.PrivateKey) {
	t.Helper()
	cfg := service.Defaults()
	cfg.SecureCookies = false
	m, err := service.Open(t.TempDir(), cfg.AuthorSession(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := service.NewServer(m, cfg, extOperatorToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	market := httptest.NewServer(srv.Handler())
	t.Cleanup(market.Close)
	call := func(method, path, token string, body any) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, market.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	call("POST", "/v1/authors", "", map[string]any{"name": "Synthetic Author", "email": "author@example.test", "password": "synthetic author password"})
	_, b := call("POST", "/v1/sessions", "", map[string]any{"email": "author@example.test", "password": "synthetic author password"})
	var tok struct{ Token string }
	_ = json.Unmarshal(b, &tok)
	pub, priv, _ := ed25519.GenerateKey(nil)
	_, b = call("POST", "/v1/authors/me/keys", tok.Token, map[string]any{"public_key_pem": marketplace.PublicKeyPEM(pub)})
	var k service.KeyView
	_ = json.Unmarshal(b, &k)
	if code, b := call("POST", "/v1/authors/me/keys/"+k.KeyID+"/verify", tok.Token, map[string]any{"signature": marketplace.Sign(priv, marketplace.KeyProofMessage(k.KeyID, k.Challenge))}); code >= 300 {
		t.Fatalf("verify key: %d %s", code, b)
	}
	pkg, err := matterclock.Archive(nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, b := call("POST", "/v1/authors/me/packages", tok.Token, map[string]any{"package_base64": base64.StdEncoding.EncodeToString(pkg), "signature": marketplace.Sign(priv, pkg), "key_id": k.KeyID}); code != http.StatusCreated {
		t.Fatalf("publish: %d %s", code, b)
	}
	return market.URL, call, priv
}

const extOperatorToken = "synthetic-operator-token-0123456789abcdef"

// TestExtensionsMarketplaceAlerts is the marketplace-alerts phase case (AC-015) against
// the real marketplace service: browsing from the admin tab, a flag's warning on the
// listing, the module alert on the admin dashboard and by email to every admin, a pulled
// module refused from the marketplace and still installable from a local file.
func TestExtensionsMarketplaceAlerts(t *testing.T) {
	clock := newTestClock()
	x := newExt(t, func(c *Config) { c.Now = clock.now })
	addr, call, authorKey := publishSample(t)
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.marketplace_address", map[string]any{"value": addr})

	// Browse on the admin tab: the listing with the operator notice and its permissions.
	tab := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules&q=matter", nil)
	if !strings.Contains(tab.body, `id="listing-matter-clock"`) || !strings.Contains(tab.body, "does not review, test or warrant") {
		t.Fatalf("marketplace listing on the tab: %s", tab.body)
	}
	if r := x.staff.do("GET", "/api/v1/marketplace/listings", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff browsed the marketplace: %d", r.code)
	}
	// Install from the marketplace on the tab; the marketplace's verified key signed it.
	if _, problem := x.tabForm(x.admin, "install-listing", url.Values{"listing_id": {"matter-clock"}, "version": {"1.0.0"}}, nil); problem != "" {
		t.Fatalf("marketplace install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	if x.count(`SELECT COUNT(*) FROM core.modules WHERE id = 'matter-clock' AND source = 'marketplace' AND enabled = 1`) != 1 {
		t.Fatal("marketplace install did not run")
	}

	// The operator flags it: the listing shows the warning...
	if code, b := call("POST", "/v1/operator/listings/matter-clock/flag", extOperatorToken, map[string]any{"note": "Synthetic flag: reports hours twice."}); code != http.StatusOK {
		t.Fatalf("flag: %d %s", code, b)
	}
	listing := x.admin.must(http.StatusOK, "GET", "/api/v1/marketplace/listings/matter-clock", nil)
	if listing.json["flagged"] != true || !strings.Contains(listing.body, "reports hours twice") {
		t.Fatalf("flagged listing: %s", listing.body)
	}
	if r := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules&q=matter", nil); !strings.Contains(r.body, html.EscapeString(marketplace.FlagWarning)) {
		t.Fatal("the tab's listing lacks the flag warning")
	}
	// ...and the installed firm is alerted: opening the modules screen checks now (a
	// check that ran under 30 seconds ago is not repeated on open).
	clock.advance(31 * time.Second)
	beforeMail := x.count(`SELECT COUNT(*) FROM core.mail_outbox`)
	x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	alerts := x.admin.must(http.StatusOK, "GET", "/api/v1/module-alerts", nil)
	if !strings.Contains(alerts.body, "reports hours twice") || !strings.Contains(alerts.body, marketplace.FlagWarning) {
		t.Fatalf("module alerts: %s", alerts.body)
	}
	if r := x.admin.must(http.StatusOK, "GET", "/api/v1/widgets/module-alerts", nil); !strings.Contains(r.body, "matter-clock") {
		t.Fatalf("admin dashboard module-alerts widget: %s", r.body)
	}
	if r := x.staff.do("GET", "/api/v1/widgets/module-alerts", nil); r.code < 400 {
		t.Fatalf("staff read module alerts: %d", r.code)
	}
	if x.count(`SELECT COUNT(*) FROM core.mail_outbox`) <= beforeMail || x.count(`SELECT COUNT(*) FROM core.mail_outbox WHERE to_address = $1`, adminEmail) == 0 {
		t.Fatal("no alert email queued for the admin")
	}
	if _, err := x.app.DeliverMail(context.Background()); err != nil {
		t.Fatal(err)
	}
	mailed := false
	for _, m := range x.smtp.to(adminEmail) {
		mailed = mailed || (strings.Contains(m.Data, "reports hours twice") && strings.Contains(m.Data, "alerted that this item has problems"))
	}
	if !mailed {
		t.Fatalf("the admin got no alert email with the warning and note: %+v", x.smtp.to(adminEmail))
	}
	clock.advance(31 * time.Second)
	x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if n := x.count(`SELECT COUNT(*) FROM core.module_alerts`); n != 1 {
		t.Fatalf("the same flag was delivered %d times", n)
	}
	// Nothing was switched off remotely.
	if x.count(`SELECT COUNT(*) FROM core.modules WHERE id = 'matter-clock' AND enabled = 1`) != 1 {
		t.Fatal("a flag disabled the module")
	}

	// Pulled: no marketplace install; a local file signed by a trusted key still installs.
	if _, problem := x.tabForm(x.admin, "uninstall", url.Values{"module_id": {"matter-clock"}, "data": {"delete"}, "confirmation": {"matter-clock"}}, nil); problem != "" {
		t.Fatalf("uninstall: %s", problem)
	}
	if code, b := call("POST", "/v1/operator/listings/matter-clock/pull", extOperatorToken, map[string]any{"note": "Synthetic pull."}); code != http.StatusOK {
		t.Fatalf("pull: %d %s", code, b)
	}
	if _, problem := x.tabForm(x.admin, "install-listing", url.Values{"listing_id": {"matter-clock"}, "version": {"1.0.0"}}, nil); problem == "" {
		t.Fatal("a pulled module installed from the marketplace")
	}
	local, err := matterclock.Build(authorKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, problem := x.tabForm(x.admin, "install", nil, local); problem != "" {
		t.Fatalf("local install of a pulled module: %s", problem)
	}
	if x.count(`SELECT COUNT(*) FROM core.modules WHERE id = 'matter-clock' AND source = 'local' AND status = 'awaiting_approval'`) != 1 {
		t.Fatal("the local file did not install")
	}
}
