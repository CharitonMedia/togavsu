package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

type stored struct {
	file    FileView
	version domain.ID
	body    string
	md      domain.ID
	mdBody  string
}

// seedFiles makes files with two versions and a Markdown copy each, on the default backend.
func seedFiles(t *testing.T, e *env, n int) []stored {
	t.Helper()
	ctx := context.Background()
	var out []stored
	for i := 0; i < n; i++ {
		matter := e.open
		if i%2 == 1 {
			matter = e.walled
		}
		f := e.create(e.attorney, NewFile{Name: fmt.Sprintf("doc-%d.pdf", i), MatterID: matter}, fmt.Sprintf("version one of %d", i))
		body := fmt.Sprintf("version two of %d %s", i, strings.Repeat("x", i*70000))
		v, err := e.svc.SaveVersion(ctx, e.attorney, f.ID, strings.NewReader(body), int64(len(body)), SaveOptions{How: "editor"})
		if err != nil {
			t.Fatal(err)
		}
		md := fmt.Sprintf("# doc %d\nmarkdown copy %d\n", i, i)
		r, err := e.svc.AddRendition(ctx, e.attorney, f.ID, v.ID, "markdown", strings.NewReader(md), int64(len(md)))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, stored{file: f, version: v.ID, body: body, md: r.ID, mdBody: md})
	}
	return out
}

// readAllFiles reads every version and Markdown copy and checks the bytes.
func readAllFiles(t *testing.T, e *env, files []stored) {
	t.Helper()
	ctx := context.Background()
	for _, f := range files {
		if got := readVersion(t, e, e.attorney, f.file.ID, f.version); got != f.body {
			t.Fatalf("%s current version differs", f.file.Name)
		}
		if got := readVersion(t, e, e.attorney, f.file.ID, *f.file.CurrentVersionID); !strings.HasPrefix(got, "version one") {
			t.Fatalf("%s first version differs", f.file.Name)
		}
		_, md, err := e.svc.MarkdownCopy(ctx, e.attorney, f.file.ID)
		if err != nil || string(md) != f.mdBody {
			t.Fatalf("%s markdown = %q %v", f.file.Name, md, err)
		}
	}
}

func objectsOnBackend(t *testing.T, e *env, backend domain.ID) int {
	t.Helper()
	placed, err := e.svc.objectsOn(context.Background(), backend, "")
	if err != nil {
		t.Fatal(err)
	}
	return len(placed)
}

func localObjectFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(filepath.Join(root, "objects"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.HasPrefix(info.Name(), ".") {
			n++
		}
		return nil
	})
	return n
}

