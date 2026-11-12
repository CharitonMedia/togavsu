package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

const extAIManifest = `id: summarizer
name: Summarizer
version: "1.0.0"
author: Synthetic author
container:
  image: ghcr.io/example/summarizer
  digest: sha256:3333333333333333333333333333333333333333333333333333333333333333
  port: 8080
permissions:
  - scope: ai:use
  - scope: matters:read
ai_tasks:
  - id: summaries
    description: Summarize a matter
`

// TestExtensionsAIMediation is the ai-mediation phase case (AC-028's module AI half,
// AC-031): a module calls POST /api/v1/ai/calls on the running core with its own token
// and the viewer's context token; the core runs it on the key in AI settings, which the
// module never sees; with the primary provider unreachable it falls back to the
// secondary, and the call log shows both, readable only by an admin.
func TestExtensionsAIMediation(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Authorization"))
		mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "Summary from the secondary provider"}}},
			"usage":   map[string]int{"prompt_tokens": 11, "completion_tokens": 7},
		})
	}))
	t.Cleanup(secondary.Close)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	x := newExt(t, func(c *Config) { c.ProviderURLs = map[string]string{"openai": secondary.URL} })
	ctx := context.Background()
	open, _ := x.matter("Estate of Harlow", "", nil)
	walled, _ := x.matter("Estate of Quist", "", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"ai.primary_provider": "compatible", "ai.compatible_base_url": deadURL, "ai.compatible_api_key": "synthetic-primary-ai-key",
		"ai.primary_strong_model": "primary-strong", "ai.primary_fast_model": "primary-fast",
		"ai.secondary_provider": "openai", "ai.openai_api_key": "synthetic-secondary-ai-key", "ai.secondary_strong_model": "gpt-test-strong",
		"ai.timeout": "5s",
	} {
		x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/"+k, map[string]any{"value": v})
	}
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	x.rt.handlers["summarizer"] = http.NotFoundHandler()
	if _, problem := x.tabForm(x.admin, "install", nil, x.build(onlyManifest(extAIManifest))); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("summarizer")
	dep := x.rt.last("summarizer")
	token := dep.Secrets["togavsu_api_token"]
	for k, v := range dep.Secrets {
		if strings.Contains(v, "-ai-key") || strings.Contains(strings.ToLower(k), "openai") {
			t.Fatalf("a provider key reached the module: %s", k)
		}
	}
	staffP := x.principal(x.staff)
	ct, err := x.app.Modules.IssueContext(ctx, staffP, "summarizer", "")
	if err != nil {
		t.Fatal(err)
	}
	module := newClient(t, x.srv.URL)
	module.bearer = token
	r := module.do("POST", "/api/v1/ai/calls", map[string]any{"task": "summaries", "input": map[string]any{"prompt": "Summarize the synthetic matter."}, "matter_id": open},
		modules.ContextHeader, ct.Token)
	if r.code != http.StatusCreated || r.json["provider"] != "openai" || r.json["fell_back"] != true {
		t.Fatalf("module AI call: %d %s", r.code, r.body)
	}
	attempts, _ := r.json["attempts"].([]any)
	if len(attempts) != 2 || attempts[0].(map[string]any)["provider"] != "compatible" || attempts[0].(map[string]any)["status"] != "failed" ||
		attempts[1].(map[string]any)["provider"] != "openai" {
		t.Fatalf("attempts: %s", r.body)
	}
	if strings.Contains(r.body, "-ai-key") || r.json["requester"] != "module:summarizer" {
		t.Fatalf("the response leaks a key or misnames the requester: %s", r.body)
	}
	mu.Lock()
	got := append([]string(nil), keys...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "Bearer synthetic-secondary-ai-key" {
		t.Fatalf("the provider got %v; want the AI tab's key", got)
	}
	// The call log, over the API and the admin's MCP, shows both providers and who asked.
	log := x.admin.must(http.StatusOK, "GET", "/api/v1/ai/calls?module_id=summarizer", nil)
	if !strings.Contains(log.body, `"provider":"compatible"`) || !strings.Contains(log.body, `"status":"failed"`) || !strings.Contains(log.body, `"provider":"openai"`) {
		t.Fatalf("call log: %s", log.body)
	}
	if x.count(`SELECT COUNT(*) FROM core.ai_call_log WHERE module_id = 'summarizer' AND fell_back = 1 AND fallback_provider = 'compatible' AND user_id = $1 AND matter_id = $2`,
		string(staffP.UserID), open) != 1 {
		t.Fatal("call log row missing the viewer or the matter")
	}
	if mcp := mcpCall(x.admin, toolName(t, "GET", "/api/v1/ai/calls"), map[string]any{"query": map[string]any{"module_id": "summarizer"}}); !strings.Contains(mcp.body, "openai") || strings.Contains(mcp.body, `"isError":true`) {
		t.Fatalf("call log over MCP: %s", mcp.body)
	}
	if r := x.staff.do("GET", "/api/v1/ai/calls", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff read the call log: %d", r.code)
	}
	// A walled matter is not found for the staff viewer before any provider is called.
	if r := module.do("POST", "/api/v1/ai/calls", map[string]any{"task": "summaries", "input": map[string]any{"prompt": "x"}, "matter_id": walled},
		modules.ContextHeader, ct.Token); r.code != http.StatusNotFound {
		t.Fatalf("walled matter: %d %s", r.code, r.body)
	}
	// A token without ai:use can't call; a user session can't use a module's token route.
	reader := newClient(t, x.srv.URL)
	reader.bearer = str(t, x.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "reader", "scopes": []string{"matters:read"}}).json, "secret")
	if r := reader.do("POST", "/api/v1/ai/calls", map[string]any{"task": "summaries", "input": map[string]any{"prompt": "x"}}); r.code != http.StatusForbidden {
		t.Fatalf("token without ai:use: %d %s", r.code, r.body)
	}
	mu.Lock()
	n := len(keys)
	mu.Unlock()
	if n != 1 {
		t.Fatal("a refused call reached a provider")
	}
}

