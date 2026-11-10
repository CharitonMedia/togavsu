package files

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestPGDropFolderTakesSteadyFilesOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	share := filepath.Join(e.root, "share")
	raw, _ := json.Marshal(map[string]string{"path": share})
	msg := json.RawMessage(raw)
	yes := true
	if _, err := e.svc.CreateBackend(ctx, e.admin, BackendWrite{Kind: "network_path", Name: "Office NAS", DropFolder: &yes, Settings: &msg}); err != nil {
		t.Fatal(err)
	}
	local := json.RawMessage(`{"path":"/tmp/x"}`)
	if _, err := e.svc.CreateBackend(ctx, e.admin, BackendWrite{Kind: "local_disk", Name: "bad", DropFolder: &yes, Settings: &local}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("a drop folder on local disk = %v", err)
	}
	if _, err := e.svc.ScanDrop(ctx); err != nil {
		t.Fatal(err)
	}
	drop := filepath.Join(share, "drop")
	for _, n := range []string{"2026-0001", "2026-0002"} {
		if st, err := os.Stat(filepath.Join(drop, n)); err != nil || !st.IsDir() {
			t.Fatalf("no subfolder for open matter %s", n)
		}
	}
	if _, err := os.Stat(filepath.Join(drop, "2025-0099")); err == nil {
		t.Fatal("a closed matter got a drop subfolder")
	}
	inMatter := filepath.Join(drop, "2026-0001", "scan-0007.pdf")
	top := filepath.Join(drop, "unsorted.jpg")
	_ = os.WriteFile(inMatter, []byte("first half"), 0o600)
	_ = os.WriteFile(top, []byte("photo"), 0o600)
	if got, err := e.svc.ScanDrop(ctx); err != nil || len(got) != 0 {
		t.Fatalf("first sighting took files: %v %v", got, err)
	}
	e.clock.advance(20 * time.Second)
	// Still being written: the size changes, so its steady clock restarts.
	_ = os.WriteFile(inMatter, []byte("first half, second half"), 0o600)
	if got, _ := e.svc.ScanDrop(ctx); len(got) != 0 {
		t.Fatalf("a growing file was taken: %v", got)
	}
	e.clock.advance(15 * time.Second)
	got, err := e.svc.ScanDrop(ctx)
	if err != nil || len(got) != 1 || got[0].Name != "unsorted.jpg" || got[0].MatterID != nil || got[0].Source != "drop_folder" {
		t.Fatalf("top-level file = %+v %v", got, err)
	}
	if _, err := os.Stat(top); !os.IsNotExist(err) {
		t.Fatal("a taken file stayed in the drop folder")
	}
	e.clock.advance(31 * time.Second)
	got, err = e.svc.ScanDrop(ctx)
	if err != nil || len(got) != 1 || got[0].MatterID == nil || *got[0].MatterID != e.open {
		t.Fatalf("matter file = %+v %v", got, err)
	}
	d, err := e.svc.OpenVersion(ctx, e.staff, got[0].ID, *got[0].CurrentVersionID, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(d.Body)
	d.Close()
	if string(b) != "first half, second half" {
		t.Fatalf("dropped bytes = %q", b)
	}
	if again, _ := e.svc.ScanDrop(ctx); len(again) != 0 {
		t.Fatalf("a file was taken twice: %v", again)
	}
	if len(e.versions) != 2 || e.versions[1].Source != "drop_folder" {
		t.Fatalf("drop files must reach file.version_created: %+v", e.versions)
	}
}

func TestPGEncryptionPerBackend(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if !e.local.Encryption {
		t.Fatal("local disk must default to encrypted")
	}
	sealed := e.create(e.staff, NewFile{Name: "a.txt", MatterID: e.open}, "PLAINTEXT-MARKER sealed")
	no := false
	if _, err := e.svc.UpdateBackend(ctx, e.admin, e.local.ID, BackendWrite{Encryption: &no}); err != nil {
		t.Fatal(err)
	}
	plain := e.create(e.staff, NewFile{Name: "b.txt", MatterID: e.open}, "PLAINTEXT-MARKER open")
	found := map[string]bool{}
	_ = filepath.Walk(e.root+"/local/objects", func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			raw, _ := os.ReadFile(p)
			found[info.Name()] = strings.Contains(string(raw), "PLAINTEXT-MARKER")
		}
		return nil
	})
	if found[string(*sealed.CurrentVersionID)] || !found[string(*plain.CurrentVersionID)] {
		t.Fatalf("plaintext on disk: %v", found)
	}
	// Both still read after the switch.
	for _, f := range []FileView{sealed, plain} {
		if got := readVersion(t, e, e.staff, f.ID, *f.CurrentVersionID); !strings.HasPrefix(got, "PLAINTEXT-MARKER") {
			t.Fatalf("%s = %q", f.Name, got)
		}
	}
	_, s3 := e.s3()
	if _, err := e.svc.UpdateBackend(ctx, e.admin, s3.ID, BackendWrite{Encryption: &no}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("turning S3 encryption off = %v", err)
	}
}

func TestPGStorageBackendsAreAdminOnlyAndKeepSecrets(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, s3 := e.s3()
	if _, err := e.svc.Backends(ctx, e.attorney); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("attorney backends = %v", err)
	}
	if _, err := e.svc.StartMove(ctx, e.staff, MoveRequest{FromBackendID: e.local.ID, ToBackendID: s3.ID, Scope: "all"}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("staff move = %v", err)
	}
	got, err := e.svc.GetBackend(ctx, e.admin, s3.ID)
	if err != nil || got.Settings["secrets_set"] != true || got.Settings["secret_key"] != nil || !got.Encryption {
		t.Fatalf("backend view = %+v %v", got, err)
	}
	var cipher string
	_ = e.pool.QueryRowContext(ctx, `SELECT settings_ciphertext FROM storage_backends WHERE id = $1`, s3.ID).Scan(&cipher)
	if strings.Contains(cipher, "fake-secret-key") || !strings.HasPrefix(cipher, "v1:") {
		t.Fatal("backend secrets are not sealed in the database")
	}
	// Editing without the secret keeps the stored secret.
	raw := json.RawMessage(`{"endpoint":"` + got.Settings["endpoint"].(string) + `","bucket":"togavsu","access_key":"AKIDTESTFAKE"}`)
	if _, err := e.svc.UpdateBackend(ctx, e.admin, s3.ID, BackendWrite{Settings: &raw}); err != nil {
		t.Fatal(err)
	}
	if tested, err := e.svc.TestBackend(ctx, e.admin, s3.ID); err != nil || tested.Health != "ok" {
		t.Fatalf("test after edit = %+v %v", tested, err)
	}
	bad := json.RawMessage(`{"endpoint":"` + got.Settings["endpoint"].(string) + `","bucket":"togavsu","access_key":"AKIDTESTFAKE","secret_key":"wrong"}`)
	if _, err := e.svc.UpdateBackend(ctx, e.admin, s3.ID, BackendWrite{Settings: &bad}); err != nil {
		t.Fatal(err)
	}
	if tested, _ := e.svc.TestBackend(ctx, e.admin, s3.ID); tested.Health != "failed" {
		t.Fatalf("a wrong secret passed the Test button: %+v", tested)
	}
	if _, err := e.svc.StartMove(ctx, e.admin, MoveRequest{FromBackendID: e.local.ID, ToBackendID: e.local.ID, Scope: "all"}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("move onto itself = %v", err)
	}
	var n int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity = 'storage_backend' AND entity_id = $1`, s3.ID).Scan(&n)
	if n < 3 {
		t.Fatalf("backend changes in the audit log = %d", n)
	}
}
