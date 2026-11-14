package modules_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

// Regression tests for Codex checkpoint 3 of PR #38 (reviewed head 82cdc66).

// AC-033 / PRD 9.3: while an update waits for approval, an explicit decline of a scope
// that was approved earlier takes effect. Declining a scope the new version requires
// drops the update and says so; declining an optional one removes it from the
// reissued token and audits the change.
func TestPGUpdateDeclinesApprovedScope(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	const timeRead = "  - scope: time:read\n    reason: Add up the hours worked on each matter this month.\n"
	addNotes := func(f map[string][]byte, version string, optionalTime bool) {
		withVersion(f, version)
		m := strings.Replace(string(f[modules.ManifestFile]), "  - scope: events:read\n", "  - scope: events:read\n  - scope: notes:read\n", 1)
		if optionalTime {
			m = strings.Replace(m, timeRead, timeRead+"    required: false\n", 1)
		}
		f[modules.ManifestFile] = []byte(m)
	}
	decide := func(ds ...map[string]string) resp {
		return e.do("POST", "/api/v1/modules/matter-clock/permissions", map[string]any{"decisions": ds}, e.as("admin")...)
	}
	running := e.rt.Last("matter-clock").Secrets["togavsu_api_token"]

	// A required scope, already approved, declined during the update: the update is
	// dropped, the admin is told, and the running version keeps its approvals.
	strict := e.build(func(f map[string][]byte) { addNotes(f, "1.1.0", false) })
	if r := e.do("POST", "/api/v1/modules/matter-clock/update", strict, e.as("admin")...); r.json["outcome"] != "awaiting_approval" {
		t.Fatalf("update with a new scope: %d %s", r.code, r.body)
	}
	r := decide(map[string]string{"scope": "notes:read", "decision": "approve"}, map[string]string{"scope": "time:read", "decision": "decline"})
	if r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeDeclined || !strings.Contains(r.body, "time:read") || !strings.Contains(r.body, "update is dropped") {
		t.Fatalf("declining a required approved scope: %d %s", r.code, r.body)
	}
	if r := e.do("GET", "/api/v1/modules/matter-clock", nil, e.as("admin")...); r.json["version"] != "1.0.0" || strings.Contains(r.body, "notes:read") {
		t.Fatalf("after dropping the update: %s", r.body)
	}
	if e.rt.Last("matter-clock").Secrets["togavsu_api_token"] != running || !strings.Contains(strings.Join(e.tokenScopes(t), ","), "time:read") {
		t.Fatal("the running version lost its token or approvals when the update was dropped")
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module:matter-clock' AND field = 'status' AND reason LIKE '%time:read declined: update dropped%'`) != 1 {
		t.Fatal("the dropped update isn't audited")
	}

	// time:read optional in the new version: declining it is applied, not ignored.
	loose := e.build(func(f map[string][]byte) { addNotes(f, "1.2.0", true) })
	if r := e.do("POST", "/api/v1/modules/matter-clock/update", loose, e.as("admin")...); r.json["outcome"] != "awaiting_approval" {
		t.Fatalf("update with a new scope: %d %s", r.code, r.body)
	}
	r = decide(map[string]string{"scope": "notes:read", "decision": "approve"},
		map[string]string{"scope": "time:read", "decision": "decline"},
		map[string]string{"scope": "events:read", "decision": "approve"})
	if r.code != http.StatusOK || r.json["version"] != "1.2.0" {
		t.Fatalf("approve the new scope and decline an approved one: %d %s", r.code, r.body)
	}
	scopes := strings.Join(e.tokenScopes(t), ",")
	if strings.Contains(scopes, "time:read") || !strings.Contains(scopes, "notes:read") || !strings.Contains(scopes, "events:read") || !strings.Contains(scopes, "matters:read") {
		t.Fatalf("reissued token scopes: %s", scopes)
	}
	if strings.Contains(strings.Join(e.tokenScopesOf(t, running), ","), "time:read") {
		t.Fatal("the old token still carries the declined scope")
	}
	perms, _ := r.json["permissions"].([]any)
	for _, raw := range perms {
		pv, _ := raw.(map[string]any)
		if pv["scope"] == "time:read" && pv["approved"] != false {
			t.Fatalf("the module view still shows time:read approved: %s", r.body)
		}
	}
	in, err := e.svc.Installed(e.ctx, "matter-clock")
	if err != nil || in.Approved["time:read"] || !in.Approved["notes:read"] {
		t.Fatalf("installed approvals: %+v %v", in.Approved, err)
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module:matter-clock' AND field = 'permission:time:read' AND old_value = 'approved' AND new_value = 'declined'`) != 1 {
		t.Fatal("the decline of an approved scope isn't audited")
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module:matter-clock' AND field = 'permission:events:read' AND old_value <> 'pending'`) != 0 {
		t.Fatal("re-approving an approved scope wrote a change")
	}
}
