package core

import (
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// batch1ModuleRepos are the five batch 1 marketplace modules (CU2-07).
var batch1ModuleRepos = []string{
	"documents", "email", "client-portal", "lawpay-docuseal", "quickbooks",
}

// cu207Route is one real module path under /m/{id}/ plus a body marker from that
// module's own code (not a stub).
type cu207Route struct {
	path   string
	marker string
}

var cu207RealRoutes = map[string]cu207Route{
	"matter-clock":    {path: "/tab", marker: "Hours this month"},
	"documents":       {path: "/templates", marker: "Templates"},
	"email":           {path: "/inbox", marker: "Inbox"},
	"client-portal":   {path: "/staff/inbox", marker: "Messages"},
	"lawpay-docuseal": {path: "/tab/setup", marker: "LawPay"},
	"quickbooks":      {path: "/setup", marker: "QuickBooks setup"},
}

// TestCU207Batch1ModulesUnchanged installs, runs and uninstalls matter-clock and the five
// batch 1 modules built from their public repositories at main (read-only), packaged with
// cmd/togavsu-module exactly as each repo's package step does (CU2-07 / CORE-CHANGES-2 §4.4).
//
// Each module binary is started out of process through the real testkit module runtime
// (no stub Default handler). The test hits /m/{id}/healthz and one real route per module
// and asserts a response from that module's own code, then uninstalls with data deleted
// and asserts schema and routes are gone. A missing network fails the test by name.
func TestCU207Batch1ModulesUnchanged(t *testing.T) {
	if os.Getenv("TOGAVSU_TEST_DATABASE_URL") == "" {
		t.Skip("TOGAVSU_TEST_DATABASE_URL unset")
	}
	repoRoot := cu207RepoRoot(t)
	modsRoot := batch1ReposRoot(t)
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))

	// Do not use a stub Default: every module under test registers its own out-of-process
	// binary via Serve before install/approve.
	x.rt.Default = nil

	dbHost, dbName := cu207DBHostName(t, x.cfg.DatabaseURL)
	binDir := t.TempDir()
	keyFile := filepath.Join(t.TempDir(), "signing.key")
	if err := os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(x.priv.Seed())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runOne := func(id, bin, signedPath, schema string, installable bool) {
		t.Helper()
		x.rt.Serve(id, func(m testkit.Module) http.Handler {
			return cu207StartProcess(t, bin, m, dbHost, dbName)
		})
		pkg, err := os.ReadFile(signedPath)
		if err != nil {
			t.Fatalf("%s read package: %v", id, err)
		}
		if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
			r := x.admin.upload("/api/v1/modules", nil, pkg)
			t.Fatalf("%s install tab=%q api=%d %s", id, problem, r.code, r.body)
		}
		x.approveOnTab(id)
		mustInstalled(t, x, id)
		if x.count(`SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1`, schema) != 1 {
			t.Fatalf("%s schema %s missing after install", id, schema)
		}
		ct := x.admin.must(http.StatusCreated, "POST", "/api/v1/modules/"+id+"/context-tokens", map[string]any{})
		tok := strings.TrimSpace(str(t, ct.json, "token"))
		if tok == "" {
			t.Fatalf("%s context token empty: %s", id, ct.body)
		}
		hz := x.admin.do("GET", "/m/"+id+"/healthz", nil)
		if hz.code != http.StatusOK || !strings.Contains(hz.body, "ok") {
			t.Fatalf("%s healthz via /m/: %d %s", id, hz.code, hz.body)
		}
		route, ok := cu207RealRoutes[id]
		if !ok {
			t.Fatalf("%s: no real route configured", id)
		}
		page := x.admin.do("GET", "/m/"+id+route.path+"?context="+url.QueryEscape(tok), nil)
		if page.code != http.StatusOK {
			t.Fatalf("%s real route %s: %d %s", id, route.path, page.code, page.body)
		}
		if !strings.Contains(page.body, route.marker) {
			t.Fatalf("%s real route missing marker %q: %s", id, route.marker, page.body[:min(800, len(page.body))])
		}
		if strings.Contains(page.body, "ok:"+id) {
			t.Fatalf("%s still answered with stub body", id)
		}
		uninstallDelete(t, x, id)
		if x.count(`SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1`, schema) != 0 {
			t.Fatalf("%s schema %s still present after uninstall", id, schema)
		}
		gone := x.admin.do("GET", "/m/"+id+"/healthz", nil)
		if gone.code != http.StatusNotFound {
			t.Fatalf("%s route after uninstall: %d %s", id, gone.code, gone.body)
		}
		_ = installable
	}

	// matter-clock from examples/matter-clock in this repo.
	mcBin := filepath.Join(binDir, "matter-clock")
	cu207GoBuild(t, repoRoot, mcBin, "./examples/matter-clock/cmd/matter-clock")
	mcPkg := filepath.Join(t.TempDir(), "matter-clock.signed.json")
	cu207Package(t, repoRoot, keyFile, filepath.Join(repoRoot, "examples/matter-clock/package"), mcPkg, false)
	runOne("matter-clock", mcBin, mcPkg, modules.SchemaFor("matter-clock"), false)

	for _, id := range batch1ModuleRepos {
		modDir := filepath.Join(modsRoot, "togavsu-"+id)
		if _, err := os.Stat(filepath.Join(modDir, "package", "togavsu-module.yaml")); err != nil {
			t.Fatalf("%s package missing at %s: %v", id, modDir, err)
		}
		bin := filepath.Join(binDir, id)
		cu207GoBuild(t, modDir, bin, "./cmd/"+id)
		signed := filepath.Join(t.TempDir(), id+".signed.json")
		cu207Package(t, repoRoot, keyFile, filepath.Join(modDir, "package"), signed, true)
		runOne(id, bin, signed, modules.SchemaFor(id), true)
	}
}

