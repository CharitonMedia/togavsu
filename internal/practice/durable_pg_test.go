package practice

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// practiceDB returns a migrated database for t, or nil when no test Postgres is set.
func practiceDB(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		return nil
	}
	ctx := context.Background()
	admin, err := db.OpenPostgres(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	sum := sha256.Sum256([]byte("practice/" + t.Name()))
	name := "p" + hex.EncodeToString(sum[:8])
	if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin, err := db.OpenPostgres(raw, "")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name)
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	plain, err := db.OpenPostgres(u.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	ms, err := db.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
	_ = plain.Close()
	out, err := db.OpenPostgres(u.String(), "core")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	if err := audit.InstallPrivileges(ctx, out); err != nil {
		t.Fatal(err)
	}
	return out
}

func requirePG(t *testing.T) *sql.DB {
	t.Helper()
	pool := practiceDB(t)
	if pool == nil {
		if os.Getenv("CI") != "" {
			t.Fatal("TOGAVSU_TEST_DATABASE_URL is required in CI")
		}
		t.Skip("TOGAVSU_TEST_DATABASE_URL is not set")
	}
	return pool
}

func count(t *testing.T, pool *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestDurableSurvivesRestart writes through the service, reopens the store from
// Postgres and finds the same matters, contacts, walls, history and closes.
func TestDurableSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	pool := requirePG(t)
	store, err := OpenDurable(ctx, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewWithSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	admin := person(tid(1), domain.RoleAdmin, domain.RoleAttorney)
	staff := person(tid(2), domain.RoleStaff)
	putUser(t, store.Memory, admin, "Avery Admin")
	putUser(t, store.Memory, staff, "Sam Staff")
	area := makeArea(t, svc, admin, "Probate")
	m, err := makeMatterErr(svc, admin, area, "Durable matter")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, m.ID, []domain.ID{admin.UserID}); err != nil {
		t.Fatal(err)
	}
	before := len(store.Audits())
	if before == 0 {
		t.Fatal("no audit rows")
	}
	if n := count(t, pool, `SELECT count(*) FROM audit_log`); n != before {
		t.Fatalf("audit_log has %d rows, practice has %d", n, before)
	}

	again, err := OpenDurable(ctx, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := again.Matter(ctx, m.ID)
	if err != nil || got.Number != m.Number || got.Name != "Durable matter" {
		t.Fatal(err, got)
	}
	if len(again.Walls(m.ID)) != 1 {
		t.Fatal("wall lost on restart", again.Walls(m.ID))
	}
	if len(again.Audits()) != before {
		t.Fatalf("history %d after restart, %d before", len(again.Audits()), before)
	}
	svc2, err := NewWithSettings(again)
	if err != nil {
		t.Fatal(err)
	}
	putUser(t, again.Memory, staff, "Sam Staff")
	if _, err := svc2.GetMatter(ctx, staff, m.ID); err == nil {
		t.Fatal("wall did not hold after restart")
	}
	list, err := svc2.ListMatters(ctx, staff, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list {
		if v.Matter.ID == m.ID {
			t.Fatal("walled matter listed to staff after restart")
		}
	}
}

// TestDurableFailedCommitLeavesNoTrace: an audit append that fails rolls back the
// practice rows written with it, and Memory reloads so the write is not served.
func TestDurableFailedCommitLeavesNoTrace(t *testing.T) {
	ctx := context.Background()
	pool := requirePG(t)
	store, err := OpenDurable(ctx, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	entry := domain.AuditEntry{Meta: domain.Meta{ID: tid(50), CreatedAt: now, UpdatedAt: now, CreatedBy: tid(1)},
		ActorID: tid(1), Role: "admin", Source: "ui", Entity: "matter", EntityID: tid(51), Field: "name", NewValue: "x"}
	if err := store.AppendAudit(ctx, []storedAudit{{Entry: entry, MatterID: tid(51)}}, nil); err != nil {
		t.Fatal(err)
	}
	// The same ledger id again violates the audit_log primary key.
	contact := domain.Contact{Meta: domain.Meta{ID: tid(52), CreatedAt: now, UpdatedAt: now, CreatedBy: tid(1)}, Kind: "person", FullName: "Rolled Back"}
	if err := store.InsertContact(ctx, contact); err != nil {
		t.Fatal(err)
	}
	err = store.AppendAudit(ctx, []storedAudit{{Entry: entry, MatterID: tid(51)}, {Entry: domain.AuditEntry{Meta: domain.Meta{ID: tid(53)}}}}, nil)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		t.Fatalf("want duplicate key, got %v", err)
	}
	if n := len(store.Audits()); n != 1 {
		t.Fatalf("memory serves %d audit rows after a failed commit", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM practice_records WHERE kind = 'audit'`); n != 1 {
		t.Fatalf("%d stored audit documents", n)
	}
	if _, err := store.Contact(ctx, contact.ID); err != nil {
		t.Fatal("committed contact lost on reload", err)
	}
	// The writer role that practice appends through cannot remove ledger rows. The
	// non-superuser owner case is internal/audit TestApplicationRoleCannotChangeAuditRows.
	if err := audit.RawAsWriter(ctx, pool, `DELETE FROM audit_log`); err == nil {
		t.Fatal("writer role deleted audit_log rows")
	}
}
