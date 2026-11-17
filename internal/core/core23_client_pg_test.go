package core

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// TestCORE23ClientProcessorPayment covers CORE-23 client-initiated payments: own matter
// and contact, operating with own sent invoice, trust destination, refusals, pending grant
// lifetime past expires_at, and replenishment_request_id satisfaction. Also asserts a
// client session cannot write trust deposits or close replenishment requests directly.
func TestCORE23ClientProcessorPayment(t *testing.T) {
	var skew atomic.Int64
	x := newExt(t, func(c *Config) { c.Now = func() time.Time { return time.Now().Add(time.Duration(skew.Load())) } })
	ctx := context.Background()
	inbox := &processorInbox{}
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	x.rt.Handle("pay-proc", inbox)
	if _, problem := x.tabForm(x.admin, "install", nil, x.build(onlyManifest(extProcessorManifest))); problem != "" {
		t.Fatalf("install pay-proc: %s", problem)
	}
	x.approveOnTab("pay-proc")

	matter, contactID := x.matter("Client Pay Estate", "core23.client@client.example", nil)
	other, otherContact := x.matter("Other Estate", "other.core23@client.example", nil)
	cliKit, _ := testkit.EnrollUser(t, x.app.Identity, x.srv.URL, x.admin.kit(), "Casey Client", "core23.client@client.example", domain.ID(contactID), domain.RoleClient)
	cli := wrapClient(cliKit)

	// P0: client session stays read-only on trust writes.
	if r := cli.do("POST", "/api/v1/trust-ledger", map[string]any{
		"trust_account_id": x.account, "client_id": contactID, "matter_id": matter,
		"entry_type": "deposit", "amount_cents": 10_00, "method": "check", "description": "client deposit",
	}); r.code != http.StatusForbidden {
		t.Fatalf("client trust deposit: %d %s", r.code, r.body)
	}
	repl := x.admin.must(http.StatusCreated, "POST", "/api/v1/trust-replenishment-requests", map[string]any{
		"matter_id": matter, "client_id": contactID, "amount_requested_cents": 50_00, "message": "fund",
	})
	replID := str(t, repl.json, "id")
	if r := cli.do("POST", "/api/v1/trust-replenishment-requests/"+replID+"/cancel", nil); r.code != http.StatusForbidden {
		t.Fatalf("client cancel replenishment: %d %s", r.code, r.body)
	}
	if r := cli.do("POST", "/api/v1/trust-replenishment-requests/"+replID+"/satisfy", nil); r.code != http.StatusForbidden {
		t.Fatalf("client satisfy replenishment: %d %s", r.code, r.body)
	}
	cliP := x.principal(cli)
	if err := x.app.Trust.SetEvergreenMinimum(ctx, cliP, domain.ID(matter), 100_00); err == nil {
		t.Fatal("client SetEvergreenMinimum allowed")
	}

	initiate := func(c *client, body map[string]any) resp {
		return c.do("POST", "/api/v1/payments/processor-initializations", body)
	}

	// Operating needs the client's own sent invoice.
	x.atty.must(http.StatusCreated, "POST", "/api/v1/time-entries", map[string]any{
		"matter_id": matter, "worked_on": time.Now().UTC().Format("2006-01-02"), "increments": 10,
		"description": "Draft", "activity_type_id": x.activity, "billable": true, "rate_cents": 200_00,
	})
	drafts := x.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter})
	invID := str(t, items(t, drafts)[0], "id")
	x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/approve", nil)
	x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/send", map[string]any{"deliver": "file"})

	if r := initiate(cli, map[string]any{
		"processor_module_id": "pay-proc", "contact_id": contactID, "matter_id": matter,
		"amount_cents": 25_00, "destination": "operating",
	}); r.code != http.StatusForbidden {
		t.Fatalf("client operating without invoice: %d %s", r.code, r.body)
	}
	if r := initiate(cli, map[string]any{
		"processor_module_id": "pay-proc", "contact_id": otherContact, "matter_id": matter,
		"amount_cents": 25_00, "destination": "operating", "invoice_id": invID,
	}); r.code != http.StatusNotFound && r.code != http.StatusForbidden {
		t.Fatalf("other contact: %d %s", r.code, r.body)
	}
	if r := initiate(cli, map[string]any{
		"processor_module_id": "pay-proc", "contact_id": contactID, "matter_id": other,
		"amount_cents": 25_00, "destination": "operating", "invoice_id": invID,
	}); r.code != http.StatusNotFound && r.code != http.StatusForbidden {
		t.Fatalf("other matter: %d %s", r.code, r.body)
	}

	r := initiate(cli, map[string]any{
		"processor_module_id": "pay-proc", "contact_id": contactID, "matter_id": matter,
		"amount_cents": 25_00, "destination": "operating", "invoice_id": invID,
	})
	if r.code != http.StatusCreated || r.json["status"] != "authorized" {
		t.Fatalf("client operating initiate: %d %s", r.code, r.body)
	}
	opPay := str(t, r.json, "payment_id")
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	_, opGrant := inbox.last(t)
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("pay-proc").Secrets["togavsu_api_token"]
	r = bot.do("POST", "/api/v1/payments", map[string]any{
		"contact_id": contactID, "matter_id": matter, "amount_cents": 25_00, "destination": "operating",
		"invoice_id": invID, "method": "card", "reference_number": "ch_cli",
	}, modules.TaskAuthorizationHeader, opGrant)
	if r.code != http.StatusCreated || r.json["id"] != opPay || r.json["source"] != "pay-proc" {
		t.Fatalf("client-authorized operating payment: %d %s", r.code, r.body)
	}

	// Trust destination under a client-authorized grant posts via the system principal.
	r = initiate(cli, map[string]any{
		"processor_module_id": "pay-proc", "contact_id": contactID, "matter_id": matter,
		"amount_cents": 40_00, "destination": "trust", "trust_account_id": x.account,
	})
	if r.code != http.StatusCreated {
		t.Fatalf("client trust initiate: %d %s", r.code, r.body)
	}
	trustPay := str(t, r.json, "payment_id")
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	_, trustGrant := inbox.last(t)
	r = bot.do("POST", "/api/v1/payments", map[string]any{
		"contact_id": contactID, "matter_id": matter, "amount_cents": 40_00, "destination": "trust",
		"trust_account_id": x.account, "method": "ach", "reference_number": "ach_cli",
	}, modules.TaskAuthorizationHeader, trustGrant)
	if r.code != http.StatusCreated || r.json["id"] != trustPay || r.json["trust_entry_id"] == nil {
		t.Fatalf("client-authorized trust payment: %d %s", r.code, r.body)
	}

	// Replenishment link: completing the trust payment satisfies the request.
	r = initiate(cli, map[string]any{
		"processor_module_id": "pay-proc", "contact_id": contactID, "matter_id": matter,
		"amount_cents": 50_00, "destination": "trust", "trust_account_id": x.account,
		"replenishment_request_id": replID,
	})
	if r.code != http.StatusCreated {
		t.Fatalf("replenishment initiate: %d %s", r.code, r.body)
	}
	replPay := str(t, r.json, "payment_id")
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	_, replGrant := inbox.last(t)
	r = bot.do("POST", "/api/v1/payments", map[string]any{
		"contact_id": contactID, "matter_id": matter, "amount_cents": 50_00, "destination": "trust",
		"trust_account_id": x.account, "method": "ach", "replenishment_request_id": replID,
	}, modules.TaskAuthorizationHeader, replGrant)
	if r.code != http.StatusCreated || r.json["id"] != replPay {
		t.Fatalf("replenishment payment: %d %s", r.code, r.body)
	}
	got := x.admin.must(http.StatusOK, "GET", "/api/v1/trust-replenishment-requests/"+replID, nil)
	if got.json["status"] != "satisfied" {
		t.Fatalf("replenishment status: %s", got.body)
	}
	if n := x.count(`SELECT COUNT(*) FROM core.events WHERE action = 'trust.replenishment_satisfied' AND record_id = $1`, replID); n != 1 {
		t.Fatalf("trust.replenishment_satisfied events: %d", n)
	}

	// Pending grant lifetime: past expires_at but within processor_pending_grant_days.
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.processor_authorization_lifetime", map[string]any{"value": "1m0s"})
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/payments.processor_pending_grant_days", map[string]any{"value": 2})
	r = initiate(cli, map[string]any{
		"processor_module_id": "pay-proc", "contact_id": contactID, "matter_id": matter,
		"amount_cents": 15_00, "destination": "trust", "trust_account_id": x.account,
	})
	if r.code != http.StatusCreated {
		t.Fatalf("pending initiate: %d %s", r.code, r.body)
	}
	pendPay := str(t, r.json, "payment_id")
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	_, pendGrant := inbox.last(t)
	r = bot.do("POST", "/api/v1/payments", map[string]any{
		"contact_id": contactID, "matter_id": matter, "amount_cents": 15_00, "destination": "trust",
		"trust_account_id": x.account, "method": "ach", "status": "pending",
	}, modules.TaskAuthorizationHeader, pendGrant)
	if r.code != http.StatusCreated || r.json["status"] != "pending" {
		t.Fatalf("pending payment: %d %s", r.code, r.body)
	}
	skew.Store(int64(2 * time.Minute)) // past authorization lifetime
	r = bot.do("POST", "/api/v1/payments/"+pendPay+"/status", map[string]any{"status": "completed"}, modules.TaskAuthorizationHeader, pendGrant)
	if r.code != http.StatusOK || r.json["status"] != "completed" {
		t.Fatalf("complete under pending grant days: %d %s", r.code, r.body)
	}

	r = initiate(cli, map[string]any{
		"processor_module_id": "pay-proc", "contact_id": contactID, "matter_id": matter,
		"amount_cents": 12_00, "destination": "trust", "trust_account_id": x.account,
	})
	if r.code != http.StatusCreated {
		t.Fatalf("expired pending initiate: %d %s", r.code, r.body)
	}
	expPay := str(t, r.json, "payment_id")
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	_, expGrant := inbox.last(t)
	skew.Store(0)
	r = bot.do("POST", "/api/v1/payments", map[string]any{
		"contact_id": contactID, "matter_id": matter, "amount_cents": 12_00, "destination": "trust",
		"trust_account_id": x.account, "method": "ach", "status": "pending",
	}, modules.TaskAuthorizationHeader, expGrant)
	if r.code != http.StatusCreated {
		t.Fatalf("second pending: %d %s", r.code, r.body)
	}
	skew.Store(int64(3 * 24 * time.Hour)) // past pending grant days
	if r := bot.do("POST", "/api/v1/payments/"+expPay+"/status", map[string]any{"status": "completed"}, modules.TaskAuthorizationHeader, expGrant); r.code != http.StatusForbidden {
		t.Fatalf("past pending grant days: %d %s", r.code, r.body)
	}
}
