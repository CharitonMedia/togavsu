package modules_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

const calendarManifest = `id: calendar-plus
name: Calendar Plus
version: "1.0.0"
author: Synthetic author
container:
  image: ghcr.io/example/calendar-plus
  digest: sha256:2222222222222222222222222222222222222222222222222222222222222222
  port: 8080
permissions:
  - scope: matters:read
replaces: calendar
`

func moduleOnly(manifest string) func(map[string][]byte) {
	return func(f map[string][]byte) {
		for k := range f {
			delete(f, k)
		}
		f[modules.ManifestFile] = []byte(manifest)
	}
}

// calendarModule implements the core calendar contract's paths (its OpenAPI document
// lists them) and answers every calendar request itself.
func calendarModule(t *testing.T, complete bool) (http.Handler, *[]http.Header) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	paths := map[string]map[string]json.RawMessage{}
	for p, ops := range spec.Paths {
		if modules.DomainOf(p) == modules.DomainCalendar {
			paths[p] = ops
		}
	}
	if !complete {
		for p := range paths {
			delete(paths, p)
			break
		}
	}
	doc, _ := json.Marshal(map[string]any{"openapi": "3.1.0", "paths": paths})
	var mu sync.Mutex
	seen := &[]http.Header{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/openapi.json" {
			_, _ = w.Write(doc)
			return
		}
		mu.Lock()
		*seen = append(*seen, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "togavsu_session=stolen")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "module-event", "title": "From Calendar Plus"}}, "served_by": "calendar-plus", "path": r.URL.Path, "method": r.Method})
	}), seen
}

// AC-014: a module switched in for calendar takes over the calendar screens and API;
// switching back restores the core's.
// handlers checks GET /api/v1/domain-handlers against the contract and want (domain to
// module id, "" for the core).
func handlers(t *testing.T, e *env, want map[string]string) {
	t.Helper()
	if r := e.do("GET", "/api/v1/domain-handlers", nil, e.as("staff")...); r.code != http.StatusForbidden {
		t.Fatalf("staff listed domain handlers: %d", r.code)
	}
	r := e.do("GET", "/api/v1/domain-handlers", nil, e.as("admin")...)
	data, _ := r.json["data"].([]any)
	if r.code != http.StatusOK || len(data) != len(want) || r.json["page"] == nil {
		t.Fatalf("domain handlers: %d %s", r.code, r.body)
	}
	for _, it := range data {
		h, _ := it.(map[string]any)
		if err := shapeOf(t, "DomainHandler", h); err != nil {
			t.Fatalf("domain handler outside the contract: %v (%s)", err, r.body)
		}
		mod, ok := want[h["domain"].(string)]
		kind := "core"
		if mod != "" {
			kind = "module"
		}
		if !ok || h["module_id"] != mod || h["handler"] != kind {
			t.Fatalf("domain handler %v, want module %q", h, mod)
		}
	}
}

