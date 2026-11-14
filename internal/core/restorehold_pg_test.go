package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Codex PR #41 (restore file bytes): a core started while a restore's file bytes
// are not written back serves no user request until the supervisor says the hold
// is over. Only the health check and the supervisor's own file import get through,
// and no background worker writes in the meantime. A supervisor that does not
// answer keeps the core held.
func TestRestoreHoldKeepsUsersOutUntilTheFilesAreBack(t *testing.T) {
	var hold, broken atomic.Bool
	hold.Store(true)
	var asked atomic.Int32
	supSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+supSecret {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/restores/hold" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		asked.Add(1)
		if broken.Load() {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"hold": hold.Load()})
	}))
	defer supSrv.Close()
	dsn := testDSN(t)
	app, err := Open(context.Background(), Config{DatabaseURL: dsn, Key: testKey, Domain: "firm.example", DataDir: dataDir(t, dsn),
		SupervisorURL: supSrv.URL, SupervisorSecret: supSecret, NoBackground: true, MailEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	do := func(method, path, accept, token string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		return res.StatusCode, body.Code
	}
	mailStarted := func() bool {
		app.bgMu.Lock()
		defer app.bgMu.Unlock()
		return app.stopMail != nil
	}

	// Held: users get 503 restore_in_progress on every route, screens included.
	for _, c := range []struct{ method, path, accept string }{
		{"GET", "/api/v1/setup", ""},
		{"POST", "/api/v1/auth/login", ""},
		{"GET", "/api/v1/matters", ""},
		{"GET", "/mcp", ""},
		{"GET", fileBytesPath, ""},
		{"PUT", fileBytesPath, ""},
		{"GET", "/login", "text/html"},
		{"GET", "/", "text/html"},
	} {
		if code, _ := do(c.method, c.path, c.accept, ""); code != http.StatusServiceUnavailable {
			t.Fatalf("held %s %s = %d, want 503", c.method, c.path, code)
		}
	}
	if _, code := do("GET", "/api/v1/matters", "", ""); code != "restore_in_progress" {
		t.Fatalf("held error code = %q, want restore_in_progress", code)
	}
	if _, code := do("GET", "/api/v1/matters", "", "not-the-supervisor"); code != "restore_in_progress" {
		t.Fatalf("held with a wrong token: code = %q", code)
	}
	if code, _ := do("GET", fileBytesPath, "", "not-the-supervisor"); code != http.StatusServiceUnavailable {
		t.Fatalf("held file bytes with a wrong token = %d, want 503", code)
	}
	// The health check and the supervisor's file import get through.
	if code, _ := do("GET", "/api/v1/health", "", ""); code != http.StatusOK {
		t.Fatalf("held health = %d, want 200", code)
	}
	if code, _ := do("PUT", fileBytesPath, "", supSecret); code == http.StatusServiceUnavailable {
		t.Fatalf("held supervisor import was refused with 503")
	}
	if mailStarted() {
		t.Fatal("trust mail delivery started while held")
	}

	// A supervisor that stops answering keeps the core held.
	broken.Store(true)
	hold.Store(false)
	before := asked.Load()
	deadline := time.Now().Add(10 * time.Second)
	for asked.Load() < before+2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if code, _ := do("GET", "/api/v1/setup", "", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("supervisor down: setup = %d, want 503", code)
	}
	if mailStarted() {
		t.Fatal("trust mail delivery started while the supervisor was not answering")
	}

	// Released: the core opens and the background workers start.
	broken.Store(false)
	deadline = time.Now().Add(10 * time.Second)
	for {
		code, _ := do("GET", "/api/v1/setup", "", "")
		if code != http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the core did not open after the supervisor released the hold")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !mailStarted() {
		t.Fatal("trust mail delivery did not start once the hold was over")
	}
	n := asked.Load()
	time.Sleep(3 * time.Second)
	if asked.Load() != n {
		t.Fatal("the core kept asking the supervisor after the hold was over")
	}
}

// A core whose supervisor answers no hold at start opens at once.
func TestRestoreHoldOpensAtOnceWhenNothingHolds(t *testing.T) {
	supSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"hold": false})
	}))
	defer supSrv.Close()
	dsn := testDSN(t)
	app, err := Open(context.Background(), Config{DatabaseURL: dsn, Key: testKey, Domain: "firm.example", DataDir: dataDir(t, dsn),
		SupervisorURL: supSrv.URL, SupervisorSecret: supSecret, NoBackground: true, MailEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/api/v1/setup")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusServiceUnavailable {
		t.Fatal("a core with nothing holding it answered 503")
	}
}
