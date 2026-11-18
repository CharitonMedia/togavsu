package modules

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSignedPublicCSRFRoundTrip(t *testing.T) {
	secret := []byte("synthetic-webhook-secret")
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	tok, err := IssueSignedPublicCSRF(secret, "intake-mod", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSignedPublicCSRF(secret, "intake-mod", tok, tok, now); err != nil {
		t.Fatalf("cookie+submitted: %v", err)
	}
	// Opaque-origin HTMX may omit the cookie.
	if err := CheckSignedPublicCSRF(secret, "intake-mod", "", tok, now.Add(time.Minute)); err != nil {
		t.Fatalf("header only: %v", err)
	}
	if err := CheckSignedPublicCSRF(secret, "intake-mod", "", "not-a-token", now); err == nil {
		t.Fatal("bad token accepted")
	}
	if err := CheckSignedPublicCSRF(secret, "other-mod", "", tok, now); err == nil {
		t.Fatal("wrong module accepted")
	}
	if err := CheckSignedPublicCSRF(secret, "intake-mod", "other", tok, now); err == nil {
		t.Fatal("mismatched cookie accepted")
	}
}

func TestClientIPProxyAndIPv6(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[2001:db8::1]:443"
	if got := clientIP(req); got != "2001:db8::1" {
		t.Fatalf("IPv6 RemoteAddr: got %q", got)
	}
	// Public peer: ignore forged XFF.
	req.RemoteAddr = "198.51.100.9:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.1")
	if got := clientIP(req); got != "198.51.100.9" {
		t.Fatalf("public peer must ignore XFF: got %q", got)
	}
	// Loopback/docker peer: trust right-most XFF hop.
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 203.0.113.50")
	if got := clientIP(req); got != "203.0.113.50" {
		t.Fatalf("proxy peer right-most XFF: got %q", got)
	}
	req.RemoteAddr = "10.0.0.2:80"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := clientIP(req); got != "198.51.100.1" {
		t.Fatalf("private peer XFF: got %q", got)
	}
}

func TestBannerBridgeRejectsOriginFilter(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	body, err := os.ReadFile(filepath.Join(root, "web", "static", "banner-bridge.js"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if strings.Contains(s, "ev.origin") {
		t.Fatal("banner-bridge.js must not filter on ev.origin")
	}
	if !strings.Contains(s, "fromBanner") || !strings.Contains(s, "ev.source") {
		t.Fatal("banner-bridge.js must match iframe contentWindow via fromBanner")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available for behavioural banner-bridge check")
	}
	cmd := exec.Command(node, filepath.Join(root, "web", "static", "banner-bridge_test.js"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("banner-bridge behavioural: %v\n%s", err, out)
	}
}
