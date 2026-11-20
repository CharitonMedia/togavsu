package coretest_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

// Keep the exact IDs from both approved PRDs, docs/modules and existing contract
// fixtures (zoom/importer), plus the shipped example. Source is not ownership.
func TestCoreUpdate2FFix2ApprovedModuleIDs(t *testing.T) {
	c := coretest.New(t)
	for _, id := range []string{"documents", "email", "client-portal", "lawpay-docuseal", "quickbooks", "intake", "discovery", "phone", "phone-zoom", "phone-google", "workflows", "google-notebooklm", "matter-wiki", "mo-estate-planning", "matter-clock", "zoom", "importer"} {
		t.Run(id, func(t *testing.T) {
			if !marketplace.ValidModuleID(id) {
				t.Errorf("approved module ID %q is invalid", id)
			}
			body := []byte(`{"synthetic":true}`)
			at := time.Now()
			h := modulekit.SignWebhookSenderHeaders("synthetic-secret", at, id, body)
			if sender, err := modulekit.VerifyWebhookSender("synthetic-secret", h, body, at, 0); err != nil || sender != id {
				t.Errorf("approved sender %q refused: %q %v", id, sender, err)
			}
			c.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			install2d(t, c, id, []string{"contracts:provide", "contracts:call"}, "provides:\n  - contract: synthetic."+id+".v1\n    operations:\n      - name: send\n        path: /send\nconsumes:\n  - contract: synthetic.email.v1\n")
			c.Admin.Must(http.StatusOK, "GET", "/api/v1/modules/"+id, nil)
		})
	}
	// Email must remain a valid endpoint of admin-approved contract links too.
	c.Admin.Must(http.StatusCreated, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": "synthetic.email.v1", "provider_id": "email", "consumer_id": "documents"})
}

func TestCoreUpdate2FFix2PurgedFileEvents(t *testing.T) {
	c := coretest.New(t)
	const module = "purge-listener"
	c.Wall(c.Walled, c.Admin, c.Attorney)
	var mu sync.Mutex
	received := map[string]map[string]int{}
	c.Serve(module, func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sender, body, err := coretest.ReadDelivery(m, r)
			if err != nil || sender != "core" {
				t.Errorf("sender %q: %v", sender, err)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var ev struct {
				Action   string `json:"action"`
				RecordID string `json:"record_id"`
			}
			if err := json.Unmarshal(body, &ev); err != nil {
				t.Error(err)
			}
			mu.Lock()
			if received[ev.RecordID] == nil {
				received[ev.RecordID] = map[string]int{}
			}
			received[ev.RecordID][ev.Action]++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})
	})
	install2d(t, c, module, []string{"files:read"}, "")
	for _, tc := range []struct {
		name, matter          string
		attorneyOnly, visible bool
	}{
		{"open", c.Open, false, true}, {"walled", c.Walled, false, false}, {"attorney", c.Open, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := stage2f(t, c.Attorney.Client, tc.matter, tc.name+".txt")
			f := c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": up, "attorney_only": tc.attorneyOnly})
			id := str(t, f.JSON, "id")
			c.Attorney.Must(http.StatusOK, "POST", "/api/v1/files/"+id+"/status", map[string]any{"status": "final"})
			c.Attorney.Must(http.StatusOK, "POST", "/api/v1/files/"+id+"/trash", map[string]any{})
			c.Admin.Must(http.StatusNoContent, "POST", "/api/v1/files/"+id+"/purge", map[string]any{"confirm_name": tc.name + ".txt"})
			c.Admin.Must(http.StatusNotFound, "GET", "/api/v1/files/"+id, nil)
			// None of these events has been pumped before purge. Older events must
			// retain the same placement visibility as file.purged, without reading bytes.
			c.DeliverEvents()
			c.DeliverEvents()
			mu.Lock()
			defer mu.Unlock()
			for _, action := range []string{"file.created", "file.version_created", "file.updated", "file.trashed", "file.purged"} {
				want := 0
				if tc.visible {
					want = c.Count(`SELECT COUNT(*) FROM core.events WHERE record_id=$1 AND action=$2`, id, action)
					if want == 0 {
						t.Errorf("fixture emitted no %s event", action)
					}
				}
				if n := received[id][action]; n != want {
					t.Errorf("%s delivered %d want %d", action, n, want)
				}
			}
		})
	}
}
