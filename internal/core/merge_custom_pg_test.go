package core

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func eventCount(f *fin, action, record string) int {
	f.t.Helper()
	return f.count(`SELECT COUNT(*) FROM core.events WHERE action = $1 AND record_id = $2`, action, record)
}

// TestReleaseArchiveAndMergeMatters is AC-002 for archiveMatter, mergeMatter and
// mergeContact on the running core: roles, walls (an admin does not pass one), the
// archived flag in lists and in core.matters, one event per change, Idempotency-Key
// replay, and the same operations over /mcp.
func TestReleaseArchiveAndMergeMatters(t *testing.T) {
	f := newFin(t)
	a, _ := f.matter("Estate of Archive", "", nil)
	b, _ := f.matter("Estate of Merged", "", nil)
	c, _ := f.matter("Estate of Survivor", "", nil)
	w, _ := f.matter("Estate of Walled", "", nil)
	f.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+w+"/wall", map[string]any{"user_ids": []string{f.attyID}})

	// archiveMatter: staff may; the accountant may not; a walled matter is not_found,
	// for an admin off the wall too.
	if r := f.acct.do("POST", "/api/v1/matters/"+a+"/archive", nil); r.code != http.StatusForbidden {
		t.Fatalf("accountant archived: %d %s", r.code, r.body)
	}
	for _, c := range []*client{f.staff, f.admin} {
		if r := c.do("POST", "/api/v1/matters/"+w+"/archive", nil); r.code != http.StatusNotFound {
			t.Fatalf("archive behind a wall: %d %s", r.code, r.body)
		}
	}
	got := f.staff.must(http.StatusOK, "POST", "/api/v1/matters/"+a+"/archive", nil)
	if got.json["archived"] != true || got.json["status"] != "open" {
		t.Fatalf("archived matter: %s", got.body)
	}
	f.staff.must(http.StatusOK, "POST", "/api/v1/matters/"+a+"/archive", nil)
	if n := eventCount(f, "matter.archived", a); n != 1 {
		t.Fatalf("matter.archived emitted %d times", n)
	}
	if f.count(`SELECT COUNT(*) FROM core.matters WHERE id = $1 AND archived = 1`, a) != 1 {
		t.Fatal("core.matters does not carry the archived flag")
	}
	if r := f.staff.must(http.StatusOK, "GET", "/api/v1/matters/"+a, nil); r.json["archived"] != true {
		t.Fatalf("an archived matter is not readable as archived: %s", r.body)
	}
	listed := func(query string) map[string]bool {
		out := map[string]bool{}
		for _, m := range items(t, f.staff.must(http.StatusOK, "GET", "/api/v1/matters"+query, nil)) {
			out[m["id"].(string)] = true
		}
		return out
	}
	if l := listed(""); l[a] || !l[b] {
		t.Fatalf("default list: %v", l)
	}
	if l := listed("?archived=true"); !l[a] || l[b] {
		t.Fatalf("archived=true list: %v", l)
	}
	if r := f.staff.do("GET", "/api/v1/matters?archived=maybe", nil); r.code != http.StatusBadRequest {
		t.Fatalf("bad archived filter: %d", r.code)
	}
	// Codex PR #55: the CSV export and the Matters screen keep the same default and
	// filter as the JSON list.
	csv := func(query string) string {
		return f.staff.must(http.StatusOK, "GET", "/api/v1/matters?format=csv"+query, nil).body
	}
	if out := csv(""); strings.Contains(out, "Estate of Archive") || !strings.Contains(out, "Estate of Merged") {
		t.Fatalf("default CSV export: %s", out)
	}
	if out := csv("&archived=true"); !strings.Contains(out, "Estate of Archive") || strings.Contains(out, "Estate of Merged") {
		t.Fatalf("archived=true CSV export: %s", out)
	}
	if r := f.staff.do("GET", "/api/v1/matters?format=csv&archived=maybe", nil); r.code != http.StatusBadRequest {
		t.Fatalf("bad archived filter on the CSV export: %d", r.code)
	}
	if page := f.staff.must(http.StatusOK, "GET", "/practice/matters", nil).body; strings.Contains(page, "Estate of Archive") || !strings.Contains(page, "Estate of Merged") {
		t.Fatal("the Matters screen lists an archived matter by default")
	}
	if page := f.staff.must(http.StatusOK, "GET", "/practice/matters?archived=true", nil).body; !strings.Contains(page, "Estate of Archive") {
		t.Fatal("the Matters screen hides archived matters when asked for them")
	}

	// mergeMatter: attorney or admin only, both matters visible, never into itself.
	merge := func(c *client, from, into string, hdr ...string) resp {
		return c.do("POST", "/api/v1/matters/"+from+"/merge", map[string]any{"survivor_id": into}, hdr...)
	}
	if r := merge(f.staff, b, c); r.code != http.StatusForbidden {
		t.Fatalf("staff merged: %d %s", r.code, r.body)
	}
	if r := merge(f.admin, b, w); r.code != http.StatusNotFound {
		t.Fatalf("admin merged into a walled matter: %d %s", r.code, r.body)
	}
	if r := merge(f.admin, w, b); r.code != http.StatusNotFound {
		t.Fatalf("admin merged a walled matter: %d %s", r.code, r.body)
	}
	if r := merge(f.atty, b, b); r.code != http.StatusBadRequest {
		t.Fatalf("merge into itself: %d %s", r.code, r.body)
	}
	if r := merge(f.atty, b, "01a1c38b-0000-7000-8000-000000000000"); r.code != http.StatusNotFound {
		t.Fatalf("merge into a missing matter: %d %s", r.code, r.body)
	}
	if r := f.atty.do("POST", "/api/v1/matters/"+b+"/merge", map[string]any{"survivor_id": c, "hard_delete": true}); r.code != http.StatusBadRequest {
		t.Fatalf("unknown merge field: %d %s", r.code, r.body)
	}
	if eventCount(f, "matter.merged", b) != 0 || f.count(`SELECT COUNT(*) FROM core.matters WHERE id = $1 AND archived = 1`, b) != 0 {
		t.Fatal("a refused merge archived or emitted")
	}
	res := merge(f.atty, b, c, "Idempotency-Key", "merge-b-into-c")
	if res.code != http.StatusOK || res.json["survivor_id"] != c || res.json["archived_id"] != b {
		t.Fatalf("merge: %d %s", res.code, res.body)
	}
	if moved, ok := res.json["modules_relinked"].([]any); !ok || len(moved) != 0 {
		t.Fatalf("modules_relinked with no modules: %s", res.body)
	}
	again := merge(f.atty, b, c, "Idempotency-Key", "merge-b-into-c")
	if again.code != http.StatusOK || again.body != res.body || eventCount(f, "matter.merged", b) != 1 {
		t.Fatalf("replay: %d %s (events %d)", again.code, again.body, eventCount(f, "matter.merged", b))
	}
	if r := f.atty.must(http.StatusOK, "GET", "/api/v1/matters/"+b, nil); r.json["archived"] != true {
		t.Fatalf("merged matter is not archived: %s", r.body)
	}
	if r := merge(f.atty, b, c); r.code != http.StatusConflict {
		t.Fatalf("merge an archived matter again: %d %s", r.code, r.body)
	}

	// mergeContact.
	contact := func(family string) string {
		return str(t, f.admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Pat", "family_name": family}).json, "id")
	}
	x, y := contact("Duplicate"), contact("Keeper")
	if r := f.staff.do("POST", "/api/v1/contacts/"+x+"/merge", map[string]any{"survivor_id": y}); r.code != http.StatusForbidden {
		t.Fatalf("staff merged contacts: %d %s", r.code, r.body)
	}
	if r := f.admin.do("POST", "/api/v1/contacts/"+x+"/merge", map[string]any{"survivor_id": "01a1c38b-0000-7000-8000-000000000000"}); r.code != http.StatusNotFound {
		t.Fatalf("contact merge into a missing contact: %d %s", r.code, r.body)
	}
	// Over /mcp, the same operation and rules.
	call := mcpCall(f.atty, "mergeContact", map[string]any{"path": map[string]any{"contact_id": x}, "body": map[string]any{"survivor_id": y}})
	if call.code != http.StatusOK || strings.Contains(call.body, `"isError":true`) || !strings.Contains(call.body, `\"archived_id\":\"`+x) {
		t.Fatalf("mergeContact over MCP: %s", call.body)
	}
	if r := f.atty.must(http.StatusOK, "GET", "/api/v1/contacts/"+x, nil); r.json["archived"] != true {
		t.Fatalf("merged contact is not archived: %s", r.body)
	}
	if eventCount(f, "contact.merged", x) != 1 {
		t.Fatal("contact.merged was not emitted once")
	}
	if f.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity_id = $1 AND field = 'archived'`, x) != 1 {
		t.Fatal("the contact merge has no audit row")
	}
	call = mcpCall(f.staff, "archiveMatter", map[string]any{"path": map[string]any{"matter_id": w}})
	if !strings.Contains(call.body, `"isError":true`) || !strings.Contains(call.body, "not_found") {
		t.Fatalf("archive a walled matter over MCP: %s", call.body)
	}
}

// TestReleaseRecordCustomFields is AC-002 for putTimeEntryCustomFields,
// putExpenseCustomFields and bulkCustomFieldValue: values are validated against the
// definitions, the records show them, walls and roles apply, a token needs only its items'
// scopes, one bad item writes nothing, and an Idempotency-Key replays.
func TestReleaseRecordCustomFields(t *testing.T) {
	f := newFin(t)
	open, clientID := f.matter("Estate of Fields", "", nil)
	walled, _ := f.matter("Estate of Hidden", "", nil)
	f.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{f.attyID}})
	def := func(record, key, typ string, extra map[string]any) string {
		body := map[string]any{"record_type": record, "key": key, "label": strings.ToUpper(key[:1]) + key[1:], "field_type": typ}
		for k, v := range extra {
			body[k] = v
		}
		return str(t, f.admin.must(http.StatusCreated, "POST", "/api/v1/custom-field-definitions", body).json, "id")
	}
	phase := def("time_entry", "phase", "pick_list", map[string]any{"options": []string{"A", "B"}})
	receipt := def("expense", "receipt_number", "text", nil)
	room := def("matter", "court_room", "text", nil)
	nick := def("contact", "nickname", "text", nil)
	entry := f.timeEntry(open, 3, 100_00)
	hidden := f.timeEntry(walled, 1, 100_00)
	expType := str(t, f.admin.must(http.StatusCreated, "POST", "/api/v1/expense-types", map[string]any{"name": "Filing fee"}).json, "id")
	expense := str(t, f.staff.must(http.StatusCreated, "POST", "/api/v1/expenses", map[string]any{"matter_id": open,
		"incurred_on": time.Now().UTC().Format("2006-01-02"), "description": "Filing", "amount_cents": 50_00, "expense_type_id": expType, "billable": true}).json, "id")
	value := func(r resp, id string) any {
		t.Helper()
		list, _ := r.json["custom_fields"].([]any)
		for _, v := range list {
			m := v.(map[string]any)
			if m["definition_id"] == id {
				return m["value"]
			}
		}
		return nil
	}

	// putTimeEntryCustomFields.
	put := func(c *client, path, def string, v any) resp {
		return c.do("PUT", path+"/custom-fields", map[string]any{"values": []map[string]any{{"definition_id": def, "key": "x", "value": v}}})
	}
	before := eventCount(f, "time_entry.updated", entry)
	r := put(f.staff, "/api/v1/time-entries/"+entry, phase, "A")
	if r.code != http.StatusOK || value(r, phase) != "A" || r.json["id"] != entry {
		t.Fatalf("put time entry custom fields: %d %s", r.code, r.body)
	}
	if eventCount(f, "time_entry.updated", entry) != before+1 {
		t.Fatal("time_entry.updated was not emitted for the value")
	}
	if g := f.atty.must(http.StatusOK, "GET", "/api/v1/time-entries/"+entry, nil); value(g, phase) != "A" {
		t.Fatalf("the time entry does not show its value: %s", g.body)
	}
	for _, e := range items(t, f.atty.must(http.StatusOK, "GET", "/api/v1/time-entries?matter_id="+open, nil)) {
		if e["id"] == entry && value(resp{json: e}, phase) != "A" {
			t.Fatalf("listed time entry lacks its value: %v", e)
		}
	}
	if r := put(f.staff, "/api/v1/time-entries/"+entry, phase, "C"); r.code != http.StatusBadRequest {
		t.Fatalf("a value outside the pick list: %d %s", r.code, r.body)
	}
	if r := put(f.staff, "/api/v1/time-entries/"+entry, receipt, "R-1"); r.code != http.StatusBadRequest {
		t.Fatalf("an expense field on a time entry: %d %s", r.code, r.body)
	}
	if r := put(f.staff, "/api/v1/time-entries/"+hidden, phase, "A"); r.code != http.StatusNotFound {
		t.Fatalf("behind a wall: %d %s", r.code, r.body)
	}
	if r := put(f.acct, "/api/v1/time-entries/"+entry, phase, "B"); r.code != http.StatusForbidden {
		t.Fatalf("accountant wrote a value: %d %s", r.code, r.body)
	}
	if r := f.staff.do("PUT", "/api/v1/time-entries/"+entry+"/custom-fields", map[string]any{}); r.code != http.StatusBadRequest {
		t.Fatalf("no values: %d %s", r.code, r.body)
	}

	// putExpenseCustomFields, over /mcp.
	call := mcpCall(f.staff, "putExpenseCustomFields", map[string]any{"path": map[string]any{"expense_id": expense},
		"body": map[string]any{"values": []map[string]any{{"definition_id": receipt, "key": "receipt_number", "value": "R-77"}}}})
	if strings.Contains(call.body, `"isError":true`) || !strings.Contains(call.body, "R-77") {
		t.Fatalf("putExpenseCustomFields over MCP: %s", call.body)
	}
	if eventCount(f, "expense.updated", expense) != 1 {
		t.Fatal("expense.updated was not emitted once")
	}

	// bulkCustomFieldValue: all four record types in one batch.
	item := func(typ, id, def string, v any) map[string]any {
		return map[string]any{"record_type": typ, "record_id": id, "values": []map[string]any{{"definition_id": def, "key": "k", "value": v}}}
	}
	batch := []map[string]any{item("matter", open, room, "4B"), item("contact", clientID, nick, "Patsy"),
		item("time_entry", entry, phase, "B"), item("expense", expense, receipt, "R-78")}
	ok := f.atty.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": batch}, "Idempotency-Key", "bulk-1")
	if ok.code != http.StatusCreated || num(t, ok.json, "updated") != 4 {
		t.Fatalf("bulk: %d %s", ok.code, ok.body)
	}
	if g := f.atty.must(http.StatusOK, "GET", "/api/v1/matters/"+open, nil); value(g, room) != "4B" {
		t.Fatalf("matter value: %s", g.body)
	}
	if g := f.atty.must(http.StatusOK, "GET", "/api/v1/time-entries/"+entry, nil); value(g, phase) != "B" {
		t.Fatalf("time entry value: %s", g.body)
	}
	if g := f.atty.must(http.StatusOK, "GET", "/api/v1/expenses/"+expense, nil); value(g, receipt) != "R-78" {
		t.Fatalf("expense value: %s", g.body)
	}
	updated := f.count(`SELECT COUNT(*) FROM core.events WHERE action = 'custom_field.updated'`)
	if updated < 4 {
		t.Fatalf("bulk emitted %d custom_field.updated events", updated)
	}
	if replay := f.atty.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": batch}, "Idempotency-Key", "bulk-1"); replay.code != http.StatusCreated ||
		f.count(`SELECT COUNT(*) FROM core.events WHERE action = 'custom_field.updated'`) != updated {
		t.Fatalf("replay: %d %s", replay.code, replay.body)
	}
	if r := f.atty.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": batch[:1]}, "Idempotency-Key", "bulk-1"); r.code != http.StatusConflict {
		t.Fatalf("a reused key with another batch: %d %s", r.code, r.body)
	}

	// One inaccessible record or one bad value rejects the whole batch.
	unchanged := func() {
		t.Helper()
		if g := f.atty.must(http.StatusOK, "GET", "/api/v1/matters/"+open, nil); value(g, room) != "4B" {
			t.Fatalf("a rejected batch wrote: %s", g.body)
		}
	}
	if r := f.staff.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": []map[string]any{
		item("matter", open, room, "9Z"), item("time_entry", hidden, phase, "A")}}); r.code != http.StatusNotFound {
		t.Fatalf("batch with a walled record: %d %s", r.code, r.body)
	}
	unchanged()
	if r := f.staff.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": []map[string]any{
		item("matter", open, room, "9Z"), item("time_entry", entry, phase, "Q")}}); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "batch_rejected") || !strings.Contains(r.body, "items[1]") {
		t.Fatalf("batch with a bad value: %d %s", r.code, r.body)
	}
	unchanged()
	if r := f.staff.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": []map[string]any{}}); r.code != http.StatusBadRequest {
		t.Fatalf("empty batch: %d %s", r.code, r.body)
	}
	if r := f.acct.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": batch[:1]}); r.code != http.StatusForbidden {
		t.Fatalf("accountant batch: %d %s", r.code, r.body)
	}
	unchanged()

	// A token needs the write scope of each item's record type, and only those.
	tok := f.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "matter fields", "scopes": []string{"matters:write"}})
	bot := newClient(t, f.srv.URL)
	bot.bearer = str(t, tok.json, "secret")
	if r := bot.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": []map[string]any{item("matter", open, room, "5C")}}); r.code != http.StatusCreated {
		t.Fatalf("matters:write token on a matter item: %d %s", r.code, r.body)
	}
	if r := bot.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": []map[string]any{
		item("matter", open, room, "6D"), item("time_entry", entry, phase, "A")}}); r.code != http.StatusForbidden {
		t.Fatalf("matters:write token on a time item: %d %s", r.code, r.body)
	}
	if g := f.atty.must(http.StatusOK, "GET", "/api/v1/matters/"+open, nil); value(g, room) != "5C" {
		t.Fatalf("scope-denied batch wrote: %s", g.body)
	}
	call = mcpCall(f.atty, "bulkCustomFieldValue", map[string]any{"body": map[string]any{"items": []map[string]any{item("contact", clientID, nick, "Pat")}}})
	if strings.Contains(call.body, `"isError":true`) || !strings.Contains(call.body, `"updated":1`) {
		t.Fatalf("bulkCustomFieldValue over MCP: %s", call.body)
	}
}
