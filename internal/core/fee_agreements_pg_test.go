package core

import (
	"net/http"
	"strings"
	"testing"
)

// TestReleaseFeeAgreements covers fee agreements on the core (PRD 5, AC-002): create,
// read, list, edit with its status rules, edit history, bulk in one transaction, one
// active agreement per matter, walls, and the accountant and token refusals.
func TestReleaseFeeAgreements(t *testing.T) {
	f := newFin(t)
	matter, _ := f.matter("Estate of Tamsin", "tamsin@client.example", nil)
	walled, _ := f.matter("Estate of Orrin", "orrin@client.example", nil)
	f.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{f.adminID, f.attyID}})

	if r := f.staff.do("POST", "/api/v1/fee-agreements", map[string]any{"matter_id": matter, "fee_type": "flat", "services_description": "Probate"}); r.code != http.StatusBadRequest {
		t.Fatalf("flat fee without an amount: %d %s", r.code, r.body)
	}
	if r := f.staff.do("POST", "/api/v1/fee-agreements", map[string]any{"matter_id": matter, "fee_type": "barter", "services_description": "x"}); r.code != http.StatusBadRequest {
		t.Fatalf("unknown fee type: %d", r.code)
	}
	created := f.staff.must(http.StatusCreated, "POST", "/api/v1/fee-agreements", map[string]any{
		"matter_id": matter, "fee_type": "flat", "amount_cents": 250000, "services_description": "Uncontested probate"})
	id := str(t, created.json, "id")
	if created.json["status"] != "draft" || created.json["amount_cents"] != float64(250000) || created.json["signed_on"] != nil {
		t.Fatal(created.body)
	}
	if r := f.staff.do("POST", "/api/v1/fee-agreements", map[string]any{"matter_id": matter, "fee_type": "hourly", "services_description": "Second"}); r.code != http.StatusConflict {
		t.Fatalf("second active agreement on a matter: %d %s", r.code, r.body)
	}
	if r := f.staff.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "signed"}); r.code != http.StatusBadRequest {
		t.Fatalf("signed without a date: %d", r.code)
	}
	f.staff.must(http.StatusOK, "PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "sent"})
	signed := f.atty.must(http.StatusOK, "PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "signed", "signed_on": "2026-11-02"})
	if signed.json["status"] != "signed" || signed.json["signed_on"] != "2026-11-02" {
		t.Fatal(signed.body)
	}
	if r := f.staff.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "draft"}); r.code != http.StatusBadRequest {
		t.Fatalf("signed back to draft: %d", r.code)
	}
	if r := f.staff.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"matter_id": walled}); r.code != http.StatusBadRequest {
		t.Fatalf("moved to another matter: %d", r.code)
	}
	if r := f.staff.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "void"}); r.code != http.StatusBadRequest {
		t.Fatalf("void without a reason: %d", r.code)
	}
	f.atty.must(http.StatusOK, "PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "void", "void_reason": "Client changed scope"})
	if r := f.staff.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "sent"}); r.code != http.StatusBadRequest {
		t.Fatalf("a void agreement came back: %d", r.code)
	}
	// After the void, the matter takes a new agreement.
	f.staff.must(http.StatusCreated, "POST", "/api/v1/fee-agreements", map[string]any{"matter_id": matter, "fee_type": "hourly", "services_description": "Hourly probate"})

	hist := f.staff.must(http.StatusOK, "GET", "/api/v1/fee-agreements/"+id+"/history", nil)
	for _, want := range []string{"Client changed scope", "signed", "2026-11-02", "void"} {
		if !strings.Contains(hist.body, want) {
			t.Fatalf("history misses %q: %s", want, hist.body)
		}
	}
	list := f.staff.must(http.StatusOK, "GET", "/api/v1/fee-agreements?matter_id="+matter, nil)
	if n := len(items(t, list)); n != 2 {
		t.Fatalf("want 2 agreements on the matter, got %d: %s", n, list.body)
	}
	if r := f.staff.must(http.StatusOK, "GET", "/api/v1/fee-agreements?status=void", nil); len(items(t, r)) != 1 {
		t.Fatalf("status filter: %s", r.body)
	}

	// Walls: the attorney inside sees the walled agreement; staff outside gets nothing.
	wid := str(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/fee-agreements", map[string]any{"matter_id": walled, "fee_type": "contingency", "services_description": "Wrongful death"}).json, "id")
	if r := f.staff.do("GET", "/api/v1/fee-agreements/"+wid, nil); r.code != http.StatusNotFound {
		t.Fatalf("staff read a walled agreement: %d", r.code)
	}
	if r := f.staff.must(http.StatusOK, "GET", "/api/v1/fee-agreements", nil); strings.Contains(r.body, wid) {
		t.Fatal("a walled agreement is in staff's list")
	}
	if r := f.staff.do("POST", "/api/v1/fee-agreements", map[string]any{"matter_id": walled, "fee_type": "hourly", "services_description": "x"}); r.code != http.StatusNotFound {
		t.Fatalf("staff wrote to a walled matter: %d", r.code)
	}
	if r := f.staff.do("GET", "/api/v1/fee-agreements/"+wid+"/history", nil); r.code != http.StatusNotFound {
		t.Fatalf("staff read a walled agreement's history: %d", r.code)
	}

	// The accountant and the client get none of it.
	if r := f.acct.do("GET", "/api/v1/fee-agreements", nil); r.code != http.StatusForbidden {
		t.Fatalf("accountant listed fee agreements: %d", r.code)
	}
	if r := f.acct.do("POST", "/api/v1/fee-agreements", map[string]any{"matter_id": matter, "fee_type": "hourly", "services_description": "x"}); r.code != http.StatusForbidden {
		t.Fatalf("accountant wrote a fee agreement: %d", r.code)
	}

	// Bulk: one bad item rejects the whole batch.
	other, _ := f.matter("Estate of Pell", "pell@client.example", nil)
	third, _ := f.matter("Estate of Quill", "quill@client.example", nil)
	bad := f.staff.do("POST", "/api/v1/fee-agreements:bulk", map[string]any{"items": []any{
		map[string]any{"matter_id": other, "fee_type": "hourly", "services_description": "ok"},
		map[string]any{"matter_id": third, "fee_type": "flat", "services_description": "no amount"},
	}})
	if bad.code != http.StatusBadRequest || !strings.Contains(bad.body, "items[1]") {
		t.Fatalf("bad batch: %d %s", bad.code, bad.body)
	}
	if r := f.staff.must(http.StatusOK, "GET", "/api/v1/fee-agreements?matter_id="+other, nil); len(items(t, r)) != 0 {
		t.Fatal("a rejected batch left an agreement")
	}
	good := f.staff.must(http.StatusCreated, "POST", "/api/v1/fee-agreements:bulk", map[string]any{"items": []any{
		map[string]any{"matter_id": other, "fee_type": "hourly", "services_description": "ok"},
		map[string]any{"matter_id": third, "fee_type": "pro_bono", "services_description": "free"},
	}})
	if n := len(good.json["data"].([]any)); n != 2 {
		t.Fatal(good.body)
	}

	// A token needs matters:read to read and matters:write to write; and /mcp works.
	issued := f.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "fa", "scopes": []string{"matters:read"}})
	bot := newClient(t, f.srv.URL)
	bot.bearer = str(t, issued.json, "secret")
	bot.must(http.StatusOK, "GET", "/api/v1/fee-agreements?matter_id="+matter, nil)
	if r := bot.do("POST", "/api/v1/fee-agreements", map[string]any{"matter_id": other, "fee_type": "hourly", "services_description": "x"}); r.code != http.StatusForbidden {
		t.Fatalf("read token wrote: %d", r.code)
	}
	call := mcpCall(f.staff, toolName(t, "GET", "/api/v1/fee-agreements/{fee_agreement_id}"), map[string]any{"path": map[string]any{"fee_agreement_id": id}})
	if !strings.Contains(call.body, "Uncontested probate") || strings.Contains(call.body, `"isError":true`) {
		t.Fatal(call.body)
	}
}
