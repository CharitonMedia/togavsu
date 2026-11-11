package modules_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

// Regression tests for Codex checkpoint 4 of PR #38 (reviewed head 41a9738): permission
// decisions made for an update whose migration then fails must stay in step with the
// token the running version holds (PRD 9.3, 9.9; AC-033, AC-037).

const failingMigration = "CREATE TABLE half_done (id TEXT PRIMARY KEY);\nCREATE INDEX missing_idx ON no_such_table (id);"

// addScope appends a required permission at the end of the list.
func addScope(f map[string][]byte, scope string) {
	f[modules.ManifestFile] = []byte(strings.Replace(string(f[modules.ManifestFile]), "\nwidgets:\n", "\n  - scope: "+scope+"\n    reason: Read the notes on each matter.\nwidgets:\n", 1))
}

func makeOptional(f map[string][]byte, scope, reason string) {
	line := "  - scope: " + scope + "\n    reason: " + reason + "\n"
	f[modules.ManifestFile] = []byte(strings.Replace(string(f[modules.ManifestFile]), line, line+"    required: false\n", 1))
}

const eventsReason = "Hear about new time entries and matters."

func (e *env) decision(scope string) string {
	var d string
	if err := e.app.Pool.QueryRowContext(e.ctx, `SELECT decision FROM core.module_permissions WHERE module_id = 'matter-clock' AND scope = $1`, scope).Scan(&d); err != nil {
		return ""
	}
	return d
}

