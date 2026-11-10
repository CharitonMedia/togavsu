package trust

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// appDB returns a migrated database owned by a fresh non-superuser login role, connected
// as that role, so privilege checks apply as they do for togavsu_app in Compose. The
// tests need TOGAVSU_TEST_DATABASE_URL (a superuser on a disposable Postgres 17) and are
// skipped without it; scripts/ci/test-core.sh fails a skipped database test.
func appDB(t *testing.T) (app *sql.DB, super *sql.DB) {
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
	sum := sha256.Sum256([]byte("trust/" + t.Name()))
	tag := hex.EncodeToString(sum[:8])
	name, role := "tr"+tag, "trapp"+tag
	const password = "synthetic-trust-app-password"
	for _, stmt := range []string{
		`DROP DATABASE IF EXISTS ` + name,
		`DROP ROLE IF EXISTS ` + role,
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
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	superURL := u.String()
	u.User = url.UserPassword(role, password)
	plain, err := db.OpenPostgres(u.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	ms, err := db.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	mine, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: append(ms, mine...)}); err != nil {
		t.Fatal(err)
	}
	_ = plain.Close()
	super, err = db.OpenPostgres(superURL, "core")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = super.Close() })
	// The audit writer role is cluster-wide; a superuser creates it once, as the
	// installer's dbinit does, and lets the application role use it.
	if err := audit.InstallPrivileges(ctx, super); err != nil {
		t.Fatal(err)
	}
	if _, err := super.ExecContext(ctx, `GRANT togavsu_audit_writer TO `+role+` WITH ADMIN OPTION`); err != nil {
		t.Fatal(err)
	}
	app, err = db.OpenPostgres(u.String(), "core")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	if err := audit.InstallPrivileges(ctx, app); err != nil {
		t.Fatal(err)
	}
	if err := InstallPrivileges(ctx, app); err != nil {
		t.Fatal(err)
	}
	return app, super
}

// The running core's stores satisfy the trust interfaces.
var (
	_ Practice = (*practice.Durable)(nil)
	_ Matters  = (*practice.Durable)(nil)
	_ Users    = (&identity.PG{}).Users()
)

// memWalls reads matter walls from the practice memory store.
type memWalls struct{ m *practice.Memory }

func (w memWalls) View(_ context.Context, p domain.Principal, matter domain.ID) (domain.WallView, error) {
	v := domain.WallView{}
	for _, row := range w.m.Walls(matter) {
		v.Walled = true
		if row.UserID == p.UserID {
			v.Allows = true
		}
	}
	return v, nil
}

type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) Publish(_ context.Context, ev []Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev...)
	return nil
}

func (r *recorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, e.Name)
	}
	return out
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
}

type env struct {
	t        *testing.T
	ctx      context.Context
	svc      *Service
	db       *sql.DB
	super    *sql.DB
	practice *practice.Memory
	reg      *settings.Registry
	events   *recorder
	users    interface {
		Get(context.Context, domain.ID) (domain.User, error)
		Update(context.Context, domain.User) error
	}
	sunkMu sync.Mutex
	sunk   []domain.TrustBalance

	admin, attorney, staff, accountant, client, other domain.Principal
	account                                           domain.ID
	matter                                            domain.Matter
	clientID                                          domain.ID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	app, super := appDB(t)
	aud, err := audit.New(audit.NewPostgres(app), nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(settings.NewMemory(), aud)
	if err != nil {
		t.Fatal(err)
	}
	ids := identity.NewMemory()
	mem := practice.NewMemory()
	e := &env{t: t, ctx: ctx, db: app, super: super, practice: mem, reg: reg, events: &recorder{}, users: ids.Users()}
	user := func(name, email string, roles ...domain.Role) domain.Principal {
		id := mustID(t)
		js, err := domain.MarshalRoles(roles)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if err := ids.Users().Insert(ctx, domain.User{Meta: domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: domain.SystemUserID},
			Name: name, Email: email, Roles: js, Active: true}); err != nil {
			t.Fatal(err)
		}
		return domain.Principal{UserID: id, Roles: roles, Source: domain.SourceUI}
	}
	e.admin = user("Ada Admin", "ada.admin@example.test", domain.RoleAdmin)
	e.attorney = user("Avery Attorney", "avery.attorney@example.test", domain.RoleAttorney)
	e.other = user("Oren Otherattorney", "oren@example.test", domain.RoleAttorney)
	e.staff = user("Sam Staff", "sam.staff@example.test", domain.RoleStaff)
	e.accountant = user("Cal Accountant", "cal.accountant@example.test", domain.RoleAccountant)
	e.client = user("Cli Ent", "client@example.test", domain.RoleClient)
	svc, err := New(Deps{DB: app, Audit: aud, Settings: reg, Practice: mem, Users: ids.Users(), Walls: memWalls{mem}, Events: e.events,
		BalanceSink: func(_ context.Context, row domain.TrustBalance) error {
			e.sunkMu.Lock()
			defer e.sunkMu.Unlock()
			e.sunk = append(e.sunk, row)
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	acct, err := svc.CreateAccount(ctx, e.admin, AccountInput{Bank: "Synthetic Bank", Name: "IOLTA", IOLTA: true, LastFour: "4242"}, "")
	if err != nil {
		t.Fatal(err)
	}
	e.account = acct.ID
	e.clientID = e.contact("Casey Client")
	e.matter = e.newMatter("2026-0001", "Client v. Synthetic", "hourly", 0, e.clientID)
	e.events.reset()
	return e
}

func mustID(t *testing.T) domain.ID {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) contact(name string) domain.ID {
	id := mustID(e.t)
	now := time.Now().UTC()
	if err := e.practice.InsertContact(e.ctx, domain.Contact{Meta: domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: domain.SystemUserID}, Kind: "person", FullName: name}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) newMatter(number, name, billing string, fee domain.Cents, client domain.ID) domain.Matter {
	now := time.Now().UTC()
	m := domain.Matter{Meta: domain.Meta{ID: mustID(e.t), CreatedAt: now, UpdatedAt: now, CreatedBy: domain.SystemUserID},
		Number: number, Name: name, Status: "open", BillingType: billing, FlatFeeCents: fee, ResponsibleAttorneyID: e.attorney.UserID}
	m, err := e.practice.InsertMatter(e.ctx, m, func([]string) (string, error) { return number, nil })
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.practice.InsertParty(e.ctx, domain.MatterParty{Meta: domain.Meta{ID: mustID(e.t), CreatedAt: now, UpdatedAt: now, CreatedBy: domain.SystemUserID},
		MatterID: m.ID, ContactID: client, Role: "client", Primary: true}); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) in(typ string, cents int64) PostInput {
	return PostInput{TrustAccountID: e.account, ClientID: e.clientID, MatterID: e.matter.ID, EntryType: typ, AmountCents: cents, Method: "check", Description: "synthetic " + typ}
}

func (e *env) post(who domain.Principal, in PostInput) PostResult {
	e.t.Helper()
	r, err := e.svc.Post(e.ctx, who, in, "")
	if err != nil {
		e.t.Fatalf("post %s %d: %v", in.EntryType, in.AmountCents, err)
	}
	return r
}

func (e *env) count(table string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func code(err error) string {
	if api, ok := err.(*domain.APIError); ok {
		return api.Code
	}
	return ""
}
