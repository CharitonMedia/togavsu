package files

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// localPath finds the file that holds object on the local disk backend.
func localPath(t *testing.T, e *env, object domain.ID) string {
	t.Helper()
	var path string
	_ = filepath.Walk(e.root+"/local/objects", func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == string(object) {
			path = p
		}
		return nil
	})
	if path == "" {
		t.Fatalf("object %s is not on local disk", object)
	}
	return path
}

// TestPGFailedMoveResumesWithoutLosingBytes is checkpoint 6 finding 1 and checkpoint 7
// finding 1: a failed move keeps every old copy and cannot be confirmed; starting the
// same move again resumes it. An undo move in between (new backend back to the old one)
// points some records back at their old copies, and the resumed move's confirmation
// leaves those current copies alone.
func TestPGFailedMoveResumesWithoutLosingBytes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	files := seedFiles(t, e, 2)
	_, s3 := e.s3()
	req := MoveRequest{FromBackendID: e.local.ID, ToBackendID: s3.ID, Scope: "all"}
	m, err := e.svc.StartMove(ctx, e.admin, req)
	if err != nil {
		t.Fatal(err)
	}
	items, err := e.svc.st.moveItems(ctx, m.ID)
	if err != nil || len(items) != m.ProgressTotal {
		t.Fatalf("items = %d %v", len(items), err)
	}
	// The last object the run reaches is corrupt, so every other one checks out first.
	bad := items[len(items)-1].ObjectID
	path := localPath(t, e, bad)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corruptLocal(t, e, bad)
	if err := e.svc.RunMove(ctx, m.ID); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("move over a corrupt object = %v", err)
	}
	if _, err := e.svc.ConfirmMove(ctx, e.admin, m.ID); !isCode(err, domain.CodeConflict) {
		t.Fatalf("confirm a failed move = %v", err)
	}
	if n := localObjectFiles(t, e.root+"/local"); n != m.ProgressTotal {
		t.Fatalf("a failed move deleted old copies: %d of %d left", n, m.ProgressTotal)
	}
	if n := objectsOnBackend(t, e, s3.ID); n != m.ProgressTotal-1 {
		t.Fatalf("objects on s3 after the failure = %d", n)
	}

	// Undo: move what reached S3 back to local disk, and confirm that.
	undo, err := e.svc.StartMove(ctx, e.admin, MoveRequest{FromBackendID: s3.ID, ToBackendID: e.local.ID, Scope: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RunMove(ctx, undo.ID); err != nil {
		t.Fatalf("undo move = %v", err)
	}
	if _, err := e.svc.ConfirmMove(ctx, e.admin, undo.ID); err != nil {
		t.Fatal(err)
	}

	// Repair the source and start the first move again: it resumes, not a new move.
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.StartMove(ctx, e.admin, req)
	if err != nil || again.ID != m.ID {
		t.Fatalf("restart = %+v %v, want move %s resumed", again, err, m.ID)
	}
	if err := e.svc.RunMove(ctx, m.ID); err != nil {
		t.Fatalf("resumed move = %v", err)
	}
	done, err := e.svc.GetMove(ctx, e.admin, m.ID)
	if err != nil || done.Status != "awaiting_confirmation" || done.ProgressDone != done.ProgressTotal {
		t.Fatalf("resumed move = %+v %v", done, err)
	}
	if _, err := e.svc.ConfirmMove(ctx, e.admin, m.ID); err != nil {
		t.Fatal(err)
	}
	// The objects the undo brought back live on local disk again; the confirmation
	// did not delete them, and every file still reads.
	readAllFiles(t, e, files)
	if local := objectsOnBackend(t, e, e.local.ID); local != m.ProgressTotal-1 || localObjectFiles(t, e.root+"/local") != local {
		t.Fatalf("local objects = %d recorded, %d on disk", local, localObjectFiles(t, e.root+"/local"))
	}
}

// TestPGConcurrentTenderNamesStayUnique is checkpoint 6 finding 3: Doc Tender filing
// two files of one matter at once under the same name gives one the -2 suffix.
func TestPGConcurrentTenderNamesStayUnique(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sys := domain.Principal{Source: domain.SourceSystem, UserID: e.admin.UserID}
	const n = 4
	var ids []domain.ID
	for i := 0; i < n; i++ {
		ids = append(ids, e.create(e.staff, NewFile{Name: "scan-" + string(rune('a'+i)) + ".pdf", MatterID: e.open}, "receipt").ID)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id domain.ID) {
			defer wg.Done()
			<-start
			_, errs[i] = e.svc.ApplyTender(ctx, sys, id, TenderUpdate{Name: "2026-11-10 Receipt.pdf"})
		}(i, id)
	}
	close(start)
	wg.Wait()
	seen := map[string]bool{}
	for i, id := range ids {
		if errs[i] != nil {
			t.Fatalf("tender %d = %v", i, errs[i])
		}
		_, _, name, _ := fileState(t, e, id)
		if seen[name] {
			t.Fatalf("two files named %q", name)
		}
		seen[name] = true
	}
	if !seen["2026-11-10 Receipt.pdf"] || !seen["2026-11-10 Receipt-2.pdf"] {
		t.Fatalf("names = %v", seen)
	}
}

