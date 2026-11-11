package core

import (
	"net/http"
	"strings"
	"testing"
)

// TestContractRoutesFailClosed walks every operation in api/openapi/v1/mcp-tools.json on a
// ready install. A mounted operation that needs a role or scope refuses an anonymous call
// with 401; an operation this core does not mount answers 404 or 405, never 200; and
// tools/list offers exactly the mounted operations.
func TestContractRoutesFailClosed(t *testing.T) {
	app, srv := start(t, testDSN(t))
	admin := firstRun(t, srv.URL)
	anon := newClient(t, srv.URL)
	mounted := 0
	for _, tl := range app.tools {
		if tl.Path == "/mcp" {
			continue
		}
		path := tl.Path
		for _, s := range tl.segs {
			if strings.HasPrefix(s, "{") {
				path = strings.ReplaceAll(path, s, "01a1c38b-0000-7000-8000-000000000000")
			}
		}
		r := anon.do(tl.Method, path, nil)
		public := len(tl.Scopes) == 0 && len(tl.Roles) == 0
		if !app.mounted(tl) {
			if r.code != http.StatusNotFound && r.code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s is not mounted but answered %d", tl.Method, tl.Path, r.code)
			}
			continue
		}
		mounted++
		if !public && r.code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d to an anonymous caller", tl.Method, tl.Path, r.code)
		}
	}
	list := admin.must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	tools, _ := list.json["result"].(map[string]any)["tools"].([]any)
	if len(tools) != mounted {
		t.Fatalf("tools/list has %d tools, %d operations are mounted", len(tools), mounted)
	}
	t.Logf("%d of %d contract operations are mounted on /api/v1 and offered on /mcp", mounted, len(app.tools))
	// Phase 1 and 2 operations that must be served; later waves add the rest.
	for _, want := range []string{
		"GET /api/v1/setup", "POST /api/v1/setup", "POST /api/v1/auth/login", "POST /api/v1/auth/totp",
		"POST /api/v1/auth/totp/confirm", "POST /api/v1/auth/logout", "GET /api/v1/me",
		"GET /api/v1/settings", "PATCH /api/v1/settings/{setting_key}", "POST /api/v1/settings/{setting_key}/reset",
		"GET /api/v1/audit-log", "POST /api/v1/api-tokens", "GET /api/v1/contacts", "POST /api/v1/contacts",
		"GET /api/v1/matters", "POST /api/v1/matters", "GET /api/v1/matters/{matter_id}", "PATCH /api/v1/matters/{matter_id}",
		"GET /api/v1/matters/{matter_id}/history", "POST /api/v1/matters/{matter_id}/close", "POST /api/v1/matters/{matter_id}/reopen",
		"PUT /api/v1/matters/{matter_id}/wall", "GET /api/v1/notes", "POST /api/v1/notes", "GET /api/v1/search",
		"POST /api/v1/conflict-records", "POST /api/v1/matters/{matter_id}/conflict-checks", "GET /api/v1/widgets/my-open-matters",
		"POST /api/v1/custom-field-definitions", "PUT /api/v1/matters/{matter_id}/custom-fields",
		// Phase 3: billing and trust.
		"GET /api/v1/time-entries", "POST /api/v1/time-entries", "POST /api/v1/invoice-drafts", "POST /api/v1/invoices/{invoice_id}/approve",
		"POST /api/v1/invoices/{invoice_id}/send", "POST /api/v1/invoices/{invoice_id}/void", "POST /api/v1/invoices/{invoice_id}/apply-trust",
		"POST /api/v1/payments", "GET /api/v1/billing/worklist", "POST /api/v1/billing/flat-fees/{matter_id}/apply", "GET /api/v1/reports/ar-aging",
		"GET /api/v1/trust-accounts", "POST /api/v1/trust-ledger", "POST /api/v1/trust-ledger:bulk", "POST /api/v1/trust-ledger/{trust_entry_id}/reversals",
		"GET /api/v1/trust/balances", "GET /api/v1/trust-flags", "POST /api/v1/trust-reconciliations", "GET /api/v1/reports/trust-balances",
		"GET /api/v1/notifications", "POST /api/v1/notifications:read-all",
	} {
		method, path, _ := strings.Cut(want, " ")
		found := false
		for _, tl := range app.tools {
			if tl.Method == method && tl.Path == path {
				found = app.mounted(tl)
			}
		}
		if !found {
			t.Errorf("%s is not mounted", want)
		}
	}
}
