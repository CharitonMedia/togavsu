package supervisor

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaddySwitchPostsUpstreamAndRollsBack(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/load" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		if strings.Contains(got, "bad:8080") {
			http.Error(w, "no", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "upstream")
	proxy := &CaddyProxy{AdminURL: srv.URL, UpstreamFile: file, HTTP: srv.Client()}
	if err := proxy.Switch(context.Background(), "core-next:8080"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "core-next:8080") || strings.Contains(got, "core:8080") || !strings.Contains(got, ":443") {
		t.Fatalf("posted %s", got)
	}
	raw, err := os.ReadFile(file)
	if err != nil || strings.TrimSpace(string(raw)) != "core-next:8080" {
		t.Fatalf("file %q %v", raw, err)
	}
	if err := proxy.Switch(context.Background(), "bad:8080"); err == nil {
		t.Fatal("accepted a rejected load")
	}
	raw, err = os.ReadFile(file)
	if err != nil || strings.TrimSpace(string(raw)) != "core-next:8080" {
		t.Fatalf("rollback file %q %v", raw, err)
	}
	if err := proxy.Switch(context.Background(), "not a dial"); err == nil {
		t.Fatal("accepted an unsafe upstream")
	}
}

func TestCaddyConsecutiveSwitchesPreserveAdmin(t *testing.T) {
	bin := caddyBinary(t)
	admin := freePort(t)
	httpPort := freePort(t)
	httpsPort := freePort(t)
	dir := t.TempDir()
	initial := caddyConfig("core:8080", "127.0.0.1:"+admin, "127.0.0.1:"+httpPort, "127.0.0.1:"+httpsPort)
	raw, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "caddy.json")
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "--config", cfgPath)
	var logs strings.Builder
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	adminURL := "http://127.0.0.1:" + admin
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := http.Get(adminURL + "/config/")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("caddy admin did not start: %v\n%s", err, logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	proxy := &CaddyProxy{
		AdminURL:     adminURL,
		AdminListen:  "127.0.0.1:" + admin,
		HTTPListen:   "127.0.0.1:" + httpPort,
		HTTPSListen:  "127.0.0.1:" + httpsPort,
		UpstreamFile: filepath.Join(dir, "upstream"),
	}
	for _, upstream := range []string{"core-a:8080", "core-b:8080"} {
		if err := proxy.Switch(context.Background(), upstream); err != nil {
			t.Fatalf("switch %s: %v\n%s", upstream, err, logs.String())
		}
		res, err := http.Get(adminURL + "/config/apps/http/servers/https/routes/")
		if err != nil {
			t.Fatalf("admin unreachable after switch to %s: %v\n%s", upstream, err, logs.String())
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if !strings.Contains(string(body), upstream) {
			t.Fatalf("config after %s: %s", upstream, body)
		}
	}
	res, err := http.Get(adminURL + "/config/admin/listen")
	if err != nil {
		t.Fatalf("admin listen: %v\n%s", err, logs.String())
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "127.0.0.1:"+admin) {
		t.Fatalf("admin listen changed: %s", body)
	}
}

func caddyBinary(t *testing.T) string {
	t.Helper()
	if path, err := exec.LookPath("caddy"); err == nil {
		return path
	}
	cached := filepath.Join(os.TempDir(), "togavsu-caddy")
	if _, err := os.Stat(cached); err == nil {
		return cached
	}
	archive := filepath.Join(os.TempDir(), "caddy.tgz")
	if _, err := os.Stat(archive); err != nil {
		cmd := exec.Command("curl", "-fsSL", "-o", archive, "https://github.com/caddyserver/caddy/releases/download/v2.10.2/caddy_2.10.2_linux_amd64.tar.gz")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("download caddy: %v %s", err, out)
		}
	}
	cmd := exec.Command("tar", "-xzf", archive, "-C", os.TempDir(), "caddy")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extract caddy: %v %s", err, out)
	}
	extracted := filepath.Join(os.TempDir(), "caddy")
	if err := os.Rename(extracted, cached); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cached, 0o755); err != nil {
		t.Fatal(err)
	}
	return cached
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}
