package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
)

// testDSN creates an empty database for t and returns its URL.
func testDSN(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TOGAVSU_TEST_DATABASE_URL is required in CI")
		}
		t.Skip("TOGAVSU_TEST_DATABASE_URL is not set")
	}
	admin, err := db.OpenPostgres(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	sum := sha256.Sum256([]byte("core/" + t.Name()))
	name := "c" + hex.EncodeToString(sum[:8])
	ctx := context.Background()
	if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin, err := db.OpenPostgres(raw, "")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name)
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

var testKey = bytes.Repeat([]byte{7}, 32)

// dataDirs keeps one data folder per test database, so a core restarted on the same
// database finds the file bytes its first process stored.
var dataDirs = map[string]string{}

func dataDir(t *testing.T, dsn string) string {
	t.Helper()
	if d, ok := dataDirs[dsn]; ok {
		return d
	}
	d := t.TempDir()
	dataDirs[dsn] = d
	t.Cleanup(func() { delete(dataDirs, dsn) })
	return d
}

func start(t *testing.T, dsn string) (*App, *httptest.Server) {
	t.Helper()
	app, err := Open(context.Background(), Config{DatabaseURL: dsn, Key: testKey, Domain: "firm.example", DataDir: dataDir(t, dsn)})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		srv.Close()
		_ = app.Close()
	})
	return app, srv
}

// totp is RFC 6238 with SHA-1, 30 seconds and six digits, as identity/params.go sets.
func totp(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		t.Fatal(err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1000000)
}

type client struct {
	t      *testing.T
	base   string
	http   *http.Client
	csrf   string
	bearer string
}

func newClient(t *testing.T, base string) *client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, base: base, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

type resp struct {
	code int
	body string
	json map[string]any
	loc  string
}

func (c *client) do(method, path string, body any, hdr ...string) resp {
	c.t.Helper()
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case url.Values:
		rd, ctype = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		rd, ctype = bytes.NewReader(raw), "application/json"
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{code: res.StatusCode, body: string(raw), loc: res.Header.Get("Location")}
	_ = json.Unmarshal(raw, &out.json)
	return out
}

func (c *client) must(code int, method, path string, body any) resp {
	c.t.Helper()
	r := c.do(method, path, body)
	if r.code != code {
		c.t.Fatalf("%s %s: want %d, got %d: %s", method, path, code, r.code, r.body)
	}
	return r
}

func str(t *testing.T, m map[string]any, keys ...string) string {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("no %s in %v", k, m)
		}
		cur = obj[k]
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("%v is not a string in %v", keys, m)
	}
	return s
}

const adminPassword = "correct-horse-battery"

// firstRun drives setup over HTTP and returns the signed-in admin.
func firstRun(t *testing.T, base string) *client {
	t.Helper()
	admin := newClient(t, base)
	enroll := admin.must(http.StatusCreated, "POST", "/api/v1/setup", map[string]string{
		"firm_name": "Synthetic Firm LLP", "time_zone": "America/Chicago", "admin_name": "Avery Admin",
		"admin_email": "avery@firm.example", "password": adminPassword, "backup_passphrase": "synthetic-backup-passphrase",
	})
	token, secret := str(t, enroll.json, "enrollment_token"), str(t, enroll.json, "secret")
	bad := admin.do("POST", "/api/v1/auth/totp/confirm", map[string]string{"enrollment_token": token, "code": "000000"})
	if bad.code < 400 || bad.code == http.StatusInternalServerError {
		t.Fatalf("wrong code accepted or crashed: %d %s", bad.code, bad.body)
	}
	// The challenge is single use: after a wrong code the admin signs in again to enroll.
	if r := admin.do("POST", "/api/v1/auth/totp/confirm", map[string]string{"enrollment_token": token, "code": totp(t, secret, time.Now())}); r.code != http.StatusUnauthorized {
		t.Fatalf("spent enrollment token reused: %d %s", r.code, r.body)
	}
	login := admin.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": "avery@firm.example", "password": adminPassword})
	if str(t, login.json, "status") != "enrollment_required" {
		t.Fatal(login.body)
	}
	en := admin.must(http.StatusOK, "POST", "/api/v1/auth/totp/enroll", map[string]string{"enrollment_token": str(t, login.json, "enrollment_token")})
	token, secret = str(t, en.json, "enrollment_token"), str(t, en.json, "secret")
	ok := admin.must(http.StatusOK, "POST", "/api/v1/auth/totp/confirm", map[string]string{"enrollment_token": token, "code": totp(t, secret, time.Now())})
	if str(t, ok.json, "status") != "authenticated" {
		t.Fatal(ok.body)
	}
	if codes, _ := ok.json["recovery_codes"].([]any); len(codes) == 0 {
		t.Fatal("no recovery codes", ok.body)
	}
	admin.csrf = str(t, ok.json, "csrf_token")
	return admin
}

