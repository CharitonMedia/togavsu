package core

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

func patchManifest(raw []byte, id, name, extraPerms, tail string) []byte {
	m := string(raw)
	m = strings.Replace(m, "id: matter-clock", "id: "+id, 1)
	m = strings.Replace(m, "name: Matter Clock", "name: "+name, 1)
	if extraPerms != "" {
		m = strings.Replace(m, "permissions:\n", "permissions:\n"+extraPerms, 1)
	}
	if tail != "" {
		if !strings.HasSuffix(m, "\n") {
			m += "\n"
		}
		m += tail
	}
	return []byte(m)
}

// TestCORE31ContractCall covers CU2-03 and CU2-01 for CORE-31.
func TestCORE31ContractCall(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	const contract = "phone.provider.v1"

	var gotBody []byte
	var gotSig, gotConsumer string
	x.rt.Serve("phone-provider", func(m testkit.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotSig = r.Header.Get(modules.SignatureHeader)
			gotConsumer = r.Header.Get("X-Togavsu-Contract-Consumer")
			gotBody, _ = io.ReadAll(r.Body)
			if err := modules.VerifyWebhook(m.WebhookSecret, gotSig, gotBody, time.Now().UTC(), 0); err != nil {
				t.Errorf("provider verify: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Provider-Host", r.Host) // must not leak to consumer
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
	})

	prov := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = patchManifest(f[modules.ManifestFile], "phone-provider", "Phone Provider",
			"  - scope: contracts:provide\n    reason: Provide contract.\n",
			"provides:\n  - contract: "+contract+"\n    operations:\n      - name: echo\n        path: /contracts/echo\n")
	})
	cons := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = patchManifest(f[modules.ManifestFile], "phone-consumer", "Phone Consumer",
			"  - scope: contracts:call\n    reason: Call contract.\n",
			"consumes:\n  - contract: "+contract+"\n")
	})
	for _, pkg := range [][]byte{prov, cons} {
		if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
			t.Fatalf("install: %s", problem)
		}
	}
	x.approveOnTab("phone-provider")
	x.approveOnTab("phone-consumer")

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("phone-consumer").Secrets["togavsu_api_token"]

	if r := bot.do("POST", "/api/v1/modules/contracts/"+contract+"/echo", map[string]any{"n": 1}); r.code != http.StatusConflict || r.json["code"] != "contract_unlinked" {
		t.Fatalf("unlinked call: %d %s", r.code, r.body)
	}

	noscope := newClient(t, x.srv.URL)
	noscope.bearer = x.rt.Last("phone-provider").Secrets["togavsu_api_token"]
	if r := noscope.do("POST", "/api/v1/modules/contracts/"+contract+"/echo", map[string]any{}); r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("provider calling without contracts:call: %d %s", r.code, r.body)
	}

	link := x.admin.must(http.StatusCreated, "POST", "/api/v1/modules/contract-links", map[string]any{
		"contract": contract, "consumer_id": "phone-consumer", "provider_id": "phone-provider",
	})
	linkID := str(t, link.json, "id")

	r := bot.must(http.StatusOK, "POST", "/api/v1/modules/contracts/"+contract+"/echo", map[string]any{"n": 7})
	if !strings.Contains(r.body, `"ok":true`) || gotConsumer != "phone-consumer" || len(gotBody) == 0 {
		t.Fatalf("linked call: body=%s consumer=%s got=%s", r.body, gotConsumer, gotBody)
	}
	if strings.Contains(r.body, "X-Provider-Host") || strings.Contains(strings.ToLower(r.body), "127.0.0.1") {
		t.Fatalf("consumer learned provider address: %s", r.body)
	}

	provTok := newClient(t, x.srv.URL)
	provTok.bearer = x.rt.Last("phone-provider").Secrets["togavsu_api_token"]
	if r := provTok.do("POST", "/api/v1/modules/contracts/"+contract+"/events", map[string]any{
		"action": "ring", "payload": map[string]any{"from": "test"},
	}); r.code != http.StatusNoContent && r.code != http.StatusOK {
		t.Fatalf("push contract event: %d %s", r.code, r.body)
	}
	if r := provTok.do("POST", "/api/v1/modules/contracts/"+contract+"/events", map[string]any{
		"action": "matter.created", "payload": map[string]any{"id": "x"},
	}); r.code < 400 || !strings.Contains(r.body, "scoped under") {
		t.Fatalf("forged core event action: %d %s", r.code, r.body)
	}

	auditCall := x.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=module_contract_call&limit=50", nil)
	if !strings.Contains(auditCall.body, contract) || !strings.Contains(auditCall.body, "echo") || !strings.Contains(auditCall.body, "phone-consumer") {
		t.Fatalf("missing contract call audit: %s", auditCall.body)
	}
	auditEvent := x.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=module_contract_event&limit=50", nil)
	if !strings.Contains(auditEvent.body, contract+".ring") && !strings.Contains(auditEvent.body, "ring") {
		t.Fatalf("missing contract event audit: %s", auditEvent.body)
	}
	if strings.Contains(strings.ToLower(auditCall.body), "127.0.0.1") || strings.Contains(strings.ToLower(auditEvent.body), "http://") {
		t.Fatalf("audit leaked address")
	}

	x.admin.must(http.StatusNoContent, "DELETE", "/api/v1/modules/contract-links/"+linkID, nil)
	if r := bot.do("POST", "/api/v1/modules/contracts/"+contract+"/echo", map[string]any{"n": 2}); r.code != http.StatusConflict || r.json["code"] != "contract_unlinked" {
		t.Fatalf("after unlink: %d %s", r.code, r.body)
	}
}

