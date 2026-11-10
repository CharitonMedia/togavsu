// Package core composes the running product: Postgres, migrations, settings, the
// append-only audit ledger, identity, practice, the HTTP routes and /mcp.
// Access control stays in the services; this package only authenticates the caller
// and hands the principal to them.
package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/web"
)

// Version is reported by /api/v1/health and MCP initialize.
var Version = "dev"

// Config is what bootstrap supplies. Nothing here is a tunable; tunables are settings.
type Config struct {
	DatabaseURL string
	Key         []byte
	Domain      string
	Now         func() time.Time
}

// App is one running core.
type App struct {
	Pool     *sql.DB
	Audit    *audit.Service
	Settings *settings.Registry
	Identity *identity.Service
	Users    interface {
		Get(context.Context, domain.ID) (domain.User, error)
	}
	Practice *practice.Service
	Store    *practice.Durable

	tools    []tool
	practice http.Handler
	mux      *http.ServeMux
	ready    atomic.Bool
}

// Migrate applies every embedded core migration to dsn in schema core.
func Migrate(ctx context.Context, dsn string) error {
	plain, err := db.OpenPostgres(dsn, "")
	if err != nil {
		return err
	}
	defer plain.Close()
	ms, err := db.Embedded()
	if err != nil {
		return err
	}
	return db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms})
}

// Open migrates, seeds reference rows, and wires every service on one pool.
func Open(ctx context.Context, cfg Config) (*App, error) {
	if len(cfg.Key) != 32 {
		return nil, errors.New("core: encryption key must be 32 bytes")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if err := Migrate(ctx, cfg.DatabaseURL); err != nil {
		return nil, fmt.Errorf("core: migrate: %w", err)
	}
	pool, err := db.OpenPostgres(cfg.DatabaseURL, "core")
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*App, error) {
		_ = pool.Close()
		return nil, err
	}
	if err := pool.PingContext(ctx); err != nil {
		return fail(fmt.Errorf("core: database: %w", err))
	}
	if err := audit.InstallPrivileges(ctx, pool); err != nil {
		return fail(fmt.Errorf("core: audit privileges: %w", err))
	}
	if err := db.ApplyReference(ctx, pool, "postgres"); err != nil {
		return fail(fmt.Errorf("core: reference seed: %w", err))
	}
	aud, err := audit.New(audit.NewPostgres(pool), now)
	if err != nil {
		return fail(err)
	}
	reg, err := settings.NewWithClock(db.SettingsStore{DB: pool, Dialect: "postgres"}, aud, now)
	if err != nil {
		return fail(err)
	}
	pg := identity.NewPG(pool)
	ids, err := identity.New(identity.Deps{
		Users: pg.Users(), Sessions: pg.Sessions(), Passwords: pg.Passwords(),
		TOTP: pg.TOTP(), Recovery: pg.Recovery(), Tokens: pg.Tokens(),
		Audit: aud, Settings: reg, Key: cfg.Key, Now: now, Mail: logMailer{},
	})
	if err != nil {
		return fail(err)
	}
	users := pg.Users()
	store, err := practice.OpenDurable(ctx, pool, users.Get)
	if err != nil {
		return fail(fmt.Errorf("core: practice load: %w", err))
	}
	app := &App{
		Pool: pool, Audit: aud, Settings: reg, Identity: ids, Users: users,
		Practice: practice.NewWithKey(store, reg, cfg.Key), Store: store,
	}
	sub, err := fs.Sub(web.Practice, "templates/practice")
	if err != nil {
		return fail(err)
	}
	ph, err := practice.NewHandlerFS(app.Practice, sub, app.principal)
	if err != nil {
		return fail(err)
	}
	app.practice = ph.Routes()
	if app.tools, err = loadTools(api.MCPTools); err != nil {
		return fail(err)
	}
	app.routes()
	return app, nil
}

// Close releases the pool.
func (a *App) Close() error { return a.Pool.Close() }

// Handler is the whole HTTP surface.
func (a *App) Handler() http.Handler { return a.setupGate(a.mux) }

// Ready reports first-run completion. Once true it stays true for this process.
func (a *App) Ready(ctx context.Context) (bool, error) {
	if a.ready.Load() {
		return true, nil
	}
	ok, err := a.Identity.Ready(ctx)
	if err != nil {
		return false, err
	}
	if ok {
		a.ready.Store(true)
	}
	return ok, nil
}

// logMailer records that a reset was requested. Outbound mail is a later package.
// ponytail: ceiling: password-reset tokens are not delivered; the admin resets the user instead.
// Upgrade path: the notifications package sends the reset mail. Trigger: SMTP settings ship.
type logMailer struct{}

func (logMailer) SendPasswordReset(email, _ string) error {
	log.Printf("core: password reset requested; mail delivery is not configured")
	return nil
}
