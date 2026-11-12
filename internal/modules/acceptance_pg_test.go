package modules_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/jackc/pgx/v5/pgconn"
)

func hasTopic(t *testing.T, e *env, module string) bool {
	t.Helper()
	chs, err := e.help.ModuleTopics(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range chs {
		for _, tp := range ch.Topics {
			if tp.Module == module {
				return true
			}
		}
	}
	return false
}

func catalogHas(e *env, p domain.Principal, id string) bool {
	for _, d := range e.cat.For(p) {
		if d.ID == id {
			return true
		}
	}
	return false
}

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// AC-012, AC-033, AC-036: the sample module installs from a local file, asks for its
// permissions, shows its widget, tab and guide, reads through include= inside walls,
// receives events and uninstalls, all on the running core.
func TestPGSampleModuleLifecycle(t *testing.T) {
	e := newEnv(t)
	e.logTime(e.staff, e.open.ID, 10)
	e.logTime(e.attorney, e.walled.ID, 5)

	// Install from a local file: awaiting approval, nothing runs yet.
	r := e.do("POST", "/api/v1/modules", e.build(nil), e.as("admin")...)
	if r.code != http.StatusAccepted || r.json["status"] != modules.StatusAwaiting || r.json["enabled"] != false {
		t.Fatalf("install: %d %s", r.code, r.body)
	}
	perms, _ := r.json["permissions"].([]any)
	if len(perms) != 3 {
		t.Fatalf("permissions shown: %s", r.body)
	}
	for _, p := range perms {
		if m := p.(map[string]any); m["approved"] != false || m["required"] != true {
			t.Fatalf("permission before decision: %v", m)
		}
	}
	if e.schemaExists("matter-clock") || catalogHas(e, e.staff, "matter-clock.hours") || len(e.rt.deployments) != 0 {
		t.Fatal("something ran before approval")
	}
	if r := e.do("POST", "/api/v1/modules", e.build(nil), e.as("staff")...); r.code != http.StatusForbidden {
		t.Fatalf("staff install: %d", r.code)
	}
	// Partial decisions are refused.
	if r := e.do("POST", "/api/v1/modules/matter-clock/permissions", map[string]any{"decisions": []map[string]string{{"scope": "matters:read", "decision": "approve"}}}, e.as("admin")...); r.code != http.StatusBadRequest {
		t.Fatalf("partial decision: %d %s", r.code, r.body)
	}
	r = e.approveAll("matter-clock")
	if r.json["status"] != modules.StatusInstalled || r.json["enabled"] != true {
		t.Fatalf("after approval: %s", r.body)
	}

	// Schema, login and migration record.
	if !e.schemaExists("matter-clock") || e.count(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'mod_matter_clock' AND table_name = 'matter_targets'`) != 1 {
		t.Fatal("matter_targets was not created in mod_matter_clock")
	}
	if e.count(`SELECT COUNT(*) FROM core.schema_migrations WHERE owner = 'mod_matter_clock' AND version = 1`) != 1 {
		t.Fatal("migration not recorded")
	}
	fk := e.count(`SELECT COUNT(*) FROM pg_constraint c JOIN pg_class t ON t.oid = c.confrelid JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE c.contype = 'f' AND c.confdeltype = 'r' AND n.nspname = 'core' AND t.relname = 'matters' AND c.conrelid = 'mod_matter_clock.matter_targets'::regclass`)
	if fk != 1 {
		t.Fatal("matter_targets is not linked to core.matters ON DELETE RESTRICT")
	}
	dep := e.rt.last("matter-clock")
	if dep.Digest == "" || dep.Secrets["togavsu_api_token"] == "" || dep.Secrets["togavsu_db_role"] == "" || dep.Secrets["togavsu_webhook_secret"] == "" {
		t.Fatalf("deployment: %+v", dep)
	}
	for k, v := range dep.Secrets {
		if strings.Contains(strings.ToLower(k), "ai") || strings.HasPrefix(v, "sk-") {
			t.Fatalf("an AI key reached the module: %s", k)
		}
	}

	// The module's token is scoped to its approved permissions.
	caller, err := e.app.Identity.Authenticate(e.ctx, identity.Presented{Bearer: dep.Secrets["togavsu_api_token"]})
	if err != nil || caller.Principal.ModuleID != "matter-clock" || caller.Principal.Source != domain.SourceModule || len(caller.Scopes) != 3 {
		t.Fatalf("module token: %+v %v", caller, err)
	}

	// Widget in the picker with its manifest target; the guide under its own heading.
	if !catalogHas(e, e.staff, "matter-clock.hours") {
		t.Fatal("widget not in the picker after install")
	}
	def, _ := e.cat.Def("matter-clock.hours")
	if def.Page != "/modules/matter-clock/tabs/hours" {
		t.Fatalf("widget target: %+v", def)
	}
	if !hasTopic(t, e, "matter-clock") {
		t.Fatal("guide not in the help center after install")
	}
	hits, err := e.help.SearchTopics(e.ctx, e.staff, "target", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hits {
		found = found || strings.HasPrefix(h.ID, "matter-clock.")
	}
	if !found {
		t.Fatalf("guide not in help search: %+v", hits)
	}
	// The widget's ? icon opens the guide page that covers it.
	tp, err := e.help.Topic(e.ctx, e.staff, "matter-clock.overview")
	if err != nil || !containsS(tp.Paths, "matter-clock.hours") {
		t.Fatalf("widget help topic: %+v %v", tp, err)
	}

	// Widget page frames the module page in a sandbox with a context token, no cookie.
	r = e.do("GET", "/modules/matter-clock/widgets/hours", nil, e.as("staff")...)
	if r.code != http.StatusOK || !strings.Contains(r.body, `sandbox="allow-scripts allow-forms"`) || strings.Contains(r.body, "allow-same-origin") ||
		!strings.Contains(r.body, "/m/matter-clock/widget?context=") {
		t.Fatalf("widget frame page: %d %s", r.code, r.body)
	}
	staffCtx := e.contextFor("staff", "matter-clock", "")
	r = e.do("GET", "/m/matter-clock/widget?context="+url.QueryEscape(staffCtx), nil, "Cookie", "togavsu_session=secret-session", "Authorization", "Bearer core-token")
	if r.code != http.StatusOK || !strings.Contains(r.body, e.open.Number) || !strings.Contains(r.body, "1.0") {
		t.Fatalf("staff widget: %d %s", r.code, r.body)
	}
	if strings.Contains(r.body, e.walled.Number) {
		t.Fatal("staff widget shows the walled matter")
	}
	for _, h := range e.rt.headers["matter-clock"] {
		if h.Get("Cookie") != "" || strings.Contains(h.Get("Authorization"), "core-token") {
			t.Fatalf("the module saw core credentials: %v", h)
		}
	}
	r = e.do("GET", "/m/matter-clock/widget?context="+url.QueryEscape(e.contextFor("attorney", "matter-clock", "")), nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, e.walled.Number) || !strings.Contains(r.body, "0.5") {
		t.Fatalf("attorney widget: %d %s", r.code, r.body)
	}
	// The widget's click-through opens the tab its manifest names.
	if r := e.do("GET", "/modules/matter-clock/tabs/hours", nil, e.as("staff")...); r.code != http.StatusOK || !strings.Contains(r.body, "/m/matter-clock/tab?context=") {
		t.Fatalf("hours tab: %d %s", r.code, r.body)
	}

	// The module writes targets in its own schema through its own login.
	mdb := e.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	if err := matterclock.SetTarget(e.ctx, mdb, string(e.open.ID), 120, "attorney-only note", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := matterclock.SetTarget(e.ctx, mdb, string(e.walled.ID), 80, "walled note", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Its login can't read core contacts, core matters or another schema.
	for _, q := range []string{`SELECT COUNT(*) FROM core.contacts`, `SELECT COUNT(*) FROM core.matters`, `SELECT COUNT(*) FROM core.api_tokens`} {
		var n int
		if err := mdb.QueryRowContext(e.ctx, q).Scan(&n); pgCode(err) != "42501" {
			t.Fatalf("%s as the module login: %v", q, err)
		}
	}
	if _, err := mdb.ExecContext(e.ctx, `CREATE TABLE core.sneaky (id TEXT)`); pgCode(err) != "42501" {
		t.Fatalf("module login created a core table: %v", err)
	}

	// The matter screen reads the target through include=, inside the viewer's access.
	r = e.do("GET", "/api/v1/matters/"+string(e.open.ID)+"?include=matter-clock.targets", nil, e.as("staff")...)
	inc := included(t, r, "matter-clock.targets")
	if len(inc) != 1 || inc[0]["monthly_target_tenths"] != float64(120) || inc[0]["note"] != "[redacted]" {
		t.Fatalf("staff include: %d %s", r.code, r.body)
	}
	r = e.do("GET", "/api/v1/matters/"+string(e.open.ID)+"?include=matter-clock.targets", nil, e.as("attorney")...)
	if inc := included(t, r, "matter-clock.targets"); len(inc) != 1 || inc[0]["note"] != "attorney-only note" {
		t.Fatalf("attorney include: %s", r.body)
	}
	r = e.do("GET", "/api/v1/matters/"+string(e.walled.ID)+"?include=matter-clock.targets", nil, e.as("staff")...)
	if r.code != http.StatusNotFound || strings.Contains(r.body, "walled note") || strings.Contains(r.body, "80") {
		t.Fatalf("staff read a walled matter's module rows: %d %s", r.code, r.body)
	}
	got, err := e.svc.Include(e.ctx, e.staff, nil, "matter", []domain.ID{e.open.ID, e.walled.ID}, []string{"matter-clock.targets"})
	if err != nil || len(got) != 1 || got[e.walled.ID] != nil {
		t.Fatalf("service include for staff: %v %v", got, err)
	}
	r = e.do("GET", "/api/v1/matters?include=matter-clock.targets", nil, e.as("staff")...)
	if r.code != http.StatusOK || strings.Contains(r.body, "walled note") || !strings.Contains(r.body, `"monthly_target_tenths":120`) {
		t.Fatalf("staff list include: %d %s", r.code, r.body)
	}
	if r := e.do("GET", "/api/v1/matters/"+string(e.open.ID)+"?include=matter-clock.nothing", nil, e.as("staff")...); r.code != http.StatusBadRequest {
		t.Fatalf("unknown include: %d", r.code)
	}
	// The matter tab: staff can't open it for the walled matter; the attorney can.
	if r := e.do("GET", "/modules/matter-clock/tabs/matter?matter_id="+string(e.walled.ID), nil, e.as("staff")...); r.code != http.StatusNotFound {
		t.Fatalf("staff matter tab on walled matter: %d", r.code)
	}
	r = e.do("GET", "/m/matter-clock/matter?context="+url.QueryEscape(e.contextFor("attorney", "matter-clock", e.walled.ID)), nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, "8.0") || !strings.Contains(r.body, "walled note") {
		t.Fatalf("attorney matter tab: %d %s", r.code, r.body)
	}

	// Events: a new time entry reaches the module by signed webhook.
	// Billing keeps its events in its own store; the core publishes them after commit.
	e.logTime(e.staff, e.open.ID, 3)
	evs, err := e.app.Billing.Events(e.ctx, e.admin, e.open.ID)
	if err != nil || len(evs) == 0 {
		t.Fatalf("billing events: %v %v", evs, err)
	}
	queued, err := e.svc.Publish(e.ctx, evs...)
	if err != nil || queued == 0 {
		t.Fatalf("publish: %d %v", queued, err)
	}
	if again, _ := e.svc.Publish(e.ctx, evs...); again != 0 {
		t.Fatalf("a republished event was queued twice: %d", again)
	}
	// Every domain writes core.events (practice and billing since integrate-extensions);
	// the pump picks those rows up. Drain what the setup wrote: the matters opened before
	// install are delivered as matter.created; the billing events published above are
	// already queued and the unique index keeps them once.
	drained, err := e.svc.Pump(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixMicro()
	evID, _ := domain.NewID()
	if _, err := e.app.Pool.ExecContext(e.ctx, `INSERT INTO core.events (id, created_at, updated_at, created_by, actor_kind, actor_id, action, record_type, record_id, matter_id, summary, client_visible)
VALUES ($1,$2,$2,$3,'staff',$3,'matter.created','matter',$4,$4,'synthetic',0)`, string(evID), at, string(e.staff.UserID), string(e.open.ID)); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.Pump(e.ctx); err != nil || n != 1 {
		t.Fatalf("pump: %d %v", n, err)
	}
	if n, err := e.svc.Pump(e.ctx); err != nil || n != 0 {
		t.Fatalf("second pump re-queued: %d %v", n, err)
	}
	if n, err := e.svc.Deliver(e.ctx); err != nil || n != queued+drained+1 {
		t.Fatalf("deliver: %d of %d, %v", n, queued+drained+1, err)
	}
	var n int
	if err := mdb.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM received_events WHERE action LIKE 'time_entry.%'`).Scan(&n); err != nil || n < 1 {
		t.Fatalf("module received %d time entry events: %v", n, err)
	}
	if err := mdb.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM received_events WHERE event_id = $1 AND action = 'matter.created'`, string(evID)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("module didn't receive the matter event: %d %v", n, err)
	}
	ds, _ := e.svc.Deliveries(e.ctx, e.admin, "matter-clock")
	for _, d := range ds {
		if d.Status != "delivered" {
			t.Fatalf("delivery: %+v", d)
		}
	}

	// Uninstall, keeping data: gone from the picker, the guide and the API, no restart.
	if r := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "keep", "confirmation": "wrong"}, e.as("admin")...); r.code != http.StatusBadRequest {
		t.Fatalf("wrong confirmation: %d", r.code)
	}
	r = e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "keep", "confirmation": "matter-clock"}, e.as("admin")...)
	if r.code != http.StatusOK || r.json["status"] != modules.StatusUninstalling || r.json["enabled"] != false {
		t.Fatalf("uninstall keep: %d %s", r.code, r.body)
	}
	// The response is a contract Module (Codex checkpoint 2 on PR #38: removed is not a
	// Module.status), and the removed module is gone from Get and List.
	if err := shapeOf(t, "Module", r.json); err != nil {
		t.Fatalf("uninstall response outside the contract: %v (%s)", err, r.body)
	}
	if g := e.do("GET", "/api/v1/modules/matter-clock", nil, e.as("admin")...); g.code != http.StatusNotFound {
		t.Fatalf("get after uninstall: %d %s", g.code, g.body)
	}
	if catalogHas(e, e.staff, "matter-clock.hours") || hasTopic(t, e, "matter-clock") {
		t.Fatal("widget or guide still shown after uninstall")
	}
	if _, err := e.app.Identity.Authenticate(e.ctx, identity.Presented{Bearer: dep.Secrets["togavsu_api_token"]}); err == nil {
		t.Fatal("module token still works after uninstall")
	}
	if r := e.do("GET", "/modules/matter-clock/widgets/hours", nil, e.as("staff")...); r.code != http.StatusNotFound {
		t.Fatalf("frame after uninstall: %d", r.code)
	}
	if !e.schemaExists("matter-clock") {
		t.Fatal("keep data dropped the schema")
	}
	if err := mdb.PingContext(e.ctx); err == nil {
		var x int
		if err := mdb.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM matter_targets`).Scan(&x); err == nil {
			t.Fatal("the locked login still reads its schema")
		}
	}
	// Reinstall picks the kept data up.
	e.installSample()
	if e.count(`SELECT COUNT(*) FROM mod_matter_clock.matter_targets`) != 2 || !catalogHas(e, e.staff, "matter-clock.hours") || !hasTopic(t, e, "matter-clock") {
		t.Fatal("reinstall lost the kept data or the screens")
	}
}

