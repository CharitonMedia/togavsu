package core

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// importItem is one reconciled bulkInvoice item: one line of cents, paid and written
// off as given, balance due the rest.
func importItem(matter, number, status string, cents, paid, writeOff int64) map[string]any {
	balance := cents - paid - writeOff
	if status == "void" {
		balance = 0
	}
	item := map[string]any{
		"matter_id": matter, "number": number, "issued_on": time.Now().UTC().Format("2006-01-02"), "status": status,
		"lines":          []map[string]any{{"kind": "adjustment", "description": "Imported balance", "amount_cents": cents}},
		"subtotal_cents": cents, "adjustment_cents": 0, "interest_cents": 0, "total_cents": cents,
		"amount_paid_cents": paid, "balance_due_cents": balance, "write_off_cents": writeOff,
		"internal_notes": "imported from the old system",
	}
	if writeOff > 0 {
		item["write_off_reason"] = "settled"
	}
	if status == "void" {
		item["void_reason"] = "issued in error"
	}
	return item
}

// TestReleaseBulkInvoices is bulkInvoice (AC-002) on the running core: per-item
// privileges, exact-cents reconciliation, matter walls (the admin does not bypass), one
// transaction, Idempotency-Key replay and conflict, invoice.created per item, token
// scopes per item, and the same operation through /mcp.
func TestReleaseBulkInvoices(t *testing.T) {
	f := newFin(t)
	matter, _ := f.matter("Estate of Quill", "", nil)
	walled, _ := f.matter("Estate of Fenn", "", nil)
	f.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{f.attyID}})
	invoices := func() int { return f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`) }
	created := func() int { return f.count(`SELECT COUNT(*) FROM core.events WHERE action = 'invoice.created'`) }
	bulk := func(c *client, items ...map[string]any) resp {
		return c.do("POST", "/api/v1/invoices:bulk", map[string]any{"items": items})
	}

	// A draft whose time line names its source bills that row.
	entry := f.timeEntry(matter, 10, 300_00)
	draft := importItem(matter, "OLD-1", "draft", 3000_00, 0, 0)
	draft["lines"] = []map[string]any{{"kind": "time", "source_id": entry, "description": "Drafting", "amount_cents": 3000_00}}
	r := bulk(f.staff, draft)
	if r.code != http.StatusCreated {
		t.Fatalf("staff draft import: %d %s", r.code, r.body)
	}
	rows := r.json["data"].([]any)
	first := rows[0].(map[string]any)
	if first["number"] != "OLD-1" || first["status"] != "draft" || first["internal_notes"] != "imported from the old system" {
		t.Fatalf("imported draft: %v", first)
	}
	if got := str(t, f.atty.must(http.StatusOK, "GET", "/api/v1/time-entries/"+entry, nil).json, "invoice_id"); got != first["id"] {
		t.Fatalf("time entry invoice_id %q, want %v", got, first["id"])
	}
	if n := f.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'invoice' AND entity_id = $1`, first["id"]); n == 0 {
		t.Fatal("the imported invoice has no audit rows")
	}
	if invoices() != 1 || created() != 1 {
		t.Fatalf("after one item: %d invoices, %d invoice.created", invoices(), created())
	}

	// Staff cannot import a sent invoice (sendInvoice is attorney or admin); the whole
	// batch is refused before anything is written.
	if r := bulk(f.staff, importItem(matter, "OLD-2", "draft", 100_00, 0, 0), importItem(matter, "OLD-3", "sent", 100_00, 0, 0)); r.code != http.StatusForbidden {
		t.Fatalf("staff importing a sent invoice: %d %s", r.code, r.body)
	}
	// A batch whose second item does not reconcile writes nothing. An item that fails
	// validation or reconciliation is batch_rejected (422), the bulk contract's code;
	// Codex PR #55.
	rejected := func(r resp) bool {
		return r.code == http.StatusUnprocessableEntity && r.json["code"] == "batch_rejected"
	}
	bad := importItem(matter, "OLD-5", "sent", 500_00, 100_00, 0)
	bad["balance_due_cents"] = 450_00
	if r := bulk(f.atty, importItem(matter, "OLD-4", "paid", 200_00, 200_00, 0), bad); !rejected(r) || !strings.Contains(r.body, "balance_due_cents") {
		t.Fatalf("unreconciled batch: %d %s", r.code, r.body)
	}
	sub := importItem(matter, "OLD-6", "draft", 100_00, 0, 0)
	sub["subtotal_cents"] = 99_00
	if r := bulk(f.atty, sub); !rejected(r) || !strings.Contains(r.body, "subtotal_cents") {
		t.Fatalf("unreconciled subtotal: %d %s", r.code, r.body)
	}
	if r := bulk(f.atty, importItem(matter, "OLD-1", "draft", 100_00, 0, 0)); !rejected(r) {
		t.Fatalf("a used invoice number: %d %s", r.code, r.body)
	}
	if r := bulk(f.atty, importItem(matter, "OLD-7", "draft", 100_00, 50_00, 0)); !rejected(r) {
		t.Fatalf("a draft with a payment: %d %s", r.code, r.body)
	}
	// A request-shape error is still an ordinary validation error.
	if r := bulk(f.atty); r.code != http.StatusBadRequest || r.json["code"] != "validation" {
		t.Fatalf("an empty batch: %d %s", r.code, r.body)
	}
	// The wall: staff outside it, and the admin too, get not_found for the batch.
	for _, c := range []*client{f.staff, f.admin} {
		if r := bulk(c, importItem(matter, "OLD-8", "draft", 100_00, 0, 0), importItem(walled, "OLD-9", "draft", 100_00, 0, 0)); r.code != http.StatusNotFound {
			t.Fatalf("walled matter in the batch: %d %s", r.code, r.body)
		}
	}
	if invoices() != 1 || created() != 1 {
		t.Fatalf("refused batches wrote: %d invoices, %d invoice.created", invoices(), created())
	}

	// The attorney on the wall imports sent, viewed, paid with a write-off, and void in one batch.
	batch := []map[string]any{
		importItem(walled, "OLD-10", "sent", 800_00, 300_00, 0), importItem(walled, "OLD-11", "viewed", 400_00, 0, 0),
		importItem(walled, "OLD-12", "paid", 600_00, 500_00, 100_00), importItem(walled, "OLD-13", "void", 250_00, 0, 0),
	}
	body := map[string]any{"items": batch}
	r = f.atty.do("POST", "/api/v1/invoices:bulk", body, "Idempotency-Key", "import-1")
	if r.code != http.StatusCreated || len(r.json["data"].([]any)) != 4 {
		t.Fatalf("attorney batch: %d %s", r.code, r.body)
	}
	for _, row := range r.json["data"].([]any) {
		inv := row.(map[string]any)
		switch inv["number"] {
		case "OLD-10":
			if num(t, inv, "balance_due_cents") != 500_00 || inv["sent_at"] == nil {
				t.Fatalf("sent import: %v", inv)
			}
		case "OLD-12":
			if inv["status"] != "paid" || num(t, inv, "write_off_cents") != 100_00 || inv["paid_at"] == nil {
				t.Fatalf("paid import: %v", inv)
			}
		}
	}
	if invoices() != 5 || created() != 5 {
		t.Fatalf("after the batch: %d invoices, %d invoice.created", invoices(), created())
	}
	again := f.atty.do("POST", "/api/v1/invoices:bulk", body, "Idempotency-Key", "import-1")
	if again.code != http.StatusCreated || strings.TrimSpace(again.body) != strings.TrimSpace(r.body) {
		t.Fatalf("replay: %d %s", again.code, again.body)
	}
	if invoices() != 5 || created() != 5 {
		t.Fatal("a replay wrote or emitted")
	}
	if r := f.atty.do("POST", "/api/v1/invoices:bulk", map[string]any{"items": batch[:1]}, "Idempotency-Key", "import-1"); r.code != http.StatusConflict || !strings.Contains(r.body, "idempotency_conflict") {
		t.Fatalf("same key, different body: %d %s", r.code, r.body)
	}
	// Staff outside the wall still cannot read the walled imports.
	if r := f.staff.do("GET", "/api/v1/invoices?matter_id="+walled, nil); strings.Contains(r.body, "OLD-10") {
		t.Fatal("staff outside the wall listed a walled import")
	}

	// A token needs each item's own scopes: invoices:write imports a draft, not a sent invoice.
	writer := newClient(t, f.srv.URL)
	writer.bearer = str(t, f.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{
		"name": "importer", "owner_user_id": f.attyID, "scopes": []string{"invoices:write"}}).json, "secret")
	if r := bulk(writer, importItem(matter, "OLD-20", "sent", 100_00, 0, 0)); r.code != http.StatusForbidden {
		t.Fatalf("token without invoices:send: %d %s", r.code, r.body)
	}
	if r := bulk(writer, importItem(matter, "OLD-21", "draft", 100_00, 0, 0)); r.code != http.StatusCreated {
		t.Fatalf("token draft import: %d %s", r.code, r.body)
	}

	// The same operation through /mcp.
	rpc := writer.must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": "bulkInvoice", "arguments": map[string]any{"body": map[string]any{"items": []map[string]any{importItem(matter, "OLD-22", "draft", 100_00, 0, 0)}}}}})
	res, _ := rpc.json["result"].(map[string]any)
	if res == nil || res["isError"] != false || !strings.Contains(rpc.body, "OLD-22") {
		t.Fatalf("bulkInvoice over MCP: %s", rpc.body)
	}
	if invoices() != 7 || created() != 7 {
		t.Fatalf("after the token imports: %d invoices, %d invoice.created", invoices(), created())
	}
}
