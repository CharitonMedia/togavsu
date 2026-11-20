package coretest_test

import (
	"net/http"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2GTargetedCoreNamespace(t *testing.T) {
	c := coretest.New(t)
	const contract = "fee_agreement.signature.v1"
	for _, id := range []string{"namespace-provider", "namespace-consumer"} {
		c.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	}
	install2d(t, c, "namespace-provider", []string{"contracts:provide"}, "provides:\n  - contract: "+contract+"\n    operations:\n      - name: sign\n        path: /sign\n")
	install2d(t, c, "namespace-consumer", []string{"contracts:call"}, "consumes:\n  - contract: "+contract+"\n")
	c.Admin.Must(201, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": contract, "provider_id": "namespace-provider", "consumer_id": "namespace-consumer"})
	bot := c.ModuleClient("namespace-provider")
	path := "/api/v1/modules/contracts/" + contract + "/events"
	// Matching the targeted contract prefix cannot bypass the reserved core root.
	event := map[string]any{"contract": contract, "action": contract + ".executed", "id": "d210ad4a-3c92-4ed1-9684-8fb164155762", "consumer_id": "namespace-consumer", "occurred_at": "2026-11-20T12:00:00Z", "payload": map[string]any{}}
	result := bot.Do("POST", path, event)
	if result.Code != 400 || result.JSON["code"] != "validation" || result.JSON["field"] != "action" {
		t.Fatalf("targeted core root: HTTP %d %s, want action validation", result.Code, result.Body)
	}
	if n := c.Count("SELECT COUNT(*) FROM core.module_deliveries WHERE module_id='namespace-consumer'"); n != 0 {
		t.Errorf("queued %d core-root contract deliveries", n)
	}
}
