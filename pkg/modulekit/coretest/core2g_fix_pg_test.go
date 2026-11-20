package coretest_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2GEventDedupeNamespaces(t *testing.T) {
	c := coretest.New(t)
	const contract = "documents.generate.v1"
	for _, id := range []string{"collision-provider", "collision-consumer"} {
		c.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	}
	install2d(t, c, "collision-provider", []string{"contracts:provide"}, "provides:\n  - contract: "+contract+"\n    operations:\n      - name: generate\n        path: /generate\n")
	install2d(t, c, "collision-consumer", []string{"contracts:call", "contacts:read"}, "consumes:\n  - contract: "+contract+"\n")
	c.Admin.Must(201, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": contract, "provider_id": "collision-provider", "consumer_id": "collision-consumer"})
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bot := c.ModuleClient("collision-provider")
	path := "/api/v1/modules/contracts/" + contract + "/events"
	for _, format := range []string{"binding", "targeted"} {
		t.Run(format, func(t *testing.T) {
			contact := c.Admin.Must(201, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Collision", "family_name": format})
			var eventID string
			if err := db.QueryRow("SELECT id FROM core.events WHERE record_id=$1 AND action='contact.created'", str(t, contact.JSON, "id")).Scan(&eventID); err != nil {
				t.Fatal(err)
			}
			var event map[string]any
			if format == "binding" {
				event = map[string]any{"contract": contract, "event": "document.generated", "event_id": eventID, "occurred_at": "2026-11-20T12:00:00Z", "data": map[string]any{}}
			} else {
				event = map[string]any{"contract": contract, "action": "document.generated", "id": eventID, "consumer_id": "collision-consumer", "occurred_at": "2026-11-20T12:00:00Z", "payload": map[string]any{}}
			}
			bot.Must(204, "POST", path, event)
			bot.Must(204, "POST", path, event)
			c.DeliverEvents()
			if n := c.Count("SELECT COUNT(*) FROM core.module_deliveries WHERE module_id='collision-consumer' AND status='delivered' AND (event_id=$1 OR payload LIKE $2)", eventID, "%"+eventID+"%"); n != 2 {
				t.Errorf("core and contract delivery count = %d want 2", n)
			}
			if c.Count("SELECT COUNT(*) FROM core.audit_log WHERE entity='module_contract_event' AND new_value LIKE '%consumers=0'") == 0 {
				t.Error("replay audit did not report zero recipients")
			}
		})
	}
	event := `{"contract":"documents.generate.v1","event":"file.updated","event":"document.generated","event_id":"d210ad4a-3c92-4ed1-9684-8fb164155760","occurred_at":"2026-11-20T12:00:00Z","data":{}}`
	if r := bot.Raw("POST", path, []byte(event), "Content-Type", "application/json"); r.Code != 400 {
		t.Errorf("duplicate event key: %d %s", r.Code, r.Body)
	}
}

func TestCoreUpdate2GContactPaymentPumpWalls(t *testing.T) {
	c, _, _, _ := processorFixture(t)
	const consumer = "payment-wall-intake"
	install2d(t, c, consumer, []string{"contracts:call", "payments:write", "contacts:write"}, "consumes:\n  - contract: processor.payment.v1\n")
	c.Admin.Must(201, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": "processor.payment.v1", "provider_id": "synthetic-processor", "consumer_id": consumer})
	bot := c.ModuleClient(consumer)
	path := "/api/v1/modules/contracts/processor.payment.v1/"
	bot.Must(200, "POST", path+"authorize", processorRequest("wall-payment"))
	contact := bot.Must(201, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Synthetic", "family_name": "Walls"})
	contactID := str(t, contact.JSON, "id")
	payment := bot.Must(200, "POST", path+"record", map[string]any{"intent_id": "wall-payment", "contact_id": contactID})
	paymentID := str(t, payment.JSON, "id")
	// Refunds have their own record IDs and must resolve the parent payment.
	refundIDs := []string{}
	for _, status := range []string{"completed", "pending"} {
		r := c.Attorney.Must(201, "POST", "/api/v1/payments/"+paymentID+"/refunds", map[string]any{"amount_cents": 100, "reason": "Synthetic refund", "status": status})
		refundIDs = append(refundIDs, str(t, r.JSON, "id"))
	}
	c.Attorney.Must(201, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": c.Walled, "contact_id": contactID, "role": "client"})
	c.Attorney.Must(200, "POST", "/api/v1/payments/"+paymentID+"/refunds/"+refundIDs[1]+"/status", map[string]any{"status": "failed"})
	c.Handle("payment-wall-reader", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	install2d(t, c, "payment-wall-reader", []string{"payments:read"}, "", func(files map[string][]byte) {
		files["togavsu-module.yaml"] = []byte(strings.Replace(string(files["togavsu-module.yaml"]), "events:\n", "events:\n  - payment.*\n", 1))
	})
	check := func(want bool) {
		c.DeliverEvents()
		for _, recordID := range append([]string{paymentID}, refundIDs...) {
			n := c.Count("SELECT COUNT(*) FROM core.module_deliveries WHERE module_id='payment-wall-reader' AND payload LIKE $1", "%"+recordID+"%")
			if (n > 0) != want {
				t.Errorf("pump payment visible=%v want %v", n > 0, want)
			}
		}
	}

	// Exercise the actual migration Down against a stored contact-only money row.
	_, here, _, _ := runtime.Caller(0)
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(here), "../../../internal/core/migrations/*contact_processor_payment.sql"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("migration files %v %v", paths, err)
	}
	migration, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	down := strings.Split(string(migration), "-- +goose Down")[1]
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("SET LOCAL search_path TO core"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(down); err != nil {
		t.Errorf("expand-only Down refused existing contact payment: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	check(false)
	for _, who := range []*coretest.User{c.Admin, c.Attorney} {
		want := who == c.Attorney
		for _, surface := range []string{"/practice/activity", "/api/v1/events?limit=200"} {
			r := who.Must(200, "GET", surface, nil)
			if strings.Contains(r.Body, map[bool]string{true: "payment.created", false: paymentID}[surface == "/practice/activity"]) != want {
				t.Errorf("%s payment visible for %s want %v", surface, who.ID, want)
			}
		}
		for _, surface := range []string{"/practice/activity", "/api/v1/events?limit=200"} {
			r := who.Must(200, "GET", surface, nil)
			for _, marker := range refundIDs {
				if surface == "/practice/activity" {
					marker = "payment.refund"
				}
				if strings.Contains(r.Body, marker) != want {
					t.Errorf("refund %s on %s visible want %v", marker, surface, want)
				}
			}
		}
		v := toolResult2d(t, who.Client, "listEvents", map[string]any{"query": map[string]any{"limit": 200}}, false)
		raw, _ := json.Marshal(v)
		if strings.Contains(string(raw), paymentID) != want {
			t.Errorf("MCP payment visible for %s want %v", who.ID, want)
		}
		for _, refundID := range refundIDs {
			if strings.Contains(string(raw), refundID) != want {
				t.Errorf("MCP refund %s visible want %v", refundID, want)
			}
		}
	}
	c.Wall(c.Walled, c.Admin, c.Attorney)
	// A service principal has no user and remains off a wall. Removing the wall
	// through the real API makes both payment events eligible for the pump.
	c.Wall(c.Walled)
	check(true)
}

func TestCoreUpdate2GOccupancyScopeRefusal(t *testing.T) {
	c := coretest.New(t)
	c.Handle("occupancy-read-only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	install2d(t, c, "occupancy-read-only", []string{"contacts:read"}, "")
	bot := c.ModuleClient("occupancy-read-only")
	query := map[string]any{"view": "occupancy", "from_time": "2026-11-20T10:00:00Z", "to_time": "2026-11-20T11:00:00Z"}
	bot.Must(403, "GET", "/api/v1/calendar-events?view=occupancy&from_time=2026-11-20T10:00:00Z&to_time=2026-11-20T11:00:00Z", nil)
	toolResult2d(t, bot, "listCalendarEvent", map[string]any{"query": query}, true)
}

// An upgraded queue can still hold the 2f raw contract key, even after delivery.
func TestCoreUpdate2GLegacyContractDedupe(t *testing.T) {
	c := coretest.New(t)
	const contract = "documents.generate.v1"
	for _, id := range []string{"legacy-provider", "legacy-consumer"} {
		c.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	}
	install2d(t, c, "legacy-provider", []string{"contracts:provide"}, "provides:\n  - contract: "+contract+"\n    operations:\n      - name: generate\n        path: /generate\n")
	install2d(t, c, "legacy-consumer", []string{"contracts:call"}, "consumes:\n  - contract: "+contract+"\n")
	c.Admin.Must(201, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": contract, "provider_id": "legacy-provider", "consumer_id": "legacy-consumer"})
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, format := range []string{"binding", "targeted"} {
		t.Run(format, func(t *testing.T) {
			eventID := "d210ad4a-3c92-4ed1-9684-8fb164155760"
			if format == "targeted" {
				eventID = "d210ad4a-3c92-4ed1-9684-8fb164155761"
			}
			body := map[string]any{"contract": contract, "event": "document.generated", "event_id": eventID, "occurred_at": "2026-11-20T12:00:00Z", "data": map[string]any{}}
			if format == "targeted" {
				body = map[string]any{"contract": contract, "action": "document.generated", "id": eventID, "consumer_id": "legacy-consumer", "occurred_at": "2026-11-20T12:00:00Z", "payload": map[string]any{}}
			}
			bot := c.ModuleClient("legacy-provider")
			path := "/api/v1/modules/contracts/" + contract + "/events"
			bot.Must(204, "POST", path, body)
			c.DeliverEvents()
			// Simulate an existing delivered row from before the namespace change.
			if _, err := db.Exec("UPDATE core.module_deliveries SET event_id=$1 WHERE event_id=$2 AND module_id='legacy-consumer' AND status='delivered'", eventID, "contract:"+eventID); err != nil {
				t.Fatal(err)
			}
			bot.Must(204, "POST", path, body)
			c.DeliverEvents()
			if n := c.Count("SELECT COUNT(*) FROM core.module_deliveries WHERE module_id='legacy-consumer' AND (event_id=$1 OR event_id=$2)", eventID, "contract:"+eventID); n != 1 {
				t.Errorf("legacy replay delivered twice: %d", n)
			}
		})
	}
}
