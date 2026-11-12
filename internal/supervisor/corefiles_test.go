package supervisor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func fileRestoreOps(t *testing.T, restoreFiles func(context.Context, string) error) (*Operations, string, *[]string) {
	t.Helper()
	store := testStore(t)
	store.FileBytes = func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "export")
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
	ops, id, order := fileRestoreOps(t, func(_ context.Context, path string) error {
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != "export" {
			t.Errorf("file export %q %v", raw, err)
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

func TestRestoreFileFailureIsReported(t *testing.T) {
	ops, id, _ := fileRestoreOps(t, func(context.Context, string) error { return errors.New("sha mismatch") })
	rec, err := ops.Restore(context.Background(), id)
	if err == nil || rec.Status != "failed" || rec.Error != "files_not_restored" {
		t.Fatalf("restore %+v %v", rec, err)
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
