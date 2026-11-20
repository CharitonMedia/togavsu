package coretest_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func processorFixture(t *testing.T, overrides ...func(coretest.Module, http.Handler) http.Handler) (*coretest.Core, *coretest.Processor, *coretest.Client, string) {
	t.Helper()
	c := coretest.New(t)
	processor := coretest.NewProcessor()
	c.Serve("synthetic-processor", func(m coretest.Module) http.Handler {
		h := processor.Handler(m)
		for _, override := range overrides {
			h = override(m, h)
		}
		return h
	})
	c.Handle("synthetic-intake", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	install2d(t, c, "synthetic-processor", []string{"contracts:provide", "payments:write"}, "payment_processor:\n  destinations:\n    - operating\nprovides:\n  - contract: processor.payment.v1\n    operations:\n      - name: authorize\n        path: /authorize\n      - name: recover\n        path: /recover\n", func(files map[string][]byte) {
		files[modulekit.ManifestFile] = []byte(strings.Replace(string(files[modulekit.ManifestFile]), "events:\n", "events:\n  - payment.processor_authorized\n", 1))
	})
	install2d(t, c, "synthetic-intake", []string{"contracts:call", "payments:write", "matters:write"}, "consumes:\n  - contract: processor.payment.v1\n")
	link := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": modulekit.ProcessorPaymentContract, "consumer_id": "synthetic-intake", "provider_id": "synthetic-processor"})
	return c, processor, c.ModuleClient("synthetic-intake"), str(t, link.JSON, "id")
}

func processorRequest(intent string) modulekit.ProcessorPaymentWrite {
	return modulekit.ProcessorPaymentWrite{IntentID: intent, AmountCents: 2500, Destination: "operating", PaymentToken: "synthetic-hosted-field-placeholder"}
}

func TestCoreUpdate2FProcessorDeclineBeforeMatterAndApprovalRecovery(t *testing.T) {
	c, processor, consumer, link := processorFixture(t)
	path := "/api/v1/modules/contracts/" + modulekit.ProcessorPaymentContract + "/"
	beforeMatters := c.Count(`SELECT COUNT(*) FROM core.matters`)
	beforePayments := c.Count(`SELECT COUNT(*) FROM core.payments`)
	beforeContacts := c.Count(`SELECT COUNT(*) FROM core.contacts`)
	continueApproved := func(result coretest.Resp) string {
		t.Helper()
		if result.JSON["status"] != "approved" {
			return ""
		}
		matter := consumer.Do("POST", "/api/v1/matters", map[string]any{
			"name": "Synthetic approved paid intake", "practice_area_id": c.PracticeArea, "status": "prospect",
		}, "Idempotency-Key", "paid-intake/lost-approval/matter")
		if matter.Code != http.StatusCreated {
			t.Fatalf("approved continuation: %d %s", matter.Code, matter.Body)
		}
		return str(t, matter.JSON, "id")
	}
	processor.DeclineNext("card_declined")
	decline := consumer.Must(http.StatusOK, "POST", path+"authorize", processorRequest("declined-intake"))
	continueApproved(decline)
	if decline.JSON["status"] != "declined" || decline.JSON["decline_code"] != "card_declined" || processor.Charges() != 0 {
		t.Fatalf("declined intake: %s; charges %d", decline.Body, processor.Charges())
	}
	consumer.Must(http.StatusOK, "POST", path+"authorize", processorRequest("declined-intake"))
	if len(processor.Calls()) != 1 {
		t.Fatal("a declined receipt authorized again")
	}
	if c.Count(`SELECT COUNT(*) FROM core.matters`) != beforeMatters || c.Count(`SELECT COUNT(*) FROM core.payments`) != beforePayments {
		t.Fatal("a declined payment wrote a matter or payment")
	}
	processor.LoseNextApprovalResponse()
	pending := consumer.Must(http.StatusAccepted, "POST", path+"authorize", processorRequest("lost-approval"))
	if pending.JSON["status"] != "pending" || processor.Charges() != 1 {
		t.Fatalf("lost approval: %s; charges %d", pending.Body, processor.Charges())
	}
	continueApproved(pending)
	if c.Count(`SELECT COUNT(*) FROM core.matters`) != beforeMatters || c.Count(`SELECT COUNT(*) FROM core.contacts`) != beforeContacts {
		t.Fatal("decline or pending continuation created core practice records")
	}
	// A service call without a signed-in viewer recovers the original approval.
	approved := consumer.Must(http.StatusOK, "POST", path+"recover", map[string]string{"intent_id": "lost-approval"})
	if approved.JSON["status"] != "approved" || approved.JSON["reference_number"] != "synthetic-approval-1" {
		t.Fatalf("recovery: %s", approved.Body)
	}
	firstMatter := continueApproved(approved)
	if secondMatter := continueApproved(approved); firstMatter == "" || secondMatter != firstMatter || c.Count(`SELECT COUNT(*) FROM core.matters`) != beforeMatters+1 {
		t.Fatal("recovered approval did not create exactly one idempotent intake matter")
	}
	consumer.Must(http.StatusOK, "POST", path+"authorize", processorRequest("lost-approval"))
	consumer.Must(http.StatusOK, "POST", path+"recover", map[string]string{"intent_id": "lost-approval"})
	calls := processor.Calls()
	if len(calls) != 3 || calls[1].Operation != "authorize" || calls[2].Operation != "recover" || calls[2].ConsumerID != "synthetic-intake" || processor.Charges() != 1 {
		t.Fatalf("approval recovery reauthorized or sender changed: %#v; charges %d", calls, processor.Charges())
	}
	changed := processorRequest("lost-approval")
	changed.AmountCents++
	conflict := consumer.Must(http.StatusConflict, "POST", path+"authorize", changed)
	if conflict.JSON["code"] != "idempotency_conflict" || len(processor.Calls()) != 3 {
		t.Fatalf("mutable approval: %s", conflict.Body)
	}
	changed = processorRequest("lost-approval")
	changed.PaymentToken = "different-synthetic-placeholder"
	consumer.Must(http.StatusConflict, "POST", path+"authorize", changed)
	// Stored approval cannot outlive the admin-approved link as a callable capability.
	c.Admin.Must(http.StatusNoContent, "DELETE", "/api/v1/modules/contract-links/"+link, nil)
	consumer.Must(http.StatusConflict, "POST", path+"recover", map[string]string{"intent_id": "lost-approval"})
	if len(processor.Calls()) != 3 {
		t.Fatal("unlink did not stop recovery")
	}
	if c.Count(`SELECT COUNT(*) FROM core.matters`) != beforeMatters+1 || c.Count(`SELECT COUNT(*) FROM core.contacts`) != beforeContacts || c.Count(`SELECT COUNT(*) FROM core.payments`) != beforePayments {
		t.Fatal("broker wrote outside the single approved consumer continuation")
	}
	if c.Count(`SELECT COUNT(*) FROM core.core_idempotency WHERE operation='processor.payment.v1' AND response LIKE '%synthetic-hosted-field-placeholder%'`) != 0 {
		t.Fatal("a hosted-field token was persisted")
	}
	if c.Count(`SELECT COUNT(*) FROM core.audit_log WHERE entity='module_processor_intent' AND source='service' AND reason LIKE '%module=synthetic-intake%'`) == 0 {
		t.Fatal("processor receipt must audit the module with source service")
	}
}

func TestCoreUpdate2FProcessorConcurrentAndInvalidRequests(t *testing.T) {
	c, processor, consumer, _ := processorFixture(t)
	path := "/api/v1/modules/contracts/" + modulekit.ProcessorPaymentContract + "/"
	for _, body := range []any{
		map[string]any{"intent_id": "bad", "amount_cents": 0, "destination": "operating", "payment_token": "synthetic"},
		map[string]any{"intent_id": "bad", "amount_cents": 1, "destination": "trust", "payment_token": "synthetic"},
		map[string]any{"intent_id": "bad", "amount_cents": 1, "destination": "operating", "payment_token": ""},
		map[string]any{"intent_id": "bad", "amount_cents": 1, "destination": "operating", "payment_token": "synthetic", "matter_id": c.Open},
		processorRequest("../bad"),
	} {
		consumer.Must(http.StatusBadRequest, "POST", path+"authorize", body)
	}
	consumer.Must(http.StatusNotFound, "POST", path+"recover", map[string]string{"intent_id": "unknown-intent"})
	if len(processor.Calls()) != 0 {
		t.Fatal("invalid request reached processor")
	}
	for _, scopes := range [][]string{{"contracts:call"}, {"payments:write"}} {
		token := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{
			"name": "narrow processor bearer", "module_id": "synthetic-intake", "scopes": scopes,
			"approved_scopes": []string{"contracts:call", "payments:write"},
		})
		narrow := c.NewClient()
		narrow.Bearer = str(t, token.JSON, "secret")
		denied := narrow.Must(http.StatusForbidden, "POST", path+"authorize", processorRequest("narrow"))
		if denied.JSON["code"] != "scope_denied" {
			t.Fatalf("narrow bearer: %s", denied.Body)
		}
		mcp := toolResult2d(t, narrow, "callModuleContract", map[string]any{
			"path": map[string]any{"contract": modulekit.ProcessorPaymentContract, "operation": "authorize"},
			"body": processorRequest("narrow"),
		}, true)
		if mcp["code"] != "scope_denied" {
			t.Fatalf("narrow bearer MCP: %#v", mcp)
		}
	}
	if len(processor.Calls()) != 0 || c.Count(`SELECT COUNT(*) FROM core.core_idempotency WHERE operation='processor.payment.v1'`) != 0 {
		t.Fatal("scope denial reached processor or persisted an intent")
	}
	var wg sync.WaitGroup
	responses := make(chan coretest.Resp, 4)
	for i := 0; i < cap(responses); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- consumer.Do("POST", path+"authorize", processorRequest("concurrent-intent"))
		}()
	}
	wg.Wait()
	close(responses)
	for response := range responses {
		if response.Code != http.StatusOK && response.Code != http.StatusAccepted || response.Code == http.StatusOK && response.JSON["reference_number"] != "synthetic-approval-1" {
			t.Fatalf("concurrent approval: %d %s", response.Code, response.Body)
		}
	}
	authorizations := 0
	for _, call := range processor.Calls() {
		if call.Operation == "authorize" {
			authorizations++
		}
	}
	if processor.Charges() != 1 || authorizations != 1 {
		t.Fatalf("concurrent charges %d calls %#v", processor.Charges(), processor.Calls())
	}
	consumer.Must(http.StatusOK, "POST", path+"recover", map[string]string{"intent_id": "concurrent-intent"})
	// The same contract is exposed over MCP with the normal conditional scopes.
	rpc := consumer.Must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": "recover", "method": "tools/call", "params": map[string]any{"name": "callModuleContract", "arguments": map[string]any{"path": map[string]any{"contract": modulekit.ProcessorPaymentContract, "operation": "recover"}, "body": map[string]any{"intent_id": "concurrent-intent"}}}})
	if strings.Contains(rpc.Body, `"isError":true`) || !strings.Contains(rpc.Body, "synthetic-approval-1") {
		t.Fatalf("processor MCP recovery: %s", rpc.Body)
	}
	// A different consumer's matching intent is isolated by signed consumer identity.
	c.Handle("other-intake", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	install2d(t, c, "other-intake", []string{"contracts:call", "payments:write"}, "consumes:\n  - contract: processor.payment.v1\n")
	c.Admin.Must(http.StatusCreated, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": modulekit.ProcessorPaymentContract, "consumer_id": "other-intake", "provider_id": "synthetic-processor"})
	other := c.ModuleClient("other-intake")
	other.Must(http.StatusNotFound, "POST", path+"recover", map[string]string{"intent_id": "concurrent-intent"})
	approval := other.Must(http.StatusOK, "POST", path+"authorize", processorRequest("concurrent-intent"))
	if approval.JSON["reference_number"] != "synthetic-approval-2" || processor.Charges() != 2 {
		t.Fatalf("consumer isolation: %s", approval.Body)
	}
	for _, call := range processor.Calls() {
		if call.ConsumerID != "synthetic-intake" && call.ConsumerID != "other-intake" {
			t.Fatalf("unverified consumer %#v", call)
		}
	}
}

func TestCoreUpdate2FProcessorMalformedApprovalNeverReauthorizes(t *testing.T) {
	var authorize, recover atomic.Int32
	c, _, consumer, _ := processorFixture(t, func(m coretest.Module, _ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sender, body, err := coretest.ReadDelivery(m, r)
			if err != nil || sender != "synthetic-intake" {
				t.Errorf("invalid signed processor caller: %s %v", sender, err)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("X-Togavsu-Contract-Operation") == "recover" {
				recover.Add(1)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			authorize.Add(1)
			var in modulekit.ProcessorPaymentWrite
			_ = json.Unmarshal(body, &in)
			// It claims a differently priced approval. Core cannot trust this as a
			// paid intake and cannot infer that no external charge occurred.
			_ = json.NewEncoder(w).Encode(modulekit.ProcessorPaymentResult{
				IntentID: in.IntentID, Status: "approved", AmountCents: in.AmountCents + 1,
				Destination: in.Destination, ReferenceNumber: "synthetic-invalid-approval",
			})
		})
	})
	path := "/api/v1/modules/contracts/" + modulekit.ProcessorPaymentContract + "/"
	before := c.Count(`SELECT COUNT(*) FROM core.matters`)
	consumer.Must(http.StatusConflict, "POST", path+"authorize", processorRequest("malformed-approval"))
	for _, op := range []string{"recover", "authorize"} {
		var body any = map[string]string{"intent_id": "malformed-approval"}
		if op == "authorize" {
			body = processorRequest("malformed-approval")
		}
		pending := consumer.Must(http.StatusAccepted, "POST", path+op, body)
		if pending.JSON["status"] != "pending" {
			t.Fatalf("unknown outcome was promoted to approval: %s", pending.Body)
		}
	}
	if authorize.Load() != 1 || recover.Load() != 2 || c.Count(`SELECT COUNT(*) FROM core.matters`) != before {
		t.Fatalf("ambiguous approval reauthorized: authorize=%d recover=%d", authorize.Load(), recover.Load())
	}
}

func TestCoreUpdate2FProcessorReceiptSurvivesProviderRestartAndAge(t *testing.T) {
	c, processor, consumer, _ := processorFixture(t)
	path := "/api/v1/modules/contracts/" + modulekit.ProcessorPaymentContract + "/"
	approved := consumer.Must(http.StatusOK, "POST", path+"authorize", processorRequest("durable-approval"))
	if approved.JSON["status"] != "approved" || processor.Charges() != 1 {
		t.Fatalf("initial approval: %s", approved.Body)
	}
	// Ordinary API idempotency retention cannot expire a financial receipt.
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE core.core_idempotency SET created_at=0 WHERE operation='processor.payment.v1'`); err != nil {
		t.Fatal(err)
	}
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/modules/synthetic-processor/disable", nil)
	restarted := coretest.NewProcessor()
	c.Serve("synthetic-processor", restarted.Handler)
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/modules/synthetic-processor/enable", nil)
	for _, op := range []string{"authorize", "recover"} {
		var body any = map[string]string{"intent_id": "durable-approval"}
		if op == "authorize" {
			body = processorRequest("durable-approval")
		}
		result := consumer.Must(http.StatusOK, "POST", path+op, body)
		if result.JSON["status"] != "approved" || result.JSON["reference_number"] != approved.JSON["reference_number"] {
			t.Fatalf("receipt lost after restart: %s", result.Body)
		}
	}
	if restarted.Charges() != 0 || len(restarted.Calls()) != 0 {
		t.Fatal("a durable terminal receipt called the restarted provider")
	}
}

func TestCoreUpdate2FProcessorConcurrentRecoveryCannotRegressApproval(t *testing.T) {
	authorizeStarted, recoverStarted := make(chan struct{}), make(chan struct{})
	releaseAuthorize, releaseRecover := make(chan struct{}), make(chan struct{})
	var authorizations atomic.Int32
	_, _, consumer, _ := processorFixture(t, func(m coretest.Module, _ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sender, _, err := coretest.ReadDelivery(m, r)
			if err != nil || sender != "synthetic-intake" {
				t.Errorf("processor caller: %s %v", sender, err)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			result := modulekit.ProcessorPaymentResult{IntentID: "inflight-approval", AmountCents: 2500, Destination: "operating"}
			if r.Header.Get("X-Togavsu-Contract-Operation") == "authorize" {
				authorizations.Add(1)
				close(authorizeStarted)
				<-releaseAuthorize
				result.Status, result.ReferenceNumber = "approved", "synthetic-inflight-approval"
			} else {
				close(recoverStarted)
				<-releaseRecover
				result.Status = "pending"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		})
	})
	path := "/api/v1/modules/contracts/" + modulekit.ProcessorPaymentContract + "/"
	authorized, recovered := make(chan coretest.Resp, 1), make(chan coretest.Resp, 1)
	go func() { authorized <- consumer.Do("POST", path+"authorize", processorRequest("inflight-approval")) }()
	<-authorizeStarted
	go func() {
		recovered <- consumer.Do("POST", path+"recover", map[string]string{"intent_id": "inflight-approval"})
	}()
	<-recoverStarted
	close(releaseAuthorize)
	approval := <-authorized
	close(releaseRecover)
	recovery := <-recovered
	for _, result := range []coretest.Resp{approval, recovery} {
		if result.Code != http.StatusOK || result.JSON["status"] != "approved" || result.JSON["reference_number"] != "synthetic-inflight-approval" {
			t.Fatalf("stale recovery regressed terminal approval: %d %s", result.Code, result.Body)
		}
	}
	if authorizations.Load() != 1 {
		t.Fatal("concurrent recovery reauthorized an intent")
	}
	consumer.Must(http.StatusOK, "POST", path+"recover", map[string]string{"intent_id": "inflight-approval"})
}