// TestExtensionsUIModules is the module half of the ui phase case (AC-011, AC-019,
// AC-033): the sample module's widget opens the tab its manifest names, its guide opens
// from the widget's ? icon, the sidebar shows its entry only while it is installed and
// enabled, and a manifest with a widget that has no click-through target is refused.
func TestExtensionsUIModules(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	if navHas(t, x.staff, "matter-clock.hours") {
		t.Fatal("sidebar has a module entry before install")
	}
	noTarget := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = []byte(strings.Replace(string(f[modules.ManifestFile]), "    target:\n      tab: hours\n      params: [matter_id]\n", "", 1))
	})
	if _, problem := x.tabForm(x.admin, "install", nil, noTarget); problem == "" || !strings.Contains(problem, "target") {
		t.Fatalf("a widget with no click-through target was accepted: %q", problem)
	}
	x.installSample()
	ws := x.staff.must(http.StatusOK, "GET", "/api/v1/widgets", nil)
	var def map[string]any
	for _, it := range items(t, ws) {
		if it["id"] == "matter-clock.hours" {
			def = it
		}
	}
	if def == nil {
		t.Fatalf("module widget not offered: %s", ws.body)
	}
	raw, _ := json.Marshal(def)
	if !strings.Contains(string(raw), "/modules/matter-clock/tabs/hours") {
		t.Fatalf("module widget does not open its manifest tab: %s", raw)
	}
	if !strings.Contains(string(raw), "matter-clock.") {
		t.Fatalf("module widget has no guide topic for its ? icon: %s", raw)
	}
	// The target page frames the module's tab for the viewer.
	if r := x.staff.must(http.StatusOK, "GET", "/modules/matter-clock/tabs/hours", nil); !strings.Contains(r.body, "/m/matter-clock/tab?") {
		t.Fatalf("widget target page: %s", r.body)
	}
	if !navHas(t, x.staff, "matter-clock.hours") {
		t.Fatal("sidebar lacks the installed module's entry")
	}
	// The rendered shell's sidebar links the module tab.
	home := x.staff.must(http.StatusOK, "GET", "/", nil)
	if !strings.Contains(home.body, "/modules/matter-clock/tabs/hours") {
		t.Fatal("the shell sidebar does not link the module tab")
	}
}

