package files

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// archiveBodies reads an ExportBytes archive back into its manifest and the body of
// each object entry, in archive order.
func archiveBodies(t *testing.T, archive []byte) ([]BackupObject, [][]byte) {
	t.Helper()
	list := readManifest(t, archive)
	tr := tar.NewReader(bytes.NewReader(archive))
	if _, err := tr.Next(); err != nil {
		t.Fatal(err)
	}
	var bodies [][]byte
	for {
		_, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	return list, bodies
}

// TestPGRestoreRejectsMissingOrRepeatedObjects is checkpoint 4 finding 2: an archive
// that ends cleanly before every manifest object appeared, or repeats one, fails the
// restore rather than reporting success.
func TestPGRestoreRejectsMissingOrRepeatedObjects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.create(e.staff, NewFile{Name: "a.txt", MatterID: e.open}, "first file")
	e.create(e.staff, NewFile{Name: "b.txt", MatterID: e.open}, "second file")
	var archive bytes.Buffer
	if _, err := e.svc.ExportBytes(ctx, e.admin, &archive, nil); err != nil {
		t.Fatal(err)
	}
	list, bodies := archiveBodies(t, archive.Bytes())
	if len(list) < 2 || len(bodies) != len(list) {
		t.Fatalf("archive has %d manifest objects and %d entries", len(list), len(bodies))
	}
	// Manifest of two, only the first object present.
	missing := buildArchiveWith(t, list, list[:1], bodies[:1])
	if _, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(missing)); err == nil || !strings.Contains(err.Error(), "ends before") {
		t.Fatalf("archive missing an object restored = %v", err)
	}
	// Manifest of two, the first object twice and the second never.
	repeated := buildArchiveWith(t, list, []BackupObject{list[0], list[0]}, [][]byte{bodies[0], bodies[0]})
	if _, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(repeated)); err == nil || !strings.Contains(err.Error(), "appears twice") {
		t.Fatalf("archive repeating an object restored = %v", err)
	}
	// A manifest naming one object twice is refused.
	doubled := buildArchiveWith(t, []BackupObject{list[0], list[0]}, list[:1], bodies[:1])
	if _, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(doubled)); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("manifest repeating an object restored = %v", err)
	}
	// The complete archive still restores.
	if rep, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(archive.Bytes())); err != nil || rep.Objects != len(list) {
		t.Fatalf("complete restore = %+v %v", rep, err)
	}
}

