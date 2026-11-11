package marketplace_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/internal/trust"
	service "github.com/CharitonMedia/togavsu/marketplace"
)

// appDB is a migrated database owned by a fresh non-superuser role, connected as that
// role, as togavsu_app is in Compose. It needs TOGAVSU_TEST_DATABASE_URL (a superuser on a
// disposable Postgres 17) and skips without it.
func appDB(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TOGAVSU_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := db.OpenPostgres(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	sum := sha256.Sum256([]byte("marketplace/" + t.Name()))
	tag := hex.EncodeToString(sum[:8])
	name, role := "mk"+tag, "mkapp"+tag
	const password = "synthetic-marketplace-app-password"
	for _, stmt := range []string{
		`DROP DATABASE IF EXISTS ` + name, `DROP ROLE IF EXISTS ` + role,
		`CREATE ROLE ` + role + ` LOGIN NOSUPERUSER NOCREATEROLE NOCREATEDB PASSWORD '` + password + `'`,
		`CREATE DATABASE ` + name + ` OWNER ` + role,
	} {
		if _, err := admin.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		admin, err := db.OpenPostgres(raw, "")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name)
		_, _ = admin.ExecContext(context.Background(), `DROP ROLE IF EXISTS `+role)
	})
	u, _ := url.Parse(raw)
	u.Path = "/" + name
	superURL := u.String()
	u.User = url.UserPassword(role, password)
	plain, err := db.OpenPostgres(u.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	var ms []db.Migration
	for _, load := range []func() ([]db.Migration, error){db.Embedded, trust.Migrations, dashboard.Migrations, marketplace.Migrations} {
		m, err := load()
		if err != nil {
			t.Fatal(err)
		}
		ms = append(ms, m...)
	}
	if err := db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
	_ = plain.Close()
	super, err := db.OpenPostgres(superURL, "core")
	if err != nil {
		t.Fatal(err)
	}
	defer super.Close()
	if err := audit.InstallPrivileges(ctx, super); err != nil {
		t.Fatal(err)
	}
	if _, err := super.ExecContext(ctx, `GRANT togavsu_audit_writer TO `+role+` WITH ADMIN OPTION`); err != nil {
		t.Fatal(err)
	}
	app, err := db.OpenPostgres(u.String(), "core")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	if err := audit.InstallPrivileges(ctx, app); err != nil {
		t.Fatal(err)
	}
	return app
}

type settingsMap struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *settingsMap) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key], nil
}

func (s *settingsMap) set(k, v string) { s.mu.Lock(); s.m[k] = v; s.mu.Unlock() }

type users []domain.User

func (u users) Get(_ context.Context, id domain.ID) (domain.User, error) {
	for _, x := range u {
		if x.ID == id {
			return x, nil
		}
	}
	return domain.User{}, domain.NotFound("user")
}

func (u users) List(_ context.Context, f repository.Filter) (repository.Page[domain.User], error) {
	// Two pages, so the service must follow the cursor to reach every admin.
	if f.Cursor == "" {
		return repository.Page[domain.User]{Items: u[:1], NextCursor: "p2"}, nil
	}
	return repository.Page[domain.User]{Items: u[1:]}, nil
}

type installed struct {
	mu sync.Mutex
	m  []marketplace.InstalledModule
	// calls counts reads; the firm's modules are never changed by an alert.
	calls int
}

func (i *installed) InstalledModules(context.Context) ([]marketplace.InstalledModule, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls++
	return append([]marketplace.InstalledModule(nil), i.m...), nil
}

const opToken = "synthetic-operator-token-0123456789abcdef"

type world struct {
	t        *testing.T
	ctx      context.Context
	db       *sql.DB
	svc      *marketplace.Service
	set      *settingsMap
	inst     *installed
	market   *httptest.Server
	now      time.Time
	admin    domain.Principal
	admin2   domain.Principal
	staff    domain.Principal
	author   string
	priv     ed25519.PrivateKey
	keyID    string
	adminA   domain.User
	adminB   domain.User
	staffU   domain.User
	inactive domain.User
}

