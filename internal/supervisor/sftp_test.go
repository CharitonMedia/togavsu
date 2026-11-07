package supervisor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSFTPRejectsInjectedRemotePath(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "sftp")
	body := "#!/bin/sh\necho RAN > " + flag + "\nexit 99\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	dest := sftpDest(t, dir, "/backups\n!touch "+flag, script)
	err := dest.Put(context.Background(), "one.tgbk", bytes.NewReader([]byte("x")), "")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(flag); !os.IsNotExist(statErr) {
		t.Fatal("injected command ran")
	}
}

func TestSFTPRoundTripPathWithSpaces(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "firm files")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "sftp")
	if err := os.WriteFile(script, []byte(sftpPython), 0o700); err != nil {
		t.Fatal(err)
	}
	dest := sftpDest(t, dir, remote, script)
	payload := []byte("synthetic sftp bytes")
	sum, err := sha256Hex(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := dest.Put(context.Background(), "one.tgbk", bytes.NewReader(payload), sum); err != nil {
		t.Fatal(err)
	}
	rc, err := dest.Get(context.Background(), "one.tgbk")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ioReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("got %q %v", got, err)
	}
	if err := dest.Delete(context.Background(), "one.tgbk"); err != nil {
		t.Fatal(err)
	}
}

func TestSFTPRetentionRemovesExpiredArchives(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "backups")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "sftp")
	if err := os.WriteFile(script, []byte(sftpPython), 0o700); err != nil {
		t.Fatal(err)
	}
	oldID := time.Now().Add(-40*24*time.Hour).UTC().Format("20060102T150405Z") + "-0123456789abcdef"
	newID := time.Now().Add(-2*time.Hour).UTC().Format("20060102T150405Z") + "-fedcba9876543210"
	for _, name := range []string{oldID + ".tgbk", oldID + ".sha256", newID + ".tgbk", newID + ".sha256"} {
		if err := os.WriteFile(filepath.Join(remote, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dest := sftpDest(t, dir, remote, script)
	n, err := PruneDestination(context.Background(), dest, 30*24*time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned %d", n)
	}
	for _, name := range []string{oldID + ".tgbk", oldID + ".sha256"} {
		if _, err := os.Stat(filepath.Join(remote, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still present (%v)", name, err)
		}
	}
	for _, name := range []string{newID + ".tgbk", newID + ".sha256"} {
		if _, err := os.Stat(filepath.Join(remote, name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
}

const sftpPython = `#!/usr/bin/env python3
import os, shlex, shutil, sys
args = sys.argv[1:]
batch = args[args.index("-b") + 1]
for raw in open(batch):
    line = raw.strip()
    if not line or line == "bye":
        continue
    if line.startswith("!"):
        sys.stderr.write("injected\n")
        sys.exit(99)
    parts = shlex.split(line)
    cmd = parts[0]
    if cmd == "put":
        os.makedirs(os.path.dirname(parts[2]), exist_ok=True)
        shutil.copy(parts[1], parts[2])
    elif cmd == "get":
        shutil.copy(parts[1], parts[2])
    elif cmd == "rm":
        os.remove(parts[1])
    elif cmd == "ls":
        print("\n".join(sorted(os.listdir(parts[-1]))))
`

func sftpDest(t *testing.T, dir, remote, command string) *SFTPDestination {
	t.Helper()
	idFile := filepath.Join(dir, "id")
	if err := os.WriteFile(idFile, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &SFTPDestination{
		Spec: DestinationSpec{
			ID: "s", Kind: "sftp", Host: "files.example", User: "backup", Port: 22,
			IdentityFile: idFile, KnownHosts: filepath.Join(dir, "known"),
			RemotePath: remote,
		},
		Command: command,
	}
}

func ioReadAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf bytes.Buffer
	tmp := make([]byte, 1024)
	for {
		n, err := r.Read(tmp)
		buf.Write(tmp[:n])
		if err != nil {
			if err.Error() == "EOF" {
				return buf.Bytes(), nil
			}
			return buf.Bytes(), err
		}
	}
}
