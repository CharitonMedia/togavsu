package core

import (
	"net/http"
	"strings"
	"testing"
)

func TestLoginPasswordOptionalPage(t *testing.T) {
	_, srv := start(t, testDSN(t))
	admin := firstRun(t, srv.URL)

	off := newClient(t, srv.URL).must(http.StatusOK, "GET", "/login", nil)
	if !strings.Contains(off.body, `<label>Password<br><input name="password" type="password" required autocomplete="current-password">`) {
		t.Fatalf("setting off must keep required password: %s", off.body)
	}
	if strings.Contains(off.body, "Password (optional on this install)") {
		t.Fatal("optional label must not appear while the setting is off")
	}

	admin.must(http.StatusOK, "PATCH", "/api/v1/settings/security.password_optional", map[string]any{"value": true})

	on := newClient(t, srv.URL).must(http.StatusOK, "GET", "/login", nil)
	if !strings.Contains(on.body, "Password (optional on this install)") {
		t.Fatalf("setting on must use the optional label: %s", on.body)
	}
	if strings.Contains(on.body, `name="password" type="password" required`) {
		t.Fatalf("setting on must drop required on the password box: %s", on.body)
	}
}
