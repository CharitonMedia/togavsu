package modules_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/calendar"
	"github.com/CharitonMedia/togavsu/internal/core"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/help"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/web"
	"io/fs"
)

// testDSN creates an empty database for t, or skips when no test database is set (CI
// requires one).
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
	sum := sha256.Sum256([]byte("modules/" + t.Name()))
	name := "m" + hex.EncodeToString(sum[:8])
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
		// Module logins are cluster-wide; drop the ones this database made.
		rows, err := admin.QueryContext(context.Background(), `SELECT rolname FROM pg_roles WHERE rolname LIKE 'mod\_%'`)
		if err == nil {
			var roles []string
			for rows.Next() {
				var r string
				if rows.Scan(&r) == nil {
					roles = append(roles, r)
				}
			}
			rows.Close()
			tag := tagFor(name)
			for _, r := range roles {
				if strings.HasSuffix(r, "_"+tag) {
					_, _ = admin.ExecContext(context.Background(), `DROP ROLE IF EXISTS "`+r+`"`)
				}
			}
		}
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

func tagFor(dbName string) string {
	sum := sha256.Sum256([]byte(dbName))
	return hex.EncodeToString(sum[:4])
}

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

var testKey = bytes.Repeat([]byte{9}, 32)

// fakeRuntime stands in for the supervisor: Deploy starts the module's server in this
// process with the secrets the core handed over, so the module really uses its token,
// its webhook secret and its own database login.
type fakeRuntime struct {
	e           *env
	mu          sync.Mutex
	servers     map[string]*httptest.Server
	handlers    map[string]http.Handler
	deployments []modules.Deployment
	headers     map[string][]http.Header
	stopped     []string
}

func (f *fakeRuntime) Deploy(_ context.Context, d modules.Deployment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deployments = append(f.deployments, d)
	if old := f.servers[d.Module]; old != nil {
		old.Close()
	}
	var h http.Handler
	if custom, ok := f.handlers[d.Module]; ok {
		h = custom
	} else {
		mdb := f.e.moduleDB(d.Secrets["togavsu_db_role"], d.Secrets["togavsu_db_password"])
		h = matterclock.New(matterclock.Config{Core: f.e.srv.URL, Token: d.Secrets["togavsu_api_token"],
			WebhookSecret: d.Secrets["togavsu_webhook_secret"], DB: mdb, SkewSeconds: 300}).Handler()
	}
	module := d.Module
	f.servers[d.Module] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.headers[module] = append(f.headers[module], r.Header.Clone())
		f.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	return nil
}

func (f *fakeRuntime) Stop(_ context.Context, module string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, module)
	if s := f.servers[module]; s != nil {
		s.Close()
		delete(f.servers, module)
	}
	return nil
}

func (f *fakeRuntime) Remove(ctx context.Context, module string) error { return f.Stop(ctx, module) }

func (f *fakeRuntime) BaseURL(module string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.servers[module]; s != nil {
		return s.URL
	}
	return "http://127.0.0.1:1"
}

func (f *fakeRuntime) last(module string) modules.Deployment {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.deployments) - 1; i >= 0; i-- {
		if f.deployments[i].Module == module {
			return f.deployments[i]
		}
	}
	return modules.Deployment{}
}

type fakeBackups struct {
	mu   sync.Mutex
	made []modules.Backup
	fail error
	// during runs inside Create, before the archive is "written".
	during func()
}

// add records an archive as if the supervisor held it (an upload, say).
func (b *fakeBackups) add(bk modules.Backup) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.made = append(b.made, bk)
}

