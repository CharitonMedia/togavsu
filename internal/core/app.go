// Package core composes the running product: Postgres, migrations, settings, the
// append-only audit ledger, identity, practice, billing, trust, calendar, files, the
// editor, Doc Tender, the shell and admin ui, the help center, the module runtime, the
// marketplace client side, the HTTP routes and /mcp.
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
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/calendar"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/doctender"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/editor"
	"github.com/CharitonMedia/togavsu/internal/files"
	"github.com/CharitonMedia/togavsu/internal/help"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/internal/trust"
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
	// MailEvery is how often queued trust mail is sent. Zero is 30 seconds; a negative
	// value starts no delivery loop (tests call DeliverMail).
	MailEvery time.Duration
	// DataDir holds upload staging, editor run folders and the default local-disk
	// backend. Empty is DataDir (the core volume); tests give a temporary folder.
	DataDir string
	// WordcraftCLI and EditorDist locate LawCraft's builds. Empty uses the image paths;
	// a missing binary or folder leaves those features answering that they are absent.
	WordcraftCLI string
	EditorDist   string
	// NoBackground leaves the documents' workers (Doc Tender, drop folder, moves and
	// reminders) stopped, for tests that drive them step by step.
	NoBackground bool
	// ProviderURLs overrides AI provider endpoints, so tests run Doc Tender against a
	// synthetic model server on the providers' wire. Production leaves it nil.
	ProviderURLs map[string]string
	// SupervisorURL and SupervisorSecret reach the supervisor's local API, which runs
	// module containers and takes the backups an uninstall needs. Empty URL is
	// SupervisorURL (the Compose service).
	SupervisorURL    string
	SupervisorSecret string
	// BackupPassphraseFile is where the core writes the backup passphrase for the
	// supervisor. Empty is PassphraseFile when that file exists, else none.
	BackupPassphraseFile string
	// ModuleRuntime and ModuleBackups replace the supervisor for tests that run a module
	// in process. Production leaves them nil.
	ModuleRuntime modules.Runtime
	ModuleBackups modules.Backups
	// HelpFiles overrides the files service behind help images and videos (tests).
	HelpFiles help.Files
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
	Trust    *trust.Service
	Billing  *billing.Service
	// Calendar, Files, Editor and DocTender are the phase 4 services (documents.go).
	Calendar  *calendar.Service
	Files     *files.Service
	Editor    *editor.Service
	DocTender *doctender.Service
	// UI is the shell, home screen, admin page and help center (ui.go); Modules and
	// Market are the module runtime and the marketplace client side (extensions.go).
	UI      *UI
	Modules *modules.Service
	Market  *marketplace.Service

	ext      *extensions
	backups  *backups
	handler  http.Handler
	ledger   *audit.Postgres
	tools    []tool
	practice http.Handler
	// domains are the service muxes behind the core routes, first match wins: trust and
	// billing own their reports ahead of practice's mirrored ones.
	domains []*http.ServeMux
	// uiMuxes are the ui's help and dashboard routes, for MCP's mounted check.
	uiMuxes  []*http.ServeMux
	mux      *http.ServeMux
	ready    atomic.Bool
	mailMu   sync.Mutex
	stopMail context.CancelFunc
	mailDone chan struct{}
	docs     *documents
	stopDocs context.CancelFunc
	docsDone chan struct{}
}

// Migrate applies every embedded core migration to dsn in schema core.
func Migrate(ctx context.Context, dsn string) error {
	_, err := MigrateCount(ctx, dsn)
	return err
}

// MigrateCount is Migrate that reports how many migrations it applied (`core migrate`).
func MigrateCount(ctx context.Context, dsn string) (int, error) {
	plain, err := db.OpenPostgres(dsn, "")
	if err != nil {
		return 0, err
	}
	defer plain.Close()
	ms, err := migrations()
	if err != nil {
		return 0, err
	}
	return db.ApplyCount(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms})
}

