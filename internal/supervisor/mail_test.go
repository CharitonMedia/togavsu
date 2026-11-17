package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got := st.Mode().Perm()
	if got != want {
		t.Fatalf("%s mode %#o, want %#o", path, got, want)
	}
}

func assertOwnerWhenRoot(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Log("skip owner check: not running as root")
		return
	}
	var info syscall.Stat_t
	if err := syscall.Stat(path, &info); err != nil {
		t.Fatal(err)
	}
	if int(info.Uid) != CoreUID || int(info.Gid) != CoreGID {
		t.Fatalf("%s owner %d:%d, want %d:%d", path, info.Uid, info.Gid, CoreUID, CoreGID)
	}
}

func TestPrepareMailOutboxDirAndNotifyPutAllPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mail")
	// Simulate a Docker named volume: root-owned 0755 already present (MkdirAll no-op).
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PrepareMailOutboxDir(dir); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, MailOutboxDirMode)
	assertOwnerWhenRoot(t, dir)

	path := filepath.Join(dir, MailOutboxFile)
	box := OpenOutbox(path)
	if err := box.Notify(context.Background(), "Update failed", "image refused"); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, MailOutboxFileMode)
	assertOwnerWhenRoot(t, path)

	got, err := box.Take()
	if err != nil || len(got) != 1 {
		t.Fatalf("take: %v %+v", err, got)
	}
	if err := box.PutAll(got); err != nil {
		t.Fatal(err)
	}
	if err := box.Ack(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, MailOutboxFileMode)
	assertOwnerWhenRoot(t, path)
}

func TestMigrateLegacyMailOutboxPermissions(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	mail := filepath.Join(root, "mail")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(data, MailOutboxFile)
	if err := os.WriteFile(legacy, []byte("{\"id\":\"a1\",\"subject\":\"Update failed\",\"body\":\"old\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyMailOutbox(data, mail); err != nil {
		t.Fatal(err)
	}
	assertMode(t, mail, MailOutboxDirMode)
	assertMode(t, filepath.Join(mail, MailOutboxFile), MailOutboxFileMode)
	assertOwnerWhenRoot(t, mail)
	assertOwnerWhenRoot(t, filepath.Join(mail, MailOutboxFile))
}

func TestOutboxTakeAckSurvivesCrashWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, MailOutboxFile)
	box := OpenOutbox(path)
	if err := box.Notify(context.Background(), "Update failed", "image refused"); err != nil {
		t.Fatal(err)
	}
	first, err := box.Take()
	if err != nil || len(first) != 1 || first[0].Subject != "Update failed" {
		t.Fatalf("take: %v %+v", err, first)
	}
	if _, err := os.Stat(path + ".draining"); err != nil {
		t.Fatalf("draining must remain until Ack: %v", err)
	}
	// Simulate a core crash before Ack: next Take recovers the same notices.
	again, err := box.Take()
	if err != nil || len(again) != 1 || again[0].ID != first[0].ID {
		t.Fatalf("recover: %v %+v want id %q", err, again, first[0].ID)
	}
	if err := box.PutAll(nil); err != nil {
		t.Fatal(err)
	}
	if err := box.Ack(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".draining"); !os.IsNotExist(err) {
		t.Fatalf("draining after Ack: %v", err)
	}
	left, err := box.List()
	if err != nil || len(left) != 0 {
		t.Fatalf("list after ack: %v %+v", err, left)
	}
}

func TestOutboxTakeAckRestoresFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, MailOutboxFile)
	box := OpenOutbox(path)
	if err := box.Notify(context.Background(), "Nightly backup failed", "disk full"); err != nil {
		t.Fatal(err)
	}
	got, err := box.Take()
	if err != nil || len(got) != 1 {
		t.Fatalf("take: %v %+v", err, got)
	}
	if err := box.PutAll(got); err != nil {
		t.Fatal(err)
	}
	if err := box.Ack(); err != nil {
		t.Fatal(err)
	}
	left, err := box.List()
	if err != nil || len(left) != 1 || left[0].Subject != "Nightly backup failed" {
		t.Fatalf("restored: %v %+v", err, left)
	}
}

func TestMigrateLegacyMailOutbox(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	mail := filepath.Join(root, "mail")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(data, MailOutboxFile)
	if err := os.WriteFile(legacy, []byte("{\"id\":\"a1\",\"subject\":\"Update failed\",\"body\":\"old\"}\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyMailOutbox(data, mail); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy still present: %v", err)
	}
	box := OpenOutbox(filepath.Join(mail, MailOutboxFile))
	notes, err := box.List()
	if err != nil || len(notes) != 1 || notes[0].Subject != "Update failed" {
		t.Fatalf("migrated: %v %+v", err, notes)
	}
	// Second start with nothing left is a no-op.
	if err := MigrateLegacyMailOutbox(data, mail); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateLegacyMailOutboxAppendsExisting(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	mail := filepath.Join(root, "mail")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mail, 0o777); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(mail, MailOutboxFile)
	if err := os.WriteFile(dest, []byte("{\"id\":\"new\",\"subject\":\"New notice\",\"body\":\"n\"}\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, MailOutboxFile), []byte("{\"id\":\"old\",\"subject\":\"Legacy\",\"body\":\"l\"}\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyMailOutbox(data, mail); err != nil {
		t.Fatal(err)
	}
	notes, err := OpenOutbox(dest).List()
	if err != nil || len(notes) != 2 {
		t.Fatalf("append: %v %+v", err, notes)
	}
}
