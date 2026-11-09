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
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPathCleanStageCrash(t *testing.T) {
	if os.Getenv("TOGAVSU_STAGE_CHILD") == "1" {
		runStageChild()
		return
	}
	t.Run("second copy", func(t *testing.T) { testCleanCrash(t, "clean-copy") })
	t.Run("before rename", func(t *testing.T) { testCleanCrash(t, "clean-rename") })
}

func testCleanCrash(t *testing.T, mode string) {
	t.Helper()
	s, root, spec, meta := stageBackup(t)
	decoy := filepath.Join(root, spec.Namespace, ".togavsu-ffffffffffffffffffffffffffffffff.tmp")
	if err := os.WriteFile(decoy, []byte("foreign-temp"), 0o600); err != nil {
		t.Fatal(err)
	}
	killStageChild(t, s, root, spec, meta, mode, true, false)
	owned, err := s.receiptBook().List("share")
	if err != nil {
		t.Fatal(err)
	}
	var staged OwnedObject
	for _, obj := range owned {
		if obj.Clean != "" && obj.CleanInode != 0 && obj.Temp != "" {
			staged = obj
		}
	}
	if staged.Clean == "" {
		t.Fatalf("clean publication was not journaled before the kill: %+v", owned)
	}
	ns := filepath.Join(root, spec.Namespace)
	info, err := os.Stat(filepath.Join(ns, staged.Clean))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= int64(tempTokenPrefixBytes) {
		t.Fatalf("clean file is not payload-sized: %d", info.Size())
	}
	if err := s.reconcileDestination(context.Background(), &PathDestination{Spec: spec, noXattr: true}); err != nil {
		t.Fatal(err)
	}
	assertNoStagedPayload(t, ns, decoy)
	if _, err := os.Stat(filepath.Join(ns, meta.ID+".tgbk")); !os.IsNotExist(err) {
		t.Fatal("unpublished archive appeared")
	}
	got, err := os.ReadFile(decoy)
	if err != nil || string(got) != "foreign-temp" {
		t.Fatalf("foreign temp changed: %q %v", got, err)
	}
}

func TestPathSidecarCrash(t *testing.T) {
	if os.Getenv("TOGAVSU_STAGE_CHILD") == "1" {
		runStageChild()
		return
	}
	t.Run("original upload", func(t *testing.T) { testSidecarOriginal(t, false) })
	t.Run("original no-xattr clean copy", func(t *testing.T) { testSidecarOriginal(t, true) })
	t.Run("repair upload", func(t *testing.T) { testSidecarRepair(t, false) })
	t.Run("repair no-xattr", func(t *testing.T) { testSidecarRepair(t, true) })
}

func testSidecarOriginal(t *testing.T, noXattr bool) {
	t.Helper()
	s, root, spec, meta := stageBackup(t)
	mode := "sidecar"
	if noXattr {
		mode = "sidecar-clean"
	}
	killStageChild(t, s, root, spec, meta, mode, noXattr, false)
	owned, err := s.receiptBook().List("share")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, obj := range owned {
		if obj.Name == meta.ID+".sha256" && obj.Temp != "" && obj.TempInode != 0 && obj.TempToken != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sidecar temp was not journaled: %+v", owned)
	}
	if err := s.reconcileDestination(context.Background(), &PathDestination{Spec: spec, noXattr: noXattr}); err != nil {
		t.Fatal(err)
	}
	ns := filepath.Join(root, spec.Namespace)
	assertNoStagedPayload(t, ns, "")
	assertPublishedPair(t, s, ns, meta)
}

