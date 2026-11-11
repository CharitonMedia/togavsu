package compose

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

	"github.com/CharitonMedia/togavsu/internal/core"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// These tests need TOGAVSU_TEST_DATABASE_URL (a superuser on a disposable Postgres 17).
// Each makes its own database owned by a fresh non-superuser role, as Compose does, opens
// the real core on it, mounts the ui, and drives it over HTTP. Synthetic data only.

func appRoleDSN(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TOGAVSU_TEST_DATABASE_URL is required in CI")
		}
		t.Skip("TOGAVSU_TEST_DATABASE_URL is not set")
	}
	sum := sha256.Sum256([]byte("ui/" + t.Name()))
	name := "ui" + hex.EncodeToString(sum[:8])
	role, password := name+"_app", "synthetic-ui-app-password"
	admin, err := db.OpenPostgres(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ctx := context.Background()
	for _, stmt := range []string{
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '` + name + `' AND pid <> pg_backend_pid()`,
		`DROP DATABASE IF EXISTS ` + name,
		`DROP ROLE IF EXISTS ` + role,
		`CREATE ROLE ` + role + ` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '` + password + `'`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'togavsu_audit_writer') THEN
		   CREATE ROLE togavsu_audit_writer NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE; END IF; END $$`,
		`GRANT togavsu_audit_writer TO ` + role + ` WITH ADMIN OPTION`,
		`CREATE DATABASE ` + name + ` OWNER ` + role,
	} {
		if _, err := admin.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	su, err := db.OpenPostgres(u.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := su.ExecContext(ctx, `CREATE SCHEMA core AUTHORIZATION `+role); err != nil {
		t.Fatal(err)
	}
	_ = su.Close()
	t.Cleanup(func() {
		admin, err := db.OpenPostgres(raw, "")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name)
		_, _ = admin.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+role)
	})
	u.User = url.UserPassword(role, password)
	return u.String()
}

var testKey = bytes.Repeat([]byte{9}, 32)

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
	id     string
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
	code  int
	body  string
	json  map[string]any
	loc   string
	ctype string
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
	if c.csrf != "" && ctype != "application/x-www-form-urlencoded" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{code: res.StatusCode, body: string(raw), loc: res.Header.Get("Location"), ctype: res.Header.Get("Content-Type")}
	_ = json.Unmarshal(raw, &out.json)
	return out
}

func (c *client) must(code int, method, path string, body any) resp {
	c.t.Helper()
	r := c.do(method, path, body)
	if r.code != code {
		c.t.Fatalf("%s %s: want %d, got %d: %.2000s", method, path, code, r.code, r.body)
	}
	return r
}

