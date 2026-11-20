package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

func TestCoreUpdate2FPermissionReasons(t *testing.T) {
	x := newExt(t)
	x.trustKey("Synthetic reasons", x.priv.Public().(ed25519.PublicKey))
	reason := "Read <script>alert('synthetic')</script> & explain why."
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("permissions:\n"), []byte("permissions:\n  - scope: notes:read\n    reason: \""+reason+"\"\n    required: false\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatal(problem)
	}
	check := func() {
		t.Helper()
		page := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
		if !strings.Contains(html.UnescapeString(page.body), reason) || strings.Contains(page.body, "<script>alert") {
			t.Fatalf("reason missing or unescaped: %s", page.body)
		}
		api := x.admin.must(http.StatusOK, "GET", "/api/v1/modules/matter-clock", nil)
		found := false
		for _, p := range api.json["permissions"].([]any) {
			row := p.(map[string]any)
			if row["scope"] == "notes:read" {
				found = row["reason"] == reason
			}
		}
		if !found {
			t.Fatalf("API reason missing: %s", api.body)
		}
	}
	check()
	view, err := x.app.Modules.Get(context.Background(), x.adminP(), "matter-clock")
	if err != nil {
		t.Fatal(err)
	}
	decisions := []modules.Decision{}
	for _, p := range view.Permissions {
		d := "approve"
		if p.Scope == "notes:read" {
			d = "decline"
		}
		decisions = append(decisions, modules.Decision{Scope: p.Scope, Decision: d})
	}
	if _, err = x.app.Modules.DecidePermissions(context.Background(), x.adminP(), "matter-clock", decisions); err != nil {
		t.Fatal(err)
	}
	check()
	// The approval UI must use the candidate's signed reasons while an update waits.
	updated := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte(`version: "1.0.0"`), []byte(`version: "1.0.1"`), 1)
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("permissions:\n"), []byte("permissions:\n  - scope: notes:read\n    reason: Candidate reason.\n    required: false\n  - scope: contacts:read\n    reason: Another candidate reason.\n    required: false\n"), 1)
	})
	if _, err := x.app.Modules.Update(context.Background(), x.adminP(), "matter-clock", updated); err != nil {
		t.Fatal(err)
	}
	page := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if !strings.Contains(page.body, "Candidate reason.") || !strings.Contains(page.body, "Another candidate reason.") {
		t.Fatal("candidate reasons missing")
	}
}

func TestCoreUpdate2FProxyRestoresFormAfterCSRF(t *testing.T) {
	x := newExt(t)
	var mu sync.Mutex
	var bodies []string
	x.rt.Handle("matter-clock", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	x.trustKey("Synthetic proxy", x.priv.Public().(ed25519.PublicKey))
	x.installSample()
	post := func(body string) int {
		t.Helper()
		req, err := http.NewRequest("POST", x.srv.URL+"/m/matter-clock/autosave", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err := x.staff.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	body := "csrf=" + url.QueryEscape(x.staff.csrf) + "&answer=hello+world&answer=second%2Bvalue&empty="
	if code := post(body); code != http.StatusNoContent {
		t.Fatalf("proxy form: %d", code)
	}
	mu.Lock()
	if len(bodies) != 1 || bodies[0] != body {
		t.Errorf("forwarded bytes: %q", bodies)
	}
	mu.Unlock()
	for _, bad := range []string{"answer=missing", "csrf=wrong&answer=bad"} {
		if code := post(bad); code != http.StatusForbidden {
			t.Errorf("bad CSRF: %d", code)
		}
	}
	// ParseForm's existing 10 MiB ceiling must still reject oversized requests.
	if code := post(body + "&large=" + strings.Repeat("x", 10<<20)); code < 400 {
		t.Fatalf("oversized form accepted: %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("refused request forwarded: %d", len(bodies))
	}
}
