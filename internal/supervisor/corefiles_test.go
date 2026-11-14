package supervisor

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCore serves the core's internal file-bytes route the way internal/core does.
func fakeCore(t *testing.T, export string, trailer string, put func(body string) int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != CoreFilesPath || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Trailer", ExportStatusTrailer)
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = io.WriteString(w, export)
			w.Header().Set(ExportStatusTrailer, trailer)
		case http.MethodPut:
			raw, _ := io.ReadAll(r.Body)
			w.WriteHeader(put(string(raw)))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func coreAt(srv *httptest.Server) CoreFiles {
	return CoreFiles{Upstream: func() string { return srv.URL }, Token: "tok", Wait: 3 * time.Second}
}

func TestCoreFilesExportNeedsTheCompleteTrailer(t *testing.T) {
	var got strings.Builder
	if err := coreAt(fakeCore(t, "tar-bytes", "complete", nil)).Export(context.Background(), &got); err != nil || got.String() != "tar-bytes" {
		t.Fatalf("export %q %v", got.String(), err)
	}
	if err := coreAt(fakeCore(t, "half", "failed", nil)).Export(context.Background(), io.Discard); err == nil {
		t.Fatal("an export without the complete trailer was accepted")
	}
	wrong := coreAt(fakeCore(t, "x", "complete", nil))
	wrong.Token = "other"
	if err := wrong.Export(context.Background(), io.Discard); err == nil {
		t.Fatal("a refused token was accepted")
	}
	if err := (CoreFiles{Upstream: wrong.Upstream}).Export(context.Background(), io.Discard); err == nil {
		t.Fatal("no token was accepted")
	}
}

func TestCoreFilesExportRefusesARedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/setup" {
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = io.WriteString(w, "<html>setup</html>")
			return
		}
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	}))
	defer srv.Close()
	if err := coreAt(srv).Export(context.Background(), io.Discard); err == nil {
		t.Fatal("the first-run redirect was taken as file bytes")
	}
}

