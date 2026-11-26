package coretest_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2GBindingEnvelope(t *testing.T) {
	c := coretest.New(t)
	const contract = "documents.generate.v1"
	const provider = "binding-provider"
	var mu sync.Mutex
	received := map[string][]map[string]any{}
	attempts := map[string]int{}
	for _, id := range []string{"binding-one", "binding-two", "binding-unlinked", "binding-disabled"} {
		id := id
		c.Serve(id, func(m coretest.Module) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sender, b, err := coretest.ReadDelivery(m, r)
				if err != nil {
					t.Error(err)
					w.WriteHeader(401)
					return
				}
				var v map[string]any
				_ = json.Unmarshal(b, &v)
				if v["contract"] == contract {
					if sender != provider {
						t.Errorf("sender %s", sender)
					}
					mu.Lock()
					attempts[id]++
					if id == "binding-one" && attempts[id] == 1 {
						mu.Unlock()
						w.WriteHeader(503)
						return
					}
					received[id] = append(received[id], v)
					mu.Unlock()
				}
				w.WriteHeader(204)
			})
		})
		install2d(t, c, id, []string{"contracts:call"}, "consumes:\n  - contract: "+contract+"\n")
	}
	install2d(t, c, provider, []string{"contracts:provide"}, "provides:\n  - contract: "+contract+"\n    operations:\n      - name: generate\n        path: /generate\n")
	for _, id := range []string{"binding-one", "binding-two", "binding-disabled"} {
		c.Admin.Must(201, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": contract, "provider_id": provider, "consumer_id": id})
	}
	c.Admin.Must(200, "POST", "/api/v1/modules/binding-disabled/disable", nil)
	event := map[string]any{"contract": contract, "event": "document.generated", "event_id": "d210ad4a-3c92-4ed1-9684-8fb164155760", "occurred_at": "2026-11-20T12:00:00+02:00", "data": map[string]any{"run_id": "synthetic-run", "file_ids": []any{"synthetic-file"}, "sender": "untrusted"}}
	bot := c.ModuleClient(provider)
	path := "/api/v1/modules/contracts/" + contract + "/events"
	bot.Must(204, "POST", path, event)
	bot.Must(204, "POST", path, event)
	c.DeliverEvents()
	// The queue keeps the exact binding body and sender across failure and unlink.
	links := c.Admin.Must(200, "GET", "/api/v1/modules/contract-links", nil)
	for _, row := range links.JSON["data"].([]any) {
		v := row.(map[string]any)
		if v["consumer_id"] == "binding-one" {
			c.Admin.Must(204, "DELETE", "/api/v1/modules/contract-links/"+v["id"].(string), nil)
		}
	}
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE core.module_deliveries SET next_attempt_at=0 WHERE payload LIKE $1`, "%"+event["event_id"].(string)+"%"); err != nil {
		t.Fatal(err)
	}
	c.DeliverEvents()
	bot.Must(204, "POST", path, event)
	c.DeliverEvents()
	mu.Lock()
	for _, id := range []string{"binding-one", "binding-two"} {
		if len(received[id]) != 1 || !reflect.DeepEqual(received[id][0], event) {
			t.Errorf("%s: %#v", id, received[id])
		}
	}
	if len(received["binding-unlinked"])+len(received["binding-disabled"]) != 0 {
		t.Error("unlinked/disabled delivery")
	}
	mu.Unlock()
	for _, change := range []map[string]any{{"contract": "other.v1"}, {"event": ""}, {"event": "calendar.changed"}, {"event": "core.changed"}, {"event": "custom_field.updated"}, {"event": "conflict.checked"}, {"event": "county.changed"}, {"event": "practice_area.changed"}, {"event": "referral_source.changed"}, {"event": "file.updated"}, {"event": "fee_agreement.executed"}, {"event": "calendar_event.created"}, {"event": "session.created"}, {"event_id": "not-a-uuid"}, {"occurred_at": "yesterday"}, {"data": nil}, {"data": []any{}}} {
		bad := map[string]any{}
		for k, v := range event {
			bad[k] = v
		}
		for k, v := range change {
			bad[k] = v
		}
		r := bot.Must(400, "POST", path, bad)
		if change["event"] == "custom_field.updated" || change["event"] == "conflict.checked" {
			if r.JSON["code"] != "validation" || r.JSON["field"] != "event" {
				t.Errorf("reserved error shape: %s", r.Body)
			}
		}
	}
}

func TestCoreUpdate2GFirmOccupancy(t *testing.T) {
	c := coretest.New(t)
	const id = "firm-occupancy"
	c.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	install2d(t, c, id, []string{"calendar:read"}, "")
	install2d(t, c, "calendar-no-read", []string{"contacts:read"}, "")
	start := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	for _, attendees := range [][]string{nil, {c.Attorney.ID}} {
		c.Attorney.Must(201, "POST", "/api/v1/calendar-events", map[string]any{"title": "SECRET", "kind": "court_date", "matter_id": c.Walled, "starts_at": start.Format(time.RFC3339), "ends_at": start.Add(time.Hour).Format(time.RFC3339), "attendee_user_ids": attendees})
	}
	// An unwalled event with no attendees remains visible; a later event is out of range.
	for _, offset := range []time.Duration{0, 5 * time.Hour} {
		c.Attorney.Must(201, "POST", "/api/v1/calendar-events", map[string]any{"title": "Open", "kind": "block", "matter_id": c.Open, "starts_at": start.Add(offset).Format(time.RFC3339), "ends_at": start.Add(offset + time.Hour).Format(time.RFC3339)})
	}
	q := url.Values{"view": {"occupancy"}, "from_time": {start.Add(-time.Hour).Format(time.RFC3339)}, "to_time": {start.Add(2 * time.Hour).Format(time.RFC3339)}}
	bot := c.ModuleClient(id)
	c.ModuleClient("calendar-no-read").Must(403, "GET", "/api/v1/calendar-events?"+q.Encode(), nil)
	toolResult2d(t, c.ModuleClient("calendar-no-read"), "listCalendarEvent", map[string]any{"query": map[string]any{"view": "occupancy", "from_time": q.Get("from_time"), "to_time": q.Get("to_time")}}, true)
	for _, lifted := range []bool{false, true} {
		if lifted {
			c.Wall(c.Walled)
		}
		for _, transport := range []string{"REST", "MCP"} {
			var v map[string]any
			if transport == "REST" {
				v = bot.Must(200, "GET", "/api/v1/calendar-events?"+q.Encode(), nil).JSON
			} else {
				v = toolResult2d(t, bot, "listCalendarEvent", map[string]any{"query": map[string]any{"view": "occupancy", "from_time": q.Get("from_time"), "to_time": q.Get("to_time")}}, false)
			}
			rows := v["data"].([]any)
			want := 2
			if lifted {
				want = 3
			}
			if len(rows) != want {
				t.Fatalf("wall lifted=%v: %s occupancy want %d: %v", lifted, transport, want, v)
			}
			for _, r := range rows {
				row := r.(map[string]any)
				if len(row) != 3 || row["all_day"] != false || row["starts_at"] != start.Format(time.RFC3339) || row["ends_at"] != start.Add(time.Hour).Format(time.RFC3339) {
					t.Fatalf("leaked details: %v", row)
				}
			}
		}
	}
	narrowService2f(t, c, id).Must(403, "GET", "/api/v1/calendar-events?"+q.Encode(), nil)
	c.Attorney.Must(403, "GET", "/api/v1/calendar-events?"+q.Encode(), nil)
	q.Set("from_time", start.Add(3*time.Hour).Format(time.RFC3339))
	q.Set("to_time", start.Add(4*time.Hour).Format(time.RFC3339))
	if rows := bot.Must(200, "GET", "/api/v1/calendar-events?"+q.Encode(), nil).JSON["data"].([]any); len(rows) != 0 {
		t.Fatalf("outside window: %v", rows)
	}
	q.Set("user_id", c.Attorney.ID)
	bot.Must(400, "GET", "/api/v1/calendar-events?"+q.Encode(), nil)
}

func TestCoreUpdate2GProcessorRecording(t *testing.T) {
	c, p, bot, _ := processorFixture(t)
	path := "/api/v1/modules/contracts/processor.payment.v1/"
	p.LoseNextApprovalResponse()
	bot.Must(202, "POST", path+"authorize", processorRequest("record-intent"))
	in := map[string]any{"intent_id": "record-intent", "matter_id": c.Open, "contact_id": c.ClientContact}
	bot.Must(409, "POST", path+"record", in)
	bot.Must(200, "POST", path+"recover", map[string]any{"intent_id": "record-intent"})
	matter := bot.Must(201, "POST", "/api/v1/matters", map[string]any{"name": "Approved synthetic intake", "practice_area_id": c.PracticeArea, "status": "prospect"})
	in["matter_id"] = str(t, matter.JSON, "id")
	// The bound target must be the consumer's newly created matter. Its client party
	// can be an existing contact; recording never creates another contact or charge.
	c.Admin.Must(201, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": in["matter_id"], "contact_id": c.ClientContact, "role": "client", "primary": true})
	first := bot.Must(200, "POST", path+"record", in)
	id := str(t, first.JSON, "id")
	if first.JSON["amount_cents"] != float64(2500) || first.JSON["destination"] != "operating" || first.JSON["source"] != "synthetic-processor" {
		t.Fatalf("bad recorded receipt: %s", first.Body)
	}
	calls := len(p.Calls())
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := bot.Must(200, "POST", path+"record", in)
			if r.JSON["id"] != id {
				t.Error("duplicate recorded payment")
			}
		}()
	}
	wg.Wait()
	if p.Charges() != 1 || len(p.Calls()) != calls || c.Count("SELECT COUNT(*) FROM core.payments WHERE id=$1", id) != 1 {
		t.Fatal("recording recharged or duplicated")
	}
	changed := map[string]any{"intent_id": "record-intent", "matter_id": c.Open, "contact_id": c.ClientContact}
	bot.Must(409, "POST", path+"record", changed)
	for _, extra := range []string{"amount_cents", "destination", "provider_id", "reference_number"} {
		bad := map[string]any{}
		for k, v := range in {
			bad[k] = v
		}
		bad[extra] = "changed"
		bot.Must(400, "POST", path+"record", bad)
	}
	bot.Must(404, "POST", path+"record", map[string]any{"intent_id": "unknown", "matter_id": in["matter_id"], "contact_id": c.ClientContact})
	if c.Count("SELECT COUNT(*) FROM core.audit_log WHERE entity='payment' AND entity_id=$1 AND actor_id='synthetic-intake' AND source='service'", id) == 0 {
		t.Fatal("missing module audit actor")
	}
	if c.Count("SELECT COUNT(*) FROM core.audit_log WHERE entity='module_processor_intent' AND field='binding' AND new_value LIKE $1 AND new_value LIKE $2 AND new_value LIKE $3", "%"+id+"%", "%"+in["matter_id"].(string)+"%", "%"+c.ClientContact+"%") != 1 {
		t.Error("missing distinct binding audit")
	}
	if strings.Contains(first.Body, "payment_token") {
		t.Fatal("token exposed")
	}
}

func TestCoreUpdate2GProcessorContactAndRefusals(t *testing.T) {
	c, p, _, link := processorFixture(t)
	const consumer = "contact-intake"
	install2d(t, c, consumer, []string{"contracts:call", "payments:write", "contacts:write"}, "consumes:\n  - contract: processor.payment.v1\n")
	c.Admin.Must(201, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": "processor.payment.v1", "provider_id": "synthetic-processor", "consumer_id": consumer})
	bot := c.ModuleClient(consumer)
	path := "/api/v1/modules/contracts/processor.payment.v1/"
	bot.Must(200, "POST", path+"authorize", processorRequest("contact-approved"))
	contact := bot.Must(201, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Synthetic", "family_name": "Paid"})
	contactID := str(t, contact.JSON, "id")
	in := map[string]any{"intent_id": "contact-approved", "contact_id": contactID}
	// A second consumer cannot use this receipt even with the same approved provider.
	c.ModuleClient("synthetic-intake").Must(404, "POST", path+"record", in)
	r := c.RecordProcessorPayment(consumer, modulekit.ProcessorPaymentRecord{IntentID: "contact-approved", ContactID: modulekit.ID(contactID)})
	if r.Code != 200 || r.JSON["matter_id"] != nil {
		t.Fatalf("contact-only record: %d %s", r.Code, r.Body)
	}
	id := str(t, r.JSON, "id")
	replay := toolResult2d(t, bot, "callModuleContract", map[string]any{"path": map[string]any{"contract": "processor.payment.v1", "operation": "record"}, "body": in}, false)
	if replay["id"] != id || p.Charges() != 1 {
		t.Fatalf("MCP replay %v", replay)
	}
	// The normal payment endpoint stays closed to arbitrary viewerless writes.
	bot.Must(403, "POST", "/api/v1/payments", map[string]any{"contact_id": contactID, "matter_id": c.Open, "amount_cents": 2500, "destination": "operating", "method": "card"})
	bot.Must(409, "POST", path+"record", map[string]any{"intent_id": "contact-approved", "contact_id": c.ClientContact}) // different binding conflicts before placement
	// Declined receipts and uncertain approvals cannot record anything.
	p.DeclineNext("card_declined")
	bot.Must(200, "POST", path+"authorize", processorRequest("contact-declined"))
	bot.Must(409, "POST", path+"record", map[string]any{"intent_id": "contact-declined", "contact_id": contactID})
	p.LoseNextApprovalResponse()
	bot.Must(202, "POST", path+"authorize", processorRequest("contact-pending"))
	bot.Must(409, "POST", path+"record", map[string]any{"intent_id": "contact-pending", "contact_id": contactID})
	// Unknown fields forbid an amount, provider, destination or intent substitution.
	if r := bot.Do("POST", path+"record", in, "X-Togavsu-Context-Token", c.ContextFor("attorney", consumer, "")); r.Code != 403 {
		t.Fatalf("viewer context accepted: %d %s", r.Code, r.Body)
	}
	narrowService2f(t, c, consumer).Must(403, "POST", path+"record", in)
	c.Attorney.Must(201, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": c.Walled, "contact_id": contactID, "role": "client"})
	bot.Must(404, "POST", path+"record", in)
	c.Admin.Must(404, "GET", "/api/v1/payments/"+id, nil)
	for _, who := range []*coretest.User{c.Admin, c.Attorney} {
		want := who == c.Attorney
		for _, surface := range []string{"/practice/activity", "/api/v1/events?limit=200"} {
			r := who.Must(200, "GET", surface, nil)
			if strings.Contains(r.Body, map[bool]string{true: "payment.created", false: id}[surface == "/practice/activity"]) != want {
				t.Errorf("payment visibility %s for %s: want %v", surface, who.ID, want)
			}
		}
		v := toolResult2d(t, who.Client, "listEvents", map[string]any{"query": map[string]any{"limit": 200}}, false)
		raw, _ := json.Marshal(v)
		if strings.Contains(string(raw), id) != want {
			t.Errorf("MCP payment visibility for %s want %v", who.ID, want)
		}
	}
	c.Attorney.Must(200, "GET", "/api/v1/payments/"+id, nil)
	c.Admin.Must(204, "DELETE", "/api/v1/modules/contract-links/"+link, nil)
	// Disabling the linked processor refuses recording/replay without contacting it.
	c.Admin.Must(200, "POST", "/api/v1/modules/synthetic-processor/disable", nil)
	bot.Must(409, "POST", path+"record", in)
}

func TestCoreUpdate2GAllBindingContracts(t *testing.T) {
	c := coretest.New(t)
	contracts := []string{"signature.request.v1", "phone.provider.v1", "meetings.provider.v1", "synthetic.other.v1"}
	var mu sync.Mutex
	received := map[string]map[string]any{}
	c.Serve("all-consumer", func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sender, b, err := coretest.ReadDelivery(m, r)
			if err != nil {
				t.Errorf("delivery sender %s error %v", sender, err)
				w.WriteHeader(401)
				return
			}
			var v map[string]any
			json.Unmarshal(b, &v)
			if name, ok := v["contract"].(string); ok {
				if sender != "all-provider" {
					t.Errorf("binding sender %s", sender)
					w.WriteHeader(401)
					return
				}
				mu.Lock()
				received[name] = v
				mu.Unlock()
			}
			w.WriteHeader(204)
		})
	})
	provides, consumes := "provides:\n", "consumes:\n"
	for _, name := range contracts {
		provides += "  - contract: " + name + "\n    operations:\n      - name: perform\n        path: /perform\n"
		consumes += "  - contract: " + name + "\n"
	}
	install2d(t, c, "all-provider", []string{"contracts:provide"}, provides)
	install2d(t, c, "all-consumer", []string{"contracts:call"}, consumes)
	expected := map[string]map[string]any{}
	for i, name := range contracts {
		c.Admin.Must(201, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": name, "provider_id": "all-provider", "consumer_id": "all-consumer"})
		event := modulekit.BindingContractEvent{Contract: name, Event: "provider.completed", EventID: fmt.Sprintf("d210ad4a-3c92-4ed1-9684-8fb16415576%d", i), OccurredAt: "2026-11-20T12:00:00Z", Data: json.RawMessage(`{"ok":true}`)}
		r := c.PushContractEvent("all-provider", event)
		if r.Code != 204 {
			t.Fatalf("binding %s: %d %s", name, r.Code, r.Body)
		}
		b, _ := json.Marshal(event)
		var v map[string]any
		json.Unmarshal(b, &v)
		expected[name] = v
	}
	c.DeliverEvents()
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(received, expected) {
		t.Fatalf("changed/missing binding contracts: %#v", received)
	}
}

func TestCoreUpdate2GLegacyReceiptApprovalOrder(t *testing.T) {
	c, p, _, _ := processorFixture(t)
	const consumer = "legacy-intake"
	install2d(t, c, consumer, []string{"contracts:call", "payments:write", "contacts:write"}, "consumes:\n  - contract: processor.payment.v1\n")
	c.Admin.Must(201, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": "processor.payment.v1", "provider_id": "synthetic-processor", "consumer_id": consumer})
	bot := c.ModuleClient(consumer)
	path := "/api/v1/modules/contracts/processor.payment.v1/"
	p.LoseNextApprovalResponse()
	bot.Must(202, "POST", path+"authorize", processorRequest("legacy-intent"))
	early := bot.Must(201, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Created", "family_name": "WhilePending"})
	bot.Must(200, "POST", path+"recover", map[string]any{"intent_id": "legacy-intent"})
	// Emulate a real 2f receipt: it has approval audit evidence, but no new
	// timestamp field. The append-only audit ledger itself remains untouched.
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE core.core_idempotency SET response=(response::jsonb - 'approved_at')::text WHERE actor=$1 AND operation='processor.payment.v1' AND idem_key='legacy-intent'`, "module-processor/"+consumer); err != nil {
		t.Fatal(err)
	}
	bot.Must(403, "POST", path+"record", map[string]any{"intent_id": "legacy-intent", "contact_id": early.JSON["id"]})
	late := bot.Must(201, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Created", "family_name": "AfterApproval"})
	body := map[string]any{"intent_id": "legacy-intent", "contact_id": late.JSON["id"]}
	first := bot.Must(200, "POST", path+"record", body)
	again := bot.Must(200, "POST", path+"record", body)
	if first.JSON["id"] != again.JSON["id"] || p.Charges() != 1 || len(p.Calls()) != 2 {
		t.Fatal("legacy recovery duplicated payment or charge")
	}
}
