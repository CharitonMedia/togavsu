package files

import (
	"context"
	"io"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPGTenderRespectsPersonLockedFields: a name or category a person set is never
// changed by Doc Tender; unlocked fields are set, clashes get -2, and the category's
// folder is used.
func TestPGTenderRespectsPersonLockedFields(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sys := domain.Principal{Source: domain.SourceSystem, UserID: e.admin.UserID}
	e.create(e.staff, NewFile{Name: "smith-2026-0001-letter-2026-10-01.pdf", MatterID: e.open}, "a")
	f := e.create(e.staff, NewFile{Name: "scan0001.pdf", MatterID: e.open}, "b")
	res, err := e.svc.ApplyTender(ctx, sys, f.ID, TenderUpdate{Name: "smith-2026-0001-letter-2026-10-01.pdf", Category: "correspondence", Folder: "Correspondence"})
	if err != nil {
		t.Fatal(err)
	}
	if res.File.Name != "smith-2026-0001-letter-2026-10-01-2.pdf" || len(res.Set) != 3 || len(res.Skipped) != 0 {
		t.Fatalf("tender = %+v", res)
	}
	got, _ := e.svc.Get(ctx, e.staff, f.ID)
	if got.ArrivedAs != "scan0001.pdf" || got.CategoryID == nil || got.FolderID == nil {
		t.Fatalf("file = %+v", got)
	}
	// A person renames and recategorizes; a reprocess must not undo either.
	if _, err := e.svc.Rename(ctx, e.staff, f.ID, "client-letter-about-custody"); err != nil {
		t.Fatal(err)
	}
	cat := "evidence_media"
	if _, err := e.svc.UpdateMetadata(ctx, e.staff, f.ID, Metadata{CategoryID: &cat}); err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.ApplyTender(ctx, sys, f.ID, TenderUpdate{Name: "smith-2026-0001-other.pdf", Category: "court_filing"})
	if err != nil {
		t.Fatal(err)
	}
	if res.File.Name != "client-letter-about-custody.pdf" || *res.File.CategoryID != "evidence_media" || len(res.Skipped) != 2 || len(res.Set) != 0 {
		t.Fatalf("locked tender = %+v", res)
	}
	if name, _ := e.svc.UniqueName(ctx, e.open, "client-letter-about-custody.pdf"); name != "client-letter-about-custody-2.pdf" {
		t.Fatalf("unique name = %s", name)
	}
	// A processing read is not a download.
	d, err := e.svc.OpenForProcessing(ctx, sys, f.ID, *f.CurrentVersionID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(d.Body)
	d.Close()
	var n int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE record_id = $1 AND action = 'file.downloaded'`, f.ID).Scan(&n)
	if string(b) != "b" || n != 0 {
		t.Fatalf("processing read %q logged %d downloads", b, n)
	}
	if _, err := e.svc.MatterFiles(ctx, e.staff, e.open); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("staff MatterFiles = %v", err)
	}
	if rows, err := e.svc.MatterFiles(ctx, sys, e.open); err != nil || len(rows) != 2 {
		t.Fatalf("system MatterFiles = %v %v", rows, err)
	}
	// Grouped photos: a grouped PDF linked to each photo source.
	photo := e.create(e.staff, NewFile{Name: "IMG_0001.jpg", MatterID: e.open}, "jpeg")
	pdf := e.create(sys, NewFile{Name: "lease.pdf", MatterID: e.open, How: "grouping"}, "%PDF")
	if _, err := e.svc.Link(ctx, sys, pdf.ID, photo.ID, "grouped_photo_source"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Link(ctx, sys, pdf.ID, photo.ID, "photo_of"); !isCode(err, domain.CodeValidation) {
		t.Fatalf("unknown relationship = %v", err)
	}
	other := e.create(e.attorney, NewFile{Name: "w.jpg", MatterID: e.walled}, "w")
	if _, err := e.svc.Link(ctx, sys, pdf.ID, other.ID, "attachment"); !isCode(err, domain.CodeValidation) {
		t.Fatalf("cross-matter link = %v", err)
	}
	ls, err := e.svc.Links(ctx, e.staff, photo.ID)
	if err != nil || len(ls) != 1 || ls[0].Relationship != "grouped_photo_source" || ls[0].FromFileID != pdf.ID {
		t.Fatalf("links = %+v %v", ls, err)
	}
}
