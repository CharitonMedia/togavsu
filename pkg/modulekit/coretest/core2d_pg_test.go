package coretest_test

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func install2d(t *testing.T, c *coretest.Core, id string, scopes []string, tail string, changes ...func(map[string][]byte)) {
	t.Helper()
	files, err := matterclock.Files()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(files[modulekit.ManifestFile]), "id: matter-clock", "id: "+id, 1)
	text = strings.Replace(text, "name: Matter Clock", "name: "+id, 1)
	permissions := "permissions:\n"
	for _, scope := range scopes {
		permissions += "  - scope: " + scope + "\n    reason: Synthetic acceptance permission.\n"
	}
	text = strings.Replace(text, "permissions:\n", permissions, 1)
	text = strings.Replace(text, "events:\n", "events:\n  - matter.party_added\n  - matter_party.*\n  - contact_address.*\n  - contact_phone.*\n  - contact_email.*\n  - contact.*\n  - calendar_event.*\n  - deadline.*\n  - note.*\n  - file.*\n", 1)
	files[modulekit.ManifestFile] = []byte(text + "\n" + tail)
	for _, change := range changes {
		change(files)
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := modulekit.Build(priv, files)
	if err != nil {
		t.Fatal(err)
	}
	c.TrustKey(id, pub)
	c.Install(signed)
	c.ApproveAll(id)
}

type received2d struct {
	body    map[string]any
	headers http.Header
}

