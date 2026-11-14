package modules_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

const sampleMigration = "migrations/0001_matter_targets.sql"

func withVersion(f map[string][]byte, v string) {
	f[modules.ManifestFile] = []byte(strings.Replace(string(f[modules.ManifestFile]), `version: "1.0.0"`, `version: "`+v+`"`, 1))
}

// AC-037: packages whose migrations break the PRD 9.9 rules are refused at install, with
// nothing created.
func TestPGRefusedMigrationsAtInstall(t *testing.T) {
	e := newEnv(t)
	cases := map[string]string{
		"own contacts table":   "CREATE TABLE contacts (id TEXT PRIMARY KEY, full_name TEXT, email TEXT);",
		"writes outside":       "CREATE TABLE core.matter_clock_side (id TEXT PRIMARY KEY);",
		"alters a core table":  "ALTER TABLE core.matters ADD COLUMN clock_hours INTEGER;",
		"adds a trigger":       "CREATE TRIGGER clock_t BEFORE INSERT ON matter_targets FOR EACH ROW EXECUTE FUNCTION clock_f();",
		"adds a function":      "CREATE FUNCTION clock_f() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$;",
		"copies a core record": "CREATE TABLE people_cache (id TEXT PRIMARY KEY, name TEXT, phone TEXT);",
	}
	for name, extra := range cases {
		raw := e.build(func(f map[string][]byte) { f["migrations/0002_bad.sql"] = []byte(extra) })
		r := e.do("POST", "/api/v1/modules", raw, e.as("admin")...)
		if r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeMigration {
			t.Errorf("%s: %d %s", name, r.code, r.body)
		}
	}
	if e.count(`SELECT COUNT(*) FROM core.modules`) != 0 || e.schemaExists("matter-clock") {
		t.Fatal("a refused package left a record or a schema")
	}
}

// The migration runs as the module's own role, so a statement the parser misread still
// can't reach core tables: defense in depth behind the install check.
func TestPGMigrationRoleCannotTouchCore(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	role := e.rt.Last("matter-clock").Secrets["togavsu_db_role"]
	for _, stmt := range []string{
		"CREATE TABLE core.evil (id TEXT)",
		"INSERT INTO core.settings (id) VALUES ('x')",
		"SELECT * FROM core.contacts",
	} {
		_, err := e.svc.Schemas().Migrate(e.ctx, "matter-clock", role, []db.Migration{
			{Version: 1, Name: "0001_matter_targets", SQL: "", Checksum: firstChecksum(t, e)},
			{Version: 90, Name: "0090_evil", SQL: stmt, Checksum: "evil"},
		})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("%s ran as the module role: %v", stmt, err)
		}
	}
	if e.count(`SELECT COUNT(*) FROM core.schema_migrations WHERE owner = 'mod_matter_clock' AND version = 90`) != 0 {
		t.Fatal("a failed migration was recorded")
	}
}

func firstChecksum(t *testing.T, e *env) string {
	t.Helper()
	applied, err := e.svc.Schemas().Applied(e.ctx, "matter-clock")
	if err != nil {
		t.Fatal(err)
	}
	return applied[1]
}

