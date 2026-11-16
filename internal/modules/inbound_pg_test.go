package modules_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

func TestPGInboundWebhook(t *testing.T) {
	e := newEnv(t)
	var seen methodCapture
	e.rt.Handle("matter-clock", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		if r.URL.Path == "/hook" {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, "ok-from-module")
			return
		}
		http.NotFound(w, r)
	}))
	r := e.do("POST", "/api/v1/modules", e.build(func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "tabs:\n", "inbound_webhooks:\n  - id: pay\n    path: /hook\ntabs:\n")
	}), e.as("admin")...)
	if r.code != http.StatusAccepted {
		t.Fatalf("install: %d %s", r.code, r.body)
	}
	e.approveAll("matter-clock")

	res := e.postRaw("/hooks/matter-clock/pay?token=abc", `{"event":"paid"}`,
		"Content-Type", "application/json",
		"Cookie", "togavsu_session=should-not-forward",
		"Authorization", "Bearer should-not-forward",
		modules.ContextHeader, "should-not-forward",
		"X-CSRF-Token", "should-not-forward",
		"X-Provider-Sig", "keep-me",
	)
	if res.code != http.StatusAccepted || res.body != "ok-from-module" {
		t.Fatalf("forward: %d %q", res.code, res.body)
	}
	if seen.n != 1 || seen.path != "/hook" || seen.query != "token=abc" || seen.body != `{"event":"paid"}` {
		t.Fatalf("module saw %+v", seen)
	}
	if seen.cookie != "" || seen.auth != "" || seen.ctx != "" || seen.csrf != "" {
		t.Fatalf("credentials leaked: cookie=%q auth=%q ctx=%q csrf=%q", seen.cookie, seen.auth, seen.ctx, seen.csrf)
	}
	if seen.module != "matter-clock" || seen.hook != "pay" || seen.provider != "keep-me" {
		t.Fatalf("headers: module=%q hook=%q provider=%q", seen.module, seen.hook, seen.provider)
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity LIKE 'module:%' AND field = 'inbound_webhook' AND new_value = 'pay 202'`) != 1 {
		t.Fatal("expected one delivery audit row with status, no body")
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE new_value LIKE '%event%paid%' OR old_value LIKE '%event%paid%'`) != 0 {
		t.Fatal("audit leaked a webhook body")
	}

	if r := e.postRaw("/hooks/matter-clock/missing", `{}`); r.code != http.StatusNotFound {
		t.Fatalf("undeclared hook: %d %s", r.code, r.body)
	}
	if r := e.postRaw("/hooks/no-such-module/pay", `{}`); r.code != http.StatusNotFound {
		t.Fatalf("missing module: %d %s", r.code, r.body)
	}
	if r := e.do("GET", "/hooks/matter-clock/pay", nil); r.code != http.StatusNotFound && r.code != http.StatusMethodNotAllowed {
		t.Fatalf("GET must not forward: %d %s", r.code, r.body)
	}
	before := seen.n

	if r := e.do("POST", "/api/v1/modules/matter-clock/disable", map[string]any{}, e.as("admin")...); r.code != http.StatusOK {
		t.Fatalf("disable: %d %s", r.code, r.body)
	}
	if r := e.postRaw("/hooks/matter-clock/pay", `{}`); r.code != http.StatusNotFound {
		t.Fatalf("disabled: %d %s", r.code, r.body)
	}
	if seen.n != before {
		t.Fatal("disabled module still received a forward")
	}
}

func TestPGInboundWebhookSizeAndRate(t *testing.T) {
	e := newEnv(t)
	e.rt.Handle("matter-clock", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	if r := e.do("POST", "/api/v1/modules", e.build(func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "tabs:\n", "inbound_webhooks:\n  - id: pay\n    path: /hook\ntabs:\n")
	}), e.as("admin")...); r.code != http.StatusAccepted {
		t.Fatalf("install: %d %s", r.code, r.body)
	}
	e.approveAll("matter-clock")

	if _, err := e.app.Settings.Set(e.ctx, "modules.inbound_webhook_max_bytes", "16", e.admin); err != nil {
		t.Fatal(err)
	}
	big := e.postRaw("/hooks/matter-clock/pay", strings.Repeat("x", 17))
	if big.code != http.StatusRequestEntityTooLarge || !strings.Contains(big.body, "payload_too_large") {
		t.Fatalf("oversize: %d %s", big.code, big.body)
	}

	if _, err := e.app.Settings.Set(e.ctx, "modules.inbound_webhook_rate_per_minute", "2", e.admin); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if r := e.postRaw("/hooks/matter-clock/pay", "ok"); r.code != http.StatusOK {
			t.Fatalf("under limit %d: %d %s", i, r.code, r.body)
		}
	}
	limited := e.postRaw("/hooks/matter-clock/pay", "ok")
	if limited.code != http.StatusTooManyRequests || limited.retryAfter == "" || !strings.Contains(limited.body, "rate_limited") {
		t.Fatalf("rate: %d retry=%q body=%s", limited.code, limited.retryAfter, limited.body)
	}
}

func TestInboundWebhookManifestValidation(t *testing.T) {
	raw := samplePackage(t, func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "tabs:\n", "inbound_webhooks:\n  - id: pay\n    path: /hook\n  - id: pay\n    path: /other\ntabs:\n")
	})
	if _, _, err := modules.CheckPackage(raw); err == nil || !strings.Contains(err.Error(), "inbound_webhooks") {
		t.Fatalf("duplicate id: %v", err)
	}
	raw = samplePackage(t, func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "tabs:\n", "inbound_webhooks:\n  - id: Bad\n    path: /hook\ntabs:\n")
	})
	if _, _, err := modules.CheckPackage(raw); err == nil {
		t.Fatal("bad id accepted")
	}
	raw = samplePackage(t, func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "tabs:\n", "inbound_webhooks:\n  - id: pay\n    path: https://evil.example/hook\ntabs:\n")
	})
	if _, _, err := modules.CheckPackage(raw); err == nil {
		t.Fatal("absolute path accepted")
	}
	ok := samplePackage(t, func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "tabs:\n", "inbound_webhooks:\n  - id: lawpay\n    path: /hooks/lawpay\n  - id: docuseal\n    path: /hooks/docuseal\ntabs:\n")
	})
	if _, _, err := modules.CheckPackage(ok); err != nil {
		t.Fatal(err)
	}
}

type methodCapture struct {
	n        int
	path     string
	query    string
	body     string
	cookie   string
	auth     string
	ctx      string
	csrf     string
	module   string
	hook     string
	provider string
}

func (c *methodCapture) record(r *http.Request) {
	c.n++
	c.path = r.URL.Path
	c.query = r.URL.RawQuery
	raw, _ := io.ReadAll(r.Body)
	c.body = string(raw)
	c.cookie = r.Header.Get("Cookie")
	c.auth = r.Header.Get("Authorization")
	c.ctx = r.Header.Get(modules.ContextHeader)
	c.csrf = r.Header.Get("X-Csrf-Token")
	if c.csrf == "" {
		c.csrf = r.Header.Get("X-CSRF-Token")
	}
	c.module = r.Header.Get("X-Togavsu-Module")
	c.hook = r.Header.Get("X-Togavsu-Inbound-Hook")
	c.provider = r.Header.Get("X-Provider-Sig")
}

type rawResp struct {
	code       int
	body       string
	retryAfter string
}

func (e *env) postRaw(path, body string, hdr ...string) rawResp {
	e.t.Helper()
	req, err := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return rawResp{code: res.StatusCode, body: string(raw), retryAfter: res.Header.Get("Retry-After")}
}
