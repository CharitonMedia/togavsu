package modules_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// Regression tests for the Codex checkpoint of PR #38 (reviewed head 1417e16).

// AC-012: an event stamped before a later event but committed after the pump has
// passed the later one is still delivered (finding 2: the cursor skipped it).
func TestPGPumpDeliversLateCommittedEvent(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	// A pump whose clock runs an hour ahead: without the open-transaction guard its
	// lookback margin alone would put the horizon past the late event.
	ahead := e.service(t, func() time.Time { return time.Now().Add(time.Hour) })
	insert := `INSERT INTO core.events (id, created_at, updated_at, created_by, actor_kind, actor_id, action, record_type, record_id, matter_id, summary, client_visible)
VALUES ($1,$2,$2,$3,'staff',$3,'matter.created','matter',$4,$4,'synthetic',0)`

	tx, err := e.app.Pool.BeginTx(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	lateID, _ := domain.NewID()
	lateAt := time.Now().UnixMicro()
	if _, err := tx.ExecContext(e.ctx, insert, string(lateID), lateAt, string(e.staff.UserID), string(e.open.ID)); err != nil {
		t.Fatal(err)
	}
	firstCommittedID, _ := domain.NewID()
	if _, err := e.app.Pool.ExecContext(e.ctx, insert, string(firstCommittedID), lateAt+1000, string(e.staff.UserID), string(e.walled.ID)); err != nil {
		t.Fatal(err)
	}
	if n, err := ahead.Pump(e.ctx); err != nil || n != 1 {
		t.Fatalf("pump with an open writer: %d %v", n, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n, err := ahead.Pump(e.ctx); err != nil || n != 1 {
		t.Fatalf("pump after the late commit: %d %v", n, err)
	}
	if e.count(`SELECT COUNT(*) FROM core.module_deliveries WHERE module_id = 'matter-clock' AND event_id = $1`, string(lateID)) != 1 {
		t.Fatal("the late-committed event was never queued")
	}
	if n, err := ahead.Pump(e.ctx); err != nil || n != 0 {
		t.Fatalf("rescan queued a duplicate: %d %v", n, err)
	}
	if e.count(`SELECT COUNT(*) FROM core.module_deliveries WHERE module_id = 'matter-clock' AND event_id IN ($1, $2)`, string(lateID), string(firstCommittedID)) != 2 {
		t.Fatal("each event should be queued exactly once")
	}
}

// AC-033 / PRD 9.3: an update that stops requesting a scope removes its approval, and
// the reissued token no longer carries it (finding 3).
func TestPGUpdateDropsUnrequestedScopes(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	if !strings.Contains(strings.Join(e.tokenScopes(t), ","), "time:read") {
		t.Fatal("setup: the installed token lacks time:read")
	}
	narrower := e.build(func(f map[string][]byte) {
		withVersion(f, "1.1.0")
		f[modules.ManifestFile] = []byte(strings.Replace(string(f[modules.ManifestFile]),
			"  - scope: time:read\n    reason: Add up the hours worked on each matter this month.\n", "", 1))
	})
	r := e.do("POST", "/api/v1/modules/matter-clock/update", narrower, e.as("admin")...)
	if r.code != http.StatusAccepted || r.json["outcome"] != "updated" {
		t.Fatalf("narrower update: %d %s", r.code, r.body)
	}
	if strings.Contains(strings.Join(e.tokenScopes(t), ","), "time:read") {
		t.Fatalf("the reissued token keeps a scope the manifest dropped: %v", e.tokenScopes(t))
	}
	if !strings.Contains(strings.Join(e.tokenScopes(t), ","), "events:read") {
		t.Fatalf("the reissued token lost a scope the manifest kept: %v", e.tokenScopes(t))
	}
	if e.count(`SELECT COUNT(*) FROM core.module_permissions WHERE module_id = 'matter-clock' AND scope = 'time:read'`) != 0 {
		t.Fatal("the dropped scope's approval survived the update")
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module:matter-clock' AND field = 'permission:time:read' AND new_value = 'removed'`) != 1 {
		t.Fatal("the removal isn't audited")
	}
	in, err := e.svc.Installed(e.ctx, "matter-clock")
	if err != nil || in.Approved["time:read"] {
		t.Fatalf("installed approvals still list time:read: %v", err)
	}
}

// AC-038 / PRD 9.9: Delete data with a named backup accepts only an archive this
// installation wrote after the module's data last changed (finding 1).
func TestPGUninstallRejectsStaleBackup(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	del := func(backup string) resp {
		body := map[string]any{"data": "delete", "confirmation": "matter-clock"}
		if backup != "" {
			body["backup_id"] = backup
		}
		return e.do("POST", "/api/v1/modules/matter-clock/uninstall", body, e.as("admin")...)
	}
	// First life: Delete data with a backup Togavsu takes itself. The login is locked
	// while the backup runs, so nothing can write between backup and drop.
	role := e.rt.last("matter-clock").Secrets["togavsu_db_role"]
	canLogin := func() bool {
		return e.count(`SELECT COUNT(*) FROM pg_roles WHERE rolname = $1 AND rolcanlogin`, role) == 1
	}
	locked := false
	e.backups.during = func() { locked = !canLogin() }
	if r := del(""); r.code != http.StatusOK {
		t.Fatalf("first delete: %d %s", r.code, r.body)
	}
	e.backups.during = nil
	if !locked {
		t.Fatal("the module could still log in while its pre-delete backup ran")
	}
	b1 := e.backups.made[0].ID

	// Second life: reinstall, write new data, then name the first life's backup.
	e.installSample()
	if r := del(b1); r.code != http.StatusConflict {
		t.Fatalf("a backup from before the reinstall was accepted: %d %s", r.code, r.body)
	}
	if !e.schemaExists("matter-clock") {
		t.Fatal("the rejected delete dropped the schema")
	}
	// A backup taken while the module still runs can miss its later writes.
	running, _ := e.backups.Create(e.ctx, "while running")
	if r := del(running.ID); r.code != http.StatusConflict {
		t.Fatalf("a backup of a running module was accepted: %d %s", r.code, r.body)
	}

	// Disabled: the login is locked; only a later, local, complete archive counts.
	if r := e.do("POST", "/api/v1/modules/matter-clock/disable", nil, e.as("admin")...); r.code != http.StatusOK {
		t.Fatalf("disable: %d %s", r.code, r.body)
	}
	if canLogin() {
		t.Fatal("a disabled module can still log in")
	}
	if r := del(running.ID); r.code != http.StatusConflict {
		t.Fatalf("a backup from before the disable was accepted: %d %s", r.code, r.body)
	}
	time.Sleep(2 * time.Millisecond)
	for name, bk := range map[string]modules.Backup{
		"uploaded":   {Source: "upload"},
		"foreign":    {Source: "destination:offsite"},
		"deleting":   {Deleting: true},
		"incomplete": {SHA256: "-"},
	} {
		id, _ := domain.NewID()
		bk.ID, bk.CreatedAt, bk.Bytes = string(id), time.Now().UTC(), 1
		if bk.SHA256 == "-" {
			bk.SHA256 = ""
		} else {
			bk.SHA256 = "synthetic-sha"
		}
		e.backups.add(bk)
		if r := del(bk.ID); r.code != http.StatusConflict {
			t.Fatalf("%s backup accepted: %d %s", name, r.code, r.body)
		}
	}

	// An update's migration changes the locked schema; an earlier backup no longer holds it.
	before, _ := e.backups.Create(e.ctx, "after disable")
	time.Sleep(2 * time.Millisecond)
	migrated := e.build(func(f map[string][]byte) {
		withVersion(f, "1.1.0")
		f["migrations/0002_target_reviews.sql"] = []byte("ALTER TABLE matter_targets ADD COLUMN reviewed_on TEXT NOT NULL DEFAULT '';")
	})
	if r := e.do("POST", "/api/v1/modules/matter-clock/update", migrated, e.as("admin")...); r.code != http.StatusAccepted || r.json["outcome"] != "updated" {
		t.Fatalf("update while disabled: %d %s", r.code, r.body)
	}
	if r := del(before.ID); r.code != http.StatusConflict {
		t.Fatalf("a backup from before the last migration was accepted: %d %s", r.code, r.body)
	}
	if !e.schemaExists("matter-clock") {
		t.Fatal("a rejected delete dropped the schema")
	}
	time.Sleep(2 * time.Millisecond)
	good, _ := e.backups.Create(e.ctx, "after the last change")
	if r := del(good.ID); r.code != http.StatusOK {
		t.Fatalf("a current backup was refused: %d %s", r.code, r.body)
	}
	if e.schemaExists("matter-clock") {
		t.Fatal("Delete data kept the schema")
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module:matter-clock' AND reason LIKE '%backup ' || $1 || '%'`, good.ID) != 1 {
		t.Fatal("the audit log doesn't name the backup used")
	}
}
