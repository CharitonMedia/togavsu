package modules_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/forms"
)

func (e *env) secretToken(t *testing.T, module string) string {
	t.Helper()
	issued, err := e.app.Identity.IssueToken(e.ctx, identity.Caller{Principal: e.admin}, identity.Issue{
		Name: module + "-secrets", ModuleID: module,
		Scopes: []string{"module_secrets:write"}, Approved: []string{"module_secrets:write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued.Secret
}

func secretPath(module, name string) string {
	return "/api/v1/modules/" + url.PathEscape(module) + "/secrets/" + url.PathEscape(name)
}

func TestPGModuleSecrets(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	tok := e.secretToken(t, "matter-clock")
	auth := []string{"Authorization", "Bearer " + tok}
	name := "qbo/access_token"
	path := secretPath("matter-clock", name)

	if r := e.do("PUT", path, map[string]any{"value": "synthetic-refresh"}, auth...); r.code != http.StatusNoContent {
		t.Fatalf("put: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.module_secrets WHERE module_id = 'matter-clock' AND name = $1 AND value LIKE 'v1:%'`, name) != 1 {
		t.Fatal("secret not sealed at rest")
	}
	r := e.do("GET", path, nil, auth...)
	if r.code != http.StatusOK || r.json["name"] != name || r.json["value"] != "synthetic-refresh" {
		t.Fatalf("get: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module_secret' AND field = 'name' AND new_value = $1`, name) != 1 {
		t.Fatal("put audit must record the name only")
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity = 'module_secret' AND (old_value LIKE '%synthetic%' OR new_value LIKE '%synthetic%')`) != 0 {
		t.Fatal("audit leaked a secret value")
	}

	ctx := e.contextFor("staff", "matter-clock", e.open.ID)
	if r := e.do("GET", path, nil, append(auth, modules.ContextHeader, ctx)...); r.code != http.StatusOK || r.json["value"] != "synthetic-refresh" {
		t.Fatalf("context ignored: %d %s", r.code, r.body)
	}

	other := e.secretToken(t, "other-module")
	if r := e.do("GET", path, nil, "Authorization", "Bearer "+other); r.code != http.StatusForbidden {
		t.Fatalf("wrong module: %d %s", r.code, r.body)
	}

	if r := e.do("GET", secretPath("matter-clock", "missing"), nil, auth...); r.code != http.StatusNotFound || r.json["code"] != "not_found" {
		t.Fatalf("missing: %d %s", r.code, r.body)
	}

	if r := e.do("PUT", secretPath("matter-clock", "BadName"), map[string]any{"value": "x"}, auth...); r.code != http.StatusBadRequest {
		t.Fatalf("bad name: %d %s", r.code, r.body)
	}
	big := strings.Repeat("a", 16<<10+1)
	if r := e.do("PUT", path, map[string]any{"value": big}, auth...); r.code != http.StatusBadRequest {
		t.Fatalf("oversize: %d %s", r.code, r.body)
	}

	if r := e.do("PUT", path, map[string]any{"value": "nope"}, e.as("admin")...); r.code != http.StatusForbidden {
		t.Fatalf("admin session: %d %s", r.code, r.body)
	}

	if r := e.do("DELETE", path, nil, auth...); r.code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.code, r.body)
	}
	if r := e.do("GET", path, nil, auth...); r.code != http.StatusNotFound {
		t.Fatalf("after delete: %d %s", r.code, r.body)
	}
}

func TestPGModuleSecretsCreateIfAbsent(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	tok := e.secretToken(t, "matter-clock")
	auth := []string{"Authorization", "Bearer " + tok}
	path := secretPath("matter-clock", "forms/ssn")

	if r := e.do("PUT", path, map[string]any{"value": "key-a"}, append(auth, "If-None-Match", "*")...); r.code != http.StatusNoContent {
		t.Fatalf("create-if-absent first: %d %s", r.code, r.body)
	}
	if r := e.do("PUT", path, map[string]any{"value": "key-b"}, append(auth, "If-None-Match", "*")...); r.code != http.StatusConflict || r.json["code"] != "conflict" {
		t.Fatalf("create-if-absent second: %d %s", r.code, r.body)
	}
	r := e.do("GET", path, nil, auth...)
	if r.code != http.StatusOK || r.json["value"] != "key-a" {
		t.Fatalf("winner key kept: %d %s", r.code, r.body)
	}
	// Plain PUT still overwrites.
	if r := e.do("PUT", path, map[string]any{"value": "key-c"}, auth...); r.code != http.StatusNoContent {
		t.Fatalf("overwrite put: %d %s", r.code, r.body)
	}
	if r := e.do("GET", path, nil, auth...); r.code != http.StatusOK || r.json["value"] != "key-c" {
		t.Fatalf("overwrite: %d %s", r.code, r.body)
	}
}

// TestPGModuleSecretsHTTPStoreEnsureKey exercises forms.HTTPSecretStore against the
// real putModuleSecret If-None-Match path so concurrent EnsureKey cannot lose seals.
func TestPGModuleSecretsHTTPStoreEnsureKey(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	tok := e.secretToken(t, "matter-clock")
	store := &forms.HTTPSecretStore{
		BaseURL: strings.TrimRight(e.srv.URL, "/") + "/api/v1/modules/matter-clock/secrets",
		Token:   tok,
	}
	ctx := context.Background()
	const n = 8
	var wg sync.WaitGroup
	sealed := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			s, err := forms.EncryptMasked(ctx, store, "forms/race", "plain-"+strings.Repeat("y", i+1))
			if err != nil {
				t.Errorf("EncryptMasked %d: %v", i, err)
				return
			}
			sealed[i] = s
		}()
	}
	wg.Wait()
	for i, s := range sealed {
		if s == "" {
			t.Fatalf("missing seal %d", i)
		}
		want := "plain-" + strings.Repeat("y", i+1)
		plain, err := forms.DecryptMasked(ctx, store, "forms/race", s)
		if err != nil || plain != want {
			t.Fatalf("decrypt %d: plain=%q err=%v", i, plain, err)
		}
	}
}

func TestPGModuleSecretsUninstall(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	tok := e.secretToken(t, "matter-clock")
	auth := []string{"Authorization", "Bearer " + tok}
	path := secretPath("matter-clock", "keep-me")
	if r := e.do("PUT", path, map[string]any{"value": "held"}, auth...); r.code != http.StatusNoContent {
		t.Fatalf("put: %d %s", r.code, r.body)
	}

	if r := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "keep", "confirmation": "matter-clock"}, e.as("admin")...); r.code != http.StatusOK {
		t.Fatalf("uninstall keep: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.module_secrets WHERE module_id = 'matter-clock' AND name = 'keep-me'`) != 1 {
		t.Fatal("keep-data dropped secrets")
	}

	e.installSample()
	tok = e.secretToken(t, "matter-clock")
	auth = []string{"Authorization", "Bearer " + tok}
	if r := e.do("GET", path, nil, auth...); r.code != http.StatusOK || r.json["value"] != "held" {
		t.Fatalf("reinstall pick-up: %d %s", r.code, r.body)
	}
	if r := e.do("PUT", secretPath("matter-clock", "drop-me"), map[string]any{"value": "gone"}, auth...); r.code != http.StatusNoContent {
		t.Fatalf("put drop-me: %d %s", r.code, r.body)
	}
	if r := e.do("POST", "/api/v1/modules/matter-clock/uninstall", map[string]any{"data": "delete", "confirmation": "matter-clock"}, e.as("admin")...); r.code != http.StatusOK {
		t.Fatalf("uninstall delete: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.module_secrets WHERE module_id = 'matter-clock'`) != 0 {
		t.Fatal("delete-data left secrets")
	}
}
