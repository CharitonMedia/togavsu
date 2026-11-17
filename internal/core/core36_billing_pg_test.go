package core

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/billing"
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

// TestCORE36BillingRowsBackfill: RebuildCoreRows copies pre-migration billing_records into
// section-5 tables with every cent field matching the API, refuses a cent mismatch, and
// skips a second start after the billing_core_rows_backfill marker (CU2-06 / CORE-36).
func TestCORE36BillingRowsBackfill(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	open, openClient := x.matter("Billing Backfill Estate", "billing.backfill@client.example", nil)
	x.timeEntry(open, 3, 150_00)
	invID := items(t, x.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": open}))[0]["id"].(string)
	x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/approve", nil)
	x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/send", map[string]any{"deliver": "file"})

	pay := x.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": openClient, "matter_id": open, "amount_cents": 25_00,
		"destination": "operating", "method": "check", "invoice_id": invID,
	})
	payID := str(t, pay.json, "id")

	expType := str(t, x.admin.must(http.StatusCreated, "POST", "/api/v1/expense-types", map[string]any{"name": "Backfill courier"}).json, "id")
	markup := 10
	exp := x.staff.must(http.StatusCreated, "POST", "/api/v1/expenses", map[string]any{
		"matter_id": open, "incurred_on": "2026-03-01", "description": "courier",
		"amount_cents": 50_00, "expense_type_id": expType, "billable": true, "markup_percent": markup,
	})
	expID := str(t, exp.json, "id")

	apiInv := x.atty.must(http.StatusOK, "GET", "/api/v1/invoices/"+invID, nil).json
	apiPay := x.admin.must(http.StatusOK, "GET", "/api/v1/payments/"+payID, nil).json
	apiExp := x.staff.must(http.StatusOK, "GET", "/api/v1/expenses/"+expID, nil).json

	// Pre-migration 1400 shape: billing_records hold the JSON; section-5 tables are empty.
	clearCoreBilling := func() {
		t.Helper()
		for _, q := range []string{
			`DELETE FROM core.payments`,
			`DELETE FROM core.invoice_lines`,
			`DELETE FROM core.invoices`,
			`DELETE FROM core.expenses`,
		} {
			if _, err := x.app.Pool.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	clearCoreBilling()
	if _, err := x.app.Pool.ExecContext(ctx, `DELETE FROM core.billing_core_rows_backfill`); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM core.invoices`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("expected empty core.invoices before backfill, got %d err=%v", left, err)
	}

	store := billing.NewPostgres(x.app.Pool)
	if err := store.RebuildCoreRows(ctx); err != nil {
		t.Fatalf("RebuildCoreRows: %v", err)
	}

	assertInvoiceCents(t, x, ctx, invID, apiInv)
	assertPaymentCents(t, x, ctx, payID, apiPay)
	assertExpenseCents(t, x, ctx, expID, apiExp)

	var markers int
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM core.billing_core_rows_backfill`).Scan(&markers); err != nil || markers != 1 {
		t.Fatalf("backfill marker: n=%d err=%v", markers, err)
	}

	// Marker case: wiping section-5 rows must not re-copy on the next start.
	clearCoreBilling()
	if err := store.RebuildCoreRows(ctx); err != nil {
		t.Fatalf("RebuildCoreRows with marker: %v", err)
	}
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM core.invoices`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("marker should skip rewrite; invoices=%d err=%v", left, err)
	}

	// Failure case: a cent mismatch on any widened field fails reconcile (stops RebuildCoreRows).
	if _, err := x.app.Pool.ExecContext(ctx, `DELETE FROM core.billing_core_rows_backfill`); err != nil {
		t.Fatal(err)
	}
	if err := store.RebuildCoreRows(ctx); err != nil {
		t.Fatalf("re-seed before mismatch: %v", err)
	}
	mustMismatch := func(kind, id string, v any, corrupt func() error) {
		t.Helper()
		if err := corrupt(); err != nil {
			t.Fatal(err)
		}
		err := billing.ReconcileCoreRowForTest(ctx, x.app.Pool, kind, id, v)
		if err == nil || !strings.Contains(err.Error(), "mismatch") {
			t.Fatalf("%s reconcile: want mismatch, got %v", kind, err)
		}
	}
	var payDoc billing.Payment
	if err := store.Get(ctx, "payment", payID, &payDoc); err != nil {
		t.Fatal(err)
	}
	mustMismatch("payment", payID, payDoc, func() error {
		_, err := x.app.Pool.ExecContext(ctx, `UPDATE core.payments SET refunded_cents = refunded_cents + 7 WHERE id = $1`, payID)
		return err
	})
	var expDoc billing.Expense
	if err := store.Get(ctx, "expense", expID, &expDoc); err != nil {
		t.Fatal(err)
	}
	mustMismatch("expense", expID, expDoc, func() error {
		_, err := x.app.Pool.ExecContext(ctx, `UPDATE core.expenses SET markup_percent = markup_percent + 3 WHERE id = $1`, expID)
		return err
	})
	var invDoc billing.Invoice
	if err := store.Get(ctx, "invoice", invID, &invDoc); err != nil {
		t.Fatal(err)
	}
	// Corrupted invoice_lines amount_cents must fail reconcile while header cents still match.
	mustMismatch("invoice", invID, invDoc, func() error {
		_, err := x.app.Pool.ExecContext(ctx,
			`UPDATE core.invoice_lines SET amount_cents = amount_cents + 11 WHERE invoice_id = $1`, invID)
		return err
	})
	if _, err := x.app.Pool.ExecContext(ctx,
		`UPDATE core.invoice_lines SET amount_cents = amount_cents - 11 WHERE invoice_id = $1`, invID); err != nil {
		t.Fatal(err)
	}
	mustMismatch("invoice", invID, invDoc, func() error {
		_, err := x.app.Pool.ExecContext(ctx, `UPDATE core.invoices SET subtotal_cents = subtotal_cents + 1 WHERE id = $1`, invID)
		return err
	})
}

func assertInvoiceCents(t *testing.T, x *ext, ctx context.Context, id string, api map[string]any) {
	t.Helper()
	var subtotal, adjustment, interest, total, paid, balance, writeOff int64
	if err := x.app.Pool.QueryRowContext(ctx,
		`SELECT subtotal_cents, adjustment_cents, interest_cents, total_cents,
amount_paid_cents, balance_due_cents, write_off_cents FROM core.invoices WHERE id = $1`, id).
		Scan(&subtotal, &adjustment, &interest, &total, &paid, &balance, &writeOff); err != nil {
		t.Fatalf("core.invoices %s: %v", id, err)
	}
	checks := []struct {
		name string
		api  int64
		sql  int64
	}{
		{"subtotal_cents", num(t, api, "subtotal_cents"), subtotal},
		{"adjustment_cents", num(t, api, "adjustment_cents"), adjustment},
		{"interest_cents", num(t, api, "interest_cents"), interest},
		{"total_cents", num(t, api, "total_cents"), total},
		{"amount_paid_cents", num(t, api, "amount_paid_cents"), paid},
		{"balance_due_cents", num(t, api, "balance_due_cents"), balance},
		{"write_off_cents", num(t, api, "write_off_cents"), writeOff},
	}
	for _, c := range checks {
		if c.api != c.sql {
			t.Fatalf("invoice %s %s api=%d sql=%d", id, c.name, c.api, c.sql)
		}
	}
	apiLines, _ := api["lines"].([]any)
	var lineCount int
	var lineSum int64
	if err := x.app.Pool.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(amount_cents), 0) FROM core.invoice_lines WHERE invoice_id = $1`, id).
		Scan(&lineCount, &lineSum); err != nil {
		t.Fatalf("core.invoice_lines %s: %v", id, err)
	}
	if lineCount != len(apiLines) {
		t.Fatalf("invoice %s invoice_lines count api=%d sql=%d", id, len(apiLines), lineCount)
	}
	var wantSum int64
	for _, raw := range apiLines {
		line, _ := raw.(map[string]any)
		wantSum += num(t, line, "amount_cents")
	}
	if lineSum != wantSum {
		t.Fatalf("invoice %s invoice_lines sum api=%d sql=%d", id, wantSum, lineSum)
	}
	rows, err := x.app.Pool.QueryContext(ctx,
		`SELECT amount_cents FROM core.invoice_lines WHERE invoice_id = $1 ORDER BY amount_cents, id`, id)
	if err != nil {
		t.Fatalf("list invoice_lines: %v", err)
	}
	defer rows.Close()
	var sqlAmts []int64
	for rows.Next() {
		var amt int64
		if err := rows.Scan(&amt); err != nil {
			t.Fatal(err)
		}
		sqlAmts = append(sqlAmts, amt)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	apiAmts := make([]int64, 0, len(apiLines))
	for _, raw := range apiLines {
		line, _ := raw.(map[string]any)
		apiAmts = append(apiAmts, num(t, line, "amount_cents"))
	}
	sort.Slice(apiAmts, func(i, j int) bool { return apiAmts[i] < apiAmts[j] })
	if len(apiAmts) != len(sqlAmts) {
		t.Fatalf("invoice %s line amounts len api=%d sql=%d", id, len(apiAmts), len(sqlAmts))
	}
	for i := range apiAmts {
		if apiAmts[i] != sqlAmts[i] {
			t.Fatalf("invoice %s line amounts api=%v sql=%v", id, apiAmts, sqlAmts)
		}
	}
}

