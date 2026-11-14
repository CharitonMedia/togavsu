package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/db"
)

// Codex PR #41 finding 4: a manual backup's request is audited before the supervisor is
// called, so an audit failure there makes no backup; the outcome is appended after, and
// a failed outcome write still reports the backup the supervisor made.
func TestBackupCreateAuditsIntentBeforeTheSupervisor(t *testing.T) {
	sup := &fakeSupervisor{dests: map[string]map[string]any{}}
	// down swaps in a refusing supervisor without racing the server's goroutines.
	var down atomic.Bool
	fake := sup.handler(t)
	supSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "busy", "message": "a backup is running"})
			return
		}
		fake.ServeHTTP(w, r)
	}))
	defer supSrv.Close()
	createdBy := func() string { sup.mu.Lock(); defer sup.mu.Unlock(); return sup.createdBy }
	dsn := testDSN(t)
	passFile := filepath.Join(t.TempDir(), "backup_passphrase")
	if err := os.WriteFile(passFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := Open(context.Background(), Config{DatabaseURL: dsn, Key: testKey, Domain: "firm.example", DataDir: dataDir(t, dsn),
		SupervisorURL: supSrv.URL, SupervisorSecret: supSecret, BackupPassphraseFile: passFile, NoBackground: true, MailEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler())
	defer func() { srv.Close(); _ = app.Close() }()
	admin := firstRun(t, srv.URL)
	me := str(t, admin.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")

	super, err := db.OpenPostgres(dsn, "core")
	if err != nil {
		t.Fatal(err)
	}
	defer super.Close()
	ctx := context.Background()
	exec := func(q string) {
		t.Helper()
		if _, err := super.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// refuse makes audit_log inserts matching cond fail, as a full disk or lost
	// connection would, until allow drops the trigger.
	refuse := func(cond string) {
		exec(`CREATE OR REPLACE FUNCTION test_refuse_audit() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN IF ` + cond + ` THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$`)
		exec(`CREATE TRIGGER test_refuse_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION test_refuse_audit()`)
	}
	allow := func() { exec(`DROP TRIGGER IF EXISTS test_refuse_audit ON audit_log`) }
	rows := func(q string) int {
		t.Helper()
		var n int
		if err := super.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The intent row cannot be written: the supervisor is never asked.
	refuse(`NEW.entity = 'backup_request'`)
	if r := admin.do("POST", "/api/v1/backups", nil); r.code < 500 {
		t.Fatalf("backup with the audit log refusing the request: %d %s", r.code, r.body)
	}
	if createdBy() != "" {
		t.Fatal("the supervisor made a backup whose request was not audited")
	}
	allow()

	// The outcome row cannot be written: the backup exists and is reported.
	refuse(`NEW.entity = 'backup' OR NEW.field = 'created'`)
	made := admin.do("POST", "/api/v1/backups", nil)
	allow()
	if made.code != http.StatusAccepted || str(t, made.json, "id") != "20261112T090000Z-0123456789abcdef" || made.json["checksum"] != heldSum {
		t.Fatalf("created backup not reported after an outcome audit failure: %d %s", made.code, made.body)
	}
	if got := createdBy(); got != me {
		t.Fatalf("supervisor saw %q", got)
	}
	if n := rows(`SELECT count(*) FROM audit_log WHERE entity = 'backup_request' AND field = 'requested'`); n != 1 {
		t.Fatalf("intent rows = %d, want 1 (the refused request wrote none)", n)
	}

	// A normal create writes the intent, then the outcome with the id and SHA-256.
	ok := admin.must(http.StatusAccepted, "POST", "/api/v1/backups", nil)
	id := str(t, ok.json, "id")
	if n := rows(`SELECT count(*) FROM audit_log WHERE entity = 'backup_request' AND field = 'requested'`); n != 2 {
		t.Fatalf("intent rows = %d, want 2", n)
	}
	if n := rows(`SELECT count(*) FROM audit_log a JOIN audit_log o ON o.entity_id = a.entity_id
		WHERE a.entity = 'backup_request' AND a.field = 'requested' AND o.field = 'created' AND o.new_value = '` + id + `' AND o.created_at >= a.created_at`); n != 1 {
		t.Fatalf("outcome rows linked to the request = %d, want 1", n)
	}
	if body := auditFields(t, admin, "backup", id); !strings.Contains(body, heldSum) {
		t.Fatalf("backup outcome lacks the SHA-256: %s", body)
	}

	// The supervisor refuses: the request's failure is appended.
	down.Store(true)
	if r := admin.do("POST", "/api/v1/backups", nil); r.code < 400 {
		t.Fatalf("supervisor refusal answered %d", r.code)
	}
	if n := rows(`SELECT count(*) FROM audit_log WHERE entity = 'backup_request' AND field = 'failed' AND new_value LIKE '%a backup is running%'`); n != 1 {
		t.Fatalf("failed outcome rows = %d, want 1", n)
	}
}

// Codex PR #41 on 690f8ae: deleting a backup and creating, changing, testing or deleting a
// destination are audited before the supervisor is asked, as create is. When the intent
// row cannot be written the supervisor never sees the write; a write that succeeds is
// reported even if its outcome row then fails.
func TestBackupWritesAuditIntentBeforeTheSupervisor(t *testing.T) {
	sup := &fakeSupervisor{dests: map[string]map[string]any{}}
	var mu sync.Mutex
	writes := map[string]int{}
	fake := sup.handler(t)
	supSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			key := r.Method + " " + r.URL.Path
			if strings.HasPrefix(r.URL.Path, "/v1/backup-destinations/") && !strings.HasSuffix(r.URL.Path, "/test") {
				key = r.Method + " /v1/backup-destinations/{id}"
			}
			mu.Lock()
			writes[key]++
			mu.Unlock()
		}
		fake.ServeHTTP(w, r)
	}))
	defer supSrv.Close()
	seen := func(key string) int { mu.Lock(); defer mu.Unlock(); return writes[key] }
	dsn := testDSN(t)
	passFile := filepath.Join(t.TempDir(), "backup_passphrase")
	if err := os.WriteFile(passFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := Open(context.Background(), Config{DatabaseURL: dsn, Key: testKey, Domain: "firm.example", DataDir: dataDir(t, dsn),
		SupervisorURL: supSrv.URL, SupervisorSecret: supSecret, BackupPassphraseFile: passFile, NoBackground: true, MailEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler())
	defer func() { srv.Close(); _ = app.Close() }()
	admin := firstRun(t, srv.URL)
	super, err := db.OpenPostgres(dsn, "core")
	if err != nil {
		t.Fatal(err)
	}
	defer super.Close()
	ctx := context.Background()
	exec := func(q string) {
		t.Helper()
		if _, err := super.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	refuse := func(cond string) {
		exec(`CREATE OR REPLACE FUNCTION test_refuse_audit() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN IF ` + cond + ` THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$`)
		exec(`CREATE TRIGGER test_refuse_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION test_refuse_audit()`)
	}
	allow := func() { exec(`DROP TRIGGER IF EXISTS test_refuse_audit ON audit_log`) }
	rows := func(q string) int {
		t.Helper()
		var n int
		if err := super.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	dest := map[string]any{"kind": "network_path", "name": "nas", "retention_days": 7, "settings": map[string]any{"path": "/srv/backups"}}
	const backup = "/api/v1/backups/20261112T090000Z-0123456789abcdef"

	// Intent refused: nothing reaches the supervisor.
	refuse(`NEW.entity = 'backup_operation' AND NEW.field = 'requested'`)
	if r := admin.do("DELETE", backup, nil); r.code < 500 {
		t.Fatalf("delete with the audit log refusing the request: %d %s", r.code, r.body)
	}
	if r := admin.do("POST", "/api/v1/backup-destinations", dest); r.code < 500 {
		t.Fatalf("destination create with the audit log refusing the request: %d %s", r.code, r.body)
	}
	allow()
	if n := seen("DELETE /v1/backups/20261112T090000Z-0123456789abcdef") + seen("POST /v1/backup-destinations"); n != 0 {
		t.Fatalf("the supervisor made %d writes whose requests were not audited", n)
	}

	// Normal writes: intent, then completed and the outcome.
	made := admin.must(http.StatusCreated, "POST", "/api/v1/backup-destinations", dest)
	id := str(t, made.json, "id")
	admin.must(http.StatusOK, "PATCH", "/api/v1/backup-destinations/"+id, map[string]any{"name": "nas2"})
	admin.must(http.StatusOK, "POST", "/api/v1/backup-destinations/"+id+"/test", nil)

	refuse(`NEW.entity = 'backup_operation' AND NEW.field = 'requested'`)
	before := seen("DELETE /v1/backup-destinations/{id}")
	if r := admin.do("DELETE", "/api/v1/backup-destinations/"+id, nil); r.code < 500 {
		t.Fatalf("destination delete with the audit log refusing the request: %d %s", r.code, r.body)
	}
	if r := admin.do("PATCH", "/api/v1/backup-destinations/"+id, map[string]any{"name": "nas3"}); r.code < 500 {
		t.Fatalf("destination update with the audit log refusing the request: %d %s", r.code, r.body)
	}
	allow()
	if seen("DELETE /v1/backup-destinations/{id}") != before || seen("POST /v1/backup-destinations") != 2 {
		t.Fatalf("unaudited destination writes reached the supervisor: %v", writes)
	}

	// Outcome refused after the supervisor deleted: the delete is reported, not hidden.
	refuse(`NEW.field = 'completed' OR NEW.field = 'deleted'`)
	gone := admin.do("DELETE", backup, nil)
	allow()
	if gone.code != http.StatusNoContent || seen("DELETE /v1/backups/20261112T090000Z-0123456789abcdef") != 1 {
		t.Fatalf("delete not reported after an outcome audit failure: %d %s", gone.code, gone.body)
	}
	admin.must(http.StatusNoContent, "DELETE", "/api/v1/backup-destinations/"+id, nil)

	if n := rows(`SELECT count(*) FROM audit_log WHERE entity = 'backup_operation' AND field = 'requested'`); n != 5 {
		t.Fatalf("intent rows = %d, want 5 (create, update, test, delete backup, delete destination)", n)
	}
	if n := rows(`SELECT count(*) FROM audit_log a JOIN audit_log o ON o.entity_id = a.entity_id
		WHERE a.entity = 'backup_operation' AND a.field = 'requested' AND o.field = 'completed'`); n != 4 {
		t.Fatalf("completed rows linked to their request = %d, want 4 (the backup delete's outcome was refused)", n)
	}
	if n := rows(`SELECT count(*) FROM audit_log WHERE entity = 'backup_destination' AND field IN ('created','name','tested','deleted')`); n != 4 {
		t.Fatalf("destination outcome rows = %d, want 4", n)
	}
}