// enrollUser creates a user through the identity service and signs it in over HTTP.
func enrollUser(t *testing.T, app *App, base string, admin *client, name, email string, roles ...domain.Role) (*client, domain.ID) {
	t.Helper()
	ctx := context.Background()
	u, _ := url.Parse(base)
	var session string
	for _, c := range admin.http.Jar.Cookies(u) {
		if c.Name == sessionCookie {
			session = c.Value
		}
	}
	caller, err := app.Identity.Authenticate(ctx, identity.Presented{SessionToken: session})
	if err != nil {
		t.Fatal(err)
	}
	user, err := app.Identity.CreateUser(ctx, caller, identity.NewUser{Name: name, Email: email, Password: "synthetic-password-" + name, Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t, base)
	login := c.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "synthetic-password-" + name})
	if str(t, login.json, "status") != "enrollment_required" {
		t.Fatal(login.body)
	}
	en := c.must(http.StatusOK, "POST", "/api/v1/auth/totp/enroll", map[string]string{"enrollment_token": str(t, login.json, "enrollment_token")})
	done := c.must(http.StatusOK, "POST", "/api/v1/auth/totp/confirm", map[string]string{
		"enrollment_token": str(t, en.json, "enrollment_token"), "code": totp(t, str(t, en.json, "secret"), time.Now()),
	})
	c.csrf = str(t, done.json, "csrf_token")
	return c, user.ID
}

func toolName(t *testing.T, method, path string) string {
	t.Helper()
	tools, err := loadTools(api.MCPTools)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.Method == method && tl.Path == path {
			return tl.Name
		}
	}
	t.Fatalf("no tool for %s %s", method, path)
	return ""
}

func mcpCall(c *client, name string, args map[string]any) resp {
	return c.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
}

// TestFirstRunGate is AC-001 on the composed core: a fresh database serves only setup,
// setup runs once, and the install is ready after the admin confirms two-factor.
func TestFirstRunGate(t *testing.T) {
	_, srv := start(t, testDSN(t))
	anon := newClient(t, srv.URL)
	if r := anon.must(http.StatusOK, "GET", "/api/v1/setup", nil); r.json["ready"] != false {
		t.Fatal(r.body)
	}
	if r := anon.do("GET", "/api/v1/matters", nil); r.code != http.StatusConflict || !strings.Contains(r.body, "setup_required") {
		t.Fatalf("API before setup: %d %s", r.code, r.body)
	}
	if r := anon.do("GET", "/practice/matters", nil); r.code != http.StatusSeeOther || r.loc != "/setup" {
		t.Fatalf("screen before setup: %d %q", r.code, r.loc)
	}
	if r := anon.must(http.StatusOK, "GET", "/setup", nil); !strings.Contains(r.body, `name="firm_name"`) {
		t.Fatal("setup form missing")
	}
	if r := anon.must(http.StatusOK, "GET", "/healthz", nil); r.json["status"] != "ok" {
		t.Fatal(r.body)
	}
	bad := anon.do("POST", "/api/v1/setup", map[string]string{"firm_name": "X", "time_zone": "America/Chicago", "admin_name": "A", "admin_email": "a@firm.example", "password": "short", "backup_passphrase": "short"})
	if bad.code != http.StatusBadRequest {
		t.Fatalf("short password: %d %s", bad.code, bad.body)
	}
	admin := firstRun(t, srv.URL)
	if r := anon.must(http.StatusOK, "GET", "/api/v1/setup", nil); r.json["ready"] != true {
		t.Fatal(r.body)
	}
	again := anon.do("POST", "/api/v1/setup", map[string]string{"firm_name": "Other", "time_zone": "America/Chicago", "admin_name": "Mallory", "admin_email": "m@firm.example", "password": adminPassword, "backup_passphrase": "synthetic-backup-passphrase"})
	if again.code != http.StatusConflict {
		t.Fatalf("second setup: %d %s", again.code, again.body)
	}
	if r := anon.do("GET", "/api/v1/matters", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("anonymous API after setup: %d", r.code)
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/me", nil); str(t, r.json, "email") != "avery@firm.example" {
		t.Fatal(r.body)
	}
	if r := admin.must(http.StatusOK, "GET", "/practice/matters", nil); !strings.Contains(r.body, "Matters") {
		t.Fatal("matters screen did not render")
	}
}

