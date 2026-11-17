package core

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"testing"
)

// TestCORE36BillingRowsAndModuleFK: after dual-write/backfill, API cents match SQL rows
// and a module migration FK to core.invoices installs and holds (CU2-06).
func TestCORE36BillingRowsAndModuleFK(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	open, openClient := x.matter("Billing Rows Estate", "billing.rows@client.example", nil)
	x.timeEntry(open, 2, 200_00)
	invID := items(t, x.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": open}))[0]["id"].(string)
	x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/approve", nil)
	sent := x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/send", map[string]any{"deliver": "file"})
	apiTotal := num(t, sent.json, "total_cents")
	apiBalance := num(t, sent.json, "balance_due_cents")

	var sqlTotal, sqlBalance int64
	if err := x.app.Pool.QueryRowContext(ctx,
		`SELECT total_cents, balance_due_cents FROM core.invoices WHERE id = $1`, invID).
		Scan(&sqlTotal, &sqlBalance); err != nil {
		t.Fatalf("core.invoices empty: %v", err)
	}
	if sqlTotal != apiTotal || sqlBalance != apiBalance {
		t.Fatalf("cent mismatch api=(%d,%d) sql=(%d,%d)", apiTotal, apiBalance, sqlTotal, sqlBalance)
	}

	pay := x.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": openClient, "matter_id": open, "amount_cents": 10_00,
		"destination": "operating", "method": "check", "invoice_id": invID,
	})
	payID := str(t, pay.json, "id")
	var payAmount int64
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT amount_cents FROM core.payments WHERE id = $1`, payID).Scan(&payAmount); err != nil {
		t.Fatalf("core.payments empty: %v", err)
	}
	if payAmount != num(t, pay.json, "amount_cents") {
		t.Fatalf("payment cents api=%d sql=%d", num(t, pay.json, "amount_cents"), payAmount)
	}

	expType := str(t, x.admin.must(http.StatusCreated, "POST", "/api/v1/expense-types", map[string]any{"name": "Billing rows fee"}).json, "id")
	exp := x.staff.must(http.StatusCreated, "POST", "/api/v1/expenses", map[string]any{
		"matter_id": open, "incurred_on": "2026-02-01", "description": "filing fee",
		"amount_cents": 75_00, "expense_type_id": expType, "billable": true,
	})
	expID := str(t, exp.json, "id")
	var expAmount int64
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT amount_cents FROM core.expenses WHERE id = $1`, expID).Scan(&expAmount); err != nil {
		t.Fatalf("core.expenses empty: %v", err)
	}
	if expAmount != num(t, exp.json, "amount_cents") {
		t.Fatalf("expense cents api=%d sql=%d", num(t, exp.json, "amount_cents"), expAmount)
	}

	// Module FK to core.invoices installs and holds (keep matter-clock id; only add migration).
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f["migrations/0002_link_invoices.sql"] = []byte(
			"-- +goose Up\nCREATE TABLE linked_invoices (invoice_id TEXT PRIMARY KEY REFERENCES core.invoices (id) ON DELETE RESTRICT);\n-- +goose Down\nDROP TABLE linked_invoices;\n")
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install with FK: %s", problem)
	}
	x.approveOnTab("matter-clock")
	role := x.rt.Last("matter-clock").Secrets["togavsu_db_role"]
	pass := x.rt.Last("matter-clock").Secrets["togavsu_db_password"]
	db := x.moduleDB(role, pass)
	if _, err := db.ExecContext(ctx, `INSERT INTO linked_invoices (invoice_id) VALUES ($1)`, invID); err != nil {
		t.Fatalf("FK insert: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM linked_invoices WHERE invoice_id = $1`, invID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("FK hold: n=%d err=%v", n, err)
	}
}