// move runs one full move with a stop in the middle, reading every file between
// objects, and returns the move after the run checks out.
func move(t *testing.T, e *env, files []stored, from, to domain.ID) MoveView {
	t.Helper()
	ctx := context.Background()
	m, err := e.svc.StartMove(ctx, e.admin, MoveRequest{FromBackendID: from, ToBackendID: to, Scope: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if m.ProgressTotal != len(files)*3 {
		t.Fatalf("move total = %d, want %d", m.ProgressTotal, len(files)*3)
	}
	if _, err := e.svc.ConfirmMove(ctx, e.admin, m.ID); !isCode(err, domain.CodeConflict) {
		t.Fatalf("confirm before the move checks out = %v", err)
	}
	runCtx, stop := context.WithCancel(ctx)
	e.svc.afterMoveItem = func(done int) {
		readAllFiles(t, e, files) // every file stays readable during the move
		if done == m.ProgressTotal/2 {
			stop()
		}
	}
	if err := e.svc.RunMove(runCtx, m.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped run = %v", err)
	}
	half, err := e.svc.GetMove(ctx, e.admin, m.ID)
	if err != nil || half.Status != "stopped" || half.ProgressDone != m.ProgressTotal/2 {
		t.Fatalf("stopped move = %+v %v", half, err)
	}
	e.svc.afterMoveItem = func(int) { readAllFiles(t, e, files) }
	if err := e.svc.RunMove(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	e.svc.afterMoveItem = nil
	done, err := e.svc.GetMove(ctx, e.admin, m.ID)
	if err != nil || done.Status != "awaiting_confirmation" || done.ProgressDone != done.ProgressTotal {
		t.Fatalf("checked move = %+v %v", done, err)
	}
	return done
}

// TestPGMoveLocalToS3ToDropbox is AC-040's move clause: every file stays readable
// during the move, every SHA-256 is checked on the new backend, and old copies are
// deleted only after the admin confirms.
func TestPGMoveLocalToS3ToDropbox(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	files := seedFiles(t, e, 4)
	fs3, s3 := e.s3()
	fdb, dbx := e.dropbox()
	localRoot := e.root + "/local"
	if n := localObjectFiles(t, localRoot); n != 12 {
		t.Fatalf("local objects = %d", n)
	}

	m := move(t, e, files, e.local.ID, s3.ID)
	// Checked, not confirmed: the old copies are all still on local disk.
	if n := localObjectFiles(t, localRoot); n != 12 {
		t.Fatalf("old copies deleted before confirmation: %d left", n)
	}
	if fs3.Len() != 12 || objectsOnBackend(t, e, s3.ID) != 12 || objectsOnBackend(t, e, e.local.ID) != 0 {
		t.Fatalf("s3 has %d objects", fs3.Len())
	}
	if _, err := e.svc.ConfirmMove(ctx, e.attorney, m.ID); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("non-admin confirm = %v", err)
	}
	if _, err := e.svc.ConfirmMove(ctx, e.admin, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := localObjectFiles(t, localRoot); n != 0 {
		t.Fatalf("local copies after confirm = %d", n)
	}
	readAllFiles(t, e, files)
	// Bytes on S3 are sealed: no plaintext reaches the backend.
	for _, k := range fs3.Keys() {
		if raw := fs3.Get(k); strings.Contains(string(raw), "version") || strings.Contains(string(raw), "markdown copy") {
			t.Fatalf("plaintext on s3 under %s", k)
		}
	}
	vs := versionRows(t, e)

	m2 := move(t, e, files, s3.ID, dbx.ID)
	if fs3.Len() != 12 {
		t.Fatalf("s3 copies deleted before confirmation: %d", fs3.Len())
	}
	if _, err := e.svc.ConfirmMove(ctx, e.admin, m2.ID); err != nil {
		t.Fatal(err)
	}
	if fs3.Len() != 0 || fdb.Len() != 12 {
		t.Fatalf("after confirm s3=%d dropbox=%d", fs3.Len(), fdb.Len())
	}
	readAllFiles(t, e, files)
	after := versionRows(t, e)
	for id, row := range vs {
		if after[id] != row {
			t.Fatalf("a move changed version row %s", id)
		}
	}
	list, err := e.svc.Moves(ctx, e.admin)
	if err != nil || len(list) != 2 || list[0].Status != "completed" || list[1].ConfirmedBy == nil {
		t.Fatalf("moves = %+v %v", list, err)
	}
	// The move checked each SHA-256 on the new backend: corrupting a Dropbox copy now
	// fails the read.
	if fdb.Flip(string(files[0].version)) != 1 {
		t.Fatal("flip")
	}
	d, err := e.svc.OpenVersion(ctx, e.attorney, files[0].file.ID, files[0].version, false)
	if err == nil {
		_, err = io.Copy(io.Discard, d.Body)
		d.Close()
	}
	if !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("corrupted dropbox copy = %v", err)
	}
}

func TestPGMoveFailsClosedOnACorruptSource(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	files := seedFiles(t, e, 2)
	fs3, s3 := e.s3()
	// Change one byte of the first file's stored bytes on local disk.
	corruptLocal(t, e, files[0].version)
	m, err := e.svc.StartMove(ctx, e.admin, MoveRequest{FromBackendID: e.local.ID, ToBackendID: s3.ID, Scope: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RunMove(ctx, m.ID); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("move over a corrupt object = %v", err)
	}
	got, _ := e.svc.GetMove(ctx, e.admin, m.ID)
	if got.Status != "failed" {
		t.Fatalf("move status = %s", got.Status)
	}
	if _, err := e.svc.ConfirmMove(ctx, e.admin, m.ID); !isCode(err, domain.CodeConflict) {
		t.Fatalf("confirm a failed move = %v", err)
	}
	if fs3.Get(string(files[0].version)) != nil {
		t.Fatal("a corrupt object was copied to the new backend")
	}
	// The corrupt object's record still points at local disk; objects that checked
	// out before it read from S3, and nothing was deleted from local disk.
	loc, err := e.svc.st.location(ctx, files[0].version, location{})
	if err != nil || loc.Backend != e.local.ID {
		t.Fatalf("corrupt object location = %+v %v", loc, err)
	}
	if n := localObjectFiles(t, e.root+"/local"); n != 6 {
		t.Fatalf("local objects after a failed move = %d", n)
	}
	if got := readVersion(t, e, e.attorney, files[1].file.ID, files[1].version); got != files[1].body {
		t.Fatal("an object moved before the failure is not readable")
	}
	if e.mail.count() == 0 {
		t.Fatal("no admin email for the corrupt object")
	}
}

func corruptLocal(t *testing.T, e *env, object domain.ID) {
	t.Helper()
	var path string
	_ = filepath.Walk(e.root+"/local/objects", func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == string(object) {
			path = p
		}
		return nil
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-3] ^= 0x20
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPGChangedByteFailsTheReadAndEmailsAdmins is AC-040's integrity clause on every
// backend kind, plain and sealed, in the service and over HTTP.
func TestPGChangedByteFailsTheReadAndEmailsAdmins(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fs3, s3 := e.s3()
	fdb, dbx := e.dropbox()
	no := false
	if _, err := e.svc.UpdateBackend(ctx, e.admin, e.local.ID, BackendWrite{Encryption: &no}); err != nil {
		t.Fatal(err)
	}
	plain := e.create(e.staff, NewFile{Name: "plain.txt", MatterID: e.open}, "plain local bytes that a person can read")
	e.makeDefault(s3)
	onS3 := e.create(e.staff, NewFile{Name: "s3.txt", MatterID: e.open}, "bytes on s3")
	e.makeDefault(dbx)
	onDbx := e.create(e.staff, NewFile{Name: "dbx.txt", MatterID: e.open}, "bytes on dropbox")

	corruptLocal(t, e, *plain.CurrentVersionID)
	fs3.Flip(string(*onS3.CurrentVersionID))
	fdb.Flip(string(*onDbx.CurrentVersionID))
	srv := e.server(func(*http.Request) (domain.Principal, error) { return e.staff, nil })
	for i, f := range []FileView{plain, onS3, onDbx} {
		d, err := e.svc.OpenVersion(ctx, e.staff, f.ID, *f.CurrentVersionID, false)
		if err == nil {
			_, err = io.Copy(io.Discard, d.Body)
			d.Close()
		}
		if !errors.Is(err, storage.ErrIntegrity) {
			t.Fatalf("%s read = %v", f.Name, err)
		}
		if e.mail.count() < i+1 {
			t.Fatalf("%s: admins were not emailed", f.Name)
		}
		resp, err := http.Get(srv.URL + "/api/v1/files/" + string(f.ID) + "/versions/" + string(*f.CurrentVersionID) + "/download")
		if err == nil {
			_, err = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		if err == nil {
			t.Fatalf("%s: the HTTP download of a changed object completed", f.Name)
		}
	}
	alerts, err := e.svc.Alerts(ctx, e.admin)
	if err != nil || len(alerts) < 3 || !alerts[0].Emailed {
		t.Fatalf("alerts = %+v %v", alerts, err)
	}
	if _, err := e.svc.Alerts(ctx, e.staff); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("staff alerts = %v", err)
	}
	// The file is never hidden by a failed check.
	if _, err := e.svc.Get(ctx, e.staff, plain.ID); err != nil {
		t.Fatalf("a corrupt file must stay listed: %v", err)
	}
}