// TestCORE31DisableKeepsLinks: disabling a module must not wipe admin-approved contract links.
func TestCORE31DisableKeepsLinks(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	const contract = "phone.provider.v1"
	x.rt.Serve("phone-provider", func(m testkit.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
	})
	prov := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = patchManifest(f[modules.ManifestFile], "phone-provider", "Phone Provider",
			"  - scope: contracts:provide\n    reason: Provide contract.\n",
			"provides:\n  - contract: "+contract+"\n    operations:\n      - name: echo\n        path: /contracts/echo\n")
	})
	cons := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = patchManifest(f[modules.ManifestFile], "phone-consumer", "Phone Consumer",
			"  - scope: contracts:call\n    reason: Call contract.\n",
			"consumes:\n  - contract: "+contract+"\n")
	})
	for _, pkg := range [][]byte{prov, cons} {
		if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
			t.Fatalf("install: %s", problem)
		}
	}
	x.approveOnTab("phone-provider")
	x.approveOnTab("phone-consumer")
	x.admin.must(http.StatusCreated, "POST", "/api/v1/modules/contract-links", map[string]any{
		"contract": contract, "consumer_id": "phone-consumer", "provider_id": "phone-provider",
	})
	if _, problem := x.tabForm(x.admin, "disable", url.Values{"module_id": {"phone-provider"}}, nil); problem != "" {
		t.Fatalf("disable: %s", problem)
	}
	links := x.admin.must(http.StatusOK, "GET", "/api/v1/modules/contract-links", nil)
	if !strings.Contains(links.body, "phone-provider") || !strings.Contains(links.body, "phone-consumer") {
		t.Fatalf("links lost on disable: %s", links.body)
	}
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("phone-consumer").Secrets["togavsu_api_token"]
	if r := bot.do("POST", "/api/v1/modules/contracts/"+contract+"/echo", map[string]any{}); r.json["code"] != "contract_unlinked" {
		t.Fatalf("disabled provider should refuse call: %d %s", r.code, r.body)
	}
}