func (b *fakeBackups) Create(_ context.Context, reason string) (modules.Backup, error) {
	if b.during != nil {
		b.during()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail != nil {
		return modules.Backup{}, b.fail
	}
	id, _ := domain.NewID()
	bk := modules.Backup{ID: string(id), CreatedAt: time.Now().UTC(), SHA256: "synthetic-sha", Bytes: 1}
	b.made = append(b.made, bk)
	return bk, nil
}

func (b *fakeBackups) Get(_ context.Context, id string) (modules.Backup, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, bk := range b.made {
		if bk.ID == id {
			return bk, nil
		}
	}
	return modules.Backup{}, domain.NotFound("backup")
}

type env struct {
	t        *testing.T
	ctx      context.Context
	dsn      string
	app      *core.App
	svc      *modules.Service
	h        *modules.Handler
	help     *help.Service
	cat      *dashboard.Catalog
	rt       *fakeRuntime
	backups  *fakeBackups
	srv      *httptest.Server
	priv     ed25519.PrivateKey
	keyID    domain.ID
	market   *marketplace.Service
	users    map[string]domain.Principal
	admin    domain.Principal
	staff    domain.Principal
	attorney domain.Principal
	open     domain.Matter
	walled   domain.Matter
	activity domain.ID
	dbs      []*sql.DB
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{t: t, ctx: ctx, dsn: testDSN(t), users: map[string]domain.Principal{}}
	app, err := core.Open(ctx, core.Config{DatabaseURL: e.dsn, Key: testKey, Domain: "firm.example", MailEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	e.app = app
	t.Cleanup(func() {
		for _, d := range e.dbs {
			_ = d.Close()
		}
		_ = app.Close()
	})
	plain, err := db.OpenPostgres(e.dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := modules.Migrate(ctx, plain); err != nil {
		t.Fatal(err)
	}
	mms, err := marketplace.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: mms}); err != nil {
		t.Fatal(err)
	}
	_ = plain.Close()

	en, err := app.Identity.Setup(ctx, identity.SetupInput{FirmName: "Synthetic Firm", TimeZone: "America/Chicago", AdminName: "Ann Admin",
		AdminEmail: "ann@example.test", Password: "synthetic-password-ann", BackupPassphrase: "synthetic backup passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := app.Identity.ConfirmEnrollment(ctx, en.EnrollmentToken, totp(t, en.Secret, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	roles, err := domain.ParseRoles(res.User.Roles)
	if err != nil {
		t.Fatal(err)
	}
	e.admin = domain.Principal{UserID: res.User.ID, Roles: roles, Source: domain.SourceUI, SessionID: res.SessionID}
	e.users["admin"] = e.admin
	e.staff = e.user("Sam Staff", "sam@example.test", domain.RoleStaff)
	e.attorney = e.user("Ada Attorney", "ada@example.test", domain.RoleAttorney)

	area, err := app.Practice.CreateArea(ctx, e.admin, "Probate", "", []string{"Intake"})
	if err != nil {
		t.Fatal(err)
	}
	// Practice keeps its records in practice_records; the section 5 rows that module
	// foreign keys point at are mirrored here, as the files tests do, until practice
	// writes them itself (billing/0300 records the same ceiling).
	at := time.Now().UnixMicro()
	if _, err := app.Pool.ExecContext(ctx, `INSERT INTO core.practice_areas (id, created_at, updated_at, created_by, name, matter_types, stages, active) VALUES ($1,$2,$2,$3,'Probate','[]','[]',1)`,
		string(area.ID), at, string(e.admin.UserID)); err != nil {
		t.Fatal(err)
	}
	e.open = e.matter("Open Estate", area.ID)
	e.walled = e.matter("Walled Estate", area.ID)
	if err := app.Practice.SetWall(ctx, e.admin, e.walled.ID, []domain.ID{e.attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	name := "Drafting"
	activity, err := app.Billing.CreateType(ctx, e.admin, false, billing.TypeInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	e.activity = activity.ID

	e.help, err = help.New(help.Deps{Store: help.NewMemory(), Settings: app.Settings, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	e.cat = dashboard.NewCatalog(nil)
	e.rt = &fakeRuntime{e: e, servers: map[string]*httptest.Server{}, handlers: map[string]http.Handler{}, headers: map[string][]http.Header{}}
	t.Cleanup(func() {
		for _, s := range e.rt.servers {
			s.Close()
		}
	})
	e.backups = &fakeBackups{}
	// The marketplace component owns the trust root: trusted local keys, published keys
	// and the package signature check (PRD 9.3).
	users, ok := app.Users.(marketplace.Users)
	if !ok {
		t.Fatal("the core user store doesn't list users")
	}
	e.market, err = marketplace.New(marketplace.Deps{DB: app.Pool, Audit: app.Audit, Settings: app.Settings, Users: users})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = e.service(t, nil)
	e.h = modules.NewHandler(e.svc, e.auth)
	e.srv = httptest.NewServer(e.compose())
	t.Cleanup(e.srv.Close)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	e.priv = priv
	e.keyID = e.trustKey("Sample module key", pub)
	return e
}

// trustKey adds pub as a trusted local key through the marketplace component and
// returns the key's id.
func (e *env) trustKey(name string, pub ed25519.PublicKey) domain.ID {
	e.t.Helper()
	pem := marketplace.PublicKeyPEM(pub)
	key, err := e.market.CreateKey(e.ctx, e.admin, marketplace.KeyInput{Name: &name, PublicKeyPEM: &pem})
	if err != nil {
		e.t.Fatal(err)
	}
	return key.ID
}

// service is a module runtime over the env's database and fakes, with clock now
// (nil for the wall clock).
func (e *env) service(t *testing.T, now func() time.Time) *modules.Service {
	t.Helper()
	app := e.app
	svc, err := modules.New(modules.Deps{DB: app.Pool, Key: testKey, Audit: app.Audit, Settings: app.Settings, Walls: app.Practice.Policy.Walls, Contacts: app.Practice, Signing: e.market,
		Users: app.Users, Tokens: modules.IdentityTokens{Svc: app.Identity}, Help: e.help, Widgets: e.cat, Runtime: e.rt, Backups: e.backups, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func (e *env) user(name, email string, roles ...domain.Role) domain.Principal {
	e.t.Helper()
	u, err := e.app.Identity.CreateUser(e.ctx, identity.Caller{Principal: e.admin}, identity.NewUser{Name: name, Email: email, Password: "synthetic-password-" + email, Roles: roles})
	if err != nil {
		e.t.Fatal(err)
	}
	p := domain.Principal{UserID: u.ID, Roles: roles, Source: domain.SourceUI}
	e.users[string(roles[0])] = p
	return p
}

func (e *env) matter(name string, area domain.ID) domain.Matter {
	e.t.Helper()
	m, err := e.app.Practice.CreateMatter(e.ctx, e.attorney, practice.MatterInput{Name: name, PracticeAreaID: area, BillingType: "hourly", ResponsibleAttorneyID: e.attorney.UserID})
	if err != nil {
		e.t.Fatal(err)
	}
	at := time.Now().UnixMicro()
	if _, err := e.app.Pool.ExecContext(e.ctx, `INSERT INTO core.matters (id, created_at, updated_at, created_by, number, name, practice_area_id, status, stage, case_number, court, client_position,
opened_on, closed_on, close_reason, statute_of_limitations, billing_type, flat_fee_cents, matter_rate_cents, responsible_attorney_id)
VALUES ($1,$2,$2,$3,$4,$5,$6,'open','','','','','','','','','hourly',0,0,$7)`, string(m.ID), at, string(e.admin.UserID), m.Number, m.Name, string(area), string(e.attorney.UserID)); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) logTime(p domain.Principal, matter domain.ID, increments int) {
	e.t.Helper()
	date := time.Now().UTC().Format(time.DateOnly)
	desc := "Synthetic work"
	if _, err := e.app.Billing.CreateTime(e.ctx, p, billing.TimeInput{MatterID: &matter, WorkedOn: &date, Increments: &increments, Description: &desc, ActivityTypeID: &e.activity}); err != nil {
		e.t.Fatal(err)
	}
}

// auth is the test core's caller: a real bearer token through identity, or a test header
// naming a signed-in user (standing in for the session cookie and CSRF the core checks).
func (e *env) auth(r *http.Request) (modules.Caller, error) {
	if b, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		c, err := e.app.Identity.Authenticate(r.Context(), identity.Presented{Bearer: b})
		if err != nil {
			return modules.Caller{}, err
		}
		return modules.Caller{Principal: c.Principal, Scopes: c.Scopes, Bearer: true}, nil
	}
	if who := r.Header.Get("X-Test-User"); who != "" {
		if p, ok := e.users[who]; ok {
			return modules.Caller{Principal: p}, nil
		}
	}
	return modules.Caller{}, domain.Unauthenticated("sign in")
}

// principal is the delegating principal the core's domain handlers get.
func (e *env) principal(r *http.Request) (domain.Principal, error) {
	c, err := e.auth(r)
	if err != nil {
		return domain.Principal{}, err
	}
	if tok := r.Header.Get(modules.ContextHeader); tok != "" {
		return e.svc.Delegate(r.Context(), c.Principal, tok)
	}
	return c.Principal, nil
}

// compose builds the core surface the way integrate-extensions wires it: the module
// routes beside the real billing, calendar and practice handlers, all behind the
// domain-replacement router and the include= reader.
func (e *env) compose() http.Handler {
	sub, err := fs.Sub(web.Practice, "templates/practice")
	if err != nil {
		e.t.Fatal(err)
	}
	ph, err := practice.NewHandlerFS(e.app.Practice, sub, e.principal)
	if err != nil {
		e.t.Fatal(err)
	}
	bh, err := billing.NewHandler(e.app.Billing, nil, e.principal)
	if err != nil {
		e.t.Fatal(err)
	}
	cs, err := calendar.New(calendar.Deps{Store: calendar.NewMemory(), Policy: e.app.Practice.Policy, Matters: calendar.PracticeMatters{Svc: e.app.Practice},
		Users: calendar.UsersFunc(e.app.Users.Get), Settings: e.app.Settings, Audit: e.app.Audit})
	if err != nil {
		e.t.Fatal(err)
	}
	ch, err := calendar.NewHandler(cs, nil, e.principal)
	if err != nil {
		e.t.Fatal(err)
	}
	mods := e.h.Routes().(*http.ServeMux)
	mk := (&marketplace.Handler{Svc: e.market, Principal: e.principal}).Routes().(*http.ServeMux)
	domains := []*http.ServeMux{mods, mk, ch.Routes().(*http.ServeMux), bh.Routes().(*http.ServeMux), ph.Routes().(*http.ServeMux)}
	top := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, m := range domains {
			if _, pattern := m.Handler(r); pattern != "" {
				m.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
	return e.svc.Router(e.principal, e.h.Include(top))
}

func (e *env) moduleDB(role, password string) *sql.DB {
	e.t.Helper()
	u, err := url.Parse(e.dsn)
	if err != nil {
		e.t.Fatal(err)
	}
	u.User = url.UserPassword(role, password)
	d, err := db.OpenPostgres(u.String(), "")
	if err != nil {
		e.t.Fatal(err)
	}
	e.dbs = append(e.dbs, d)
	return d
}

type resp struct {
	code int
	body string
	json map[string]any
}

func (e *env) do(method, path string, body any, hdr ...string) resp {
	e.t.Helper()
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("package", "module.tar.gz")
		_, _ = fw.Write(b)
		_ = mw.Close()
		rd, ctype = &buf, mw.FormDataContentType()
	default:
		raw, _ := json.Marshal(b)
		rd, ctype = bytes.NewReader(raw), "application/json"
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{code: res.StatusCode, body: string(raw)}
	_ = json.Unmarshal(raw, &out.json)
	return out
}

func (e *env) as(who string) []string { return []string{"X-Test-User", who} }

func (e *env) build(edit func(map[string][]byte)) []byte {
	e.t.Helper()
	raw, err := matterclock.Build(e.priv, edit)
	if err != nil {
		e.t.Fatal(err)
	}
	return raw
}

// installSample installs and approves the sample module over the API.
func (e *env) installSample() {
	e.t.Helper()
	r := e.do("POST", "/api/v1/modules", e.build(nil), e.as("admin")...)
	if r.code != http.StatusAccepted || r.json["status"] != modules.StatusAwaiting {
		e.t.Fatalf("install: %d %s", r.code, r.body)
	}
	e.approveAll("matter-clock")
}

func (e *env) approveAll(module string) resp {
	e.t.Helper()
	v, err := e.svc.Get(e.ctx, e.admin, module)
	if err != nil {
		e.t.Fatal(err)
	}
	var ds []modules.Decision
	for _, p := range v.Permissions {
		if !p.Approved {
			ds = append(ds, modules.Decision{Scope: p.Scope, Decision: "approve"})
		}
	}
	r := e.do("POST", "/api/v1/modules/"+module+"/permissions", map[string]any{"decisions": ds}, e.as("admin")...)
	if r.code != http.StatusOK {
		e.t.Fatalf("approve %s: %d %s", module, r.code, r.body)
	}
	return r
}

func (e *env) contextFor(who, module string, matter domain.ID) string {
	e.t.Helper()
	ct, err := e.svc.IssueContext(e.ctx, e.users[who], module, matter)
	if err != nil {
		e.t.Fatal(err)
	}
	return ct.Token
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.app.Pool.QueryRowContext(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) schemaExists(module string) bool {
	return e.count(`SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1`, modules.SchemaFor(module)) == 1
}