func TestCoreUpdate2DReadsWallsMailSettingsAndPhone(t *testing.T) {
	c := coretest.New(t)
	const id = "kit-two-d"
	var mu sync.Mutex
	var deliveries []received2d
	c.Serve(id, func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if err := modulekit.VerifyWebhook(m.WebhookSecret, r.Header, b, time.Now(), 0); err != nil {
				t.Errorf("event signature: %v", err)
			}
			if r.Header.Get(modulekit.TimestampHeader) == "" {
				t.Error("missing timestamp")
			}
			var value map[string]any
			_ = json.Unmarshal(b, &value)
			mu.Lock()
			deliveries = append(deliveries, received2d{value, r.Header.Clone()})
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})
	})
	scopes := []string{"contacts:read", "contacts:write", "calendar:read", "deadlines:read", "notes:read", "settings:read", "firm:read", "users:directory", "mail:send", "events:write", "module_settings:write", "contracts:call"}
	install2d(t, c, id, scopes, "consumes:\n  - contract: phone.provider.v1\ncontact_phone_items:\n  - id: call\n    title: Call\n    path: /call\n")
	bot := c.ModuleClient(id)
	mustBot := func(status int, method, path string, body any, headers ...string) coretest.Resp {
		t.Helper()
		r := bot.Do(method, path, body, headers...)
		if r.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, r.Code, status, r.Body)
		}
		return r
	}
	day := time.Now().AddDate(0, 0, 2).UTC().Format("2006-01-02")
	at := time.Now().Add(time.Hour).UTC()
	event := func(who *coretest.User, matter, title string) string {
		return str(t, who.Must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": title, "kind": "other", "starts_at": at.Format(time.RFC3339), "ends_at": at.Add(time.Hour).Format(time.RFC3339), "matter_id": matter}).JSON, "id")
	}
	deadline := func(who *coretest.User, matter, title string) string {
		return str(t, who.Must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"title": title, "due_on": day, "matter_id": matter, "responsible_user_id": c.Attorney.ID}).JSON, "id")
	}
	openEvent := event(c.Admin, c.Open, "Open event")
	wallEvent := event(c.Attorney, c.Walled, "WALL-EVENT-SECRET")
	_ = event(c.Attorney, c.Walled, "WALL-SIBLING-SECRET")
	openDeadline := deadline(c.Admin, c.Open, "Open deadline")
	wallDeadline := deadline(c.Attorney, c.Walled, "WALL-DEADLINE-SECRET")
	address := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/contact-addresses", map[string]any{"contact_id": c.ClientContact, "label": "work", "line1": "1 Synthetic St"})
	phone := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{"contact_id": c.ClientContact, "label": "work", "e164": "+15555550123", "effective_from": "2026-01-01"})
	phoneID := str(t, phone.JSON, "id")
	for _, path := range []string{"/api/v1/calendar-events/" + openEvent, "/api/v1/deadlines/" + openDeadline, "/api/v1/contact-addresses?contact_id=" + c.ClientContact, "/api/v1/contact-addresses/" + str(t, address.JSON, "id"), "/api/v1/party-roles", "/api/v1/custom-field-definitions", "/api/v1/firm-profile", "/api/v1/user-directory", "/api/v1/matter-parties?contact_id=" + c.ClientContact} {
		if r := bot.Do("GET", path, nil); r.Code != http.StatusOK {
			t.Fatalf("service read %s: %d %s", path, r.Code, r.Body)
		}
	}
	firm := mustBot(http.StatusOK, "GET", "/api/v1/firm-profile", nil)
	if firm.JSON["time_zone"] == nil {
		t.Fatal("firm.time_zone missing")
	}
	for _, path := range []string{"/api/v1/calendar-events/" + wallEvent, "/api/v1/deadlines/" + wallDeadline, "/api/v1/matters/" + c.Walled} {
		mustBot(http.StatusNotFound, "GET", path, nil)
		mustBot(http.StatusNotFound, "GET", path, nil, modulekit.ContextHeader, c.ContextFor(coretest.Staff, id, ""))
		r := mustBot(http.StatusOK, "GET", path, nil, modulekit.ContextHeader, c.ContextFor(coretest.Attorney, id, c.Walled))
		if strings.Contains(r.Body, "wall_member_user_ids") || strings.Contains(r.Body, `"walled"`) {
			t.Fatalf("wall metadata: %s", r.Body)
		}
	}
	for _, path := range []string{"/api/v1/calendar-events", "/api/v1/deadlines", "/api/v1/matter-parties"} {
		r := mustBot(http.StatusOK, "GET", path, nil)
		if strings.Contains(r.Body, "WALL-") || strings.Contains(r.Body, c.Walled) {
			t.Fatalf("walled list leaked: %s", r.Body)
		}
	}
	directory := mustBot(http.StatusOK, "GET", "/api/v1/user-directory?matter_id="+c.Walled, nil, modulekit.ContextHeader, c.ContextFor(coretest.Attorney, id, c.Walled))
	if !strings.Contains(directory.Body, c.Attorney.Email) || strings.Contains(directory.Body, c.Staff.ID) || strings.Contains(directory.Body, c.Admin.ID) {
		t.Fatalf("directory wall filter: %s", directory.Body)
	}
	if strings.Contains(c.Client.Must(http.StatusOK, "GET", "/api/v1/user-directory", nil).Body, "work_email") {
		t.Fatal("client work email leak")
	}
	c.DeliverEvents()
	// Events confer no record credentials, and legacy credentials cannot change access.
	mu.Lock()
	for _, d := range deliveries {
		if d.headers.Get("X-Togavsu-Event-Authorization") != "" {
			t.Error("event minted a record grant")
		}
	}
	mu.Unlock()
	grant := "legacy-or-forged-event-token"
	mustBot(http.StatusNotFound, "GET", "/api/v1/calendar-events/"+wallEvent, nil, "X-Togavsu-Event-Authorization", grant)
	rpc := mustBot(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "getCalendarEvent", "arguments": map[string]any{"path": map[string]any{"calendar_event_id": wallEvent}}}}, "X-Togavsu-Event-Authorization", grant)
	if !strings.Contains(rpc.Body, "not_found") {
		t.Fatalf("MCP crossed wall: %s", rpc.Body)
	}
	badHeader := mustBot(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "getCalendarEvent", "arguments": map[string]any{"path": map[string]any{"calendar_event_id": wallEvent}, "headers": map[string]string{"Authorization": "spoofed"}}}})
	if !strings.Contains(badHeader.Body, "header is not allowed") {
		t.Fatal("MCP accepted credential override")
	}

	// Module settings scope can save only own values, with an admin viewer.
	ownPath := "/api/v1/modules/" + id + "/own-settings"
	mustBot(http.StatusForbidden, "PUT", ownPath, map[string]string{"increment_minutes": "12"})
	mustBot(http.StatusForbidden, "PUT", ownPath, map[string]string{"increment_minutes": "12"}, modulekit.ContextHeader, c.ContextFor(coretest.Staff, id, ""))
	mustBot(http.StatusOK, "PUT", ownPath, map[string]string{"increment_minutes": "12"}, modulekit.ContextHeader, c.ContextFor(coretest.Admin, id, ""))
	settings := mustBot(http.StatusOK, "GET", "/api/v1/modules/"+id+"/settings", nil)
	if !strings.Contains(settings.Body, "12") {
		t.Fatal(settings.Body)
	}
	mustBot(http.StatusForbidden, "PUT", "/api/v1/modules/another/own-settings", map[string]string{}, modulekit.ContextHeader, c.ContextFor(coretest.Admin, id, ""))
	mustBot(http.StatusForbidden, "PUT", "/api/v1/modules/"+id+"/settings", map[string]string{"increment_minutes": "8"}, modulekit.ContextHeader, c.ContextFor(coretest.Admin, id, ""))
	mustBot(http.StatusOK, "GET", "/api/v1/settings/modules.contract_call_max_bytes", nil)
	mustBot(http.StatusOK, "GET", "/api/v1/settings", nil)
	mustBot(http.StatusForbidden, "GET", "/api/v1/settings/smtp.password", nil)
	// Install approval cannot substitute for this bearer's settings:read.
	token := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "narrow contract token", "module_id": id, "scopes": []string{"contracts:call"}, "approved_scopes": []string{"contracts:call", "settings:read"}})
	narrow := c.NewClient()
	narrow.Bearer = str(t, token.JSON, "secret")
	for _, path := range []string{"/api/v1/settings", "/api/v1/settings/modules.contract_call_max_bytes"} {
		for _, headers := range [][]string{nil, {modulekit.ContextHeader, c.ContextFor(coretest.Admin, id, "")}} {
			got := narrow.Do("GET", path, nil, headers...)
			if got.Code != http.StatusForbidden || !strings.Contains(got.Body, "scope_denied") {
				t.Fatalf("settings scope: %d %s", got.Code, got.Body)
			}
		}
	}
	// Delegated changes retain the user and carry module attribution separately.
	delegated := mustBot(http.StatusCreated, "POST", "/api/v1/events", map[string]any{"action": id + ".delegated", "record_type": "matter", "record_id": c.Open, "matter_id": c.Open, "summary": "Synthetic delegated call"}, modulekit.ContextHeader, c.ContextFor(coretest.Staff, id, c.Open))
	if delegated.JSON["actor_id"] != c.Staff.ID || delegated.JSON["module_id"] != id || delegated.JSON["actor_kind"] != "staff" || delegated.JSON["actor_name"] != nil {
		t.Fatalf("delegated actor: %s", delegated.Body)
	}
	feed := c.Staff.Must(http.StatusOK, "GET", "/practice/activity?tab=staff&user="+c.Staff.ID, nil)
	if !strings.Contains(feed.Body, "Synthetic delegated call") || !strings.Contains(feed.Body, "via "+id) {
		t.Fatalf("person feed: %s", feed.Body)
	}
	timeline := c.Staff.Must(http.StatusOK, "GET", "/api/v1/matters/"+c.Open+"/timeline", nil)
	if !strings.Contains(timeline.Body, `"actor_id":"`+c.Staff.ID+`"`) || !strings.Contains(timeline.Body, `"module_id":"`+id+`"`) {
		t.Fatalf("timeline attribution: %s", timeline.Body)
	}
	// Mail resolves contact addresses from core parties and records the matter.
	before := len(c.Mail.Messages())
	mail := mustBot(http.StatusOK, "POST", "/api/v1/mail/messages", map[string]any{"subject": "Synthetic service mail", "text": "Example only", "matter_id": c.Open, "to_contact_ids": []string{c.ClientContact}})
	if !strings.Contains(mail.Body, "sent") || len(c.Mail.Messages()) != before+1 {
		t.Fatalf("service contact mail %s", mail.Body)
	}
	mustBot(http.StatusNotFound, "POST", "/api/v1/mail/messages", map[string]any{"subject": "Hidden", "text": "Synthetic", "matter_id": c.Walled, "to_contact_ids": []string{c.ClientContact}})
	// The module event is visible in both shared timeline and Activities Feed, named.
	mustBot(http.StatusCreated, "POST", "/api/v1/events", map[string]any{"action": id + ".called", "record_type": "matter", "record_id": c.Open, "matter_id": c.Open, "summary": "Synthetic module call"})
	for _, path := range []string{"/api/v1/matters/" + c.Open + "/timeline", "/api/v1/events?matter_id=" + c.Open, "/practice/matters/" + c.Open, "/practice/activity"} {
		r := c.Attorney.Must(http.StatusOK, "GET", path, nil)
		if !strings.Contains(r.Body, id) || !strings.Contains(r.Body, "Synthetic module call") {
			t.Fatalf("named module activity %s: %s", path, r.Body)
		}
	}
	// The phone row displays the module action, whose frame rechecks contact access.
	page := c.Staff.Must(http.StatusOK, "GET", "/practice/contacts/"+c.ClientContact, nil)
	if !strings.Contains(page.Body, "contact-phone-items/call") {
		t.Fatal("contact phone action missing")
	}
	list := c.Staff.Must(http.StatusOK, "GET", "/api/v1/modules/contact-phone-items?phone_id="+phoneID, nil)
	if !strings.Contains(list.Body, id) {
		t.Fatal(list.Body)
	}
	frame := c.Staff.Must(http.StatusOK, "GET", "/modules/"+id+"/contact-phone-items/call?phone_id="+phoneID, nil)
	if !strings.Contains(frame.Body, "sandbox") || !strings.Contains(frame.Body, "phone_id=") || strings.Contains(frame.Body, "phone=") || strings.Contains(frame.Body, "15555550123") {
		t.Fatal(frame.Body)
	}
	// A module with no read scopes is refused even with a legacy credential.
	c.Handle("limited-two-d", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	install2d(t, c, "limited-two-d", nil, "")
	limited := c.ModuleClient("limited-two-d")
	mustLimited := func(status int, method, path string, body any, headers ...string) coretest.Resp {
		t.Helper()
		r := limited.Do(method, path, body, headers...)
		if r.Code != status {
			t.Fatalf("limited %s: %d want %d: %s", path, r.Code, status, r.Body)
		}
		return r
	}
	for _, path := range []string{"/api/v1/firm-profile", "/api/v1/deadlines", "/api/v1/calendar-events", "/api/v1/contact-addresses", "/api/v1/party-roles", "/api/v1/custom-field-definitions", "/api/v1/user-directory"} {
		r := mustLimited(http.StatusForbidden, "GET", path, nil)
		if !strings.Contains(r.Body, "scope_denied") {
			t.Fatal(r.Body)
		}
	}
	mustLimited(http.StatusNotFound, "GET", "/api/v1/matters/"+c.Walled, nil, "X-Togavsu-Event-Authorization", grant)
	// Assert each service read adds exactly one operation/path/module audit row.
	for _, tc := range []struct{ path, operation string }{
		{"/api/v1/calendar-events/" + openEvent, "getCalendarEvent"},
		{"/api/v1/deadlines/" + openDeadline, "getDeadline"},
		{"/api/v1/contact-addresses/" + str(t, address.JSON, "id"), "getContactAddress"},
		{"/api/v1/party-roles", "listPartyRole"}, {"/api/v1/custom-field-definitions", "listCustomFieldDefinition"},
		{"/api/v1/firm-profile", "getFirmProfile"}, {"/api/v1/user-directory", "listUserDirectory"},
		{"/api/v1/matter-parties", "listMatterParty"},
	} {
		query := `SELECT COUNT(*) FROM core.audit_log WHERE entity='service_call' AND source='service' AND field='operation' AND new_value=$1 AND reason='token=' || (SELECT token_id FROM core.modules WHERE id=$3) || ' module=' || $3 || ' service.call record=' || $2`
		record := tc.path
		before := c.Count(query, tc.operation, record, id)
		mustBot(http.StatusOK, "GET", tc.path, nil)
		if after := c.Count(query, tc.operation, record, id); after != before+1 {
			t.Fatalf("exact audit row %s: before %d after %d", tc.operation, before, after)
		}
	}
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/modules/"+id+"/disable", nil)
	mustBot(http.StatusUnauthorized, "GET", "/api/v1/calendar-events/"+wallEvent, nil, "X-Togavsu-Event-Authorization", grant)
}

