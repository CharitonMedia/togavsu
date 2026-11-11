// Package compose mounts the ui assignment's packages (internal/dashboard and
// internal/help) and the calendar on a running core.App, so the shell, home screen,
// admin page, help center and their APIs run against the real practice, billing, trust
// and identity services on Postgres.
//
// It is the wiring the integration lead folds into internal/core (integrate-extensions):
// core owns router composition, so this package does it from outside without editing
// core. It authenticates exactly as core does (identity sessions with the CSRF token on
// writes, bearer tokens limited to the contract scopes of the route).
//
// ponytail: ceiling: the calendar here runs on calendar.Memory, as the calendar package
// itself ships (its Postgres store is pending), so calendar rows do not survive a
// restart; the dashboard and help rows are on Postgres. Upgrade path: integrate-documents
// mounts calendar on the shared pool and core.App exposes it; Mount then takes it from
// the App. Trigger: integrate-documents merges.
package compose

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/calendar"
	"github.com/CharitonMedia/togavsu/internal/core"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/help"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/trust"
	"github.com/CharitonMedia/togavsu/web/static"
	"github.com/CharitonMedia/togavsu/web/templates/admin"
	dashtpl "github.com/CharitonMedia/togavsu/web/templates/dashboard"
	"github.com/CharitonMedia/togavsu/web/templates/shell"
)

// Options configures Mount.
type Options struct {
	// DatabaseURL is the core's DSN; the ui migrations run on it before mounting.
	DatabaseURL string
	// CalendarTemplates holds web/templates/calendar; nil serves the calendar API only.
	CalendarTemplates fs.FS
	Now               func() time.Time
}

// UI is the mounted ui: its services and the combined handler.
type UI struct {
	App       *core.App
	Dashboard *dashboard.Service
	Help      *help.Service
	Calendar  *calendar.Service
	handler   http.Handler
	dash      *http.ServeMux
	helpMux   *http.ServeMux
	cal       *http.ServeMux
	scopes    *http.ServeMux
	scopeOf   map[string][]string
}

// Migrations is the ui range (900-999): dashboard then help.
func Migrations() ([]db.Migration, error) {
	a, err := dashboard.Migrations()
	if err != nil {
		return nil, err
	}
	b, err := help.Migrations()
	if err != nil {
		return nil, err
	}
	ms := append(a, b...)
	if err := db.ValidateMigrationVersions(ms); err != nil {
		return nil, err
	}
	return ms, nil
}

// Migrate applies the ui migrations to dsn in schema core under the core owner.
func Migrate(ctx context.Context, dsn string) error {
	plain, err := db.OpenPostgres(dsn, "")
	if err != nil {
		return err
	}
	defer plain.Close()
	ms, err := Migrations()
	if err != nil {
		return err
	}
	return db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms})
}