func TestCoreFilesImportRetriesAStartingCoreOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bytes.tar")
	if err := os.WriteFile(path, []byte("export"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := fakeCore(t, "", "", func(body string) int {
		if body != "export" {
			return http.StatusBadRequest
		}
		if calls.Add(1) == 1 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	})
	if err := coreAt(srv).Import(context.Background(), path); err != nil || calls.Load() != 2 {
		t.Fatalf("import %v after %d calls", err, calls.Load())
	}
	calls.Store(0)
	refused := fakeCore(t, "", "", func(string) int { calls.Add(1); return http.StatusUnprocessableEntity })
	if err := coreAt(refused).Import(context.Background(), path); err == nil || calls.Load() != 1 {
		t.Fatalf("a refused import: %v after %d calls", err, calls.Load())
	}
}

func TestUpstreamFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upstream")
	if got := UpstreamFrom(path)(); got != "core:8080" {
		t.Fatalf("missing file = %q", got)
	}
	if err := os.WriteFile(path, []byte("core-next:8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := UpstreamFrom(path)(); got != "core-next:8080" {
		t.Fatalf("upstream = %q", got)
	}
}

func TestBackupCarriesTheCoreFileExport(t *testing.T) {
	s := testStore(t)
	s.FileBytes = func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "versions-and-markdown")
		return err
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil || !meta.Files {
		t.Fatalf("create %+v %v", meta, err)
	}
	err = s.Restore(context.Background(), meta.ID, "", func(_ context.Context, p RestorePayload) error {
		if p.Manifest.FileBytes == nil || p.FileBytes == "" {
			t.Fatalf("payload has no file export: %+v", p.Manifest)
		}
		raw, err := os.ReadFile(p.FileBytes)
		if err != nil || string(raw) != "versions-and-markdown" {
			t.Fatalf("file export %q %v", raw, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The safety backup a restore takes runs with the core stopped.
	safety, err := s.Create(context.Background(), reasonSafety)
	if err != nil || safety.Files {
		t.Fatalf("safety %+v %v", safety, err)
	}
	s.FileBytes = func(context.Context, io.Writer) error { return errors.New("core down") }
	if _, err := s.Create(context.Background(), reasonNightly); err == nil {
		t.Fatal("a backup without the file bytes succeeded")
	}
	s.FileBytes = func(context.Context, io.Writer) error { return nil }
	if _, err := s.Create(context.Background(), reasonNightly); err == nil {
		t.Fatal("an empty file export was accepted")
	}
}

// fileExport builds a core file export (files.ExportBytes): the manifest, then each
// object. damage, when set, changes the tar after the manifest is written.
func fileExport(t *testing.T, objects map[string]string, damage func(id string, body string) (string, bool)) []byte {
	t.Helper()
	ids := make([]string, 0, len(objects))
	for id := range objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var list []exportObject
	for _, id := range ids {
		sum := sha256.Sum256([]byte(objects[id]))
		list = append(list, exportObject{ObjectID: id, Size: int64(len(objects[id])), SHA256: hex.EncodeToString(sum[:])})
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	put := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(body)), Mode: 0o600, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	put(fileManifestName, string(raw))
	for _, id := range ids {
		body, keep := objects[id], true
		if damage != nil {
			body, keep = damage(id, body)
		}
		if keep {
			put("objects/"+id, body)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var syntheticObjects = map[string]string{"obj-1": "version one bytes", "obj-2": "rendition markdown"}

func fileRestoreOps(t *testing.T, restoreFiles func(context.Context, string) error) (*Operations, string, *[]string) {
	t.Helper()
	return fileRestoreOpsWith(t, fileExport(t, syntheticObjects, nil), restoreFiles)
}

func fileRestoreOpsWith(t *testing.T, export []byte, restoreFiles func(context.Context, string) error) (*Operations, string, *[]string) {
	t.Helper()
	store := testStore(t)
	store.FileBytes = func(_ context.Context, w io.Writer) error {
		_, err := w.Write(export)
		return err
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	order := &[]string{}
	state := t.TempDir()
	ops := &Operations{
		Store:        store,
		Gate:         &Gate{},
		Now:          store.Now,
		Passphrase:   func() (string, error) { return store.Passphrase, nil },
		ApplyDump:    func(context.Context, io.Reader) error { *order = append(*order, "dump"); return nil },
		RestoreFiles: restoreFiles,
		Runtime:      &scriptRuntime{order: order, cores: []ContainerInfo{{ID: "core-1", Name: "core", Image: "img"}}},
		Ledger:       OpenLedger(filepath.Join(state, "ledger.jsonl")),
		Outbox:       OpenOutbox(filepath.Join(state, "outbox.jsonl")),
	}
	return ops, meta.ID, order
}

func TestRestoreHandsTheFilesToTheRestartedCore(t *testing.T) {
	var ops *Operations
	var sawStart bool
	want := fileExport(t, syntheticObjects, nil)
	ops, id, order := fileRestoreOps(t, func(_ context.Context, path string) error {
		raw, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, want) {
			t.Errorf("file export %d bytes %v", len(raw), err)
		}
		sawStart = strings.Contains(strings.Join(*ops.Runtime.(*scriptRuntime).order, ","), "start")
		return nil
	})
	rec, err := ops.Restore(context.Background(), id)
	if err != nil || rec.Status != "completed" {
		t.Fatalf("restore %+v %v (order %v)", rec, err, *order)
	}
	if !sawStart {
		t.Fatalf("files were restored before the core restarted: %v", *order)
	}
}

// Codex PR #41 finding 5: a damaged or partial file set fails before the database
// swap, leaving the existing database in place, and the core is never handed it.
func TestRestoreRefusesADamagedFileExportBeforeTheSwap(t *testing.T) {
	for name, export := range map[string][]byte{
		"object bytes differ from the manifest": fileExport(t, syntheticObjects, func(id, body string) (string, bool) {
			if id == "obj-2" {
				return "rendition markdowN", true
			}
			return body, true
		}),
		"object missing": fileExport(t, syntheticObjects, func(id, body string) (string, bool) { return body, id != "obj-2" }),
		"object size differs": fileExport(t, syntheticObjects, func(id, body string) (string, bool) {
			return body + "+", true
		}),
		"no manifest": []byte("not a tar export"),
	} {
		t.Run(name, func(t *testing.T) {
			handed := false
			ops, id, order := fileRestoreOpsWith(t, export, func(context.Context, string) error { handed = true; return nil })
			rec, err := ops.Restore(context.Background(), id)
			if err == nil || rec.Status != "failed" || rec.Error != "invalid_backup" {
				t.Fatalf("restore %+v %v", rec, err)
			}
			got := strings.Join(*order, ",")
			if strings.Contains(got, "dump") || handed {
				t.Fatalf("a damaged file set reached the database swap or the core: %s handed=%v", got, handed)
			}
			if !strings.Contains(got, "restart:core-1") {
				t.Fatalf("writers not restarted on the existing data: %s", got)
			}
			if notes, _ := ops.Outbox.List(); len(notes) == 0 || !strings.Contains(notes[len(notes)-1].Body, "Existing data was left in place") {
				t.Fatalf("notices %+v", notes)
			}
		})
	}
}

// Codex PR #41 finding 5: when the restarted core cannot write the checked bytes (a
// backend write failure), the safety backup taken before the swap is restored through
// the ordinary restore path, so the previous database is live again.
func TestRestoreFileWriteFailureRollsBackTheSwap(t *testing.T) {
	ops, id, order := fileRestoreOps(t, func(context.Context, string) error { return errors.New("backend write failed") })
	rec, err := ops.Restore(context.Background(), id)
	if err == nil || rec.Status != "failed" || rec.Error != "files_rolled_back" {
		t.Fatalf("restore %+v %v", rec, err)
	}
	got := strings.Join(*order, ",")
	if strings.Count(got, "dump") != 2 || strings.Count(got, "stop:core-1") != 2 {
		t.Fatalf("the rollback did not stop writers and swap the database back: %s", got)
	}
	if !strings.HasSuffix(got, "health") {
		t.Fatalf("writers not back after the rollback: %s", got)
	}
	var rollback RestoreRecord
	ops.jobsMu.Lock()
	for _, j := range ops.jobs {
		if j.Source == "rollback:"+rec.ID {
			rollback = j
		}
	}
	ops.jobsMu.Unlock()
	if rollback.Status != "completed" || !strings.HasPrefix(rollback.BackupID, "2026") || rollback.BackupID == id {
		t.Fatalf("rollback record %+v", rollback)
	}
	rc, meta, err := ops.Store.Open(rollback.BackupID)
	if err != nil || meta.Reason != reasonSafety {
		t.Fatalf("rollback restored %+v %v, not the safety backup", meta, err)
	}
	_ = rc.Close()
	if notes, _ := ops.Outbox.List(); len(notes) == 0 || notes[len(notes)-1].Subject != "Restore rolled back" {
		t.Fatalf("notices %+v", notes)
	}
}

// When the rollback itself fails, the record says files_not_restored and the admin is
// told the restored database is still live.
func TestRestoreFileWriteFailureWithAFailedRollbackIsReported(t *testing.T) {
	ops, id, order := fileRestoreOps(t, func(context.Context, string) error { return errors.New("backend write failed") })
	dumps := 0
	ops.ApplyDump = func(context.Context, io.Reader) error {
		dumps++
		*order = append(*order, "dump")
		if dumps > 1 {
			return errors.New("rollback swap failed")
		}
		return nil
	}
	rec, err := ops.Restore(context.Background(), id)
	if err == nil || rec.Status != "failed" || rec.Error != "files_not_restored" {
		t.Fatalf("restore %+v %v", rec, err)
	}
	if notes, _ := ops.Outbox.List(); len(notes) == 0 || !strings.Contains(notes[len(notes)-1].Body, "putting the previous database back failed") {
		t.Fatalf("notices %+v", notes)
	}
}

func TestFileArchiveRejectedWithoutACoreBeforeWritersStop(t *testing.T) {
	ops, id, order := fileRestoreOps(t, nil)
	if _, err := ops.Restore(context.Background(), id); !errors.Is(err, ErrFileRestoreUnsupported) {
		t.Fatalf("restore = %v", err)
	}
	if len(*order) != 0 {
		t.Fatalf("writers were touched: %v", *order)
	}
}

// Codex PR #41 finding on 2f13c23: a committed swap whose replaced-database drop fails
// ends through finishCleanup. It must still hand the spooled bytes to the core before
// the restore reports completed.
func TestRestoreCleanupPendingStillWritesTheFiles(t *testing.T) {
	handed := false
	ops, id, order := fileRestoreOps(t, func(_ context.Context, path string) error {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("spooled export missing: %v", err)
		}
		handed = true
		return nil
	})
	ops.ApplyRestore = func(context.Context, RestorePayload) error {
		*order = append(*order, "dump")
		return ErrRestoreCleanup
	}
	rec, err := ops.Restore(context.Background(), id)
	if rec.Status != "completed" || rec.Error != "cleanup_pending" || !errors.Is(err, ErrRestoreCleanup) {
		t.Fatalf("restore %+v %v (order %v)", rec, err, *order)
	}
	if !handed {
		t.Fatalf("the restore completed with cleanup pending and the core never got the files: %v", *order)
	}
}

// The same cleanup path with a backend write failure rolls back like the plain path.
func TestRestoreCleanupPendingFileWriteFailureRollsBack(t *testing.T) {
	ops, id, order := fileRestoreOps(t, func(context.Context, string) error { return errors.New("backend write failed") })
	dumps := 0
	ops.ApplyRestore = func(context.Context, RestorePayload) error {
		dumps++
		*order = append(*order, "dump")
		if dumps == 1 {
			return ErrRestoreCleanup
		}
		return nil
	}
	rec, err := ops.Restore(context.Background(), id)
	if err == nil || rec.Status != "failed" || rec.Error != "files_rolled_back" {
		t.Fatalf("restore %+v %v (order %v)", rec, err, *order)
	}
	if dumps != 2 {
		t.Fatalf("the safety backup was not restored: %v", *order)
	}
}

// A committed restore left pending (the core did not come back healthy) keeps its spool
// only in this process. After a supervisor restart, recovery finds the journal's file
// export still pending with no spool, so it rolls back to the safety backup instead of
// marking the restore completed without the bytes.
func TestRecoveryAfterRestartRollsBackALostFileExport(t *testing.T) {
	ops, id, order := fileRestoreOps(t, func(context.Context, string) error { return nil })
	rt := ops.Runtime.(*scriptRuntime)
	ops.Journal = OpenJournal(filepath.Join(t.TempDir(), "restore-journal.jsonl"))
	rt.health = errors.New("core not healthy")
	rec, err := ops.Restore(context.Background(), id)
	if err == nil || rec.Status != "running" {
		t.Fatalf("restore %+v %v (order %v)", rec, err, *order)
	}
	if item, ok := ops.journalItem(rec.ID); !ok || item.Files != filesPending || item.FilesSafety == "" {
		t.Fatalf("journal does not say the file export is pending: %+v", item)
	}
	rt.health = nil
	handed := 0
	next := &Operations{
		Store: ops.Store, Gate: &Gate{}, Now: ops.Now, Passphrase: ops.Passphrase,
		ApplyDump:    ops.ApplyDump,
		RestoreFiles: func(context.Context, string) error { handed++; return nil },
		Runtime:      rt, Journal: ops.Journal, Ledger: ops.Ledger, Outbox: ops.Outbox,
	}
	if err := next.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	got, ok, err := next.LookupRestore(rec.ID)
	if err != nil || !ok || got.Status != "failed" || got.Error != "files_rolled_back" {
		t.Fatalf("recovered %+v %v %v (order %v)", got, ok, err, *order)
	}
	// A safety backup holds the database only, so the rollback hands the core nothing.
	if strings.Count(strings.Join(*order, ","), "dump") != 2 || handed != 0 {
		t.Fatalf("the safety backup was not restored: %v handed=%d", *order, handed)
	}
	if notes, _ := next.Outbox.List(); len(notes) == 0 || notes[len(notes)-1].Subject != "Restore rolled back" {
		t.Fatalf("notices %+v", notes)
	}
}

// When the bytes were written, recovery of the same committed record completes it.
func TestRecoveryCompletesACommittedRestoreWhoseFilesWereWritten(t *testing.T) {
	handed := 0
	ops, id, order := fileRestoreOps(t, func(context.Context, string) error { handed++; return nil })
	ops.Journal = OpenJournal(filepath.Join(t.TempDir(), "restore-journal.jsonl"))
	rec, err := ops.Restore(context.Background(), id)
	if err != nil || rec.Status != "completed" || handed != 1 {
		t.Fatalf("restore %+v %v handed=%d (order %v)", rec, err, handed, *order)
	}
	if item, _ := ops.journalItem(rec.ID); item.Files != "" {
		t.Fatalf("written file export still pending in the journal: %+v", item)
	}
}
