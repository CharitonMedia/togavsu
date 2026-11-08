package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestPathOwnedTemp(t *testing.T) {
	switch os.Getenv("TOGAVSU_PATH_CHILD") {
	case "upload":
		runPathUploadChild()
		return
	case "hold":
		runPathHoldChild()
		return
	}
	t.Run("kill during copy", func(t *testing.T) { testPathCrashBeforeRename(t) })
	t.Run("recreated name after kill", func(t *testing.T) { testPathRecreatedAfterKill(t) })
	t.Run("rename holds the lock", func(t *testing.T) { testPathRenameHoldsLock(t) })
	t.Run("rename lost acknowledgment", func(t *testing.T) { testPathRenameLostAck(t) })
	t.Run("success", func(t *testing.T) { testPathTempSuccess(t) })
	t.Run("foreign identity and retry", func(t *testing.T) { testPathForeignTemp(t) })
	t.Run("receipt keeps ownership until the holder exits", func(t *testing.T) { testPathActiveTemp(t) })
	t.Run("directory sync failure keeps ownership", func(t *testing.T) { testPathDeleteSync(t) })
	t.Run("retention keeps unresolved ownership", func(t *testing.T) { testPathRetentionKeepsTemp(t) })
}

func testPathCrashBeforeRename(t *testing.T) {
	t.Helper()
	root, book := pathTempFixture(t)
	decoy := filepath.Join(root, "nsownedtmp", ".togavsu-ffffffffffffffffffffffffffffffff.tmp")
	if err := os.MkdirAll(filepath.Dir(decoy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte("foreign-temp"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPathOwnedTemp$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"TOGAVSU_PATH_CHILD=upload",
		"TOGAVSU_PATH_MODE=copy",
		"TOGAVSU_PATH_DIR="+root,
		"TOGAVSU_PATH_RECEIPT="+book,
		"TOGAVSU_PATH_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitPathFile(t, ready)
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
	owned := pathReceipts(t, book)
	if len(owned) != 1 || owned[0].Temp == "" || owned[0].TempInode == 0 || owned[0].TempToken == "" {
		t.Fatalf("temp ownership was not durable: %+v", owned)
	}
	tempPath := filepath.Join(root, "nsownedtmp", owned[0].Temp)
	if _, err := os.Stat(tempPath); err != nil {
		t.Fatal(err)
	}
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("owned temp remained: %v", err)
	}
	got, err := os.ReadFile(decoy)
	if err != nil || string(got) != "foreign-temp" {
		t.Fatalf("foreign temp changed: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "nsownedtmp", "backup.tgbk")); !os.IsNotExist(err) {
		t.Fatal("unpublished archive appeared")
	}
}

func testPathRenameLostAck(t *testing.T) {
	t.Helper()
	root, book := pathTempFixture(t)
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPathOwnedTemp$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"TOGAVSU_PATH_CHILD=upload",
		"TOGAVSU_PATH_MODE=lost",
		"TOGAVSU_PATH_DIR="+root,
		"TOGAVSU_PATH_RECEIPT="+book,
		"TOGAVSU_PATH_READY="+ready,
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	waitPathFile(t, ready)
	body, err := os.ReadFile(filepath.Join(root, "nsownedtmp", "backup.tgbk"))
	if err != nil || string(body) != "archive-bytes" {
		t.Fatalf("published file: %q %v", body, err)
	}
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(filepath.Join(root, "nsownedtmp", "backup.tgbk"))
	if err != nil || string(body) != "archive-bytes" {
		t.Fatalf("recovery removed the published file: %q %v", body, err)
	}
	for _, obj := range pathReceipts(t, book) {
		if obj.Temp != "" {
			t.Fatalf("temp ownership remained after rename: %+v", obj)
		}
	}
}

func testPathTempSuccess(t *testing.T) {
	t.Helper()
	root, book := pathTempFixture(t)
	dest := pathDest(root)
	if err := dest.Put(pathTempContext(book), "backup.tgbk", bytes.NewReader([]byte("archive-bytes")), ""); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "nsownedtmp", "backup.tgbk"))
	if err != nil || string(body) != "archive-bytes" {
		t.Fatalf("file: %q %v", body, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "nsownedtmp"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if ownedTempName(ent.Name()) {
			t.Fatalf("temp remained after success: %s", ent.Name())
		}
	}
	for _, obj := range pathReceipts(t, book) {
		if obj.Temp != "" {
			t.Fatalf("temp ownership remained: %+v", obj)
		}
	}
}

func testPathForeignTemp(t *testing.T) {
	t.Helper()
	root, book := pathTempFixture(t)
	ns := filepath.Join(root, "nsownedtmp")
	if err := os.MkdirAll(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	name := ".togavsu-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.tmp"
	path := filepath.Join(ns, name)
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	var info syscall.Stat_t
	if err := syscall.Stat(path, &info); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := os.WriteFile(path, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := newTempToken()
	if err != nil {
		t.Fatal(err)
	}
	receipts := &ReceiptBook{Path: book}
	if err := receipts.Add(OwnedObject{
		DestinationID: "share", Name: "backup.tgbk", State: receiptPending,
		Temp: name, TempInode: info.Ino, TempToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "foreign" {
		t.Fatalf("foreign file changed: %q %v", got, err)
	}
	dest := pathDest(root)
	if err := dest.Put(pathTempContext(book), "backup.tgbk", bytes.NewReader([]byte("retried")), ""); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != "foreign" {
		t.Fatalf("retry removed the foreign file: %q %v", got, err)
	}
	body, err := os.ReadFile(filepath.Join(ns, "backup.tgbk"))
	if err != nil || string(body) != "retried" {
		t.Fatalf("retry file: %q %v", body, err)
	}
}

func testPathActiveTemp(t *testing.T) {
	t.Helper()
	root, book := pathTempFixture(t)
	ns := filepath.Join(root, "nsownedtmp")
	if err := os.MkdirAll(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	name := ".togavsu-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.tmp"
	path := filepath.Join(ns, name)
	if err := os.WriteFile(path, []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	inode, token := stampTemp(t, path)
	receipts := &ReceiptBook{Path: book}
	if err := receipts.Add(OwnedObject{
		DestinationID: "share", Name: "backup.tgbk", State: receiptPending,
		Temp: name, TempInode: inode, TempToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPathOwnedTemp$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"TOGAVSU_PATH_CHILD=hold",
		"TOGAVSU_PATH_FILE="+path,
		"TOGAVSU_PATH_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	waitPathFile(t, ready)
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "active" {
		t.Fatalf("active upload was removed: %q %v", got, err)
	}
	kept := pathReceipts(t, book)
	if len(kept) != 1 || kept[0].TempToken != token || kept[0].State == "" {
		t.Fatalf("reconciliation dropped ownership while the upload was active: %+v", kept)
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	time.Sleep(50 * time.Millisecond)
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp remained after the holder exited: %v", err)
	}
	for _, obj := range pathReceipts(t, book) {
		if obj.Temp != "" || obj.TempToken != "" {
			t.Fatalf("ownership remained after verified removal: %+v", obj)
		}
	}
}

func waitPathFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func stampTemp(t *testing.T, path string) (uint64, string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	token, err := newTempToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := fsetxattr(int(f.Fd()), tempTokenAttr, []byte(token)); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	var info syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &info); err != nil {
		t.Fatal(err)
	}
	return info.Ino, token
}

func pathTempFixture(t *testing.T) (root, book string) {
	t.Helper()
	dir := t.TempDir()
	root = filepath.Join(dir, "dest")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	book = filepath.Join(dir, "destination-receipts.json")
	return root, book
}

func pathDest(root string) *PathDestination {
	return &PathDestination{Spec: DestinationSpec{ID: "share", Kind: "path", Path: root, Namespace: "nsownedtmp"}}
}

func pathTempContext(book string) context.Context {
	receipts := &ReceiptBook{Path: book}
	return WithPathTemp(context.Background(), func(temp string, inode uint64, token, link string, release bool) error {
		obj := OwnedObject{
			DestinationID: "share", Name: "backup.tgbk", State: receiptPending,
			Temp: temp, TempInode: inode, TempToken: token, TempLink: link,
		}
		if release {
			obj.releaseTemp = true
			obj.Temp, obj.TempInode, obj.TempToken, obj.TempLink = "", 0, "", ""
		}
		return receipts.Add(obj)
	})
}

func pathReceipts(t *testing.T, book string) []OwnedObject {
	t.Helper()
	owned, err := (&ReceiptBook{Path: book}).List("share")
	if err != nil {
		t.Fatal(err)
	}
	return owned
}

func pathReconcile(t *testing.T, root, book string) error {
	t.Helper()
	s := &Store{Book: &DestinationBook{Path: filepath.Join(filepath.Dir(book), "destinations.json")}}
	if s.receiptBook().Path != book {
		t.Fatalf("receipt path %s", s.receiptBook().Path)
	}
	return s.reconcileDestination(context.Background(), pathDest(root))
}

func testPathRecreatedAfterKill(t *testing.T) {
	t.Helper()
	root, book, tempPath := killDuringCopy(t)
	if err := os.Remove(tempPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("backup.tgbk", tempPath); err != nil {
		t.Fatal(err)
	}
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(tempPath)
	if err != nil || got != "backup.tgbk" {
		t.Fatalf("substituted symlink changed: %q %v", got, err)
	}
	owned := pathReceipts(t, book)
	if len(owned) != 1 || owned[0].TempToken == "" {
		t.Fatalf("ownership was forgotten for a substituted name: %+v", owned)
	}
}

func testPathRenameHoldsLock(t *testing.T) {
	t.Helper()
	root, book := pathTempFixture(t)
	ready := filepath.Join(t.TempDir(), "ready")
	release := filepath.Join(t.TempDir(), "release")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPathOwnedTemp$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"TOGAVSU_PATH_CHILD=upload",
		"TOGAVSU_PATH_MODE=rename",
		"TOGAVSU_PATH_DIR="+root,
		"TOGAVSU_PATH_RECEIPT="+book,
		"TOGAVSU_PATH_READY="+ready,
		"TOGAVSU_PATH_RELEASE="+release,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	waitPathFile(t, ready)
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	owned := pathReceipts(t, book)
	if len(owned) != 1 || owned[0].Temp == "" {
		t.Fatalf("rename boundary lost the temp record: %+v", owned)
	}
	tempPath := filepath.Join(root, "nsownedtmp", owned[0].Temp)
	if _, err := os.Stat(tempPath); err != nil {
		t.Fatalf("reconciliation unlinked the locked upload: %v", err)
	}
	if err := os.WriteFile(release, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "nsownedtmp", "backup.tgbk"))
	if err != nil || string(body) != "archive-bytes" {
		t.Fatalf("published file: %q %v", body, err)
	}
}

func testPathDeleteSync(t *testing.T) {
	t.Helper()
	root, book := pathTempFixture(t)
	ns := filepath.Join(root, "nsownedtmp")
	if err := os.MkdirAll(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	name := ".togavsu-cccccccccccccccccccccccccccccccc.tmp"
	path := filepath.Join(ns, name)
	if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	inode, token := stampTemp(t, path)
	if err := (&ReceiptBook{Path: book}).Add(OwnedObject{
		DestinationID: "share", Name: "backup.tgbk", State: receiptPending,
		Temp: name, TempInode: inode, TempToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	failSync := true
	dest := pathDest(root)
	dest.dirSync = func(step string) error {
		if failSync && step == "delete" {
			return errors.New("injected directory sync failure")
		}
		return nil
	}
	s := pathStore(t, book)
	err := s.reconcileDestination(context.Background(), dest)
	if err == nil {
		t.Fatal("directory sync failure was acknowledged")
	}
	owned := pathReceipts(t, book)
	if len(owned) != 1 || owned[0].TempToken != token {
		t.Fatalf("sync failure forgot ownership: %+v", owned)
	}
	failSync = false
	if err := s.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	for _, obj := range pathReceipts(t, book) {
		if obj.Temp != "" || obj.TempToken != "" {
			t.Fatalf("absent retry left ownership: %+v", obj)
		}
	}
}

func testPathRetentionKeepsTemp(t *testing.T) {
	t.Helper()
	s := testStore(t)
	root := filepath.Join(t.TempDir(), "share")
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	spec, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, Namespace: "nsownedtmp", RetentionDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	ns := filepath.Join(root, spec.Namespace)
	if err := os.MkdirAll(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(ns, "backup.tgbk")
	if err := os.WriteFile(archive, []byte("published"), 0o600); err != nil {
		t.Fatal(err)
	}
	tempName := ".togavsu-dddddddddddddddddddddddddddddddd.tmp"
	tempPath := filepath.Join(ns, tempName)
	if err := os.WriteFile(tempPath, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := newTempToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.receiptBook().Add(OwnedObject{
		DestinationID: "share", Name: "backup.tgbk", Created: time.Now().Add(-48 * time.Hour),
		Temp: tempName, TempInode: 1, TempToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneDestinations(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(archive)
	if err != nil || string(body) != "published" {
		t.Fatalf("retention removed the archive while temp ownership was unresolved: %q %v", body, err)
	}
	got, err := os.ReadFile(tempPath)
	if err != nil || string(got) != "foreign" {
		t.Fatalf("retention removed the foreign temp: %q %v", got, err)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || len(owned) != 1 || owned[0].TempToken != token {
		t.Fatalf("retention forgot temp ownership: %+v %v", owned, err)
	}
}

func killDuringCopy(t *testing.T) (root, book, tempPath string) {
	t.Helper()
	root, book = pathTempFixture(t)
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPathOwnedTemp$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"TOGAVSU_PATH_CHILD=upload",
		"TOGAVSU_PATH_MODE=copy",
		"TOGAVSU_PATH_DIR="+root,
		"TOGAVSU_PATH_RECEIPT="+book,
		"TOGAVSU_PATH_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitPathFile(t, ready)
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
	owned := pathReceipts(t, book)
	if len(owned) != 1 || owned[0].TempToken == "" {
		t.Fatalf("copy ownership was not durable: %+v", owned)
	}
	return root, book, filepath.Join(root, "nsownedtmp", owned[0].Temp)
}

func pathStore(t *testing.T, book string) *Store {
	t.Helper()
	s := &Store{Book: &DestinationBook{Path: filepath.Join(filepath.Dir(book), "destinations.json")}}
	if s.receiptBook().Path != book {
		t.Fatalf("receipt path %s", s.receiptBook().Path)
	}
	return s
}

func runPathUploadChild() {
	root := os.Getenv("TOGAVSU_PATH_DIR")
	book := os.Getenv("TOGAVSU_PATH_RECEIPT")
	ready := os.Getenv("TOGAVSU_PATH_READY")
	dest := pathDest(root)
	if os.Getenv("TOGAVSU_NO_XATTR") == "1" {
		dest.noXattr = true
	}
	switch os.Getenv("TOGAVSU_PATH_MODE") {
	case "copy":
		dest.stall = func(step string) {
			if step == "copy" {
				_ = os.WriteFile(ready, []byte("ok"), 0o600)
				time.Sleep(time.Minute)
			}
		}
	case "rename":
		dest.stall = func(step string) {
			if step != "rename" {
				return
			}
			_ = os.WriteFile(ready, []byte("ok"), 0o600)
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(os.Getenv("TOGAVSU_PATH_RELEASE")); err == nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			os.Exit(3)
		}
	case "lost":
		dest.dirSync = func(step string) error {
			if step == "file" {
				_ = os.WriteFile(ready, []byte("ok"), 0o600)
				return fmt.Errorf("%w: directory sync", ErrRemoteUncertain)
			}
			return nil
		}
	default:
		os.Exit(2)
	}
	err := dest.Put(pathTempContext(book), "backup.tgbk", bytes.NewReader([]byte("archive-bytes")), "")
	if os.Getenv("TOGAVSU_PATH_MODE") == "lost" && errors.Is(err, ErrRemoteUncertain) {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func TestPathTempRestoreStall(t *testing.T) {
	old := destinationIOTimeout
	destinationIOTimeout = 200 * time.Millisecond
	t.Cleanup(func() { destinationIOTimeout = old })
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "share")
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	spec, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, Namespace: "nsownedtmp", RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	ns := filepath.Join(root, spec.Namespace)
	if err := os.MkdirAll(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	tempName := ".togavsu-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.tmp"
	tempPath := filepath.Join(ns, tempName)
	if err := os.WriteFile(tempPath, []byte("partial-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	inode, token := stampTemp(t, tempPath)
	if err := s.receiptBook().Add(OwnedObject{
		DestinationID: "share", Name: "old.tgbk", State: receiptPending,
		Temp: tempName, TempInode: inode, TempToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	releaseCh := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})
	s.OpenDestination = func(got DestinationSpec) (Destination, error) {
		return &PathDestination{Spec: got, stall: func(step string) {
			if step == "cleanup" {
				once.Do(func() { close(started) })
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
		t.Fatal("safety backup did not reach temp cleanup")
	}
	select {
	case got := <-done:
		if got.err != nil || got.rec.Status != "completed" {
			t.Fatalf("restore did not recover: %+v %v order %v", got.rec, got.err, order)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled temp cleanup held the restore")
	}
	if !containsStep(order, "stop:core-1") || !containsStep(order, "restart:core-1") {
		t.Fatalf("writers were not recovered: %v", order)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, obj := range owned {
		if obj.TempToken == token {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("timed-out cleanup cleared ownership: %+v", owned)
	}
	if !remoteBusy(remoteKey("share", tempName)) {
		t.Fatal("timed-out cleanup dropped its tracking")
	}
	close(releaseCh)
	deadline := time.Now().Add(3 * time.Second)
	for remoteBusy(remoteKey("share", tempName)) {
		if time.Now().After(deadline) {
			t.Fatal("cleanup worker did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	owned, err = s.receiptBook().List("share")
	if err != nil {
		t.Fatal(err)
	}
	kept = false
	for _, obj := range owned {
		if obj.TempToken == token {
			kept = true
		}
	}
	if !kept {
		t.Fatal("the late worker cleared ownership without a new reconciliation")
	}
}

func TestPathShareFallback(t *testing.T) {
	if os.Getenv("TOGAVSU_PATH_CHILD") == "noxattr" {
		runPathUploadChild()
		return
	}
	t.Run("probe sidecar and archive", func(t *testing.T) { testPathNoXattrSuccess(t) })
	t.Run("killed copy recovers", func(t *testing.T) { testPathNoXattrKill(t) })
}

func testPathNoXattrSuccess(t *testing.T) {
	t.Helper()
	root, _ := pathTempFixture(t)
	dest := pathDest(root)
	dest.noXattr = true
	if err := dest.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	spec, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, Namespace: "nsownedtmp", RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	share := &PathDestination{Spec: spec, noXattr: true}
	if err := s.copyOne(context.Background(), share, meta); err != nil {
		t.Fatal(err)
	}
	ns := filepath.Join(root, spec.Namespace)
	body, err := os.ReadFile(filepath.Join(ns, meta.ID+".tgbk"))
	if err != nil || bytes.Contains(body, []byte("TGVTMP1")) {
		t.Fatalf("published archive: %v prefix %v", err, bytes.Contains(body, []byte("TGVTMP1")))
	}
	side, err := os.ReadFile(filepath.Join(ns, meta.ID+".sha256"))
	if err != nil || !bytes.Contains(side, []byte(meta.SHA256)) {
		t.Fatalf("sidecar: %q %v", side, err)
	}
}

func testPathNoXattrKill(t *testing.T) {
	t.Helper()
	root, book := pathTempFixture(t)
	decoy := filepath.Join(root, "nsownedtmp", ".togavsu-ffffffffffffffffffffffffffffffff.tmp")
	if err := os.MkdirAll(filepath.Dir(decoy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte("foreign-temp"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPathShareFallback$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"TOGAVSU_PATH_CHILD=noxattr",
		"TOGAVSU_PATH_MODE=copy",
		"TOGAVSU_NO_XATTR=1",
		"TOGAVSU_PATH_DIR="+root,
		"TOGAVSU_PATH_RECEIPT="+book,
		"TOGAVSU_PATH_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitPathFile(t, ready)
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
	owned := pathReceipts(t, book)
	if len(owned) != 1 || owned[0].TempToken == "" {
		t.Fatalf("no-xattr ownership was not durable: %+v", owned)
	}
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "nsownedtmp", owned[0].Temp)); !os.IsNotExist(err) {
		t.Fatalf("partial temp remained: %v", err)
	}
	got, err := os.ReadFile(decoy)
	if err != nil || string(got) != "foreign-temp" {
		t.Fatalf("foreign temp changed: %q %v", got, err)
	}
}

func TestPathDisposeRestoresForeign(t *testing.T) {
	old := destinationIOTimeout
	destinationIOTimeout = 200 * time.Millisecond
	t.Cleanup(func() { destinationIOTimeout = old })
	root, book := pathTempFixture(t)
	ns := filepath.Join(root, "nsownedtmp")
	if err := os.MkdirAll(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	name := ".togavsu-abababababababababababababababab.tmp"
	path := filepath.Join(ns, name)
	if err := os.WriteFile(path, []byte("owned-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	inode, token := stampTemp(t, path)
	link := tempLinkName(name)
	if err := os.Link(path, filepath.Join(ns, link)); err != nil {
		t.Fatal(err)
	}
	if err := (&ReceiptBook{Path: book}).Add(OwnedObject{
		DestinationID: "share", Name: "backup.tgbk", State: receiptPending,
		Temp: name, TempInode: inode, TempToken: token, TempLink: link,
	}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	dest := pathDest(root)
	dest.stall = func(step string) {
		if step == "dispose" {
			once.Do(func() { close(started) })
			<-release
		}
	}
	s := pathStore(t, book)
	errc := make(chan error, 1)
	go func() {
		errc <- s.reconcileDestination(context.Background(), dest)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("disposition did not start")
	}
	aside := filepath.Join(ns, "owned-aside")
	if err := os.Rename(path, aside); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("foreign-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, ErrRemoteUncertain) {
			t.Fatalf("cleanup returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed-out disposition did not return")
	}
	kept := pathReceipts(t, book)
	if len(kept) != 1 || kept[0].TempToken != token {
		t.Fatalf("timed-out cleanup cleared ownership: %+v", kept)
	}
	if !remoteBusy(remoteKey("share", name)) {
		t.Fatal("timed-out disposition dropped its tracking")
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for remoteBusy(remoteKey("share", name)) {
		if time.Now().After(deadline) {
			t.Fatal("disposition worker did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "foreign-bytes" {
		t.Fatalf("foreign file: %q %v", got, err)
	}
	got, err = os.ReadFile(aside)
	if err != nil || string(got) != "owned-bytes" {
		t.Fatalf("owned inode was not recoverable: %q %v", got, err)
	}
}

func TestAbortedPathUpload(t *testing.T) {
	if os.Getenv("TOGAVSU_COPY_CHILD") == "1" {
		runStoreCopyChild()
		return
	}
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "share")
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	spec, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, Namespace: "nsownedtmp", RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	ns := filepath.Join(root, spec.Namespace)
	if err := os.MkdirAll(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(ns, ".togavsu-ffffffffffffffffffffffffffffffff.tmp")
	if err := os.WriteFile(decoy, []byte("foreign-temp"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAbortedPathUpload$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"TOGAVSU_COPY_CHILD=1",
		"TOGAVSU_COPY_DIR="+s.Dir,
		"TOGAVSU_COPY_ROOT="+root,
		"TOGAVSU_COPY_NS="+spec.Namespace,
		"TOGAVSU_COPY_ID="+meta.ID,
		"TOGAVSU_COPY_SHA="+meta.SHA256,
		"TOGAVSU_COPY_CREATED="+meta.CreatedAt.Format(time.RFC3339Nano),
		"TOGAVSU_COPY_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitPathFile(t, ready)
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
	if err := s.reconcileDestination(context.Background(), &PathDestination{Spec: spec}); err != nil {
		t.Fatal(err)
	}
	if owned, err := s.receiptBook().List("share"); err != nil || len(owned) != 0 {
		t.Fatalf("aborted upload still recorded: %+v %v", owned, err)
	}
	got, err := os.ReadFile(decoy)
	if err != nil || string(got) != "foreign-temp" {
		t.Fatalf("foreign temp changed: %q %v", got, err)
	}
	meta.CreatedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := writeFileAtomic(filepath.Join(s.Dir, meta.ID+".json"), mustJSON(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := s.Prune(context.Background(), time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("retention = %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, meta.ID+".tgbk")); !os.IsNotExist(err) {
		t.Fatalf("local archive remained: %v", err)
	}
	if err := s.DeleteDestination("share"); err != nil {
		t.Fatal(err)
	}
}

func runStoreCopyChild() {
	dir := os.Getenv("TOGAVSU_COPY_DIR")
	created, err := time.Parse(time.RFC3339Nano, os.Getenv("TOGAVSU_COPY_CREATED"))
	if err != nil {
		os.Exit(2)
	}
	s := &Store{
		Dir:        dir,
		Passphrase: "synthetic-passphrase",
		Book:       &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(dir, "destinations.json")},
		Now:        func() time.Time { return created },
	}
	dest := &PathDestination{
		Spec: DestinationSpec{
			ID: "share", Kind: "path",
			Path: os.Getenv("TOGAVSU_COPY_ROOT"), Namespace: os.Getenv("TOGAVSU_COPY_NS"),
		},
		stall: func(step string) {
			if step == "copy" {
				_ = os.WriteFile(os.Getenv("TOGAVSU_COPY_READY"), []byte("ok"), 0o600)
				time.Sleep(time.Minute)
			}
		},
	}
	err = s.copyOne(context.Background(), dest, BackupMeta{
		ID: os.Getenv("TOGAVSU_COPY_ID"), SHA256: os.Getenv("TOGAVSU_COPY_SHA"), CreatedAt: created,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runPathHoldChild() {
	f, err := os.OpenFile(os.Getenv("TOGAVSU_PATH_FILE"), os.O_RDWR, 0)
	if err != nil {
		os.Exit(2)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("TOGAVSU_PATH_READY"), []byte("ok"), 0o600); err != nil {
		os.Exit(4)
	}
	time.Sleep(time.Minute)
}
