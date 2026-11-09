package supervisor

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func publicationStore(dir string) *Store {
	return &Store{
		Dir:        dir,
		Passphrase: "synthetic-passphrase",
		Now:        func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) },
		Dump: func(ctx context.Context, w io.Writer) error {
			_, err := io.WriteString(w, "COPY core.synthetic_items (id) FROM stdin;\n1\n2\n3\n\\.\n")
			return err
		},
	}
}

// leftovers lists every backup-volume entry that is not part of a completed
// backup in keep.
func leftovers(t *testing.T, dir string, keep ...string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ent := range entries {
		name := ent.Name()
		owned := false
		for _, id := range keep {
			if name == id+".tgbk" || name == id+".sha256" || name == id+".json" {
				owned = true
			}
		}
		if !owned {
			out = append(out, name)
		}
	}
	return out
}

func TestPublicationStepFailureRemovesPayload(t *testing.T) {
	for _, step := range []string{"archive", "checksum", "metadata"} {
		t.Run(step, func(t *testing.T) {
			s := publicationStore(t.TempDir())
			done, err := s.Create(context.Background(), reasonManual)
			if err != nil {
				t.Fatal(err)
			}
			s.publishHook = func(got string) error {
				if got == step {
					return errors.New("injected " + step + " failure")
				}
				return nil
			}
			if _, err := s.Create(context.Background(), reasonManual); err == nil {
				t.Fatal("create succeeded with injected failure")
			}
			if extra := leftovers(t, s.Dir, done.ID); len(extra) != 0 {
				t.Fatalf("unreclaimed files after %s failure: %v", step, extra)
			}
			all, err := s.List()
			if err != nil || len(all) != 1 || all[0].ID != done.ID {
				t.Fatalf("completed backup not preserved: %+v %v", all, err)
			}
		})
	}
}

func TestPublicationSyncFailureRemovesPayload(t *testing.T) {
	for _, kind := range []string{"archive", "checksum"} {
		t.Run(kind, func(t *testing.T) {
			s := publicationStore(t.TempDir())
			s.failSync = kind
			if _, err := s.Create(context.Background(), reasonManual); err == nil {
				t.Fatal("create succeeded with failed sync")
			}
			if extra := leftovers(t, s.Dir); len(extra) != 0 {
				t.Fatalf("unreclaimed files after %s sync failure: %v", kind, extra)
			}
		})
	}
}

func TestPublicationKilledProcessIsReconciled(t *testing.T) {
	if os.Getenv("TOGAVSU_PUBLICATION_CHILD") == "1" {
		step := os.Getenv("TOGAVSU_PUBLICATION_STEP")
		ready := os.Getenv("TOGAVSU_PUBLICATION_READY")
		s := publicationStore(os.Getenv("TOGAVSU_PUBLICATION_DIR"))
		s.publishHook = func(got string) error {
			if got == step {
				_ = os.WriteFile(ready, []byte("1"), 0o600)
				time.Sleep(time.Minute)
			}
			return nil
		}
		_, _ = s.Create(context.Background(), reasonManual)
		return
	}
	for _, step := range []string{"archive", "checksum", "metadata"} {
		t.Run(step, func(t *testing.T) {
			dir := t.TempDir()
			s := publicationStore(dir)
			done, err := s.Create(context.Background(), reasonManual)
			if err != nil {
				t.Fatal(err)
			}
			ready := filepath.Join(t.TempDir(), "ready")
			cmd := exec.Command(os.Args[0], "-test.run=TestPublicationKilledProcessIsReconciled", "-test.count=1")
			cmd.Env = append(os.Environ(),
				"TOGAVSU_PUBLICATION_CHILD=1",
				"TOGAVSU_PUBLICATION_DIR="+dir,
				"TOGAVSU_PUBLICATION_READY="+ready,
				"TOGAVSU_PUBLICATION_STEP="+step,
			)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatal("child did not reach " + step)
				}
				time.Sleep(15 * time.Millisecond)
			}
			if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()

			stranded := leftovers(t, dir, done.ID)
			if !containsSuffix(stranded, ".tgbk") || !containsSuffix(stranded, publicationSuffix) {
				t.Fatalf("kill at %s left %v; expected an archive and its intent", step, stranded)
			}
			// An unrelated file under the backup volume must survive.
			foreign := filepath.Join(dir, "operator-notes.txt")
			if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Normal restart path: a fresh store runs Prune, which recovers first.
			restart := publicationStore(dir)
			if n, err := restart.Prune(context.Background(), 24*365*time.Hour); err != nil || n != 0 {
				t.Fatalf("prune after kill = %d %v", n, err)
			}
			if extra := leftovers(t, dir, done.ID); len(extra) != 1 || extra[0] != "operator-notes.txt" {
				t.Fatalf("after recovery: %v", extra)
			}
			all, err := restart.List()
			if err != nil || len(all) != 1 || all[0].ID != done.ID {
				t.Fatalf("completed backup not preserved: %+v %v", all, err)
			}
			rc, _, err := restart.Open(done.ID)
			if err != nil {
				t.Fatalf("completed backup unreadable: %v", err)
			}
			rc.Close()
			if err := restart.Delete(context.Background(), done.ID); err != nil {
				t.Fatalf("delete completed backup: %v", err)
			}
			if extra := leftovers(t, dir); len(extra) != 1 {
				t.Fatalf("after delete: %v", extra)
			}
		})
	}
}

func TestPublicationAckLostKeepsCompletedBackup(t *testing.T) {
	s := publicationStore(t.TempDir())
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	// The process stopped after publishing .json but before dropping the intent.
	if err := s.beginPublication(meta.ID, meta.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverPlaintext(); err != nil {
		t.Fatal(err)
	}
	if extra := leftovers(t, s.Dir, meta.ID); len(extra) != 0 {
		t.Fatalf("intent not settled: %v", extra)
	}
	rc, _, err := s.Open(meta.ID)
	if err != nil {
		t.Fatalf("completed backup removed or damaged: %v", err)
	}
	rc.Close()
}

func TestPublicationLeavesNonRegularFilesAlone(t *testing.T) {
	s := publicationStore(t.TempDir())
	id, err := newBackupID(s.now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.beginPublication(id, s.now()); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(s.Dir, id+".tgbk")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverPlaintext(); err != nil {
		t.Fatalf("recovery blocked by a foreign entry: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(s.Dir, id+".tgbk")); err != nil {
		t.Fatalf("symlink removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(s.Dir, id+publicationSuffix)); err != nil {
		t.Fatalf("intent dropped while a foreign entry remains: %v", err)
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "foreign" {
		t.Fatalf("symlink target touched: %q %v", raw, err)
	}
	if _, err := s.Create(context.Background(), reasonManual); err != nil {
		t.Fatalf("later backup blocked: %v", err)
	}
}

func containsSuffix(names []string, suffix string) bool {
	for _, n := range names {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}