// form posts a browser form with the CSRF field, as the shell's forms do, and returns the
// redirect target.
func (c *client) form(path string, v url.Values) resp {
	c.t.Helper()
	if v == nil {
		v = url.Values{}
	}
	v.Set("csrf", c.csrf)
	r := c.do("POST", path, v)
	if r.code != http.StatusSeeOther {
		c.t.Fatalf("POST %s: want 303, got %d: %.1500s", path, r.code, r.body)
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

func items(t *testing.T, r resp) []map[string]any {
	t.Helper()
	raw, _ := r.json["data"].([]any)
	if raw == nil {
		raw, _ = r.json["items"].([]any)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

const adminPassword = "correct-horse-battery"

// env is one running core with the ui mounted and four signed-in users.
type env struct {
	t                         *testing.T
	recovery                  map[string][]string
	app                       *core.App
	ui                        *UI
	srv                       *httptest.Server
	admin, atty, staff, acct  *client
	area, account, activityID string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := appRoleDSN(t)
	ctx := context.Background()
	app, err := core.Open(ctx, core.Config{DatabaseURL: dsn, Key: testKey, Domain: "firm.example", MailEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	cal := os.DirFS("../../../web/templates/calendar")
	ui, err := Mount(ctx, app, Options{DatabaseURL: dsn, CalendarTemplates: cal})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(ui.Handler())
	t.Cleanup(func() {
		srv.Close()
		_ = app.Close()
	})
	e := &env{t: t, app: app, ui: ui, srv: srv, recovery: map[string][]string{}}
	e.admin = e.firstRun()
	e.atty = e.enroll("Ada Attorney", "ada@firm.example", domain.RoleAttorney)
	e.staff = e.enroll("Sam Staff", "sam@firm.example", domain.RoleStaff)
	e.acct = e.enroll("Ari Accountant", "ari@firm.example", domain.RoleAccountant)
	e.area = str(t, e.admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Probate", "stages": []string{"Intake"}}).json, "id")
	e.account = str(t, e.admin.must(http.StatusCreated, "POST", "/api/v1/trust-accounts", map[string]any{"bank": "Synthetic Bank", "name": "IOLTA", "iolta": true, "last_four": "4242"}).json, "id")
	e.activityID = str(t, e.admin.must(http.StatusCreated, "POST", "/api/v1/activity-types", map[string]any{"name": "Drafting"}).json, "id")
	return e
}

func (e *env) firstRun() *client {
	t := e.t
	admin := newClient(t, e.srv.URL)
	admin.must(http.StatusCreated, "POST", "/api/v1/setup", map[string]string{
		"firm_name": "Synthetic Firm LLP", "time_zone": "America/Chicago", "admin_name": "Avery Admin",
		"admin_email": "avery@firm.example", "password": adminPassword, "backup_passphrase": "synthetic-backup-passphrase",
	})
	login := admin.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": "avery@firm.example", "password": adminPassword})
	en := admin.must(http.StatusOK, "POST", "/api/v1/auth/totp/enroll", map[string]string{"enrollment_token": str(t, login.json, "enrollment_token")})
	ok := admin.must(http.StatusOK, "POST", "/api/v1/auth/totp/confirm", map[string]string{"enrollment_token": str(t, en.json, "enrollment_token"), "code": totp(t, str(t, en.json, "secret"), time.Now())})
	admin.csrf = str(t, ok.json, "csrf_token")
	admin.id = str(t, admin.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	return admin
}

func (e *env) session(c *client) string {
	u, _ := url.Parse(e.srv.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == identity.SessionCookie("").Name {
			return ck.Value
		}
	}
	e.t.Fatal("no session cookie")
	return ""
}

func (e *env) createUser(name, email string, contact domain.ID, roles ...domain.Role) domain.User {
	e.t.Helper()
	caller, err := e.app.Identity.Authenticate(context.Background(), identity.Presented{SessionToken: e.session(e.admin)})
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := e.app.Identity.CreateUser(context.Background(), caller, identity.NewUser{Name: name, Email: email, Password: "synthetic-password-" + name, Roles: roles, ContactID: contact})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) signIn(name, email string) *client {
	t := e.t
	t.Helper()
	c := newClient(t, e.srv.URL)
	login := c.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "synthetic-password-" + name})
	switch str(t, login.json, "status") {
	case "authenticated":
		c.csrf = str(t, login.json, "csrf_token")
	case "enrollment_required":
		en := c.must(http.StatusOK, "POST", "/api/v1/auth/totp/enroll", map[string]string{"enrollment_token": str(t, login.json, "enrollment_token")})
		done := c.must(http.StatusOK, "POST", "/api/v1/auth/totp/confirm", map[string]string{"enrollment_token": str(t, en.json, "enrollment_token"), "code": totp(t, str(t, en.json, "secret"), time.Now())})
		c.csrf = str(t, done.json, "csrf_token")
		for _, code := range done.json["recovery_codes"].([]any) {
			e.recovery[email] = append(e.recovery[email], code.(string))
		}
	case "totp_required":
		// A second computer: sign in with a recovery code, since the current TOTP code
		// was spent by the first sign-in.
		codes := e.recovery[email]
		if len(codes) == 0 {
			t.Fatal("no recovery code left for " + email)
		}
		e.recovery[email] = codes[1:]
		done := c.must(http.StatusOK, "POST", "/api/v1/auth/recovery", map[string]string{"totp_token": str(t, login.json, "totp_token"), "recovery_code": codes[0]})
		c.csrf = str(t, done.json, "csrf_token")
	default:
		t.Fatalf("login: %s", login.body)
	}
	c.id = str(t, c.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	return c
}

func (e *env) enroll(name, email string, roles ...domain.Role) *client {
	e.t.Helper()
	e.createUser(name, email, "", roles...)
	return e.signIn(name, email)
}

// matter opens a matter with a client party; returns matter id, number and client id.
func (e *env) matter(name string, extra map[string]any) (string, string, string) {
	t := e.t
	t.Helper()
	body := map[string]any{"name": name, "practice_area_id": e.area, "billing_type": "hourly", "responsible_attorney_user_id": e.atty.id, "assigned_staff_user_id": e.staff.id}
	for k, v := range extra {
		body[k] = v
	}
	m := e.admin.must(http.StatusCreated, "POST", "/api/v1/matters", body)
	parts := strings.Fields(name)
	contact := str(t, e.admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Client", "family_name": parts[len(parts)-1]}).json, "id")
	e.admin.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": str(t, m.json, "id"), "contact_id": contact, "role": "client", "primary": true})
	return str(t, m.json, "id"), str(t, m.json, "number"), contact
}

func (e *env) wall(matter string, users ...string) {
	e.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+matter+"/wall", map[string]any{"user_ids": users})
}

func (e *env) invoice(matter string) (id, number string) {
	t := e.t
	t.Helper()
	e.atty.must(http.StatusCreated, "POST", "/api/v1/time-entries", map[string]any{
		"matter_id": matter, "worked_on": time.Now().UTC().Format("2006-01-02"), "increments": 10,
		"description": "Synthetic drafting", "activity_type_id": e.activityID, "billable": true, "rate_cents": 300_00})
	drafts := items(t, e.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter}))
	id = drafts[0]["id"].(string)
	inv := e.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+id+"/approve", nil)
	return id, str(t, inv.json, "number")
}

func csrfCookie(t *testing.T, c *client) string {
	u, _ := url.Parse(c.base)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == practice.CSRFCookie {
			return ck.Value
		}
	}
	t.Fatal("no csrf cookie")
	return ""
}
