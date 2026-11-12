package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestReleaseModuleUpdatesViewsAndRollback carries three module cases from the
// internal/modules suite onto the running core: the module's guide refreshes on update
// with no restart (AC-033); an update whose migration fails at run time rolls back and
// the old version keeps running (AC-037); and a second module reads Matter Clock's
// published view only after the admin approves that scope, inside the viewer's walls
// (AC-036).
func TestReleaseModuleUpdatesViewsAndRollback(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	open, _ := x.matter("Estate of Halvard", "", nil)
	walled, _ := x.matter("Estate of Wendeline", "", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	x.installSample()
	dep := x.rt.last("matter-clock")
	mdb := x.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	for id, v := range map[string]int{open: 30, walled: 40} {
		if err := matterclock.SetTarget(ctx, mdb, id, v, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	version := func(module string) string {
		v, err := x.app.Modules.Get(ctx, x.adminP(), module)
		if err != nil {
			t.Fatal(err)
		}
		return v.Version
	}
	bump := func(f map[string][]byte, to string) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte(`version: "1.0.0"`), []byte(`version: "`+to+`"`), 1)
	}

	// AC-033: an update replaces the module's guide pages with the new version's.
	if r := x.staff.must(http.StatusOK, "GET", "/help?q=quasar", nil); strings.Contains(r.body, "Quasar refresh") {
		t.Fatal("the new guide text is there before the update")
	}
	refreshed := x.build(func(f map[string][]byte) {
		bump(f, "1.1.0")
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("keywords: [hours, target, clock, monthly]"), []byte("keywords: [hours, target, clock, monthly, quasar]"), 1)
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("Matter Clock shows the hours worked on each matter this month."), []byte("Quasar refresh: Matter Clock shows the hours worked on each matter this month."), 1)
	})
	if _, problem := x.tabForm(x.admin, "update", url.Values{"module_id": {"matter-clock"}}, refreshed); problem != "" {
		t.Fatalf("update to 1.1.0: %s", problem)
	}
	if v := version("matter-clock"); v != "1.1.0" {
		t.Fatalf("version after update: %s", v)
	}
	if r := x.staff.must(http.StatusOK, "GET", "/help?q=quasar", nil); !strings.Contains(r.body, "Matter Clock") {
		t.Fatalf("help search does not find the updated guide: %.400s", r.body)
	}

	// AC-037: a migration that fails when it runs rolls back whole, and 1.1.0 keeps running.
	broken := x.build(func(f map[string][]byte) {
		bump(f, "1.2.0")
		f["migrations/0002_broken.sql"] = []byte("CREATE TABLE release_broken (id TEXT PRIMARY KEY);\nCREATE TABLE release_broken (id TEXT PRIMARY KEY);\n")
	})
	notice, problem := x.tabForm(x.admin, "update", url.Values{"module_id": {"matter-clock"}}, broken)
	v, err := x.app.Modules.Get(ctx, x.adminP(), "matter-clock")
	if err != nil || v.Version != "1.1.0" || !v.Enabled || !strings.Contains(v.LastError, "rolled back") ||
		!strings.Contains(notice+problem, "kept previous") {
		t.Fatalf("after the failed migration: notice %q, error %q, module %+v %v", notice, problem, v, err)
	}
	var n int
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'mod_matter_clock' AND table_name = 'release_broken'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the failed migration left its table: %d %v", n, err)
	}
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT count(*) FROM core.schema_migrations WHERE owner = 'matter-clock' AND version = 2`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the failed migration is recorded as run: %d %v", n, err)
	}
	if r := x.staff.must(http.StatusOK, "GET", "/help?q=quasar", nil); !strings.Contains(r.body, "Matter Clock") {
		t.Fatal("the running version's guide went away after the failed update")
	}

	// AC-036: a second module reads the published view only after the admin approves it.
	reader := func(version string) []byte {
		return x.build(func(f map[string][]byte) {
			for k := range f {
				if strings.HasPrefix(k, "migrations/") {
					delete(f, k)
				}
			}
			f[modules.ManifestFile] = []byte(`id: target-reader
name: Target Reader
version: "` + version + `"
author: Synthetic author
container:
  image: ghcr.io/example/target-reader
  digest: sha256:1111111111111111111111111111111111111111111111111111111111111111
  port: 8080
permissions:
  - scope: matters:read
  - scope: views:matter-clock.monthly_targets:read
    required: false
`)
		})
	}
	x.rt.mu.Lock()
	x.rt.handlers["target-reader"] = http.NotFoundHandler()
	x.rt.mu.Unlock()
	if _, problem := x.tabForm(x.admin, "install", nil, reader("1.0.0")); problem != "" {
		t.Fatalf("install target-reader: %s", problem)
	}
	x.admin.must(http.StatusOK, "POST", "/api/v1/modules/target-reader/permissions", map[string]any{"decisions": []map[string]string{
		{"scope": "matters:read", "decision": "approve"}, {"scope": "views:matter-clock.monthly_targets:read", "decision": "decline"}}})
	path := "/api/v1/modules/matter-clock/views/monthly_targets?record_id=" + open + "&record_id=" + walled
	asReader := func(viewer *client) resp {
		bot := newClient(t, x.srv.URL)
		bot.bearer = x.rt.last("target-reader").Secrets["togavsu_api_token"]
		tok := str(t, viewer.must(http.StatusCreated, "POST", "/api/v1/modules/target-reader/context-tokens", map[string]any{}).json, "token")
		return bot.do("GET", path, nil, modules.ContextHeader, tok)
	}
	if r := asReader(x.staff); r.code != http.StatusForbidden {
		t.Fatalf("view read before approval: %d %s", r.code, r.body)
	}
	if _, problem := x.tabForm(x.admin, "update", url.Values{"module_id": {"target-reader"}}, reader("1.0.1")); problem != "" && !strings.Contains(strings.ToLower(problem), "approv") {
		t.Fatalf("update asking for the view: %s", problem)
	}
	x.approveOnTab("target-reader")
	r := asReader(x.staff)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"monthly_target_tenths":30`) || strings.Contains(r.body, `"monthly_target_tenths":40`) {
		t.Fatalf("staff outside the wall reading the view after approval: %d %s", r.code, r.body)
	}
	if r := asReader(x.atty); !strings.Contains(r.body, `"monthly_target_tenths":40`) {
		t.Fatalf("attorney inside the wall reading the view: %s", r.body)
	}
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.last("target-reader").Secrets["togavsu_api_token"]
	if r := bot.do("GET", path, nil); r.code != http.StatusOK || strings.Contains(r.body, "monthly_target_tenths") {
		t.Fatalf("a viewerless view read returned rows: %d %s", r.code, r.body)
	}
}