func mustInstalled(t *testing.T, x *ext, id string) {
	t.Helper()
	view := x.admin.must(http.StatusOK, "GET", "/api/v1/modules/"+id, nil)
	if view.json["status"] != "installed" {
		t.Fatalf("%s status: %s", id, view.body)
	}
}

func uninstallDelete(t *testing.T, x *ext, id string) {
	t.Helper()
	x.admin.must(http.StatusOK, "POST", "/api/v1/modules/"+id+"/uninstall", map[string]any{
		"data": "delete", "confirmation": id,
	})
	if r := x.admin.do("GET", "/api/v1/modules/"+id, nil); r.code != http.StatusNotFound {
		t.Fatalf("%s after uninstall: %d %s", id, r.code, r.body)
	}
}

// batch1ReposRoot returns a directory holding togavsu-{id} clones at main. Prefer
// TOGAVSU_BATCH1_MODS; otherwise clone read-only into the test temp dir. A missing
// network fails with an explicit message (never a silent skip).
func batch1ReposRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("TOGAVSU_BATCH1_MODS"); root != "" {
		return root
	}
	root := t.TempDir()
	for _, id := range batch1ModuleRepos {
		dest := filepath.Join(root, "togavsu-"+id)
		url := "https://github.com/CharitonMedia/togavsu-" + id + ".git"
		cmd := exec.Command("git", "clone", "--depth", "1", "--branch", "main", url, dest)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := string(out) + err.Error()
			if cu207NetworkMissing(msg) {
				t.Fatalf("CU2-07 requires network to clone %s (missing network): %v\n%s", url, err, out)
			}
			t.Fatalf("clone %s: %v\n%s", id, err, out)
		}
	}
	return root
}

func cu207NetworkMissing(msg string) bool {
	low := strings.ToLower(msg)
	for _, needle := range []string{
		"could not resolve host", "name or service not known", "network is unreachable",
		"temporary failure in name resolution", "no route to host", "connection timed out",
		"i/o timeout", "tls handshake timeout", "proxyconnect", "dial tcp",
	} {
		if strings.Contains(low, needle) {
			return true
		}
	}
	return false
}

func cu207RepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/core → repo root
	root := filepath.Clean(filepath.Join(wd, "../.."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s: %v", root, err)
	}
	return root
}

func cu207DBHostName(t *testing.T, dsn string) (host, name string) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host = u.Host
	if host == "" {
		t.Fatal("database URL has no host")
	}
	name = strings.TrimPrefix(u.Path, "/")
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		name = "postgres"
	}
	return host, name
}

func cu207GoBuild(t *testing.T, dir, out, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	outb, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(outb) + err.Error()
		if cu207NetworkMissing(msg) {
			t.Fatalf("CU2-07 requires network to go build %s (missing network): %v\n%s", pkg, err, outb)
		}
		t.Fatalf("go build %s in %s: %v\n%s", pkg, dir, err, outb)
	}
}

func cu207Package(t *testing.T, repoRoot, keyFile, pkgDir, outFile string, installable bool) {
	t.Helper()
	args := []string{"run", "./cmd/togavsu-module", "package"}
	if installable {
		args = append(args, "-installable")
	}
	args = append(args, "-dir", pkgDir, "-key", keyFile, "-out", outFile)
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	outb, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("togavsu-module package %s: %v\n%s", pkgDir, err, outb)
	}
}

// cu207StartProcess writes deployment secrets and starts the module binary out of
// process, returning a reverse proxy to its listen address for the testkit Runtime.
func cu207StartProcess(t *testing.T, bin string, m testkit.Module, dbHost, dbName string) http.Handler {
	t.Helper()
	secrets := t.TempDir()
	for name, val := range m.Deployment.Secrets {
		if err := os.WriteFile(filepath.Join(secrets, name), []byte(val), 0o600); err != nil {
			t.Fatalf("secret %s: %v", name, err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cmd := exec.Command(bin, "serve",
		"-listen", addr,
		"-core", m.CoreURL,
		"-secrets", secrets,
		"-db-host", dbHost,
		"-db-name", dbName,
	)
	cmd.Stdout = io.Discard
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", m.ID, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	base := "http://" + addr
	deadline := time.Now().Add(45 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("%s process exited early: %v\nstderr: %s", m.ID, err, stderr.String())
		default:
		}
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("%s process did not become healthy: %v\nstderr: %s", m.ID, err, stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	target, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})
}