func TestPGCalendarReplacement(t *testing.T) {
	e := newEnv(t)
	h, seen := calendarModule(t, true)
	e.rt.Handle("calendar-plus", h)
	e.do("POST", "/api/v1/modules", e.build(moduleOnly(calendarManifest)), e.as("admin")...)
	e.approveAll("calendar-plus")

	core := e.do("GET", "/api/v1/calendar-events", nil, e.as("staff")...)
	if core.code != http.StatusOK || strings.Contains(core.body, "calendar-plus") {
		t.Fatalf("core calendar before the switch: %d %s", core.code, core.body)
	}
	// Staff can't switch a domain; an incomplete contract is refused.
	if r := e.do("PUT", "/api/v1/domain-handlers/calendar", map[string]any{"module_id": "calendar-plus"}, e.as("staff")...); r.code != http.StatusForbidden {
		t.Fatalf("staff switch: %d", r.code)
	}
	partial, _ := calendarModule(t, false)
	e.rt.Handle("calendar-plus", partial)
	e.rt.Deploy(e.ctx, e.rt.Last("calendar-plus"))
	if r := e.do("PUT", "/api/v1/domain-handlers/calendar", map[string]any{"module_id": "calendar-plus"}, e.as("admin")...); r.code != http.StatusBadRequest || !strings.Contains(r.body, "missing") {
		t.Fatalf("incomplete contract accepted: %d %s", r.code, r.body)
	}
	e.rt.Handle("calendar-plus", h)
	e.rt.Deploy(e.ctx, e.rt.Last("calendar-plus"))
	// The sample module doesn't declare replaces: calendar.
	e.installSample()
	if r := e.do("PUT", "/api/v1/domain-handlers/calendar", map[string]any{"module_id": "matter-clock"}, e.as("admin")...); r.code != http.StatusBadRequest {
		t.Fatalf("non-replacing module accepted: %d %s", r.code, r.body)
	}

	if r := e.do("PUT", "/api/v1/domain-handlers/calendar", map[string]any{"module_id": "calendar-plus"}, e.as("admin")...); r.code != http.StatusOK ||
		r.json["handler"] != "module" || r.json["module_id"] != "calendar-plus" || r.json["domain"] != "calendar" || shapeOf(t, "DomainHandler", r.json) != nil {
		t.Fatalf("switch: %d %s", r.code, r.body)
	}
	// The contract list (listDomainHandlers) reports every replaceable domain.
	handlers(t, e, map[string]string{"time": "", "trust": "", "calendar": "calendar-plus"})
	// Next request, no restart: API and screens go to the module.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/calendar-events"}, {"POST", "/api/v1/calendar-events"}, {"GET", "/api/v1/deadlines"}, {"GET", "/calendar"}, {"GET", "/calendar/deadlines"},
	} {
		var body any
		if c.method == "POST" {
			body = map[string]string{"title": "Hearing"}
		}
		r := e.do(c.method, c.path, body, append(e.as("staff"), "Cookie", "togavsu_session=secret")...)
		if r.code != http.StatusOK || r.json["served_by"] != "calendar-plus" || r.json["path"] != c.path {
			t.Fatalf("%s %s after the switch: %d %s", c.method, c.path, r.code, r.body)
		}
	}
	for _, hd := range *seen {
		if hd.Get("Cookie") != "" || hd.Get("Authorization") != "" {
			t.Fatalf("the module saw core credentials: %v", hd)
		}
		if hd.Get(modules.ContextHeader) == "" {
			t.Fatal("the module got no context token for the viewer")
		}
	}
	tok := (*seen)[0].Get(modules.ContextHeader)
	if c, err := e.svc.VerifyContext(e.ctx, tok); err != nil || c.UserID != e.staff.UserID || c.Module != "calendar-plus" {
		t.Fatalf("context token: %+v %v", c, err)
	}
	// The module can't set the core's cookies.
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/calendar-events", nil)
	req.Header.Set("X-Test-User", "staff")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.Header.Get("Set-Cookie") != "" {
		t.Fatal("a module response set a cookie on the core's domain")
	}
	// Other domains stay with the core.
	if r := e.do("GET", "/api/v1/time-entries", nil, e.as("staff")...); r.code != http.StatusOK || strings.Contains(r.body, "calendar-plus") {
		t.Fatalf("time after the calendar switch: %d %s", r.code, r.body)
	}
	// While it replaces calendar it can't be disabled or removed.
	if r := e.do("POST", "/api/v1/modules/calendar-plus/disable", nil, e.as("admin")...); r.code != http.StatusConflict {
		t.Fatalf("disable a replacing module: %d", r.code)
	}

	// Switch back: the core's calendar again, with its data untouched.
	if r := e.do("PUT", "/api/v1/domain-handlers/calendar", map[string]any{"module_id": ""}, e.as("admin")...); r.code != http.StatusOK ||
		r.json["handler"] != "core" || r.json["module_id"] != "" || shapeOf(t, "DomainHandler", r.json) != nil {
		t.Fatalf("switch back: %d %s", r.code, r.body)
	}
	handlers(t, e, map[string]string{"time": "", "trust": "", "calendar": ""})
	if e.count(`SELECT COUNT(*) FROM core.events WHERE action = 'domain.switched'`) != 2 {
		t.Fatal("the switches didn't emit the contract's domain.switched event")
	}
	back := e.do("GET", "/api/v1/calendar-events", nil, e.as("staff")...)
	if back.code != http.StatusOK || back.body != core.body {
		t.Fatalf("after switching back: %d %s (before: %s)", back.code, back.body, core.body)
	}
	// The switch went through the settings registry with its audit row.
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE field = 'modules.replace_calendar' AND old_value = '' AND new_value = 'calendar-plus'`) != 1 ||
		e.count(`SELECT COUNT(*) FROM core.audit_log WHERE field = 'modules.replace_calendar' AND old_value = 'calendar-plus' AND new_value = ''`) != 1 {
		t.Fatal("the switch has no audit row")
	}
}

// openAIStandIn answers the OpenAI chat wire format and records the key it was given.
func openAIStandIn(t *testing.T, keys *[]string) *httptest.Server {
	var mu sync.Mutex
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*keys = append(*keys, r.Header.Get("Authorization"))
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
	t.Cleanup(s.Close)
	return s
}

const aiManifest = `id: summarizer
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

