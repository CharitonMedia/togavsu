package billing

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// billingDB creates a fresh database with the core migrations and billing's migration 300
// applied in one db.Apply call, as the core is expected to run them. It skips without
// TOGAVSU_TEST_DATABASE_URL.
func billingDB(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TOGAVSU_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := db.OpenPostgres(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	sum := sha256.Sum256([]byte("billing/" + t.Name()))
	name := "b" + hex.EncodeToString(sum[:8])
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
	core, err := db.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	mine, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ValidateMigrationVersions(mine); err != nil {
		t.Fatal(err)
	}
	if task, _ := db.TaskForVersion(mine[0].Version); task != "billing" {
		t.Fatalf("migration %d is outside the billing range", mine[0].Version)
	}
	if err := db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: append(core, mine...)}); err != nil {
		t.Fatal(err)
	}
	_ = plain.Close()
	pool, err := db.OpenPostgres(u.String(), "core")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := audit.InstallPrivileges(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func pgFx(t *testing.T) (*fx, *sql.DB) {
	pool := billingDB(t)
	f := newFxStore(t, NewPostgres(pool))
	ledger := audit.NewPostgres(pool)
	f.newService(ledger)
	f.auditLen = func() int {
		var n int
		if err := pool.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return f, pool
}

// The AC-008 and AC-026 flows on Postgres: rows and their audit_log entries commit together,
// survive a new service on the same database, and a failed write leaves both untouched.
func TestPostgresBillingFlows(t *testing.T) {
	f, pool := pgFx(t)
	m, client := f.matter("PG estate", "hourly", 0, 0)
	te := f.logTime(f.attorney, m.ID, "2026-06-01", 4, nil)
	inv := f.sent(m.ID)
	if _, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: inv.TotalCents, Destination: DestOperating, Method: "check", InvoiceID: inv.ID}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := pool.QueryRow(`SELECT count(*) FROM billing_records WHERE kind = 'invoice'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("invoice rows %d %v", rows, err)
	}
	var audits int
	pool.QueryRow(`SELECT count(*) FROM audit_log WHERE entity = 'invoice' AND entity_id = $1`, string(inv.ID)).Scan(&audits)
	if audits < 3 {
		t.Fatalf("invoice audit rows %d", audits)
	}

	// A fresh service on the same database sees the paid invoice.
	f.newService(audit.NewPostgres(pool))
	again, err := f.svc.Invoice(f.ctx, f.accountant, inv.ID)
	if err != nil || again.Status != StatusPaid {
		t.Fatalf("reload: %v %s", err, again.Status)
	}
	if row, _ := f.svc.timeEntry(f.ctx, te.ID); row.InvoiceID != inv.ID {
		t.Fatal("time link lost")
	}

	// A write that fails part way stores nothing: no billing rows and no audit rows.
	flat, fclient := f.matter("PG will", "flat", 400_00, 0)
	f.deposit(flat.ID, fclient, 400_00)
	before := f.snapshot()
	orig := f.svc.d.Trust
	f.svc.d.Trust = failingPost{orig}
	if _, _, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, flat.ID, fclient, f.account, 400_00); err == nil {
		t.Fatal("expected failure")
	}
	if f.snapshot() != before {
		t.Fatal("a failed Postgres write left rows")
	}
	f.svc.d.Trust = orig
	posted, paid, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, flat.ID, fclient, f.account, 400_00)
	if err != nil || paid.Status != StatusPaid || posted.EntryID.IsZero() {
		t.Fatalf("apply: %v", err)
	}
	wl, _ := f.svc.Worklist(f.ctx, f.staff)
	for _, r := range wl.Flat {
		if r.MatterID == flat.ID && r.Status != FlatCollected {
			t.Fatalf("status %s", r.Status)
		}
	}
	// An accountant write is refused before any SQL write.
	before = f.snapshot()
	if _, err := f.svc.VoidInvoice(f.ctx, f.accountant, paid.ID, "x"); code(err) != domain.CodeForbidden {
		t.Fatal(err)
	}
	if f.snapshot() != before {
		t.Fatal("refused write changed Postgres")
	}
	// The audit rows billing writes stay append-only for the application role.
	if err := audit.RawAsWriter(f.ctx, pool, `DELETE FROM audit_log WHERE entity = 'invoice'`); err == nil {
		t.Fatal("audit_log delete must be refused")
	}
}
