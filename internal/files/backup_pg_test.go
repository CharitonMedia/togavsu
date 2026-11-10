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

// TestPGBackupRestoresFilesFromEveryBackend is AC-040's restore clause and AC-050's
// backup clause on the files side: the byte backup holds every version and Markdown
// copy from local disk, S3 and Dropbox, and after every backend is lost the restore
// brings each object back with a matching SHA-256.
// The supervisor's encrypted archive and database restore are separate (pending).
func TestPGBackupRestoresFilesFromEveryBackend(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fs3, s3 := e.s3()
	fdb, dbx := e.dropbox()
	var files []stored
	for _, b := range []BackendView{e.local, s3, dbx} {
		e.makeDefault(b)
		files = append(files, seedFiles(t, e, 2)...)
	}
	if fs3.Len() != 6 || fdb.Len() != 6 || localObjectFiles(t, e.root+"/local") != 6 {
		t.Fatalf("objects local=%d s3=%d dropbox=%d", localObjectFiles(t, e.root+"/local"), fs3.Len(), fdb.Len())
	}
	if _, err := e.svc.ExportBytes(ctx, e.staff, io.Discard, nil); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("staff backup = %v", err)
	}
	var archive bytes.Buffer
	rep, err := e.svc.ExportBytes(ctx, e.admin, &archive, nil)
	if err != nil || rep.Objects != 18 || len(rep.Excluded) != 0 {
		t.Fatalf("export = %+v %v", rep, err)
	}
	// The manifest lists every Markdown copy.
	tr := tar.NewReader(bytes.NewReader(archive.Bytes()))
	if _, err := tr.Next(); err != nil {
		t.Fatal(err)
	}
	var manifest []BackupObject
	if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	mds := 0
	for _, f := range files {
		for _, o := range manifest {
			if o.ObjectID == f.md && o.Kind == "rendition" && o.SHA256 == shaHex([]byte(f.mdBody)) {
				mds++
			}
		}
	}
	if mds != len(files) {
		t.Fatalf("markdown copies in the backup = %d of %d", mds, len(files))
	}
	// A second backup skips what the first already holds: versions never change.
	skip := map[domain.ID]bool{}
	for _, o := range manifest {
		skip[o.ObjectID] = true
	}
	if rep2, err := e.svc.ExportBytes(ctx, e.admin, io.Discard, skip); err != nil || rep2.Objects != 0 || rep2.Skipped != 18 {
		t.Fatalf("incremental export = %+v %v", rep2, err)
	}

	// Every backend loses its bytes.
	fs3.Wipe()
	fdb.Wipe()
	if err := os.RemoveAll(e.root + "/local/objects"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.OpenVersion(ctx, e.attorney, files[0].file.ID, files[0].version, false); err == nil {
		t.Fatal("read succeeded with the bytes gone")
	}
	restored, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(archive.Bytes()))
	if err != nil || restored.Restored != 18 || restored.Objects != 18 {
		t.Fatalf("restore = %+v %v", restored, err)
	}
	if fs3.Len() != 6 || fdb.Len() != 6 || localObjectFiles(t, e.root+"/local") != 6 {
		t.Fatalf("restored local=%d s3=%d dropbox=%d", localObjectFiles(t, e.root+"/local"), fs3.Len(), fdb.Len())
	}
	readAllFiles(t, e, files)
	// Restoring again leaves good copies alone.
	again, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(archive.Bytes()))
	if err != nil || again.Verified != 18 || again.Restored != 0 {
		t.Fatalf("second restore = %+v %v", again, err)
	}
	// A tampered archive entry is refused.
	bad := bytes.Replace(archive.Bytes(), []byte(files[0].mdBody), []byte(strings.ToUpper(files[0].mdBody)), 1)
	fs3.Wipe()
	fdb.Wipe()
	_ = os.RemoveAll(e.root + "/local/objects")
	if _, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(bad)); err == nil {
		t.Fatal("a tampered backup restored")
	}
}

func TestPGBackupExcludesABackendTurnedOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, s3 := e.s3()
	e.makeDefault(s3)
	e.create(e.staff, NewFile{Name: "a.txt", MatterID: e.open}, "a")
	no := false
	if _, err := e.svc.UpdateBackend(ctx, e.admin, s3.ID, BackendWrite{BackedUp: &no}); err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.ExportBytes(ctx, e.admin, io.Discard, nil)
	if err != nil || rep.Objects != 0 || len(rep.Excluded) != 1 || rep.Excluded[0] != s3.ID {
		t.Fatalf("export = %+v %v", rep, err)
	}
}
