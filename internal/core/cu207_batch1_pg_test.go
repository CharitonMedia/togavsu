package core

import (
	"crypto/ed25519"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/testkit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

// batch1ModuleRepos are the five batch 1 marketplace modules (CU2-07).
var batch1ModuleRepos = []string{
	"documents", "email", "client-portal", "lawpay-docuseal", "quickbooks",
}

// TestCU207Batch1ModulesUnchanged installs, runs and uninstalls matter-clock and the five
// batch 1 modules built from their public repositories at main (read-only), with no change
// to their packages (CU2-07 / CORE-CHANGES-2 §4.4).
//
// Honesty bound: Deploy replaces each module with a stub HTTP handler. This proves package
// bytes/manifests/migrations install and uninstall on this core, and that /m/{id}/ proxies
// through the frame. It does not execute the modules' real handlers or container images.
func TestCU207Batch1ModulesUnchanged(t *testing.T) {
	if os.Getenv("TOGAVSU_TEST_DATABASE_URL") == "" {
		t.Skip("TOGAVSU_TEST_DATABASE_URL unset")
	}
	root := batch1ReposRoot(t)
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))

	// Batch 1 modules (and matter-clock): Deploy uses a stub HTTP handler so the lane can
	// run without each module's container image. Package bytes, manifests and migrations
	// come from public main (or the core sample for matter-clock). "Run" hits /m/{id}/
	// through the core proxy so the frame path stays live on this core revision.
	x.rt.Default = func(m testkit.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok:" + m.ID))
		})
	}

	runSurface := func(id string) {
		t.Helper()
		mustInstalled(t, x, id)
		ct := x.admin.must(http.StatusCreated, "POST", "/api/v1/modules/"+id+"/context-tokens", map[string]any{})
		if strings.TrimSpace(str(t, ct.json, "token")) == "" {
			t.Fatalf("%s context token empty: %s", id, ct.body)
		}
		frame := x.admin.do("GET", "/m/"+id+"/", nil)
		if frame.code != http.StatusOK || !strings.Contains(frame.body, "ok:"+id) {
			t.Fatalf("%s /m/ surface: %d %s", id, frame.code, frame.body)
		}
	}

	// matter-clock from the core example package (reference; no separate public repo).
	if _, problem := x.tabForm(x.admin, "install", nil, x.build(nil)); problem != "" {
		t.Fatalf("matter-clock install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	runSurface("matter-clock")
	uninstallKeep(t, x, "matter-clock")

	for _, id := range batch1ModuleRepos {
		pkgDir := filepath.Join(root, "togavsu-"+id, "package")
		if _, err := os.Stat(filepath.Join(pkgDir, "togavsu-module.yaml")); err != nil {
			t.Fatalf("%s package missing at %s: %v", id, pkgDir, err)
		}
		files, err := modulekit.ReadDir(pkgDir)
		if err != nil {
			t.Fatalf("%s ReadDir: %v", id, err)
		}
		signed, _, err := modulekit.BuildInstallable(x.priv, files)
		if err != nil {
			t.Fatalf("%s BuildInstallable: %v", id, err)
		}
		if _, problem := x.tabForm(x.admin, "install", nil, signed); problem != "" {
			r := x.admin.upload("/api/v1/modules", nil, signed)
			t.Fatalf("%s install tab=%q api=%d %s", id, problem, r.code, r.body)
		}
		x.approveOnTab(id)
		runSurface(id)
		uninstallKeep(t, x, id)
	}
}

func mustInstalled(t *testing.T, x *ext, id string) {
	t.Helper()
	view := x.admin.must(http.StatusOK, "GET", "/api/v1/modules/"+id, nil)
	if view.json["status"] != "installed" {
		t.Fatalf("%s status: %s", id, view.body)
	}
}

func uninstallKeep(t *testing.T, x *ext, id string) {
	t.Helper()
	x.admin.must(http.StatusOK, "POST", "/api/v1/modules/"+id+"/uninstall", map[string]any{
		"data": "keep", "confirmation": id,
	})
	if r := x.admin.do("GET", "/api/v1/modules/"+id, nil); r.code != http.StatusNotFound {
		t.Fatalf("%s after uninstall: %d %s", id, r.code, r.body)
	}
}

// batch1ReposRoot returns a directory holding togavsu-{id} clones at main. Prefer
// TOGAVSU_BATCH1_MODS; otherwise clone read-only into the test temp dir.
func batch1ReposRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("TOGAVSU_BATCH1_MODS"); root != "" {
		return root
	}
	root := t.TempDir()
	for _, id := range batch1ModuleRepos {
		url := "https://github.com/CharitonMedia/togavsu-" + id + ".git"
		cmd := exec.Command("git", "clone", "--depth", "1", "--branch", "main", url, filepath.Join(root, "togavsu-"+id))
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("clone %s: %v\n%s", id, err, out)
		}
	}
	return root
}
