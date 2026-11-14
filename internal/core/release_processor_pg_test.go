package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

const extProcessorManifest = `id: pay-proc
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
  - scope: events:read
events:
  - payment.processor_authorized
`

// processorInbox is the processor module's event endpoint: it keeps each delivery's body
// and grant header.
type processorInbox struct {
	mu     sync.Mutex
	bodies []map[string]any
	grants []string
}

func (p *processorInbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	p.mu.Lock()
	p.bodies = append(p.bodies, body)
	p.grants = append(p.grants, r.Header.Get(modules.TaskAuthorizationHeader))
	p.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (p *processorInbox) last(t *testing.T) (map[string]any, string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bodies) == 0 {
		t.Fatal("the processor received no delivery")
	}
	return p.bodies[len(p.bodies)-1], p.grants[len(p.grants)-1]
}

// TestReleaseProcessorInitialization is initiateProcessorPayment (AC-002) on the running
// core: a staff session authorizes a registered processor for one payment; the core
// reserves the payment id, writes no payment and no trust row, and delivers the grant to
// that module only. The processor then records exactly that payment with the grant and no
// viewer, and completes it; a mismatched, reused, expired or foreign grant is refused.
// Unregistered modules, walls and missing scopes are refused before any grant is issued.
func TestReleaseProcessorInitialization(t *testing.T) {
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
	matter, clientID := x.matter("Estate of Pell", "", nil)
	walled, walledClient := x.matter("Estate of Rook", "", nil)
	x.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{x.adminID, x.attyID}})
	initiate := func(c *client, body map[string]any) resp {
		return c.do("POST", "/api/v1/payments/processor-initializations", body)
	}
	operating := func(m, contact string, cents int64) map[string]any {
		return map[string]any{"processor_module_id": "pay-proc", "contact_id": contact, "matter_id": m, "amount_cents": cents, "destination": "operating"}
	}
	payments := func() int { return x.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'payment'`) }
	grants := func() int {
		return x.count(`SELECT COUNT(*) FROM core.module_deliveries WHERE action = 'payment.processor_authorized'`)
	}

	// Refusals issue nothing.
	unregistered := operating(matter, clientID, 50_00)
	unregistered["processor_module_id"] = "nope-proc"
	if r := initiate(x.staff, unregistered); r.code != http.StatusForbidden {
		t.Fatalf("unregistered processor: %d %s", r.code, r.body)
	}
	if r := initiate(x.staff, operating(walled, walledClient, 50_00)); r.code != http.StatusNotFound {
		t.Fatalf("walled matter: %d %s", r.code, r.body)
	}
	if r := initiate(x.acct, operating(matter, clientID, 50_00)); r.code != http.StatusForbidden {
		t.Fatalf("accountant: %d %s", r.code, r.body)
	}
	if grants() != 0 {
		t.Fatal("a refused initialization queued a grant")
	}

	// A staff session authorizes the processor.
	r := initiate(x.staff, operating(matter, clientID, 120_00))
	if r.code != http.StatusCreated || r.json["status"] != "authorized" || r.json["destination"] != "operating" || r.json["trust_account_id"] != nil {
		t.Fatalf("initiate: %d %s", r.code, r.body)
	}
	if strings.Contains(r.body, "tpa1.") || strings.Contains(r.body, "secret") {
		t.Fatalf("the initiation response carries the grant: %s", r.body)
	}
	payID, initID := str(t, r.json, "payment_id"), str(t, r.json, "id")
	if payments() != 0 {
		t.Fatal("initiation wrote a payment")
	}
	if n := x.count(`SELECT COUNT(*) FROM core.events WHERE action = 'payment.processor_authorized' AND record_id = $1`, initID); n != 1 {
		t.Fatalf("payment.processor_authorized events: %d", n)
	}
	// The fan-out never queues it to another subscriber; only the direct delivery exists.
	if _, err := x.app.Modules.Pump(ctx); err != nil {
		t.Fatal(err)
	}
	if grants() != 1 {
		t.Fatalf("deliveries of the grant event: %d", grants())
	}
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	body, grant := inbox.last(t)
	auth, _ := body["processor_authorization"].(map[string]any)
	if auth == nil || auth["payment_id"] != payID || auth["initialization_id"] != initID || auth["issued_by"] != "core" || !strings.HasPrefix(grant, "tpa1.") {
		t.Fatalf("delivery: %v grant %q", body, grant)
	}

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("pay-proc").Secrets["togavsu_api_token"]
	pay := map[string]any{"contact_id": clientID, "matter_id": matter, "amount_cents": 120_00, "destination": "operating", "method": "card", "reference_number": "ch_1"}
	if r := bot.do("POST", "/api/v1/payments", pay); r.code != http.StatusForbidden {
		t.Fatalf("processor payment with no grant: %d %s", r.code, r.body)
	}
	if r := x.staff.do("POST", "/api/v1/payments", pay, modules.TaskAuthorizationHeader, grant); r.code != http.StatusForbidden {
		t.Fatalf("a staff session presenting the grant: %d %s", r.code, r.body)
	}
	wrongAmount := map[string]any{}
	for k, v := range pay {
		wrongAmount[k] = v
	}
	wrongAmount["amount_cents"] = 999_00
	if r := bot.do("POST", "/api/v1/payments", wrongAmount, modules.TaskAuthorizationHeader, grant); r.code != http.StatusForbidden {
		t.Fatalf("amount outside the grant: %d %s", r.code, r.body)
	}
	other, otherClient := x.matter("Estate of Vale", "", nil)
	wrongMatter := map[string]any{"contact_id": otherClient, "matter_id": other, "amount_cents": 120_00, "destination": "operating", "method": "card"}
	if r := bot.do("POST", "/api/v1/payments", wrongMatter, modules.TaskAuthorizationHeader, grant); r.code != http.StatusNotFound {
		t.Fatalf("matter outside the grant: %d %s", r.code, r.body)
	}
	// Change a character well inside the grant's secret: the last base64 character
	// carries unused bits, so a swap there can decode to the same secret and pass.
	bad := []byte(grant)
	at := len(bad) - 10
	if bad[at] == 'A' {
		bad[at] = 'B'
	} else {
		bad[at] = 'A'
	}
	if r := bot.do("POST", "/api/v1/payments", pay, modules.TaskAuthorizationHeader, string(bad)); r.code != http.StatusForbidden {
		t.Fatalf("a forged grant: %d %s", r.code, r.body)
	}
	// A field outside the grant's allowed_fields is field_locked: the processor cannot
	// forge the payment's source, not even as its own id or another module's.
	for _, src := range []string{"manual", "other-module", "pay-proc"} {
		forged := map[string]any{"source": src}
		for k, v := range pay {
			forged[k] = v
		}
		if r := bot.do("POST", "/api/v1/payments", forged, modules.TaskAuthorizationHeader, grant); r.code != http.StatusForbidden || r.json["code"] != "field_locked" || r.json["field"] != "source" {
			t.Fatalf("source %q outside the grant: %d %s", src, r.code, r.body)
		}
	}
	if payments() != 0 {
		t.Fatal("a refused processor call wrote a payment")
	}
	r = bot.do("POST", "/api/v1/payments", pay, modules.TaskAuthorizationHeader, grant)
	if r.code != http.StatusCreated || r.json["id"] != payID || r.json["source"] != "pay-proc" || r.json["status"] != "completed" {
		t.Fatalf("granted payment: %d %s", r.code, r.body)
	}
	if again := bot.do("POST", "/api/v1/payments", pay, modules.TaskAuthorizationHeader, grant); again.code != http.StatusCreated || again.json["id"] != payID {
		t.Fatalf("repeat with the same fields: %d %s", again.code, again.body)
	}
	pay["reference_number"] = "ch_2"
	if r := bot.do("POST", "/api/v1/payments", pay, modules.TaskAuthorizationHeader, grant); r.code != http.StatusConflict {
		t.Fatalf("a second payment on one authorization: %d %s", r.code, r.body)
	}
	if payments() != 1 {
		t.Fatalf("payments after the processor: %d", payments())
	}

	// A pending trust payment completes under its grant; trust posts once.
	r = initiate(x.staff, map[string]any{"processor_module_id": "pay-proc", "contact_id": clientID, "matter_id": matter, "amount_cents": 75_00,
		"destination": "trust", "trust_account_id": x.account})
	if r.code != http.StatusCreated || r.json["trust_account_id"] != x.account {
		t.Fatalf("trust initiation: %d %s", r.code, r.body)
	}
	trustPay := str(t, r.json, "payment_id")
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	_, trustGrant := inbox.last(t)
	r = bot.do("POST", "/api/v1/payments", map[string]any{"contact_id": clientID, "matter_id": matter, "amount_cents": 75_00, "destination": "trust",
		"trust_account_id": x.account, "method": "ach", "status": "pending"}, modules.TaskAuthorizationHeader, trustGrant)
	if r.code != http.StatusCreated || r.json["id"] != trustPay || r.json["trust_entry_id"] != nil {
		t.Fatalf("pending trust payment: %d %s", r.code, r.body)
	}
	if r := bot.do("POST", "/api/v1/payments/"+payID+"/status", map[string]any{"status": "failed"}, modules.TaskAuthorizationHeader, trustGrant); r.code != http.StatusNotFound {
		t.Fatalf("status of a payment outside the grant: %d %s", r.code, r.body)
	}
	r = bot.do("POST", "/api/v1/payments/"+trustPay+"/status", map[string]any{"status": "completed"}, modules.TaskAuthorizationHeader, trustGrant)
	if r.code != http.StatusOK || r.json["status"] != "completed" || r.json["trust_entry_id"] == nil {
		t.Fatalf("completing the trust payment: %d %s", r.code, r.body)
	}

	// A grant expires after billing.processor_authorization_lifetime.
	r = initiate(x.atty, operating(matter, clientID, 10_00))
	if r.code != http.StatusCreated {
		t.Fatalf("attorney initiation: %d %s", r.code, r.body)
	}
	if _, err := x.app.Modules.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	_, late := inbox.last(t)
	skew.Store(int64(31 * time.Minute))
	if r := bot.do("POST", "/api/v1/payments", map[string]any{"contact_id": clientID, "matter_id": matter, "amount_cents": 10_00, "destination": "operating", "method": "card"},
		modules.TaskAuthorizationHeader, late); r.code != http.StatusForbidden {
		t.Fatalf("expired grant: %d %s", r.code, r.body)
	}
	skew.Store(0)

	// Without trust:write approved for the module, a trust authorization is refused.
	x.rt.Handle("pay-lite", &processorInbox{})
	lite := strings.NewReplacer("id: pay-proc", "id: pay-lite", "  - scope: trust:write\n", "").Replace(extProcessorManifest)
	if _, problem := x.tabForm(x.admin, "install", nil, x.build(onlyManifest(lite))); problem != "" {
		t.Fatalf("install pay-lite: %s", problem)
	}
	x.approveOnTab("pay-lite")
	before := grants()
	if r := initiate(x.staff, map[string]any{"processor_module_id": "pay-lite", "contact_id": clientID, "matter_id": matter, "amount_cents": 5_00,
		"destination": "trust", "trust_account_id": x.account}); r.code != http.StatusForbidden {
		t.Fatalf("trust without the module's trust:write: %d %s", r.code, r.body)
	}
	// A token without trust:write cannot authorize a trust payment either.
	tok := newClient(t, x.srv.URL)
	tok.bearer = str(t, x.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{
		"name": "payer", "owner_user_id": x.staffID, "scopes": []string{"payments:write"}}).json, "secret")
	if r := initiate(tok, map[string]any{"processor_module_id": "pay-proc", "contact_id": clientID, "matter_id": matter, "amount_cents": 5_00,
		"destination": "trust", "trust_account_id": x.account}); r.code != http.StatusForbidden {
		t.Fatalf("token without trust:write: %d %s", r.code, r.body)
	}
	if grants() != before {
		t.Fatal("a refused trust authorization queued a grant")
	}
	liteBot := newClient(t, x.srv.URL)
	liteBot.bearer = x.rt.Last("pay-lite").Secrets["togavsu_api_token"]
	if r := liteBot.do("POST", "/api/v1/payments", map[string]any{"contact_id": clientID, "matter_id": matter, "amount_cents": 10_00, "destination": "operating", "method": "card"},
		modules.TaskAuthorizationHeader, late); r.code != http.StatusForbidden {
		t.Fatalf("another module presenting the grant: %d %s", r.code, r.body)
	}

	// The same operation through /mcp.
	rpc := tok.must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": "initiateProcessorPayment", "arguments": map[string]any{"body": operating(matter, clientID, 15_00)}}})
	res, _ := rpc.json["result"].(map[string]any)
	sc, _ := res["structuredContent"].(map[string]any)
	if res["isError"] != false || sc["status"] != "authorized" {
		t.Fatalf("initiateProcessorPayment over MCP: %s", rpc.body)
	}
}
