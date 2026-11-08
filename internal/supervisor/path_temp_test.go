package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	t.Run("crash before rename", func(t *testing.T) { testPathCrashBeforeRename(t) })
	t.Run("rename lost acknowledgment", func(t *testing.T) { testPathRenameLostAck(t) })
	t.Run("success", func(t *testing.T) { testPathTempSuccess(t) })
	t.Run("foreign identity and retry", func(t *testing.T) { testPathForeignTemp(t) })
	t.Run("active other process", func(t *testing.T) { testPathActiveTemp(t) })
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
		"TOGAVSU_PATH_MODE=crash",
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
	if len(owned) != 1 || owned[0].Temp == "" || owned[0].TempInode == 0 {
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
	sum, err := sha256Hex(bytes.NewReader([]byte("first")))
	if err != nil {
		t.Fatal(err)
	}
	receipts := &ReceiptBook{Path: book}
	if err := receipts.Add(OwnedObject{
		DestinationID: "share", Name: "backup.tgbk", State: receiptPending,
		Temp: name, TempInode: info.Ino, TempMtime: statMtime(info), TempDigest: sum,
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
	var info syscall.Stat_t
	if err := syscall.Stat(path, &info); err != nil {
		t.Fatal(err)
	}
	receipts := &ReceiptBook{Path: book}
	if err := receipts.Add(OwnedObject{
		DestinationID: "share", Name: "backup.tgbk", State: receiptPending,
		Temp: name, TempInode: info.Ino, TempMtime: statMtime(info),
		TempDigest: mustSHA(t, []byte("active")),
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
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	time.Sleep(50 * time.Millisecond)
	if err := pathDest(root).RemoveOwnedTemp(context.Background(), name, info.Ino, statMtime(info), mustSHA(t, []byte("active"))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp remained after the holder exited: %v", err)
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

func mustSHA(t *testing.T, raw []byte) string {
	t.Helper()
	sum, err := sha256Hex(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return sum
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
	return WithPathTemp(context.Background(), func(temp string, inode uint64, mtime int64, digest string) error {
		return receipts.Add(OwnedObject{
			DestinationID: "share", Name: "backup.tgbk", State: receiptPending,
			Temp: temp, TempInode: inode, TempMtime: mtime, TempDigest: digest,
		})
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

func runPathUploadChild() {
	root := os.Getenv("TOGAVSU_PATH_DIR")
	book := os.Getenv("TOGAVSU_PATH_RECEIPT")
	ready := os.Getenv("TOGAVSU_PATH_READY")
	dest := pathDest(root)
	switch os.Getenv("TOGAVSU_PATH_MODE") {
	case "crash":
		dest.stall = func(step string) {
			if step == "hash" {
				_ = os.WriteFile(ready, []byte("ok"), 0o600)
				time.Sleep(time.Minute)
			}
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
