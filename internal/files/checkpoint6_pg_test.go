package files

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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
