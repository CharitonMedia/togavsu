package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBackupPassphrase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup_passphrase")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBackupPassphrase(path, "synthetic-backup-passphrase"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "synthetic-backup-passphrase\n" {
		t.Fatalf("file %q %v", got, err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st.Mode(), err)
	}
	// A shorter passphrase replaces the old one completely.
	if err := writeBackupPassphrase(path, "short-synthetic"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "short-synthetic\n" {
		t.Fatalf("rewrite left %q", got)
	}
	// Bootstrap creates the file; a missing file in an existing secrets directory fails.
	if err := writeBackupPassphrase(filepath.Join(dir, "absent"), "x-synthetic"); err == nil {
		t.Fatal("wrote a passphrase file bootstrap did not create")
	}
	// No secrets directory (a process outside the image) is not an error.
	if err := writeBackupPassphrase(filepath.Join(dir, "no-dir", "backup_passphrase"), "x-synthetic"); err != nil {
		t.Fatal(err)
	}
	if err := writeBackupPassphrase(path, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "short-synthetic\n" {
		t.Fatalf("empty passphrase changed the file to %q", got)
	}
}