func (w *world) clock() time.Time { return w.now }

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, ctx: context.Background(), db: appDB(t), now: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	// The marketplace service runs separately, on its own data directory.
	cfg := service.Defaults()
	cfg.SecureCookies = false
	m, err := service.Open(t.TempDir(), cfg.AuthorSession(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := service.NewServer(m, cfg, opToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.market = httptest.NewServer(srv.Handler())
	t.Cleanup(w.market.Close)

	mk := func(name, email, roles string, active bool) domain.User {
		id, _ := domain.NewID()
		act := 0
		if active {
			act = 1
		}
		if _, err := w.db.ExecContext(w.ctx, `INSERT INTO users (id, created_at, updated_at, created_by, name, email, roles, active, default_hourly_rate_cents, bar_number, bar_state) VALUES ($1,1,1,$1,$2,$3,$4,$5,0,'','')`,
			string(id), name, email, roles, act); err != nil {
			t.Fatal(err)
		}
		return domain.User{Meta: domain.Meta{ID: id}, Name: name, Email: email, Roles: roles, Active: active}
	}
	w.adminA = mk("Admin A", "admin-a@example.test", `["admin"]`, true)
	w.staffU = mk("Staff", "staff@example.test", `["staff"]`, true)
	w.adminB = mk("Admin B", "admin-b@example.test", `["admin","attorney"]`, true)
	w.inactive = mk("Gone Admin", "gone@example.test", `["admin"]`, false)
	// Admin B turned module-alert email off for themself (PRD 8.17); in-app stays on.
	if _, err := w.db.ExecContext(w.ctx, `INSERT INTO notification_preferences (user_id, kind, updated_at, in_app, email) VALUES ($1, 'module_alert', 1, 1, 0)`, string(w.adminB.ID)); err != nil {
		t.Fatal(err)
	}
	w.admin = domain.Principal{UserID: w.adminA.ID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
	w.admin2 = domain.Principal{UserID: w.adminB.ID, Roles: []domain.Role{domain.RoleAdmin, domain.RoleAttorney}, Source: domain.SourceUI}
	w.staff = domain.Principal{UserID: w.staffU.ID, Roles: []domain.Role{domain.RoleStaff}, Source: domain.SourceUI}

	w.set = &settingsMap{m: map[string]string{
		"modules.marketplace_address": w.market.URL,
		"modules.alert_check":         "daily",
		"notifications.defaults":      domain.DefaultNotificationPrefs,
		"api.page_size":               "50",
		"api.max_page_size":           "200",
	}}
	au, err := audit.New(audit.NewPostgres(w.db), w.clock)
	if err != nil {
		t.Fatal(err)
	}
	w.inst = &installed{}
	w.svc, err = marketplace.New(marketplace.Deps{DB: w.db, Audit: au, Settings: w.set, Users: users{w.adminA, w.staffU, w.adminB, w.inactive}, Installed: w.inst, Now: w.clock})
	if err != nil {
		t.Fatal(err)
	}
	w.registerAuthor()
	return w
}

func (w *world) call(method, path, token string, body any) (int, []byte) {
	w.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.market.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (w *world) registerAuthor() {
	w.call("POST", "/v1/authors", "", map[string]any{"name": "Synthetic Author", "email": "author@example.test", "password": "synthetic author password"})
	_, b := w.call("POST", "/v1/sessions", "", map[string]any{"email": "author@example.test", "password": "synthetic author password"})
	var tok struct{ Token string }
	json.Unmarshal(b, &tok)
	w.author = tok.Token
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, b = w.call("POST", "/v1/authors/me/keys", w.author, map[string]any{"public_key_pem": marketplace.PublicKeyPEM(pub)})
	var k service.KeyView
	json.Unmarshal(b, &k)
	w.call("POST", "/v1/authors/me/keys/"+k.KeyID+"/verify", w.author, map[string]any{"signature": marketplace.Sign(priv, marketplace.KeyProofMessage(k.KeyID, k.Challenge))})
	w.priv, w.keyID = priv, k.KeyID
}

func modulePackage(t *testing.T, id, version string) []byte {
	man, _ := json.Marshal(map[string]any{"id": id, "name": "Module " + id, "version": version, "permissions": []string{"matters:read", "time:read"}})
	p, err := marketplace.BuildPackage(man, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (w *world) publish(id, version string) []byte {
	p := modulePackage(w.t, id, version)
	code, b := w.call("POST", "/v1/authors/me/packages", w.author, map[string]any{"package_base64": base64.StdEncoding.EncodeToString(p), "signature": marketplace.Sign(w.priv, p), "key_id": w.keyID})
	if code != http.StatusCreated {
		w.t.Fatalf("publish %d %s", code, b)
	}
	return p
}

func (w *world) count(q string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.db.QueryRowContext(w.ctx, q, args...).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func status(err error) int {
	var me *marketplace.Error
	if errors.As(err, &me) {
		return me.HTTPStatus()
	}
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.HTTPStatus()
	}
	return 0
}

// TestPGFlagAndPullAlertInstalledFirms is AC-015 end to end: the real marketplace
// service, the core's client side on Postgres, and the module-alerts widget.
func TestPGFlagAndPullAlertInstalledFirms(t *testing.T) {
	w := newWorld(t)
	w.publish("matter-clock", "0.1.0")
	w.publish("other-module", "1.0.0")
	w.inst.m = []marketplace.InstalledModule{{ID: "matter-clock", Version: "0.1.0"}}

	// Browsing shows the PRD 10.2 listing; only admins may browse.
	ls, err := w.svc.Listings(w.ctx, w.admin, "matter")
	if err != nil || len(ls) != 1 || ls[0].Author != "Synthetic Author" || ls[0].SigningKey != w.keyID || ls[0].Notice != marketplace.Notice {
		t.Fatalf("browse %+v %v", ls, err)
	}
	if _, err := w.svc.Listings(w.ctx, w.staff, ""); status(err) != http.StatusForbidden {
		t.Fatalf("staff browse: %v", err)
	}

	// No flag yet: the check finds nothing.
	res, err := w.svc.Check(w.ctx)
	if err != nil || len(res.New) != 0 || res.Checked != 1 {
		t.Fatalf("first check %+v %v", res, err)
	}

	// The operator flags the module.
	note := "Reports that totals are wrong on closed matters."
	if code, b := w.call("POST", "/v1/operator/listings/matter-clock/flag", opToken, map[string]any{"note": note}); code != http.StatusOK {
		t.Fatalf("flag %d %s", code, b)
	}
	// The listing shows the warning, in the API and on the listing page.
	l, err := w.svc.Listing(w.ctx, w.admin, "matter-clock")
	if err != nil || !l.Flagged || l.FlagText() != "We've been alerted that this item has problems. "+note {
		t.Fatalf("flagged listing %+v %v", l, err)
	}
	_, page := w.call("GET", "/listings/matter-clock", "", nil)
	if !strings.Contains(string(page), "We&#39;ve been alerted that this item has problems. "+note) {
		t.Fatal("listing page lacks the flag warning")
	}

	// Installs stay possible, and the warning shows again at install.
	v, err := w.svc.FetchForInstall(w.ctx, w.admin, "matter-clock", "0.1.0")
	if err != nil || v.ModuleID != "matter-clock" || v.Version != "0.1.0" || v.Source != "marketplace" || v.Warning != l.FlagText() || v.Signer.KeyID != w.keyID {
		t.Fatalf("flagged install %+v %v", v, err)
	}
	if _, err := w.svc.FetchForInstall(w.ctx, w.staff, "matter-clock", "0.1.0"); status(err) != http.StatusForbidden {
		t.Fatalf("staff install: %v", err)
	}
	// Keep the signed package file the firm downloaded, for the local install below.
	_, raw := w.call("GET", "/v1/listings/matter-clock/versions/0.1.0/package", "", nil)

	// The installed firm's check delivers the alert: dashboard, notification, email.
	w.now = w.now.Add(time.Minute)
	res, err = w.svc.Check(w.ctx)
	if err != nil || len(res.New) != 1 {
		t.Fatalf("flag check %+v %v", res, err)
	}
	a := res.New[0]
	if a.Kind != "flag" || a.ModuleID != "matter-clock" || a.Note != note || a.Warning != "We've been alerted that this item has problems. "+note || a.InstalledVersion != "0.1.0" {
		t.Fatalf("alert %+v", a)
	}
	// In-app for both active admins; nothing for staff or the deactivated admin.
	for _, u := range []domain.User{w.adminA, w.adminB} {
		if n := w.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'module_alert' AND link = '/admin?tab=modules' AND read_at = 0`, string(u.ID)); n != 1 {
			t.Fatalf("%s notifications: %d", u.Name, n)
		}
	}
	for _, u := range []domain.User{w.staffU, w.inactive} {
		if n := w.count(`SELECT count(*) FROM notifications WHERE user_id = $1`, string(u.ID)); n != 0 {
			t.Fatalf("%s got a notification", u.Name)
		}
	}
	// Email to every admin who keeps the default; admin B turned it off.
	var to, subject, body string
	if err := w.db.QueryRowContext(w.ctx, `SELECT to_address, subject, body FROM mail_outbox WHERE kind = 'module_alert' AND record_id = $1`, string(a.ID)).Scan(&to, &subject, &body); err != nil {
		t.Fatalf("one email: %v (%d rows)", err, w.count(`SELECT count(*) FROM mail_outbox`))
	}
	if to != "admin-a@example.test" || !strings.Contains(subject, "matter-clock") || !strings.Contains(body, "We've been alerted that this item has problems. "+note) || !strings.Contains(body, "Operator's note: "+note) || !strings.Contains(body, "Nothing has been switched off. You decide") {
		t.Fatalf("email %q %q %q", to, subject, body)
	}
	// The trust mail drain sends it like every other system mail.
	tr := trustDrain(t, w)
	sent := &sink{}
	if _, err := tr.DeliverMail(w.ctx, sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.to) != 1 || sent.to[0] != "admin-a@example.test" {
		t.Fatalf("delivered %v", sent.to)
	}
	// The alert is audited under System.
	if n := w.count(`SELECT count(*) FROM audit_log WHERE entity = 'module_alerts' AND entity_id = $1 AND source = 'system'`, string(a.ID)); n != 1 {
		t.Fatalf("audit rows %d", n)
	}

	// A second check, or the admin opening the modules screen, delivers nothing twice.
	w.now = w.now.Add(time.Hour)
	if res, err := w.svc.Check(w.ctx); err != nil || len(res.New) != 0 {
		t.Fatalf("repeat check %+v %v", res, err)
	}
	alerts, st, err := w.svc.OpenModulesScreen(w.ctx, w.admin2)
	if err != nil || len(alerts) != 1 || st.LastError != "" || st.SucceededAt.IsZero() {
		t.Fatalf("modules screen %+v %+v %v", alerts, st, err)
	}
	if n := w.count(`SELECT count(*) FROM notifications`); n != 2 {
		t.Fatalf("notifications after repeat: %d", n)
	}

	// The admin dashboard's module-alerts widget shows it; other roles get nothing.
	cat := dashboard.NewCatalog(map[string]dashboard.Source{"module-alerts": w.svc.WidgetSource()})
	wd, err := cat.Load(w.ctx, w.admin, "module-alerts", nil)
	if err != nil || len(wd.Rows) != 1 || wd.Rows[0].Subtitle != a.Warning || wd.Target.URL() != "/admin?tab=modules" || !strings.HasPrefix(wd.Rows[0].TargetPath, "/admin?tab=modules#alert-") {
		t.Fatalf("widget %+v %v", wd, err)
	}
	if _, err := cat.Load(w.ctx, w.staff, "module-alerts", nil); err == nil {
		t.Fatal("staff loaded the module alerts widget")
	}

	// Nothing was switched off: the installed module list was only read.
	if got, _ := w.inst.InstalledModules(w.ctx); len(got) != 1 || got[0].Version != "0.1.0" {
		t.Fatalf("installed modules changed: %+v", got)
	}

	// The operator pulls the module.
	if code, b := w.call("POST", "/v1/operator/listings/matter-clock/pull", opToken, map[string]any{"note": "Pulled at the author's request."}); code != http.StatusOK {
		t.Fatalf("pull %d %s", code, b)
	}
	// It disappears from search and can't be installed from the marketplace.
	if ls, err := w.svc.Listings(w.ctx, w.admin, "matter"); err != nil || len(ls) != 0 {
		t.Fatalf("pulled module in browse: %+v %v", ls, err)
	}
	if _, err := w.svc.FetchForInstall(w.ctx, w.admin, "matter-clock", "0.1.0"); status(err) != http.StatusGone {
		t.Fatalf("marketplace install of a pulled module: %v", err)
	}
	if _, err := w.svc.FetchForInstall(w.ctx, w.admin, "matter-clock", ""); status(err) != http.StatusGone {
		t.Fatalf("marketplace install of a pulled module, latest: %v", err)
	}
	// Installed firms get a pull alert by dashboard and email.
	w.now = w.now.Add(time.Minute)
	res, err = w.svc.Check(w.ctx)
	if err != nil || len(res.New) != 1 || res.New[0].Kind != "pull" || !strings.Contains(res.New[0].Warning, "Pulled at the author's request.") {
		t.Fatalf("pull check %+v %v", res, err)
	}
	if n := w.count(`SELECT count(*) FROM mail_outbox WHERE kind = 'module_alert' AND record_id = $1 AND to_address = 'admin-a@example.test'`, string(res.New[0].ID)); n != 1 {
		t.Fatalf("pull email %d", n)
	}

	// It can still be installed from a local file, even with the marketplace gone.
	w.market.Close()
	lv, err := w.svc.VerifyLocalFile(w.ctx, w.admin, raw)
	if err != nil || lv.ModuleID != "matter-clock" || lv.Version != "0.1.0" || lv.Source != "local" || lv.Signer.KeyID != w.keyID {
		t.Fatalf("local install of a pulled module: %+v %v", lv, err)
	}
	// A tampered local file is still refused.
	var sp marketplace.SignedPackage
	json.Unmarshal(raw, &sp)
	pkg, _ := base64.StdEncoding.DecodeString(sp.PackageBase64)
	bad := append([]byte{}, pkg...)
	bad[len(bad)/2] ^= 1
	if _, err := w.svc.VerifyLocal(w.ctx, w.admin, bad, sp.Signature, sp.KeyID); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("tampered local file: %v", err)
	}
	if _, err := w.svc.VerifyLocal(w.ctx, w.staff, pkg, sp.Signature, sp.KeyID); status(err) != http.StatusForbidden {
		t.Fatalf("staff local install: %v", err)
	}
	// An unreachable marketplace is recorded, not hidden.
	w.now = w.now.Add(time.Minute)
	if _, err := w.svc.Check(w.ctx); err == nil {
		t.Fatal("check against a closed marketplace succeeded")
	}
	if st, _ := w.svc.Status(w.ctx, w.admin); st.LastError == "" {
		t.Fatal("check failure not recorded")
	}
}

// TestPGVersionFlagAndSchedule covers a flag on one version, the alert check schedule and
// the firm's own signing key.
func TestPGVersionFlagAndSchedule(t *testing.T) {
	w := newWorld(t)
	w.publish("matter-clock", "0.1.0")
	w.publish("matter-clock", "0.2.0")
	w.inst.m = []marketplace.InstalledModule{{ID: "matter-clock", Version: "0.2.0"}}

	// A flag on a version the firm doesn't run doesn't alert it.
	w.call("POST", "/v1/operator/listings/matter-clock/flag", opToken, map[string]any{"note": "old version only", "version": "0.1.0"})
	if res, err := w.svc.Check(w.ctx); err != nil || len(res.New) != 0 {
		t.Fatalf("other-version flag %+v %v", res, err)
	}
	// Installing that version from the marketplace shows its warning again.
	v, err := w.svc.FetchForInstall(w.ctx, w.admin, "matter-clock", "0.1.0")
	if err != nil || v.Warning != "We've been alerted that this item has problems. old version only" {
		t.Fatalf("version flag at install %+v %v", v, err)
	}
	if v, err := w.svc.FetchForInstall(w.ctx, w.admin, "matter-clock", "0.2.0"); err != nil || v.Warning != "" {
		t.Fatalf("unflagged version %+v %v", v, err)
	}
	// After the firm downgrades to the flagged version, the next check alerts it.
	w.inst.m = []marketplace.InstalledModule{{ID: "matter-clock", Version: "0.1.0"}}

	// Schedule: just checked, so not due until a day passes (default daily).
	if due, err := w.svc.Due(w.ctx); err != nil || due {
		t.Fatalf("due right after a check: %v %v", due, err)
	}
	w.now = w.now.Add(23 * time.Hour)
	if ran, _, _ := w.svc.RunDue(w.ctx); ran {
		t.Fatal("ran before the daily period")
	}
	w.set.set("modules.alert_check", "hourly")
	ran, res, err := w.svc.RunDue(w.ctx)
	if !ran || err != nil || len(res.New) != 1 || res.New[0].Version != "0.1.0" {
		t.Fatalf("hourly run %v %+v %v", ran, res, err)
	}
	w.set.set("modules.alert_check", "weekly")
	w.now = w.now.Add(25 * time.Hour)
	if due, _ := w.svc.Due(w.ctx); due {
		t.Fatal("weekly check due after a day")
	}
	// A failed check retries after the short retry period, not a week later.
	w.set.set("modules.marketplace_address", "http://marketplace.example.test")
	if _, err := w.svc.Check(w.ctx); err == nil {
		t.Fatal("check with a non-loopback http address")
	}
	if due, _ := w.svc.Due(w.ctx); due {
		t.Fatal("retry right away")
	}
	w.now = w.now.Add(16 * time.Minute)
	if due, _ := w.svc.Due(w.ctx); !due {
		t.Fatal("no retry after a failure")
	}
	w.set.set("modules.marketplace_address", w.market.URL)

	// A firm that patches a module signs its own build with a trusted local key.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	patched := modulePackage(t, "matter-clock", "0.1.0-firm.1")
	sig := marketplace.Sign(priv, patched)
	if _, err := w.svc.VerifyLocal(w.ctx, w.admin, patched, sig, marketplace.KeyID(pub)); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("untrusted firm key: %v", err)
	}
	name, pemText := "Firm build key", marketplace.PublicKeyPEM(pub)
	if _, err := w.svc.CreateKey(w.ctx, w.staff, marketplace.KeyInput{Name: &name, PublicKeyPEM: &pemText}); status(err) != http.StatusForbidden {
		t.Fatalf("staff added a key: %v", err)
	}
	k, err := w.svc.CreateKey(w.ctx, w.admin, marketplace.KeyInput{Name: &name, PublicKeyPEM: &pemText})
	if err != nil || k.KeyID != marketplace.KeyID(pub) {
		t.Fatalf("create key %+v %v", k, err)
	}
	if _, err := w.svc.CreateKey(w.ctx, w.admin, marketplace.KeyInput{Name: &name, PublicKeyPEM: &pemText}); status(err) != http.StatusConflict {
		t.Fatalf("duplicate key: %v", err)
	}
	lv, err := w.svc.VerifyLocal(w.ctx, w.admin, patched, sig, k.KeyID)
	if err != nil || lv.Signer.Source != "local" || lv.Version != "0.1.0-firm.1" || lv.Warning != "" {
		t.Fatalf("firm-signed build %+v %v", lv, err)
	}
	if n := w.count(`SELECT count(*) FROM audit_log WHERE entity = 'trusted_signing_keys' AND entity_id = $1`, string(k.ID)); n != 2 {
		t.Fatalf("key audit rows %d", n)
	}
	renamed := "Firm key 2026"
	if k2, err := w.svc.UpdateKey(w.ctx, w.admin, k.ID, marketplace.KeyInput{Name: &renamed}); err != nil || k2.Name != renamed || k2.KeyID != k.KeyID {
		t.Fatalf("rename %+v %v", k2, err)
	}
	if err := w.svc.DeleteKey(w.ctx, w.admin, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.VerifyLocal(w.ctx, w.admin, patched, sig, k.KeyID); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("deleted key still trusted: %v", err)
	}
	if err := w.svc.DeleteKey(w.ctx, w.admin, k.ID); status(err) != http.StatusNotFound {
		t.Fatalf("delete twice: %v", err)
	}
}

// TestPGHTTPRoutes checks the core routes' access and shapes.
func TestPGHTTPRoutes(t *testing.T) {
	w := newWorld(t)
	w.publish("matter-clock", "0.1.0")
	w.inst.m = []marketplace.InstalledModule{{ID: "matter-clock", Version: "0.1.0"}}
	w.call("POST", "/v1/operator/listings/matter-clock/flag", opToken, map[string]any{"note": "route test"})
	who := w.admin
	h := &marketplace.Handler{Svc: w.svc, Principal: func(*http.Request) (domain.Principal, error) { return who, nil }}
	core := httptest.NewServer(h.Routes())
	defer core.Close()
	get := func(path string) (int, map[string]any) {
		res, err := http.Get(core.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	// Opening the modules screen (check=true) asks the marketplace and lists the alert.
	code, out := get("/api/v1/module-alerts?check=true")
	data, _ := out["data"].([]any)
	if code != http.StatusOK || len(data) != 1 {
		t.Fatalf("module alerts %d %v", code, out)
	}
	al := data[0].(map[string]any)
	if al["kind"] != "flag" || al["note"] != "route test" || al["warning"] != "We've been alerted that this item has problems. route test" {
		t.Fatalf("alert json %v", al)
	}
	if code, one := get("/api/v1/module-alerts/" + al["id"].(string)); code != http.StatusOK || one["module_id"] != "matter-clock" {
		t.Fatalf("one alert %d %v", code, one)
	}
	if code, _ := get("/api/v1/module-alerts/not-a-uuid"); code != http.StatusNotFound {
		t.Fatalf("bad alert id %d", code)
	}
	code, out = get("/api/v1/marketplace/listings?q=clock")
	data, _ = out["data"].([]any)
	if code != http.StatusOK || len(data) != 1 || data[0].(map[string]any)["flagged"] != true || data[0].(map[string]any)["notice"] != marketplace.Notice {
		t.Fatalf("listings %d %v", code, out)
	}
	if code, l := get("/api/v1/marketplace/listings/matter-clock"); code != http.StatusOK || l["flag_note"] != "route test" || l["signing_key"] != w.keyID {
		t.Fatalf("listing %d %v", code, l)
	}
	if code, _ := get("/api/v1/marketplace/listings?limit=999999"); code != http.StatusBadRequest {
		t.Fatalf("limit over the cap %d", code)
	}
	// A browser write without the CSRF echo is refused; a bearer write is not.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	body, _ := json.Marshal(map[string]any{"name": "k", "public_key_pem": marketplace.PublicKeyPEM(pub)})
	req, _ := http.NewRequest("POST", core.URL+"/api/v1/trusted-signing-keys", bytes.NewReader(body))
	req.Header.Set("Cookie", "togavsu_session=x")
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", core.URL+"/api/v1/trusted-signing-keys", bytes.NewReader(body))
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create key %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", core.URL+"/api/v1/trusted-signing-keys", bytes.NewReader([]byte(`{"name":"x","public_key_pem":"nope"}`)))
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad key %d", res.StatusCode)
	}
	if code, out := get("/api/v1/trusted-signing-keys"); code != http.StatusOK || len(out["data"].([]any)) != 1 {
		t.Fatalf("keys %d %v", code, out)
	}
	// Every route is admin-only, in the service.
	who = w.staff
	for _, p := range []string{"/api/v1/module-alerts", "/api/v1/marketplace/listings", "/api/v1/marketplace/listings/matter-clock", "/api/v1/trusted-signing-keys"} {
		if code, _ := get(p); code != http.StatusForbidden {
			t.Fatalf("staff %s: %d", p, code)
		}
	}
	who = domain.Principal{UserID: w.staffU.ID, Roles: []domain.Role{domain.RoleClient}, Source: domain.SourceAPIToken}
	if code, _ := get("/api/v1/module-alerts"); code != http.StatusForbidden {
		t.Fatalf("client: %d", code)
	}
}

type sink struct{ to []string }

func (s *sink) Send(_ context.Context, to, _, _ string) error {
	s.to = append(s.to, to)
	return nil
}

// trustDrain is the trust service, whose DeliverMail drains the shared system mail outbox
// in the running core.
func trustDrain(t *testing.T, w *world) *trust.Service {
	t.Helper()
	au, err := audit.New(audit.NewPostgres(w.db), w.clock)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(settings.NewMemory(), au)
	if err != nil {
		t.Fatal(err)
	}
	s, err := trust.New(trust.Deps{DB: w.db, Audit: au, Settings: reg, Practice: noPractice{}, Users: users{w.adminA, w.staffU, w.adminB}, Walls: noWalls{}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type noPractice struct{}

func (noPractice) Matter(context.Context, domain.ID) (domain.Matter, error) {
	return domain.Matter{}, domain.NotFound("matter")
}
func (noPractice) Contact(context.Context, domain.ID) (domain.Contact, error) {
	return domain.Contact{}, domain.NotFound("contact")
}
func (noPractice) Parties(domain.ID, domain.ID) []domain.MatterParty { return nil }

type noWalls struct{}

func (noWalls) View(context.Context, domain.Principal, domain.ID) (domain.WallView, error) {
	return domain.WallView{}, nil
}