func assertPaymentCents(t *testing.T, x *ext, ctx context.Context, id string, api map[string]any) {
	t.Helper()
	var amount, refunded int64
	var status string
	var invoiceID *string
	if err := x.app.Pool.QueryRowContext(ctx,
		`SELECT amount_cents, refunded_cents, status, invoice_id FROM core.payments WHERE id = $1`, id).
		Scan(&amount, &refunded, &status, &invoiceID); err != nil {
		t.Fatalf("core.payments %s: %v", id, err)
	}
	if amount != num(t, api, "amount_cents") || refunded != num(t, api, "refunded_cents") {
		t.Fatalf("payment %s cents api=(%d,%d) sql=(%d,%d)", id,
			num(t, api, "amount_cents"), num(t, api, "refunded_cents"), amount, refunded)
	}
	if status != str(t, api, "status") {
		t.Fatalf("payment %s status api=%s sql=%s", id, str(t, api, "status"), status)
	}
	wantInv := str(t, api, "invoice_id")
	gotInv := ""
	if invoiceID != nil {
		gotInv = *invoiceID
	}
	if gotInv != wantInv {
		t.Fatalf("payment %s invoice_id api=%s sql=%s", id, wantInv, gotInv)
	}
}

func assertExpenseCents(t *testing.T, x *ext, ctx context.Context, id string, api map[string]any) {
	t.Helper()
	var amount int64
	var markup int
	if err := x.app.Pool.QueryRowContext(ctx,
		`SELECT amount_cents, markup_percent FROM core.expenses WHERE id = $1`, id).
		Scan(&amount, &markup); err != nil {
		t.Fatalf("core.expenses %s: %v", id, err)
	}
	if amount != num(t, api, "amount_cents") || int64(markup) != num(t, api, "markup_percent") {
		t.Fatalf("expense %s api=(%d,%d) sql=(%d,%d)", id,
			num(t, api, "amount_cents"), num(t, api, "markup_percent"), amount, markup)
	}
}