// AC-028 and AC-031: a module's AI call runs on the key in AI settings, the module never
// sees the key, and with the primary provider unreachable the call falls back to the
// secondary and the call log shows both.
func TestPGModuleAICallFallsBack(t *testing.T) {
	e := newEnv(t)
	var keys []string
	secondary := openAIStandIn(t, &keys)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	e.svc.AIBaseURLs = map[string]string{"openai": secondary.URL}
	for k, v := range map[string]string{
		"ai.primary_provider": "compatible", "ai.compatible_base_url": deadURL, "ai.compatible_api_key": "synthetic-primary-ai-key",
		"ai.primary_strong_model": "primary-strong", "ai.primary_fast_model": "primary-fast",
		"ai.secondary_provider": "openai", "ai.openai_api_key": "synthetic-secondary-ai-key", "ai.secondary_strong_model": "gpt-test-strong",
		"ai.timeout": "5s",
	} {
		if _, err := e.app.Settings.Set(e.ctx, k, v, e.admin); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	e.rt.Handle("summarizer", http.NotFoundHandler())
	e.do("POST", "/api/v1/modules", e.build(moduleOnly(aiManifest)), e.as("admin")...)
	e.approveAll("summarizer")
	dep := e.rt.Last("summarizer")
	token := dep.Secrets["togavsu_api_token"]
	for _, v := range dep.Secrets {
		if strings.Contains(v, "-ai-key") {
			t.Fatal("a provider key was handed to the module")
		}
	}

	r := e.do("POST", "/api/v1/ai/calls", map[string]any{"task": "summaries", "input": map[string]any{"prompt": "Summarize the synthetic matter."}, "matter_id": string(e.open.ID)},
		"Authorization", "Bearer "+token, modules.ContextHeader, e.contextFor("staff", "summarizer", ""))
	if r.code != http.StatusCreated || r.json["provider"] != "openai" || r.json["model"] != "gpt-test-strong" || r.json["fell_back"] != true {
		t.Fatalf("module AI call: %d %s", r.code, r.body)
	}
	attempts, _ := r.json["attempts"].([]any)
	if len(attempts) != 2 || attempts[0].(map[string]any)["provider"] != "compatible" || attempts[0].(map[string]any)["status"] != "failed" ||
		attempts[1].(map[string]any)["provider"] != "openai" {
		t.Fatalf("attempts: %s", r.body)
	}
	if out, _ := r.json["output"].(map[string]any); out["text"] != "Summary from the secondary provider" {
		t.Fatalf("output: %s", r.body)
	}
	if strings.Contains(r.body, "-ai-key") || r.json["requester"] != "module:summarizer" {
		t.Fatalf("the response leaks a key or misnames the requester: %s", r.body)
	}
	if len(keys) != 1 || keys[0] != "Bearer synthetic-secondary-ai-key" {
		t.Fatalf("the provider got %v; want the AI tab's key", keys)
	}
	// The call log shows both providers and the module that asked.
	if e.count(`SELECT COUNT(*) FROM core.ai_call_log WHERE module_id = 'summarizer' AND provider = 'openai' AND fell_back = 1 AND fallback_provider = 'compatible'
AND task = 'summarizer.summaries' AND user_id = $1 AND matter_id = $2`, string(e.staff.UserID), string(e.open.ID)) != 1 {
		t.Fatal("call log row missing or incomplete")
	}
	log := e.do("GET", "/api/v1/ai/calls?module_id=summarizer", nil, e.as("admin")...)
	if log.code != http.StatusOK || !strings.Contains(log.body, `"provider":"compatible","model":"primary-strong","status":"failed"`) || !strings.Contains(log.body, `"provider":"openai"`) {
		t.Fatalf("call log API: %d %s", log.code, log.body)
	}
	if r := e.do("GET", "/api/v1/ai/calls", nil, e.as("staff")...); r.code != http.StatusForbidden {
		t.Fatalf("staff read the call log: %d", r.code)
	}
	// The call log is append-only for the role that writes it.
	for _, stmt := range []string{`UPDATE core.ai_call_log SET provider = 'x'`, `DELETE FROM core.ai_call_log`} {
		tx, err := e.app.Pool.BeginTx(e.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(e.ctx, `SET LOCAL ROLE togavsu_ai_log_writer`); err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(e.ctx, stmt)
		_ = tx.Rollback()
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("%s on the call log: %v", stmt, err)
		}
	}

	// A walled matter is not_found before any provider is called; undeclared tasks and
	// tokens without ai:use are refused.
	before := len(keys)
	if r := e.do("POST", "/api/v1/ai/calls", map[string]any{"task": "summaries", "input": map[string]any{"prompt": "x"}, "matter_id": string(e.walled.ID)},
		"Authorization", "Bearer "+token, modules.ContextHeader, e.contextFor("staff", "summarizer", "")); r.code != http.StatusNotFound {
		t.Fatalf("walled matter: %d %s", r.code, r.body)
	}
	if r := e.do("POST", "/api/v1/ai/calls", map[string]any{"task": "drafting", "input": map[string]any{"prompt": "x"}}, "Authorization", "Bearer "+token); r.code != http.StatusBadRequest {
		t.Fatalf("undeclared task: %d %s", r.code, r.body)
	}
	e.installSample()
	clock := e.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	if r := e.do("POST", "/api/v1/ai/calls", map[string]any{"task": "summaries", "input": map[string]any{"prompt": "x"}}, "Authorization", "Bearer "+clock); r.code != http.StatusForbidden {
		t.Fatalf("module without ai:use: %d %s", r.code, r.body)
	}
	if len(keys) != before {
		t.Fatal("a refused call reached a provider")
	}
	// With both providers down the module gets an error, never a key.
	e.svc.AIBaseURLs = map[string]string{"openai": deadURL}
	r = e.do("POST", "/api/v1/ai/calls", map[string]any{"task": "summaries", "input": map[string]any{"prompt": "x"}}, "Authorization", "Bearer "+token)
	if r.code != http.StatusBadGateway || strings.Contains(r.body, "-ai-key") {
		t.Fatalf("both providers down: %d %s", r.code, r.body)
	}
}

// Webhook deliveries retry on the admin's schedule and stop at the attempt limit.
func TestPGWebhookRetries(t *testing.T) {
	e := newEnv(t)
	if _, err := e.app.Settings.Set(e.ctx, "api.webhook_attempts", "2", e.admin); err != nil {
		t.Fatal(err)
	}
	e.installSample()
	var fails int
	e.rt.Handle("matter-clock", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fails++
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	e.rt.Deploy(e.ctx, e.rt.Last("matter-clock"))
	id, _ := domain.NewID()
	if n, err := e.svc.Publish(e.ctx, domain.Event{Meta: domain.Meta{ID: id}, Action: "matter.created", RecordType: "matter", RecordID: e.open.ID, MatterID: e.open.ID}); err != nil || n != 1 {
		t.Fatalf("publish: %d %v", n, err)
	}
	if _, err := e.svc.Publish(e.ctx, domain.Event{Meta: domain.Meta{ID: id + "x"}, Action: "matter.updated", RecordType: "matter", RecordID: e.open.ID}); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.Deliver(e.ctx); n != 0 || fails != 1 {
		t.Fatalf("first attempt: sent %d, module hit %d", n, fails)
	}
	ds, _ := e.svc.Deliveries(e.ctx, e.admin, "matter-clock")
	if len(ds) != 1 || ds[0].Status != "pending" || ds[0].Attempts != 1 || !strings.Contains(ds[0].LastError, "503") {
		t.Fatalf("after one failure (unsubscribed matter.updated must not be queued): %+v", ds)
	}
	// Not due yet: the retry waits on the schedule.
	if _, _ = e.svc.Deliver(e.ctx); fails != 1 {
		t.Fatal("retried before its scheduled time")
	}
	later, err := modules.New(modules.Deps{DB: e.app.Pool, Key: testKey, Audit: e.app.Audit, Settings: e.app.Settings, Walls: e.app.Practice.Policy.Walls, Contacts: e.app.Practice,
		Users: e.app.Users, Tokens: modules.IdentityTokens{Svc: e.app.Identity}, Runtime: e.rt, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := later.Deliver(e.ctx); err != nil || fails != 2 {
		t.Fatalf("retry: %v, module hit %d", err, fails)
	}
	ds, _ = e.svc.Deliveries(e.ctx, e.admin, "matter-clock")
	if ds[0].Status != "failed" || ds[0].Attempts != 2 {
		t.Fatalf("after the last attempt: %+v", ds[0])
	}
}
