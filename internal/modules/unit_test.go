package modules_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

func code(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func sampleManifest(t *testing.T) []byte {
	t.Helper()
	files, err := matterclock.Files()
	if err != nil {
		t.Fatal(err)
	}
	return files[modules.ManifestFile]
}

func TestSampleManifestParses(t *testing.T) {
	m, err := modules.ParseManifest(sampleManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "matter-clock" || m.Version != "1.0.0" || m.Schema() != "mod_matter_clock" {
		t.Fatalf("manifest: %+v", m)
	}
	if len(m.Widgets) != 1 || m.Widgets[0].Target == nil || m.Widgets[0].Target.Tab != "hours" {
		t.Fatalf("widget: %+v", m.Widgets)
	}
	ws, err := m.DashboardWidgets()
	if err != nil || ws[0].Target != "/modules/matter-clock/tabs/hours" || ws[0].W != 4 {
		t.Fatalf("dashboard widget %+v %v", ws, err)
	}
	if !strings.Contains(m.Help.Pages[0].Body, "Hours this month") || !strings.HasSuffix(m.Help.Pages[0].Body, "\n") {
		t.Fatalf("block text: %q", m.Help.Pages[0].Body)
	}
	if got := m.Data.Extends[0]; got.Name != "targets" || got.Table != "matter_targets" || !got.Columns[1].AttorneyOnly {
		t.Fatalf("extends: %+v", got)
	}
	if m.Data.Views[0].Name != "monthly_targets" || !m.Permissions[0].IsRequired() {
		t.Fatalf("views/permissions: %+v %+v", m.Data.Views, m.Permissions)
	}
}

func TestYAMLSubsetRefusals(t *testing.T) {
	base := string(sampleManifest(t))
	cases := map[string]string{
		"anchor":        strings.Replace(base, "name: Matter Clock", "name: &a Matter Clock", 1),
		"tab indent":    strings.Replace(base, "  image:", "\timage:", 1),
		"duplicate key": base + "\nname: Again\n",
		"flow map":      strings.Replace(base, "    size:\n      w: 4\n      h: 2\n", "    size: {w: 4, h: 2}\n", 1),
		"unknown key":   base + "\nsurprise: true\n",
		"two documents": base + "\n---\nid: other\n",
		"lone {":        strings.Replace(base, "name: Matter Clock", "name: {", 1),
	}
	for name, src := range cases {
		if _, err := modules.ParseManifest([]byte(src)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestManifestRefusals(t *testing.T) {
	base := string(sampleManifest(t))
	cases := map[string]struct{ from, to string }{
		// AC-011: a widget with no click-through target is refused at install.
		"widget without target": {"    target:\n      tab: hours\n      params: [matter_id]\n", ""},
		"target tab undeclared": {"      tab: hours\n      params", "      tab: nowhere\n      params"},
		"forbidden scope":       {"  - scope: events:read\n", "  - scope: tokens:write\n"},
		"events need scope":     {"  - scope: events:read\n    reason: Hear about new time entries and matters.\n", ""},
		"bad id":                {"id: matter-clock", "id: Matter_Clock"},
		"bad replaces":          {"settings:\n", "replaces: notes\nsettings:\n"},
		"trust needs flag":      {"settings:\n", "replaces: trust\nsettings:\n"},
		"unquoted version":      {`version: "1.0.0"`, "version: 1.0"},
		"no digest":             {"  digest: sha256:", "  digest: md5:"},
		"page outside module":   {"    path: /widget", "    path: //evil.example/widget"},
		"unknown record":        {"      record: matter\n      table", "      record: wiki\n      table"},
	}
	for name, c := range cases {
		if !strings.Contains(base, c.from) {
			t.Fatalf("%s: fixture text missing", name)
		}
		_, err := modules.ParseManifest([]byte(strings.Replace(base, c.from, c.to, 1)))
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := strings.Replace(base, "settings:\n", "replaces: trust\nemits: [trust.flag_raised]\nsettings:\n", 1)
	if _, err := modules.ParseManifest([]byte(ok)); err != nil {
		t.Fatalf("trust replacement with flag: %v", err)
	}
}

// Signature checks belong to the marketplace component (Deps.Signing); ReadPackage only
// unpacks bytes it verified and parses the manifest.
func TestReadPackage(t *testing.T) {
	raw, err := matterclock.Archive(nil)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := modules.ReadPackage(raw)
	if err != nil || pkg.Manifest.ID != "matter-clock" || !strings.HasPrefix(pkg.Digest, "sha256:") {
		t.Fatalf("read %+v %v", pkg, err)
	}
	noManifest, _ := matterclock.Archive(func(f map[string][]byte) { delete(f, modules.ManifestFile) })
	if _, err := modules.ReadPackage(noManifest); err == nil {
		t.Fatal("package without a manifest accepted")
	}
	if _, err := modules.ReadPackage([]byte("not a tarball")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestPackageMigrations(t *testing.T) {
	raw, err := matterclock.Archive(func(f map[string][]byte) {
		f["migrations/0002_more.sql"] = []byte("CREATE INDEX received_events_action_idx ON received_events (action);")
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := modules.ReadPackage(raw)
	ms, err := pkg.Migrations()
	if err != nil || len(ms) != 2 || ms[0].Version != 1 || ms[1].Version != 2 || ms[0].Checksum == "" {
		t.Fatalf("migrations %+v %v", ms, err)
	}
	raw, _ = matterclock.Archive(func(f map[string][]byte) { f["migrations/bad.sql"] = []byte("SELECT 1") })
	pkg, _ = modules.ReadPackage(raw)
	if _, err := pkg.Migrations(); code(err) != modules.CodeMigration {
		t.Fatalf("badly named migration: %v", err)
	}
}

func TestWebhookSignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"id":"e1"}`)
	sig := modules.SignWebhook("s3cret", now, body)
	if err := modules.VerifyWebhook("s3cret", sig, body, now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := modules.VerifyWebhook("other", sig, body, now, time.Minute); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if err := modules.VerifyWebhook("s3cret", sig, []byte(`{"id":"e2"}`), now, time.Minute); err == nil {
		t.Fatal("changed body accepted")
	}
	if err := modules.VerifyWebhook("s3cret", sig, body, now.Add(time.Hour), time.Minute); err == nil {
		t.Fatal("stale signature accepted")
	}
}

func TestSubscriptionsAndDomains(t *testing.T) {
	subs := []string{"time_entry.*", "matter.created"}
	for action, want := range map[string]bool{"time_entry.created": true, "time_entry.updated": true, "matter.created": true, "matter.updated": false, "time_entryx.created": false} {
		if modules.Subscribed(subs, action) != want {
			t.Errorf("%s: want %v", action, want)
		}
	}
	for path, want := range map[string]string{
		"/api/v1/calendar-events": "calendar", "/api/v1/calendar-events/x": "calendar", "/api/v1/deadlines:bulk": "calendar",
		"/calendar": "calendar", "/calendar/deadlines": "calendar", "/api/v1/trust-entries": "trust", "/trust/ledger": "trust",
		"/api/v1/time-entries": "time", "/api/v1/matters": "", "/calendarx": "", "/api/v1/modules": "",
	} {
		if got := modules.DomainOf(path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

func TestSupervisorClient(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/v1/backups" {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"b1","sha256":"abc","bytes":10,"created_at":"2026-11-11T00:00:00Z"}`)
			return
		}
		if r.Method == http.MethodPut {
			var d modules.Deployment
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil || d.Digest == "" || d.Secrets["togavsu_api_token"] != "tok" {
				http.Error(w, "bad deployment", http.StatusBadRequest)
				return
			}
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	s := &modules.Supervisor{URL: srv.URL, Secret: "local-secret", ModuleURL: "http://togavsu-mod-%s:%d", Ports: func(string) int { return 8080 }}
	ctx := context.Background()
	if err := s.Deploy(ctx, modules.Deployment{Module: "matter-clock", Digest: "sha256:x", Secrets: map[string]string{"togavsu_api_token": "tok"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, "matter-clock"); err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, "before deleting module data")
	if err != nil || b.ID != "b1" || b.SHA256 != "abc" {
		t.Fatalf("backup: %+v %v", b, err)
	}
	want := "PUT /v1/modules/matter-clock,POST /v1/modules/matter-clock/start,POST /v1/modules/matter-clock/stop,POST /v1/backups"
	if got := strings.Join(calls, ","); got != want {
		t.Fatalf("calls %s", got)
	}
	if s.BaseURL("matter-clock") != "http://togavsu-mod-matter-clock:8080" {
		t.Fatal(s.BaseURL("matter-clock"))
	}
	bad := &modules.Supervisor{URL: srv.URL, Secret: "wrong"}
	if err := bad.Stop(ctx, "matter-clock"); err == nil {
		t.Fatal("wrong supervisor secret accepted")
	}
}
