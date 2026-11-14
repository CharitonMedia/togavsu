package core

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestDocumentsDocTenderTokensAndMCP checks the Doc Tender routes the contract adds
// (getDocTenderStatus, reprocessDocTenderFile, getDocTenderModelChoices) and the
// ai.model_prices setting on the running core: a bearer token reaches each route with
// its scope and is refused without it, /mcp offers and runs each one, and a file behind
// a matter wall is not_found over the token and /mcp alike, with nothing of it in the
// answer. A price set in ai.model_prices is the cost the next model call logs.
func TestDocumentsDocTenderTokensAndMCP(t *testing.T) {
	prov := NewProvider()
	defer prov.Close()
	d := newDocs(t, func(c *Config) { c.ProviderURLs = map[string]string{"openai": prov.URL()} })
	d.useProvider()
	open, _ := d.matter("Example Dissolution", "", map[string]any{"case_number": "23AR-CV00825"})
	walled, _ := d.matter("Sealed Adoption", "", nil)
	d.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{d.adminID}})
	openFile := str(t, d.upload(d.atty, open, "motion.txt", "text/plain", []byte(motionText), nil), "id")
	sealedFile := str(t, d.upload(d.admin, walled, "sealed-WALLTEXT.txt", "text/plain", []byte(strings.Replace(motionText, "MOTION", "SEALED WALLTEXT", 1)), nil), "id")
	// The core's own worker may hold a job's lease; wait for the open file to publish and
	// the walled file's first run to settle (it ends in exception: no matter matches its
	// text), so no model call from either first run lands after ai.model_prices is set
	// below and is priced, which would fail the 0-cent check.
	published := func(c *client, file string) func() bool {
		return func() bool { d.drain(); return d.dtStatus(c, file)["status"] == "published" }
	}
	waitFor(t, 2*time.Minute, "the open file's processing", published(d.atty, openFile))
	waitFor(t, 2*time.Minute, "the walled file's processing", func() bool {
		d.drain()
		s := d.dtStatus(d.admin, sealedFile)["status"]
		return s == "published" || s == "exception"
	})

	// The setting is registered, checked on write, and in the AI tab's catalog.
	for _, bad := range []any{"not json", map[string]any{"m": map[string]any{"input": -1, "output": 0}}, map[string]any{"m": map[string]any{"input": 1}}, []any{1}} {
		if r := d.admin.do("PATCH", "/api/v1/settings/ai.model_prices", map[string]any{"value": bad}); r.code != http.StatusBadRequest && r.code != http.StatusUnprocessableEntity {
			t.Fatalf("ai.model_prices accepted %v: %d %s", bad, r.code, r.body)
		}
	}
	if r := d.atty.do("PATCH", "/api/v1/settings/ai.model_prices", map[string]any{"value": map[string]any{}}); r.code != http.StatusForbidden {
		t.Fatalf("an attorney set ai.model_prices: %d %s", r.code, r.body)
	}
	// 100 cents per token in, 0 out: every logged call from here costs its prompt tokens.
	d.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/ai.model_prices", map[string]any{"value": map[string]any{
		"gpt-6.1-sol": map[string]any{"input": 1e8, "output": 0}}})

	tok := func(owner string, scopes ...string) *client {
		return d.token(map[string]any{"name": "dt " + owner + " " + strings.Join(scopes, ","), "owner_user_id": owner, "scopes": scopes})
	}
	staffRead := tok(d.staffID, "doc_tender:read")
	staffFiles := tok(d.staffID, "files:read")
	attyWrite := tok(d.attyID, "doc_tender:write", "doc_tender:read")
	attyRead := tok(d.attyID, "doc_tender:read")
	adminRead := tok(d.adminID, "doc_tender:read")
	leaks := func(body string) bool { return strings.Contains(body, "WALLTEXT") || strings.Contains(body, "Sealed") }

	// --- getDocTenderStatus ---
	if r := staffRead.must(http.StatusOK, "GET", "/api/v1/files/"+openFile+"/doc-tender-status", nil); r.json["status"] != "published" {
		t.Fatalf("staff token status: %s", r.body)
	}
	if r := staffFiles.do("GET", "/api/v1/files/"+openFile+"/doc-tender-status", nil); r.code != http.StatusForbidden {
		t.Fatalf("a token without doc_tender:read read the status: %d %s", r.code, r.body)
	}
	if r := staffRead.do("GET", "/api/v1/files/"+sealedFile+"/doc-tender-status", nil); r.code != http.StatusNotFound || leaks(r.body) {
		t.Fatalf("staff token status of the walled file: %d %s", r.code, r.body)
	}
	if r := attyRead.do("GET", "/api/v1/files/"+sealedFile+"/doc-tender-status", nil); r.code != http.StatusNotFound || leaks(r.body) {
		t.Fatalf("attorney token status of the walled file: %d %s", r.code, r.body)
	}
	adminRead.must(http.StatusOK, "GET", "/api/v1/files/"+sealedFile+"/doc-tender-status", nil)
	status := toolName(t, "GET", "/api/v1/files/{file_id}/doc-tender-status")
	if call := mcpCall(d.staff, status, map[string]any{"path": map[string]any{"file_id": openFile}}); strings.Contains(call.body, `"isError":true`) || !strings.Contains(call.body, "published") {
		t.Fatalf("MCP status as staff: %s", call.body)
	}
	if call := mcpCall(d.staff, status, map[string]any{"path": map[string]any{"file_id": sealedFile}}); !strings.Contains(call.body, `"isError":true`) || leaks(call.body) {
		t.Fatalf("MCP status of the walled file as staff: %s", call.body)
	}
	if call := mcpCall(staffRead, status, map[string]any{"path": map[string]any{"file_id": sealedFile}}); !strings.Contains(call.body, `"isError":true`) || leaks(call.body) {
		t.Fatalf("MCP status of the walled file by token: %s", call.body)
	}

	// --- reprocessDocTenderFile ---
	events := func() int {
		return d.count(`SELECT COUNT(*) FROM events WHERE action = 'doc_tender.retry_scheduled'`)
	}
	before := events()
	if r := attyRead.do("POST", "/api/v1/files/"+openFile+"/doc-tender-reprocess", map[string]any{}); r.code != http.StatusForbidden {
		t.Fatalf("a token without doc_tender:write reprocessed: %d %s", r.code, r.body)
	}
	// Staff do not reprocess (admins and attorneys do): no staff token carries the scope,
	// and a staff session is refused.
	if r := d.admin.do("POST", "/api/v1/api-tokens", map[string]any{"name": "staff writer", "owner_user_id": d.staffID, "scopes": []string{"doc_tender:write"}}); r.code != http.StatusForbidden {
		t.Fatalf("a staff token with doc_tender:write was issued: %d %s", r.code, r.body)
	}
	if r := d.staff.do("POST", "/api/v1/files/"+openFile+"/doc-tender-reprocess", map[string]any{}); r.code != http.StatusForbidden {
		t.Fatalf("staff reprocessed: %d %s", r.code, r.body)
	}
	if r := attyWrite.do("POST", "/api/v1/files/"+sealedFile+"/doc-tender-reprocess", map[string]any{}); r.code != http.StatusNotFound || leaks(r.body) {
		t.Fatalf("attorney token reprocess of the walled file: %d %s", r.code, r.body)
	}
	reprocess := toolName(t, "POST", "/api/v1/files/{file_id}/doc-tender-reprocess")
	if call := mcpCall(d.atty, reprocess, map[string]any{"path": map[string]any{"file_id": sealedFile}}); !strings.Contains(call.body, `"isError":true`) || leaks(call.body) {
		t.Fatalf("MCP reprocess of the walled file: %s", call.body)
	}
	if n := events(); n != before {
		t.Fatalf("a refused reprocess scheduled a job: %d events, want %d", n, before)
	}
	// Before the price was set every call cost 0 cents.
	if n := d.count(`SELECT COUNT(*) FROM ai_call_log WHERE cost_cents <> 0`); n != 0 {
		t.Fatalf("%d calls were priced before ai.model_prices was set", n)
	}
	r := attyWrite.must(http.StatusAccepted, "POST", "/api/v1/files/"+openFile+"/doc-tender-reprocess", map[string]any{})
	if (r.json["status"] != "queued" && r.json["status"] != "running") || r.json["file_version_id"] == nil {
		t.Fatalf("reprocess by token: %s", r.body)
	}
	if n := events(); n != before+1 {
		t.Fatalf("reprocess emitted %d doc_tender.retry_scheduled events, want 1", n-before)
	}
	waitFor(t, 2*time.Minute, "the reprocessed run", published(d.atty, openFile))
	if call := mcpCall(d.atty, reprocess, map[string]any{"path": map[string]any{"file_id": openFile}}); strings.Contains(call.body, `"isError":true`) || !strings.Contains(call.body, "file_version_id") {
		t.Fatalf("MCP reprocess: %s", call.body)
	}
	if n := events(); n != before+2 {
		t.Fatalf("two reprocess requests emitted %d doc_tender.retry_scheduled events", n-before)
	}
	waitFor(t, 2*time.Minute, "the run reprocessed over /mcp", published(d.atty, openFile))
	// The reprocessed run's calls are priced from ai.model_prices.
	if n := d.count(`SELECT COUNT(*) FROM ai_call_log WHERE model = 'gpt-6.1-sol' AND cost_cents > 0 AND cost_cents = prompt_tokens * 100`); n == 0 {
		t.Fatal("no gpt-6.1-sol call after the price was set logged its cost")
	}
	if n := d.count(`SELECT COUNT(*) FROM ai_call_log WHERE model <> 'gpt-6.1-sol' AND cost_cents <> 0`); n != 0 {
		t.Fatalf("%d calls on unpriced models cost more than 0 cents", n)
	}

	// --- getDocTenderModelChoices ---
	choices := adminRead.must(http.StatusOK, "GET", "/api/v1/doc-tender/model-choices", nil)
	for _, task := range []string{"labeling", "categorizing"} {
		list, _ := choices.json[task].([]any)
		if len(list) == 0 {
			t.Fatalf("model choices by token: %s", choices.body)
		}
		for _, c := range list {
			if strings.HasPrefix(c.(string), "local") {
				t.Fatalf("model choices offer %s for %s", c, task)
			}
		}
	}
	if r := attyRead.do("GET", "/api/v1/doc-tender/model-choices", nil); r.code != http.StatusForbidden {
		t.Fatalf("an attorney token read the model choices: %d %s", r.code, r.body)
	}
	if r := tok(d.adminID, "files:read").do("GET", "/api/v1/doc-tender/model-choices", nil); r.code != http.StatusForbidden {
		t.Fatalf("an admin token without doc_tender:read read the model choices: %d %s", r.code, r.body)
	}
	choicesTool := toolName(t, "GET", "/api/v1/doc-tender/model-choices")
	if call := mcpCall(d.admin, choicesTool, map[string]any{}); strings.Contains(call.body, `"isError":true`) || !strings.Contains(call.body, "categorizing") {
		t.Fatalf("MCP model choices as admin: %s", call.body)
	}
	if call := mcpCall(d.staff, choicesTool, map[string]any{}); !strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("MCP model choices as staff: %s", call.body)
	}

	// /mcp offers all three.
	list := d.admin.must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}).body
	for _, name := range []string{status, reprocess, choicesTool} {
		if !strings.Contains(list, `"`+name+`"`) {
			t.Fatalf("/mcp tools/list does not offer %s", name)
		}
	}
}
