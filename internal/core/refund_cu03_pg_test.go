package core

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

const refundProcessorManifest = `id: pay-proc
name: Synthetic Processor
version: "1.0.0"
author: Synthetic author
container:
  image: ghcr.io/example/pay-proc
  digest: sha256:4444444444444444444444444444444444444444444444444444444444444444
  port: 8080
  events: /events
permissions:
  - scope: payments:write
  - scope: trust:write
  - scope: trust:withdraw
  - scope: events:read
payment_processor:
  destinations: [operating, trust]
events:
  - payment.processor_authorized
  - payment.refund_authorized
`

// TestCU03TrustRefundBelowZero: a trust payment refund that takes the client below zero
// posts, raises trust.flag_raised and notifies the admins / responsible attorney (CU-03).
func TestCU03TrustRefundBelowZero(t *testing.T) {
	x := newExt(t, func(c *Config) { c.MailEvery = 50 * time.Millisecond })
	matter, clientID := x.matter("Below Zero Refund", "bz-refund@client.example", nil)
	pay := x.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": clientID, "matter_id": matter, "amount_cents": 100_00,
		"destination": "trust", "trust_account_id": x.account, "method": "ach",
	})
	payID := str(t, pay.json, "id")
	x.atty.must(http.StatusCreated, "POST", "/api/v1/trust-ledger", map[string]any{
		"trust_account_id": x.account, "client_id": clientID, "matter_id": matter,
		"entry_type": "withdrawal", "amount_cents": 80_00, "method": "check", "description": "draw",
	})

	beforeFlags := x.count(`SELECT COUNT(*) FROM trust_flags`)
	ref := x.atty.must(http.StatusCreated, "POST", "/api/v1/payments/"+payID+"/refunds", map[string]any{
		"amount_cents": 100_00, "reason": "full refund after draw", "status": "completed",
	})
	if ref.json["trust_entry_id"] == nil || ref.json["trust_entry_id"] == "" {
		t.Fatalf("below-zero refund entry: %v", ref.json)
	}
	if entries := x.count(`SELECT COUNT(*) FROM trust_ledger WHERE entry_type = 'refund' AND matter_id = $1`, matter); entries != 1 {
		t.Fatalf("refund ledger rows: %d", entries)
	}
	if x.count(`SELECT COUNT(*) FROM trust_flags`) <= beforeFlags {
		t.Fatal("expected trust_flags after below-zero refund")
	}
	var notes int
	if err := x.app.Pool.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM notifications WHERE matter_id = $1`, matter).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes == 0 {
		t.Fatal("expected below-zero notifications for admins/attorney")
	}
	if mail := x.count(`SELECT COUNT(*) FROM mail_outbox WHERE kind = $1`, "trust_flag"); mail == 0 {
		t.Fatal("expected below-zero flag email queued for admins/attorney")
	}
}

// TestCORE24RefundAuthorizationGrant: a pending refund on a processor-sourced payment
// delivers refund_authorization; the module completes it with the task authorization.
func TestCORE24RefundAuthorizationGrant(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	inbox := &processorInbox{}
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	x.rt.Handle("pay-proc", inbox)
	if _, problem := x.tabForm(x.admin, "install", nil, x.build(onlyManifest(refundProcessorManifest))); problem != "" {
		t.Fatalf("install pay-proc: %s", problem)
	}
	x.approveOnTab("pay-proc")
	matter, clientID := x.matter("Refund Grant Estate", "refund-grant@client.example", nil)

	init := x.staff.must(http.StatusCreated, "POST", "/api/v1/payments/processor-initializations", map[string]any{
		"processor_module_id": "pay-proc", "contact_id": clientID, "matter_id": matter,
		"amount_cents": 90_00, "destination": "operating",
	})
	payID := str(t, init.json, "payment_id")
	if _, err := x.app.Modules.Pump(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	_, grant := inbox.last(t)
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("pay-proc").Secrets["togavsu_api_token"]
	created := bot.do("POST", "/api/v1/payments", map[string]any{
		"contact_id": clientID, "matter_id": matter, "amount_cents": 90_00,
		"destination": "operating", "method": "card", "reference_number": "ch_refund",
	}, modules.TaskAuthorizationHeader, grant)
	if created.code != http.StatusCreated || created.json["id"] != payID {
		t.Fatalf("payment id: %d %s", created.code, created.body)
	}

	inbox.mu.Lock()
	inbox.bodies = nil
	inbox.grants = nil
	inbox.mu.Unlock()

	pending := x.atty.must(http.StatusCreated, "POST", "/api/v1/payments/"+payID+"/refunds", map[string]any{
		"amount_cents": 40_00, "reason": "partial via processor", "status": "pending",
	})
	refundID := str(t, pending.json, "id")
	if _, err := x.app.Modules.Pump(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	body, refundGrant := inbox.last(t)
	auth, _ := body["refund_authorization"].(map[string]any)
	if auth == nil || auth["refund_id"] != refundID || auth["payment_id"] != payID || !strings.HasPrefix(refundGrant, "tra1.") {
		t.Fatalf("refund grant delivery: %v grant %q", body, refundGrant)
	}
	done := bot.do("POST", "/api/v1/payments/"+payID+"/refunds/"+refundID+"/status", map[string]any{
		"status": "completed",
	}, modules.TaskAuthorizationHeader, refundGrant)
	if done.code != http.StatusOK || done.json["status"] != "completed" {
		t.Fatalf("complete via grant: %d %s", done.code, done.body)
	}
	got := x.atty.must(http.StatusOK, "GET", "/api/v1/payments/"+payID, nil).json
	if int64(got["refunded_cents"].(float64)) != 40_00 || got["refund_state"] != "partial" {
		t.Fatalf("payment after grant refund: %v", got)
	}
	if rows := x.count(`SELECT COUNT(*) FROM payment_refunds WHERE id = $1 AND status = 'completed'`, refundID); rows != 1 {
		t.Fatalf("payment_refunds after grant: %d", rows)
	}
}
