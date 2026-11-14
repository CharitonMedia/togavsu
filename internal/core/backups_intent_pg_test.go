package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