// TestCORE32PublicPages covers CU2-04 and CSRF POST success/failure.
func TestCORE32PublicPages(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))

	var lastCSRF, lastStep string
	var lastMethod string
	x.rt.Serve("intake-mod", func(m testkit.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/intake" && !strings.HasPrefix(r.URL.Path, "/intake/") {
				http.NotFound(w, r)
				return
			}
			lastMethod = r.Method
			lastStep = r.Header.Get(modules.PublicStepHeader)
			lastCSRF = r.Header.Get(modules.PublicCSRFPassHeader)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte("<html><body>posted ok</body></html>"))
				return
			}
			_, _ = w.Write([]byte("<html><body>public intake step=" + lastStep + " csrf=" + lastCSRF + "</body></html>"))
		})
	})

	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = patchManifest(f[modules.ManifestFile], "intake-mod", "Intake",
			"  - scope: public_pages:serve\n    reason: Public pages.\n  - scope: contacts:write\n    reason: Intake.\n",
			"public_pages:\n  - path: /intake\npublic_operations:\n  - createContact\n")
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	// Install/approval view must name public pages and operations (CORE-32).
	tab := x.admin.do("GET", "/admin?tab=modules", nil)
	if tab.code != http.StatusOK || !strings.Contains(tab.body, "/intake") || !strings.Contains(tab.body, "createContact") || !strings.Contains(tab.body, "id=\"public-intake-mod\"") {
		snip := tab.body
		if len(snip) > 800 {
			snip = snip[:800]
		}
		t.Fatalf("admin install view missing public pages/ops: %d %s", tab.code, snip)
	}
	x.approveOnTab("intake-mod")

	anon := newClient(t, x.srv.URL)
	r := anon.do("GET", "/p/intake-mod/intake", nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, "public intake") {
		t.Fatalf("public page: %d %s", r.code, r.body)
	}
	if lastCSRF == "" || lastStep == "" {
		t.Fatalf("module did not receive CSRF pass-through or step token: csrf=%q step=%q", lastCSRF, lastStep)
	}
	u, _ := url.Parse(x.srv.URL + "/p/intake-mod/intake")
	var csrfCookie string
	for _, c := range anon.http.Jar.Cookies(u) {
		if c.Name == modules.PublicCSRFCookie {
			csrfCookie = c.Value
		}
	}
	if csrfCookie == "" || csrfCookie != lastCSRF {
		t.Fatalf("csrf cookie=%q pass=%q", csrfCookie, lastCSRF)
	}
	if r := anon.do("GET", "/p/intake-mod/secret", nil); r.code != http.StatusNotFound {
		t.Fatalf("undeclared public path: %d %s", r.code, r.body)
	}

	// POST without CSRF must fail.
	if r := anon.do("POST", "/p/intake-mod/intake", url.Values{"name": {"Avery"}}); r.code != http.StatusForbidden || !strings.Contains(r.body, "csrf") {
		t.Fatalf("POST without csrf: %d %s", r.code, r.body)
	}
	// POST with form csrf (buffered after body read) must reach the module.
	postOK := anon.do("POST", "/p/intake-mod/intake", url.Values{"csrf": {csrfCookie}, "name": {"Avery"}})
	if postOK.code != http.StatusOK || !strings.Contains(postOK.body, "posted ok") || lastMethod != http.MethodPost {
		t.Fatalf("POST with csrf: %d %s method=%s", postOK.code, postOK.body, lastMethod)
	}
	// Wrong csrf fails.
	if r := anon.do("POST", "/p/intake-mod/intake", url.Values{"csrf": {"not-the-token"}, "name": {"x"}}); r.code != http.StatusForbidden {
		t.Fatalf("POST bad csrf: %d %s", r.code, r.body)
	}

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("intake-mod").Secrets["togavsu_api_token"]
	secret := x.rt.Last("intake-mod").Secrets["togavsu_webhook_secret"]
	step, err := modules.IssueStepToken([]byte(secret), "intake-mod", 0, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if r := bot.do("GET", "/api/v1/matters", nil, modules.PublicStepHeader, step); r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("listMatter outside public_operations: %d %s", r.code, r.body)
	}
	// createContact is listed; may succeed or fail validation, but not scope_denied for the op itself.
	if r := bot.do("POST", "/api/v1/contacts", map[string]any{
		"display_name": "Public Person", "kind": "person",
	}, modules.PublicStepHeader, step); r.json["code"] == "scope_denied" && strings.Contains(r.body, "public_operations") {
		t.Fatalf("createContact in public_operations denied: %s", r.body)
	}

	// Fresh IP key: rate counters are per IP+module in-process; reset by using a distinct
	// Forwarded-For so earlier GETs in this test do not burn the budget.
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.public_page_rate_per_minute", map[string]any{"value": 1})
	anon2 := newClient(t, x.srv.URL)
	if r := anon2.do("GET", "/p/intake-mod/intake", nil, "X-Forwarded-For", "203.0.113.50"); r.code != http.StatusOK {
		t.Fatalf("first after rate set: %d %s", r.code, r.body)
	}
	if r := anon2.do("GET", "/p/intake-mod/intake", nil, "X-Forwarded-For", "203.0.113.50"); r.code != http.StatusTooManyRequests {
		t.Fatalf("rate limited want 429 got %d %s", r.code, r.body)
	}
}

