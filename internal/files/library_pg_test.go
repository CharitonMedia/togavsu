package files

import (
	"context"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPGLibraryFiles covers CORE-17: firm library placement, list filter, access, and
// updateFileMetadata refusal of matter or contact on a library file.
func TestPGLibraryFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	up := e.stage(e.staff, UploadRequest{FileName: "handbook.pdf", Library: true}, "firm handbook")
	lib, err := e.svc.PublishUpload(ctx, e.staff, Publish{UploadID: up, Library: true})
	if err != nil || !lib.Library || lib.MatterID != nil || lib.ContactID != nil {
		t.Fatalf("publish library = %+v %v", lib, err)
	}
	if n := e.count(`SELECT COUNT(*) FROM events WHERE record_id = ? AND action = 'file.version_created'`, lib.ID); n != 1 {
		t.Fatalf("file.version_created = %d", n)
	}
	if len(e.versions) != 1 || !e.versions[0].Library || e.versions[0].FileID != lib.ID {
		t.Fatalf("OnVersion for library = %+v", e.versions)
	}

	got, err := e.svc.Get(ctx, e.staff, lib.ID)
	if err != nil || !got.Library {
		t.Fatalf("staff get = %+v %v", got, err)
	}
	if _, err := e.svc.Get(ctx, e.accountant, lib.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("accountant get = %v", err)
	}
	if _, err := e.svc.Get(ctx, e.client, lib.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("client get = %v", err)
	}

	yes, no := true, false
	onlyLib, _, err := e.svc.List(ctx, e.staff, ListQuery{Library: &yes})
	if err != nil || len(onlyLib) != 1 || onlyLib[0].ID != lib.ID {
		t.Fatalf("list library=true = %v %v", onlyLib, err)
	}
	matterFile := e.create(e.staff, NewFile{Name: "motion.txt", MatterID: e.open}, "body")
	nonLib, _, err := e.svc.List(ctx, e.staff, ListQuery{Library: &no})
	if err != nil || len(nonLib) == 0 {
		t.Fatalf("list library=false = %v %v", nonLib, err)
	}
	for _, f := range nonLib {
		if f.Library || f.ID == lib.ID {
			t.Fatalf("library=false included %+v", f)
		}
	}
	_ = matterFile

	matter := e.open
	if _, err := e.svc.CreateUpload(ctx, e.staff, UploadRequest{FileName: "bad.pdf", Library: true, MatterID: matter, SizeBytes: 1}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("library with matter = %v", err)
	}
	if _, err := e.svc.CreateUpload(ctx, e.staff, UploadRequest{FileName: "bad.pdf", Library: true, ContactID: e.clientContact, SizeBytes: 1}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("library with contact = %v", err)
	}
	badUp := e.stage(e.staff, UploadRequest{FileName: "onto-matter.pdf", MatterID: e.open}, "bytes")
	if _, err := e.svc.PublishUpload(ctx, e.staff, Publish{UploadID: badUp, Library: true}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("publish library onto matter = %v", err)
	}
	if e.uploadStatus(badUp) != "staged" {
		t.Fatal("failed library publish changed the upload")
	}

	if _, err := e.svc.UpdateMetadata(ctx, e.staff, lib.ID, Metadata{MatterID: &matter}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("metadata matter on library = %v", err)
	}
	contact := e.clientContact
	if _, err := e.svc.UpdateMetadata(ctx, e.staff, lib.ID, Metadata{ContactID: &contact}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("metadata contact on library = %v", err)
	}

	bulkUp := e.stage(e.attorney, UploadRequest{FileName: "policy.docx", Library: true}, "policy text")
	rows, err := e.svc.PublishBatch(ctx, e.attorney, []BulkItem{{UploadID: bulkUp, Library: true}})
	if err != nil || len(rows) != 1 || !rows[0].Library {
		t.Fatalf("bulk library = %+v %v", rows, err)
	}

	direct, err := e.svc.Create(ctx, e.admin, NewFile{Name: "template.docx", Library: true}, strings.NewReader("tpl"), 3)
	if err != nil || !direct.Library {
		t.Fatalf("direct create = %+v %v", direct, err)
	}
	if _, err := e.svc.Create(ctx, e.admin, NewFile{Name: "nope.docx", Library: true, MatterID: e.open}, strings.NewReader("x"), 1); !isCode(err, domain.CodeValidation) {
		t.Fatalf("direct create with matter = %v", err)
	}
}
