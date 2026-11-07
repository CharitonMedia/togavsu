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
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{
		Dir:        dir,
		Passphrase: "synthetic-passphrase",
		Now:        func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) },
		Dump: func(ctx context.Context, w io.Writer) error {
			_, err := io.WriteString(w, "COPY core.synthetic_items (id) FROM stdin;\n1\n2\n3\n\\.\n")
			return err
		},
	}
}

func TestBackupRoundTripAndChecksum(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SHA256 == "" || meta.Bytes == 0 {
		t.Fatalf("meta = %+v", meta)
	}
	var restored bytes.Buffer
	err = s.Restore(context.Background(), meta.ID, "", func(_ context.Context, payload RestorePayload) error {
		if payload.Manifest.RecordCounts["core.synthetic_items"] != 3 {
			t.Fatalf("counts = %v", payload.Manifest.RecordCounts)
		}
		_, err := io.Copy(&restored, payload.Dump)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restored.String(), "synthetic_items") {
		t.Fatalf("dump = %s", restored.String())
	}
	path := filepath.Join(s.Dir, meta.ID+".tgbk")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Open(meta.ID)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("tamper open = %v", err)
	}
}

func TestBackupIDRejectsTraversalAndKeepsCanonicalIDs(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil || !validID(meta.ID) {
		t.Fatalf("%+v %v", meta, err)
	}
	outside := filepath.Join(filepath.Dir(s.Dir), "escaped-backup.json")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })
	hostile := []string{
		"../other/bakfile-0123456789abcdef",
		"..%2fother%2fbakfile-0123456789abcdef",
		"20260102T023000Z-0123456789abcd",
		"20260102T023000Z-0123456789ABCDEF",
		"20261399T023000Z-0123456789abcdef",
		"20260102t023000Z-0123456789abcdef",
		meta.ID + "/../../" + filepath.Base(outside),
	}
	for _, id := range hostile {
		if validID(id) {
			t.Fatalf("accepted %q", id)
		}
		if _, err := s.lookup(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("lookup %q: %v", id, err)
		}
		if err := s.Delete(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("delete %q: %v", id, err)
		}
		if err := s.Restore(context.Background(), id, "", func(context.Context, RestorePayload) error { return nil }); !errors.Is(err, ErrNotFound) {
			t.Fatalf("restore %q: %v", id, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("hostile id touched a file outside the store: %v", err)
	}
	poison := filepath.Join(s.Dir, "20260102T030405Z-abcdef0123456789.json")
	if err := os.WriteFile(poison, []byte(`{"id":"../other/bakfile-0123456789abcdef","created_at":"2026-01-02T03:04:05Z","reason":"manual","sha256":"abc","bytes":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.lookup("20260102T030405Z-abcdef0123456789"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("untrusted metadata id was accepted: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("metadata id escaped the store: %v", err)
	}
	rc, _, err := s.Open(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
}

func TestBackupRequiresPassphrase(t *testing.T) {
	s := testStore(t)
	s.Passphrase = ""
	_, err := s.Create(context.Background(), reasonManual)
	if !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestBackupWrongPassphrase(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonNightly)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Restore(context.Background(), meta.ID, "nope", func(context.Context, RestorePayload) error {
		t.Fatal("apply should not run")
		return nil
	})
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("got %v", err)
	}
}

func TestBackupFileDedupAndBadHash(t *testing.T) {
	s := testStore(t)
	body := []byte("version-bytes")
	sum, err := sha256Hex(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	s.Files = func(context.Context) ([]FileBlob, error) {
		open := func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		return []FileBlob{
			{Name: "a.pdf", SHA256: sum, Open: open},
			{Name: "a-again.pdf", SHA256: sum, Open: open},
		}, nil
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Restore(context.Background(), meta.ID, "", func(_ context.Context, payload RestorePayload) error {
		if len(payload.Manifest.Files) != 2 || payload.Manifest.Files[0].SHA256 != payload.Manifest.Files[1].SHA256 {
			t.Fatalf("files = %+v", payload.Manifest.Files)
		}
		if len(payload.Files) != 2 {
			t.Fatalf("recovered %d files", len(payload.Files))
		}
		for _, file := range payload.Files {
			got, err := os.ReadFile(file.Path)
			if err != nil {
				return err
			}
			if !bytes.Equal(got, body) {
				t.Fatalf("recovered %s = %q", file.Name, got)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	s.Files = func(context.Context) ([]FileBlob, error) {
		return []FileBlob{{
			Name:   "bad.pdf",
			SHA256: sum,
			Open:   func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader([]byte("other"))), nil },
		}}, nil
	}
	_, err = s.Create(context.Background(), reasonManual)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("bad hash = %v", err)
	}
}

func TestBackupPruneAndDelete(t *testing.T) {
	s := testStore(t)
	old, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2026, 4, 4, 5, 6, 7, 0, time.UTC) }
	recent, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.Prune(context.Background(), 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned %d", n)
	}
	if _, err := s.lookup(old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old lookup %v", err)
	}
	if _, err := s.lookup(recent.ID); err != nil {
		t.Fatal(err)
	}
}

func TestStalledDestinationReleasesTheGateAndKeepsTheLocalBackup(t *testing.T) {
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, buf)
		// Stay open until the client timeout closes the connection.
		one := make([]byte, 1)
		_, _ = conn.Read(one)
	}))
	defer stall.Close()
	s := testStore(t)
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{
		ID: "stall", Kind: "s3", Endpoint: stall.URL, Region: "us-east-1",
		Bucket: "bucket", AccessKey: "key", SecretKey: "secret", RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		return &S3Destination{Spec: spec, HTTP: &http.Client{Timeout: 200 * time.Millisecond}}, nil
	}
	ops := &Operations{Store: s, Gate: &Gate{}, Passphrase: func() (string, error) { return s.Passphrase, nil }}
	meta, err := ops.CreateBackup(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.DestinationErrors) == 0 {
		t.Fatal("stalled copy was reported as success")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, meta.ID+".tgbk")); err != nil {
		t.Fatalf("local backup was not kept: %v", err)
	}
	release, ok := ops.Gate.Try()
	if !ok {
		t.Fatal("operation gate stayed held after the stalled copy")
	}
	release()
	if s3HTTPClient.Timeout <= 0 {
		t.Fatal("default S3 client has no timeout")
	}
}

func TestDestinationRelocationKeepsTheCopyLocation(t *testing.T) {
	s := testStore(t)
	dirA := filepath.Join(t.TempDir(), "a")
	dirB := filepath.Join(t.TempDir(), "b")
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	saved, err := s.PutDestination(DestinationSpec{ID: "share", Kind: "path", Path: dirA, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil || len(meta.DestinationErrors) != 0 {
		t.Fatalf("%+v %v", meta, err)
	}
	archive := filepath.Join(dirA, saved.Namespace, meta.ID+".tgbk")
	if _, err := os.Stat(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutDestination(DestinationSpec{ID: "share", Kind: "path", Path: dirA, Namespace: saved.Namespace, RetentionDays: 9}); err != nil {
		t.Fatalf("retention change: %v", err)
	}
	if _, err := s.PutDestination(DestinationSpec{ID: "share", Kind: "path", Path: dirB, Namespace: saved.Namespace, RetentionDays: 9}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("relocation %v", err)
	}
	specs, err := s.Book.Specs()
	if err != nil || specs[0].Path != dirA {
		t.Fatalf("%+v %v", specs, err)
	}
	if err := s.Delete(context.Background(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("archive left at the original location: %v", err)
	}

	started := make(chan struct{})
	releaseCopy := make(chan struct{})
	var once sync.Once
	base, err := OpenDestination(saved)
	if err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(DestinationSpec) (Destination, error) {
		return blockOnceDest{Destination: base, started: started, release: releaseCopy, once: &once}, nil
	}
	created := make(chan error, 1)
	go func() {
		_, err := s.Create(context.Background(), reasonManual)
		created <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("copy did not start")
	}
	if s.mu.TryLock() {
		s.mu.Unlock()
		t.Fatal("store lock was not held while the copy was blocked")
	}
	relocated := make(chan error, 1)
	go func() {
		_, err := s.PutDestination(DestinationSpec{ID: "share", Kind: "path", Path: dirB, Namespace: saved.Namespace, RetentionDays: 9})
		relocated <- err
	}()
	close(releaseCopy)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if err := <-relocated; !errors.Is(err, ErrBadRequest) {
		t.Fatalf("concurrent relocation %v", err)
	}
	specs, err = s.Book.Specs()
	if err != nil || specs[0].Path != dirA {
		t.Fatalf("path changed during copy %+v %v", specs, err)
	}
}

func TestBackupPathDestinationCopyFailureNotifies(t *testing.T) {
	s := testStore(t)
	destDir := filepath.Join(t.TempDir(), "share")
	bookPath := filepath.Join(s.Dir, "destinations.json")
	s.Book = &DestinationBook{Path: bookPath}
	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: destDir, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	out := OpenOutbox(filepath.Join(s.Dir, "outbox.jsonl"))
	s.Outbox = out
	meta, err := s.Create(context.Background(), reasonNightly)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.DestinationErrors) != 0 {
		t.Fatalf("errors = %v", meta.DestinationErrors)
	}
	specs, err := s.Book.Specs()
	if err != nil || len(specs) != 1 || specs[0].Namespace == "" {
		t.Fatal(specs, err)
	}
	copied, err := os.ReadFile(filepath.Join(destDir, specs[0].Namespace, meta.ID+".tgbk"))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := sha256Hex(bytes.NewReader(copied))
	if err != nil || sum != meta.SHA256 {
		t.Fatalf("copied checksum %s want %s err %v", sum, meta.SHA256, err)
	}

	bad := &DestinationBook{Path: filepath.Join(t.TempDir(), "none.json")}
	if _, err := bad.Put(DestinationSpec{ID: "missing", Kind: "path", Path: filepath.Join(t.TempDir(), "nope", "blocked")}); err != nil {
		t.Fatal(err)
	}
	// Make the destination directory a file so the copy fails.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "bad-dest.json")}
	if _, err := s.Book.Put(DestinationSpec{ID: "blocked", Kind: "path", Path: blocked}); err != nil {
		t.Fatal(err)
	}
	meta, err = s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.DestinationErrors) != 1 {
		t.Fatalf("errors = %v", meta.DestinationErrors)
	}
	notes, err := out.List()
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes = %+v err %v", notes, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, meta.ID+".tgbk")); err != nil {
		t.Fatal("local archive missing after destination failure")
	}
}

func TestTestRestoreCountMismatch(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	err = s.TestRestore(context.Background(), meta.ID, func(context.Context, io.Reader) (map[string]int64, error) {
		return map[string]int64{"core.synthetic_items": 2}, nil
	})
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("got %v", err)
	}
	err = s.TestRestore(context.Background(), meta.ID, func(context.Context, io.Reader) (map[string]int64, error) {
		return map[string]int64{"core.synthetic_items": 3}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRejectsArchivePathEscape(t *testing.T) {
	var buf bytes.Buffer
	if err := writeTar(&buf, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	err := untar(t.TempDir(), strings.NewReader("not a tar"))
	if err == nil {
		t.Fatal("accepted garbage")
	}
	// Build a tiny tar with a parent path by hand through the reader check.
	err = untar(t.TempDir(), tarWithName(t, "../escape.txt"))
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("got %v", err)
	}
}

func TestDestinationSpecRedactsSecret(t *testing.T) {
	book := &DestinationBook{Path: filepath.Join(t.TempDir(), "dest.json")}
	spec := DestinationSpec{
		ID: "obj", Kind: "s3", Endpoint: "http://127.0.0.1:9000", Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKIA", SecretKey: "super-secret-value",
	}
	if _, err := book.Put(spec); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(book.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	pub := spec.Public()
	if pub.SecretKey != "" || strings.Contains(pub.AccessKey, "AKIA") {
		t.Fatalf("public = %+v", pub)
	}
	if _, err := book.Put(DestinationSpec{ID: "../x", Kind: "path", Path: "/tmp"}); err == nil {
		t.Fatal("accepted bad id")
	}
}

type listDest struct{ objs []RemoteObject }

func (d listDest) ID() string   { return "list" }
func (d listDest) Kind() string { return "sftp" }
func (d listDest) Put(context.Context, string, io.Reader, string) error {
	return nil
}
func (d listDest) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, ErrNotFound
}
func (d listDest) Delete(context.Context, string) error { return nil }
func (d listDest) List(context.Context) ([]RemoteObject, error) {
	return d.objs, nil
}
func (d listDest) Test(context.Context) error { return nil }

func TestPruneSkipsUnknownSFTPTime(t *testing.T) {
	deleted, err := PruneDestination(context.Background(), listDest{objs: []RemoteObject{
		{Name: "20200101T000000Z-0123456789abcdef.tgbk"},
		{Name: "notes.txt"},
	}}, time.Hour, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("pruned %v", deleted)
	}
}

func TestLocalPruneKeepsLongerDestinationCopy(t *testing.T) {
	s := testStore(t)
	old := time.Now().Add(-40 * 24 * time.Hour)
	s.Now = func() time.Time { return old }
	destDir := filepath.Join(t.TempDir(), "share")
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: destDir, RetentionDays: 365}); err != nil {
		t.Fatal(err)
	}
	meta, err := s.Create(context.Background(), reasonNightly)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := s.Book.Specs()
	if err != nil || len(specs) != 1 || specs[0].Namespace == "" {
		t.Fatal(specs, err)
	}
	remote := filepath.Join(destDir, specs[0].Namespace, meta.ID+".tgbk")
	if err := os.Chtimes(remote, old, old); err != nil {
		t.Fatal(err)
	}
	s.Now = time.Now
	n, err := s.Prune(context.Background(), 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("local pruned %d", n)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, meta.ID+".tgbk")); !os.IsNotExist(err) {
		t.Fatal("local archive still present")
	}
	if _, err := os.Stat(remote); err != nil {
		t.Fatalf("remote copy removed with local retention: %v", err)
	}
	removed, err := s.PruneDestinations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("destination pruned %d within 365 days", removed)
	}
	if _, err := os.Stat(remote); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRetriesAfterRemoteFailure(t *testing.T) {
	s := testStore(t)
	rootA := filepath.Join(t.TempDir(), "a")
	rootB := filepath.Join(t.TempDir(), "b")
	book := &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := book.Put(DestinationSpec{ID: "a", Kind: "path", Path: rootA, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	specB, err := book.Put(DestinationSpec{ID: "b", Kind: "path", Path: rootB, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	s.Book = book
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(rootA, "other-firm.tgbk")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootA, "other-firm.sha256"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	nsB := filepath.Join(rootB, specB.Namespace)
	if err := os.Chmod(nsB, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), meta.ID); !errors.Is(err, ErrDestination) {
		_ = os.Chmod(nsB, 0o700)
		t.Fatalf("got %v", err)
	}
	if err := os.Chmod(nsB, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(nsB, meta.ID+".tgbk")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	kept, err := s.lookup(meta.ID)
	if err != nil || !kept.Deleting {
		t.Fatalf("metadata was dropped before remote cleanup: %+v %v", kept, err)
	}
	if _, err := os.Stat(filepath.Join(rootB, specB.Namespace, meta.ID+".sha256")); err != nil {
		t.Fatalf("checksum sidecar was removed before the failed delete could be retried: %v", err)
	}
	s.Now = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	if n, err := s.Prune(context.Background(), time.Hour); err != nil || n != 0 {
		t.Fatalf("local prune removed a deleting archive: %d %v", n, err)
	}
	if _, err := s.lookup(meta.ID); err != nil {
		t.Fatal(err)
	}
	reopened := &Store{Dir: s.Dir, Book: book, Passphrase: s.Passphrase}
	if err := reopened.Delete(context.Background(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.lookup(meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("metadata remained after confirmed delete: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated archive removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootA, "other-firm.sha256")); err != nil {
		t.Fatalf("unrelated checksum removed: %v", err)
	}
}

func TestDeleteWaitsForBlockedCopy(t *testing.T) {
	s := testStore(t)
	root := filepath.Join(t.TempDir(), "share")
	book := &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	s.Book = book
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		base, err := OpenDestination(spec)
		if err != nil {
			return nil, err
		}
		return blockOnceDest{Destination: base, started: started, release: release, once: &once}, nil
	}
	errc := make(chan error, 1)
	go func() {
		_, err := s.Create(context.Background(), reasonManual)
		errc <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("copy did not start")
	}
	var id string
	deadline := time.Now().Add(2 * time.Second)
	for id == "" {
		all, _ := s.List()
		if len(all) == 1 {
			id = all[0].ID
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metadata was not published while the copy was blocked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- s.Delete(context.Background(), id) }()
	select {
	case err := <-deleted:
		t.Fatalf("delete finished while the upload was still blocked: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	specs, err := book.Specs()
	if err != nil || len(specs) != 1 {
		t.Fatal(specs, err)
	}
	if _, err := os.Stat(filepath.Join(root, specs[0].Namespace, id+".tgbk")); !os.IsNotExist(err) {
		t.Fatalf("archive reappeared after delete: %v", err)
	}
	if _, err := s.lookup(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("metadata reappeared: %v", err)
	}
}

type blockOnceDest struct {
	Destination
	started chan struct{}
	release chan struct{}
	once    *sync.Once
}

func (b blockOnceDest) Put(ctx context.Context, name string, r io.Reader, sha string) error {
	b.once.Do(func() { close(b.started); <-b.release })
	return b.Destination.Put(ctx, name, r, sha)
}

func TestReceiptsAcrossBooksPreserveUnrelated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "destination-receipts.json")
	a := &ReceiptBook{Path: path}
	b := &ReceiptBook{Path: path}
	old := time.Now().Add(-48 * time.Hour)
	if err := a.Add(OwnedObject{DestinationID: "share", Name: "owned.tgbk", Created: old}); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(OwnedObject{DestinationID: "share", Name: "other.tgbk", Created: old}); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove("share", "owned.tgbk"); err != nil {
		t.Fatal(err)
	}
	got, err := b.List("share")
	if err != nil || len(got) != 1 || got[0].Name != "other.tgbk" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestPathDeleteMissingIsIdempotentAndPermissionIsNot(t *testing.T) {
	root := t.TempDir()
	dest := &PathDestination{Spec: DestinationSpec{ID: "path", Kind: "path", Path: root, Namespace: "nsowned01"}}
	if err := os.MkdirAll(dest.dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := dest.Delete(context.Background(), "missing.tgbk"); err != nil {
		t.Fatalf("missing owned archive: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest.dir(), "kept.tgbk"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dest.dir(), 0o555); err != nil {
		t.Fatal(err)
	}
	err := dest.Delete(context.Background(), "kept.tgbk")
	_ = os.Chmod(dest.dir(), 0o700)
	if err == nil || os.IsNotExist(err) {
		t.Fatalf("permission error was treated as absence: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest.dir(), "kept.tgbk")); statErr != nil {
		t.Fatalf("permission failure removed the archive: %v", statErr)
	}
}

func TestEncryptFailureDoesNotHang(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "big"), bytes.Repeat([]byte("a"), 4<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- writeEncryptedTar(&failAfterWriter{limit: 64}, work, "phrase")
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a write failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("encryption blocked the tar producer")
	}
}

type failAfterWriter struct{ limit int }

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		return 0, errors.New("disk full")
	}
	if len(p) > w.limit {
		n := w.limit
		w.limit = 0
		return n, errors.New("disk full")
	}
	w.limit -= len(p)
	return len(p), nil
}

func TestCompareCountsRequiresTheSameTables(t *testing.T) {
	if err := compareCounts(map[string]int64{"core.users": 0}, map[string]int64{"core.users": 0}); err != nil {
		t.Fatalf("empty table that exists: %v", err)
	}
	if err := compareCounts(map[string]int64{}, map[string]int64{}); err != nil {
		t.Fatalf("no tables: %v", err)
	}
	if err := compareCounts(map[string]int64{"core.users": 0}, map[string]int64{"core.matters": 0}); err == nil || !errors.Is(err, ErrChecksum) {
		t.Fatalf("different zero-row tables: %v", err)
	}
	if err := compareCounts(map[string]int64{"core.users": 0, "core.matters": 1}, map[string]int64{"core.users": 0, "core.notes": 1}); err == nil || !errors.Is(err, ErrChecksum) {
		t.Fatalf("same length, different names: %v", err)
	}
	if err := compareCounts(map[string]int64{"core.users": 0}, map[string]int64{"core.users": 0, "core.matters": 0}); err == nil || !errors.Is(err, ErrChecksum) {
		t.Fatalf("extra empty table: %v", err)
	}
	if err := compareCounts(map[string]int64{"core.users": 2}, map[string]int64{"core.users": 1}); err == nil || !errors.Is(err, ErrChecksum) {
		t.Fatalf("count mismatch: %v", err)
	}
}

func TestCountsFromDumpIgnoreLaterText(t *testing.T) {
	sql := "INSERT INTO core.synthetic_items VALUES (9);\nCOPY core.synthetic_items (id) FROM stdin;\n1\n2\n\\.\n"
	counts, err := countsFromDump(strings.NewReader(sql))
	if err != nil {
		t.Fatal(err)
	}
	if counts["core.synthetic_items"] != 2 {
		t.Fatalf("counts %v", counts)
	}
}

func TestStalledPathPutReleasesTheGateAndKeepsPendingOwnership(t *testing.T) {
	old := destinationIOTimeout
	destinationIOTimeout = 200 * time.Millisecond
	t.Cleanup(func() { destinationIOTimeout = old })

	s := testStore(t)
	root := filepath.Join(t.TempDir(), "share")
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	releaseCh := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(release)
	spec, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(got DestinationSpec) (Destination, error) {
		return &PathDestination{Spec: got, stall: func(step string) {
			if step == "hash" {
				startOnce.Do(func() { close(started) })
				<-releaseCh
			}
		}}, nil
	}
	ops := &Operations{Store: s, Gate: &Gate{}, Passphrase: func() (string, error) { return s.Passphrase, nil }}
	type result struct {
		meta BackupMeta
		err  error
	}
	done := make(chan result, 1)
	go func() {
		meta, err := ops.CreateBackup(context.Background(), reasonManual)
		done <- result{meta, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("path hash did not start")
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptPending {
		t.Fatalf("ownership before the upload finished: %+v %v", owned, err)
	}
	archiveName := owned[0].Name
	again := &PathDestination{Spec: spec}
	err = again.Put(context.Background(), archiveName, bytes.NewReader([]byte("x")), "")
	if !errors.Is(err, ErrRemoteUncertain) || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second put = %v", err)
	}
	partial := filepath.Join(root, spec.Namespace, archiveName+".partial")
	if _, err := os.Stat(partial); err != nil {
		t.Fatalf("partial archive: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.meta.DestinationErrors) != 1 || !strings.Contains(got.meta.DestinationErrors[0], "still running") || strings.Contains(got.meta.DestinationErrors[0], "cancel") {
			t.Fatalf("errors = %v", got.meta.DestinationErrors)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("backup stayed blocked on path destination io")
	}
	releaseGate, ok := ops.Gate.Try()
	if !ok {
		t.Fatal("operation gate stayed held after the wait ended")
	}
	releaseGate()
	if !remoteBusy(remoteKey("share", archiveName)) {
		t.Fatal("filesystem operation was dropped while the syscall was still running")
	}
	release()
	deadline := time.Now().Add(2 * time.Second)
	for remoteBusy(remoteKey("share", archiveName)) {
		if time.Now().After(deadline) {
			t.Fatal("path worker did not finish after the syscall returned")
		}
		time.Sleep(10 * time.Millisecond)
	}
	dest, err := OpenDestination(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	owned, err = s.receiptBook().List("share")
	sideName := strings.TrimSuffix(archiveName, ".tgbk") + ".sha256"
	if err != nil || !receiptState(owned, archiveName, "") || !receiptState(owned, sideName, "") {
		t.Fatalf("receipt after the upload finished: %+v %v", owned, err)
	}
	if _, err := os.Stat(filepath.Join(root, spec.Namespace, archiveName)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, spec.Namespace, sideName)); err != nil {
		t.Fatalf("checksum sidecar missing: %v", err)
	}
}

func TestStalledPathReadStaysTrackedUntilTheSyscallReturns(t *testing.T) {
	old := destinationIOTimeout
	destinationIOTimeout = 150 * time.Millisecond
	t.Cleanup(func() { destinationIOTimeout = old })
	root := t.TempDir()
	dest := &PathDestination{Spec: DestinationSpec{ID: "share", Kind: "path", Path: root, Namespace: "nsowned01"}}
	body := []byte("synthetic-archive")
	sum, err := sha256Hex(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := dest.Put(context.Background(), "owned.tgbk", bytes.NewReader(body), sum); err != nil {
		t.Fatal(err)
	}
	releaseCh := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(release)
	dest.stall = func(step string) {
		if step == "read" {
			startOnce.Do(func() { close(started) })
			<-releaseCh
		}
	}
	rc, err := dest.Get(context.Background(), "owned.tgbk")
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, rc)
		errc <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not start")
	}
	select {
	case err := <-errc:
		if !errors.Is(err, ErrRemoteUncertain) || !strings.Contains(err.Error(), "still running") || strings.Contains(err.Error(), "cancel") {
			t.Fatalf("read = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read wait did not end")
	}
	if !remoteBusy(remoteKey("share", "owned.tgbk")) {
		t.Fatal("stalled read was dropped")
	}
	release()
	deadline := time.Now().Add(2 * time.Second)
	for remoteBusy(remoteKey("share", "owned.tgbk")) {
		if time.Now().After(deadline) {
			t.Fatal("read worker did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStalledPathRestoreRestartsWriters(t *testing.T) {
	old := destinationIOTimeout
	destinationIOTimeout = 200 * time.Millisecond
	t.Cleanup(func() { destinationIOTimeout = old })
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "share")
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	releaseCh := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(release)
	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		return &PathDestination{Spec: spec, stall: func(step string) {
			if step == "hash" {
				startOnce.Do(func() { close(started) })
				<-releaseCh
			}
		}}, nil
	}
	var order []string
	ops := &Operations{
		Store: s, Gate: &Gate{}, Now: s.Now,
		Runtime:    &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
		Passphrase: func() (string, error) { return s.Passphrase, nil },
		ApplyDump: func(_ context.Context, r io.Reader) error {
			_, err := io.Copy(io.Discard, r)
			return err
		},
	}
	type result struct {
		rec RestoreRecord
		err error
	}
	done := make(chan result, 1)
	go func() {
		rec, err := ops.Restore(context.Background(), meta.ID)
		done <- result{rec, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("safety backup did not reach the path destination")
	}
	select {
	case got := <-done:
		if got.err != nil || got.rec.Status != "completed" {
			t.Fatalf("%+v %v order %v", got.rec, got.err, order)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restore left the cores stopped on path destination io")
	}
	if !containsStep(order, "stop:core-1") || !containsStep(order, "restart:core-1") {
		t.Fatalf("order %v", order)
	}
	releaseGate, ok := ops.Gate.Try()
	if !ok {
		t.Fatal("gate stayed held")
	}
	releaseGate()
	release()
}

func TestPendingUploadReconcileDoesNotTouchOtherInstallations(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "share")
	ns := "nsowned01"
	dir := filepath.Join(root, ns)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	otherDir := filepath.Join(root, "other-installation")
	if err := os.MkdirAll(otherDir, 0o700); err != nil {
		t.Fatal(err)
	}
	otherFile := filepath.Join(otherDir, "other.tgbk")
	if err := os.WriteFile(otherFile, []byte("other-firm"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "foreign.tgbk")
	if err := os.WriteFile(foreign, []byte("not-ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	spec, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, Namespace: ns, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	receipts := s.receiptBook()
	dest, err := OpenDestination(spec)
	if err != nil {
		t.Fatal(err)
	}
	name := meta.ID + ".tgbk"
	partial := filepath.Join(dir, name+".partial")
	if err := os.WriteFile(partial, []byte("partial-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := receipts.Add(OwnedObject{DestinationID: "share", Name: name, Created: meta.CreatedAt, State: receiptPending}); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Fatalf("other installation removed: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("unreceipted object removed: %v", err)
	}
	owned, err := receipts.List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptUncertain || owned[0].Name != name {
		t.Fatalf("missing object erased a crash-recovered upload: %+v %v", owned, err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatalf("reconcile published an archive that was not there: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := receipts.Add(OwnedObject{DestinationID: "share", Name: name, Created: meta.CreatedAt, State: receiptPending}); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	owned, err = receipts.List("share")
	if err != nil || !receiptState(owned, name, "") || !receiptState(owned, meta.ID+".sha256", "") {
		t.Fatalf("archive completed without a valid sidecar: %+v %v", owned, err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, meta.ID+".sha256")); err != nil {
		t.Fatalf("sidecar was not stored: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, name), []byte("not-the-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := receipts.Add(OwnedObject{DestinationID: "share", Name: name, Created: meta.CreatedAt, State: receiptPending}); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatalf("mismatched upload remained: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("unreceipted object removed: %v", err)
	}
	if owned, err := receipts.List("share"); err != nil || receiptState(owned, name, "") || receiptState(owned, name, receiptPending) {
		t.Fatalf("mismatched archive receipt remained: %+v %v", owned, err)
	}
}

func TestRejectedUploadClearsWhenAbsenceIsConfirmed(t *testing.T) {
	s := testStore(t)
	d := &scriptDest{id: "share", objects: map[string][]byte{"other-firm.tgbk": []byte("keep")}}
	d.archivePut = &remoteStatusError{Status: http.StatusForbidden, Detail: "denied"}
	d.denyDelete = true
	d.denyRead = true
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: t.TempDir(), RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(DestinationSpec) (Destination, error) { return d, nil }
	// The first Create had no destination. This one is refused before any object exists.
	copied, err := s.Create(context.Background(), reasonManual)
	if err != nil || len(copied.DestinationErrors) != 1 {
		t.Fatalf("%+v %v", copied.DestinationErrors, err)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptRejected || owned[0].Name != copied.ID+".tgbk" {
		t.Fatalf("rejection was not durable: %+v %v", owned, err)
	}
	if _, ok := d.objects[copied.ID+".tgbk"]; ok {
		t.Fatal("rejected put stored an object")
	}
	if err := s.DeleteDestination("share"); err == nil {
		t.Fatal("destination removed while the rejected receipt remained")
	}
	restarted := &Store{Dir: s.Dir, Book: s.Book, Passphrase: s.Passphrase, Now: s.Now, OpenDestination: s.OpenDestination}
	if err := restarted.reconcileDestination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	owned, err = restarted.receiptBook().List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptRejected {
		t.Fatalf("403 cleanup became uncertain: %+v %v", owned, err)
	}
	d.denyDelete = false
	d.denyRead = false
	if err := restarted.reconcileDestination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if owned, err = restarted.receiptBook().List("share"); err != nil || len(owned) != 0 {
		t.Fatalf("confirmed absence left the rejected receipt: %+v %v", owned, err)
	}
	if string(d.objects["other-firm.tgbk"]) != "keep" {
		t.Fatalf("other firm %q", d.objects["other-firm.tgbk"])
	}
	if err := restarted.DeleteDestination("share"); err != nil {
		t.Fatal(err)
	}
}

func TestCrashBeforeSidecarReceiptUploadsAndValidates(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("archive-bytes-"), 16000)
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(s.Dir, meta.ID+".tgbk"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, meta.ID+".sha256"), []byte(digest+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir, meta.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored BackupMeta
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	stored.SHA256 = digest
	stored.Bytes = int64(len(payload))
	if err := os.WriteFile(filepath.Join(s.Dir, meta.ID+".json"), append(mustJSON(stored), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	stream := &chunkReader{b: payload}
	d := &scriptDest{id: "share", objects: map[string][]byte{meta.ID + ".tgbk": payload}, stream: stream}
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: t.TempDir(), RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	if err := s.receiptBook().Add(OwnedObject{DestinationID: "share", Name: meta.ID + ".tgbk", Created: meta.CreatedAt, State: receiptPending}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(DestinationSpec) (Destination, error) { return d, nil }
	if err := s.reconcileDestination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if stream.max > 32*1024 {
		t.Fatalf("archive hash asked for %d bytes at once", stream.max)
	}
	if stream.err != nil {
		t.Fatal(stream.err)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || !receiptState(owned, meta.ID+".tgbk", "") || !receiptState(owned, meta.ID+".sha256", "") {
		t.Fatalf("copy completed without a validated sidecar: %+v %v", owned, err)
	}
	if !bytes.Equal(bytes.TrimSpace(d.objects[meta.ID+".sha256"]), []byte(digest)) {
		t.Fatalf("sidecar %q", d.objects[meta.ID+".sha256"])
	}
	if len(d.puts) != 1 || d.puts[0] != meta.ID+".sha256" {
		t.Fatalf("sidecar puts %v", d.puts)
	}
}

func TestMalformedSidecarDoesNotCompleteTheArchive(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir, meta.ID+".tgbk"))
	if err != nil {
		t.Fatal(err)
	}
	d := &scriptDest{
		id:       "share",
		objects:  map[string][]byte{meta.ID + ".tgbk": raw},
		override: map[string][]byte{meta.ID + ".sha256": bytes.Repeat([]byte("x"), 4096)},
	}
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: t.TempDir(), RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	if err := s.receiptBook().Add(OwnedObject{DestinationID: "share", Name: meta.ID + ".tgbk", Created: meta.CreatedAt, State: receiptPending}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(DestinationSpec) (Destination, error) { return d, nil }
	if err := s.reconcileDestination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || receiptState(owned, meta.ID+".tgbk", "") || receiptState(owned, meta.ID+".sha256", "") {
		t.Fatalf("malformed sidecar completed the copy: %+v %v", owned, err)
	}
}

func TestUploadOutcomeBoundaries(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir, meta.ID+".tgbk"))
	if err != nil {
		t.Fatal(err)
	}
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: t.TempDir(), RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	archive := meta.ID + ".tgbk"
	side := meta.ID + ".sha256"
	d := &scriptDest{id: "share", objects: map[string][]byte{}}
	s.OpenDestination = func(DestinationSpec) (Destination, error) { return d, nil }
	receipts := s.receiptBook()

	d.archivePut = io.EOF
	if _, err := s.Create(context.Background(), reasonManual); err != nil {
		t.Fatal(err)
	}
	d.archivePut = nil
	// The copy above is a second backup. Use the fresh id from the book by listing.
	// The EOF case is asserted through the receipt of that new backup below.
	owned, err := receipts.List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptUncertain {
		t.Fatalf("lost archive ack: %+v %v", owned, err)
	}
	lostName := owned[0].Name
	if err := s.reconcileDestination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	owned, err = receipts.List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptUncertain || owned[0].Name != lostName {
		t.Fatalf("404 cleared a lost upload: %+v %v", owned, err)
	}

	d.putErr = map[string]error{side: io.EOF}
	d.objects[archive] = raw
	if err := receipts.Add(OwnedObject{DestinationID: "share", Name: archive, Created: meta.CreatedAt, State: receiptPending}); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileDestination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	owned, err = receipts.List("share")
	if err != nil || receiptState(owned, archive, "") || !receiptState(owned, side, receiptUncertain) {
		t.Fatalf("sidecar eof completed the archive: %+v %v", owned, err)
	}
	delete(d.objects, side)
	if err := s.reconcileDestination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if owned, err = receipts.List("share"); err != nil || !receiptState(owned, side, receiptUncertain) {
		t.Fatalf("sidecar 404 cleared a lost checksum: %+v %v", owned, err)
	}
}

type scriptDest struct {
	id         string
	objects    map[string][]byte
	putErr     map[string]error
	delErr     map[string]error
	override   map[string][]byte
	archivePut error
	denyDelete bool
	denyRead   bool
	stream     *chunkReader
	puts       []string
}

func (d *scriptDest) ID() string                 { return d.id }
func (d *scriptDest) Kind() string               { return "script" }
func (d *scriptDest) Test(context.Context) error { return nil }
func (d *scriptDest) List(context.Context) ([]RemoteObject, error) {
	return nil, nil
}
func (d *scriptDest) Put(_ context.Context, name string, r io.Reader, _ string) error {
	d.puts = append(d.puts, name)
	if err := d.putErr[name]; err != nil {
		return err
	}
	if d.archivePut != nil && strings.HasSuffix(name, ".tgbk") {
		return d.archivePut
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if d.objects == nil {
		d.objects = map[string][]byte{}
	}
	d.objects[name] = body
	return nil
}
func (d *scriptDest) Get(_ context.Context, name string) (io.ReadCloser, error) {
	if d.denyRead {
		return nil, &remoteStatusError{Status: http.StatusForbidden, Detail: "denied"}
	}
	if d.stream != nil && strings.HasSuffix(name, ".tgbk") {
		return io.NopCloser(d.stream), nil
	}
	if body, ok := d.override[name]; ok {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	body, ok := d.objects[name]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}
func (d *scriptDest) Delete(_ context.Context, name string) error {
	if err := d.delErr[name]; err != nil {
		return err
	}
	if d.denyDelete {
		return &remoteStatusError{Status: http.StatusForbidden, Detail: "denied"}
	}
	delete(d.objects, name)
	delete(d.override, name)
	return nil
}

type chunkReader struct {
	b   []byte
	off int
	max int
	err error
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(p) > 32*1024 {
		c.err = errors.New("whole-buffer read")
		return 0, c.err
	}
	if len(p) > c.max {
		c.max = len(p)
	}
	if c.off >= len(c.b) {
		return 0, io.EOF
	}
	n := copy(p, c.b[c.off:])
	c.off += n
	return n, nil
}

func receiptState(owned []OwnedObject, name, state string) bool {
	for _, obj := range owned {
		if obj.Name == name && obj.State == state {
			return true
		}
	}
	return false
}

func TestTimedOutUploadDeleteKeepsOwnershipUntilTheWriterFinishes(t *testing.T) {
	old := destinationIOTimeout
	destinationIOTimeout = 200 * time.Millisecond
	t.Cleanup(func() { destinationIOTimeout = old })

	s := testStore(t)
	root := filepath.Join(t.TempDir(), "share")
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	releaseCh := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(release)
	spec, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(got DestinationSpec) (Destination, error) {
		return &PathDestination{Spec: got, stall: func(step string) {
			if step == "rename" {
				startOnce.Do(func() { close(started) })
				<-releaseCh
			}
		}}, nil
	}
	ops := &Operations{Store: s, Gate: &Gate{}, Passphrase: func() (string, error) { return s.Passphrase, nil }}
	type result struct {
		meta BackupMeta
		err  error
	}
	done := make(chan result, 1)
	go func() {
		meta, err := ops.CreateBackup(context.Background(), reasonManual)
		done <- result{meta, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("rename did not start")
	}
	var created result
	select {
	case created = <-done:
		if created.err != nil || len(created.meta.DestinationErrors) != 1 || !strings.Contains(created.meta.DestinationErrors[0], "still running") {
			t.Fatalf("%+v %v", created.meta.DestinationErrors, created.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("backup stayed blocked after the upload wait")
	}
	name := created.meta.ID + ".tgbk"
	ns := filepath.Join(root, spec.Namespace)
	partial := filepath.Join(ns, name+".partial")
	final := filepath.Join(ns, name)
	if _, err := os.Stat(partial); err != nil {
		t.Fatalf("partial: %v", err)
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatal("final archive existed before the late writer resumed")
	}
	otherDir := filepath.Join(root, "other-installation")
	if err := os.MkdirAll(otherDir, 0o700); err != nil {
		t.Fatal(err)
	}
	otherFile := filepath.Join(otherDir, "other.tgbk")
	if err := os.WriteFile(otherFile, []byte("other-firm"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(ns, "foreign.tgbk")
	if err := os.WriteFile(foreign, []byte("not-ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/v1/backups/"+created.meta.ID, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	NewMux("secret", ops, nil).ServeHTTP(rec, req)
	if rec.Code == http.StatusNoContent {
		t.Fatal("delete reported success while the upload was still running")
	}
	kept, err := s.lookup(created.meta.ID)
	if err != nil || !kept.Deleting {
		t.Fatalf("deleting record was dropped: %+v %v", kept, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, name)); err != nil {
		t.Fatalf("local archive removed: %v", err)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || len(owned) != 1 || owned[0].Name != name || owned[0].State != receiptPending {
		t.Fatalf("receipt was dropped while the writer was running: %+v %v", owned, err)
	}
	release()
	deadline := time.Now().Add(2 * time.Second)
	for remoteBusy(remoteKey("share", name)) {
		if time.Now().After(deadline) {
			t.Fatal("late writer did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("late writer did not publish the archive: %v", err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("partial remained after rename: %v", err)
	}
	dest, err := OpenDestination(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("late archive remained after reconcile: %v", err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("partial remained after reconcile: %v", err)
	}
	if owned, err := s.receiptBook().List("share"); err != nil || len(owned) != 0 {
		t.Fatalf("receipt remained after the objects were confirmed absent: %+v %v", owned, err)
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Fatalf("other installation removed: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("unreceipted object removed: %v", err)
	}
	restarted := &Store{Dir: s.Dir, Book: s.Book, Passphrase: s.Passphrase, Now: s.Now}
	if err := restarted.Delete(context.Background(), created.meta.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.lookup(created.meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting record remained after absence was confirmed: %v", err)
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Fatalf("other installation removed on restart: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("unreceipted object removed on restart: %v", err)
	}
}

func TestReceiptWriteFailureDoesNotUpload(t *testing.T) {
	s := testStore(t)
	root := filepath.Join(t.TempDir(), "share")
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	spec, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(s.Dir, "destination-receipts.json")
	if err := os.Mkdir(receiptPath, 0o700); err != nil {
		t.Fatal(err)
	}
	s.receipts = &ReceiptBook{Path: receiptPath}
	var puts int
	s.OpenDestination = func(got DestinationSpec) (Destination, error) {
		base, err := OpenDestination(got)
		if err != nil {
			return nil, err
		}
		return putCount{Destination: base, puts: &puts}, nil
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if puts != 0 {
		t.Fatalf("uploaded without a receipt: %d", puts)
	}
	if len(meta.DestinationErrors) != 1 {
		t.Fatalf("errors = %v", meta.DestinationErrors)
	}
	if _, err := os.Stat(filepath.Join(root, spec.Namespace)); !os.IsNotExist(err) {
		t.Fatalf("destination directory was created: %v", err)
	}
}

type putCount struct {
	Destination
	puts *int
}

func (p putCount) Put(ctx context.Context, name string, r io.Reader, sha string) error {
	*p.puts++
	return p.Destination.Put(ctx, name, r, sha)
}

func TestDeleteSkipsUnreceiptedDestinations(t *testing.T) {
	s := testStore(t)
	root := filepath.Join(t.TempDir(), "owned")
	otherRoot := filepath.Join(t.TempDir(), "unreceipted")
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{ID: "owned", Kind: "path", Path: root, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil || len(meta.DestinationErrors) != 0 {
		t.Fatalf("%+v %v", meta.DestinationErrors, err)
	}
	var contacted int
	prev := s.OpenDestination
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		if spec.Kind == "s3" {
			contacted++
			return nil, errors.New("unreachable s3")
		}
		if prev != nil {
			return prev(spec)
		}
		return OpenDestination(spec)
	}
	if _, err := s.Book.Put(DestinationSpec{
		ID: "later", Kind: "s3", Endpoint: "http://127.0.0.1:9", Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKID", SecretKey: "secret", RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	specs, err := s.Book.Specs()
	if err != nil {
		t.Fatal(err)
	}
	var ns string
	for _, spec := range specs {
		if spec.ID == "owned" {
			ns = spec.Namespace
		}
	}
	sameName := filepath.Join(otherRoot, "nsother01", meta.ID+".tgbk")
	if err := os.MkdirAll(filepath.Dir(sameName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sameName, []byte("not-ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if contacted != 0 {
		t.Fatalf("unreceipted destination was contacted %d times", contacted)
	}
	if _, err := os.Stat(sameName); err != nil {
		t.Fatalf("unreceipted name removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ns, meta.ID+".tgbk")); !os.IsNotExist(err) {
		t.Fatalf("owned archive remained: %v", err)
	}
}

func TestDestinationRemovalKeepsTheOwnedLocation(t *testing.T) {
	s := testStore(t)
	dirA := filepath.Join(t.TempDir(), "a")
	dirB := filepath.Join(t.TempDir(), "b")
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	saved, err := s.PutDestination(DestinationSpec{ID: "share", Kind: "path", Path: dirA, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil || len(meta.DestinationErrors) != 0 {
		t.Fatalf("%+v %v", meta.DestinationErrors, err)
	}
	if err := s.DeleteDestination("share"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("removed a destination that still has a copy: %v", err)
	}
	if _, err := s.PutDestination(DestinationSpec{ID: "share", Kind: "path", Path: dirB, Namespace: saved.Namespace, RetentionDays: 30}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("re-add changed the location: %v", err)
	}
	specs, err := s.Book.Specs()
	if err != nil || len(specs) != 1 || specs[0].Path != dirA {
		t.Fatalf("spec %+v %v", specs, err)
	}
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		base, err := OpenDestination(spec)
		if err != nil {
			return nil, err
		}
		return blockOnceDest{Destination: base, started: started, release: release, once: &once}, nil
	}
	errc := make(chan error, 1)
	go func() {
		_, err := s.Create(context.Background(), reasonManual)
		errc <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("copy did not start")
	}
	removed := make(chan error, 1)
	go func() { removed <- s.DeleteDestination("share") }()
	select {
	case err := <-removed:
		t.Fatalf("destination removal finished during the copy: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	close(release)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if err := <-removed; !errors.Is(err, ErrBadRequest) {
		t.Fatalf("removal after the copy: %v", err)
	}
	specs, err = s.Book.Specs()
	if err != nil || specs[0].Path != dirA {
		t.Fatalf("location changed during the copy %+v %v", specs, err)
	}
}

func tarWithName(t *testing.T, name string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("nope")
	hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}
