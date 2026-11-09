package supervisor

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestRenameat2Number(t *testing.T) {
	switch runtime.GOARCH {
	case "amd64":
		if sysRenameat2 != 316 {
			t.Fatalf("amd64 renameat2 = %d", sysRenameat2)
		}
	case "arm64":
		if sysRenameat2 != 276 {
			t.Fatalf("arm64 renameat2 = %d", sysRenameat2)
		}
	default:
		t.Fatalf("renameat2 is not defined for %s", runtime.GOARCH)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	occupied := filepath.Join(dir, "occupied")
	if err := os.WriteFile(src, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(occupied, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := renameNoReplaceAt(int(d.Fd()), "src", int(d.Fd()), "dst"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(dst)
	if err != nil || string(body) != "owned" {
		t.Fatalf("renamed body %q %v", body, err)
	}
	if err := renameNoReplaceAt(int(d.Fd()), "dst", int(d.Fd()), "occupied"); err != syscall.EEXIST {
		t.Fatalf("rename over an occupied name = %v", err)
	}
	body, err = os.ReadFile(occupied)
	if err != nil || string(body) != "foreign" {
		t.Fatalf("occupied body %q %v", body, err)
	}
}
