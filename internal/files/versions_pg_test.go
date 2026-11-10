package files

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func versionRows(t *testing.T, e *env) map[string]string {
	t.Helper()
	rows, err := e.pool.QueryContext(context.Background(), `SELECT id, created_at, updated_at, created_by, file_id, number, storage_backend_id,
storage_key, size_bytes, sha256, media_type, saved_by, saved_at, how, note, quarantined FROM file_versions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id string
		vals := make([]any, 15)
		ptrs := make([]any, 16)
		ptrs[0] = &id
		for i := range vals {
			ptrs[i+1] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out[id] = fmt.Sprint(vals...)
	}
	return out
}

func readVersion(t *testing.T, e *env, p domain.Principal, file, version domain.ID) string {
	t.Helper()
	d, err := e.svc.OpenVersion(context.Background(), p, file, version, false)
	if err != nil {
		t.Fatalf("open %s: %v", version, err)
	}
	defer d.Close()
	b, err := io.ReadAll(d.Body)
	if err != nil {
		t.Fatalf("read %s: %v", version, err)
	}
	return string(b)
}

// TestPGEverySaveAddsAVersionAndNoVersionChanges is AC-039's version clause.
func TestPGEverySaveAddsAVersionAndNoVersionChanges(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.create(e.staff, NewFile{Name: "motion.docx", MatterID: e.open}, "draft one")
	v1 := *f.CurrentVersionID
	before := versionRows(t, e)
	var saved []VersionView
	for i, body := range []string{"draft two", "draft three"} {
		how := []string{"editor", "module"}[i]
		v, err := e.svc.SaveVersion(ctx, e.staff, f.ID, strings.NewReader(body), int64(len(body)), SaveOptions{How: how})
		if err != nil {
			t.Fatal(err)
		}
		if v.Number != i+2 || v.SHA256 != shaHex([]byte(body)) || v.How != how || v.SavedBy != e.staff.UserID {
			t.Fatalf("version %d = %+v", i+2, v)
		}
		saved = append(saved, v)
	}
	// Restore copies v1 forward as v4; v1 itself is unchanged.
	v4, err := e.svc.RestoreVersion(ctx, e.staff, f.ID, v1)
	if err != nil || v4.Number != 4 || v4.SHA256 != shaHex([]byte("draft one")) || !strings.Contains(v4.Note, "version 1") {
		t.Fatalf("restore = %+v %v", v4, err)
	}
	// A scanner's quarantine mark and a rename do not touch any version row.
	if _, err := e.svc.Quarantine(ctx, e.admin, f.ID, saved[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Rename(ctx, e.staff, f.ID, "motion-to-withdraw"); err != nil {
		t.Fatal(err)
	}
	after := versionRows(t, e)
	for id, row := range before {
		if after[id] != row {
			t.Fatalf("version %s changed:\n%s\n%s", id, row, after[id])
		}
	}
	if len(after) != 4 {
		t.Fatalf("versions = %d", len(after))
	}
	if got := readVersion(t, e, e.staff, f.ID, v1); got != "draft one" {
		t.Fatalf("v1 = %q", got)
	}
	if got := readVersion(t, e, e.staff, f.ID, v4.ID); got != "draft one" {
		t.Fatalf("v4 = %q", got)
	}
	if _, err := e.svc.OpenVersion(ctx, e.staff, f.ID, saved[0].ID, false); err != ErrQuarantined {
		t.Fatalf("quarantined download without confirm = %v", err)
	}
	if d, err := e.svc.OpenVersion(ctx, e.staff, f.ID, saved[0].ID, true); err != nil {
		t.Fatalf("quarantined with confirm = %v", err)
	} else {
		d.Close()
	}
	vs, err := e.svc.Versions(ctx, e.staff, f.ID)
	if err != nil || len(vs) != 4 || !vs[1].Quarantined || vs[0].Quarantined {
		t.Fatalf("versions = %+v %v", vs, err)
	}
	got, err := e.svc.Get(ctx, e.staff, f.ID)
	if err != nil || got.Name != "motion-to-withdraw.docx" || got.ArrivedAs != "motion.docx" || *got.CurrentVersionID != v4.ID {
		t.Fatalf("file = %+v %v", got, err)
	}
	if len(got.LockedFields) != 1 || got.LockedFields[0] != "name" {
		t.Fatalf("a person's rename must lock the name: %v", got.LockedFields)
	}
	// Each version's bytes live under its own opaque key, never the file name.
	err = filepath.Walk(e.root+"/local", func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.Contains(filepath.Base(p), "motion") {
			t.Errorf("a file name reached storage: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := e.svc.History(ctx, e.staff, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	saves, edits := 0, 0
	for _, x := range h {
		if x.Kind == "version_save" {
			saves++
		} else if x.Field == "name" && x.OldValue == "motion.docx" {
			edits++
		}
	}
	if saves != 4 || edits != 1 {
		t.Fatalf("history saves %d renames %d: %+v", saves, edits, h)
	}
	if n := len(e.versions); n != 4 {
		t.Fatalf("file.version_created events = %d", n)
	}
}

func TestPGTrashRestoreAndAdminPurge(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.create(e.staff, NewFile{Name: "old-draft.docx", MatterID: e.open}, "bytes")
	if err := e.svc.Purge(ctx, e.admin, f.ID, f.Name); !isCode(err, domain.CodeConflict) {
		t.Fatalf("purge outside trash = %v", err)
	}
	if _, err := e.svc.Trash(ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	trash, _, err := e.svc.List(ctx, e.staff, ListQuery{MatterID: e.open, Trashed: true})
	if err != nil || len(trash) != 1 || trash[0].TrashedAt == nil {
		t.Fatalf("trash = %v %v", trash, err)
	}
	if live, _, _ := e.svc.List(ctx, e.staff, ListQuery{MatterID: e.open}); len(live) != 0 {
		t.Fatalf("a trashed file is in the live list")
	}
	// Still readable while in the trash.
	if got := readVersion(t, e, e.staff, f.ID, *f.CurrentVersionID); got != "bytes" {
		t.Fatalf("trashed read %q", got)
	}
	if err := e.svc.Purge(ctx, e.attorney, f.ID, f.Name); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("non-admin purge = %v", err)
	}
	if err := e.svc.Purge(ctx, e.admin, f.ID, "wrong name"); !isCode(err, domain.CodeValidation) {
		t.Fatalf("purge without the typed name = %v", err)
	}
	if _, err := e.svc.Restore(ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Trash(ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Purge(ctx, e.admin, f.ID, f.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, e.admin, f.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("purged get = %v", err)
	}
	var n int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE id = $1 AND purged_at <> 0`, f.ID).Scan(&n)
	if n != 1 {
		t.Fatal("the purged record must stay for the audit log")
	}
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity_id = $1 AND field = 'purged_at'`, f.ID).Scan(&n)
	if n != 1 {
		t.Fatal("purge is not in the audit log")
	}
	b, err := e.svc.at(ctx, location{Backend: e.local.ID, Key: string(*f.CurrentVersionID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stat(ctx, string(*f.CurrentVersionID)); err == nil {
		t.Fatal("purged bytes are still stored")
	}
}

func TestPGFoldersAndMetadataLocks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	folders, err := e.svc.Folders(ctx, e.staff, e.open)
	if err != nil || len(folders) != 7 || folders[0].Name != "Correspondence" {
		t.Fatalf("default folders = %+v %v", folders, err)
	}
	f := e.create(e.staff, NewFile{Name: "letter.pdf", MatterID: e.open, FolderID: folders[0].ID}, "x")
	moved, err := e.svc.MoveToFolder(ctx, e.staff, f.ID, folders[1].ID)
	if err != nil || moved.FolderID == nil || *moved.FolderID != folders[1].ID {
		t.Fatalf("move = %+v %v", moved, err)
	}
	other, _ := e.svc.Folders(ctx, e.attorney, e.walled)
	if _, err := e.svc.MoveToFolder(ctx, e.staff, f.ID, other[0].ID); !isCode(err, domain.CodeValidation) {
		t.Fatalf("move into another matter's folder = %v", err)
	}
	if err := e.svc.DeleteFolder(ctx, e.staff, folders[1].ID); !isCode(err, domain.CodeConflict) {
		t.Fatalf("delete a folder in use = %v", err)
	}
	cat := "correspondence"
	got, err := e.svc.UpdateMetadata(ctx, e.staff, f.ID, Metadata{CategoryID: &cat})
	if err != nil || got.CategoryID == nil || *got.CategoryID != cat || len(got.LockedFields) != 1 || got.LockedFields[0] != "category" {
		t.Fatalf("category = %+v %v", got, err)
	}
	bad := "not_a_category"
	if _, err := e.svc.UpdateMetadata(ctx, e.staff, f.ID, Metadata{CategoryID: &bad}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("unknown category = %v", err)
	}
	if _, err := e.svc.MarkStatus(ctx, e.staff, f.ID, "filed"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkStatus(ctx, e.staff, f.ID, "approved"); !isCode(err, domain.CodeValidation) {
		t.Fatalf("bad status = %v", err)
	}
	// Moving the file to the walled matter needs access there; the staff member is on it.
	if _, err := e.svc.UpdateMetadata(ctx, e.outsider, f.ID, Metadata{MatterID: &e.walled}); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider moves file into walled matter = %v", err)
	}
}
