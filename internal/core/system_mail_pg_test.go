package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/supervisor"
)

// TestCORE05SystemMailRouting: route through module (202), SMTP fallback on
// not_configured, refuse a second system_mail module.
func TestCORE05SystemMailRouting(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))

	var mu sync.Mutex
	var posts []map[string]any
	var keys []string
	status := http.StatusAccepted
	problemCode := ""
	x.rt.Handle("mail-router", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system-mail" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		_ = json.Unmarshal(body, &msg)
		mu.Lock()
		posts = append(posts, msg)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if r.Header.Get(modules.SignatureHeader) == "" {
			t.Error("missing X-Togavsu-Signature")
		}
		switch status {
		case http.StatusServiceUnavailable:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": problemCode, "message": "not ready"})
		default:
			w.WriteHeader(status)
		}
	}))

	pkg := x.build(func(f map[string][]byte) {
		m := string(f[modules.ManifestFile])
		m = strings.Replace(m, "id: matter-clock", "id: mail-router", 1)
		m = strings.Replace(m, "name: Matter Clock", "name: Mail Router", 1)
		m = strings.Replace(m, "  events: /events\n", "  events: /events\n  system_mail: /system-mail\n", 1)
		m = strings.Replace(m, "permissions:\n", "permissions:\n  - scope: mail:system\n    reason: Deliver system mail.\n    required: false\n", 1)
		f[modules.ManifestFile] = []byte(m)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("mail-router")
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.route_through_email_module", map[string]any{"value": true})

	status = http.StatusAccepted
	before := len(x.smtp.messages())
	x.admin.must(http.StatusAccepted, "POST", "/api/v1/mail/test", map[string]any{"to": "avery@firm.example"})
	mu.Lock()
	nPosts, nKeys := len(posts), len(keys)
	key0 := ""
	if nKeys > 0 {
		key0 = keys[0]
	}
	mu.Unlock()
	if nPosts != 1 || key0 == "" {
		t.Fatalf("module posts=%d keys=%v", nPosts, keys)
	}
	if len(x.smtp.messages()) != before {
		t.Fatal("SMTP used when module accepted")
	}

	status, problemCode = http.StatusServiceUnavailable, "not_configured"
	mu.Lock()
	posts = nil
	mu.Unlock()
	beforeBlake := len(x.smtp.to("blake@firm.example"))
	x.admin.must(http.StatusAccepted, "POST", "/api/v1/mail/test", map[string]any{"to": "blake@firm.example"})
	if len(x.smtp.to("blake@firm.example")) != beforeBlake+1 {
		t.Fatalf("SMTP fallback missing: got %d", len(x.smtp.to("blake@firm.example")))
	}

	pkg2 := x.build(func(f map[string][]byte) {
		m := string(f[modules.ManifestFile])
		m = strings.Replace(m, "id: matter-clock", "id: mail-router-2", 1)
		m = strings.Replace(m, "name: Matter Clock", "name: Mail Router Two", 1)
		m = strings.Replace(m, "  events: /events\n", "  events: /events\n  system_mail: /system-mail\n", 1)
		m = strings.Replace(m, "permissions:\n", "permissions:\n  - scope: mail:system\n    reason: Deliver system mail.\n    required: false\n", 1)
		f[modules.ManifestFile] = []byte(m)
	})
	up := x.admin.upload("/api/v1/modules", nil, pkg2)
	if up.code != http.StatusConflict || !strings.Contains(up.body, "system_mail") {
		t.Fatalf("second system_mail: %d %s", up.code, up.body)
	}
}

// TestCORE05SystemMailTimeoutSameKey: a module timeout is retryable with the same
// Idempotency-Key and does not fall back to SMTP (CORE-05).
func TestCORE05SystemMailTimeoutSameKey(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))

	var mu sync.Mutex
	var keys []string
	var hits int
	x.rt.Handle("mail-router", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system-mail" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		hits++
		n := hits
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if n == 1 {
			// Stall longer than the system mailer client timeout.
			time.Sleep(50 * time.Millisecond)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	pkg := x.build(func(f map[string][]byte) {
		m := string(f[modules.ManifestFile])
		m = strings.Replace(m, "id: matter-clock", "id: mail-router", 1)
		m = strings.Replace(m, "name: Matter Clock", "name: Mail Router", 1)
		m = strings.Replace(m, "  events: /events\n", "  events: /events\n  system_mail: /system-mail\n", 1)
		m = strings.Replace(m, "permissions:\n", "permissions:\n  - scope: mail:system\n    reason: Deliver system mail.\n    required: false\n", 1)
		f[modules.ManifestFile] = []byte(m)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("mail-router")
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.route_through_email_module", map[string]any{"value": true})

	// Bound the client tightly so the first POST times out inside this test.
	x.app.sysMail.HTTP = &http.Client{Timeout: 20 * time.Millisecond}

	before := len(x.smtp.messages())
	id := "018f1a00-7b3c-7d4e-8a11-mailtimeout001"
	err := x.app.sysMail.deliver(context.Background(), systemMail{
		ID: id, Kind: "system", To: []string{"timeout@firm.example"}, Subject: "Retry me", Text: "body",
	})
	if err == nil {
		t.Fatal("expected timeout error on first attempt")
	}
	if len(x.smtp.messages()) != before {
		t.Fatal("timeout fell back to SMTP")
	}

	// Retry with the same message id; module accepts.
	if err := x.app.sysMail.deliver(context.Background(), systemMail{
		ID: id, Kind: "system", To: []string{"timeout@firm.example"}, Subject: "Retry me", Text: "body",
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) < 2 || keys[0] != id || keys[1] != id {
		t.Fatalf("Idempotency-Key on attempts: %v want %q twice", keys, id)
	}
	if len(x.smtp.messages()) != before {
		t.Fatal("SMTP used after successful module retry")
	}
}

// TestCORE05SupervisorOutboxDrain: DeliverMail routes supervisor mail-outbox notices
// through systemMailer (update_failure kind) so email-module routing and SMTP fallback apply.
func TestCORE05SupervisorOutboxDrain(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "mail-outbox.jsonl")
	x := newExt(t, func(c *Config) { c.SupervisorMailOutbox = outPath })
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))

	var mu sync.Mutex
	var posts []map[string]any
	x.rt.Handle("mail-router", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system-mail" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		_ = json.Unmarshal(body, &msg)
		mu.Lock()
		posts = append(posts, msg)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	pkg := x.build(func(f map[string][]byte) {
		m := string(f[modules.ManifestFile])
		m = strings.Replace(m, "id: matter-clock", "id: mail-router", 1)
		m = strings.Replace(m, "name: Matter Clock", "name: Mail Router", 1)
		m = strings.Replace(m, "  events: /events\n", "  events: /events\n  system_mail: /system-mail\n", 1)
		m = strings.Replace(m, "permissions:\n", "permissions:\n  - scope: mail:system\n    reason: Deliver system mail.\n    required: false\n", 1)
		f[modules.ManifestFile] = []byte(m)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("mail-router")
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.route_through_email_module", map[string]any{"value": true})

	box := supervisor.OpenOutbox(outPath)
	if err := box.Notify(context.Background(), "Update failed", "The signed release could not be applied."); err != nil {
		t.Fatal(err)
	}
	before := len(x.smtp.messages())
	n, err := x.app.DeliverMail(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("DeliverMail sent %d, want at least the supervisor notice", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 {
		t.Fatalf("module posts=%d want 1 (got SMTP=%d)", len(posts), len(x.smtp.messages())-before)
	}
	if posts[0]["kind"] != "update_failure" || posts[0]["subject"] != "Update failed" {
		t.Fatalf("module body: %+v", posts[0])
	}
	left, err := box.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("outbox still has %d notices", len(left))
	}
}

// TestCORE05SupervisorOutboxRetrySameKey: a 5xx from the email module puts the notice
// back and the next drain reuses the same Idempotency-Key (CORE-05).
func TestCORE05SupervisorOutboxRetrySameKey(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "mail-outbox.jsonl")
	x := newExt(t, func(c *Config) { c.SupervisorMailOutbox = outPath })
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))

	var mu sync.Mutex
	var keys []string
	var hits int
	x.rt.Handle("mail-router", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system-mail" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		hits++
		n := hits
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	pkg := x.build(func(f map[string][]byte) {
		m := string(f[modules.ManifestFile])
		m = strings.Replace(m, "id: matter-clock", "id: mail-router", 1)
		m = strings.Replace(m, "name: Matter Clock", "name: Mail Router", 1)
		m = strings.Replace(m, "  events: /events\n", "  events: /events\n  system_mail: /system-mail\n", 1)
		m = strings.Replace(m, "permissions:\n", "permissions:\n  - scope: mail:system\n    reason: Deliver system mail.\n    required: false\n", 1)
		f[modules.ManifestFile] = []byte(m)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("mail-router")
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.route_through_email_module", map[string]any{"value": true})

	box := supervisor.OpenOutbox(outPath)
	if err := box.Notify(context.Background(), "Update failed", "retry with same key"); err != nil {
		t.Fatal(err)
	}
	listed, err := box.List()
	if err != nil || len(listed) != 1 || listed[0].ID == "" {
		t.Fatalf("notice before drain: %+v %v", listed, err)
	}
	wantKey := listed[0].ID

	if _, err := x.app.DeliverMail(context.Background()); err != nil {
		t.Fatal(err)
	}
	left, err := box.List()
	if err != nil || len(left) != 1 || left[0].ID != wantKey {
		t.Fatalf("after 5xx outbox=%+v err=%v want id %q", left, err, wantKey)
	}

	if _, err := x.app.DeliverMail(context.Background()); err != nil {
		t.Fatal(err)
	}
	left, err = box.List()
	if err != nil || len(left) != 0 {
		t.Fatalf("after 202 outbox=%+v err=%v", left, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 2 || keys[0] != wantKey || keys[1] != wantKey {
		t.Fatalf("Idempotency-Key on attempts: %v want %q twice", keys, wantKey)
	}
}

// TestCORE05SystemMailNeedsPermission: install refuses container.system_mail without mail:system.
func TestCORE05SystemMailNeedsPermission(t *testing.T) {
	raw := samplePackageForMail(t, false)
	_, _, refs := modules.CheckAll(raw)
	if len(refs) == 0 || refs[0].Path != "container.system_mail" {
		t.Fatalf("expected system_mail permission refusal: %v", refs)
	}
}

func samplePackageForMail(t *testing.T, withScope bool) []byte {
	t.Helper()
	// Reuse modules_test sample via CheckAll on a minimal archive built like refusal_test.
	files := map[string][]byte{}
	for _, name := range []string{modules.ManifestFile, "migrations/0001_matter_targets.sql"} {
		b, err := readMatterClock(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = b
	}
	m := string(files[modules.ManifestFile])
	m = strings.Replace(m, "  events: /events\n", "  events: /events\n  system_mail: /system-mail\n", 1)
	if withScope {
		m = strings.Replace(m, "permissions:\n", "permissions:\n  - scope: mail:system\n    reason: Deliver system mail.\n", 1)
	}
	files[modules.ManifestFile] = []byte(m)
	raw, err := modules.Archive(files)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func readMatterClock(name string) ([]byte, error) {
	return modulesReadFile("../../examples/matter-clock/package/" + name)
}

// modulesReadFile is os.ReadFile; named to keep the test file's imports clear.
func modulesReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