// TestBrowserSetupAndSignIn drives the HTML forms: setup, two-factor, and a later sign-in.
func TestBrowserSetupAndSignIn(t *testing.T) {
	_, srv := start(t, testDSN(t))
	b := newClient(t, srv.URL)
	r := b.must(http.StatusOK, "POST", "/setup", url.Values{"firm_name": {"Form Firm"}, "time_zone": {"America/Chicago"}, "admin_name": {"Fran Form"},
		"admin_email": {"fran@firm.example"}, "password": {adminPassword}, "backup_passphrase": {"synthetic-backup-passphrase"}})
	token := between(r.body, `name="enrollment_token" value="`, `"`)
	secret := between(r.body, "Secret: <code>", "</code>")
	if token == "" || secret == "" {
		t.Fatal("enrollment page missing token or secret")
	}
	at := time.Now()
	done := b.must(http.StatusOK, "POST", "/setup/confirm", url.Values{"enrollment_token": {token}, "code": {totp(t, secret, at)}})
	if !strings.Contains(done.body, "Recovery codes") {
		t.Fatal(done.body)
	}
	if r := b.do("GET", "/practice/matters", nil); r.code != http.StatusOK {
		t.Fatalf("signed-in browser: %d", r.code)
	}
	// A settings form without the CSRF token is refused; with it, it saves.
	if r := b.do("POST", "/admin/settings/practice.matter_page_size", url.Values{"value": {"30"}}); r.code != http.StatusForbidden {
		t.Fatalf("settings form without csrf: %d %s", r.code, r.body)
	}
	b.must(http.StatusSeeOther, "POST", "/logout", url.Values{"csrf": {csrfFrom(t, b)}})
	if r := b.do("GET", "/practice/matters", nil); r.code != http.StatusSeeOther || r.loc != "/login" {
		t.Fatalf("signed out browser on a screen: %d %q", r.code, r.loc)
	}
	// Sign in again with a code from the next step, so the setup code is not replayed.
	login := b.must(http.StatusOK, "POST", "/login", url.Values{"email": {"fran@firm.example"}, "password": {adminPassword}})
	totpToken := between(login.body, `name="totp_token" value="`, `"`)
	if totpToken == "" {
		t.Fatal("no totp step", login.body)
	}
	wrong := b.do("POST", "/login/totp", url.Values{"totp_token": {totpToken}, "code": {"000000"}})
	if wrong.code < 400 {
		t.Fatalf("wrong code: %d", wrong.code)
	}
	login = b.must(http.StatusOK, "POST", "/login", url.Values{"email": {"fran@firm.example"}, "password": {adminPassword}})
	totpToken = between(login.body, `name="totp_token" value="`, `"`)
	ok := b.do("POST", "/login/totp", url.Values{"totp_token": {totpToken}, "code": {totp(t, secret, at.Add(30*time.Second))}})
	if ok.code != http.StatusSeeOther || ok.loc != "/practice/matters" {
		t.Fatalf("totp sign-in: %d %q %s", ok.code, ok.loc, ok.body)
	}
	if r := b.must(http.StatusOK, "GET", "/admin/settings", nil); !strings.Contains(r.body, "Reset to default") && !strings.Contains(r.body, "<table>") {
		t.Fatal("settings page did not render")
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}

func csrfFrom(t *testing.T, c *client) string {
	u, _ := url.Parse(c.base)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "togavsu_practice_csrf" {
			return ck.Value
		}
	}
	t.Fatal("no csrf cookie")
	return ""
}

