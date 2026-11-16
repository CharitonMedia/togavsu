package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestCORE25FeeAgreementSignatureCallback: a module token that moves an agreement to
// sent is recorded as signature_module_id; that module's service call may move sent to
// signed with signed_on and signed_file_id, and markFileStatus may set that file
// executed. Other fields, transitions, modules and files are refused (CORE-25).
func TestCORE25FeeAgreementSignatureCallback(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("  - scope: events:read\n"),
			[]byte("  - scope: matters:write\n    reason: Send and sign fee agreements.\n  - scope: files:write\n    reason: Mark the signed copy executed.\n  - scope: events:read\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	other := moduleBearer(t, x.docs.fin, "other-mod", "matters:write", "files:write")

	matter, _ := x.matter("Estate of Signature", "sign@client.example", nil)
	created := x.staff.must(http.StatusCreated, "POST", "/api/v1/fee-agreements", map[string]any{
		"matter_id": matter, "fee_type": "hourly", "services_description": "Probate",
	})
	id := str(t, created.json, "id")
	if created.json["signature_module_id"] != nil && created.json["signature_module_id"] != "" {
		t.Fatalf("draft should have no signature module: %s", created.body)
	}

	// Staff send without a module token leaves signature_module_id empty.
	staffSent := x.staff.must(http.StatusOK, "PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "sent"})
	if staffSent.json["signature_module_id"] != nil && staffSent.json["signature_module_id"] != "" {
		t.Fatalf("staff send set signature_module_id: %s", staffSent.body)
	}
	x.staff.must(http.StatusOK, "PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "void", "void_reason": "resend via module"})

	created = x.staff.must(http.StatusCreated, "POST", "/api/v1/fee-agreements", map[string]any{
		"matter_id": matter, "fee_type": "hourly", "services_description": "Probate again",
	})
	id = str(t, created.json, "id")

	ct := x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{})
	tok := str(t, ct.json, "token")
	sent := bot.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "sent"}, modules.ContextHeader, tok)
	if sent.code != http.StatusOK || sent.json["status"] != "sent" || sent.json["signature_module_id"] != "matter-clock" {
		t.Fatalf("module send: %d %s", sent.code, sent.body)
	}

	signedPDF := x.upload(x.staff, matter, "signed.pdf", "application/pdf", []byte("%PDF-1.4 signed"), nil)
	fileID := str(t, signedPDF, "id")
	otherFile := str(t, x.upload(x.staff, matter, "other.pdf", "application/pdf", []byte("%PDF-1.4 other"), nil), "id")

	// Wrong module cannot sign.
	if r := other.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{
		"status": "signed", "signed_on": "2026-11-16", "signed_file_id": fileID,
	}); r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("other module sign: %d %v", r.code, r.json)
	}

	// Service call cannot void, change fee_type, or skip signed_file_id.
	if r := bot.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{"status": "void", "void_reason": "no"}); r.code != http.StatusBadRequest {
		t.Fatalf("service void: %d %s", r.code, r.body)
	}
	if r := bot.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{
		"status": "signed", "signed_on": "2026-11-16", "signed_file_id": fileID, "fee_type": "flat",
	}); r.code != http.StatusBadRequest || !strings.Contains(r.body, "status, signed_on and signed_file_id") {
		t.Fatalf("service extra field: %d %s", r.code, r.body)
	}
	if r := bot.do("PATCH", "/api/v1/fee-agreements/"+id, map[string]any{
		"status": "signed", "signed_on": "2026-11-16",
	}); r.code != http.StatusBadRequest || !strings.Contains(r.body, "signed_file_id") {
		t.Fatalf("service without file: %d %s", r.code, r.body)
	}

	signed := bot.must(http.StatusOK, "PATCH", "/api/v1/fee-agreements/"+id, map[string]any{
		"status": "signed", "signed_on": "2026-11-16", "signed_file_id": fileID,
	})
	if signed.json["status"] != "signed" || signed.json["signed_on"] != "2026-11-16" ||
		signed.json["signed_file_id"] != fileID || signed.json["signature_module_id"] != "matter-clock" {
		t.Fatalf("signed: %s", signed.body)
	}
	var source string
	if err := x.app.Pool.QueryRowContext(ctx,
		`SELECT source FROM core.audit_log WHERE entity = 'fee_agreement' AND entity_id = $1 ORDER BY created_at DESC LIMIT 1`,
		id).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "service" {
		t.Fatalf("audit source %q, want service", source)
	}

	// markFileStatus: only executed on that signed file.
	if r := bot.do("POST", "/api/v1/files/"+otherFile+"/status", map[string]any{"status": "executed"}); r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("mark other file: %d %v", r.code, r.json)
	}
	if r := bot.do("POST", "/api/v1/files/"+fileID+"/status", map[string]any{"status": "final"}); r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("mark non-executed: %d %v", r.code, r.json)
	}
	if r := other.do("POST", "/api/v1/files/"+fileID+"/status", map[string]any{"status": "executed"}); r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("other module mark: %d %v", r.code, r.json)
	}
	marked := bot.must(http.StatusOK, "POST", "/api/v1/files/"+fileID+"/status", map[string]any{"status": "executed"})
	if marked.json["status"] != "executed" {
		t.Fatalf("marked: %s", marked.body)
	}

	// Staff may still mark status without the signature path.
	x.staff.must(http.StatusOK, "POST", "/api/v1/files/"+otherFile+"/status", map[string]any{"status": "final"})
}
