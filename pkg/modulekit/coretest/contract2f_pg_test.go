package coretest_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2FDocumentGeneratedContractEnvelope(t *testing.T) {
	c := coretest.New(t)
	const contract = "documents.generate.v1"
	const provider = "synthetic-documents"
	const consumer = "synthetic-intake"
	var mu sync.Mutex
	var events []map[string]any
	c.Serve(consumer, func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sender, body, err := coretest.ReadDelivery(m, r)
			if err != nil {
				t.Error(err)
				w.WriteHeader(401)
				return
			}
			var v map[string]any
			if err := json.Unmarshal(body, &v); err != nil {
				t.Error(err)
			}
			if v["contract"] == contract {
				if sender != provider {
					t.Errorf("generated sender %q", sender)
				}
				mu.Lock()
				events = append(events, v)
				mu.Unlock()
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	install2d(t, c, provider, []string{"contracts:provide"}, "provides:\n  - contract: "+contract+"\n    operations:\n      - name: generate\n        path: /generate\n")
	install2d(t, c, consumer, []string{"contracts:call"}, "consumes:\n  - contract: "+contract+"\n")
	c.Admin.Must(http.StatusCreated, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": contract, "provider_id": provider, "consumer_id": consumer})
	envelope := map[string]any{"id": "synthetic-generated-event", "contract": contract, "consumer_id": consumer, "occurred_at": time.Now().UTC().Format(time.RFC3339Nano), "action": "document.generated", "payload": map[string]any{"file_id": "synthetic-file", "request_id": "synthetic-request", "sender": "untrusted-claim"}}
	bot := c.ModuleClient(provider)
	bot.Must(http.StatusNoContent, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope)
	bot.Must(http.StatusNoContent, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope)
	c.DeliverEvents()
	mu.Lock()
	if len(events) != 1 || !reflect.DeepEqual(events[0], envelope) {
		t.Errorf("generated envelope changed/duplicate: %#v", events)
	}
	mu.Unlock()
	envelope["id"] = "synthetic-core-spoof"
	envelope["action"] = "file.trashed"
	bot.Must(http.StatusBadRequest, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope)
	envelope["action"] = "document.generated"
	envelope["consumer_id"] = "unlinked-consumer"
	bot.Must(http.StatusForbidden, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope)
	// The exception belongs to this contract only; arbitrary providers cannot emit it.
	install2d(t, c, "synthetic-other-provider", []string{"contracts:provide"}, "provides:\n  - contract: other.generate.v1\n    operations:\n      - name: generate\n        path: /generate\n")
	envelope["contract"] = "other.generate.v1"
	c.ModuleClient("synthetic-other-provider").Must(http.StatusBadRequest, "POST", "/api/v1/modules/contracts/other.generate.v1/events", envelope)
}
