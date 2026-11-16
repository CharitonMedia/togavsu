package core

import (
	"net/http"
	"testing"
)

// TestPaymentRefundsAPI is CORE-24 on the running core: create/list/complete a refund on a
// completed operating payment, refuse an over-remaining amount, and post a trust refund
// ledger row in the same transaction.
func TestPaymentRefundsAPI(t *testing.T) {
	x := newExt(t)
	matter, clientID := x.matter("Refund Estate", "refund@client.example", nil)
	x.timeEntry(matter, 10, 200_00)
	drafts := x.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter})
	list := items(t, drafts)
	invID := list[0]["id"].(string)
	x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/approve", nil)
	sent := x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/send", map[string]any{"deliver": "file"})
	total := int64(sent.json["total_cents"].(float64))

	pay := x.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": clientID, "matter_id": matter, "amount_cents": total,
		"destination": "operating", "method": "check", "invoice_id": invID,
	}).json
	payID := str(t, pay, "id")

	if r := x.staff.do("POST", "/api/v1/payments/"+payID+"/refunds", map[string]any{
		"amount_cents": 50_00, "reason": "partial", "status": "completed",
	}); r.code != http.StatusForbidden {
		t.Fatalf("staff refund: %d %v", r.code, r.json)
	}

	ref := x.atty.must(http.StatusCreated, "POST", "/api/v1/payments/"+payID+"/refunds", map[string]any{
		"amount_cents": 50_00, "reason": "client dispute", "status": "completed",
	}).json
	if ref["status"] != "completed" || int64(ref["amount_cents"].(float64)) != 50_00 {
		t.Fatalf("refund: %v", ref)
	}
	got := x.atty.must(http.StatusOK, "GET", "/api/v1/payments/"+payID, nil).json
	if int64(got["refunded_cents"].(float64)) != 50_00 || got["refund_state"] != "partial" || got["status"] != "completed" {
		t.Fatalf("payment: %v", got)
	}
	listed := x.atty.must(http.StatusOK, "GET", "/api/v1/payments/"+payID+"/refunds", nil).json
	if data, _ := listed["data"].([]any); len(data) != 1 {
		t.Fatalf("list: %v", listed)
	}

	if r := x.atty.do("POST", "/api/v1/payments/"+payID+"/refunds", map[string]any{
		"amount_cents": total, "reason": "too much",
	}); r.code != http.StatusBadRequest || r.json["code"] != "refund_exceeds_payment" {
		t.Fatalf("over-remaining: %d %v", r.code, r.json)
	}

	pending := x.atty.must(http.StatusCreated, "POST", "/api/v1/payments/"+payID+"/refunds", map[string]any{
		"amount_cents": 25_00, "reason": "hold", "status": "pending",
	}).json
	refundID := str(t, pending, "id")
	x.atty.must(http.StatusOK, "POST", "/api/v1/payments/"+payID+"/refunds/"+refundID+"/status", map[string]any{"status": "pending"})
	x.atty.must(http.StatusOK, "POST", "/api/v1/payments/"+payID+"/refunds/"+refundID+"/status", map[string]any{"status": "completed"})

	listed2 := x.atty.must(http.StatusOK, "GET", "/api/v1/payments/"+payID+"/refunds", nil).json
	if data, _ := listed2["data"].([]any); len(data) != 2 {
		t.Fatalf("list after complete: %v", listed2)
	}
	got2 := x.atty.must(http.StatusOK, "GET", "/api/v1/payments/"+payID, nil).json
	if int64(got2["refunded_cents"].(float64)) != 75_00 || got2["refund_state"] != "partial" {
		t.Fatalf("payment after refunds: %v", got2)
	}

	trustPay := x.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": clientID, "matter_id": matter, "amount_cents": 100_00,
		"destination": "trust", "trust_account_id": x.account, "method": "ach",
	}).json
	trustID := str(t, trustPay, "id")
	tref := x.atty.must(http.StatusCreated, "POST", "/api/v1/payments/"+trustID+"/refunds", map[string]any{
		"amount_cents": 40_00, "reason": "overpayment", "status": "completed",
	}).json
	if tref["trust_entry_id"] == nil || tref["trust_entry_id"] == "" {
		t.Fatalf("trust refund entry: %v", tref)
	}
	if entries := x.count(`SELECT COUNT(*) FROM trust_ledger WHERE entry_type = 'refund' AND matter_id = $1`, matter); entries != 1 {
		t.Fatalf("trust refund ledger rows: %d", entries)
	}
}
