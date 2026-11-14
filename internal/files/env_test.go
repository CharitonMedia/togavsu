package files

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/internal/storage/storagetest"
)

// filesDB returns a fresh database with the core and files migrations, or nil when
// TOGAVSU_TEST_DATABASE_URL is unset.
func filesDB(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TOGAVSU_TEST_DATABASE_URL is required in CI")
		}
		t.Skip("TOGAVSU_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := db.OpenPostgres(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	sum := sha256.Sum256([]byte("files/" + t.Name()))
	name := "f" + hex.EncodeToString(sum[:8])
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
	plain, err := db.OpenPostgres(u.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	ms, err := db.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, plain); err != nil {
		t.Fatalf("files migrations: %v", err)
	}
	// The publication grant table and file_uploads.grant_id are a core release
	// migration (the core applies every package's migrations as one ordered set); the
	// upload paths here read them, so this test database applies that one file too.
	grants, err := os.ReadFile("../core/migrations/1252_file_publication_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := db.LoadFS(fstest.MapFS{"1252_file_publication_grants.sql": {Data: grants}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: rel}); err != nil {
		t.Fatalf("grant migration: %v", err)
	}
	_ = plain.Close()
	pool, err := db.OpenPostgres(u.String(), "core")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := audit.InstallPrivileges(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyReference(ctx, pool, "postgres"); err != nil {
		t.Fatal(err)
	}
	return pool
}

// dir is the test Directory and wall reader: matters, walls and client contacts.
type dir struct {
	mu       sync.Mutex
	matters  map[domain.ID]Matter
	walls    map[domain.ID][]domain.ID
	clients  map[domain.ID]domain.ID // matter -> client contact
	contacts map[domain.ID]domain.ID // user -> contact
	areas    map[domain.ID][]string  // practice area -> default folders
}

func (d *dir) AreaFolders(_ context.Context, area domain.ID) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.areas[area], nil
}

func (d *dir) Matter(_ context.Context, id domain.ID) (Matter, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.matters[id]
	if !ok {
		return Matter{}, repository.ErrNotFound
	}
	return m, nil
}

func (d *dir) OpenMatters(context.Context) ([]Matter, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Matter
	for _, m := range d.matters {
		if m.Status != "closed" {
			out = append(out, m)
		}
	}
	return out, nil
}

func (d *dir) UserContact(_ context.Context, user domain.ID) (domain.ID, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.contacts[user], nil
}

func (d *dir) View(_ context.Context, p domain.Principal, matter domain.ID) (domain.WallView, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := domain.WallView{}
	for _, u := range d.walls[matter] {
		v.Walled = true
		if u == p.UserID {
			v.Allows = true
		}
	}
	c, ok := d.contacts[p.UserID]
	v.ClientMatter = ok && d.clients[matter] == c
	return v, nil
}

type mails struct {
	mu   sync.Mutex
	sent []string
}

func (m *mails) MailAdmins(_ context.Context, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, subject+"\n"+body)
	return nil
}

func (m *mails) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Millisecond)
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type quiet struct{}

func (quiet) SettingChanged(context.Context, settings.Change) error { return nil }

// env is one test install: users of every role, an open matter, a walled matter, a
// closed matter, a client contact, and a local disk default backend.
type env struct {
	t        *testing.T
	pool     *sql.DB
	svc      *Service
	cfg      Config
	dir      *dir
	mail     *mails
	clock    *clock
	reg      *settings.Registry
	root     string
	versions []VersionCreated

	admin, attorney, staff, outsider, accountant, client domain.Principal
	open, walled, closed                                 domain.ID
	clientContact, otherContact                          domain.ID
	local                                                BackendView
}

