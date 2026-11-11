package modules_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

func newKey(t *testing.T) (ed25519.PrivateKey, modules.TrustedKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, modules.TrustedKey{KeyID: modules.KeyID(pub), PublicKey: pub, Source: "local"}
}

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

func TestSignatureRefusals(t *testing.T) {
	priv, key := newKey(t)
	raw, err := matterclock.Build(priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := modules.ReadPackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.Verify([]modules.TrustedKey{key}); err != nil {
		t.Fatalf("signed package refused: %v", err)
	}

	files, _ := matterclock.Files()
	unsigned, _ := modules.Archive(files)
	pkg, _ = modules.ReadPackage(unsigned)
	if _, err := pkg.Verify([]modules.TrustedKey{key}); code(err) != modules.CodeUnsigned {
		t.Fatalf("unsigned: %v", err)
	}

	otherPriv, _ := newKey(t)
	foreign, _ := matterclock.Build(otherPriv, nil)
	pkg, _ = modules.ReadPackage(foreign)
	if _, err := pkg.Verify([]modules.TrustedKey{key}); code(err) != modules.CodeUnsigned {
		t.Fatalf("untrusted signer: %v", err)
	}

	// Tampered: change a signed file after signing and repack with the old signature.
	signed, _ := modules.ReadPackage(raw)
	signed.Files["migrations/0001_matter_targets.sql"] = append(signed.Files["migrations/0001_matter_targets.sql"], []byte("\nDROP TABLE matter_targets;")...)
	tampered, _ := modules.Archive(signed.Files)
	pkg, _ = modules.ReadPackage(tampered)
	if _, err := pkg.Verify([]modules.TrustedKey{key}); code(err) != modules.CodeTampered {
		t.Fatalf("tampered migration: %v", err)
	}
	signed, _ = modules.ReadPackage(raw)
	signed.Files["extra.txt"] = []byte("smuggled")
	added, _ := modules.Archive(signed.Files)
	pkg, _ = modules.ReadPackage(added)
	if _, err := pkg.Verify([]modules.TrustedKey{key}); code(err) != modules.CodeTampered {
		t.Fatalf("added file: %v", err)
	}
	if _, err := modules.ReadPackage([]byte("not a tarball")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func migrations(t *testing.T, sqls ...string) []db.Migration {
	t.Helper()
	var out []db.Migration
	for i, s := range sqls {
		out = append(out, db.Migration{Version: i + 1, Name: "m", SQL: s, Checksum: "x"})
	}
	return out
}

func TestMigrationCheck(t *testing.T) {
	m, err := modules.ParseManifest(sampleManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	files, _ := matterclock.Files()
	sample := string(files["migrations/0001_matter_targets.sql"])
	if err := modules.CheckMigrations(m, migrations(t, sample)); err != nil {
		t.Fatalf("sample refused: %v", err)
	}
	ok := []string{
		"CREATE INDEX received_events_action_idx ON received_events (action);",
		"ALTER TABLE matter_targets ADD COLUMN reviewed_by TEXT REFERENCES core.users (id) ON DELETE RESTRICT;",
		"CREATE TABLE mod_matter_clock.notes_cache (id TEXT PRIMARY KEY, body TEXT);",
		"CREATE TABLE client_contacts (contact_id TEXT NOT NULL REFERENCES core.contacts (id) ON DELETE RESTRICT, full_name TEXT, email TEXT);",
		"DROP VIEW IF EXISTS monthly_targets; CREATE VIEW monthly_targets AS SELECT matter_id, monthly_target_tenths FROM matter_targets;",
		"-- a comment; with a semicolon\nCREATE TABLE x1 (id TEXT PRIMARY KEY, label TEXT DEFAULT 'a;b');",
	}
	for _, extra := range ok {
		if err := modules.CheckMigrations(m, migrations(t, sample, extra)); err != nil {
			t.Errorf("allowed statement refused: %s: %v", extra, err)
		}
	}
	refused := map[string]string{
		"own contacts table":       "CREATE TABLE contacts (id TEXT PRIMARY KEY, name TEXT);",
		"quoted contacts table":    `CREATE TABLE "Contacts" (id TEXT PRIMARY KEY);`,
		"synonym people":           "CREATE TABLE people (id TEXT PRIMARY KEY);",
		"rename to matters":        "ALTER TABLE received_events RENAME TO matters;",
		"identifying copy":         "CREATE TABLE leads (id TEXT PRIMARY KEY, full_name TEXT, email TEXT, phone TEXT);",
		"table in core":            "CREATE TABLE core.module_stuff (id TEXT PRIMARY KEY);",
		"quoted core schema":       `CREATE TABLE "core"."x" (id TEXT PRIMARY KEY);`,
		"alter core table":         "ALTER TABLE core.matters ADD COLUMN hours INTEGER;",
		"other module schema":      "CREATE TABLE mod_other.x (id TEXT PRIMARY KEY);",
		"insert into core":         "INSERT INTO core.contacts (id) VALUES ('x');",
		"update own table":         "UPDATE matter_targets SET note = '';",
		"index on core":            "CREATE INDEX x ON core.contacts (id);",
		"trigger":                  "CREATE TRIGGER t BEFORE INSERT ON matter_targets FOR EACH ROW EXECUTE FUNCTION f();",
		"function":                 "CREATE FUNCTION f() RETURNS trigger AS $$ BEGIN RETURN NEW; END $$ LANGUAGE plpgsql;",
		"function without dollars": "CREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE sql AS 'SELECT 1';",
		"extension":                "CREATE EXTENSION pg_trgm;",
		"role":                     "CREATE ROLE sneaky LOGIN;",
		"grant":                    "GRANT SELECT ON matter_targets TO PUBLIC;",
		"revoke":                   "REVOKE ALL ON matter_targets FROM PUBLIC;",
		"do block":                 "DO 'BEGIN END';",
		"set search_path":          "SET search_path TO core;",
		"schema":                   "CREATE SCHEMA other;",
		"view over core":           "CREATE VIEW leak AS SELECT * FROM core.contacts;",
		"view join core":           "CREATE VIEW leak2 AS SELECT t.matter_id FROM matter_targets t JOIN core.matters m ON m.id = t.matter_id;",
		"view over function":       "CREATE VIEW leak3 AS SELECT * FROM pg_read_file('/etc/passwd');",
		"fk cascade":               "CREATE TABLE a1 (matter_id TEXT REFERENCES core.matters (id) ON DELETE CASCADE);",
		"fk without restrict":      "CREATE TABLE a2 (matter_id TEXT REFERENCES core.matters (id));",
		"fk to non-id":             "CREATE TABLE a3 (n TEXT REFERENCES core.matters (number) ON DELETE RESTRICT);",
		"fk to secret table":       "CREATE TABLE a4 (t TEXT REFERENCES core.api_tokens (id) ON DELETE RESTRICT);",
		"default calls core":       "CREATE TABLE a5 (id TEXT DEFAULT core.secret_fn());",
		"table as select":          "CREATE TABLE copy AS SELECT * FROM matter_targets;",
		"drop cascade":             "DROP TABLE received_events CASCADE;",
		"owner change":             "ALTER TABLE matter_targets OWNER TO postgres;",
		"set schema":               "ALTER TABLE matter_targets SET SCHEMA public;",
		"disable trigger":          "ALTER TABLE matter_targets DISABLE TRIGGER ALL;",
		"comment hides nothing":    "/* harmless */ CREATE TRIGGER t2 AFTER INSERT ON matter_targets EXECUTE FUNCTION f();",
		"index concurrently":       "CREATE INDEX CONCURRENTLY y ON matter_targets (note);",
		"copy":                     "COPY matter_targets FROM '/etc/passwd';",
	}
	for name, extra := range refused {
		err := modules.CheckMigrations(m, migrations(t, sample, extra))
		if code(err) != modules.CodeMigration {
			t.Errorf("%s: not refused (%v)", name, err)
		}
	}
	// The manifest's extension table must be keyed by the core record's id.
	bad := strings.Replace(sample, "matter_id TEXT PRIMARY KEY REFERENCES core.matters (id) ON DELETE RESTRICT", "id TEXT PRIMARY KEY, matter_id TEXT REFERENCES core.matters (id) ON DELETE RESTRICT", 1)
	if err := modules.CheckMigrations(m, migrations(t, bad)); code(err) != modules.CodeMigration {
		t.Fatalf("extension without the record key: %v", err)
	}
	noView := strings.Replace(sample, "CREATE VIEW monthly_targets", "CREATE VIEW other_targets", 1)
	if err := modules.CheckMigrations(m, migrations(t, noView)); code(err) != modules.CodeMigration {
		t.Fatalf("published view missing: %v", err)
	}
}

func TestPackageMigrations(t *testing.T) {
	priv, _ := newKey(t)
	raw, err := matterclock.Build(priv, func(f map[string][]byte) {
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
	raw, _ = matterclock.Build(priv, func(f map[string][]byte) { f["migrations/bad.sql"] = []byte("SELECT 1") })
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
