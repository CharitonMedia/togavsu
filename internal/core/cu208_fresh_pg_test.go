package core

import (
	"net/http"
	"strings"
	"testing"
)

// TestCU208FreshInstallNoModules: a fresh install reaches first-run setup with no module
// installed (CU2-08 / CORE-CHANGES-2 §4.4 half). The update-load half is scripts/ci/update-load.sh.
func TestCU208FreshInstallNoModules(t *testing.T) {
	app, srv := start(t, testDSN(t))
	anon := newClient(t, srv.URL)
	if r := anon.must(http.StatusOK, "GET", "/api/v1/setup", nil); r.json["ready"] != false {
		t.Fatalf("fresh install must wait for setup: %s", r.body)
	}
	if r := anon.do("GET", "/setup", nil); r.code != http.StatusOK ||
		!strings.Contains(r.body, `name="firm_name"`) || !strings.Contains(r.body, `name="admin_email"`) {
		t.Fatalf("setup page: %d", r.code)
	}
	admin := firstRun(t, srv.URL)
	mods := admin.must(http.StatusOK, "GET", "/api/v1/modules", nil)
	data, _ := mods.json["data"].([]any)
	if len(data) != 0 {
		t.Fatalf("fresh install has modules: %s", mods.body)
	}
	var n int
	if err := app.Pool.QueryRow(`SELECT COUNT(*) FROM core.modules`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("core.modules rows: %d", n)
	}
}
