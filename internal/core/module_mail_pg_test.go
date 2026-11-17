package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestPGSendModuleMail covers CORE-04: module mail to users and contacts, SMTP success
// and failure, skipped recipients, mail.sent timeline, and no addresses in the response.
func TestPGSendModuleMail(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile],
			[]byte("permissions:\n"),
			[]byte("permissions:\n  - scope: mail:send\n    reason: Send firm mail.\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")

	matter, clientID := x.matter("Estate of Module Mail", "party@client.example", nil)
	_, inactiveID := enrollUser(t, x.app, x.srv.URL, x.admin, "Idle", "idle@firm.example", domain.RoleStaff)
	x.admin.must(http.StatusOK, "POST", "/api/v1/users/"+string(inactiveID)+"/deactivate", nil)

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]

	body := map[string]any{
		"subject": "Portal update", "text": "You have a new message.",
		"link_path": "/portal/matter-clock/hours", "matter_id": matter,
		"to_user_ids": []string{x.staffID, string(inactiveID)},
	}
	beforeSMTP := len(x.smtp.to("sam@firm.example"))
	sent := bot.do("POST", "/api/v1/mail/messages", body, "Idempotency-Key", "mail-1")
	if sent.code != http.StatusOK {
		t.Fatalf("send: %d %s", sent.code, sent.body)
	}
	results, _ := sent.json["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results: %s", sent.body)
	}
	for _, needle := range []string{"sam@", "idle@", "party@"} {
		if strings.Contains(sent.body, needle) {
			t.Fatalf("response leaked an address (%s): %s", needle, sent.body)
		}
	}
	byUser := map[string]string{}
	for _, row := range results {
		m := row.(map[string]any)
		byUser[str(t, m, "user_id")] = str(t, m, "status")
	}
	if byUser[x.staffID] != "sent" || byUser[string(inactiveID)] != "skipped" {
		t.Fatalf("user statuses: %s", sent.body)
	}
	msgs := x.smtp.to("sam@firm.example")
	if len(msgs) != beforeSMTP+1 || !strings.Contains(msgs[len(msgs)-1].Data, "https://firm.example/portal/matter-clock/hours") {
		t.Fatalf("smtp body: %+v", msgs)
	}
	if x.count(`SELECT COUNT(*) FROM core.events WHERE action = 'mail.sent' AND matter_id = $1 AND summary = $2 AND client_visible = 0`,
		matter, "Email sent: Portal update") != 1 {
		t.Fatal("expected one mail.sent timeline row")
	}
	if x.count(`SELECT COUNT(*) FROM core.events WHERE action = 'mail.sent' AND summary LIKE '%You have%'`) != 0 {
		t.Fatal("timeline must not carry the body text")
	}

	again := bot.do("POST", "/api/v1/mail/messages", body, "Idempotency-Key", "mail-1")
	if again.code != http.StatusOK {
		t.Fatalf("replay: %d %s", again.code, again.body)
	}
	if again.body != sent.body {
		t.Fatalf("replay body changed: %s vs %s", again.body, sent.body)
	}
	if len(x.smtp.to("sam@firm.example")) != beforeSMTP+1 {
		t.Fatal("replay re-sent mail")
	}

	ct := x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{"matter_id": matter})
	contactBody := map[string]any{
		"subject": "Signing invite", "text": "Please sign.",
		"matter_id": matter, "to_contact_ids": []string{clientID},
		"link_path": "/portal/matter-clock/hours?signing=1",
	}
	contact := bot.do("POST", "/api/v1/mail/messages", contactBody,
		"Idempotency-Key", "mail-2", modules.ContextHeader, str(t, ct.json, "token"))
	if contact.code != http.StatusOK {
		t.Fatalf("contact mail: %d %s", contact.code, contact.body)
	}
	crow := contact.json["results"].([]any)[0].(map[string]any)
	if crow["status"] != "sent" || crow["contact_id"] != clientID {
		t.Fatalf("contact result: %s", contact.body)
	}
	if len(x.smtp.to("party@client.example")) != 1 {
		t.Fatal("contact mail not delivered")
	}

	if r := bot.do("POST", "/api/v1/mail/messages", contactBody, "Idempotency-Key", "mail-3"); r.code != http.StatusForbidden {
		t.Fatalf("service to_contact_ids: %d %s", r.code, r.body)
	}

	x.smtp.setReject(true)
	failBody := map[string]any{
		"subject": "Will fail", "text": "bounce", "to_user_ids": []string{x.staffID},
	}
	failed := bot.do("POST", "/api/v1/mail/messages", failBody, "Idempotency-Key", "mail-4")
	if failed.code != http.StatusOK {
		t.Fatalf("failed send: %d %s", failed.code, failed.body)
	}
	frow := failed.json["results"].([]any)[0].(map[string]any)
	if frow["status"] != "failed" || str(t, frow, "error") == "" {
		t.Fatalf("failed result: %s", failed.body)
	}
	if x.count(`SELECT COUNT(*) FROM core.mail_outbox WHERE subject = 'Will fail' AND sent_at = 0 AND last_error <> ''`) != 1 {
		t.Fatal("failed row should stay queued with last_error")
	}

	if r := x.admin.do("POST", "/api/v1/api-tokens", map[string]any{
		"name": "human-mail", "user_id": x.adminID, "scopes": []string{"mail:send"},
	}); r.code == http.StatusCreated || (!strings.Contains(r.body, "scope_denied") && r.json["code"] != "scope_denied") {
		t.Fatalf("human token mail:send: %d %s", r.code, r.body)
	}
}

// TestPGSendModuleMailOutboxIdempotencyKey: sendModuleMail posts to the email module with
// Idempotency-Key equal to the mail_outbox row id (CORE-05 same-key on every attempt).
func TestPGSendModuleMailOutboxIdempotencyKey(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))

	var mu sync.Mutex
	var keys []string
	x.rt.Handle("mail-router", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system-mail" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))

	pkg := x.build(func(f map[string][]byte) {
		m := string(f[modules.ManifestFile])
		m = strings.Replace(m, "id: matter-clock", "id: mail-router", 1)
		m = strings.Replace(m, "name: Matter Clock", "name: Mail Router", 1)
		m = strings.Replace(m, "  events: /events\n", "  events: /events\n  system_mail: /system-mail\n", 1)
		m = strings.Replace(m, "permissions:\n",
			"permissions:\n  - scope: mail:system\n    reason: Deliver system mail.\n    required: false\n"+
				"  - scope: mail:send\n    reason: Send firm mail.\n", 1)
		f[modules.ManifestFile] = []byte(m)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("mail-router")
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.route_through_email_module", map[string]any{"value": true})

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("mail-router").Secrets["togavsu_api_token"]
	sent := bot.do("POST", "/api/v1/mail/messages", map[string]any{
		"subject": "Via module", "text": "hello", "to_user_ids": []string{x.staffID},
	}, "Idempotency-Key", "mod-mail-key")
	if sent.code != http.StatusOK || sent.json["results"].([]any)[0].(map[string]any)["status"] != "sent" {
		t.Fatalf("send: %d %s", sent.code, sent.body)
	}

	var outboxID string
	if err := x.app.Pool.QueryRowContext(context.Background(),
		`SELECT id FROM core.mail_outbox WHERE subject = 'Via module' ORDER BY created_at DESC LIMIT 1`).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 1 || keys[0] != outboxID {
		t.Fatalf("Idempotency-Key=%v want outbox id %q", keys, outboxID)
	}
}
