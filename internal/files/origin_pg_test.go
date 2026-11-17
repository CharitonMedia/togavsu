package files

import (
	"context"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPGModulePublishOriginAndClientSource is CORE-16: optional origin on publishFile
// and bulkFile is accepted only from a module token; a client-only delegated viewer
// forces source client_upload and refuses origin firm; audit still names the module.
func TestPGModulePublishOriginAndClientSource(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.modules(map[string]bool{"mail-intake": true, "client-portal": true})
	mod := moduleP("mail-intake")
	open := e.open
	if _, err := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "matter", MatterID: &open, Source: "email"}, ""); err != nil {
		t.Fatal(err)
	}

	// Module token may set origin firm (outbound mail) even under an email grant that
	// would otherwise mark the file received.
	firmUp := e.stage(mod, UploadRequest{FileName: "sent.html", MatterID: open}, "<p>sent</p>")
	firm, err := e.svc.PublishUpload(ctx, mod, Publish{UploadID: firmUp, MatterID: &open, Origin: "firm"})
	if err != nil || firm.Origin != "firm" || firm.Source != "mail-intake" {
		t.Fatalf("origin firm = %+v %v", firm, err)
	}

	// Omitted origin keeps the grant rule: email grant → received.
	recvUp := e.stage(mod, UploadRequest{FileName: "inbox.html", MatterID: open}, "<p>in</p>")
	recv, err := e.svc.PublishUpload(ctx, mod, Publish{UploadID: recvUp, MatterID: &open})
	if err != nil || recv.Origin != "received" || recv.Source != "mail-intake" {
		t.Fatalf("omitted origin = %+v %v", recv, err)
	}

	// Staff may not pass origin.
	staffUp := e.stage(e.staff, UploadRequest{FileName: "staff.txt", MatterID: open}, "staff")
	if _, err := e.svc.PublishUpload(ctx, e.staff, Publish{UploadID: staffUp, Origin: "firm"}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("staff origin = %v", err)
	}

	// Client through the portal: source client_upload, origin received, uploader the
	// client, timeline client-visible; audit source is still the module.
	portal := domain.Principal{
		Source: domain.SourceModule, ModuleID: "client-portal",
		UserID: e.client.UserID, Roles: []domain.Role{domain.RoleClient},
	}
	clientUp := e.stage(portal, UploadRequest{FileName: "from-client.pdf", MatterID: open}, "%PDF")
	got, err := e.svc.PublishUpload(ctx, portal, Publish{UploadID: clientUp, MatterID: &open})
	if err != nil || got.Source != "client_upload" || got.Origin != "received" || !got.SharedWithClient || got.AttorneyOnly {
		t.Fatalf("client via module = %+v %v", got, err)
	}
	if got.CreatedBy != e.client.UserID {
		t.Fatalf("uploader = %s, want client %s", got.CreatedBy, e.client.UserID)
	}
	var auditSource, auditReason string
	if err := e.pool.QueryRowContext(ctx,
		`SELECT source, reason FROM audit_log WHERE entity_id = $1 AND field = 'name' ORDER BY created_at DESC LIMIT 1`,
		got.ID).Scan(&auditSource, &auditReason); err != nil {
		t.Fatal(err)
	}
	if auditSource != string(domain.SourceModule) || !strings.Contains(auditReason, "module=client-portal") {
		t.Fatalf("audit channel = %q %q", auditSource, auditReason)
	}
	var clientVisible bool
	if err := e.pool.QueryRowContext(ctx,
		`SELECT client_visible FROM events WHERE record_id = $1 AND action = 'file.created'`, got.ID).Scan(&clientVisible); err != nil || !clientVisible {
		t.Fatalf("timeline highlight = %v %v", clientVisible, err)
	}

	// origin firm with a client-only viewer is validation_failed.
	firmClient := e.stage(portal, UploadRequest{FileName: "bad-firm.pdf", MatterID: open}, "%PDF2")
	if _, err := e.svc.PublishUpload(ctx, portal, Publish{UploadID: firmClient, MatterID: &open, Origin: "firm"}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("client origin firm = %v", err)
	}
	if e.uploadStatus(firmClient) != "staged" {
		t.Fatal("refused publish changed the upload")
	}

	// bulkFile carries origin the same way.
	bulkUp := e.stage(mod, UploadRequest{FileName: "bulk-sent.html", MatterID: open}, "<p>bulk</p>")
	rows, err := e.svc.PublishBatch(ctx, mod, []BulkItem{{UploadID: bulkUp, MatterID: &open, Origin: "firm"}})
	if err != nil || len(rows) != 1 || rows[0].Origin != "firm" {
		t.Fatalf("bulk origin firm = %+v %v", rows, err)
	}
	badBulk := e.stage(portal, UploadRequest{FileName: "bulk-bad.pdf", MatterID: open}, "%PDF3")
	if _, err := e.svc.PublishBatch(ctx, portal, []BulkItem{{UploadID: badBulk, MatterID: &open, Origin: "firm"}}); !isCode(err, codeBatchRejected) {
		t.Fatalf("bulk client origin firm = %v", err)
	}
}
