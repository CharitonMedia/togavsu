package core

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestMeContactSelfEdit covers CORE-13 / MOD-POR-16: a client reads and changes their
// linked contact primary email, phone and mailing address with history rows, audit as
// the client actor, and a client-visible contact.updated timeline row. Sign-in email
// stays on /api/v1/me/email-change.
func TestMeContactSelfEdit(t *testing.T) {
	f := newFin(t)
	matter, contact := f.matter("Casey Estate", "casey.contact@client.example", nil)
	f.admin.must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{
		"contact_id": contact, "label": "mobile", "phone": "+15555550100", "e164": "+15555550100", "primary": true,
		"effective_from": "2026-01-01",
	})
	f.admin.must(http.StatusCreated, "POST", "/api/v1/contact-addresses", map[string]any{
		"contact_id": contact, "label": "mailing", "primary": true, "line1": "1 Oak St", "city": "St Louis",
		"region": "MO", "postal_code": "63101", "country": "US", "effective_from": "2026-01-01",
	})

	cli, clientID := enrollUser(t, f.app, f.srv.URL, f.admin, "Casey Client", "casey.signin@client.example", domain.RoleClient)
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/users/"+string(clientID), map[string]any{"linked_contact_id": contact})

	if r := f.staff.do("GET", "/api/v1/me/contact", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff get: %d %s", r.code, r.body)
	}
	if r := f.staff.do("PATCH", "/api/v1/me/contact", map[string]any{"phone": "+15555550999"}); r.code != http.StatusForbidden {
		t.Fatalf("staff patch: %d %s", r.code, r.body)
	}

	got := cli.must(http.StatusOK, "GET", "/api/v1/me/contact", nil)
	if str(t, got.json, "contact_id") != contact || str(t, got.json, "email") != "casey.contact@client.example" ||
		str(t, got.json, "phone") != "+15555550100" {
		t.Fatalf("get: %s", got.body)
	}
	addr, _ := got.json["address"].(map[string]any)
	if addr["line1"] != "1 Oak St" || addr["city"] != "St Louis" {
		t.Fatalf("address: %s", got.body)
	}
	me := cli.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if str(t, me.json, "email") != "casey.signin@client.example" {
		t.Fatalf("sign-in email: %s", me.body)
	}

	updated := cli.must(http.StatusOK, "PATCH", "/api/v1/me/contact", map[string]any{
		"phone": "+15555550199",
		"email": "casey.next@client.example",
		"address": map[string]any{
			"line1": "9 Pine Ave", "line2": "Apt 2", "city": "Clayton", "region": "MO",
			"postal_code": "63105", "country": "US",
		},
	})
	if str(t, updated.json, "phone") != "+15555550199" || str(t, updated.json, "email") != "casey.next@client.example" {
		t.Fatalf("patch: %s", updated.body)
	}
	addr, _ = updated.json["address"].(map[string]any)
	if addr["line1"] != "9 Pine Ave" || addr["city"] != "Clayton" {
		t.Fatalf("patched address: %s", updated.body)
	}
	if got := str(t, cli.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "email"); got != "casey.signin@client.example" {
		t.Fatalf("sign-in email changed: %s", got)
	}

	// History lives in practice_records; assert via the list APIs (MOD-POR-16).
	phones := items(t, f.admin.must(http.StatusOK, "GET", "/api/v1/contact-phones?contact_id="+contact, nil))
	var oldPhone, newPhone bool
	for _, row := range phones {
		switch row["e164"] {
		case "+15555550100":
			if row["primary"] == false && str(t, row, "effective_to") != "" {
				oldPhone = true
			}
		case "+15555550199":
			if row["primary"] == true {
				newPhone = true
			}
		}
	}
	if !oldPhone || !newPhone {
		t.Fatalf("phone history: %v", phones)
	}
	emails := items(t, f.admin.must(http.StatusOK, "GET", "/api/v1/contact-emails?contact_id="+contact, nil))
	var oldEmail, newEmail bool
	for _, row := range emails {
		switch row["email"] {
		case "casey.contact@client.example":
			if row["primary"] == false && str(t, row, "effective_to") != "" {
				oldEmail = true
			}
		case "casey.next@client.example":
			if row["primary"] == true {
				newEmail = true
			}
		}
	}
	if !oldEmail || !newEmail {
		t.Fatalf("email history: %v", emails)
	}
	addresses := items(t, f.admin.must(http.StatusOK, "GET", "/api/v1/contact-addresses?contact_id="+contact, nil))
	var oldAddr, newAddr bool
	for _, row := range addresses {
		switch row["line1"] {
		case "1 Oak St":
			if row["primary"] == false && str(t, row, "effective_to") != "" {
				oldAddr = true
			}
		case "9 Pine Ave":
			if row["primary"] == true {
				newAddr = true
			}
		}
	}
	if !oldAddr || !newAddr {
		t.Fatalf("address history: %v", addresses)
	}

	if n := f.count(`SELECT COUNT(*) FROM audit_log WHERE actor_id = $1 AND entity IN ('contact_phone','contact_email','contact_address','contact')`, string(clientID)); n < 1 {
		t.Fatalf("client audit rows: %d", n)
	}
	var actorKind string
	var visible int
	err := f.app.Pool.QueryRow(`SELECT actor_kind, client_visible FROM events WHERE action = 'contact.updated' AND record_id = $1 ORDER BY created_at DESC LIMIT 1`, contact).Scan(&actorKind, &visible)
	if err != nil || actorKind != "client" || visible != 1 {
		t.Fatalf("contact.updated: kind=%q visible=%d err=%v", actorKind, visible, err)
	}
	timeline := f.admin.must(http.StatusOK, "GET", "/api/v1/matters/"+matter+"/timeline", nil)
	if !strings.Contains(timeline.body, "contact.updated") || !strings.Contains(timeline.body, string(clientID)) {
		t.Fatalf("matter timeline missing client contact.updated: %s", timeline.body)
	}

	getTool := toolName(t, "GET", "/api/v1/me/contact")
	patchTool := toolName(t, "PATCH", "/api/v1/me/contact")
	if call := mcpCall(cli, getTool, map[string]any{}); !strings.Contains(call.body, "+15555550199") || strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("mcp get: %s", call.body)
	}
	if call := mcpCall(cli, patchTool, map[string]any{"body": map[string]any{"phone": "+15555550200"}}); !strings.Contains(call.body, "+15555550200") || strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("mcp patch: %s", call.body)
	}
}