// TestExtensionsScreens is the visual QC of the integrate-extensions screens on the
// running core: the Modules and marketplace tab (installed module, alert, trusted key,
// marketplace listing with its flag), the matter screen's module panel, the module's home
// tab in its sandboxed frame, the help center with the module guide and the admin home
// with the module-alerts widget, as the roles that use them (scripts/ci/documents-qc.mjs
// in headless Chromium). Every page must load with no console or page error or failed
// request; staff get the walled matter as a 404. It runs when TOGAVSU_QC_DIR, CHROME and
// PUPPETEER_CORE are set, and writes the screenshots and qc.json there.
func TestExtensionsScreens(t *testing.T) {
	out := os.Getenv("TOGAVSU_QC_DIR")
	if out == "" || os.Getenv("CHROME") == "" || os.Getenv("PUPPETEER_CORE") == "" {
		t.Skip("TOGAVSU_QC_DIR, CHROME and PUPPETEER_CORE are not all set")
	}
	clock := newTestClock()
	x := newExt(t, func(c *Config) { c.Now = clock.now })
	ctx := context.Background()
	addr, call, _ := publishSample(t)
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.marketplace_address", map[string]any{"value": addr})
	x.trustKey("Firm build key", x.priv.Public().(ed25519.PublicKey))
	open, _ := x.matter("Estate of Harlow", "", nil)
	walled, _ := x.matter("Sealed Guardianship", "", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID), domain.ID(x.adminID)}); err != nil {
		t.Fatal(err)
	}
	if _, problem := x.tabForm(x.admin, "install-listing", url.Values{"listing_id": {"matter-clock"}, "version": {"1.0.0"}}, nil); problem != "" {
		t.Fatalf("marketplace install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	dep := x.rt.last("matter-clock")
	mdb := x.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	if err := matterclock.SetTarget(ctx, mdb, open, 120, "partner note", time.Now()); err != nil {
		t.Fatal(err)
	}
	x.staff.must(http.StatusCreated, "POST", "/api/v1/time-entries", map[string]any{"matter_id": open, "worked_on": time.Now().UTC().Format(time.DateOnly),
		"increments": 15, "description": "Synthetic inventory", "activity_type_id": x.activity, "billable": true})
	if code, b := call("POST", "/v1/operator/listings/matter-clock/flag", extOperatorToken, map[string]any{"note": "Synthetic flag: reports hours twice."}); code != http.StatusOK {
		t.Fatalf("flag: %d %s", code, b)
	}
	clock.advance(31 * time.Second)
	x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	// The admin's home layout carries the module-alerts widget and the module's widget.
	x.admin.must(http.StatusOK, "PUT", "/api/v1/me/layout", map[string]any{"widgets": []map[string]any{
		{"widget_id": "module-alerts", "x": 0, "y": 0, "w": 6, "h": 3}, {"widget_id": "matter-clock.hours", "x": 6, "y": 0, "w": 4, "h": 2}}})

	u, _ := url.Parse(x.srv.URL)
	cookies := func(c *client) []map[string]any {
		var out []map[string]any
		for _, k := range c.http.Jar.Cookies(u) {
			out = append(out, map[string]any{"name": k.Name, "value": k.Value})
		}
		return out
	}
	type step struct {
		Name string `json:"name"`
		Role string `json:"role"`
		Path string `json:"path"`
		Wait int    `json:"wait,omitempty"`
	}
	steps := []step{
		{"01-admin-modules-tab", "admin", "/admin?tab=modules&q=matter", 0},
		{"02-admin-home-module-alerts", "admin", "/", 1500},
		{"03-matter-screen-module-panel", "staff", "/practice/matters/" + open, 0},
		{"04-module-home-tab", "staff", "/modules/matter-clock/tabs/hours", 1000},
		{"05-module-matter-tab", "attorney", "/modules/matter-clock/tabs/matter?matter_id=" + open, 1000},
		{"06-help-module-guide", "staff", "/help", 0},
		{"07-walled-matter-staff", "staff", "/practice/matters/" + walled, 0},
		{"08-modules-tab-staff", "staff", "/admin?tab=modules", 0},
	}
	plan := map[string]any{"base": x.srv.URL, "pages": steps,
		"roles": map[string]any{"staff": cookies(x.staff), "attorney": cookies(x.atty), "admin": cookies(x.admin)}}
	raw, _ := json.Marshal(plan)
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join("..", "..", "scripts", "ci", "documents-qc.mjs"), planPath, out)
	cmd.Stderr = os.Stderr
	res, err := cmd.Output()
	if err != nil {
		t.Fatalf("documents-qc.mjs: %v", err)
	}
	var got struct {
		Pages []struct {
			Name    string   `json:"name"`
			Status  int      `json:"status"`
			Landed  string   `json:"landed"`
			Title   string   `json:"title"`
			Console []string `json:"console"`
			Failed  []string `json:"failed"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(res, &got); err != nil || len(got.Pages) != len(steps) {
		t.Fatalf("QC output %s: %v", res, err)
	}
	for _, p := range got.Pages {
		want := http.StatusOK
		switch p.Name {
		case "07-walled-matter-staff", "08-modules-tab-staff":
			want = http.StatusNotFound
		}
		if p.Name == "08-modules-tab-staff" && p.Status == http.StatusForbidden {
			want = http.StatusForbidden
		}
		if p.Status != want {
			t.Errorf("%s: status %d, want %d", p.Name, p.Status, want)
		}
		console, failed := p.Console, p.Failed
		if want != http.StatusOK && len(console) == 1 && strings.Contains(console[0], "status of 40") && len(failed) <= 1 {
			console, failed = nil, nil // the browser logs the page's own 403 or 404
		}
		if len(console) > 0 || len(failed) > 0 {
			t.Errorf("%s: console %v, failed requests %v", p.Name, p.Console, p.Failed)
		}
		if strings.Contains(p.Title, "Sealed") && p.Name == "07-walled-matter-staff" {
			t.Errorf("%s shows the walled matter", p.Name)
		}
	}
	summary, _ := json.MarshalIndent(map[string]any{"pages": got.Pages}, "", " ")
	if err := os.WriteFile(filepath.Join(out, "qc.json"), summary, 0o644); err != nil {
		t.Fatal(err)
	}
}