// AC-037 and AC-033: a changed migration that already ran is refused on update; a
// failing migration rolls back and the old version keeps running; a good update applies
// its migration and refreshes the guide, all without a restart.
func TestPGUpdates(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	running := e.rt.Last("matter-clock")

	changed := e.build(func(f map[string][]byte) {
		withVersion(f, "1.1.0")
		f[sampleMigration] = append(f[sampleMigration], []byte("\nCREATE INDEX matter_targets_updated_idx ON matter_targets (updated_at);\n")...)
	})
	r := e.do("POST", "/api/v1/modules/matter-clock/update", changed, e.as("admin")...)
	if r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeMigration || !strings.Contains(r.body, "changed") {
		t.Fatalf("changed migration: %d %s", r.code, r.body)
	}
	dropped := e.build(func(f map[string][]byte) {
		withVersion(f, "1.1.0")
		delete(f, sampleMigration)
		f["migrations/0002_other.sql"] = []byte("CREATE TABLE other_things (id TEXT PRIMARY KEY);")
	})
	if r := e.do("POST", "/api/v1/modules/matter-clock/update", dropped, e.as("admin")...); r.json["code"] != modules.CodeMigration {
		t.Fatalf("dropped migration: %d %s", r.code, r.body)
	}

	failing := e.build(func(f map[string][]byte) {
		withVersion(f, "1.2.0")
		f["migrations/0002_failing.sql"] = []byte("CREATE TABLE half_done (id TEXT PRIMARY KEY);\nCREATE INDEX missing_idx ON no_such_table (id);")
	})
	r = e.do("POST", "/api/v1/modules/matter-clock/update", failing, e.as("admin")...)
	mod, _ := r.json["module"].(map[string]any)
	if r.code != http.StatusAccepted || r.json["outcome"] != "kept_previous" || mod["version"] != "1.0.0" || r.json["previous_version"] != "1.0.0" {
		t.Fatalf("failing migration: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'mod_matter_clock' AND table_name = 'half_done'`) != 0 {
		t.Fatal("the failed migration didn't roll back")
	}
	if e.count(`SELECT COUNT(*) FROM core.schema_migrations WHERE owner = 'mod_matter_clock' AND version = 2`) != 0 {
		t.Fatal("the failed migration was recorded")
	}
	if e.rt.Last("matter-clock").Version != "1.0.0" || e.rt.Last("matter-clock").Secrets["togavsu_api_token"] != running.Secrets["togavsu_api_token"] {
		t.Fatal("the old version stopped running")
	}
	if r := e.do("GET", "/api/v1/modules/matter-clock", nil, e.as("admin")...); r.json["version"] != "1.0.0" || r.json["status"] != modules.StatusInstalled {
		t.Fatalf("after a failed update: %s", r.body)
	}

	good := e.build(func(f map[string][]byte) {
		withVersion(f, "1.3.0")
		f["migrations/0002_target_reviews.sql"] = []byte("ALTER TABLE matter_targets ADD COLUMN reviewed_on TEXT NOT NULL DEFAULT '';")
		f[modules.ManifestFile] = []byte(strings.Replace(string(f[modules.ManifestFile]), "      title: Matter Clock\n      keywords", "      title: Matter Clock monthly targets\n      keywords", 1))
	})
	r = e.do("POST", "/api/v1/modules/matter-clock/update", good, e.as("admin")...)
	mod, _ = r.json["module"].(map[string]any)
	if r.code != http.StatusAccepted || r.json["outcome"] != "updated" || mod["version"] != "1.3.0" {
		t.Fatalf("good update: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = 'mod_matter_clock' AND table_name = 'matter_targets' AND column_name = 'reviewed_on'`) != 1 {
		t.Fatal("the update's migration didn't run")
	}
	tp, err := e.help.Topic(e.ctx, e.staff, "matter-clock.overview")
	if err != nil || tp.Title != "Matter Clock monthly targets" {
		t.Fatalf("guide not refreshed on update: %+v %v", tp, err)
	}
	if e.rt.Last("matter-clock").Version != "1.3.0" {
		t.Fatal("the new version wasn't deployed")
	}

	// New permissions wait for the admin while the old version keeps running.
	wider := e.build(func(f map[string][]byte) {
		withVersion(f, "1.4.0")
		f["migrations/0002_target_reviews.sql"] = []byte("ALTER TABLE matter_targets ADD COLUMN reviewed_on TEXT NOT NULL DEFAULT '';")
		f[modules.ManifestFile] = []byte(strings.Replace(string(f[modules.ManifestFile]), "  - scope: events:read\n", "  - scope: events:read\n  - scope: notes:read\n", 1))
	})
	r = e.do("POST", "/api/v1/modules/matter-clock/update", wider, e.as("admin")...)
	mod, _ = r.json["module"].(map[string]any)
	if r.code != http.StatusAccepted || r.json["outcome"] != "awaiting_approval" || mod["version"] != "1.3.0" {
		t.Fatalf("update with a new permission: %d %s", r.code, r.body)
	}
	if e.rt.Last("matter-clock").Version != "1.3.0" {
		t.Fatal("the old version stopped while the new permission waited")
	}
	r = e.do("POST", "/api/v1/modules/matter-clock/permissions", map[string]any{"decisions": []map[string]string{{"scope": "notes:read", "decision": "decline"}}}, e.as("admin")...)
	if r.code != http.StatusUnprocessableEntity {
		t.Fatalf("declining the new permission: %d %s", r.code, r.body)
	}
	if r := e.do("GET", "/api/v1/modules/matter-clock", nil, e.as("admin")...); r.json["version"] != "1.3.0" || strings.Contains(r.body, "notes:read") {
		t.Fatalf("after declining the update: %s", r.body)
	}
	e.do("POST", "/api/v1/modules/matter-clock/update", wider, e.as("admin")...)
	e.approveAll("matter-clock")
	if r := e.do("GET", "/api/v1/modules/matter-clock", nil, e.as("admin")...); r.json["version"] != "1.4.0" {
		t.Fatalf("approved update: %s", r.body)
	}
	if !strings.Contains(strings.Join(e.tokenScopes(t), ","), "notes:read") {
		t.Fatal("the reissued token lacks the approved scope")
	}
	if strings.Contains(strings.Join(e.tokenScopesOf(t, running.Secrets["togavsu_api_token"]), ","), "time:read") {
		t.Fatal("the old token still works after the update")
	}

	// Disable and enable without a restart.
	if r := e.do("POST", "/api/v1/modules/matter-clock/disable", nil, e.as("admin")...); r.json["enabled"] != false || catalogHas(e, e.staff, "matter-clock.hours") || hasTopic(t, e, "matter-clock") {
		t.Fatalf("disable: %s", r.body)
	}
	if !e.schemaExists("matter-clock") {
		t.Fatal("disable dropped the schema")
	}
	if r := e.do("POST", "/api/v1/modules/matter-clock/enable", nil, e.as("admin")...); r.json["enabled"] != true || !catalogHas(e, e.staff, "matter-clock.hours") || !hasTopic(t, e, "matter-clock") {
		t.Fatalf("enable: %s", r.body)
	}
}

func (e *env) tokenScopes(t *testing.T) []string {
	return e.tokenScopesOf(t, e.rt.Last("matter-clock").Secrets["togavsu_api_token"])
}

func (e *env) tokenScopesOf(t *testing.T, secret string) []string {
	t.Helper()
	r := e.do("GET", "/api/v1/modules/matter-clock/settings", nil, "Authorization", "Bearer "+secret)
	if r.code != http.StatusOK {
		return nil
	}
	c, err := e.auth(mustReq(t, secret))
	if err != nil {
		return nil
	}
	return c.Scopes
}

func mustReq(t *testing.T, secret string) *http.Request {
	t.Helper()
	r, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+secret)
	return r
}