// Mount migrates the ui tables and builds the ui on app.
func Mount(ctx context.Context, app *core.App, opt Options) (*UI, error) {
	if err := Migrate(ctx, opt.DatabaseURL); err != nil {
		return nil, err
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	u := &UI{App: app}
	cal, err := calendar.New(calendar.Deps{
		Store: calendar.NewMemory(), Policy: app.Practice.Policy, Matters: calendar.PracticeMatters{Svc: app.Practice},
		Users: calendar.UsersFunc(app.Users.Get), Settings: app.Settings, Audit: app.Audit, Mailer: trust.SMTP{Settings: app.Settings}, Now: now,
	})
	if err != nil {
		return nil, err
	}
	u.Calendar = cal
	ch, err := calendar.NewHandler(cal, opt.CalendarTemplates, u.principal)
	if err != nil {
		return nil, err
	}
	calMux, ok := ch.Routes().(*http.ServeMux)
	if !ok {
		return nil, errors.New("compose: calendar handler is not a ServeMux")
	}
	u.cal = calMux
	cs := dashboard.CoreServices{Practice: app.Practice, Trust: app.Trust, Billing: app.Billing, Calendar: cal, Settings: app.Settings, Now: now}
	staff := func(ctx context.Context) ([]domain.User, error) { return listUsers(ctx, app) }
	ds, err := dashboard.New(dashboard.Deps{
		Store: dashboard.PG{DB: app.Pool}, Settings: app.Settings, Users: app.Users, Staff: staff,
		Catalog: dashboard.NewCatalog(cs.Sources()), Notifier: cs.Notifier(), Search: cs.Searcher(), Timers: cs,
		Mailer: trust.SMTP{Settings: app.Settings}, Badges: cs.Badges(), Now: now,
	})
	if err != nil {
		return nil, err
	}
	u.Dashboard = ds
	hs, err := help.New(help.Deps{Store: help.PG{DB: app.Pool}, Settings: app.Settings, Version: core.Version, Now: now})
	if err != nil {
		return nil, err
	}
	u.Help = hs
	staticFS, err := fs.Sub(static.FS, ".")
	if err != nil {
		return nil, err
	}
	dh, err := dashboard.NewHandler(ds, hs, u.principal, csrfOf, staticFS, shell.FS, dashtpl.FS, admin.FS)
	if err != nil {
		return nil, err
	}
	dh.Users = func(r *http.Request) ([]domain.User, error) { return listUsers(r.Context(), app) }
	ledger := audit.NewPostgres(app.Pool)
	dh.Audit = func(r *http.Request, limit int) ([]dashboard.AuditRow, error) {
		return auditRows(r.Context(), ledger, app, limit)
	}
	u.dash = dh.Routes()
	u.helpMux = (&help.Handler{Svc: hs, Principal: u.principal}).Routes()
	if err := u.loadScopes(); err != nil {
		return nil, err
	}
	u.handler = u.route(app.Handler())
	return u, nil
}

// Handler is the whole site: ui routes first, then calendar, then the core.
func (u *UI) Handler() http.Handler { return u.handler }

// coreWidgets are the widget routes a domain service already serves; the core keeps them.
var coreWidgets = map[string]bool{
	"/api/v1/widgets/my-open-matters": true, "/api/v1/widgets/pending-conflict-checks": true,
	"/api/v1/widgets/trust-alerts": true, "/api/v1/widgets/replenishment-requests": true,
	"/api/v1/widgets/running-timer": true,
}

// openPaths need no sign-in and no finished setup.
func openPath(p string) bool {
	return p == "/llms.txt" || p == "/api/v1/llms.txt" || strings.HasPrefix(p, "/ui/static/") || p == "/ui/appearance.css"
}

func (u *UI) route(coreH http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !openPath(r.URL.Path) {
			ok, err := u.App.Ready(r.Context())
			if err != nil || !ok {
				coreH.ServeHTTP(w, r) // the core sends the browser to /setup
				return
			}
		}
		r = withMemo(r)
		if strings.HasPrefix(r.URL.Path, "/api/v1/widgets/") {
			if coreWidgets[r.URL.Path] {
				coreH.ServeHTTP(w, r)
				return
			}
			if _, pat := u.cal.Handler(r); pat != "" {
				u.cal.ServeHTTP(w, r)
				return
			}
		}
		for _, m := range []*http.ServeMux{u.helpMux, u.dash, u.cal} {
			if _, pat := m.Handler(r); pat != "" && pat != "/" {
				m.ServeHTTP(w, r)
				return
			}
		}
		coreH.ServeHTTP(w, r)
	})
}

func csrfOf(r *http.Request) string {
	if c, err := r.Cookie(practice.CSRFCookie); err == nil {
		return c.Value
	}
	return ""
}

type memoKey struct{}

type memo struct {
	once sync.Once
	p    domain.Principal
	err  error
}

func withMemo(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), memoKey{}, &memo{}))
}

// principal authenticates once per request, as the core does.
func (u *UI) principal(r *http.Request) (domain.Principal, error) {
	m, ok := r.Context().Value(memoKey{}).(*memo)
	if !ok {
		return u.authenticate(r)
	}
	m.once.Do(func() { m.p, m.err = u.authenticate(r) })
	return m.p, m.err
}

