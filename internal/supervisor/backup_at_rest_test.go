package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestOtherProcessWaitsForTheLiveScratchWorkspace uses two Store values in
// this process. Each opens its own lock descriptor. It does not spawn an OS child.
func TestOtherProcessWaitsForTheLiveScratchWorkspace(t *testing.T) {
	s := testStore(t)
	work, cleanup, err := s.plaintextWorkspace("staging-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "dump.sql"), []byte("LIVE"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.lockScratch()
	if err != nil {
		t.Fatal(err)
	}
	other := &Store{Dir: s.Dir}
	done := make(chan error, 1)
	go func() {
		done <- other.RecoverPlaintext()
	}()
	select {
	case err := <-done:
		unlock()
		cleanup()
		t.Fatalf("recovery removed a locked workspace: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(work, "dump.sql")); err != nil {
		unlock()
		t.Fatalf("live workspace was removed: %v", err)
	}
	cleanup()
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not finish after the workspace lock was released")
	}
}

func TestPlaintextWorkspacesStayOffTheBackupVolume(t *testing.T) {
	s := testStore(t)
	s.Dump = func(ctx context.Context, w io.Writer) error {
		entries, err := os.ReadDir(s.Dir)
		if err != nil {
			return err
		}
		for _, ent := range entries {
			if strings.HasPrefix(ent.Name(), "staging-") || strings.HasPrefix(ent.Name(), "restore-") {
				return fmt.Errorf("plaintext workspace %s is on the backup volume", ent.Name())
			}
		}
		_, err = io.WriteString(w, "COPY core.synthetic_items (id) FROM stdin;\n1\n2\n3\n\\.\n")
		return err
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Validate(meta.ID, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), "staging-") || strings.HasPrefix(ent.Name(), "restore-") {
			t.Fatalf("workspace left on the volume: %s", ent.Name())
		}
	}
	root, err := s.scratchRoot()
	if err != nil {
		t.Fatal(err)
	}
	if dirContains(s.Dir, root) || dirContains(root, s.Dir) {
		t.Fatalf("scratch %s shares backup %s", root, s.Dir)
	}
	s.Scratch = filepath.Join(s.Dir, "scratch")
	if _, _, err := s.plaintextWorkspace("staging-"); err == nil {
		t.Fatal("scratch inside the backup volume was accepted")
	}
}

