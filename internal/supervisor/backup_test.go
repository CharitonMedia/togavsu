package supervisor

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
			_, err := io.WriteString(w, "CREATE TABLE synthetic_items(id int);\nINSERT INTO synthetic_items VALUES (1),(2),(3);\n")
			return err
		},
		Counts: func(context.Context) (map[string]int64, error) {
			return map[string]int64{"core.synthetic_items": 3}, nil
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
	err = s.Restore(context.Background(), meta.ID, "", func(_ context.Context, r io.Reader, man Manifest) error {
		if man.RecordCounts["core.synthetic_items"] != 3 {
			t.Fatalf("counts = %v", man.RecordCounts)
		}
		_, err := io.Copy(&restored, r)
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
	err = s.Restore(context.Background(), meta.ID, "nope", func(context.Context, io.Reader, Manifest) error {
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
	err = s.Restore(context.Background(), meta.ID, "", func(_ context.Context, _ io.Reader, man Manifest) error {
		if len(man.Files) != 2 || man.Files[0].SHA256 != man.Files[1].SHA256 {
			t.Fatalf("files = %+v", man.Files)
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
	copied, err := os.ReadFile(filepath.Join(destDir, meta.ID+".tgbk"))
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
	n, err := PruneDestination(context.Background(), listDest{objs: []RemoteObject{
		{Name: "old.tgbk"},
	}}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("pruned %d", n)
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
