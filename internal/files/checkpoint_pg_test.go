package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// TestPGTenderKeepsAPersonsFiling: a person's folder move, or a locked category the
// folder follows, is never undone by a Doc Tender reprocess, and Doc Tender does not
// create the folder it would have used.
func TestPGTenderKeepsAPersonsFiling(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sys := domain.Principal{Source: domain.SourceSystem, UserID: e.admin.UserID}
	evidence, err := e.svc.FolderByName(ctx, e.staff, e.open, "Evidence")
	if err != nil {
		t.Fatal(err)
	}
	folderOf := func(id domain.ID) domain.ID {
		t.Helper()
		got, err := e.svc.Get(ctx, e.staff, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.FolderID == nil {
			return ""
		}
		return *got.FolderID
	}
	tenderPick := func() bool {
		t.Helper()
		fs, err := e.svc.Folders(ctx, e.staff, e.open)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			if f.Name == "Tender pick" {
				return true
			}
		}
		return false
	}

	// A manual move locks the folder.
	moved := e.create(e.staff, NewFile{Name: "photo.jpg", MatterID: e.open}, "a")
	got, err := e.svc.MoveToFolder(ctx, e.staff, moved.ID, evidence.ID)
	if err != nil || !hasField(got.LockedFields, "folder") {
		t.Fatalf("move = %+v %v", got, err)
	}
	res, err := e.svc.ApplyTender(ctx, sys, moved.ID, TenderUpdate{Category: "court_filing", Folder: "Tender pick"})
	if err != nil {
		t.Fatal(err)
	}
	if folderOf(moved.ID) != evidence.ID || !hasField(res.Skipped, "folder") || hasField(res.Set, "folder") || !hasField(res.Set, "category") {
		t.Fatalf("tender after a manual move = %+v", res)
	}

	// A locked category keeps the folder too, even with no manual move.
	cat := "evidence_media"
	locked := e.create(e.staff, NewFile{Name: "scan.pdf", MatterID: e.open}, "b")
	if _, err := e.svc.UpdateMetadata(ctx, e.staff, locked.ID, Metadata{CategoryID: &cat}); err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.ApplyTender(ctx, sys, locked.ID, TenderUpdate{Category: "court_filing", Folder: "Tender pick"})
	if err != nil {
		t.Fatal(err)
	}
	if folderOf(locked.ID) != "" || !hasField(res.Skipped, "category") || !hasField(res.Skipped, "folder") || len(res.Set) != 0 {
		t.Fatalf("tender with a locked category = %+v", res)
	}
	if tenderPick() {
		t.Fatal("Doc Tender created a folder for a file a person had filed")
	}

	// An unsettled file is still filed.
	free := e.create(e.staff, NewFile{Name: "free.pdf", MatterID: e.open}, "c")
	res, err = e.svc.ApplyTender(ctx, sys, free.ID, TenderUpdate{Category: "court_filing", Folder: "Tender pick"})
	if err != nil || !hasField(res.Set, "folder") || folderOf(free.ID) == "" || !tenderPick() {
		t.Fatalf("tender on an unsettled file = %+v %v", res, err)
	}
}

func hasField(fields []string, f string) bool {
	for _, x := range fields {
		if x == f {
			return true
		}
	}
	return false
}

