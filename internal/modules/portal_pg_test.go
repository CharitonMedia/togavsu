package modules_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

func TestPGClientPortalFrames(t *testing.T) {
	e := newEnv(t)
	e.user("Cora Client", "cora@client.example", domain.RoleClient)

	homeTab := "  - id: hours\n    title: Matter Clock\n    placement: home\n    path: /tab\n"
	raw := e.build(func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "tabs:\n", "client_home: home\ntabs:\n")
		replace(f, modules.ManifestFile, homeTab,
			"  - id: home\n    title: Home\n    placement: client\n    path: /client/home\n    params: [matter_id]\n"+
				"  - id: docs\n    title: Documents\n    placement: client\n    path: /client/docs\n"+
				"  - id: pay\n    title: Pay\n    placement: client\n    path: /pay\n    params: [invoice_id, return_to]\n    nav: false\n"+
				homeTab)
	})
	r := e.do("POST", "/api/v1/modules", raw, e.as("admin")...)
	if r.code != http.StatusAccepted {
		t.Fatalf("install: %d %s", r.code, r.body)
	}
	e.approveAll("matter-clock")

	r = e.do("GET", "/portal/matter-clock/home", nil, e.as("client")...)
	want := `sandbox="allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox allow-top-navigation-by-user-activation"`
	if r.code != http.StatusOK || !strings.Contains(r.body, want) || strings.Contains(r.body, "allow-same-origin") {
		t.Fatalf("client frame: %d %s", r.code, r.body)
	}
	if !strings.Contains(r.body, "/m/matter-clock/client/home?context=") {
		t.Fatalf("src: %s", r.body)
	}
	// Declared params pass through; undeclared ones do not (no matter claim needed).
	r = e.do("GET", "/portal/matter-clock/pay?invoice_id=inv-1&secret=x&return_to=/portal", nil, e.as("client")...)
	if r.code != http.StatusOK || strings.Contains(r.body, "secret=") || !strings.Contains(r.body, "invoice_id=") {
		t.Fatalf("params: %d %s", r.code, r.body)
	}
	for _, who := range []string{"staff", "attorney", "admin"} {
		if r := e.do("GET", "/portal/matter-clock/home", nil, e.as(who)...); r.code != http.StatusNotFound {
			t.Fatalf("%s: %d", who, r.code)
		}
	}
	r = e.do("GET", "/api/v1/nav/client-context", nil, e.as("client")...)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"tab_id":"home"`) || strings.Contains(r.body, `"tab_id":"pay"`) {
		t.Fatalf("nav: %d %s", r.code, r.body)
	}
	if r := e.do("GET", "/api/v1/nav/client-context", nil, e.as("staff")...); r.code != http.StatusForbidden {
		t.Fatalf("staff nav: %d", r.code)
	}

	raw2 := e.build(func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "id: matter-clock\n", "id: other-portal\n")
		replace(f, modules.ManifestFile, "tabs:\n", "client_home: home\ntabs:\n")
		replace(f, modules.ManifestFile, homeTab,
			"  - id: home\n    title: Home\n    placement: client\n    path: /home\n"+homeTab)
	})
	if r := e.do("POST", "/api/v1/modules", raw2, e.as("admin")...); r.code != http.StatusConflict {
		t.Fatalf("second home: %d %s", r.code, r.body)
	}
}

func TestPGClientPortalEmptyState(t *testing.T) {
	e := newEnv(t)
	e.user("Cora Client", "cora@client.example", domain.RoleClient)
	r := e.do("GET", "/portal", nil, e.as("client")...)
	if r.code != http.StatusOK || !strings.Contains(r.body, "isn't set up yet") {
		t.Fatalf("empty: %d %s", r.code, r.body)
	}
}