// TestPGRenameFolderToATakenNameIsAConflict is checkpoint 6 finding 4: renaming a
// folder to a name the matter already has, in any case, is a 409, not a 500.
func TestPGRenameFolderToATakenNameIsAConflict(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, err := e.svc.CreateFolder(ctx, e.attorney, e.open, "Experts", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.CreateFolder(ctx, e.attorney, e.open, "Drafts", 1)
	if err != nil {
		t.Fatal(err)
	}
	taken := "experts"
	if _, err := e.svc.UpdateFolder(ctx, e.attorney, b.ID, &taken, nil); !isCode(err, domain.CodeConflict) {
		t.Fatalf("rename to a taken name = %v", err)
	}
	// Renaming a folder to its own name in another case is fine.
	own := "EXPERTS"
	if got, err := e.svc.UpdateFolder(ctx, e.attorney, a.ID, &own, nil); err != nil || got.Name != own {
		t.Fatalf("rename to own name = %+v %v", got, err)
	}
	srv := e.server(func(*http.Request) (domain.Principal, error) { return e.attorney, nil })
	c := apiClient{t: t, url: srv.URL}
	code, _ := c.json("PATCH", "/api/v1/folders/"+string(b.ID), map[string]any{"name": "Experts"}, nil)
	if code != http.StatusConflict {
		t.Fatalf("PATCH to a taken name = %d", code)
	}
}

// TestPGPurgeRemovesTheSearchText is checkpoint 8 finding 1: emptying a file from the
// trash also removes the lowercased Markdown text Files search keeps for it.
func TestPGPurgeRemovesTheSearchText(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.create(e.staff, NewFile{Name: "memo.txt", MatterID: e.open}, "memo")
	md := "# Settlement terms\nconfidential figures\n"
	if _, err := e.svc.AddRendition(ctx, e.attorney, f.ID, *f.CurrentVersionID, "markdown", strings.NewReader(md), int64(len(md))); err != nil {
		t.Fatal(err)
	}
	rows := func() int {
		t.Helper()
		var n int
		if err := e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_search_text WHERE file_id = $1`, f.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if rows() != 1 {
		t.Fatal("no search text before the purge")
	}
	purgeFile(t, e, f)
	if n := rows(); n != 0 {
		t.Fatalf("search text rows after the purge = %d", n)
	}
}

// TestPGDropSubfoldersStayInsideTheRoot is checkpoint 8 finding 2: a matter number
// with a separator gets a hyphenated subfolder, and one that would leave the drop root
// gets none.
func TestPGDropSubfoldersStayInsideTheRoot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	root := filepath.Join(e.root, "share", "drop")
	matters := []Matter{
		{ID: e.open, Number: "2026/0001", Status: "open"},
		{ID: e.walled, Number: "../escape", Status: "open"},
	}
	if _, err := e.svc.scanOne(ctx, domain.StorageBackend{}, root, matters, 0); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "2026-0001")); err != nil || !fi.IsDir() {
		t.Fatalf("hyphenated subfolder = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "2026")); !os.IsNotExist(err) {
		t.Fatalf("nested subfolder made: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "share", "escape")); !os.IsNotExist(err) {
		t.Fatalf("a folder was made outside the drop root: %v", err)
	}
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"2026-0001", "2026-0001", true}, {"2026/0001", "2026-0001", true}, {`2026\0001`, "2026-0001", true},
		{"../escape", "", false}, {"..", "", false}, {".hidden", "", false}, {"", "", false}, {"a/../b", "", false},
	} {
		if got, ok := dropFolderName(c.in); got != c.want || ok != c.ok {
			t.Fatalf("dropFolderName(%q) = %q %v", c.in, got, ok)
		}
	}
}

// onLocalDisk reports whether the local disk backend still holds object.
func onLocalDisk(e *env, object domain.ID) bool {
	found := false
	_ = filepath.Walk(e.root+"/local/objects", func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == string(object) {
			found = true
		}
		return nil
	})
	return found
}

func purgeAsAttorney(t *testing.T, e *env, f FileView) []domain.ID {
	t.Helper()
	ctx := context.Background()
	objs, err := e.svc.objectsOf(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Trash(ctx, e.attorney, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Purge(ctx, e.admin, f.ID, f.Name); err != nil {
		t.Fatal(err)
	}
	var ids []domain.ID
	for _, o := range objs {
		ids = append(ids, o.ID)
	}
	return ids
}

// TestPGPurgeLeavesNoMoveCopies is checkpoint 9: emptying a file from the trash removes
// every copy of its bytes that a storage move made or kept, on both backends, whether
// the move failed or is waiting for confirmation.
func TestPGPurgeLeavesNoMoveCopies(t *testing.T) {
	t.Run("failed move", func(t *testing.T) {
		e := newEnv(t)
		ctx := context.Background()
		files := seedFiles(t, e, 1)
		fs3, s3 := e.s3()
		m, err := e.svc.StartMove(ctx, e.admin, MoveRequest{FromBackendID: e.local.ID, ToBackendID: s3.ID, Scope: "all"})
		if err != nil {
			t.Fatal(err)
		}
		items, err := e.svc.st.moveItems(ctx, m.ID)
		if err != nil || len(items) < 2 {
			t.Fatalf("items = %d %v", len(items), err)
		}
		bad := items[len(items)-1].ObjectID
		corruptLocal(t, e, bad)
		if err := e.svc.RunMove(ctx, m.ID); !errors.Is(err, storage.ErrIntegrity) {
			t.Fatalf("move over a corrupt object = %v", err)
		}
		// A run that stopped mid-copy can leave a copy on the target under the object id.
		raw, _, err := e.svc.adapter(ctx, s3.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := raw.Put(ctx, string(bad), strings.NewReader("partial"), int64(len("partial"))); err != nil {
			t.Fatal(err)
		}
		purgeAsAttorney(t, e, files[0].file)
		if n := localObjectFiles(t, e.root+"/local"); n != 0 {
			t.Fatalf("local disk keeps %d copies of the purged file", n)
		}
		if fs3.Len() != 0 {
			t.Fatalf("s3 keeps %d copies of the purged file: %v", fs3.Len(), fs3.Keys())
		}
	})
	t.Run("move awaiting confirmation", func(t *testing.T) {
		e := newEnv(t)
		ctx := context.Background()
		files := seedFiles(t, e, 1)
		kept := e.create(e.attorney, NewFile{Name: "kept.txt", MatterID: e.open}, "stays")
		fs3, s3 := e.s3()
		m, err := e.svc.StartMove(ctx, e.admin, MoveRequest{FromBackendID: e.local.ID, ToBackendID: s3.ID, Scope: "all"})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.svc.RunMove(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		gone := purgeAsAttorney(t, e, files[0].file)
		for _, id := range gone {
			if onLocalDisk(e, id) || fs3.Get(string(id)) != nil {
				t.Fatalf("object %s of the purged file is still stored", id)
			}
		}
		if _, err := e.svc.ConfirmMove(ctx, e.admin, m.ID); err != nil {
			t.Fatalf("confirm after a purge = %v", err)
		}
		if n := localObjectFiles(t, e.root+"/local"); n != 0 {
			t.Fatalf("local disk keeps %d copies after the confirmation", n)
		}
		if got := readVersion(t, e, e.attorney, kept.ID, *kept.CurrentVersionID); got != "stays" {
			t.Fatalf("the kept file reads %q", got)
		}
	})
}