func TestCoreUpdate2DContractForwardingTargetAndLimit(t *testing.T) {
	c := coretest.New(t)
	const contract = "phone.provider.v1"
	var mu sync.Mutex
	var forwarded string
	var consumerEvents, otherEvents []map[string]any
	provider := func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			if err := modulekit.VerifyWebhook(m.WebhookSecret, r.Header, raw, time.Now(), 0); err != nil {
				t.Errorf("signature %v", err)
			}
			if r.Header.Get(modulekit.TimestampHeader) == "" {
				t.Error("missing contract timestamp")
			}
			tok := r.Header.Get(modulekit.ContextHeader)
			if r.URL.Path == "/large" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"synthetic":"` + strings.Repeat("x", 100) + `"}`))
				return
			}
			cli := c.NewClient()
			cli.Bearer = m.Token
			read := cli.Do("GET", "/api/v1/matters/"+c.Walled, nil, modulekit.ContextHeader, tok)
			mu.Lock()
			forwarded = tok
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"read_status": read.Code})
		})
	}
	c.Serve("provider-two-d", provider)
	collect := func(dst *[]map[string]any) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var v map[string]any
			_ = json.NewDecoder(r.Body).Decode(&v)
			if v["contract"] == contract {
				mu.Lock()
				*dst = append(*dst, v)
				mu.Unlock()
			}
			w.WriteHeader(204)
		})
	}
	c.Handle("consumer-two-d", collect(&consumerEvents))
	c.Handle("other-two-d", collect(&otherEvents))
	install2d(t, c, "provider-two-d", []string{"contracts:provide"}, "provides:\n  - contract: "+contract+"\n    operations:\n      - name: read\n        path: /read\n      - name: large\n        path: /large\n")
	for _, id := range []string{"consumer-two-d", "other-two-d"} {
		install2d(t, c, id, []string{"contracts:call"}, "consumes:\n  - contract: "+contract+"\n")
	}
	links := map[string]string{}
	for _, id := range []string{"consumer-two-d", "other-two-d"} {
		link := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": contract, "consumer_id": id, "provider_id": "provider-two-d"})
		links[id] = str(t, link.JSON, "id")
	}
	consumer := c.ModuleClient("consumer-two-d")
	prov := c.ModuleClient("provider-two-d")
	viewer := c.ContextFor(coretest.Attorney, "consumer-two-d", c.Walled)
	got := consumer.Do("POST", "/api/v1/modules/contracts/"+contract+"/read", map[string]any{}, modulekit.ContextHeader, viewer)
	if got.Code != 200 || got.JSON["read_status"] != float64(200) {
		t.Fatalf("provider delegated read: %d %s", got.Code, got.Body)
	}
	mu.Lock()
	token := forwarded
	mu.Unlock()
	if token == "" || token == viewer {
		t.Fatal("forwarded token was not audience-bound")
	}
	raw := prov.Do("GET", "/api/v1/matters/"+c.Walled, nil, modulekit.ContextHeader, viewer)
	if raw.Code != 403 {
		t.Fatalf("provider accepted direct consumer token: %d %s", raw.Code, raw.Body)
	}
	verified := prov.Do("POST", "/api/v1/modules/provider-two-d/context-tokens/verify", nil, modulekit.ContextHeader, token)
	if verified.Code != 200 || verified.JSON["viewer_user_id"] != c.Attorney.ID {
		t.Fatalf("verify forwarded context %d %s", verified.Code, verified.Body)
	}
	stolen := c.ModuleClient("other-two-d").Do("GET", "/api/v1/matters/"+c.Walled, nil, modulekit.ContextHeader, token)
	if stolen.Code != 403 {
		t.Fatal("unrelated module accepted forwarded token")
	}
	envelope := map[string]any{"id": "synthetic-ring-2d", "contract": contract, "consumer_id": "consumer-two-d", "occurred_at": time.Now().UTC().Format(time.RFC3339Nano), "action": contract + ".ring", "payload": map[string]any{"call_id": "synthetic-call"}}
	prov.Must(http.StatusNoContent, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope)
	prov.Must(http.StatusNoContent, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope) // same event id is idempotent
	c.DeliverEvents()
	mu.Lock()
	if len(consumerEvents) != 1 || len(otherEvents) != 0 {
		t.Fatalf("contract event broadcast/duplicate: consumer %d other %d", len(consumerEvents), len(otherEvents))
	}
	eventJSON, _ := json.Marshal(consumerEvents[0])
	wantJSON, _ := json.Marshal(envelope)
	mu.Unlock()
	if string(eventJSON) != string(wantJSON) {
		t.Fatalf("envelope rewritten: %s want %s", eventJSON, wantJSON)
	}
	envelope["consumer_id"] = "not-linked"
	prov.Must(http.StatusForbidden, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope)
	c.Admin.Must(http.StatusOK, "PATCH", "/api/v1/settings/modules.contract_call_max_bytes", map[string]any{"value": 32})
	limit := consumer.Do("POST", "/api/v1/modules/contracts/"+contract+"/large", map[string]any{})
	if limit.Code != http.StatusRequestEntityTooLarge || limit.JSON["code"] != "too_large" {
		t.Fatalf("contract response size: %d %s", limit.Code, limit.Body)
	}
	c.Admin.Must(http.StatusNoContent, "DELETE", "/api/v1/modules/contract-links/"+links["consumer-two-d"], nil)
	revoked := prov.Do("GET", "/api/v1/matters/"+c.Walled, nil, modulekit.ContextHeader, token)
	if revoked.Code != 403 {
		t.Fatalf("forwarded token survived unlink: %d %s", revoked.Code, revoked.Body)
	}
}

func upload2d(t *testing.T, who *coretest.User, matter string) map[string]any {
	t.Helper()
	data := []byte("Synthetic converter input")
	up := who.Must(http.StatusCreated, "POST", "/api/v1/file-uploads", map[string]any{"file_name": "synthetic.txt", "matter_id": matter, "size_bytes": len(data), "media_type": "text/plain"})
	id := str(t, up.JSON, "id")
	chunk := who.Raw("PUT", "/api/v1/file-uploads/"+id, data, "Content-Type", "application/octet-stream", "Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(data)-1, len(data)))
	if chunk.Code != http.StatusOK {
		t.Fatal(chunk.Body)
	}
	who.Must(http.StatusOK, "POST", "/api/v1/file-uploads/"+id+"/complete", map[string]any{})
	return who.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": id}).JSON
}

func TestCoreUpdate2DPurgeRestrictAndConverter(t *testing.T) {
	c := coretest.New(t)
	c.Handle("retention-two-d", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	install2d(t, c, "retention-two-d", nil, "", func(files map[string][]byte) {
		files["migrations/0002_retained.sql"] = []byte("CREATE TABLE retained_files (file_id TEXT PRIMARY KEY REFERENCES core.files(id) ON DELETE RESTRICT);")
	})
	file := upload2d(t, c.Admin, c.Open)
	id := str(t, file, "id")
	c.Admin.Must(http.StatusCreated, "POST", "/api/v1/files/"+id+"/conversions", map[string]any{"format": "pdf"})
	calls := c.Converter.Calls()
	if len(calls) != 1 || calls[0].To != "pdf" || string(calls[0].Input) != "Synthetic converter input" {
		t.Fatalf("converter stand-in calls: %+v", calls)
	}
	before := c.Count(`SELECT COUNT(*) FROM core.files`)
	c.Converter.SetResult("pdf", []byte("invalid output"))
	failed := c.Admin.Do("POST", "/api/v1/files/"+id+"/conversions", map[string]any{"format": "pdf"})
	if failed.Code < 400 || c.Count(`SELECT COUNT(*) FROM core.files`) != before {
		t.Fatalf("invalid converter output published %d %s", failed.Code, failed.Body)
	}
	c.Converter.Fail(fmt.Errorf("synthetic converter unavailable"))
	failed = c.Admin.Do("POST", "/api/v1/files/"+id+"/conversions", map[string]any{"format": "pdf"})
	if failed.Code != http.StatusInternalServerError || c.Count(`SELECT COUNT(*) FROM core.files`) != before {
		t.Fatalf("converter failure %d %s", failed.Code, failed.Body)
	}
	hidden := upload2d(t, c.Attorney, c.Walled)
	wallFile := str(t, hidden, "id")
	n := len(c.Converter.Calls())
	c.Staff.Must(http.StatusNotFound, "POST", "/api/v1/files/"+wallFile+"/conversions", map[string]any{"format": "pdf"})
	if len(c.Converter.Calls()) != n {
		t.Fatal("hidden file reached converter")
	}
	db := c.ModuleDB("retention-two-d")
	if _, err := db.Exec(`INSERT INTO retained_files(file_id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/files/"+id+"/trash", nil)
	held := c.Admin.Must(http.StatusConflict, "POST", "/api/v1/files/"+id+"/purge", map[string]any{"confirm_name": str(t, file, "name")})
	if !strings.Contains(held.Body, "retention-two-d") {
		t.Fatal("purge refusal does not name retaining module")
	}
	if c.Count(`SELECT COUNT(*) FROM core.files WHERE id=$1 AND purged_at=0`, id) != 1 {
		t.Fatal("held file was purged")
	}
	if _, err := db.Exec(`DELETE FROM retained_files WHERE file_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	c.Admin.Must(http.StatusNoContent, "POST", "/api/v1/files/"+id+"/purge", map[string]any{"confirm_name": str(t, file, "name")})
	c.Admin.Must(http.StatusNotFound, "GET", "/api/v1/files/"+id, nil)
}

func TestCoreUpdate2DInboundAuthorizationOptIn(t *testing.T) {
	c := coretest.New(t)
	var mu sync.Mutex
	var headers []http.Header
	c.Handle("push-two-d", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Clone())
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	install2d(t, c, "push-two-d", []string{"webhooks:authorization"}, "inbound_webhooks:\n  - id: pubsub\n    path: /push\n    forward_authorization: true\n  - id: ordinary\n    path: /push\n")
	cli := c.NewClient()
	for _, hook := range []string{"pubsub", "ordinary"} {
		r := cli.Do("POST", "/hooks/push-two-d/"+hook, map[string]any{"synthetic": "push"}, "Authorization", "Bearer synthetic-provider", "Cookie", "synthetic-cookie=value", modulekit.ContextHeader, "spoofed", "X-Togavsu-Event-Authorization", "spoofed")
		if r.Code != http.StatusAccepted {
			t.Fatalf("public webhook treated provider bearer as core auth: %d %s", r.Code, r.Body)
		}
	}
	mu.Lock()
	got := append([]http.Header(nil), headers...)
	mu.Unlock()
	if len(got) != 2 || got[0].Get("Authorization") != "Bearer synthetic-provider" || got[1].Get("Authorization") != "" {
		t.Fatalf("authorization opt-in %v", got)
	}
	for _, h := range got {
		if h.Get("Cookie") != "" || h.Get(modulekit.ContextHeader) != "" || h.Get("X-Togavsu-Event-Authorization") != "" || h.Get("X-Togavsu-Inbound-Hook") == "" {
			t.Fatal("hook forwarded core credentials or missed hook marker")
		}
	}
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/modules/push-two-d/disable", nil)
	cli.Must(http.StatusNotFound, "POST", "/hooks/push-two-d/pubsub", map[string]any{})
	mu.Lock()
	defer mu.Unlock()
	if len(headers) != 2 {
		t.Fatal("disabled module received provider auth")
	}
}

func TestCoreUpdate2DServiceWallsEveryRecord(t *testing.T) {
	c := coretest.New(t)
	const id = "event-kinds-two-d"
	var mu sync.Mutex
	var delivered int
	c.Serve(id, func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			if err := modulekit.VerifyWebhook(m.WebhookSecret, r.Header, raw, time.Now(), 0); err != nil {
				t.Error(err)
			}
			if r.Header.Get("X-Togavsu-Event-Authorization") != "" {
				t.Error("event created a grant")
			}
			mu.Lock()
			delivered++
			mu.Unlock()
			w.WriteHeader(204)
		})
	})
	install2d(t, c, id, []string{"contacts:read", "calendar:read", "deadlines:read", "notes:read", "files:read", "events:write", "users:directory"}, "")
	matter, contact := c.Matter("Walled event records", "event-only@synthetic.example", nil)
	// Emit records while open, then wall them before the module reads delivered events.
	note := c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/notes", map[string]any{"title": "WALL-NOTE", "body": "Synthetic note body", "matter_id": matter})
	deadline := c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"title": "WALL-DEADLINE", "due_on": time.Now().AddDate(0, 0, 1).UTC().Format("2006-01-02"), "responsible_user_id": c.Attorney.ID, "matter_id": matter})
	at := time.Now().Add(time.Hour).UTC()
	event := c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": "WALL-EVENT", "kind": "other", "starts_at": at.Format(time.RFC3339), "ends_at": at.Add(time.Hour).Format(time.RFC3339), "matter_id": matter})
	file := upload2d(t, c.Attorney, matter)
	address := c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/contact-addresses", map[string]any{"contact_id": contact, "label": "work", "line1": "WALL-ADDRESS"})
	phone := c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{"contact_id": contact, "label": "work", "e164": "+15555550128", "effective_from": "2026-01-01"})
	email := c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/contact-emails", map[string]any{"contact_id": contact, "label": "work", "email": "named@synthetic.example"})
	parties := c.Attorney.Must(http.StatusOK, "GET", "/api/v1/matter-parties?matter_id="+matter, nil)
	party := parties.JSON["data"].([]any)[0].(map[string]any)["id"].(string)
	c.DeliverEvents()
	mu.Lock()
	count := delivered
	mu.Unlock()
	if count == 0 {
		t.Fatal("no event delivered before wall")
	}
	c.Wall(matter, c.Attorney)
	bot := c.ModuleClient(id)
	// A module-appended event naming a walled contact without a matter grants nothing.
	appended := bot.Do("POST", "/api/v1/events", map[string]any{"action": id + ".contact_named", "record_type": "contact", "record_id": contact, "summary": "Synthetic reference"})
	if appended.Code != http.StatusCreated {
		t.Fatal(appended.Body)
	}
	c.DeliverEvents()
	records := map[string]string{matter: "matters", contact: "contacts", str(t, note.JSON, "id"): "notes", str(t, deadline.JSON, "id"): "deadlines", str(t, event.JSON, "id"): "calendar-events", str(t, file, "id"): "files", str(t, address.JSON, "id"): "contact-addresses", str(t, phone.JSON, "id"): "contact-phones", str(t, email.JSON, "id"): "contact-emails", party: "matter-parties"}
	inside := c.ContextFor(coretest.Attorney, id, matter)
	outside := c.ContextFor(coretest.Staff, id, "")
	for record, collection := range records {
		t.Run(collection, func(t *testing.T) {
			path := "/api/v1/" + collection + "/" + record
			for _, headers := range [][]string{nil, {"X-Togavsu-Event-Authorization", "legacy-delivered-event-token"}, {modulekit.ContextHeader, outside}} {
				got := bot.Do("GET", path, nil, headers...)
				if got.Code != http.StatusNotFound || !strings.Contains(got.Body, "not_found") {
					t.Fatalf("wall get %s: %d %s", collection, got.Code, got.Body)
				}
			}
			got := bot.Do("GET", path, nil, modulekit.ContextHeader, inside)
			if got.Code != http.StatusOK {
				t.Fatalf("inside get %s: %d %s", collection, got.Code, got.Body)
			}
			if strings.Contains(got.Body, "wall_member_user_ids") || strings.Contains(got.Body, `"walled"`) {
				t.Fatalf("membership disclosed: %s", got.Body)
			}
			for _, headers := range [][]string{nil, {"X-Togavsu-Event-Authorization", "legacy-delivered-event-token"}, {modulekit.ContextHeader, outside}} {
				list := bot.Do("GET", listPath2d(collection), nil, headers...)
				if list.Code != http.StatusOK || strings.Contains(list.Body, record) {
					t.Fatalf("wall list %s: %d %s", collection, list.Code, list.Body)
				}
			}
		})
	}
	for _, headers := range [][]string{nil, {modulekit.ContextHeader, outside}} {
		got := bot.Do("GET", "/api/v1/user-directory?matter_id="+matter, nil, headers...)
		if got.Code != http.StatusNotFound {
			t.Fatalf("directory wall: %d %s", got.Code, got.Body)
		}
	}
	c.Wall(matter)
	for record, collection := range records {
		got := bot.Do("GET", "/api/v1/"+collection+"/"+record, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("lifted get %s: %d %s", collection, got.Code, got.Body)
		}
		list := bot.Do("GET", listPath2d(collection), nil)
		if list.Code != http.StatusOK || !strings.Contains(list.Body, record) {
			t.Fatalf("lifted list %s: %d %s", collection, list.Code, list.Body)
		}
	}
	if got := bot.Do("GET", "/api/v1/user-directory?matter_id="+matter, nil); got.Code != http.StatusOK {
		t.Fatalf("lifted directory: %d %s", got.Code, got.Body)
	}
}

func listPath2d(collection string) string {
	if collection == "contacts" {
		return "/api/v1/contacts?email=event-only@synthetic.example"
	}
	return "/api/v1/" + collection
}
