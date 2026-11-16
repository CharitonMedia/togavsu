package modules_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

func installDeviceModule(t *testing.T, e *env) {
	t.Helper()
	r := e.do("POST", "/api/v1/modules", e.build(func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "tabs:\n", "device_connect: /outlook-addin/connected\ntabs:\n")
	}), e.as("admin")...)
	if r.code != http.StatusAccepted || r.json["status"] != modules.StatusAwaiting {
		t.Fatalf("install: %d %s", r.code, r.body)
	}
	e.approveAll("matter-clock")
}

func deviceModuleToken(t *testing.T, e *env) string {
	t.Helper()
	issued, err := e.app.Identity.IssueToken(e.ctx, identity.Caller{Principal: e.admin}, identity.Issue{
		Name: "matter-clock-device", ModuleID: "matter-clock",
		Scopes: []string{"modules:read", "matters:read"}, Approved: []string{"modules:read", "matters:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued.Secret
}

func TestPGModuleDevices(t *testing.T) {
	e := newEnv(t)
	installDeviceModule(t, e)
	modTok := deviceModuleToken(t, e)
	modAuth := []string{"Authorization", "Bearer " + modTok}

	if r := e.do("POST", "/api/v1/me/module-devices", map[string]any{"module_id": "matter-clock"}, modAuth...); r.code != http.StatusForbidden {
		t.Fatalf("bearer create: %d %s", r.code, r.body)
	}
	created := e.do("POST", "/api/v1/me/module-devices", map[string]any{"module_id": "matter-clock", "name": "Outlook"}, e.as("staff")...)
	if created.code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.code, created.body)
	}
	secret, _ := created.json["secret"].(string)
	deviceID, _ := created.json["id"].(string)
	if secret == "" || deviceID == "" {
		t.Fatalf("create missing secret/id: %s", created.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.events WHERE action = 'module_device.created' AND record_id = $1`, deviceID) != 1 {
		t.Fatal("missing module_device.created")
	}
	if e.count(`SELECT COUNT(*) FROM core.module_devices WHERE id = $1 AND secret_hash <> '' AND secret_hash <> $2`, deviceID, secret) != 1 {
		t.Fatal("secret stored in clear")
	}

	list := e.do("GET", "/api/v1/me/module-devices", nil, e.as("staff")...)
	if list.code != http.StatusOK {
		t.Fatalf("list: %d %s", list.code, list.body)
	}
	data, _ := list.json["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("list want 1 got %d: %s", len(data), list.body)
	}
	row, _ := data[0].(map[string]any)
	if _, ok := row["secret"]; ok {
		t.Fatal("list leaked secret")
	}

	ex := e.do("POST", "/api/v1/modules/matter-clock/device-context-tokens", map[string]any{"secret": secret}, modAuth...)
	if ex.code != http.StatusCreated {
		t.Fatalf("exchange: %d %s", ex.code, ex.body)
	}
	tok, _ := ex.json["token"].(string)
	if tok == "" {
		t.Fatalf("exchange token: %s", ex.body)
	}
	expRaw, _ := ex.json["expires_at"].(string)
	exp, err := time.Parse(time.RFC3339Nano, expRaw)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(exp); d < 8*time.Minute || d > 12*time.Minute {
		t.Fatalf("exchange ttl: %v", d)
	}
	claims, err := e.svc.VerifyContext(e.ctx, tok)
	if err != nil || claims.UserID != e.users["staff"].UserID || claims.Module != "matter-clock" {
		t.Fatalf("verify exchanged: %v %+v", err, claims)
	}

	if r := e.do("POST", "/api/v1/modules/matter-clock/device-context-tokens",
		map[string]any{"secret": secret, "matter_id": string(e.walled.ID)}, modAuth...); r.code != http.StatusNotFound {
		t.Fatalf("walled matter: %d %s", r.code, r.body)
	}
	if r := e.do("POST", "/api/v1/modules/matter-clock/device-context-tokens",
		map[string]any{"secret": secret, "matter_id": string(e.open.ID)}, modAuth...); r.code != http.StatusCreated {
		t.Fatalf("open matter: %d %s", r.code, r.body)
	}

	ctxHdr := []string{"Authorization", "Bearer " + modTok, modules.ContextHeader, e.contextFor("staff", "matter-clock", e.open.ID)}
	if r := e.do("POST", "/api/v1/modules/matter-clock/device-context-tokens", map[string]any{"secret": secret}, ctxHdr...); r.code != http.StatusCreated {
		t.Fatalf("context ignored: %d %s", r.code, r.body)
	}

	page := e.do("GET", "/modules/matter-clock/device-connect", nil, e.as("staff")...)
	if page.code != http.StatusOK || !strings.Contains(page.body, "Connect") {
		t.Fatalf("device-connect page: %d %s", page.code, page.body)
	}
	e.user("Cora Client", "cora-device@client.example", domain.RoleClient)
	if r := e.do("GET", "/modules/matter-clock/device-connect", nil, e.as("client")...); r.code != http.StatusForbidden {
		t.Fatalf("client page: %d %s", r.code, r.body)
	}

	req, err := http.NewRequest("POST", e.srv.URL+"/modules/matter-clock/device-connect", strings.NewReader("csrf=test"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Test-User", "staff")
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusFound {
		t.Fatalf("device-connect post: %d", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, "/m/matter-clock/outlook-addin/connected#grant=") {
		t.Fatalf("redirect: %s", loc)
	}

	if r := e.do("DELETE", "/api/v1/me/module-devices/"+deviceID, nil, e.as("staff")...); r.code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.events WHERE action = 'module_device.revoked' AND record_id = $1`, deviceID) != 1 {
		t.Fatal("missing module_device.revoked")
	}
	if r := e.do("POST", "/api/v1/modules/matter-clock/device-context-tokens", map[string]any{"secret": secret}, modAuth...); r.code != http.StatusUnauthorized {
		t.Fatalf("revoked exchange: %d %s", r.code, r.body)
	}
}

func TestPGModuleDevicesLapse(t *testing.T) {
	e := newEnv(t)
	installDeviceModule(t, e)
	modTok := deviceModuleToken(t, e)
	modAuth := []string{"Authorization", "Bearer " + modTok}

	created := e.do("POST", "/api/v1/me/module-devices", map[string]any{"module_id": "matter-clock"}, e.as("staff")...)
	if created.code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.code, created.body)
	}
	secret := created.json["secret"].(string)
	deviceID := created.json["id"].(string)

	old := time.Now().UTC().Add(-91 * 24 * time.Hour).UnixMicro()
	if _, err := e.app.Pool.ExecContext(e.ctx, `UPDATE core.module_devices SET last_used_at = $1 WHERE id = $2`, old, deviceID); err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/v1/modules/matter-clock/device-context-tokens", map[string]any{"secret": secret}, modAuth...); r.code != http.StatusUnauthorized {
		t.Fatalf("90-day lapse: %d %s", r.code, r.body)
	}
	list := e.do("GET", "/api/v1/me/module-devices", nil, e.as("staff")...)
	data, _ := list.json["data"].([]any)
	if len(data) != 0 {
		t.Fatalf("lapsed still listed: %v", data)
	}

	fresh := e.do("POST", "/api/v1/me/module-devices", map[string]any{"module_id": "matter-clock"}, e.as("staff")...)
	if fresh.code != http.StatusCreated {
		t.Fatalf("fresh create: %d %s", fresh.code, fresh.body)
	}
	freshSecret := fresh.json["secret"].(string)
	freshID := fresh.json["id"].(string)
	staffID := e.users["staff"].UserID
	if err := e.svc.RevokeDevicesForUser(e.ctx, e.admin, staffID); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT COUNT(*) FROM core.module_devices WHERE id = $1 AND revoked_at IS NOT NULL`, freshID) != 1 {
		t.Fatal("deactivate revoke did not mark grant")
	}
	if r := e.do("POST", "/api/v1/modules/matter-clock/device-context-tokens", map[string]any{"secret": freshSecret}, modAuth...); r.code != http.StatusUnauthorized {
		t.Fatalf("revoked-on-deactivate exchange: %d %s", r.code, r.body)
	}

	alive := e.do("POST", "/api/v1/me/module-devices", map[string]any{"module_id": "matter-clock"}, e.as("attorney")...)
	if alive.code != http.StatusCreated {
		t.Fatalf("attorney create: %d %s", alive.code, alive.body)
	}
	aliveSecret := alive.json["secret"].(string)
	if _, err := e.app.Pool.ExecContext(e.ctx, `UPDATE core.users SET active = 0 WHERE id = $1`, string(e.attorney.UserID)); err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/v1/modules/matter-clock/device-context-tokens", map[string]any{"secret": aliveSecret}, modAuth...); r.code != http.StatusUnauthorized {
		t.Fatalf("inactive user exchange: %d %s", r.code, r.body)
	}
}