func containsS(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func included(t *testing.T, r resp, name string) []map[string]any {
	t.Helper()
	if r.code != http.StatusOK {
		t.Fatalf("read: %d %s", r.code, r.body)
	}
	inc, _ := r.json["included"].(map[string]any)
	rows, _ := inc[name].([]any)
	var out []map[string]any
	for _, x := range rows {
		out = append(out, x.(map[string]any))
	}
	return out
}

// AC-012: declining a required permission cancels the install.
func TestPGDeclinedPermissionCancels(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/v1/modules", e.build(nil), e.as("admin")...)
	r := e.do("POST", "/api/v1/modules/matter-clock/permissions", map[string]any{"decisions": []map[string]string{
		{"scope": "matters:read", "decision": "approve"}, {"scope": "time:read", "decision": "decline"}, {"scope": "events:read", "decision": "approve"}}}, e.as("admin")...)
	if r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeDeclined {
		t.Fatalf("decline: %d %s", r.code, r.body)
	}
	if e.schemaExists("matter-clock") || len(e.rt.deployments) != 0 || catalogHas(e, e.staff, "matter-clock.hours") {
		t.Fatal("a cancelled install left something running")
	}
	if e.count(`SELECT COUNT(*) FROM core.api_tokens WHERE module_id = 'matter-clock'`) != 0 {
		t.Fatal("a cancelled install issued a token")
	}
	if r := e.do("GET", "/api/v1/modules", nil, e.as("admin")...); strings.Contains(r.body, "matter-clock") {
		t.Fatalf("cancelled module listed: %s", r.body)
	}
	// It can be installed again from the file.
	e.installSample()
}

// AC-013: an unsigned or tampered module is refused, over the API, with nothing kept.
// The signature check is the marketplace component's (Deps.Signing).
func TestPGUnsignedTamperedRefused(t *testing.T) {
	e := newEnv(t)
	unsigned, _ := matterclock.Archive(nil)
	if r := e.do("POST", "/api/v1/modules", unsigned, e.as("admin")...); r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeUnsigned {
		t.Fatalf("unsigned: %d %s", r.code, r.body)
	}
	_, other, _ := ed25519.GenerateKey(nil)
	foreign, _ := matterclock.Build(other, nil)
	if r := e.do("POST", "/api/v1/modules", foreign, e.as("admin")...); r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeUnsigned {
		t.Fatalf("untrusted signer: %d %s", r.code, r.body)
	}
	// Tampered: a scope added after signing, with the old digest, and then with a fresh
	// digest but the old signature.
	var sp marketplace.SignedPackage
	if err := json.Unmarshal(e.build(nil), &sp); err != nil {
		t.Fatal(err)
	}
	inner, _ := base64.StdEncoding.DecodeString(sp.PackageBase64)
	pkg, err := modules.ReadPackage(inner)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Files[modules.ManifestFile] = []byte(strings.Replace(string(pkg.Files[modules.ManifestFile]), "  - scope: events:read", "  - scope: trust:withdraw\n  - scope: events:read", 1))
	changed, _ := modules.Archive(pkg.Files)
	sp.PackageBase64 = base64.StdEncoding.EncodeToString(changed)
	for _, digest := range []string{sp.Digest, marketplace.Digest(changed)} {
		sp.Digest = digest
		tampered, _ := json.Marshal(sp)
		if r := e.do("POST", "/api/v1/modules", tampered, e.as("admin")...); r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeTampered {
			t.Fatalf("tampered (digest %s): %d %s", digest, r.code, r.body)
		}
	}
	// A key the admin removed no longer verifies.
	if err := e.market.DeleteKey(e.ctx, e.admin, e.keyID); err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/v1/modules", e.build(nil), e.as("admin")...); r.json["code"] != modules.CodeUnsigned {
		t.Fatalf("removed key: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.modules`) != 0 || e.count(`SELECT COUNT(*) FROM core.module_packages`) != 0 {
		t.Fatal("a refused package was recorded")
	}
}

// AC-038: a core record that module rows point to can't be hard-deleted, and the core
// names the module; a merge moves the module's links.
func TestPGHardDeleteBlockedAndRelink(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	dep := e.rt.last("matter-clock")
	mdb := e.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	if err := matterclock.SetTarget(e.ctx, mdb, string(e.open.ID), 50, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	// The matter's own history (core.events) also blocks a hard delete; take it away here
	// so the module's row is what stops it.
	if _, err := e.app.Pool.ExecContext(e.ctx, `DELETE FROM core.events WHERE matter_id = $1`, string(e.open.ID)); err != nil {
		t.Fatal(err)
	}
	_, err := e.app.Pool.ExecContext(e.ctx, `DELETE FROM core.matters WHERE id = $1`, string(e.open.ID))
	if pgCode(err) != "23503" {
		t.Fatalf("hard delete of a linked matter: %v", err)
	}
	named, ok := e.svc.Schemas().DeleteBlocked(e.ctx, err)
	if !ok || !strings.Contains(named.Error(), "matter-clock") {
		t.Fatalf("delete error doesn't name the module: %v", named)
	}
	holders, err := e.svc.Schemas().Holders(e.ctx, "matters", e.open.ID)
	if err != nil || len(holders) != 1 || holders[0] != "matter-clock" {
		t.Fatalf("holders: %v %v", holders, err)
	}
	if h, _ := e.svc.Schemas().Holders(e.ctx, "matters", e.walled.ID); len(h) != 0 {
		t.Fatalf("unlinked matter has holders: %v", h)
	}
	tx, err := e.app.Pool.BeginTx(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Schemas().Relink(e.ctx, tx, "matters", e.open.ID, e.walled.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT COUNT(*) FROM mod_matter_clock.matter_targets WHERE matter_id = $1`, string(e.walled.ID)) != 1 {
		t.Fatal("relink didn't move the row")
	}
	if err := matterclock.SetTarget(e.ctx, mdb, string(e.open.ID), 9, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	tx, _ = e.app.Pool.BeginTx(e.ctx, nil)
	defer func() { _ = tx.Rollback() }()
	if err := e.svc.Schemas().Relink(e.ctx, tx, "matters", e.open.ID, e.walled.ID); err == nil || !strings.Contains(err.Error(), "matter-clock") {
		t.Fatalf("relink onto an existing extension row: %v", err)
	}
}

// AC-038: Delete data drops the schema only after the typed confirmation and a backup.
func TestPGUninstallDeleteData(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	role := e.rt.last("matter-clock").Secrets["togavsu_db_role"]
	if r := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "delete", "confirmation": "matter clock"}, e.as("admin")...); r.code != http.StatusBadRequest {
		t.Fatalf("mistyped confirmation: %d", r.code)
	}
	e.backups.fail = errors.New("backup volume full")
	if r := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "delete", "confirmation": "matter-clock"}, e.as("admin")...); r.code != http.StatusConflict {
		t.Fatalf("failed backup: %d %s", r.code, r.body)
	}
	if !e.schemaExists("matter-clock") || !catalogHas(e, e.staff, "matter-clock.hours") {
		t.Fatal("a failed backup still removed the module or its data")
	}
	old := "00000000-0000-7000-8000-000000000000"
	if r := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "delete", "confirmation": "matter-clock", "backup_id": old}, e.as("admin")...); r.code != http.StatusConflict {
		t.Fatalf("unknown backup id: %d", r.code)
	}
	e.backups.fail = nil
	if r := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "delete", "confirmation": "matter-clock"}, e.as("staff")...); r.code != http.StatusForbidden {
		t.Fatalf("staff uninstall: %d", r.code)
	}
	idem := append(e.as("admin"), "Idempotency-Key", "uninstall-1")
	r := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "delete", "confirmation": "matter-clock"}, idem...)
	if r.code != http.StatusOK {
		t.Fatalf("delete data: %d %s", r.code, r.body)
	}
	if err := shapeOf(t, "Module", r.json); err != nil || r.json["status"] != modules.StatusUninstalling {
		t.Fatalf("uninstall response outside the contract: %v (%s)", err, r.body)
	}
	// Contract idempotencyKey (Codex checkpoint 2 on PR #38): the same key with a
	// different body is idempotency_conflict, not the first response; the same key and
	// body replay it with Idempotent-Replay and no second backup.
	if c := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "keep", "confirmation": "matter-clock"}, idem...); c.code != http.StatusConflict || c.json["code"] != "idempotency_conflict" {
		t.Fatalf("same key, different body: %d %s", c.code, c.body)
	}
	if rp := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "delete", "confirmation": "matter-clock"}, idem...); rp.code != http.StatusOK || rp.body != r.body {
		t.Fatalf("same key and body: %d %s", rp.code, rp.body)
	}
	if len(e.backups.made) != 1 {
		t.Fatalf("backups taken: %d", len(e.backups.made))
	}
	if e.schemaExists("matter-clock") || e.count(`SELECT COUNT(*) FROM pg_roles WHERE rolname = $1`, role) != 0 ||
		e.count(`SELECT COUNT(*) FROM core.schema_migrations WHERE owner = 'mod_matter_clock'`) != 0 {
		t.Fatal("schema, login or migration records survived Delete data")
	}
	// The matter can now be hard-deleted as far as modules are concerned.
	if h, _ := e.svc.Schemas().Holders(e.ctx, "matters", e.open.ID); len(h) != 0 {
		t.Fatalf("holders after delete: %v", h)
	}
	// The audit log names the backup.
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module:matter-clock' AND reason LIKE '%backup ' || $1 || '%'`, e.backups.made[0].ID) != 1 {
		t.Fatal("audit log doesn't record the backup before the delete")
	}
}

// AC-036: a second module reads the published view only after the admin approves the
// views scope, and only rows for matters its viewer can read.
func TestPGPublishedViewNeedsApproval(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	dep := e.rt.last("matter-clock")
	mdb := e.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	for id, v := range map[domain.ID]int{e.open.ID: 30, e.walled.ID: 40} {
		if err := matterclock.SetTarget(e.ctx, mdb, string(id), v, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	reader := func(version string) []byte {
		return e.build(func(f map[string][]byte) {
			for k := range f {
				if strings.HasPrefix(k, "migrations/") {
					delete(f, k)
				}
			}
			f[modules.ManifestFile] = []byte(`id: target-reader
name: Target Reader
version: "` + version + `"
author: Synthetic author
container:
  image: ghcr.io/example/target-reader
  digest: sha256:1111111111111111111111111111111111111111111111111111111111111111
  port: 8080
permissions:
  - scope: matters:read
  - scope: views:matter-clock.monthly_targets:read
    required: false
`)
		})
	}
	e.rt.handlers["target-reader"] = http.NotFoundHandler()
	e.do("POST", "/api/v1/modules", reader("1.0.0"), e.as("admin")...)
	r := e.do("POST", "/api/v1/modules/target-reader/permissions", map[string]any{"decisions": []map[string]string{
		{"scope": "matters:read", "decision": "approve"}, {"scope": "views:matter-clock.monthly_targets:read", "decision": "decline"}}}, e.as("admin")...)
	if r.code != http.StatusOK || r.json["status"] != modules.StatusInstalled {
		t.Fatalf("install with an optional scope declined: %d %s", r.code, r.body)
	}
	token := e.rt.last("target-reader").Secrets["togavsu_api_token"]
	path := "/api/v1/modules/matter-clock/views/monthly_targets?record_id=" + string(e.open.ID) + "&record_id=" + string(e.walled.ID)
	ctxTok := e.contextFor("staff", "target-reader", "")
	if r := e.do("GET", path, nil, "Authorization", "Bearer "+token, modules.ContextHeader, ctxTok); r.code != http.StatusForbidden {
		t.Fatalf("view read before approval: %d %s", r.code, r.body)
	}
	if r := e.do("GET", "/api/v1/matters/"+string(e.open.ID)+"?include=matter-clock.targets", nil, "Authorization", "Bearer "+token, modules.ContextHeader, ctxTok); r.code != http.StatusForbidden {
		t.Fatalf("another module's extension through include=: %d %s", r.code, r.body)
	}
	// An update asks for the scope again; the admin approves it.
	res, err := e.svc.Update(e.ctx, e.admin, "target-reader", reader("1.0.1"))
	if err != nil || res.Outcome != "awaiting_approval" || res.Module.Version != "1.0.0" {
		t.Fatalf("update asking for the view: %+v %v", res, err)
	}
	e.approveAll("target-reader")
	token = e.rt.last("target-reader").Secrets["togavsu_api_token"]
	ctxTok = e.contextFor("staff", "target-reader", "")
	r = e.do("GET", path, nil, "Authorization", "Bearer "+token, modules.ContextHeader, ctxTok)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"monthly_target_tenths":30`) || strings.Contains(r.body, `"monthly_target_tenths":40`) {
		t.Fatalf("staff view read after approval: %d %s", r.code, r.body)
	}
	r = e.do("GET", path, nil, "Authorization", "Bearer "+token, modules.ContextHeader, e.contextFor("attorney", "target-reader", ""))
	if !strings.Contains(r.body, `"monthly_target_tenths":40`) {
		t.Fatalf("attorney view read: %s", r.body)
	}
	// A module token without a viewer gets no rows on walled or open matters.
	r = e.do("GET", path, nil, "Authorization", "Bearer "+token)
	if r.code != http.StatusOK || strings.Contains(r.body, "monthly_target_tenths") {
		t.Fatalf("viewerless view read: %d %s", r.code, r.body)
	}
	// A context token for one module can't be used by another.
	if r := e.do("GET", path, nil, "Authorization", "Bearer "+token, modules.ContextHeader, e.contextFor("staff", "matter-clock", "")); r.code != http.StatusForbidden {
		t.Fatalf("cross-module context token: %d", r.code)
	}
}

// Context tokens are signed, short-lived and bound to an active user.
func TestPGContextTokens(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	if _, err := e.svc.IssueContext(e.ctx, e.staff, "matter-clock", e.walled.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("context on a walled matter: %v", err)
	}
	tok := e.contextFor("staff", "matter-clock", e.open.ID)
	c, err := e.svc.VerifyContext(e.ctx, tok)
	if err != nil || c.UserID != e.staff.UserID || c.MatterID != e.open.ID {
		t.Fatalf("verify: %+v %v", c, err)
	}
	if _, err := e.svc.VerifyContext(e.ctx, tok+"x"); err == nil {
		t.Fatal("forged token accepted")
	}
	payload, mac, _ := strings.Cut(tok, ".")
	if _, err := e.svc.VerifyContext(e.ctx, payload+"e30."+mac); err == nil {
		t.Fatal("altered payload accepted")
	}
	later, err := modules.New(modules.Deps{DB: e.app.Pool, Key: testKey, Audit: e.app.Audit, Settings: e.app.Settings, Walls: e.app.Practice.Policy.Walls, Contacts: e.app.Practice,
		Users: e.app.Users, Tokens: modules.IdentityTokens{Svc: e.app.Identity}, Now: func() time.Time { return time.Now().Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := later.VerifyContext(e.ctx, tok); err == nil {
		t.Fatal("expired token accepted")
	}
	if _, err := e.svc.IssueContext(e.ctx, domain.Principal{Source: domain.SourceModule, ModuleID: "matter-clock"}, "matter-clock", ""); err == nil {
		t.Fatal("a module minted its own context token")
	}
	r := e.do("POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{"matter_id": string(e.open.ID)}, e.as("staff")...)
	if r.code != http.StatusCreated || r.json["viewer_user_id"] != string(e.staff.UserID) {
		t.Fatalf("context token API: %d %s", r.code, r.body)
	}
}

func isCode(err error, c string) bool {
	var api *domain.APIError
	return errors.As(err, &api) && api.Code == c
}

var _ = dashboard.ModuleWidgetID
var _ = context.Background

// A module owns its schema and can change it through its login at any time. Whatever it
// puts there runs as the module's role when the core reads it, never as the core.
func TestPGModuleObjectsRunAsModuleRole(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	dep := e.rt.last("matter-clock")
	role := dep.Secrets["togavsu_db_role"]
	mdb := e.moduleDB(role, dep.Secrets["togavsu_db_password"])
	if err := matterclock.SetTarget(e.ctx, mdb, string(e.open.ID), 10, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, st := range []string{
		`ALTER TABLE matter_targets RENAME TO matter_targets_data`,
		`CREATE VIEW matter_targets AS SELECT matter_id, monthly_target_tenths, current_user::text AS note FROM matter_targets_data`,
	} {
		if _, err := mdb.ExecContext(e.ctx, st); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
	}
	got, err := e.svc.Include(e.ctx, e.attorney, nil, "matter", []domain.ID{e.open.ID}, []string{"matter-clock.targets"})
	if err != nil {
		t.Fatal(err)
	}
	rows := got[e.open.ID]["matter-clock.targets"]
	if len(rows) != 1 || rows[0]["note"] != role {
		t.Fatalf("include ran as %v, want the module role %s", rows, role)
	}
	// The frame proxy marks module pages sandboxed even outside the frame.
	r := e.do("GET", "/m/matter-clock/healthz", nil)
	if r.code != http.StatusOK {
		t.Fatalf("health through the frame proxy: %d", r.code)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/m/matter-clock/healthz", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("module page without a sandbox policy: %v", res.Header)
	}
}