// TestCORE34StaffBanner covers banner listing and shell slot (CU2-01).
func TestCORE34StaffBanner(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))

	x.rt.Serve("banner-mod", func(m testkit.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("banner"))
		})
	})
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = patchManifest(f[modules.ManifestFile], "banner-mod", "Banner",
			"  - scope: banner:show\n    reason: Staff banner.\n",
			"staff_banner:\n  path: /banner\n  height: 40\n")
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("banner-mod")

	list := x.admin.must(http.StatusOK, "GET", "/api/v1/modules/banners", nil)
	raw, _ := json.Marshal(list.json["data"])
	if !bytes.Contains(raw, []byte("banner-mod")) {
		t.Fatalf("banners: %s", list.body)
	}
	x.admin.must(http.StatusNoContent, "PUT", "/api/v1/modules/banner-order", map[string]any{
		"order": []string{"banner-mod"},
	})

	tab := x.admin.do("GET", "/admin?tab=modules", nil)
	if tab.code != http.StatusOK || !strings.Contains(tab.body, `id="banner-order"`) || !strings.Contains(tab.body, "banner-mod") {
		t.Fatalf("modules tab missing banner-order form: %d", tab.code)
	}
	if _, problem := x.tabForm(x.admin, "banner-order", url.Values{"order": {"banner-mod"}}, nil); problem != "" {
		t.Fatalf("banner-order form: %s", problem)
	}

	home := x.admin.do("GET", "/", nil)
	if home.code != http.StatusOK || !strings.Contains(home.body, "staff-banner") || !strings.Contains(home.body, "banner-mod") {
		snip := home.body
		if len(snip) > 500 {
			snip = snip[:500]
		}
		t.Fatalf("shell missing banner: %d %s", home.code, snip)
	}
	bridge := x.admin.do("GET", "/ui/static/banner-bridge.js", nil)
	if bridge.code != http.StatusOK || !strings.Contains(bridge.body, "ev.origin") || !strings.Contains(bridge.body, "fromBanner") {
		t.Fatalf("banner-bridge missing origin/source checks: %d", bridge.code)
	}
}

// TestCORE2BOpenAPIAndMCP: new operations appear in openapi and on /mcp (CU2-01).
func TestCORE2BOpenAPIAndMCP(t *testing.T) {
	x := newExt(t)
	spec := x.admin.must(http.StatusOK, "GET", "/api/v1/openapi.json", nil)
	body := spec.body
	for _, id := range []string{"callModuleContract", "pushModuleContractEvent", "createModuleContractLink", "deleteModuleContractLink", "listModuleContractLinks", "listModuleBanners", "putModuleBannerOrder"} {
		if !strings.Contains(body, `"operationId": "`+id+`"`) && !strings.Contains(body, `"operationId":"`+id+`"`) {
			t.Fatalf("openapi missing %s", id)
		}
	}
	for _, scope := range []string{"contracts:call", "contracts:provide", "public_pages:serve", "banner:show"} {
		if !strings.Contains(body, scope) {
			t.Fatalf("openapi missing scope %s", scope)
		}
	}
	for _, key := range []string{"modules.public_page_rate_per_minute", "modules.public_page_max_bytes", "modules.contract_call_max_bytes", "modules.staff_banner_order"} {
		if !strings.Contains(body, key) {
			t.Fatalf("openapi missing setting %s", key)
		}
	}
	list := x.admin.must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	toolsBody := list.body
	for _, name := range []string{"callModuleContract", "pushModuleContractEvent", "createModuleContractLink", "deleteModuleContractLink", "listModuleBanners", "putModuleBannerOrder"} {
		if !strings.Contains(toolsBody, `"`+name+`"`) {
			t.Fatalf("/mcp tools/list missing %s", name)
		}
	}
	noscope := newClient(t, x.srv.URL)
	tok := x.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{
		"name": "core2b-noscope", "scopes": []string{"matters:read"},
	})
	noscope.bearer = str(t, tok.json, "secret")
	if r := noscope.do("POST", "/api/v1/modules/contracts/phone.provider.v1/echo", map[string]any{}); r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("call without contracts:call: %d %s", r.code, r.body)
	}
	if r := mcpCall(noscope, "callModuleContract", map[string]any{
		"path": map[string]any{"contract": "phone.provider.v1", "operation": "echo"},
		"body": map[string]any{},
	}); !strings.Contains(r.body, "scope_denied") {
		t.Fatalf("mcp call without scope: %d %s", r.code, r.body)
	}
}