// TestPGFolderArchiveLeavesOutHiddenFiles: a folder zip holds only what the caller may
// see; naming a hidden file still denies the whole archive.
func TestPGFolderArchiveLeavesOutHiddenFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	folder, err := e.svc.FolderByName(ctx, e.attorney, e.open, "Correspondence")
	if err != nil {
		t.Fatal(err)
	}
	letter := e.create(e.staff, NewFile{Name: "letter.pdf", MatterID: e.open, FolderID: folder.ID}, "letter")
	memo := e.create(e.attorney, NewFile{Name: "memo.pdf", MatterID: e.open, FolderID: folder.ID, AttorneyOnly: true}, "memo")
	names := func(p domain.Principal) []string {
		t.Helper()
		var buf bytes.Buffer
		if err := e.svc.Archive(ctx, p, nil, folder.ID, false, &buf); err != nil {
			t.Fatalf("folder archive: %v", err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range zr.File {
			out = append(out, f.Name)
		}
		sort.Strings(out)
		return out
	}
	if got := names(e.staff); len(got) != 1 || got[0] != "letter.pdf" {
		t.Fatalf("staff folder zip = %v", got)
	}
	if got := names(e.attorney); len(got) != 2 || got[0] != "letter.pdf" || got[1] != "memo.pdf" {
		t.Fatalf("attorney folder zip = %v", got)
	}
	var n int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE record_id = $1 AND action = 'file.downloaded'`, memo.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("memo downloads logged = %d, want the attorney's one", n)
	}
	if err := e.svc.Archive(ctx, e.staff, []domain.ID{letter.ID, memo.ID}, "", false, &bytes.Buffer{}); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("staff naming the memo = %v", err)
	}
	if err := e.svc.Archive(ctx, e.outsider, nil, mustFolder(t, e, e.walled), false, &bytes.Buffer{}); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider walled folder archive = %v", err)
	}
}

func mustFolder(t *testing.T, e *env, matter domain.ID) domain.ID {
	t.Helper()
	f, err := e.svc.FolderByName(context.Background(), e.attorney, matter, "Correspondence")
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

// TestPGRestoreChecksTheDatabase: an archive whose manifest and bytes agree with each
// other but not with the restored database is refused, as is an object the database
// does not know; nothing is written for either.
func TestPGRestoreChecksTheDatabase(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.create(e.staff, NewFile{Name: "a.txt", MatterID: e.open}, "real bytes")
	var archive bytes.Buffer
	if _, err := e.svc.ExportBytes(ctx, e.admin, &archive, nil); err != nil {
		t.Fatal(err)
	}
	manifest := readManifest(t, archive.Bytes())
	if len(manifest) != 1 || manifest[0].ObjectID != *f.CurrentVersionID {
		t.Fatalf("manifest = %+v", manifest)
	}
	if err := os.RemoveAll(e.root + "/local/objects"); err != nil {
		t.Fatal(err)
	}

	// Same id, other bytes, manifest rewritten to match them.
	fake := []byte("other bytes, consistent manifest")
	m := manifest[0]
	m.Size, m.SHA256 = int64(len(fake)), shaHex(fake)
	if _, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(buildArchive(t, []BackupObject{m}, [][]byte{fake}))); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("mismatched archive restore = %v", err)
	}
	// An object the database does not know, pointed at another key.
	unknown := BackupObject{ObjectID: id(t), Kind: "version", FileID: f.ID, BackendID: e.local.ID, Key: "planted", Size: 4, SHA256: shaHex([]byte("evil"))}
	if _, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(buildArchive(t, []BackupObject{unknown}, [][]byte{[]byte("evil")}))); err == nil ||
		!strings.Contains(err.Error(), "no version or rendition") {
		t.Fatalf("unknown object restore = %v", err)
	}
	if n := localObjectFiles(t, e.root+"/local"); n != 0 {
		t.Fatalf("refused restores wrote %d objects", n)
	}
	// The genuine archive still restores and reads back.
	rep, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(archive.Bytes()))
	if err != nil || rep.Restored != 1 {
		t.Fatalf("restore = %+v %v", rep, err)
	}
	if _, err := e.svc.OpenVersion(ctx, e.staff, f.ID, *f.CurrentVersionID, false); err != nil {
		t.Fatalf("read after restore: %v", err)
	}
}

func readManifest(t *testing.T, archive []byte) []BackupObject {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(archive))
	if _, err := tr.Next(); err != nil {
		t.Fatal(err)
	}
	var out []BackupObject
	if err := json.NewDecoder(tr).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func buildArchive(t *testing.T, list []BackupObject, bodies [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	raw, _ := json.Marshal(list)
	if err := writeEntry(tw, manifestName, int64(len(raw)), bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for i, o := range list {
		if err := writeEntry(tw, "objects/"+string(o.ObjectID), int64(len(bodies[i])), bytes.NewReader(bodies[i])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// stagedUpload uploads body in one chunk and completes it.
func stagedUpload(t *testing.T, e *env, p domain.Principal, name, body string) Upload {
	t.Helper()
	ctx := context.Background()
	up, err := e.svc.CreateUpload(ctx, p, UploadRequest{FileName: name, MatterID: e.open, SizeBytes: int64(len(body))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutChunk(ctx, p, up.ID, 0, int64(len(body))-1, int64(len(body)), strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CompleteUpload(ctx, p, up.ID); err != nil {
		t.Fatal(err)
	}
	return up
}

// TestPGConcurrentPublishMakesOneFile: retries of one publish racing each other make
// one file, one version and one stored object; every caller gets that file.
func TestPGConcurrentPublishMakesOneFile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	up := stagedUpload(t, e, e.staff, "retry.pdf", "the same upload")
	const n = 6
	ids := make([]domain.ID, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, err := e.svc.PublishUpload(ctx, e.staff, Publish{UploadID: up.ID})
			ids[i], errs[i] = f.ID, err
		}(i)
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("publish %d = %s %v (first %s)", i, ids[i], errs[i], ids[0])
		}
	}
	var files, versions int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&files)
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_versions`).Scan(&versions)
	if files != 1 || versions != 1 || localObjectFiles(t, e.root+"/local") != 1 {
		t.Fatalf("files=%d versions=%d objects=%d", files, versions, localObjectFiles(t, e.root+"/local"))
	}

	// The same for a new version from one upload.
	vup := stagedUpload(t, e, e.staff, "retry.pdf", "second version")
	vids := make([]domain.ID, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := e.svc.AddUploadedVersion(ctx, e.staff, ids[0], vup.ID)
			vids[i], errs[i] = v.ID, err
		}(i)
	}
	wg.Wait()
	for i := range vids {
		if errs[i] != nil || vids[i] != vids[0] {
			t.Fatalf("add version %d = %s %v", i, vids[i], errs[i])
		}
	}
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_versions`).Scan(&versions)
	if versions != 2 || localObjectFiles(t, e.root+"/local") != 2 {
		t.Fatalf("versions=%d objects=%d", versions, localObjectFiles(t, e.root+"/local"))
	}
}

// TestPGLosingPublishRollsBack drives the race's losing side deterministically: a
// publish holding a stale staged row finds the upload taken, its file and version
// roll back and its stored copy is dropped.
func TestPGLosingPublishRollsBack(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	up := stagedUpload(t, e, e.staff, "race.pdf", "race bytes")
	stale, err := e.svc.st.getUpload(ctx, up.ID, false)
	if err != nil || stale.Status != "staged" {
		t.Fatalf("stale = %+v %v", stale, err)
	}
	won, err := e.svc.PublishUpload(ctx, e.staff, Publish{UploadID: up.ID})
	if err != nil {
		t.Fatal(err)
	}
	nf := NewFile{Name: "race.pdf", MatterID: e.open, MediaType: stale.MediaType, Source: "upload", How: "upload"}
	_, err = e.svc.create(ctx, e.staff, nf, strings.NewReader("race bytes"), stale.SizeBytes, stale.SHA256,
		func(ctx context.Context, f fileRow, v domain.FileVersion) error {
			stale.FileID, stale.VersionID, stale.UpdatedAt = f.ID, v.ID, e.clock.now()
			return e.svc.claimUpload(ctx, stale)
		})
	if !errors.Is(err, errUploadTaken) {
		t.Fatalf("losing create = %v", err)
	}
	var files int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&files)
	if files != 1 || localObjectFiles(t, e.root+"/local") != 1 {
		t.Fatalf("files=%d objects=%d after a lost publish", files, localObjectFiles(t, e.root+"/local"))
	}
	if got, err := e.svc.replayPublish(ctx, e.staff, up.ID, err); err != nil || got.ID != won.ID {
		t.Fatalf("replay = %+v %v", got, err)
	}
}
