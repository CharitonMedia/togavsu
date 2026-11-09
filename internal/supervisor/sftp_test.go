package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

func TestSFTPFirstUseCreatesNamespaceWithoutParentMkdir(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "backups")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "sftp")
	if err := os.WriteFile(script, []byte(sftpPython), 0o700); err != nil {
		t.Fatal(err)
	}
	dest := sftpDest(t, dir, remote, script)
	dest.Spec.Namespace = "nsowned01"
	payload := []byte("synthetic sftp bytes")
	sum, err := sha256Hex(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := dest.Put(context.Background(), "one.tgbk", bytes.NewReader(payload), sum); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(remote, "nsowned01", "one.tgbk")); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest.Spec.RemotePath = blocked
	if err := dest.Put(context.Background(), "two.tgbk", bytes.NewReader(payload), sum); err == nil {
		t.Fatal("put succeeded when the configured base was not a directory")
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
	dest := sftpDest(t, dir, remote, script)
	dest.Spec.Namespace = "nsowned01"
	if err := os.MkdirAll(filepath.Join(remote, dest.Spec.Namespace), 0o700); err != nil {
		t.Fatal(err)
	}
	other := "20200101T000000Z-abcdefabcdefabcd"
	for _, name := range []string{oldID + ".tgbk", oldID + ".sha256", newID + ".tgbk", newID + ".sha256", other + ".tgbk", other + ".sha256"} {
		if err := os.WriteFile(filepath.Join(remote, dest.Spec.Namespace, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"notes.txt", other + ".tgbk", other + ".sha256"} {
		if err := os.WriteFile(filepath.Join(remote, name), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := PruneDestination(context.Background(), dest, 30*24*time.Hour, time.Now(), []OwnedObject{{
		DestinationID: dest.ID(), Name: oldID + ".tgbk", Created: time.Now().Add(-40 * 24 * time.Hour),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != oldID+".tgbk" {
		t.Fatalf("pruned %v", deleted)
	}
	for _, name := range []string{oldID + ".tgbk", oldID + ".sha256"} {
		if _, err := os.Stat(filepath.Join(remote, dest.Spec.Namespace, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still present (%v)", name, err)
		}
	}
	for _, name := range []string{newID + ".tgbk", newID + ".sha256", other + ".tgbk", other + ".sha256"} {
		if _, err := os.Stat(filepath.Join(remote, dest.Spec.Namespace, name)); err != nil {
			t.Fatalf("%s missing from namespace: %v", name, err)
		}
	}
	for _, name := range []string{"notes.txt", other + ".tgbk", other + ".sha256"} {
		if _, err := os.Stat(filepath.Join(remote, name)); err != nil {
			t.Fatalf("%s missing from shared root: %v", name, err)
		}
	}
}

func TestSFTPDeleteMissingIsIdempotentAndPermissionIsNot(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "sftp")
	body := `#!/bin/sh
cmd=
prev=
for arg in "$@"; do
  if [ "$prev" = "--command" ] || [ "$prev" = "-b" ]; then batch=$arg; fi
  prev=$arg
done
# The supervisor passes the batch file with -b. Read the rm operand.
while IFS= read -r line; do
  case "$line" in
    *missing*) echo "Couldn't delete file: No such file or directory" >&2; exit 1 ;;
    *denied*) echo "Couldn't delete file: Permission denied" >&2; exit 1 ;;
    *down*) echo "ssh: connect to host files.example port 22: Connection refused" >&2; exit 255 ;;
  esac
done < "$batch"
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	dest := sftpDest(t, dir, "/backups", script)
	if err := dest.Delete(context.Background(), "missing.tgbk"); err != nil {
		t.Fatalf("missing owned archive: %v", err)
	}
	if err := dest.Delete(context.Background(), "missing.sha256"); err != nil {
		t.Fatalf("missing owned sidecar: %v", err)
	}
	if err := dest.Delete(context.Background(), "denied.tgbk"); err == nil || remoteObjectAbsent(err) {
		t.Fatalf("permission error was treated as absence: %v", err)
	}
	if err := dest.Delete(context.Background(), "down.tgbk"); err == nil || remoteObjectAbsent(err) {
		t.Fatalf("network error was treated as absence: %v", err)
	}
}

func TestSFTPGetMissingIsNotFoundAndFailuresStayErrors(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "sftp")
	body := `#!/bin/sh
prev=
batch=
for arg in "$@"; do
  if [ "$prev" = "-b" ]; then batch=$arg; fi
  prev=$arg
done
while IFS= read -r line; do
  case "$line" in
    *missing*) echo "Couldn't stat remote file: No such file or directory" >&2; exit 1 ;;
    *denied*) echo "Couldn't stat remote file: Permission denied" >&2; exit 1 ;;
    *down*) echo "ssh: connect to host files.example port 22: Connection refused" >&2; exit 255 ;;
  esac
done < "$batch"
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	dest := sftpDest(t, dir, "/backups", script)
	if _, err := dest.Get(context.Background(), "missing.tgbk"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing get = %v", err)
	}
	if _, err := dest.Get(context.Background(), "denied.tgbk"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("permission get = %v", err)
	}
	if _, err := dest.Get(context.Background(), "down.tgbk"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("connection get = %v", err)
	}
}

func TestSFTPDeleteConfirmsAbsenceAndRetriesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	mode := filepath.Join(dir, "mode")
	if err := os.WriteFile(mode, []byte("denied"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "sftp")
	body := "#!/bin/sh\nprev=\nbatch=\nfor arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-b\" ]; then batch=$arg; fi\n  prev=$arg\ndone\n" +
		"mode=$(cat " + mode + ")\n" +
		"while IFS= read -r line; do\n  case \"$line\" in\n" +
		"    get*)\n" +
		"      if [ \"$mode\" = denied ]; then echo \"Couldn't stat remote file: Permission denied\" >&2; exit 1; fi\n" +
		"      echo \"Couldn't stat remote file: No such file or directory\" >&2; exit 1 ;;\n" +
		"    rm*) exit 0 ;;\n" +
		"  esac\ndone < \"$batch\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{
		ID: "share", Kind: "sftp", Host: "files.example", User: "backup", Port: 22,
		IdentityFile: filepath.Join(dir, "id"), KnownHosts: filepath.Join(dir, "known"),
		RemotePath: "/backups", RetentionDays: 30,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "id"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		return &SFTPDestination{Spec: spec, Command: script}, nil
	}
	if err := s.receiptBook().Add(OwnedObject{DestinationID: "share", Name: meta.ID + ".tgbk", Created: meta.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	ops := &Operations{Store: s, Gate: &Gate{}, Passphrase: func() (string, error) { return s.Passphrase, nil }}
	req := httptest.NewRequest(http.MethodDelete, "/v1/backups/"+meta.ID, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	NewMux("secret", ops, nil).ServeHTTP(rec, req)
	if rec.Code == http.StatusNoContent {
		t.Fatal("delete succeeded before absence was confirmed")
	}
	kept, err := s.lookup(meta.ID)
	if err != nil || !kept.Deleting {
		t.Fatalf("deleting record dropped: %+v %v", kept, err)
	}
	if owned, err := s.receiptBook().List("share"); err != nil || len(owned) != 1 {
		t.Fatalf("receipt dropped: %+v %v", owned, err)
	}
	if err := os.WriteFile(mode, []byte("missing"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := &Store{Dir: s.Dir, Book: s.Book, Passphrase: s.Passphrase, Now: s.Now, OpenDestination: s.OpenDestination}
	ops.Store = restarted
	rec = httptest.NewRecorder()
	NewMux("secret", ops, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("retry status %d %s", rec.Code, rec.Body.String())
	}
	if _, err := restarted.lookup(meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("metadata remained: %v", err)
	}
	if owned, err := restarted.receiptBook().List("share"); err != nil || len(owned) != 0 {
		t.Fatalf("receipt remained: %+v %v", owned, err)
	}
}

func TestOpenSSHMissingDiagnostics(t *testing.T) {
	if remoteObjectAbsent(fmt.Errorf("sftp: Can't ls: \"/backups/nsowned01\" not found")) != true {
		t.Fatal("ls not found was not absence")
	}
	if remoteObjectAbsent(fmt.Errorf("sftp: File \"/backups/nsowned01/a.tgbk\" not found.")) != true {
		t.Fatal("get not found was not absence")
	}
	if remoteObjectAbsent(fmt.Errorf("sftp: Couldn't stat remote file: No such file or directory")) != true {
		t.Fatal("no such file was not absence")
	}
	if remoteObjectAbsent(fmt.Errorf("sftp: Permission denied")) {
		t.Fatal("permission was treated as absence")
	}
	if remoteObjectAbsent(fmt.Errorf("ssh: connect to host files.example port 22: Connection refused")) {
		t.Fatal("connection refused was treated as absence")
	}
	if remoteObjectAbsent(fmt.Errorf("ssh: Could not resolve hostname files.example: Name or service not known")) {
		t.Fatal("name lookup was treated as absence")
	}
}

func TestOpenSSHNamespaceFirstUseAndStoreDelete(t *testing.T) {
	if _, err := os.Stat("/usr/sbin/sshd"); err != nil {
		t.Fatal("real sshd is required")
	}
	if _, err := exec.LookPath("sftp"); err != nil {
		t.Fatal("real sftp client is required")
	}
	dest, root := startRealSFTP(t)
	s := testStore(t)
	s.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(s.Dir, "destinations.json")}
	spec := dest.Spec
	spec.RetentionDays = 30
	if _, err := s.Book.Put(spec); err != nil {
		t.Fatal(err)
	}
	specs, err := s.Book.Specs()
	if err != nil || len(specs) != 1 || specs[0].Namespace == "" {
		t.Fatal(specs, err)
	}
	dest.Spec.Namespace = specs[0].Namespace
	s.OpenDestination = func(got DestinationSpec) (Destination, error) {
		copy := *dest
		copy.Spec = got
		return &copy, nil
	}
	other := filepath.Join(root, "other-firm.tgbk")
	write := exec.Command("sudo", "tee", other)
	write.Stdin = strings.NewReader("other-firm")
	if out, err := write.CombinedOutput(); err != nil {
		t.Fatalf("other firm file: %v %s", err, out)
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil || len(meta.DestinationErrors) != 0 {
		t.Fatalf("%+v %v", meta.DestinationErrors, err)
	}
	nsDir := filepath.Join(root, specs[0].Namespace)
	if _, err := os.Stat(filepath.Join(nsDir, meta.ID+".tgbk")); err != nil {
		t.Fatalf("first use did not publish the archive: %v", err)
	}
	if err := s.Delete(context.Background(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(nsDir, meta.ID+".tgbk")); !os.IsNotExist(err) {
		t.Fatalf("archive remained after confirmed absence: %v", err)
	}
	if _, err := s.lookup(meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("local metadata remained: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("other firm file removed: %v", err)
	}
	if _, err := dest.Get(context.Background(), meta.ID+".tgbk"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("real client did not confirm absence: %v", err)
	}
}

func startRealSFTP(t *testing.T) (*SFTPDestination, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(dir, "host")
	id := filepath.Join(dir, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-f", host, "-N", "").CombinedOutput(); err != nil {
		t.Fatalf("host key: %v %s", err, out)
	}
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-f", id, "-N", "").CombinedOutput(); err != nil {
		t.Fatalf("user key: %v %s", err, out)
	}
	if err := os.Chmod(id+".pub", 0o644); err != nil {
		t.Fatal(err)
	}
	user := "togavsu-sftp-test"
	home := filepath.Join(dir, "home")
	root := filepath.Join(home, "backups")
	_ = exec.Command("sudo", "userdel", user).Run()
	if out, err := exec.Command("sudo", "useradd", "-M", "-d", home, "-s", "/bin/sh", user).CombinedOutput(); err != nil {
		t.Fatalf("useradd: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("sudo", "userdel", user).Run() })
	if out, err := exec.Command("sudo", "usermod", "-p", "*", "-d", home, "-s", "/bin/sh", user).CombinedOutput(); err != nil {
		t.Fatalf("usermod: %v %s", err, out)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sudo", "chown", "-R", user+":"+user, home).CombinedOutput(); err != nil {
		t.Fatalf("chown: %v %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("sudo", "chown", "-R", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), dir).Run()
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	pidFile := filepath.Join(dir, "sshd.pid")
	cfg := filepath.Join(dir, "sshd_config")
	text := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nPubkeyAuthentication yes\nStrictModes no\nSubsystem sftp internal-sftp\nAllowUsers %s\nPrintMotd no\nX11Forwarding no\nAllowTcpForwarding no\n", port, host, pidFile, id+".pub", user)
	if err := os.WriteFile(cfg, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "sshd.log")
	cmd := exec.Command("sudo", "/usr/sbin/sshd", "-f", cfg, "-E", logPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		log, _ := os.ReadFile(logPath)
		t.Fatalf("sshd: %v %s %s", err, out, log)
	}
	t.Cleanup(func() {
		pid, err := os.ReadFile(pidFile)
		if err == nil {
			_ = exec.Command("sudo", "kill", strings.TrimSpace(string(pid))).Run()
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	scan := exec.Command("ssh-keyscan", "-p", strconv.Itoa(port), "-t", "ed25519", "127.0.0.1")
	known, err := scan.Output()
	if err != nil || len(known) == 0 {
		t.Fatalf("keyscan: %v %q", err, known)
	}
	knownPath := filepath.Join(dir, "known")
	if err := os.WriteFile(knownPath, known, 0o644); err != nil {
		t.Fatal(err)
	}
	probe := exec.Command("sftp", "-b", "-", "-i", id, "-P", strconv.Itoa(port), "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+knownPath, "--", user+"@127.0.0.1")
	probe.Stdin = strings.NewReader("bye\n")
	if out, err := probe.CombinedOutput(); err != nil {
		log, _ := exec.Command("sudo", "cat", logPath).CombinedOutput()
		t.Fatalf("probe: %v %s\nsshd: %s", err, out, log)
	}
	return &SFTPDestination{
		Command: "sftp",
		Spec: DestinationSpec{
			ID: "share", Kind: "sftp", Host: "127.0.0.1", User: user, Port: port,
			IdentityFile: id, KnownHosts: knownPath, RemotePath: root,
		},
	}, root
}

func TestSFTPHostileArgumentsDoNotStart(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "argv")
	script := filepath.Join(dir, "sftp")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + flag + "\nexit 99\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	base := sftpDest(t, dir, "/backups", script)
	cases := []DestinationSpec{
		{User: "-D/tmp/evil", Host: "files.example"},
		{User: "-oProxyCommand=touch /tmp/x", Host: "files.example"},
		{User: "backup", Host: "-oProxyCommand=id"},
		{User: "backup\n-oProxyCommand=id", Host: "files.example"},
		{User: "backup@elsewhere", Host: "files.example"},
		{User: "backup", Host: "files.example", Port: 65536},
		{User: "backup", Host: "files.example", IdentityFile: "-oProxyCommand=id"},
		{User: "backup", Host: "files.example", KnownHosts: "known\n-oProxyCommand=id"},
	}
	for _, tc := range cases {
		spec := base.Spec
		if tc.User != "" {
			spec.User = tc.User
		}
		if tc.Host != "" {
			spec.Host = tc.Host
		}
		if tc.Port != 0 {
			spec.Port = tc.Port
		}
		if tc.IdentityFile != "" {
			spec.IdentityFile = tc.IdentityFile
		}
		if tc.KnownHosts != "" {
			spec.KnownHosts = tc.KnownHosts
		}
		dest := &SFTPDestination{Spec: spec, Command: script}
		if _, err := dest.argv("batch"); err == nil || !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%+v argv err %v", tc, err)
		}
		err := dest.Put(context.Background(), "one.tgbk", bytes.NewReader([]byte("x")), "")
		if !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%+v put %v", tc, err)
		}
		if _, statErr := os.Stat(flag); !os.IsNotExist(statErr) {
			t.Fatalf("%+v started sftp", tc)
		}
	}
	ok := base.Spec
	args, err := (&SFTPDestination{Spec: ok}).argv("/tmp/batch")
	if err != nil {
		t.Fatal(err)
	}
	if len(args) < 2 || args[len(args)-2] != "--" || args[len(args)-1] != "backup@files.example" {
		t.Fatalf("argv %q", args)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-D") || strings.Contains(arg, "ProxyCommand") {
			t.Fatalf("option selected by destination text: %q", args)
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
    if cmd == "mkdir":
        os.mkdir(parts[1])
    elif cmd == "put":
        parent = os.path.dirname(parts[2])
        if not os.path.isdir(parent):
            sys.stderr.write("Couldn't open dest: No such file or directory\n")
            sys.exit(1)
        shutil.copy(parts[1], parts[2])
    elif cmd == "get":
        shutil.copy(parts[1], parts[2])
    elif cmd == "rm":
        os.remove(parts[1])
    elif cmd == "ls":
        target = parts[-1]
        if not os.path.isdir(target):
            if os.path.exists(target):
                sys.stderr.write("Not a directory\n")
            else:
                sys.stderr.write("No such file or directory\n")
            sys.exit(1)
        print("\n".join(sorted(os.listdir(target))))
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
