package supervisor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	initial := caddyConfig("core:8080", "127.0.0.1:"+admin, "127.0.0.1:"+httpPort, "127.0.0.1:"+httpsPort, "localhost")
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
	checkApplicationHTTP(t, "http://127.0.0.1:"+httpPort, &logs)
	for _, upstream := range []string{"core-a:8080", "core-b:8080"} {
		if err := proxy.Switch(context.Background(), upstream); err != nil {
			t.Fatalf("switch %s: %v\n%s", upstream, err, logs.String())
		}
		checkApplicationHTTP(t, "http://127.0.0.1:"+httpPort, &logs)
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

func TestHTTPSHandshakeBeforeAndAfterSwitch(t *testing.T) {
	bin := caddyBinary(t)
	domain := "firm.example"
	admin := freePort(t)
	httpPort := freePort(t)
	httpsPort := freePort(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "caddy-data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	initial := caddyConfig("core:8080", "127.0.0.1:"+admin, "127.0.0.1:"+httpPort, "127.0.0.1:"+httpsPort, domain)
	raw, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "caddy.json")
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "--config", cfgPath)
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+data, "HOME="+filepath.Join(dir, "home"))
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
	waitHTTP(t, adminURL+"/config/", &logs)
	checkApplicationHTTP(t, "http://127.0.0.1:"+httpPort, &logs)
	pool := trustCaddyRoot(t, data, &logs)
	checkHTTPS(t, httpsPort, domain, pool, &logs)
	proxy := &CaddyProxy{
		AdminURL:     adminURL,
		AdminListen:  "127.0.0.1:" + admin,
		HTTPListen:   "127.0.0.1:" + httpPort,
		HTTPSListen:  "127.0.0.1:" + httpsPort,
		UpstreamFile: filepath.Join(dir, "upstream"),
		Domain:       domain,
	}
	if err := proxy.Switch(context.Background(), "core-next:8080"); err != nil {
		t.Fatalf("switch: %v\n%s", err, logs.String())
	}
	checkHTTPS(t, httpsPort, domain, pool, &logs)
	res, err := http.Get(adminURL + "/config/apps/http/servers/https/routes/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "core-next:8080") || !strings.Contains(string(body), domain) {
		t.Fatalf("switched https config %s", body)
	}
}

