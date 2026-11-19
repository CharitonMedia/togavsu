package coretest_test

import (
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

// MCP carries operation failures in a successful JSON-RPC response. Assert the
// envelope and structured result so an unknown tool cannot satisfy a refusal case.
func toolResult2d(t *testing.T, client *coretest.Client, name string, args map[string]any, wantError bool) map[string]any {
	t.Helper()
	r := client.Must(http.StatusOK, "POST", "/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	result, ok := r.JSON["result"].(map[string]any)
	if !ok || r.JSON["error"] != nil || result["isError"] != wantError {
		t.Fatalf("%s MCP envelope: %s", name, r.Body)
	}
	value, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("%s missing structuredContent: %s", name, r.Body)
	}
	return value
}

func TestCoreUpdate2DOwnSettingsRESTAndMCPScopes(t *testing.T) {
	for _, transport := range []string{"REST", "MCP"} {
		t.Run(transport, func(t *testing.T) {
			c := coretest.New(t)
			const id, other = "settings-two-d", "other-settings-two-d"
			for _, module := range []string{id, other} {
				c.Handle(module, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
				install2d(t, c, module, []string{"module_settings:write"}, "")
			}
			bot := c.ModuleClient(id)
			viewer := c.ContextFor(coretest.Admin, id, "")
			put := func(client *coretest.Client, module string, values map[string]string, status int) map[string]any {
				t.Helper()
				if transport == "MCP" {
					return toolResult2d(t, client, "putOwnModuleSettings", map[string]any{
						"path": map[string]string{"module_id": module}, "body": values, "context_token": viewer,
					}, status >= http.StatusBadRequest)
				}
				r := client.Do("PUT", "/api/v1/modules/"+module+"/own-settings", values, modulekit.ContextHeader, viewer)
				if r.Code != status {
					t.Fatalf("own settings: got %d want %d: %s", r.Code, status, r.Body)
				}
				return r.JSON
			}
			// Both the installation and viewer permit setup; this bearer lacks only
			// the write scope. Approved installation scopes must not widen it.
			token := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{
				"name": "narrow settings bearer", "module_id": id, "scopes": []string{"modules:read"},
				"approved_scopes": []string{"modules:read", "module_settings:write"},
			})
			narrow := c.NewClient()
			narrow.Bearer = str(t, token.JSON, "secret")
			denied := put(narrow, id, map[string]string{"increment_minutes": "9"}, http.StatusForbidden)
			if denied["code"] != "scope_denied" || !strings.Contains(str(t, denied, "message"), "module_settings:write") {
				t.Fatalf("missing write scope: %v", denied)
			}
			if n := c.Count(`SELECT COUNT(*) FROM core.module_settings WHERE module_id=$1`, id); n != 0 {
				t.Fatalf("scope-denied write stored %d settings", n)
			}

			put(bot, id, map[string]string{"increment_minutes": "12"}, http.StatusOK)
			if n := c.Count(`SELECT COUNT(*) FROM core.module_settings WHERE module_id=$1 AND key='increment_minutes' AND value='12'`, id); n != 1 {
				t.Fatal("successful setup did not persist its value")
			}
			cross := put(bot, other, map[string]string{"increment_minutes": "15"}, http.StatusForbidden)
			if cross["code"] != "forbidden" || !strings.Contains(str(t, cross, "message"), "only own module settings") {
				t.Fatalf("cross-module write: %v", cross)
			}
			if n := c.Count(`SELECT COUNT(*) FROM core.module_settings WHERE module_id=$1`, other); n != 0 {
				t.Fatal("cross-module write changed the other module")
			}
			invalid := put(bot, id, map[string]string{"increment_minutes": "18", "undeclared_key": "synthetic"}, http.StatusBadRequest)
			if invalid["code"] != "validation" || invalid["field"] != "undeclared_key" {
				t.Fatalf("undeclared key: %v", invalid)
			}
			if n := c.Count(`SELECT COUNT(*) FROM core.module_settings WHERE module_id=$1 AND key='increment_minutes' AND value='12'`, id); n != 1 {
				t.Fatal("invalid setup changed the existing value")
			}
			if n := c.Count(`SELECT COUNT(*) FROM core.module_settings WHERE module_id=$1 AND key='undeclared_key'`, id); n != 0 {
				t.Fatal("invalid setup stored an undeclared key")
			}
		})
	}
}

func TestCoreUpdate2DContactPhoneItemsRESTAndMCPScopes(t *testing.T) {
	for _, transport := range []string{"REST", "MCP"} {
		t.Run(transport, func(t *testing.T) {
			c := coretest.New(t)
			const id = "phone-items-two-d"
			c.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			var files map[string][]byte
			install2d(t, c, id, []string{"contacts:read"}, "contact_phone_items:\n  - id: call\n    title: Call\n    path: /call\n", func(f map[string][]byte) {
				f[modulekit.ManifestFile] = []byte(strings.Replace(string(f[modulekit.ManifestFile]), "  - scope: contacts:read\n", "  - scope: contacts:read\n    required: false\n", 1))
				files = f
			})
			phone := func(contact string) string {
				t.Helper()
				return str(t, c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{
					"contact_id": contact, "label": "work", "e164": "+15555550123", "effective_from": "2026-01-01",
				}).JSON, "id")
			}
			openPhone := phone(c.ClientContact)
			walled, contact := c.Matter("Synthetic phone wall", "", nil)
			wallPhone := phone(contact)
			c.Wall(walled, c.Attorney)
			list := func(client *coretest.Client, phoneID string, status int) map[string]any {
				t.Helper()
				if transport == "MCP" {
					return toolResult2d(t, client, "listContactPhoneItems", map[string]any{"query": map[string]string{"phone_id": phoneID}}, status >= http.StatusBadRequest)
				}
				return client.Must(status, "GET", "/api/v1/modules/contact-phone-items?phone_id="+phoneID, nil).JSON
			}
			denied := list(c.AgentToken(coretest.Staff, "matters:read"), openPhone, http.StatusForbidden)
			if denied["code"] != "scope_denied" || !strings.Contains(str(t, denied, "message"), "contacts:read") {
				t.Fatalf("missing contacts scope: %v", denied)
			}
			reader := c.AgentToken(coretest.Staff, "contacts:read")
			assertItems := func(value map[string]any, want int) {
				t.Helper()
				rows, ok := value["data"].([]any)
				if !ok || len(rows) != want {
					t.Fatalf("want %d phone items: %v", want, value)
				}
				if want == 1 {
					item, ok := rows[0].(map[string]any)
					if !ok || item["module_id"] != id || item["id"] != "call" || item["path"] != "/modules/"+id+"/contact-phone-items/call?phone_id="+openPhone {
						t.Fatalf("wrong phone item: %v", rows[0])
					}
				}
			}
			assertItems(list(reader, openPhone, http.StatusOK), 1)
			if hidden := list(reader, wallPhone, http.StatusNotFound); hidden["code"] != "not_found" {
				t.Fatalf("walled contact phone: %v", hidden)
			}
			// Prove the phone exists and the refusal applies the caller's wall.
			inside := list(c.AgentToken(coretest.Attorney, "contacts:read"), wallPhone, http.StatusOK)
			if rows, ok := inside["data"].([]any); !ok || len(rows) != 1 {
				t.Fatalf("inside-wall phone action missing: %v", inside)
			}
			// A new optional permission opens the update approval flow. Decline
			// contacts:read there while keeping the module and its item enabled.
			manifest := strings.Replace(string(files[modulekit.ManifestFile]), `version: "1.0.0"`, `version: "1.0.1"`, 1)
			manifest = strings.Replace(manifest, "permissions:\n", "permissions:\n  - scope: notes:read\n    reason: Synthetic update permission.\n    required: false\n", 1)
			files[modulekit.ManifestFile] = []byte(manifest)
			pub, priv, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatal(err)
			}
			c.TrustKey("phone update key", pub)
			signed, err := modulekit.Build(priv, files)
			if err != nil {
				t.Fatal(err)
			}
			if update := c.TryUpdate(id, signed); update.Code != http.StatusAccepted || update.JSON["outcome"] != "awaiting_approval" {
				t.Fatalf("update approval: %d %s", update.Code, update.Body)
			}
			updated := c.Admin.Must(http.StatusOK, "POST", "/api/v1/modules/"+id+"/permissions", map[string]any{
				"decisions": []map[string]string{{"scope": "notes:read", "decision": "decline"}, {"scope": "contacts:read", "decision": "decline"}},
			})
			if updated.JSON["enabled"] != true || c.Last(id).Version != "1.0.1" {
				t.Fatalf("module did not stay enabled after scope withdrawal: %s", updated.Body)
			}
			assertItems(list(reader, openPhone, http.StatusOK), 0)
		})
	}
}