func testSidecarRepair(t *testing.T, noXattr bool) {
	t.Helper()
	s, root, spec, meta := stageBackup(t)
	dest := &PathDestination{Spec: spec, noXattr: noXattr}
	if err := s.copyOne(context.Background(), dest, meta); err != nil {
		t.Fatal(err)
	}
	if err := s.receiptBook().Add(OwnedObject{
		DestinationID: "share", Name: meta.ID + ".tgbk", Created: meta.CreatedAt,
		State: receiptPending, Digest: meta.SHA256,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.receiptBook().Remove("share", meta.ID+".sha256"); err != nil {
		t.Fatal(err)
	}
	ns := filepath.Join(root, spec.Namespace)
	if err := os.Remove(filepath.Join(ns, meta.ID+".sha256")); err != nil {
		t.Fatal(err)
	}
	mode := "repair"
	if noXattr {
		mode = "repair-clean"
	}
	killStageChild(t, s, root, spec, meta, mode, noXattr, false)
	owned, err := s.receiptBook().List("share")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, obj := range owned {
		if obj.Name == meta.ID+".sha256" && (obj.Temp != "" || obj.Clean != "") {
			found = true
		}
	}
	if !found {
		t.Fatalf("repair sidecar was not journaled: %+v", owned)
	}
	if err := s.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	assertNoStagedPayload(t, ns, "")
	assertPublishedPair(t, s, ns, meta)
}

func TestPathQuarantineStage(t *testing.T) {
	if os.Getenv("TOGAVSU_STAGE_CHILD") == "1" {
		runStageChild()
		return
	}
	t.Run("after temp rename", func(t *testing.T) { testQuarantineOwned(t, "temp") })
	t.Run("after anchor rename", func(t *testing.T) { testQuarantineOwned(t, "link") })
	t.Run("foreign restored then second pass", func(t *testing.T) { testForeignSecondPass(t, false) })
	t.Run("occupied name keeps foreign and anchor until second pass", func(t *testing.T) { testForeignSecondPass(t, true) })
}

func testQuarantineOwned(t *testing.T, which string) {
	t.Helper()
	root, book, obj := quarantineFixture(t)
	mode := "quarantine-temp"
	if which == "link" {
		mode = "quarantine-link"
	}
	killQuarantineChild(t, root, book, mode)
	ns := filepath.Join(root, "nsownedtmp")
	held := quarantineFile(t, ns, obj, which)
	body, err := os.ReadFile(held)
	if err != nil || string(body) != "owned-bytes" {
		t.Fatalf("quarantine file %s: %q %v", held, body, err)
	}
	if _, err := os.Stat(filepath.Join(ns, obj.Temp)); which == "temp" && !os.IsNotExist(err) {
		t.Fatalf("source name still present after the rename: %v", err)
	}
	owned := pathReceipts(t, book)
	if len(owned) != 1 || (which == "temp" && owned[0].TempQuarantine == "") || (which == "link" && owned[0].LinkQuarantine == "") {
		t.Fatalf("quarantine name was not journaled: %+v", owned)
	}
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(held); !os.IsNotExist(err) {
		t.Fatalf("owned quarantine remained: %v", err)
	}
	assertNoStagedPayload(t, ns, "")
}

func testForeignSecondPass(t *testing.T, occupy bool) {
	t.Helper()
	root, book, obj := quarantineFixture(t)
	ns := filepath.Join(root, "nsownedtmp")
	path := filepath.Join(ns, obj.Temp)
	aside := filepath.Join(ns, "owned-aside")
	if err := os.Rename(path, aside); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("foreign-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	killQuarantineChild(t, root, book, "quarantine-temp")
	if occupy {
		if err := os.WriteFile(path, []byte("new-occupant"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(ns, obj.TempLink)
	if _, err := os.Stat(anchor); err != nil {
		t.Fatalf("first recovery removed the anchor: %v", err)
	}
	if occupy {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "new-occupant" {
			t.Fatalf("occupant: %q %v", got, err)
		}
		hold := quarantineFile(t, ns, obj, "temp")
		got, err = os.ReadFile(hold)
		if err != nil || string(got) != "foreign-bytes" {
			t.Fatalf("parked foreign: %q %v", got, err)
		}
		kept := pathReceipts(t, book)
		if len(kept) != 1 || kept[0].TempHold == "" || !kept[0].TempConflict || kept[0].TempLink == "" {
			t.Fatalf("hold evidence: %+v", kept)
		}
	} else {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "foreign-bytes" {
			t.Fatalf("restored foreign: %q %v", got, err)
		}
	}
	got, err := os.ReadFile(aside)
	if err != nil || string(got) != "owned-bytes" {
		t.Fatalf("owned inode: %q %v", got, err)
	}
	if err := pathReconcile(t, root, book); err != nil {
		t.Fatal(err)
	}
	if occupy {
		got, err = os.ReadFile(path)
		if err != nil || string(got) != "new-occupant" {
			t.Fatalf("second pass overwrote the occupant: %q %v", got, err)
		}
		hold := quarantineFile(t, ns, obj, "temp")
		got, err = os.ReadFile(hold)
		if err != nil || string(got) != "foreign-bytes" {
			t.Fatalf("second pass lost the parked file: %q %v", got, err)
		}
		kept := pathReceipts(t, book)
		if len(kept) != 1 || kept[0].TempHold == "" {
			t.Fatalf("second pass discarded hold evidence: %+v", kept)
		}
	} else {
		got, err = os.ReadFile(path)
		if err != nil || string(got) != "foreign-bytes" {
			t.Fatalf("second pass changed the foreign file: %q %v", got, err)
		}
		if _, err := os.Stat(anchor); !os.IsNotExist(err) {
			t.Fatalf("second pass left the anchor: %v", err)
		}
	}
	got, err = os.ReadFile(aside)
	if err != nil || string(got) != "owned-bytes" {
		t.Fatalf("second pass changed the saved inode: %q %v", got, err)
	}
}

func TestPathPublishDurability(t *testing.T) {
	if os.Getenv("TOGAVSU_STAGE_CHILD") == "1" {
		runStageChild()
		return
	}
	t.Run("sync failure keeps identities", func(t *testing.T) { testPublishSyncFailure(t) })
	t.Run("crash before deletion durability", func(t *testing.T) { testPublishCrash(t) })
	t.Run("lost ack stays published", func(t *testing.T) { testLostAckSuccess(t, false) })
	t.Run("lost ack no-xattr", func(t *testing.T) { testLostAckSuccess(t, true) })
}

func testPublishSyncFailure(t *testing.T) {
	t.Helper()
	s, root, spec, meta := stageBackup(t)
	failDelete := true
	dest := &PathDestination{Spec: spec, noXattr: true, dirSync: func(step string) error {
		if failDelete && step == "delete" {
			return errors.New("injected directory sync failure")
		}
		return nil
	}}
	err := s.copyOne(context.Background(), dest, meta)
	if !errors.Is(err, ErrRemoteUncertain) {
		t.Fatalf("post-publish cleanup error: %v", err)
	}
	ns := filepath.Join(root, spec.Namespace)
	body, err := os.ReadFile(filepath.Join(ns, meta.ID+".tgbk"))
	if err != nil || bytes.Contains(body, []byte("TGVTMP1")) {
		t.Fatalf("final archive after failed cleanup: %v prefix %v", err, bytes.Contains(body, []byte("TGVTMP1")))
	}
	owned, err := s.receiptBook().List("share")
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, obj := range owned {
		if obj.Name == meta.ID+".tgbk" && (obj.Temp != "" || obj.Clean != "" || obj.TempLink != "") {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("publication released identities after a failed delete sync: %+v", owned)
	}
	failDelete = false
	if err := s.reconcileDestination(context.Background(), &PathDestination{Spec: spec, noXattr: true}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(filepath.Join(ns, meta.ID+".tgbk"))
	if err != nil || bytes.Contains(body, []byte("TGVTMP1")) {
		t.Fatal("retry removed the published archive")
	}
	assertNoStagedPayload(t, ns, "")
}

func testPublishCrash(t *testing.T) {
	t.Helper()
	s, root, spec, meta := stageBackup(t)
	killStageChild(t, s, root, spec, meta, "publish-held", true, false)
	ns := filepath.Join(root, spec.Namespace)
	body, err := os.ReadFile(filepath.Join(ns, meta.ID+".tgbk"))
	if err != nil || bytes.Contains(body, []byte("TGVTMP1")) {
		t.Fatalf("published bytes: %v", err)
	}
	if err := s.reconcileDestination(context.Background(), &PathDestination{Spec: spec, noXattr: true}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(filepath.Join(ns, meta.ID+".tgbk"))
	if err != nil || bytes.Contains(body, []byte("TGVTMP1")) {
		t.Fatal("reconciliation removed the published archive")
	}
	assertNoStagedPayload(t, ns, "")
	assertPublishedPair(t, s, ns, meta)
}

func testLostAckSuccess(t *testing.T, noXattr bool) {
	t.Helper()
	s, root, spec, meta := stageBackup(t)
	fileSyncs := 0
	dest := &PathDestination{Spec: spec, noXattr: noXattr, dirSync: func(step string) error {
		if step == "file" {
			fileSyncs++
			if fileSyncs == 1 {
				return fmt.Errorf("%w: directory sync", ErrRemoteUncertain)
			}
		}
		return nil
	}}
	err := s.copyOne(context.Background(), dest, meta)
	if !errors.Is(err, ErrRemoteUncertain) {
		t.Fatalf("lost ack: %v", err)
	}
	ns := filepath.Join(root, spec.Namespace)
	raw, err := os.ReadFile(filepath.Join(s.Dir, meta.ID+".tgbk"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(ns, meta.ID+".tgbk"))
	if err != nil || !bytes.Equal(body, raw) {
		t.Fatalf("published archive mismatch: %v", err)
	}
	if err := s.reconcileDestination(context.Background(), &PathDestination{Spec: spec, noXattr: noXattr}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(filepath.Join(ns, meta.ID+".tgbk"))
	if err != nil || !bytes.Equal(body, raw) || bytes.Contains(body, []byte("TGVTMP1")) {
		t.Fatal("lost acknowledgement was not kept as success")
	}
	assertPublishedPair(t, s, ns, meta)
}

func TestPathAbortProof(t *testing.T) {
	if os.Getenv("TOGAVSU_STAGE_CHILD") == "1" {
		runStageChild()
		return
	}
	s, root, spec, meta := stageBackup(t)
	killStageChild(t, s, root, spec, meta, "copy", false, false)
	flaky := &flakyPathGet{PathDestination: &PathDestination{Spec: spec}, fails: 1}
	if err := s.reconcileDestination(context.Background(), flaky); err != nil {
		t.Fatal(err)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || len(owned) == 0 {
		t.Fatalf("transient read dropped the receipt: %+v %v", owned, err)
	}
	proof := false
	for _, obj := range owned {
		if obj.CleanupVerified && (obj.Temp != "" || obj.TempToken != "") && obj.State != receiptAborted {
			proof = true
		}
		if obj.Temp == "" && obj.TempToken == "" && !obj.CleanupVerified && (obj.State == receiptPending || obj.State == receiptUncertain) {
			t.Fatalf("pending row survived without cleanup proof: %+v", obj)
		}
	}
	if !proof {
		t.Fatalf("cleanup proof was not durable across the failed read: %+v", owned)
	}
	restarted := &Store{Dir: s.Dir, Book: s.Book, Passphrase: s.Passphrase, Now: s.Now}
	if err := restarted.reconcileDestination(context.Background(), &PathDestination{Spec: spec}); err != nil {
		t.Fatal(err)
	}
	if owned, err = restarted.receiptBook().List("share"); err != nil || len(owned) != 0 {
		t.Fatalf("restart left the aborted upload: %+v %v", owned, err)
	}
	if _, err := os.Stat(filepath.Join(root, spec.Namespace, meta.ID+".tgbk")); !os.IsNotExist(err) {
		t.Fatal("final object appeared after the aborted copy")
	}
	if restarted.remoteRecoveryOpen(meta.ID) {
		t.Fatal("local retention is still blocked")
	}
	if err := restarted.deleteRemotes(context.Background(), meta.ID); err != nil {
		t.Fatalf("deletion: %v", err)
	}
	meta.CreatedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := writeFileAtomic(filepath.Join(s.Dir, meta.ID+".json"), mustJSON(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := restarted.Prune(context.Background(), time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("retention = %d %v", n, err)
	}
	if err := restarted.DeleteDestination("share"); err != nil {
		t.Fatal(err)
	}
}

func TestPathNoHardlink(t *testing.T) {
	if os.Getenv("TOGAVSU_STAGE_CHILD") == "1" {
		runStageChild()
		return
	}
	t.Run("success", func(t *testing.T) {
		s, root, spec, meta := stageBackup(t)
		dest := &PathDestination{Spec: spec, noXattr: true, noHardlink: true}
		if err := s.copyOne(context.Background(), dest, meta); err != nil {
			t.Fatal(err)
		}
		ns := filepath.Join(root, spec.Namespace)
		assertPublishedPair(t, s, ns, meta)
		assertNoStagedPayload(t, ns, "")
		entries, err := os.ReadDir(ns)
		if err != nil {
			t.Fatal(err)
		}
		for _, ent := range entries {
			if strings.HasSuffix(ent.Name(), ".own") {
				t.Fatalf("hardlink anchor on a no-hardlink share: %s", ent.Name())
			}
		}
	})
	t.Run("interrupted", func(t *testing.T) {
		s, root, spec, meta := stageBackup(t)
		killStageChild(t, s, root, spec, meta, "clean-copy", true, true)
		if err := s.reconcileDestination(context.Background(), &PathDestination{Spec: spec, noXattr: true, noHardlink: true}); err != nil {
			t.Fatal(err)
		}
		assertNoStagedPayload(t, filepath.Join(root, spec.Namespace), "")
	})
}

func TestPathPrejournalResidual(t *testing.T) {
	if os.Getenv("TOGAVSU_STAGE_CHILD") == "1" {
		runStageChild()
		return
	}
	token := "0123456789abcdef0123456789abcdef"
	if len(tempPrefix(token)) != tempTokenPrefixBytes {
		t.Fatalf("prefix length %d", len(tempPrefix(token)))
	}
	t.Run("source gap", func(t *testing.T) {
		s, root, spec, meta := stageBackup(t)
		killStageChild(t, s, root, spec, meta, "journal", true, false)
		ns := filepath.Join(root, spec.Namespace)
		owned, err := s.receiptBook().List("share")
		if err != nil {
			t.Fatal(err)
		}
		for _, obj := range owned {
			if obj.Temp != "" || obj.Clean != "" {
				t.Fatalf("pre-journal kill recorded an identity: %+v", obj)
			}
		}
		entries, err := os.ReadDir(ns)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		var ino uint64
		for _, ent := range entries {
			if !strings.HasPrefix(ent.Name(), ".togavsu-") {
				continue
			}
			n++
			info, err := ent.Info()
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() > int64(tempTokenPrefixBytes) {
				t.Fatalf("pre-journal file %s holds payload (%d)", ent.Name(), info.Size())
			}
			var st syscall.Stat_t
			if err := syscall.Stat(filepath.Join(ns, ent.Name()), &st); err != nil {
				t.Fatal(err)
			}
			if ino == 0 {
				ino = st.Ino
			} else if st.Ino != ino {
				t.Fatalf("pre-journal names are not one inode")
			}
		}
		if n == 0 || n > 2 {
			t.Fatalf("pre-journal directory entries = %d", n)
		}
	})
	t.Run("clean gap", func(t *testing.T) {
		s, root, spec, meta := stageBackup(t)
		killStageChild(t, s, root, spec, meta, "clean-create", true, false)
		owned, err := s.receiptBook().List("share")
		if err != nil {
			t.Fatal(err)
		}
		var staged OwnedObject
		for _, obj := range owned {
			if obj.Temp != "" {
				staged = obj
			}
			if obj.Clean != "" {
				t.Fatalf("clean file was journaled before clean-create returned: %+v", obj)
			}
		}
		if staged.Temp == "" {
			t.Fatal("source temp was not journaled")
		}
		ns := filepath.Join(root, spec.Namespace)
		entries, err := os.ReadDir(ns)
		if err != nil {
			t.Fatal(err)
		}
		extra := 0
		for _, ent := range entries {
			if ent.Name() == staged.Temp || ent.Name() == staged.TempLink {
				continue
			}
			info, err := ent.Info()
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() != 0 {
				t.Fatalf("unjournaled clean file %s is %d bytes", ent.Name(), info.Size())
			}
			extra++
		}
		if extra != 1 {
			t.Fatalf("unjournaled clean files = %d", extra)
		}
		if err := s.reconcileDestination(context.Background(), &PathDestination{Spec: spec, noXattr: true}); err != nil {
			t.Fatal(err)
		}
		left := 0
		entries, err = os.ReadDir(ns)
		if err != nil {
			t.Fatal(err)
		}
		for _, ent := range entries {
			info, err := ent.Info()
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() > int64(tempTokenPrefixBytes) {
				t.Fatalf("payload remained: %s", ent.Name())
			}
			if info.Size() == 0 {
				left++
			}
		}
		if left != 1 {
			t.Fatalf("clean residual count = %d", left)
		}
	})
}

type flakyPathGet struct {
	*PathDestination
	fails int
}

func (f *flakyPathGet) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	if f.fails > 0 {
		f.fails--
		return nil, ErrRemoteUncertain
	}
	return f.PathDestination.Get(ctx, name)
}

func (f *flakyPathGet) RemoveOwnedTemp(ctx context.Context, obj OwnedObject) error {
	return f.PathDestination.RemoveOwnedTemp(ctx, obj)
}

func stageBackup(t *testing.T) (s *Store, root string, spec DestinationSpec, meta BackupMeta) {
	t.Helper()
	s = testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(t.TempDir(), "share")
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	spec, err = s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, Namespace: "nsstagetmp", RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, spec.Namespace), 0o700); err != nil {
		t.Fatal(err)
	}
	return s, root, spec, meta
}

func killStageChild(t *testing.T, s *Store, root string, spec DestinationSpec, meta BackupMeta, mode string, noXattr, noHardlink bool) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPath(CleanStageCrash|SidecarCrash|PublishDurability|AbortProof|NoHardlink|PrejournalResidual)$", "-test.count=1")
	env := []string{
		"TOGAVSU_STAGE_CHILD=1",
		"TOGAVSU_STAGE_MODE=" + mode,
		"TOGAVSU_STAGE_DIR=" + s.Dir,
		"TOGAVSU_STAGE_ROOT=" + root,
		"TOGAVSU_STAGE_NS=" + spec.Namespace,
		"TOGAVSU_STAGE_ID=" + meta.ID,
		"TOGAVSU_STAGE_SHA=" + meta.SHA256,
		"TOGAVSU_STAGE_CREATED=" + meta.CreatedAt.Format(time.RFC3339Nano),
		"TOGAVSU_STAGE_READY=" + ready,
	}
	if noXattr {
		env = append(env, "TOGAVSU_NO_XATTR=1")
	}
	if noHardlink {
		env = append(env, "TOGAVSU_NO_HARDLINK=1")
	}
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitPathFile(t, ready)
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
}

func killQuarantineChild(t *testing.T, root, book, mode string) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPathQuarantineStage$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"TOGAVSU_STAGE_CHILD=1",
		"TOGAVSU_STAGE_MODE="+mode,
		"TOGAVSU_STAGE_ROOT="+root,
		"TOGAVSU_STAGE_RECEIPT="+book,
		"TOGAVSU_STAGE_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitPathFile(t, ready)
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
}

func quarantineFixture(t *testing.T) (root, book string, obj OwnedObject) {
	t.Helper()
	root, book = pathTempFixture(t)
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
	obj = OwnedObject{
		DestinationID: "share", Name: "backup.tgbk", State: receiptPending,
		Temp: name, TempInode: inode, TempToken: token, TempLink: link,
	}
	if err := (&ReceiptBook{Path: book}).Add(obj); err != nil {
		t.Fatal(err)
	}
	return root, book, obj
}

func quarantineFile(t *testing.T, ns string, obj OwnedObject, which string) string {
	t.Helper()
	name := obj.Temp
	if which == "link" {
		name = obj.TempLink
	}
	hold, err := quarantineName(name)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(ns, hold)
}

func assertNoStagedPayload(t *testing.T, ns, decoy string) {
	t.Helper()
	entries, err := os.ReadDir(ns)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if decoy != "" && filepath.Base(decoy) == ent.Name() {
			continue
		}
		if strings.HasSuffix(ent.Name(), ".tgbk") || strings.HasSuffix(ent.Name(), ".sha256") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > int64(tempTokenPrefixBytes) {
			t.Fatalf("staged payload remained: %s (%d)", ent.Name(), info.Size())
		}
	}
}

func assertPublishedPair(t *testing.T, s *Store, ns string, meta BackupMeta) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.Dir, meta.ID+".tgbk"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(ns, meta.ID+".tgbk"))
	if err != nil || !bytes.Equal(body, raw) || bytes.Contains(body, []byte("TGVTMP1")) {
		t.Fatalf("archive publication: %v", err)
	}
	side, err := os.ReadFile(filepath.Join(ns, meta.ID+".sha256"))
	if err != nil || !bytes.Contains(side, []byte(meta.SHA256)) || bytes.Contains(side, []byte("TGVTMP1")) {
		t.Fatalf("sidecar publication: %q %v", side, err)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range owned {
		if obj.hasStaging() || obj.State != "" {
			t.Fatalf("receipt not confirmed: %+v", obj)
		}
	}
}

func runStageChild() {
	mode := os.Getenv("TOGAVSU_STAGE_MODE")
	if mode == "quarantine-temp" || mode == "quarantine-link" {
		runQuarantineChild(mode)
		return
	}
	dir := os.Getenv("TOGAVSU_STAGE_DIR")
	created, err := time.Parse(time.RFC3339Nano, os.Getenv("TOGAVSU_STAGE_CREATED"))
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
			Path: os.Getenv("TOGAVSU_STAGE_ROOT"), Namespace: os.Getenv("TOGAVSU_STAGE_NS"),
		},
		noXattr:    os.Getenv("TOGAVSU_NO_XATTR") == "1",
		noHardlink: os.Getenv("TOGAVSU_NO_HARDLINK") == "1",
	}
	copies := 0
	cleanCopies := 0
	holds := 0
	dest.stall = func(step string) {
		ready := os.Getenv("TOGAVSU_STAGE_READY")
		switch mode {
		case "clean-copy":
			if step == "clean-copy" {
				stageReady(ready)
			}
		case "clean-rename":
			if step == "clean-rename" {
				stageReady(ready)
			}
		case "journal":
			if step == "journal" {
				stageReady(ready)
			}
		case "clean-create":
			if step == "clean-create" {
				stageReady(ready)
			}
		case "copy":
			if step == "copy" {
				stageReady(ready)
			}
		case "sidecar":
			if step == "copy" {
				copies++
				if copies == 2 {
					stageReady(ready)
				}
			}
		case "sidecar-clean":
			if step == "clean-copy" {
				cleanCopies++
				if cleanCopies == 2 {
					stageReady(ready)
				}
			}
		case "repair":
			if step == "copy" {
				stageReady(ready)
			}
		case "repair-clean":
			if step == "clean-copy" {
				stageReady(ready)
			}
		case "publish-held":
			if step == "quarantine-held" {
				holds++
				if holds == 1 {
					stageReady(ready)
				}
			}
		}
	}
	meta := BackupMeta{ID: os.Getenv("TOGAVSU_STAGE_ID"), SHA256: os.Getenv("TOGAVSU_STAGE_SHA"), CreatedAt: created}
	if mode == "repair" || mode == "repair-clean" {
		err = s.ensureValidSidecar(context.Background(), dest, s.receiptBook(), meta.ID, created)
	} else {
		err = s.copyOne(context.Background(), dest, meta)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runQuarantineChild(mode string) {
	root := os.Getenv("TOGAVSU_STAGE_ROOT")
	book := os.Getenv("TOGAVSU_STAGE_RECEIPT")
	dest := pathDest(root)
	ready := os.Getenv("TOGAVSU_STAGE_READY")
	holds := 0
	dest.stall = func(step string) {
		if step != "quarantine-held" {
			return
		}
		holds++
		if mode == "quarantine-temp" && holds == 1 {
			stageReady(ready)
		}
		if mode == "quarantine-link" && holds == 2 {
			stageReady(ready)
		}
	}
	owned, err := (&ReceiptBook{Path: book}).List("share")
	if err != nil || len(owned) != 1 {
		os.Exit(2)
	}
	err = dest.RemoveOwnedTemp(pathTempContext(book), owned[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func stageReady(path string) {
	_ = os.WriteFile(path, []byte("ok"), 0o600)
	time.Sleep(time.Minute)
}

func init() {
	if len(tempPrefix("0123456789abcdef0123456789abcdef")) != tempTokenPrefixBytes {
		panic("token prefix length drifted from tempTokenPrefixBytes")
	}
}