func TestAbandonedPlaintextIsRemovedOnReopenAndRetention(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	plant := func(dir, name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Join(path, "db"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "db", "dump.sql"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	volumeStaging := plant(s.Dir, "staging-killed", "PLAINTEXT-DUMP-ROW")
	volumeRestore := plant(s.Dir, "restore-killed", "PLAINTEXT-RESTORE-ROW")
	root, err := s.scratchRoot()
	if err != nil {
		t.Fatal(err)
	}
	work := plant(root, "staging-killed", "SCRATCH-PLAINTEXT-ROW")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep-outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.Dir, "staging-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(s.Dir, meta.ID+".tgbk")
	before, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}

	reopened := &Store{Dir: s.Dir, Passphrase: s.Passphrase, Dump: s.Dump, Now: s.Now}
	ops := &Operations{Store: reopened, Gate: &Gate{}}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{volumeStaging, volumeRestore, work} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("abandoned workspace %s remained: %v", path, err)
		}
	}
	live, liveCleanup, err := reopened.plaintextWorkspace("restore-")
	if err != nil {
		t.Fatal(err)
	}
	defer liveCleanup()
	if err := os.WriteFile(filepath.Join(live, "dump.sql"), []byte("LIVE-RESTORE-ROW"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reopened.RecoverPlaintext(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(live, "dump.sql")); err != nil {
		t.Fatalf("live workspace was removed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(outside, "keep"))
	if err != nil || string(got) != "keep-outside" {
		t.Fatalf("symlink target changed: %q %v", got, err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink was removed: %v", err)
	}
	after, err := os.ReadFile(archive)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("archive changed: %v", err)
	}
	if _, err := reopened.Validate(meta.ID, s.Passphrase); err != nil {
		t.Fatal(err)
	}

	again := plant(s.Dir, "staging-pruned", "PRUNE-PLAINTEXT-ROW")
	if _, err := reopened.Prune(context.Background(), 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(again); !os.IsNotExist(err) {
		t.Fatalf("retention left %s: %v", again, err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("retention removed the archive: %v", err)
	}
}

func TestSealedCredentialReopensAndCopies(t *testing.T) {
	secret := "s3-credential-plaintext-9f3a"
	legacy := "legacy-plaintext-secret-22c1"
	var stored []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			stored = append([]byte(nil), body...)
			if !strings.Contains(r.Header.Get("Authorization"), "Credential=AKIAREOPENEXAMPLE/") {
				http.Error(w, "unsigned", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet {
			if stored == nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(stored)
			return
		}
		if r.Method == http.MethodDelete {
			stored = nil
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := testStore(t)
	key := testInstallerKey()
	bookPath := filepath.Join(s.Dir, "destinations.json")
	book := &DestinationBook{Path: bookPath, Key: key}
	if _, err := book.Put(DestinationSpec{
		ID: "share", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKIAREOPENEXAMPLE", SecretKey: secret, RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(bookPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, key) || !bytes.Contains(raw, []byte(destSealPrefix)) {
		t.Fatalf("persisted state:\n%s", raw)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if strings.Contains(strings.ToLower(ent.Name()), "key") {
			t.Fatalf("key file %s was written beside the ciphertext", ent.Name())
		}
	}
	if _, err := (&DestinationBook{Path: bookPath}).Put(DestinationSpec{
		ID: "other", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKIA", SecretKey: "another-secret",
	}); err == nil {
		t.Fatal("secret was saved without the installer key")
	}

	var specs []DestinationSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		t.Fatal(err)
	}
	specs = append(specs, DestinationSpec{
		ID: "old", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKIAPLAINTEXT", SecretKey: legacy, RetentionDays: 7,
		Namespace: "nslegacy1",
	})
	mixed, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bookPath, append(mixed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened := &DestinationBook{Path: bookPath, Key: append([]byte(nil), key...)}
	got, err := reopened.Specs()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, spec := range got {
		found[spec.ID] = spec.SecretKey
	}
	if found["share"] != secret || found["old"] != legacy {
		t.Fatalf("reopened secrets %+v", found)
	}
	raw, err = os.ReadFile(bookPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte(legacy)) || bytes.Contains(raw, key) {
		t.Fatalf("migration left a credential:\n%s", raw)
	}
	badKey := testInstallerKey()
	badKey[0] ^= 0xff
	if _, err := (&DestinationBook{Path: bookPath, Key: badKey}).Specs(); err == nil {
		t.Fatal("wrong installer key opened the credentials")
	}
	if err := reopened.Delete("old"); err != nil {
		t.Fatal(err)
	}

	s2 := &Store{
		Dir: s.Dir, Passphrase: s.Passphrase, Dump: s.Dump, Now: s.Now, Book: reopened,
		OpenDestination: func(spec DestinationSpec) (Destination, error) {
			if spec.SecretKey != secret {
				return nil, fmt.Errorf("reopened secret %q", spec.SecretKey)
			}
			return &S3Destination{Spec: spec, HTTP: srv.Client(), Now: s.Now}, nil
		},
	}
	copied, err := s2.Create(context.Background(), reasonManual)
	if err != nil || len(copied.DestinationErrors) != 0 {
		t.Fatalf("copy after reopen: %+v %v", copied.DestinationErrors, err)
	}
	if len(stored) == 0 {
		t.Fatal("reopened credential did not upload")
	}
}

func TestSFTPIdentityRemainsAFileReference(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "id_ed25519")
	material := "-----BEGIN OPENSSH PRIVATE KEY-----\nsynthetic-identity-material\n"
	if err := os.WriteFile(keyFile, []byte(material), 0o600); err != nil {
		t.Fatal(err)
	}
	known := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(known, []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	book := &DestinationBook{Path: filepath.Join(dir, "destinations.json"), Key: testInstallerKey()}
	if _, err := book.Put(DestinationSpec{
		ID: "share", Kind: "sftp", Host: "files.example", User: "backup",
		IdentityFile: keyFile, KnownHosts: known, RemotePath: "/backups",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(book.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("synthetic-identity-material")) {
		t.Fatalf("identity material was copied: %s", raw)
	}
	if !bytes.Contains(raw, []byte(keyFile)) {
		t.Fatalf("identity path missing: %s", raw)
	}
	got, err := (&DestinationBook{Path: book.Path, Key: testInstallerKey()}).Specs()
	if err != nil || len(got) != 1 || got[0].IdentityFile != keyFile {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestMissingDestinationParentCleansUpAfterRepair(t *testing.T) {
	s := testStore(t)
	parent := filepath.Join(t.TempDir(), "absent-parent")
	root := filepath.Join(parent, "dest")
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	openErr := error(nil)
	if _, err := openNoFollowDir(root); err == nil || ambiguousRemote(err) || !os.IsNotExist(err) {
		openErr = err
		t.Fatalf("missing parent classified as uncertain: %v", openErr)
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil || len(meta.DestinationErrors) != 1 {
		t.Fatalf("copy %+v %v", meta.DestinationErrors, err)
	}
	if strings.Contains(meta.DestinationErrors[0], "uncertain") {
		t.Fatalf("pre-publication failure was uncertain: %s", meta.DestinationErrors[0])
	}
	owned, err := s.receiptBook().List("share")
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range owned {
		if obj.State == receiptUncertain || obj.State == receiptPending {
			t.Fatalf("pre-publication receipt %+v", owned)
		}
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.lookup(meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete after repair: %v", err)
	}
	if err := s.DeleteDestination("share"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: root, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	copied, err := s.Create(context.Background(), reasonManual)
	if err != nil || len(copied.DestinationErrors) != 0 {
		t.Fatalf("copy after repair: %+v %v", copied.DestinationErrors, err)
	}
	s.Now = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	n, err := s.PruneDestinations(context.Background())
	if err != nil || n == 0 {
		t.Fatalf("prune after repair removed %d: %v", n, err)
	}
	if err := s.DeleteDestination("share"); err != nil {
		t.Fatal(err)
	}
}

func TestUncertainAbsenceStillBlocksCleanup(t *testing.T) {
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{ID: "share", Kind: "path", Path: destDir, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	name := meta.ID + ".tgbk"
	if err := s.receiptBook().Add(OwnedObject{
		DestinationID: "share", Name: name, Created: meta.CreatedAt, State: receiptUncertain, Digest: meta.SHA256,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), meta.ID); err == nil {
		t.Fatal("absent uncertain object was deleted")
	}
	if _, err := s.lookup(meta.ID); err != nil {
		t.Fatalf("local archive was removed: %v", err)
	}
	if err := s.DeleteDestination("share"); !errors.Is(err, errDestinationRemoval) {
		t.Fatalf("destination removal: %v", err)
	}
	s.Now = func() time.Time { return meta.CreatedAt.Add(48 * time.Hour) }
	if n, err := s.Prune(context.Background(), time.Hour); err != nil || n != 0 {
		t.Fatalf("retention removed %d while the upload was uncertain: %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, meta.ID+".tgbk")); err != nil {
		t.Fatal(err)
	}
}

func TestPrePublicationFailuresStayDefinite(t *testing.T) {
	for _, err := range []error{
		syscall.ENOENT,
		syscall.ENOTDIR,
		syscall.EACCES,
		fmt.Errorf("open: %w", syscall.ENOENT),
		&os.PathError{Op: "open", Path: "/var/missing", Err: syscall.ENOENT},
		beforePublication(fmt.Errorf("sftp: exit status 1: dest open \"/backups\": No such file or directory")),
		beforePublication(fmt.Errorf("sftp: %w", os.ErrNotExist)),
	} {
		if ambiguousRemote(err) || !definiteRejection(err) {
			t.Fatalf("%v was not a completed pre-publication failure", err)
		}
	}
	for _, err := range []error{
		syscall.ECONNRESET,
		syscall.EPIPE,
		syscall.ETIMEDOUT,
		context.DeadlineExceeded,
		&os.PathError{Op: "read", Path: "socket", Err: syscall.ECONNRESET},
		beforePublication(fmt.Errorf("sftp: %w: killed", context.DeadlineExceeded)),
		execExit(),
		fmt.Errorf("sftp: %w: Connection reset by peer", execExit()),
	} {
		if !ambiguousRemote(err) || definiteRejection(err) {
			t.Fatalf("%v lost uncertain ownership", err)
		}
	}
}

func TestSFTPMissingRemoteIsRejectedThenCleaned(t *testing.T) {
	dir := t.TempDir()
	mode := filepath.Join(dir, "mode")
	if err := os.WriteFile(mode, []byte("missing"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "sftp")
	body := "#!/bin/sh\nprev=\nbatch=\nfor arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-b\" ]; then batch=$arg; fi\n  prev=$arg\ndone\n" +
		"mode=$(cat " + mode + ")\n" +
		"while IFS= read -r line; do\n  case \"$line\" in\n" +
		"    put*)\n" +
		"      if [ \"$mode\" = missing ]; then echo 'dest open \"/backups\": No such file or directory' >&2; exit 1; fi\n" +
		"      exit 0 ;;\n" +
		"    get*)\n" +
		"      if [ \"$mode\" = missing ]; then echo 'No such file or directory' >&2; exit 1; fi\n" +
		"      exit 0 ;;\n" +
		"    rm*) exit 0 ;;\n" +
		"    ls*) exit 0 ;;\n" +
		"  esac\ndone < \"$batch\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{
		ID: "share", Kind: "sftp", Host: "files.example", User: "backup", Port: 22,
		IdentityFile: filepath.Join(dir, "id"), KnownHosts: filepath.Join(dir, "known"),
		RemotePath: "/backups", RetentionDays: 1,
	}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		return &SFTPDestination{Spec: spec, Command: script}, nil
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil || len(meta.DestinationErrors) != 1 {
		t.Fatalf("%+v %v", meta.DestinationErrors, err)
	}
	if strings.Contains(strings.ToLower(meta.DestinationErrors[0]), "uncertain") {
		t.Fatalf("missing remote path stayed uncertain: %s", meta.DestinationErrors[0])
	}
	owned, err := s.receiptBook().List("share")
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range owned {
		if obj.State == receiptUncertain || obj.State == receiptPending {
			t.Fatalf("receipt %+v", owned)
		}
	}
	if err := os.WriteFile(mode, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.lookup(meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("metadata remained: %v", err)
	}
	if err := s.DeleteDestination("share"); err != nil {
		t.Fatal(err)
	}
}

func execExit() error {
	err := errors.New("exit status 1")
	return err
}
