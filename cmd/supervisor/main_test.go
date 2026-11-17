package main

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/supervisor"
)

func TestWireMailOutboxPermissions(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	backup := filepath.Join(root, "backups")
	mail := filepath.Join(root, "mail")
	// Pre-create as 0755 so PrepareMailOutboxDir must Chmod an existing dir (named volume case).
	if err := os.MkdirAll(mail, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mail, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := supervisor.Config{
		Token:             "test-token",
		DataDir:           data,
		MailOutboxDir:     mail,
		BackupDir:         backup,
		FirmTimezone:      "UTC",
		UpdateWindowStart: "02:00",
		UpdateWindowEnd:   "04:00",
		BackupTime:        "01:00",
		DockerSocket:      filepath.Join(root, "docker.sock"),
		UpstreamFile:      filepath.Join(data, "upstream"),
	}
	ops, err := wire(cfg)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(mail)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != supervisor.MailOutboxDirMode {
		t.Fatalf("mail dir mode %#o, want %#o", st.Mode().Perm(), supervisor.MailOutboxDirMode)
	}
	if os.Geteuid() == 0 {
		var info syscall.Stat_t
		if err := syscall.Stat(mail, &info); err != nil {
			t.Fatal(err)
		}
		if int(info.Uid) != supervisor.CoreUID || int(info.Gid) != supervisor.CoreGID {
			t.Fatalf("mail dir owner %d:%d, want %d:%d", info.Uid, info.Gid, supervisor.CoreUID, supervisor.CoreGID)
		}
	} else {
		t.Log("skip owner check: not running as root")
	}

	if err := ops.Outbox.Notify(context.Background(), "Backup failed", "disk full"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mail, supervisor.MailOutboxFile)
	st, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != supervisor.MailOutboxFileMode {
		t.Fatalf("outbox file mode %#o, want %#o", st.Mode().Perm(), supervisor.MailOutboxFileMode)
	}
	got, err := ops.Outbox.Take()
	if err != nil || len(got) != 1 {
		t.Fatalf("take: %v %+v", err, got)
	}
	if err := ops.Outbox.PutAll(got); err != nil {
		t.Fatal(err)
	}
	if err := ops.Outbox.Ack(); err != nil {
		t.Fatal(err)
	}
	st, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != supervisor.MailOutboxFileMode {
		t.Fatalf("after PutAll mode %#o, want %#o", st.Mode().Perm(), supervisor.MailOutboxFileMode)
	}
	if os.Geteuid() == 0 {
		var info syscall.Stat_t
		if err := syscall.Stat(path, &info); err != nil {
			t.Fatal(err)
		}
		if int(info.Uid) != supervisor.CoreUID || int(info.Gid) != supervisor.CoreGID {
			t.Fatalf("outbox owner %d:%d, want %d:%d", info.Uid, info.Gid, supervisor.CoreUID, supervisor.CoreGID)
		}
	}
}