func (u *UI) authenticate(r *http.Request) (domain.Principal, error) {
	in := identity.Presented{Write: r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions}
	if c, err := r.Cookie(identity.SessionCookie("").Name); err == nil {
		in.SessionToken = c.Value
	}
	if h := r.Header.Get("Authorization"); h != "" {
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			return domain.Principal{}, domain.Unauthenticated("authorization must be a bearer token")
		}
		in.Bearer = strings.TrimSpace(token)
	}
	if in.Write && in.SessionToken != "" {
		in.CSRF = r.Header.Get("X-CSRF-Token")
		if in.CSRF == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			in.CSRF = r.FormValue("csrf")
		}
	}
	c, err := u.App.Identity.Authenticate(r.Context(), in)
	if err != nil {
		return domain.Principal{}, shared(err)
	}
	if in.Bearer != "" {
		scopes, ok := u.routeScopes(r)
		if !ok {
			return domain.Principal{}, domain.Forbidden("this route is not available to tokens")
		}
		if err := c.RequireScopes(scopes...); err != nil {
			return domain.Principal{}, shared(err)
		}
	}
	return c.Principal, nil
}

func shared(err error) error {
	var api *domain.APIError
	if !errors.As(err, &api) {
		return err
	}
	switch api.Code {
	case "csrf_required", "scope_denied":
		return domain.Forbidden(api.Message)
	case "ambiguous_auth":
		return domain.Invalid("authorization", api.Message)
	case "locked_out":
		return &domain.APIError{Code: domain.CodeRateLimited, Message: api.Message}
	}
	return err
}

// loadScopes reads each contract route's scopes from the published OpenAPI document.
func (u *UI) loadScopes() error {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.OpenAPI, &doc); err != nil {
		return err
	}
	u.scopes = http.NewServeMux()
	u.scopeOf = map[string][]string{}
	for path, ops := range doc.Paths {
		for method, raw := range ops {
			if method == "parameters" {
				continue
			}
			var op struct {
				Access struct {
					Scopes []string `json:"scopes"`
				} `json:"x-togavsu-access"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				return err
			}
			pattern := strings.ToUpper(method) + " " + path
			func() {
				defer func() { _ = recover() }() // a pattern ServeMux cannot hold is not token-callable
				u.scopes.Handle(pattern, http.NotFoundHandler())
				u.scopeOf[pattern] = op.Access.Scopes
			}()
		}
	}
	return nil
}

func (u *UI) routeScopes(r *http.Request) ([]string, bool) {
	_, pat := u.scopes.Handler(r)
	s, ok := u.scopeOf[pat]
	return s, ok && pat != ""
}

func listUsers(ctx context.Context, app *core.App) ([]domain.User, error) {
	type lister interface {
		List(context.Context, repository.Filter) (repository.Page[domain.User], error)
	}
	l, ok := app.Users.(lister)
	if !ok {
		return nil, errors.New("compose: user repository cannot list")
	}
	var out []domain.User
	cursor := ""
	for {
		page, err := l.List(ctx, repository.Filter{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func auditRows(ctx context.Context, ledger *audit.Postgres, app *core.App, limit int) ([]dashboard.AuditRow, error) {
	page, err := ledger.List(ctx, repository.Filter{})
	if err != nil {
		return nil, err
	}
	rows := page.Items
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	names := map[domain.ID]string{}
	var out []dashboard.AuditRow
	for _, e := range rows {
		name, ok := names[e.ActorID]
		if !ok {
			name = "System"
			if !e.ActorID.IsZero() {
				if usr, err := app.Users.Get(ctx, e.ActorID); err == nil {
					name = usr.Name
				}
			}
			names[e.ActorID] = name
		}
		out = append(out, dashboard.AuditRow{At: e.CreatedAt, Actor: name, Role: e.Role, Source: e.Source, Entity: e.Entity, Field: e.Field, Old: e.OldValue, New: e.NewValue})
	}
	return out, nil
}