// An approved scope the running version doesn't require, declined together with an
// update whose migration fails: the decline stands, the running module gets a token
// without it, the old token is revoked, and the scope the failed update asked for is on
// no token and no decision, so a later Enable can't grant it.
func TestPGFailedUpdateAppliesDeclineToRunningToken(t *testing.T) {
	e := newEnv(t)
	r := e.do("POST", "/api/v1/modules", e.build(func(f map[string][]byte) { makeOptional(f, "events:read", eventsReason) }), e.as("admin")...)
	if r.code != http.StatusAccepted {
		t.Fatalf("install: %d %s", r.code, r.body)
	}
	e.approveAll("matter-clock")
	running := e.rt.last("matter-clock").Secrets["togavsu_api_token"]
	if !strings.Contains(strings.Join(e.tokenScopes(t), ","), "events:read") {
		t.Fatal("events:read was approved but isn't on the token")
	}

	failing := e.build(func(f map[string][]byte) {
		withVersion(f, "1.1.0")
		makeOptional(f, "events:read", eventsReason)
		addScope(f, "notes:read")
		f["migrations/0002_failing.sql"] = []byte(failingMigration)
	})
	if r := e.do("POST", "/api/v1/modules/matter-clock/update", failing, e.as("admin")...); r.json["outcome"] != "awaiting_approval" {
		t.Fatalf("update with a new scope: %d %s", r.code, r.body)
	}
	r = e.do("POST", "/api/v1/modules/matter-clock/permissions", map[string]any{"decisions": []map[string]string{
		{"scope": "notes:read", "decision": "approve"},
		{"scope": "events:read", "decision": "decline"},
	}}, e.as("admin")...)
	if r.code != http.StatusOK || r.json["version"] != "1.0.0" {
		t.Fatalf("decisions for a failing update: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'mod_matter_clock' AND table_name = 'half_done'`) != 0 {
		t.Fatal("the failed migration didn't roll back")
	}

	now := e.rt.last("matter-clock").Secrets["togavsu_api_token"]
	if now == running {
		t.Fatal("the running module kept the token that carries the declined scope")
	}
	scopes := strings.Join(e.tokenScopes(t), ",")
	if strings.Contains(scopes, "events:read") || strings.Contains(scopes, "notes:read") || !strings.Contains(scopes, "matters:read") || !strings.Contains(scopes, "time:read") {
		t.Fatalf("running token scopes after the failed update: %s", scopes)
	}
	if e.tokenScopesOf(t, running) != nil {
		t.Fatal("the old token, which carries the declined scope, still works")
	}
	if e.rt.last("matter-clock").Version != "1.0.0" {
		t.Fatal("the running version changed")
	}
	if d := e.decision("events:read"); d != "declined" {
		t.Fatalf("events:read decision = %q, want declined", d)
	}
	if d := e.decision("notes:read"); d != "" {
		t.Fatalf("the failed update's scope kept a decision: %q", d)
	}
	in, err := e.svc.Installed(e.ctx, "matter-clock")
	if err != nil || in.Approved["events:read"] || in.Approved["notes:read"] || !in.Approved["time:read"] {
		t.Fatalf("installed approvals: %+v %v", in.Approved, err)
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module:matter-clock' AND field = 'permission:notes:read' AND new_value = 'removed'`) != 1 {
		t.Fatal("removing the failed update's scope isn't audited")
	}

	// Disable and enable: the fresh token carries only what the running version has approved.
	e.do("POST", "/api/v1/modules/matter-clock/disable", nil, e.as("admin")...)
	if r := e.do("POST", "/api/v1/modules/matter-clock/enable", nil, e.as("admin")...); r.json["enabled"] != true {
		t.Fatalf("enable: %d %s", r.code, r.body)
	}
	scopes = strings.Join(e.tokenScopes(t), ",")
	if strings.Contains(scopes, "notes:read") || strings.Contains(scopes, "events:read") {
		t.Fatalf("token after enable: %s", scopes)
	}
}

// A scope the running version requires, declined for an update (where it's optional)
// whose migration then fails: the running version keeps its approval, its required flag
// and its token, and the update's newly approved scope is on no token and no decision.
func TestPGFailedUpdateRestoresRunningDecisions(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	running := e.rt.last("matter-clock").Secrets["togavsu_api_token"]

	changed := e.build(func(f map[string][]byte) {
		withVersion(f, "1.1.0")
		makeOptional(f, "time:read", "Add up the hours worked on each matter this month.")
		addScope(f, "notes:read")
		f["migrations/0002_failing.sql"] = []byte(failingMigration)
	})
	if r := e.do("POST", "/api/v1/modules/matter-clock/update", changed, e.as("admin")...); r.json["outcome"] != "awaiting_approval" {
		t.Fatalf("update with a new scope: %d %s", r.code, r.body)
	}
	r := e.do("POST", "/api/v1/modules/matter-clock/permissions", map[string]any{"decisions": []map[string]string{
		{"scope": "notes:read", "decision": "approve"},
		{"scope": "time:read", "decision": "decline"},
	}}, e.as("admin")...)
	if r.code != http.StatusOK || r.json["version"] != "1.0.0" {
		t.Fatalf("decisions for a failing update: %d %s", r.code, r.body)
	}
	if e.rt.last("matter-clock").Secrets["togavsu_api_token"] != running {
		t.Fatal("the running token changed though its decisions were restored")
	}
	scopes := strings.Join(e.tokenScopes(t), ",")
	if strings.Contains(scopes, "notes:read") || !strings.Contains(scopes, "time:read") {
		t.Fatalf("running token scopes: %s", scopes)
	}
	if d := e.decision("time:read"); d != "approved" {
		t.Fatalf("time:read decision = %q, want approved (version 1.0.0 requires it)", d)
	}
	if e.count(`SELECT COUNT(*) FROM core.module_permissions WHERE module_id = 'matter-clock' AND scope = 'time:read' AND required = 1`) != 1 {
		t.Fatal("time:read lost the running version's required flag")
	}
	if d := e.decision("notes:read"); d != "" {
		t.Fatalf("the failed update's scope kept a decision: %q", d)
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module:matter-clock' AND field = 'permission:time:read' AND old_value = 'declined' AND new_value = 'approved' AND reason LIKE '%1.1.0 failed%'`) != 1 {
		t.Fatal("restoring the running version's decision isn't audited")
	}
	if r := e.do("GET", "/api/v1/modules/matter-clock", nil, e.as("admin")...); r.json["version"] != "1.0.0" || strings.Contains(r.body, "notes:read") {
		t.Fatalf("after the failed update: %s", r.body)
	}
	e.do("POST", "/api/v1/modules/matter-clock/disable", nil, e.as("admin")...)
	e.do("POST", "/api/v1/modules/matter-clock/enable", nil, e.as("admin")...)
	if strings.Contains(strings.Join(e.tokenScopes(t), ","), "notes:read") {
		t.Fatal("enable granted the failed update's scope")
	}
}