func waitHTTP(t *testing.T, url string, logs *strings.Builder) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var err error
	for {
		var res *http.Response
		res, err = http.Get(url)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("caddy admin did not start: %v\n%s", err, logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func trustCaddyRoot(t *testing.T, data string, logs *strings.Builder) *x509.CertPool {
	t.Helper()
	path := filepath.Join(data, "caddy", "pki", "authorities", "local", "root.crt")
	deadline := time.Now().Add(15 * time.Second)
	var raw []byte
	var err error
	for {
		raw, err = os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("local CA %s: %v\n%s", path, err, logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		t.Fatal("local CA was not a certificate")
	}
	return pool
}

func checkHTTPS(t *testing.T, port, domain string, pool *x509.CertPool, logs *strings.Builder) {
	t.Helper()
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			ServerName: domain,
		}},
	}
	deadline := time.Now().Add(15 * time.Second)
	var err error
	for {
		req, reqErr := http.NewRequest(http.MethodGet, "https://"+domain+"/ready", nil)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		client.Transport = &http.Transport{
			DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
				return tls.Dial("tcp", "127.0.0.1:"+port, &tls.Config{RootCAs: pool, ServerName: domain})
			},
		}
		var res *http.Response
		res, err = client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode == http.StatusOK && string(body) == "ok" && res.TLS != nil && len(res.TLS.PeerCertificates) > 0 {
				names := res.TLS.PeerCertificates[0].DNSNames
				found := false
				for _, name := range names {
					if name == domain {
						found = true
					}
				}
				if !found {
					t.Fatalf("certificate names %v\n%s", names, logs.String())
				}
				return
			}
			err = fmt.Errorf("status %d body %q", res.StatusCode, body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("https handshake: %v\n%s", err, logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSwitchRollsBackAppliedLoadWhenResponseIsLost(t *testing.T) {
	var mu sync.Mutex
	var live []byte
	var loads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config/", "/config":
			mu.Lock()
			body := append([]byte(nil), live...)
			mu.Unlock()
			if len(body) == 0 {
				raw, _ := json.Marshal(caddyConfig("core:8080", "127.0.0.1:2019", ":80", ":443", "localhost"))
				body = raw
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		case "/load":
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			loads++
			n := loads
			live = append([]byte(nil), raw...)
			mu.Unlock()
			if n == 1 {
				hj, ok := w.(http.Hijacker)
				if !ok {
					t.Fatal("server cannot hijack")
				}
				conn, _, err := hj.Hijack()
				if err != nil {
					t.Fatal(err)
				}
				conn.Close()
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	proxy := &CaddyProxy{AdminURL: srv.URL, UpstreamFile: filepath.Join(t.TempDir(), "upstream"), HTTP: srv.Client()}
	err := proxy.Switch(context.Background(), "core-next:8080")
	if err == nil {
		t.Fatal("lost response was treated as success")
	}
	if errors.Is(err, ErrProxyUnreconciled) {
		t.Fatalf("rollback did not reconcile the live config: %v", err)
	}
	mu.Lock()
	got := string(live)
	mu.Unlock()
	if strings.Contains(got, "core-next:8080") || !strings.Contains(got, "core:8080") {
		t.Fatalf("live config after lost response: %s", got)
	}
}

func TestPersistFailureAfterLoadDoesNotRemoveServingCore(t *testing.T) {
	live, srv := loadServer(t, false)
	defer srv.Close()
	dir := t.TempDir()
	blocked := filepath.Join(dir, "upstream")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{
		ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	up := testUpdater(t, rt, &order)
	up.Proxy = &CaddyProxy{AdminURL: srv.URL, UpstreamFile: blocked, HTTP: srv.Client()}
	_, err := up.Apply(context.Background(), true)
	if err == nil || errors.Is(err, ErrProxyUnreconciled) {
		t.Fatalf("expected a reconciled file error, got %v", err)
	}
	if containsStep(order, "stop:old") || containsStep(order, "remove:old") {
		t.Fatalf("removed the core Caddy rolled back to: %v", order)
	}
	if strings.Contains(live(), "core:8080") == false || strings.Contains(live(), ":8080") && dialIsNew(live(), order) {
		t.Fatalf("live config %s order %v", live(), order)
	}
}

func TestPersistFailureWithoutRollbackKeepsNewCore(t *testing.T) {
	live, srv := loadServer(t, true)
	defer srv.Close()
	dir := t.TempDir()
	blocked := filepath.Join(dir, "upstream")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{
		ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	up := testUpdater(t, rt, &order)
	up.Proxy = &CaddyProxy{AdminURL: srv.URL, UpstreamFile: blocked, HTTP: srv.Client()}
	_, err := up.Apply(context.Background(), true)
	if !errors.Is(err, ErrProxyUnreconciled) {
		t.Fatalf("got %v", err)
	}
	name := startedName(order)
	if name == "" {
		t.Fatalf("no new core: %v", order)
	}
	if containsStep(order, "stop:new-"+name) || containsStep(order, "remove:new-"+name) {
		t.Fatalf("removed the core Caddy still serves: %v", order)
	}
	if !strings.Contains(live(), name+":8080") {
		t.Fatalf("live config lost the serving upstream: %s", live())
	}
}

func loadServer(t *testing.T, rejectRollback bool) (func() string, *httptest.Server) {
	t.Helper()
	var mu sync.Mutex
	var body []byte
	var loads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config/", "/config":
			mu.Lock()
			raw := append([]byte(nil), body...)
			mu.Unlock()
			if len(raw) == 0 {
				raw, _ = json.Marshal(caddyConfig("core:8080", "127.0.0.1:2019", ":80", ":443", "localhost"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
		case "/load":
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			loads++
			n := loads
			if n == 1 || !rejectRollback {
				body = append([]byte(nil), raw...)
			}
			mu.Unlock()
			if n > 1 && rejectRollback {
				http.Error(w, "no", http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return string(body)
	}, srv
}

func startedName(order []string) string {
	for _, step := range order {
		if strings.HasPrefix(step, "name:") {
			return strings.TrimPrefix(step, "name:")
		}
	}
	return ""
}

func dialIsNew(live string, order []string) bool {
	name := startedName(order)
	return name != "" && strings.Contains(live, name+":8080")
}

func checkApplicationHTTP(t *testing.T, base string, logs *strings.Builder) {
	t.Helper()
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	ready, err := client.Get(base + "/ready")
	if err != nil {
		t.Fatalf("ready: %v\n%s", err, logs.String())
	}
	ready.Body.Close()
	if ready.StatusCode != http.StatusOK {
		t.Fatalf("ready status %d\n%s", ready.StatusCode, logs.String())
	}
	app, err := client.Get(base + "/matters/12")
	if err != nil {
		t.Fatalf("application path: %v\n%s", err, logs.String())
	}
	app.Body.Close()
	if app.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("application path status %d, want 308\n%s", app.StatusCode, logs.String())
	}
	loc := app.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://") || !strings.Contains(loc, "/matters/12") {
		t.Fatalf("application redirect %q\n%s", loc, logs.String())
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
