package core

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// TestClientTrustReads covers CORE-12: /me/trust, /me/trust-ledger, and client
// replenishment list/get with the TrustReplenishmentClient allowlist.
func TestClientTrustReads(t *testing.T) {
	f := newFin(t)
	mine, contactID := f.matter("Mine Trust v. Acme", "cora.trust@client.example", nil)
	other, otherContact := f.matter("Other Trust v. Beta", "other.trust@client.example", nil)

	f.post(f.admin, mine, contactID, "deposit", 50000)
	f.post(f.admin, other, otherContact, "deposit", 90000)

	mineRepl := f.admin.must(http.StatusCreated, "POST", "/api/v1/trust-replenishment-requests", map[string]any{
		"matter_id": mine, "client_id": contactID, "amount_requested_cents": 25000, "message": "please fund Mine",
	})
	mineReplID := str(t, mineRepl.json, "id")
	otherRepl := f.admin.must(http.StatusCreated, "POST", "/api/v1/trust-replenishment-requests", map[string]any{
		"matter_id": other, "client_id": otherContact, "amount_requested_cents": 11000, "message": "other only",
	})
	otherReplID := str(t, otherRepl.json, "id")

	cli, _ := testkit.EnrollUser(t, f.app.Identity, f.srv.URL, f.admin.kit(), "Cora Trust", "cora.trust@client.example", domain.ID(contactID), domain.RoleClient)
	client := wrapClient(cli)

	if r := f.staff.do("GET", "/api/v1/me/trust", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff me/trust: %d %s", r.code, r.body)
	}

	summary := client.must(http.StatusOK, "GET", "/api/v1/me/trust", nil)
	rows := items(t, summary)
	if len(rows) != 1 {
		t.Fatalf("me/trust want 1 matter got %d: %s", len(rows), summary.body)
	}
	row := rows[0]
	if str(t, row, "matter_id") != mine {
		t.Fatalf("matter_id: %s", summary.body)
	}
	if num(t, row, "balance_cents") != 50000 {
		t.Fatalf("balance_cents: %s", summary.body)
	}
	repls, _ := row["replenishment_requests"].([]any)
	if len(repls) != 1 {
		t.Fatalf("active replenishments: %s", summary.body)
	}
	repl, _ := repls[0].(map[string]any)
	for _, leak := range []string{"client_id", "amount_requested_cents", "threshold_cents", "invoice_id", "created_by", "updated_at"} {
		if _, ok := repl[leak]; ok {
			t.Fatalf("me/trust replenishment leaked %s: %v", leak, repl)
		}
	}
	for _, need := range []string{"id", "matter_id", "amount_cents", "message", "status", "created_at"} {
		if repl[need] == nil || repl[need] == "" {
			t.Fatalf("missing %s: %v", need, repl)
		}
	}
	if num(t, repl, "amount_cents") != 25000 || str(t, repl, "status") != "active" {
		t.Fatalf("replenishment fields: %v", repl)
	}

	ledger := client.must(http.StatusOK, "GET", "/api/v1/me/trust-ledger?matter_id="+mine, nil)
	entries := items(t, ledger)
	if len(entries) != 1 {
		t.Fatalf("ledger entries: %s", ledger.body)
	}
	entry := entries[0]
	for _, leak := range []string{"trust_account_id", "client_id", "last_four", "created_by"} {
		if _, ok := entry[leak]; ok {
			t.Fatalf("ledger leaked %s: %v", leak, entry)
		}
	}
	for _, need := range []string{"date", "type", "amount_cents", "description"} {
		if entry[need] == nil || entry[need] == "" {
			t.Fatalf("ledger missing %s: %v", need, entry)
		}
	}
	if str(t, entry, "type") != "deposit" || num(t, entry, "amount_cents") != 50000 {
		t.Fatalf("ledger entry: %v", entry)
	}
	if r := client.do("GET", "/api/v1/me/trust-ledger?matter_id="+other, nil); r.code != http.StatusNotFound {
		t.Fatalf("foreign ledger: %d %s", r.code, r.body)
	}
	if r := client.do("GET", "/api/v1/me/trust-ledger", nil); r.code != http.StatusBadRequest {
		t.Fatalf("missing matter_id: %d %s", r.code, r.body)
	}

	list := client.must(http.StatusOK, "GET", "/api/v1/trust-replenishment-requests", nil)
	listed := items(t, list)
	if len(listed) != 1 || str(t, listed[0], "id") != mineReplID {
		t.Fatalf("client list replenishment: %s", list.body)
	}
	if _, ok := listed[0]["amount_requested_cents"]; ok {
		t.Fatalf("list leaked amount_requested_cents: %v", listed[0])
	}
	if num(t, listed[0], "amount_cents") != 25000 {
		t.Fatalf("list amount_cents: %v", listed[0])
	}

	one := client.must(http.StatusOK, "GET", "/api/v1/trust-replenishment-requests/"+mineReplID, nil)
	if num(t, one.json, "amount_cents") != 25000 || one.json["client_id"] != nil {
		t.Fatalf("get allowlist: %s", one.body)
	}
	if r := client.do("GET", "/api/v1/trust-replenishment-requests/"+otherReplID, nil); r.code != http.StatusNotFound {
		t.Fatalf("other replenishment: %d %s", r.code, r.body)
	}

	// Firm ledger and balances stay staff-facing.
	if r := client.do("GET", "/api/v1/trust-ledger?matter_id="+mine, nil); r.code != http.StatusNotFound {
		t.Fatalf("client firm ledger: %d %s", r.code, r.body)
	}
	if r := client.do("GET", "/api/v1/trust/balances?matter_id="+mine, nil); r.code != http.StatusNotFound {
		t.Fatalf("client firm balances: %d %s", r.code, r.body)
	}

	tool := toolName(t, "GET", "/api/v1/me/trust")
	mcp := mcpCall(client, tool, map[string]any{})
	if mcp.code != http.StatusOK || !strings.Contains(mcp.body, mine) || strings.Contains(mcp.body, other) {
		t.Fatalf("mcp me/trust: %s", mcp.body)
	}
	replTool := toolName(t, "GET", "/api/v1/trust-replenishment-requests")
	mcpList := mcpCall(client, replTool, map[string]any{})
	if mcpList.code != http.StatusOK || !strings.Contains(mcpList.body, mineReplID) || strings.Contains(mcpList.body, otherReplID) {
		t.Fatalf("mcp list replenishment: %s", mcpList.body)
	}
}