func id(t *testing.T) domain.ID {
	t.Helper()
	v, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	pool := filesDB(t)
	e := &env{t: t, pool: pool, mail: &mails{}, clock: &clock{t: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}, root: t.TempDir()}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, db.Rebind(q, "postgres"), args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	at := domain.UnixMicroUTC(e.clock.t)
	sys := string(domain.SystemUserID)
	e.clientContact, e.otherContact = id(t), id(t)
	for _, c := range []domain.ID{e.clientContact, e.otherContact} {
		exec(`INSERT INTO contacts (id, created_at, updated_at, created_by, kind, given_name, middle_name, family_name, prefix, suffix, full_name,
preferred_name, also_known_as, date_of_birth, date_of_death, ssn_ein_ciphertext, entity_type, formation_state, formation_date, archived, source)
VALUES (?,?,?,?,'person','Test','','Client','','','Test Client','','','','','','','','',0,'test')`, c, at, at, sys)
	}
	user := func(roles string, contact domain.ID, rs ...domain.Role) domain.Principal {
		u := id(t)
		var c any
		if contact != "" {
			c = string(contact)
		}
		exec(`INSERT INTO users (id, created_at, updated_at, created_by, name, email, roles, active, default_hourly_rate_cents, contact_id, bar_number, bar_state)
VALUES (?,?,?,?,?,?,?,1,0,?,'','')`, u, at, at, sys, "User "+roles, string(u)+"@example.invalid", roles, c)
		return domain.Principal{UserID: u, Roles: rs, Source: domain.SourceUI}
	}
	e.admin = user(`["admin","attorney"]`, "", domain.RoleAdmin, domain.RoleAttorney)
	e.attorney = user(`["attorney"]`, "", domain.RoleAttorney)
	e.staff = user(`["staff"]`, "", domain.RoleStaff)
	e.outsider = user(`["staff"]`, "", domain.RoleStaff)
	e.accountant = user(`["accountant"]`, "", domain.RoleAccountant)
	e.client = user(`["client"]`, e.clientContact, domain.RoleClient)
	area := id(t)
	exec(`INSERT INTO practice_areas (id, created_at, updated_at, created_by, name, matter_types, stages, active) VALUES (?,?,?,?,'Family','[]','[]',1)`, area, at, at, sys)
	matter := func(number, status string) domain.ID {
		m := id(t)
		exec(`INSERT INTO matters (id, created_at, updated_at, created_by, number, name, practice_area_id, status, stage, case_number, court, client_position,
opened_on, closed_on, close_reason, statute_of_limitations, billing_type, flat_fee_cents, matter_rate_cents, responsible_attorney_id)
VALUES (?,?,?,?,?,?,?,?,'','','','','','','','','hourly',0,0,?)`, m, at, at, sys, number, "Matter "+number, area, status, e.attorney.UserID)
		return m
	}
	e.open, e.walled, e.closed = matter("2026-0001", "open"), matter("2026-0002", "open"), matter("2025-0099", "closed")
	e.dir = &dir{
		matters: map[domain.ID]Matter{
			e.open: {ID: e.open, Number: "2026-0001", Status: "open"}, e.walled: {ID: e.walled, Number: "2026-0002", Status: "open"},
			e.closed: {ID: e.closed, Number: "2025-0099", Status: "closed"},
		},
		walls:    map[domain.ID][]domain.ID{e.walled: {e.attorney.UserID, e.staff.UserID}},
		clients:  map[domain.ID]domain.ID{e.open: e.clientContact},
		contacts: map[domain.ID]domain.ID{e.client.UserID: e.clientContact},
	}
	reg, err := settings.New(settings.NewMemory(), quiet{})
	if err != nil {
		t.Fatal(err)
	}
	e.reg = reg
	ledger := audit.NewPostgres(pool)
	aud, err := audit.New(ledger, e.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	e.cfg = Config{
		Pool: pool, Audit: aud, Settings: reg, Directory: e.dir, Walls: e.dir, Mail: e.mail, Key: key,
		StagingDir: e.root + "/staging", Now: e.clock.now,
		OnVersion: func(_ context.Context, ev VersionCreated) { e.versions = append(e.versions, ev) },
	}
	e.svc, err = New(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if e.local, err = e.svc.EnsureLocalDisk(ctx, e.admin, e.root+"/local"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) set(key, value string) {
	e.t.Helper()
	if _, err := e.reg.Set(context.Background(), key, value, e.admin); err != nil {
		e.t.Fatal(err)
	}
}

// reopen builds a second service on the same database and staging, as after a restart.
func (e *env) reopen() *Service {
	e.t.Helper()
	svc, err := New(e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	return svc
}

func (e *env) s3() (*storagetest.S3, BackendView) {
	e.t.Helper()
	f := storagetest.NewS3("togavsu")
	e.t.Cleanup(f.Server.Close)
	raw, _ := json.Marshal(f.Settings())
	msg := json.RawMessage(raw)
	b, err := e.svc.CreateBackend(context.Background(), e.admin, BackendWrite{Kind: "s3", Name: "S3", Settings: &msg})
	if err != nil {
		e.t.Fatal(err)
	}
	return f, b
}

func (e *env) dropbox() (*storagetest.Dropbox, BackendView) {
	e.t.Helper()
	f := storagetest.NewDropbox()
	e.t.Cleanup(f.Server.Close)
	raw, _ := json.Marshal(f.Settings())
	msg := json.RawMessage(raw)
	b, err := e.svc.CreateBackend(context.Background(), e.admin, BackendWrite{Kind: "dropbox", Name: "Dropbox", Settings: &msg})
	if err != nil {
		e.t.Fatal(err)
	}
	return f, b
}

func (e *env) makeDefault(b BackendView) {
	e.t.Helper()
	yes := true
	if _, err := e.svc.UpdateBackend(context.Background(), e.admin, b.ID, BackendWrite{Default: &yes}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) create(p domain.Principal, nf NewFile, body string) FileView {
	e.t.Helper()
	f, err := e.svc.Create(context.Background(), p, nf, strings.NewReader(body), int64(len(body)))
	if err != nil {
		e.t.Fatalf("create %s: %v", nf.Name, err)
	}
	return f
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func isCode(err error, code string) bool {
	var api *domain.APIError
	return errors.As(err, &api) && api.Code == code
}
