package coretest_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2GNonMoneyPaymentMilestoneReadable(t *testing.T) {
	c := coretest.New(t)
	const id = "nonmoney-events"
	var mu sync.Mutex
	received := map[string]int{}
	c.Serve(id, func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, raw, err := coretest.ReadDelivery(m, r)
			if err != nil {
				t.Error(err)
				w.WriteHeader(401)
				return
			}
			var ev struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			received[ev.ID]++
			mu.Unlock()
			w.WriteHeader(204)
		})
	})
	install2d(t, c, id, []string{"events:write", "payments:read"}, "", func(files map[string][]byte) {
		files[modulekit.ManifestFile] = []byte(strings.Replace(string(files[modulekit.ManifestFile]), "events:\n", "events:\n  - payment.*\n  - synthetic.*\n", 1))
	})
	bot := c.ModuleClient(id)
	appended := bot.Must(201, "POST", "/api/v1/events", map[string]any{"action": "payment.future_event", "record_type": "fee_agreement", "record_id": "synthetic-fee", "matter_id": c.Open, "summary": "Synthetic future signing milestone"})
	eventID := str(t, appended.JSON, "id")
	hiddenMilestone := c.Attorney.Must(201, "POST", "/api/v1/events", map[string]any{"action": "payment.future_event", "record_type": "fee_agreement", "record_id": "synthetic-walled-fee", "matter_id": c.Walled, "summary": "Synthetic walled signing milestone"})
	parties := c.Attorney.Must(200, "GET", "/api/v1/matter-parties?matter_id="+c.Walled, nil).JSON["data"].([]any)
	contactID := parties[0].(map[string]any)["contact_id"]
	payment := c.Attorney.Must(201, "POST", "/api/v1/payments", map[string]any{"contact_id": contactID, "matter_id": c.Walled, "amount_cents": 100, "destination": "operating", "method": "cash"})
	paymentID := str(t, payment.JSON, "id")
	// Even a non-payment action on a real money record must retain payment gating.
	disguised := c.Admin.Must(201, "POST", "/api/v1/events", map[string]any{"action": "synthetic.payment_changed", "record_type": "payment", "record_id": paymentID, "summary": "Synthetic money record without matter"})
	for _, who := range []*coretest.User{c.Admin, c.Attorney} {
		allowed := who == c.Attorney
		for _, surface := range []string{"/practice/activity", "/api/v1/events?limit=200"} {
			r := who.Must(200, "GET", surface, nil)
			if !strings.Contains(r.Body, "Synthetic future signing milestone") {
				t.Errorf("%s hides appended non-money milestone for %s", surface, who.ID)
			}
			for _, marker := range []string{"Synthetic walled signing milestone", "payment.created", "Synthetic money record without matter"} {
				if strings.Contains(r.Body, marker) != allowed {
					t.Errorf("%s visibility of %s for %s want=%v", surface, marker, who.ID, allowed)
				}
			}
		}
		v := toolResult2d(t, who.Client, "listEvents", map[string]any{"query": map[string]any{"limit": 200}}, false)

		seen := map[string]bool{}
		for _, row := range v["data"].([]any) {
			seen[str(t, row.(map[string]any), "id")] = true
		}
		if !seen[eventID] {
			t.Errorf("MCP hides appended non-money milestone for %s", who.ID)
		}
		for _, marker := range []string{str(t, hiddenMilestone.JSON, "id"), str(t, disguised.JSON, "id")} {
			if seen[marker] != allowed {
				t.Errorf("MCP event %s visible for %s want=%v", marker, who.ID, allowed)
			}
		}
		for _, row := range v["data"].([]any) {
			event := row.(map[string]any)
			if event["action"] == "payment.created" && event["record_id"] == paymentID && !allowed {
				t.Error("MCP leaked walled payment.created")
			}
		}
	}
	c.DeliverEvents()
	mu.Lock()
	defer mu.Unlock()
	if received[eventID] != 1 {
		t.Errorf("non-money delivery count=%d want=1", received[eventID])
	}
	if received[str(t, disguised.JSON, "id")] != 0 {
		t.Error("walled event delivered")
	}
	if n := c.Count("SELECT COUNT(*) FROM core.module_deliveries WHERE module_id=$1 AND action='payment.created' AND payload LIKE $2", id, "%"+paymentID+"%"); n != 0 {
		t.Errorf("queued %d walled payment.created events", n)
	}
}