// MigrateSealed is "core migrate": it applies the core migrations as the application
// role behind dsn and leaves the append-only tables owned by db.LedgerOwner. adminDSN is
// a superuser on the same server; its database name is replaced with dsn's. It hands
// the sealed tables back to the application role for the run, so a migration can still
// ALTER them, and seals them again even when a migration fails. ran reports whether a
// migration was applied. An empty adminDSN migrates only.
func MigrateSealed(ctx context.Context, dsn, adminDSN string) (ran bool, err error) {
	if adminDSN == "" {
		n, err := MigrateCount(ctx, dsn)
		return n > int(0), err
	}
	plain, err := db.OpenPostgres(dsn, "")
	if err != nil {
		return false, err
	}
	defer plain.Close()
	var app string
	if err := plain.QueryRowContext(ctx, `SELECT current_user`).Scan(&app); err != nil {
		return false, err
	}
	target, err := url.Parse(dsn)
	if err != nil {
		return false, errors.New("core: database url does not parse")
	}
	au, err := url.Parse(adminDSN)
	if err != nil {
		return false, errors.New("core: admin database url does not parse")
	}
	au.Path = target.Path
	admin, err := db.OpenPostgres(au.String(), "")
	if err != nil {
		return false, err
	}
	defer admin.Close()
	if err := db.UnsealLedgers(ctx, admin, app); err != nil {
		return false, fmt.Errorf("core: unseal ledgers: %w", err)
	}
	n, merr := MigrateCount(ctx, dsn)
	ran = n > int(0)
	if serr := db.SealLedgers(ctx, admin, app); serr != nil {
		return ran, errors.Join(merr, fmt.Errorf("core: seal ledgers: %w", serr))
	}
	return ran, merr
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
	ledger := audit.NewPostgres(pool)
	aud, err := audit.New(ledger, now)
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
		Audit: aud, Settings: reg, Key: cfg.Key, Now: now, Mail: resetMailer{mail: smtpMailer{Settings: reg}},
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
		Practice: practice.NewWithKey(store, reg, cfg.Key), Store: store, ledger: ledger,
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
	dataDir, cli, dist := cfg.DataDir, cfg.WordcraftCLI, cfg.EditorDist
	if dataDir == "" {
		dataDir = DataDir
	}
	docs, err := openFiles(ctx, pool, aud, reg, store, app.Practice.Policy.Walls, cfg.Key, dataDir, now)
	if err != nil {
		return fail(err)
	}
	app.docs = docs
	fin, err := openFinance(ctx, pool, aud, reg, store, users, app.Practice.Policy.Walls, docs.Files, app.principal, app.scopes, now)
	if err != nil {
		return fail(err)
	}
	app.Trust, app.Billing = fin.Trust, fin.Billing
	if cli == "" {
		cli = WordcraftCLI
	}
	if dist == "" {
		if _, err := os.Stat(EditorDist); err == nil {
			dist = EditorDist
		}
	}
	host, _ := os.Hostname()
	if err := openDocuments(ctx, docs, pool, aud, reg, store, app.Practice, users, cfg.Domain,
		documentsConfig{DataDir: dataDir, WordcraftCLI: cli, EditorDist: dist, ProviderURLs: cfg.ProviderURLs, Worker: "core-" + host},
		app.principal, now); err != nil {
		return fail(err)
	}
	app.Calendar, app.Files, app.Editor, app.DocTender = docs.Calendar, docs.Files, docs.Editor, docs.DocTender
	// First match wins: trust and billing own their reports ahead of practice's mirrored
	// ones, the editor's pages go before files', and practice answers the rest.
	handlers := []http.Handler{fin.trustH, fin.billH}
	handlers = append(handlers, docs.handlers...)
	for _, h := range append(handlers, app.practice) {
		mux, ok := h.(*http.ServeMux)
		if !ok {
			return fail(errors.New("core: a service handler is not a ServeMux"))
		}
		app.domains = append(app.domains, mux)
	}
	// The module runtime and the marketplace serve beside the other domains, ahead of
	// practice, which answers the rest.
	ext, err := app.openMarketplace(now)
	if err != nil {
		return fail(err)
	}
	app.ext, app.Market = ext, ext.Market
	// A module's own token writes files only while the module is installed and enabled
	// (PRD 8.19, 9.3); the files service asks the module runtime, which is opened after
	// the ui, so until then every module principal is refused.
	docs.Files.SetModuleCheck(func(ctx context.Context, module string) (bool, error) {
		if ext.Modules == nil {
			return false, nil
		}
		in, err := ext.Modules.Installed(ctx, module)
		var api *domain.APIError
		if errors.As(err, &api) && api.Code == domain.CodeNotFound {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return in.Enabled, nil
	})
	if app.tools, err = loadTools(api.MCPTools); err != nil {
		return fail(err)
	}
	app.backups = newBackups(app, cfg, dataDir, now)
	app.routes()
	files := cfg.HelpFiles
	if files == nil {
		files = FileVersions{Svc: docs.Files}
	}
	ui, err := MountUI(ctx, app, UIOptions{Now: now, Files: files, DocTender: docs.DocTender,
		Sources: map[string]dashboard.Source{"module-alerts": ext.Market.WidgetSource()}})
	if err != nil {
		return fail(err)
	}
	app.UI = ui
	if err := ext.openModules(app, cfg, ui, now); err != nil {
		return fail(err)
	}
	app.Modules = ext.Modules
	ph.Panels = app.matterPanels
	modMux, ok := ext.modH.Routes().(*http.ServeMux)
	if !ok {
		return fail(errors.New("core: the module handler is not a ServeMux"))
	}
	mkMux, ok := (&marketplace.Handler{Svc: ext.Market, Principal: app.principal}).Routes().(*http.ServeMux)
	if !ok {
		return fail(errors.New("core: the marketplace handler is not a ServeMux"))
	}
	last := len(app.domains) - 1
	app.domains = append(app.domains[:last:last], modMux, mkMux, app.domains[last])
	app.uiMuxes = []*http.ServeMux{ui.helpMux, ui.dash}
	app.handler = ui.Handler()
	// Module events are pumped and their webhooks sent every five seconds.
	if err := ext.start(ctx, !cfg.NoBackground, 5*time.Second); err != nil {
		return fail(err)
	}
	if !cfg.NoBackground {
		docCtx, stop := context.WithCancel(context.Background())
		app.stopDocs, app.docsDone = stop, make(chan struct{})
		go func() {
			defer close(app.docsDone)
			app.runDocuments(docCtx, 10*time.Second, time.Minute)
		}()
	}
	if cfg.MailEvery >= 0 {
		every := cfg.MailEvery
		if every == 0 {
			every = 30 * time.Second
		}
		mailCtx, stop := context.WithCancel(context.Background())
		app.stopMail, app.mailDone = stop, make(chan struct{})
		go func() {
			defer close(app.mailDone)
			app.deliverTrustMail(mailCtx, every)
		}()
	}
	return app, nil
}

// Close stops mail delivery and releases the pool. Cancelling the worker's context
// closes any SMTP connection it has open (internal/smtpsend), so a stalled server cannot
// hold shutdown or an update.
func (a *App) Close() error {
	if a.stopDocs != nil {
		a.stopDocs()
		<-a.docsDone
	}
	a.ext.close()
	if a.docs != nil {
		a.docs.close()
	}
	if a.stopMail != nil {
		a.stopMail()
		<-a.mailDone
	}
	return a.Pool.Close()
}

// Handler is the whole HTTP surface: the module runtime's domain router and include=
// reader around the ui, which serves its own routes and hands the rest to the core.
func (a *App) Handler() http.Handler { return a.handler }

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
		// The supervisor reads the passphrase setup stored from a file; a failure
		// here is retried before each backup and restore the core asks for.
		if err := a.backups.syncPassphrase(ctx); err != nil {
			log.Printf("core: backup passphrase file: %v", err)
		}
	}
	return ok, nil
}