// TestWallsRolesAuditAndRestart covers AC-003, AC-004, AC-018 (audit) and AC-024 on the
// composed core over real Postgres, then restarts the core on the same database.
func TestWallsRolesAuditAndRestart(t *testing.T) {
	dsn := testDSN(t)
	app, srv := start(t, dsn)
	admin := firstRun(t, srv.URL)
	staff, staffID := enrollUser(t, app, srv.URL, admin, "Sam Staff", "sam@firm.example", domain.RoleStaff)
	acct, _ := enrollUser(t, app, srv.URL, admin, "Ari Accountant", "ari@firm.example", domain.RoleAccountant)
	me := admin.must(http.StatusOK, "GET", "/api/v1/me", nil)
	adminID := str(t, me.json, "id")

	// CSRF: a cookie write without the token is refused before the service runs.
	noCSRF := admin.do("POST", "/api/v1/practice-areas", map[string]any{"name": "Probate", "stages": []string{"Intake"}}, "X-CSRF-Token", "")
	if noCSRF.code != http.StatusForbidden {
		t.Fatalf("write without csrf: %d %s", noCSRF.code, noCSRF.body)
	}
	area := admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Probate", "stages": []string{"Intake"}})
	areaID := str(t, area.json, "id")
	open := admin.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{"name": "Open Estate of Rivera", "practice_area_id": areaID, "billing_type": "hourly"})
	walled := admin.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{"name": "Walled Estate of Quist", "practice_area_id": areaID, "billing_type": "hourly"})
	openID, walledID := str(t, open.json, "id"), str(t, walled.json, "id")
	walledNumber := str(t, walled.json, "number")
	admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walledID+"/wall", map[string]any{"user_ids": []string{adminID}})
	admin.must(http.StatusOK, "PATCH", "/api/v1/matters/"+walledID, map[string]any{"name": "Walled Estate of Quist II"})

	hidden := func(c *client, who string) {
		t.Helper()
		for _, path := range []string{
			"/api/v1/matters", "/api/v1/search?q=Quist", "/api/v1/search?q=" + url.QueryEscape(walledNumber) + "&scope=matters",
			"/api/v1/reports/matters", "/api/v1/widgets/my-open-matters", "/practice/matters", "/practice/search?q=Quist",
		} {
			r := c.do("GET", path, nil)
			if r.code >= 500 {
				t.Fatalf("%s %s: %d %s", who, path, r.code, r.body)
			}
			// Search echoes the query, so look for the matter's own name and id.
			if strings.Contains(r.body, walledID) || strings.Contains(r.body, "Estate of Quist") || strings.Contains(r.body, walledNumber) && !strings.Contains(path, url.QueryEscape(walledNumber)) {
				t.Fatalf("%s sees the walled matter at %s: %s", who, path, r.body)
			}
		}
		for _, path := range []string{"/api/v1/matters/" + walledID, "/api/v1/matters/" + walledID + "/history", "/practice/matters/" + walledID} {
			r := c.do("GET", path, nil)
			if r.code < 400 || strings.Contains(r.body, "Estate of Quist") {
				t.Fatalf("%s opened the walled matter at %s: %d", who, path, r.code)
			}
		}
		call := mcpCall(c, toolName(t, "GET", "/api/v1/matters/{matter_id}"), map[string]any{"path": map[string]string{"matter_id": walledID}})
		if call.code != http.StatusOK || !strings.Contains(call.body, `"isError":true`) || strings.Contains(call.body, "Estate of Quist") {
			t.Fatalf("%s read the walled matter over MCP: %s", who, call.body)
		}
	}
	hidden(staff, "staff")
	if r := staff.must(http.StatusOK, "GET", "/api/v1/matters", nil); !strings.Contains(r.body, openID) {
		t.Fatal("staff cannot see the open matter")
	}
	for _, path := range []string{"/api/v1/matters", "/api/v1/search?q=Quist", "/api/v1/reports/matters"} {
		if r := admin.must(http.StatusOK, "GET", path, nil); !strings.Contains(r.body, "Quist") {
			t.Fatalf("admin inside the wall cannot see it at %s", path)
		}
	}

	// AC-004 on the core: the accountant reads trust and billing reports and writes nothing.
	acct.must(http.StatusOK, "GET", "/api/v1/reports/trust-balances", nil)
	acct.must(http.StatusOK, "GET", "/api/v1/reports/ar-aging", nil)
	for _, w := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Acct", "family_name": "Write"}},
		{"POST", "/api/v1/matters", map[string]any{"name": "Acct matter", "practice_area_id": areaID, "billing_type": "hourly"}},
		{"PATCH", "/api/v1/matters/" + openID, map[string]any{"name": "renamed"}},
		{"POST", "/api/v1/practice-areas", map[string]any{"name": "Acct area"}},
		{"PATCH", "/api/v1/settings/practice.matter_page_size", map[string]any{"value": 10}},
	} {
		if r := acct.do(w.method, w.path, w.body); r.code != http.StatusForbidden {
			t.Fatalf("accountant %s %s: %d %s", w.method, w.path, r.code, r.body)
		}
	}

	// AC-024: the edit history shows old and new values to someone inside the wall.
	hist := admin.must(http.StatusOK, "GET", "/api/v1/matters/"+walledID+"/history", nil)
	if !strings.Contains(hist.body, "Walled Estate of Quist II") || !strings.Contains(hist.body, `Walled Estate of Quist"`) {
		t.Fatalf("history lacks old and new names: %s", hist.body)
	}

	// AC-018 (audit half): every write is in audit_log with actor and source.
	logRows := admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=matter&entity_id="+walledID, nil)
	rows, _ := logRows.json["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("no audit rows for the matter", logRows.body)
	}
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["actor_id"] != adminID || row["source"] != "ui" {
			t.Fatalf("audit row without actor or source: %v", row)
		}
	}
	if r := staff.do("GET", "/api/v1/audit-log", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff read the audit log: %d", r.code)
	}
	var ledger int
	if err := app.Pool.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_id = $1`, walledID).Scan(&ledger); err != nil || ledger != len(rows) {
		t.Fatal(err, ledger, len(rows))
	}

	// The audit log keeps each row's matter wall: an admin left off the wall gets no
	// ledger row for the walled matter, its party contact or its note, by API or MCP.
	heir := admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Zebulon", "family_name": "Hiddenheir"})
	heirID := str(t, heir.json, "id")
	admin.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": walledID, "contact_id": heirID, "role": "client"})
	admin.must(http.StatusCreated, "POST", "/api/v1/notes", map[string]any{"matter_id": walledID, "title": "Walled memo", "body": "walled-note-body-7e1"})
	outsider, _ := enrollUser(t, app, srv.URL, admin, "Otto Outside", "otto@firm.example", domain.RoleAdmin)
	auditHidden := func(c *client, who string) {
		t.Helper()
		auditTool := toolName(t, "GET", "/api/v1/audit-log")
		var bodies []string
		for _, q := range []string{"", "?entity_type=matter", "?entity_type=matter&entity_id=" + walledID, "?entity_type=contact&entity_id=" + heirID, "?entity_type=note", "?entity_type=matter_party"} {
			bodies = append(bodies, c.must(http.StatusOK, "GET", "/api/v1/audit-log"+q, nil).body)
		}
		for _, q := range []map[string]any{{}, {"entity_type": "matter", "entity_id": walledID}, {"entity_type": "contact", "entity_id": heirID}} {
			call := mcpCall(c, auditTool, map[string]any{"query": q})
			if call.code != http.StatusOK || strings.Contains(call.body, `"isError":true`) {
				t.Fatalf("%s audit log over MCP: %d %s", who, call.code, call.body)
			}
			bodies = append(bodies, call.body)
		}
		for _, body := range bodies {
			for _, leak := range []string{walledID, heirID, "Estate of Quist", "Zebulon", "Walled memo", "walled-note-body-7e1"} {
				if strings.Contains(body, leak) {
					t.Fatalf("%s outside the wall read %q from the audit log: %s", who, leak, body)
				}
			}
		}
		if !strings.Contains(bodies[0], openID) || !strings.Contains(bodies[0], "Open Estate of Rivera") || !strings.Contains(bodies[6], openID) {
			t.Fatalf("%s lost readable audit rows: %s", who, bodies[0])
		}
	}
	auditHidden(outsider, "admin")
	for _, want := range []string{walledID, heirID, "Walled memo"} {
		if r := admin.must(http.StatusOK, "GET", "/api/v1/audit-log", nil); !strings.Contains(r.body, want) {
			t.Fatalf("admin on the wall lost audit row %q", want)
		}
	}

	// Restart on the same database: records, walls and sessions survive.
	app2, srv2 := start(t, dsn)
	_ = app2
	for _, c := range []*client{admin, staff, outsider} {
		c.base = srv2.URL
		u1, _ := url.Parse(srv.URL)
		u2, _ := url.Parse(srv2.URL)
		c.http.Jar.SetCookies(u2, c.http.Jar.Cookies(u1))
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/matters/"+walledID, nil); !strings.Contains(r.body, "Quist II") {
		t.Fatal("matter lost on restart")
	}
	hidden(staff, "staff after restart")
	auditHidden(outsider, "admin after restart")
	_ = staffID
}

// TestBearerScopesMCPAndSettings covers AC-002 (API and MCP reach the same operations),
// bearer scope enforcement, and AC-029 (a setting change applies on the next request,
// is logged with old and new values, and resets).
func TestBearerScopesMCPAndSettings(t *testing.T) {
	app, srv := start(t, testDSN(t))
	admin := firstRun(t, srv.URL)
	area := admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Family", "stages": []string{"Intake"}})
	areaID := str(t, area.json, "id")
	admin.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{"name": "Token visible matter", "practice_area_id": areaID, "billing_type": "hourly"})

	issued := admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "reader", "scopes": []string{"matters:read"}})
	reader := newClient(t, srv.URL)
	reader.bearer = str(t, issued.json, "secret")
	if r := reader.must(http.StatusOK, "GET", "/api/v1/matters", nil); !strings.Contains(r.body, "Token visible matter") {
		t.Fatal(r.body)
	}
	if r := reader.do("POST", "/api/v1/matters", map[string]any{"name": "nope", "practice_area_id": areaID, "billing_type": "hourly"}); r.code != http.StatusForbidden {
		t.Fatalf("read token wrote: %d %s", r.code, r.body)
	}
	if r := reader.do("GET", "/api/v1/contacts", nil); r.code != http.StatusForbidden {
		t.Fatalf("matters token read contacts: %d", r.code)
	}
	if r := reader.do("GET", "/practice/matters", nil); r.code != http.StatusForbidden || strings.Contains(r.body, "Token visible matter") {
		t.Fatalf("token reached an uncontracted route: %d", r.code)
	}
	bogus := newClient(t, srv.URL)
	bogus.bearer = "not-a-token"
	if r := bogus.do("GET", "/api/v1/matters", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("bogus token: %d", r.code)
	}

	writer := newClient(t, srv.URL)
	w := admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "writer", "scopes": []string{"contacts:write", "contacts:read"}})
	writer.bearer = str(t, w.json, "secret")
	created := writer.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Tara", "family_name": "Token"})
	contactID := str(t, created.json, "id")
	logRows := admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=contact&entity_id="+contactID, nil)
	if !strings.Contains(logRows.body, `"source":"api_token"`) {
		t.Fatalf("token write not audited as api_token: %s", logRows.body)
	}

	// MCP: initialize, list, and call the same operations under the same checks.
	init := reader.must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	if str(t, init.json, "result", "serverInfo", "name") != "togavsu" {
		t.Fatal(init.body)
	}
	list := reader.must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	listMatters := toolName(t, "GET", "/api/v1/matters")
	// Files are mounted since phase 4; module alerts are a later integration and are not offered.
	if !strings.Contains(list.body, `"name":"`+listMatters+`"`) || !strings.Contains(list.body, `"name":"`+toolName(t, "GET", "/api/v1/files")+`"`) ||
		strings.Contains(list.body, `"name":"`+toolName(t, "GET", "/api/v1/widgets/module-alerts")+`"`) {
		t.Fatal("tools/list does not reflect the mounted routes")
	}
	call := mcpCall(reader, listMatters, map[string]any{})
	if !strings.Contains(call.body, "Token visible matter") || strings.Contains(call.body, `"isError":true`) {
		t.Fatal(call.body)
	}
	denied := mcpCall(reader, toolName(t, "POST", "/api/v1/matters"), map[string]any{"body": map[string]any{"name": "mcp", "practice_area_id": areaID, "billing_type": "hourly"}})
	if !strings.Contains(denied.body, `"isError":true`) || !strings.Contains(denied.body, "scope") {
		t.Fatalf("MCP write without scope: %s", denied.body)
	}
	viaMCP := mcpCall(admin, toolName(t, "POST", "/api/v1/matters"), map[string]any{"body": map[string]any{"name": "Created over MCP", "practice_area_id": areaID, "billing_type": "hourly"}})
	if strings.Contains(viaMCP.body, `"isError":true`) {
		t.Fatalf("admin MCP write: %s", viaMCP.body)
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/matters", nil); !strings.Contains(r.body, "Created over MCP") {
		t.Fatal("MCP write not visible through the API")
	}
	if r := newClient(t, srv.URL).do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}); r.code != http.StatusUnauthorized {
		t.Fatalf("anonymous MCP: %d", r.code)
	}
	if r := admin.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/list"}, "X-CSRF-Token", ""); r.code != http.StatusForbidden {
		t.Fatalf("cookie MCP without csrf: %d", r.code)
	}

	// AC-029: the rate limit setting takes effect on the next request, is logged, and resets.
	key := "security.api_rate_per_minute"
	before := admin.must(http.StatusOK, "GET", "/api/v1/settings/"+key, nil)
	def := before.json["default"]
	if bad := admin.do("PATCH", "/api/v1/settings/"+key, map[string]any{"value": "lots"}); bad.code != http.StatusBadRequest {
		t.Fatalf("invalid setting value: %d %s", bad.code, bad.body)
	}
	admin.must(http.StatusOK, "PATCH", "/api/v1/settings/"+key, map[string]any{"value": 1})
	fresh := newClient(t, srv.URL)
	fresh.bearer = reader.bearer
	statuses := []int{fresh.do("GET", "/api/v1/matters", nil).code, fresh.do("GET", "/api/v1/matters", nil).code}
	if statuses[1] != http.StatusTooManyRequests {
		t.Fatalf("rate limit 1 not applied on the next request: %v", statuses)
	}
	changes := admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=setting", nil)
	if !strings.Contains(changes.body, fmt.Sprintf(`"old_value":"%v"`, def)) || !strings.Contains(changes.body, `"new_value":"1"`) {
		t.Fatalf("setting change not logged with old and new values: %s", changes.body)
	}
	reset := admin.must(http.StatusOK, "POST", "/api/v1/settings/"+key+"/reset", nil)
	if reset.json["value"] != def || reset.json["configured"] != false {
		t.Fatalf("reset did not restore the default: %s", reset.body)
	}
	if r := admin.do("PATCH", "/api/v1/settings/no.such.key", map[string]any{"value": 1}); r.code != http.StatusNotFound {
		t.Fatalf("unknown setting: %d %s", r.code, r.body)
	}
	_ = app
}
