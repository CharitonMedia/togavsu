package core

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/testkit"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCoreUpdate2GFrameSessionCSRF(t *testing.T) {
	x := newExt(t)
	var posts atomic.Int32
	x.rt.Handle("matter-clock", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			fmt.Fprintf(w, `<form method="post"><input name="csrf" value="%s"></form>`, html.EscapeString(r.Header.Get("X-Togavsu-Session-CSRF")))
			return
		}
		posts.Add(1)
		w.WriteHeader(204)
	}))
	x.trustKey("Synthetic frame", x.priv.Public().(ed25519.PublicKey))
	x.installSample()
	_, contactID := x.matter("Synthetic frame client", "frame-client@synthetic.example", nil)
	cliKit, _ := testkit.EnrollUser(t, x.app.Identity, x.srv.URL, x.admin.kit(), "Frame Client", "frame-client@synthetic.example", domain.ID(contactID), domain.RoleClient)
	cli := wrapClient(cliKit)
	for _, person := range []*client{x.staff, cli} {
		req, _ := http.NewRequest("GET", x.srv.URL+"/m/matter-clock/review", nil)
		req.Header.Set("X-Togavsu-Session-CSRF", "forged")
		req.Header.Set("X-CSRF-Token", "forged")
		res, err := person.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		match := regexp.MustCompile(`value="([^"]+)"`).FindSubmatch(b)
		if len(match) != 2 {
			t.Fatalf("no frame token: %s", b)
		}
		frameToken := string(match[1])
		if frameToken == person.csrf || frameToken == "forged" || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("frame did not get current session token privately: %s, cache %s", b, res.Header.Get("Cache-Control"))
		}
		for _, token := range []string{frameToken, "", map[*client]string{x.staff: cli.csrf, cli: x.staff.csrf}[person]} {
			form := url.Values{"csrf": {token}, "answer": {"synthetic"}}
			req, _ := http.NewRequest("POST", x.srv.URL+"/m/matter-clock/review", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			res, err := person.http.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			want := 403
			if token == frameToken {
				want = 204
			}
			if res.StatusCode != want {
				t.Errorf("form status %d want %d", res.StatusCode, want)
			}
		}
		for _, path := range []string{"/api/v1/events", "/m/another-module/review"} {
			req, _ := http.NewRequest("POST", x.srv.URL+path, strings.NewReader("{}"))
			req.Header.Set("X-CSRF-Token", frameToken)
			res, err := person.http.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var problem struct {
				Code string `json:"code"`
			}
			if path == "/api/v1/events" {
				if err := json.NewDecoder(res.Body).Decode(&problem); err != nil {
					t.Fatal(err)
				}
				if problem.Code != "csrf_required" {
					t.Errorf("core write refused for wrong reason: %q", problem.Code)
				}
			}
			res.Body.Close()
			if res.StatusCode != 403 {
				t.Errorf("frame token accepted by %s: %d", path, res.StatusCode)
			}
		}
		other := x.staff
		if person == x.staff {
			other = cli
		}
		req, _ = http.NewRequest("POST", x.srv.URL+"/m/matter-clock/review", nil)
		req.Header.Set("X-CSRF-Token", frameToken)
		res, err = other.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Errorf("other session accepted frame token: %d", res.StatusCode)
		}
	}
	req, _ := http.NewRequest("GET", x.srv.URL+"/m/matter-clock/review", nil)
	req.Header.Set("X-Togavsu-Session-CSRF", x.staff.csrf)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(b), x.staff.csrf) || strings.Contains(string(b), x.admin.csrf) {
		t.Fatal("unauthenticated frame leaked token")
	}
	if posts.Load() != 2 {
		t.Fatalf("rejected form forwarded: %d", posts.Load())
	}
}