// buildArchiveWith writes a manifest and a separate list of object entries, so a test
// can make the two disagree.
func buildArchiveWith(t *testing.T, manifest, entries []BackupObject, bodies [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEntry(tw, manifestName, int64(len(raw)), bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for i, o := range entries {
		if err := writeEntry(tw, "objects/"+string(o.ObjectID), int64(len(bodies[i])), bytes.NewReader(bodies[i])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestPGStreamsMustMatchTheirDeclaredSize is checkpoint 4 findings 4 and 5: a file
// stream longer than its declared size is refused rather than saved truncated, and a
// rendition, Markdown or not, must be exactly the size it declares.
func TestPGStreamsMustMatchTheirDeclaredSize(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Create(ctx, e.staff, NewFile{Name: "long.txt", MatterID: e.open}, strings.NewReader("hello world"), 5); !isCode(err, domain.CodeConflict) {
		t.Fatalf("longer stream than declared = %v", err)
	}
	if _, err := e.svc.Create(ctx, e.staff, NewFile{Name: "short.txt", MatterID: e.open}, strings.NewReader("hi"), 5); err == nil {
		t.Fatalf("shorter stream than declared = %v", err)
	}
	if got, _, err := e.svc.List(ctx, e.staff, ListQuery{MatterID: e.open}); err != nil || len(got) != 0 {
		t.Fatalf("refused creates left files: %v %v", got, err)
	}
	f := e.create(e.staff, NewFile{Name: "ok.txt", MatterID: e.open}, "body")
	v := *f.CurrentVersionID
	for _, c := range []struct {
		kind, body string
		size       int64
		code       bool // the refusal is a validation error, not a backend short write
	}{
		{"markdown", "# longer than declared", 3, true},
		{"markdown", "ab", 3, true},
		{"pdf_preview", "longer preview bytes", 4, true},
		{"pdf_preview", "ab", 4, false},
	} {
		_, err := e.svc.AddRendition(ctx, e.attorney, f.ID, v, c.kind, strings.NewReader(c.body), c.size)
		if err == nil || (c.code && !isCode(err, domain.CodeValidation)) {
			t.Fatalf("%s of %d bytes declared %d = %v", c.kind, len(c.body), c.size, err)
		}
	}
	// Search never indexed the oversized Markdown text.
	if got, err := e.svc.Search(ctx, e.attorney, "declared", 10); err != nil || len(got) != 0 {
		t.Fatalf("search found refused Markdown: %v %v", got, err)
	}
	md := "# exact"
	if _, err := e.svc.AddRendition(ctx, e.attorney, f.ID, v, "markdown", strings.NewReader(md), int64(len(md))); err != nil {
		t.Fatalf("exact Markdown = %v", err)
	}
}

// purgeFile trashes and empties a file and returns how many objects it had.
func purgeFile(t *testing.T, e *env, f FileView) int {
	t.Helper()
	ctx := context.Background()
	objs, err := e.svc.objectsOf(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Trash(ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Purge(ctx, e.admin, f.ID, f.Name); err != nil {
		t.Fatal(err)
	}
	return len(objs)
}

// TestPGRestoreSkipsPurgedFiles is checkpoint 5 finding 2: restoring an archive taken
// before a file was emptied from the trash does not write that file's bytes back.
func TestPGRestoreSkipsPurgedFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	gone := e.create(e.staff, NewFile{Name: "gone.txt", MatterID: e.open}, "purged later")
	kept := e.create(e.staff, NewFile{Name: "kept.txt", MatterID: e.open}, "still here")
	keptObjs, err := e.svc.objectsOf(ctx, kept.ID)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if _, err := e.svc.ExportBytes(ctx, e.admin, &archive, nil); err != nil {
		t.Fatal(err)
	}
	purged := purgeFile(t, e, gone)
	if err := os.RemoveAll(e.root + "/local/objects"); err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(archive.Bytes()))
	if err != nil || rep.Purged != purged || rep.Restored != len(keptObjs) {
		t.Fatalf("restore = %+v %v, want %d purged and %d restored", rep, err, purged, len(keptObjs))
	}
	if n := localObjectFiles(t, e.root+"/local"); n != len(keptObjs) {
		t.Fatalf("restore wrote %d objects, want only the %d of the kept file", n, len(keptObjs))
	}
	if got := readVersion(t, e, e.staff, kept.ID, *kept.CurrentVersionID); got != "still here" {
		t.Fatalf("kept file = %q", got)
	}
}

// TestPGMoveSkipsPurgedFiles is checkpoint 5 finding 3: a file emptied from the trash
// before a move, or while one of its objects is being copied, is not left on the new
// backend and does not fail the move.
func TestPGMoveSkipsPurgedFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fs3, s3 := e.s3()
	before := e.create(e.staff, NewFile{Name: "before.txt", MatterID: e.open}, "purged before the run")
	during := e.create(e.staff, NewFile{Name: "during.txt", MatterID: e.open}, "purged during the copy")
	live := e.create(e.staff, NewFile{Name: "live.txt", MatterID: e.open}, "moves normally")
	liveObjs, err := e.svc.objectsOf(ctx, live.ID)
	if err != nil {
		t.Fatal(err)
	}
	duringObjs, err := e.svc.objectsOf(ctx, during.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Trash(ctx, e.staff, during.ID); err != nil {
		t.Fatal(err)
	}
	m, err := e.svc.StartMove(ctx, e.admin, MoveRequest{FromBackendID: e.local.ID, ToBackendID: s3.ID, Scope: "all"})
	if err != nil {
		t.Fatal(err)
	}
	purgeFile(t, e, before)
	of := map[domain.ID]bool{}
	for _, o := range duringObjs {
		of[o.ID] = true
	}
	once := false
	e.svc.beforeMoveCommit = func(obj domain.ID) {
		if of[obj] && !once {
			once = true
			if err := e.svc.Purge(ctx, e.admin, during.ID, during.Name); err != nil {
				t.Errorf("purge during the move: %v", err)
			}
		}
	}
	if err := e.svc.RunMove(ctx, m.ID); err != nil {
		t.Fatalf("move with purged files = %v", err)
	}
	e.svc.beforeMoveCommit = nil
	if !once {
		t.Fatal("the purge during the copy never ran")
	}
	done, err := e.svc.GetMove(ctx, e.admin, m.ID)
	if err != nil || done.Status != "awaiting_confirmation" || done.ProgressDone != done.ProgressTotal {
		t.Fatalf("move = %+v %v", done, err)
	}
	if fs3.Len() != len(liveObjs) || objectsOnBackend(t, e, s3.ID) != len(liveObjs) {
		t.Fatalf("s3 holds %d objects, want only the %d of the live file", fs3.Len(), len(liveObjs))
	}
	if _, err := e.svc.ConfirmMove(ctx, e.admin, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := localObjectFiles(t, e.root+"/local"); n != 0 {
		t.Fatalf("local objects after confirm = %d", n)
	}
	if got := readVersion(t, e, e.staff, live.ID, *live.CurrentVersionID); got != "moves normally" {
		t.Fatalf("live file after move = %q", got)
	}
}

// TestPGRestoreKeepsACopyItCannotRead is checkpoint 12: when a backend fails a read for
// a reason other than a bad or missing copy, the restore stops and the copy stays,
// even when the archive's own entry is bad.
func TestPGRestoreKeepsACopyItCannotRead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fs3, s3 := e.s3()
	e.makeDefault(s3)
	f := e.create(e.staff, NewFile{Name: "kept.txt", MatterID: e.open}, "bytes that must survive")
	objs, err := e.svc.objectsOf(ctx, f.ID)
	if err != nil || len(objs) == 0 {
		t.Fatalf("objects = %v %v", objs, err)
	}
	var archive bytes.Buffer
	if _, err := e.svc.ExportBytes(ctx, e.admin, &archive, nil); err != nil {
		t.Fatal(err)
	}
	list, bodies := archiveBodies(t, archive.Bytes())
	for i := range bodies {
		bodies[i] = append([]byte("tampered "), bodies[i]...)
	}
	bad := buildArchiveWith(t, list, list, bodies)
	before := map[string][]byte{}
	for _, o := range objs {
		before[string(o.ID)] = fs3.Get(string(o.ID))
		if before[string(o.ID)] == nil {
			t.Fatalf("object %s is not on s3", o.ID)
		}
	}
	fs3.Unavailable.Store(true)
	for name, a := range map[string][]byte{"good archive": archive.Bytes(), "bad archive": bad} {
		if rep, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(a)); err == nil {
			t.Fatalf("restore of a %s over an unreadable backend = %+v, want an error", name, rep)
		}
	}
	fs3.Unavailable.Store(false)
	for key, want := range before {
		if got := fs3.Get(key); !bytes.Equal(got, want) {
			t.Fatalf("object %s changed or went missing during the failed restores", key)
		}
	}
	if got := readVersion(t, e, e.staff, f.ID, *f.CurrentVersionID); got != "bytes that must survive" {
		t.Fatalf("file reads %q", got)
	}
}
