package files

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// dropShare adds a network path backend with its drop folder on and makes the matter
// subfolders. It returns the drop folder.
func dropShare(t *testing.T, e *env) string {
	t.Helper()
	ctx := context.Background()
	share := filepath.Join(e.root, "share")
	raw, _ := json.Marshal(map[string]string{"path": share})
	msg := json.RawMessage(raw)
	yes := true
	if _, err := e.svc.CreateBackend(ctx, e.admin, BackendWrite{Kind: "network_path", Name: "NAS", DropFolder: &yes, Settings: &msg}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ScanDrop(ctx); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(share, "drop")
}

// scanUntil runs drop passes, advancing past the steady time, and returns every file taken.
func scanUntil(t *testing.T, e *env, svc *Service, passes int) []FileView {
	t.Helper()
	var out []FileView
	for i := 0; i < passes; i++ {
		got, err := svc.ScanDrop(context.Background())
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		out = append(out, got...)
		e.clock.advance(31 * time.Second)
	}
	return out
}

// TestPGDropFolderRedropAfterPurgeIsANewRecord is checkpoint 3 finding 1: a document
// taken, trashed and emptied from the trash, then dropped again with the same name and
// bytes, becomes a new record rather than being deleted from the share unrecorded. The
// same holds for a top-level drop matching a purged file and for a live re-drop.
func TestPGDropFolderRedropAfterPurgeIsANewRecord(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	drop := dropShare(t, e)
	inMatter := filepath.Join(drop, "2026-0001", "retainer.pdf")
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(inMatter, "signed retainer")
	first := scanUntil(t, e, e.svc, 2)
	if len(first) != 1 {
		t.Fatalf("first drop = %v", first)
	}
	if _, err := os.Stat(inMatter); !os.IsNotExist(err) {
		t.Fatal("the first drop stayed on the share")
	}
	if _, err := e.svc.Trash(ctx, e.staff, first[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Purge(ctx, e.admin, first[0].ID, "retainer.pdf"); err != nil {
		t.Fatal(err)
	}

	// The same document dropped again into the same matter is a new record.
	write(inMatter, "signed retainer")
	again := scanUntil(t, e, e.svc, 2)
	if len(again) != 1 || again[0].ID == first[0].ID {
		t.Fatalf("re-drop after purge = %v", again)
	}
	if got := readVersion(t, e, e.staff, again[0].ID, *again[0].CurrentVersionID); got != "signed retainer" {
		t.Fatalf("re-dropped bytes = %q", got)
	}

	// A deliberate re-drop of a live file's bytes is also a new record, not a silent delete.
	write(inMatter, "signed retainer")
	live := scanUntil(t, e, e.svc, 2)
	if len(live) != 1 || live[0].ID == again[0].ID {
		t.Fatalf("live re-drop = %v", live)
	}

	// A top-level drop with the same name and bytes as a purged matter file is taken.
	if _, err := e.svc.Trash(ctx, e.staff, live[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Purge(ctx, e.admin, live[0].ID, live[0].Name); err != nil {
		t.Fatal(err)
	}
	top := filepath.Join(drop, "retainer.pdf")
	write(top, "signed retainer")
	topTaken := scanUntil(t, e, e.svc, 2)
	if len(topTaken) != 1 || topTaken[0].MatterID != nil {
		t.Fatalf("top-level re-drop = %v", topTaken)
	}
	var n int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE source = 'drop_folder'`).Scan(&n)
	if n != 4 {
		t.Fatalf("records = %d, want 4", n)
	}
}

// TestPGDropFolderRefusedDeleteThenPurgedIsTakenAgain: when the share refused the
// delete and the record was later emptied from the trash, the share copy is the only
// copy left; the scanner takes it again instead of retrying the delete.
func TestPGDropFolderRefusedDeleteThenPurgedIsTakenAgain(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	drop := dropShare(t, e)
	e.svc.removeDrop = func(string) error { return &fs.PathError{Op: "remove", Path: "x", Err: fs.ErrPermission} }
	p := filepath.Join(drop, "2026-0001", "held.pdf")
	if err := os.WriteFile(p, []byte("held open"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := scanUntil(t, e, e.svc, 3)
	if len(first) != 1 {
		t.Fatalf("first = %v", first)
	}
	if _, err := e.svc.Trash(ctx, e.staff, first[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Purge(ctx, e.admin, first[0].ID, "held.pdf"); err != nil {
		t.Fatal(err)
	}
	e.svc.removeDrop = os.Remove
	again := scanUntil(t, e, e.svc, 2)
	if len(again) != 1 || again[0].ID == first[0].ID {
		t.Fatalf("after purge = %v", again)
	}
	if got := readVersion(t, e, e.staff, again[0].ID, *again[0].CurrentVersionID); got != "held open" {
		t.Fatalf("bytes = %q", got)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("the share copy was not removed after it was recorded again")
	}
}

// TestPGAttorneyOnlyEventsAreNotClientVisible is checkpoint 3 finding 2: a file that
// is attorney-only and shared never produces client-visible timeline rows, while a
// plain shared file and a client's own download still do.
func TestPGAttorneyOnlyEventsAreNotClientVisible(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	visible := func(file domain.ID) (shown, hidden int) {
		t.Helper()
		rows, err := e.pool.QueryContext(ctx, `SELECT client_visible FROM events WHERE record_id = $1`, file)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			if v == 1 {
				shown++
			} else {
				hidden++
			}
		}
		return shown, hidden
	}
	secret := e.create(e.attorney, NewFile{Name: "strategy memo.docx", MatterID: e.open, AttorneyOnly: true, SharedWithClient: true}, "plan")
	if _, err := e.svc.SaveVersion(ctx, e.attorney, secret.ID, strings.NewReader("plan b"), 6, SaveOptions{How: "editor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Rename(ctx, e.attorney, secret.ID, "strategy memo v2.docx"); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.OpenVersion(ctx, e.attorney, secret.ID, *secret.CurrentVersionID, false)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	sys := domain.Principal{Source: domain.SourceSystem, UserID: e.admin.UserID}
	if _, err := e.svc.ApplyTender(ctx, sys, secret.ID, TenderUpdate{Folder: "Correspondence"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.OpenVersion(ctx, e.client, secret.ID, *secret.CurrentVersionID, false); err == nil {
		t.Fatal("a client opened an attorney-only file")
	}
	if shown, hidden := visible(secret.ID); shown != 0 || hidden < 5 {
		t.Fatalf("attorney-only events: %d client-visible, %d hidden", shown, hidden)
	}

	// Turning attorney-only on for a shared file hides that change and what follows.
	shared := e.create(e.attorney, NewFile{Name: "engagement letter.pdf", MatterID: e.open, SharedWithClient: true}, "letter")
	if shown, _ := visible(shared.ID); shown != 1 {
		t.Fatalf("a shared file's add should be client-visible: %d", shown)
	}
	d, err = e.svc.OpenVersion(ctx, e.client, shared.ID, *shared.CurrentVersionID, false)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	var kind string
	var cv int
	if err := e.pool.QueryRowContext(ctx, `SELECT actor_kind, client_visible FROM events WHERE record_id = $1 AND action = 'file.downloaded'`, shared.ID).Scan(&kind, &cv); err != nil || cv != 1 {
		t.Fatalf("client download event = %s %d %v", kind, cv, err)
	}
	yes := true
	if _, err := e.svc.UpdateMetadata(ctx, e.attorney, shared.ID, Metadata{AttorneyOnly: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Rename(ctx, e.attorney, shared.ID, "engagement letter (attorney notes).pdf"); err != nil {
		t.Fatal(err)
	}
	if shown, hidden := visible(shared.ID); shown != 2 || hidden != 2 {
		t.Fatalf("after attorney-only: %d client-visible, %d hidden", shown, hidden)
	}
}

// TestPGFolderSeedingAndTenderFoldersConverge is checkpoint 3 finding 3: parallel first
// opens of a new matter's Files tab and parallel Doc Tender filings into one new
// category folder all succeed and leave exactly one row per folder name.
func TestPGFolderSeedingAndTenderFoldersConverge(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.set("files.default_folders", `["Pleadings","Correspondence","Discovery"]`)
	sys := domain.Principal{Source: domain.SourceSystem, UserID: e.admin.UserID}
	var files []FileView
	for i := 0; i < 8; i++ {
		files = append(files, e.create(e.staff, NewFile{Name: fmt.Sprintf("scan-%d.pdf", i), MatterID: e.open}, fmt.Sprint("scan ", i)))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	folderOf := make(chan domain.ID, 16)
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := e.svc.Folders(ctx, e.staff, e.open)
			errs <- err
		}()
		go func(f FileView) {
			defer wg.Done()
			res, err := e.svc.ApplyTender(ctx, sys, f.ID, TenderUpdate{Folder: "Medical records"})
			if err == nil && res.File.FolderID != nil {
				folderOf <- *res.File.FolderID
			}
			errs <- err
		}(files[i])
	}
	wg.Wait()
	close(errs)
	close(folderOf)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ids := map[domain.ID]bool{}
	n := 0
	for id := range folderOf {
		ids[id] = true
		n++
	}
	if n != 8 || len(ids) != 1 {
		t.Fatalf("tender folders: %d filed into %d folders", n, len(ids))
	}
	rows, err := e.pool.QueryContext(ctx, `SELECT name, COUNT(*) FROM folders WHERE matter_id = $1 GROUP BY name`, e.open)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var name string
		var c int
		if err := rows.Scan(&name, &c); err != nil {
			t.Fatal(err)
		}
		got[name] = c
	}
	want := []string{"Pleadings", "Correspondence", "Discovery", "Medical records"}
	for _, w := range want {
		if got[w] != 1 {
			t.Fatalf("folders = %v", got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("folders = %v", got)
	}

	// A person creating a folder that exists (in any case) still gets a conflict.
	if _, err := e.svc.CreateFolder(ctx, e.staff, e.open, "medical RECORDS", 9); !isCode(err, domain.CodeConflict) {
		t.Fatalf("duplicate folder = %v", err)
	}
}
