package core

// ui.go mounts the ui assignment's packages (internal/dashboard and internal/help) on the
// running core: the shell, home screen, admin page, help center and their APIs run
// against the real practice, billing, trust, calendar, Doc Tender and identity services
// on Postgres. Open mounts it; MountUI builds another one over the same core, for tests
// that need different options (help media over a fake file store).
//
// The ui authenticates exactly as the core does (identity sessions with the CSRF token
// on writes, bearer tokens limited to the contract scopes of the route); a module's
// call carrying a context token runs as the viewer.

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
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
	"github.com/CharitonMedia/togavsu/internal/frame"
	"github.com/CharitonMedia/togavsu/internal/help"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/trust"
	"github.com/CharitonMedia/togavsu/web"
	"github.com/CharitonMedia/togavsu/web/static"
	"github.com/CharitonMedia/togavsu/web/templates/admin"
	dashtpl "github.com/CharitonMedia/togavsu/web/templates/dashboard"
	"github.com/CharitonMedia/togavsu/web/templates/shell"
)

// UIOptions configures MountUI.
type UIOptions struct {
	Now func() time.Time
	// Files backs uploaded help images and videos, resolved as the viewer. The core
	// passes FileVersions{Svc: its files service}; nil refuses file-backed help items.
	Files help.Files
	// DocTender is the Doc Tender service (*doctender.Service); it backs the live count
	// on the sidebar's Documents > Doc Tender entry. nil shows the entry with no count.
	DocTender dashboard.ReviewQueue
	// Sources adds widget sources the dashboard does not compose itself: the marketplace
	// component's module-alerts widget.
	Sources map[string]dashboard.Source
}

// FileVersions adapts the files service to help.Files: Get runs as the viewer, so a
// wall, role or attorney-only rule that hides the file hides the help media too.
type FileVersions struct{ Svc *files.Service }

// CurrentVersion is the file's current version id, or the files service's error.
func (f FileVersions) CurrentVersion(ctx context.Context, p domain.Principal, file domain.ID) (domain.ID, error) {
	v, err := f.Svc.Get(ctx, p, file)
	if err != nil {
		return "", err
	}
	if v.CurrentVersionID == nil {
		return "", nil
	}
	return *v.CurrentVersionID, nil
}

// UI is the mounted ui: its services and the combined handler.
type UI struct {
	App       *App
	Dashboard *dashboard.Service
	Help      *help.Service
	Calendar  *calendar.Service
	Accounts  *dashboard.Accounts
	// Catalog is the widget catalog; the module runtime adds and removes module widgets
	// in it while the core runs.
	Catalog *dashboard.Catalog
	inner   http.Handler
	dash    *http.ServeMux
	helpMux *http.ServeMux
	cal     *http.ServeMux
	shell   frame.Shell
	scopes  *http.ServeMux
	scopeOf map[string][]string
}

// MountUI builds the ui on app. Its tables (900-999) are in the core migrations Open
// applied.
func MountUI(ctx context.Context, app *App, opt UIOptions) (*UI, error) {
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	u := &UI{App: app}
	// The calendar is the core's own Postgres-backed service. Its routes are served by
	// the core handler; calMux only tells route() which paths are the calendar's, so a
	// ui route never shadows them.
	cal := app.Calendar
	if cal == nil {
		return nil, errors.New("core: ui: core has no calendar")
	}
	u.Calendar = cal
	calTpl, err := fs.Sub(web.Calendar, "templates/calendar")
	if err != nil {
		return nil, err
	}
	ch, err := calendar.NewHandler(cal, calTpl, u.principal)
	if err != nil {
		return nil, err
	}
	calMux, ok := ch.Routes().(*http.ServeMux)
	if !ok {
		return nil, errors.New("core: ui: calendar handler is not a ServeMux")
	}
	u.cal = calMux
	cs := dashboard.CoreServices{Practice: app.Practice, Trust: app.Trust, Billing: app.Billing, Calendar: cal, DocTender: opt.DocTender, Settings: app.Settings, Now: now}
	staff := func(ctx context.Context) ([]domain.User, error) { return ListUsers(ctx, app) }
	sources := cs.Sources()
	for id, src := range opt.Sources {
		sources[id] = src
	}
	u.Catalog = dashboard.NewCatalog(sources)
	ds, err := dashboard.New(dashboard.Deps{
		Store: dashboard.PG{DB: app.Pool}, Settings: app.Settings, Users: app.Users, Staff: staff,
		Catalog: u.Catalog, Notifier: cs.Notifier(), Search: cs.Searcher(), Timers: cs,
		Mailer: trust.SMTP{Settings: app.Settings}, Badges: cs.Badges(), Now: now,
	})
	if err != nil {
		return nil, err
	}
	u.Dashboard = ds
	hs, err := help.New(help.Deps{Store: help.PG{DB: app.Pool}, Settings: app.Settings, Files: opt.Files, Version: Version, Now: now})
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
	writer, ok := app.Users.(dashboard.UserWriter)
	if !ok {
		return nil, errors.New("core: ui: user repository cannot update")
	}
	dh.Accounts = &dashboard.Accounts{
		Identity: app.Identity, Users: writer, List: func(ctx context.Context) ([]domain.User, error) { return ListUsers(ctx, app) },
		Audit: app.Audit, WallSvc: app.Practice, Profiles: dashboard.PG{DB: app.Pool}, Now: now,
	}
	u.Accounts = dh.Accounts
	if app.ext != nil {
		dh.Modules = modulesAdmin{x: app.ext}
		dh.AICalls = func(r *http.Request, p domain.Principal) (any, error) {
			if app.ext.Modules == nil {
				return nil, nil
			}
			return aiCallPanel(r, p, app.ext.Modules, app.Settings)
		}
	}
	ledger := audit.NewPostgres(app.Pool)
	dh.Audit = func(r *http.Request, p domain.Principal, limit int) ([]dashboard.AuditRow, error) {
		return AuditRows(r.Context(), p, ledger, app, limit)
	}
	u.dash = dh.Routes()
	u.shell = dh.Frame
	u.helpMux = (&help.Handler{Svc: hs, Principal: u.principal}).Routes()
	if err := u.loadScopes(); err != nil {
		return nil, err
	}
	u.inner = u.route(app.setupGate(app.mux))
	return u, nil
}

// Handler is the whole site: the module runtime's domain router and include= reader,
// then calendar routes to the core, then ui routes, then the core. Every request carries
// the shell, so each area's screens (and a module's tab) draw inside it.
func (u *UI) Handler() http.Handler {
	site := u.App.extend(u.inner)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site.ServeHTTP(w, frame.With(r, u.shell))
	})
}

// uiCoreWidgets are the widget routes a domain service already serves; the core keeps them.
var uiCoreWidgets = map[string]bool{
	"/api/v1/widgets/my-open-matters": true, "/api/v1/widgets/pending-conflict-checks": true,
	"/api/v1/widgets/trust-alerts": true, "/api/v1/widgets/replenishment-requests": true,
	"/api/v1/widgets/running-timer": true,
}

// uiOpenPath paths need no sign-in and no finished setup.
func uiOpenPath(p string) bool {
	return p == "/llms.txt" || p == "/api/v1/llms.txt" || strings.HasPrefix(p, "/ui/static/") || p == "/ui/appearance.css"
}

func (u *UI) route(coreH http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !uiOpenPath(r.URL.Path) {
			ok, err := u.App.Ready(r.Context())
			if err != nil || !ok {
				coreH.ServeHTTP(w, r) // the core sends the browser to /setup
				return
			}
		}
		r = withUIMemo(r)
		if strings.HasPrefix(r.URL.Path, "/api/v1/widgets/") {
			if uiCoreWidgets[r.URL.Path] {
				coreH.ServeHTTP(w, r)
				return
			}
			if _, pat := u.cal.Handler(r); pat != "" {
				coreH.ServeHTTP(w, r)
				return
			}
		}
		if _, pat := u.cal.Handler(r); pat != "" && pat != "/" {
			coreH.ServeHTTP(w, r) // the core's calendar on Postgres
			return
		}
		for _, m := range []*http.ServeMux{u.helpMux, u.dash} {
			if _, pat := m.Handler(r); pat != "" && pat != "/" {
				m.ServeHTTP(w, r)
				return
			}
		}
		coreH.ServeHTTP(w, r)
	})
}

type uiMemoKey struct{}

type uiMemo struct {
	once sync.Once
	p    domain.Principal
	err  error
}

func withUIMemo(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), uiMemoKey{}, &uiMemo{}))
}

// principal authenticates once per request, as the core does.
func (u *UI) principal(r *http.Request) (domain.Principal, error) {
	m, ok := r.Context().Value(uiMemoKey{}).(*uiMemo)
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
		ct := r.Header.Get("Content-Type")
		// The multipart forms, the profile picture and the Modules tab's package uploads,
		// are read here only on their own routes, whose handlers capped the body with
		// http.MaxBytesReader first. Other multipart writes are not parsed before sign-in.
		multipart := strings.HasPrefix(ct, "multipart/form-data") &&
			(r.URL.Path == dashboard.PicturePath || strings.HasPrefix(r.URL.Path, dashboard.ModulesActionPrefix))
		if in.CSRF == "" && (strings.HasPrefix(ct, "application/x-www-form-urlencoded") || multipart) {
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
	return u.App.delegate(r, c.Principal)
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

// ListUsers lists every user by name, as the admin page does.
func ListUsers(ctx context.Context, app *App) ([]domain.User, error) {
	type lister interface {
		List(context.Context, repository.Filter) (repository.Page[domain.User], error)
	}
	l, ok := app.Users.(lister)
	if !ok {
		return nil, errors.New("core: ui: user repository cannot list")
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

// AuditRows is the System tab's recent audit log as the requesting admin may read it.
// The rows pass through practice's audit scope exactly as GET /api/v1/audit-log does:
// a row about a matter behind a wall p is not on, or about a contact only on such
// matters, is dropped, and attorney-only values are redacted for p. The limit applies
// after that filter, so hidden rows never take a visible row's place.
func AuditRows(ctx context.Context, p domain.Principal, ledger *audit.Postgres, app *App, limit int) ([]dashboard.AuditRow, error) {
	// ponytail: ceiling: reads the whole ledger page per System tab view, like core's unfiltered audit-log.
	// Upgrade path: newest-first cursor reads in audit.Postgres. Trigger: an install passes 100k audit rows.
	page, err := ledger.List(ctx, repository.Filter{})
	if err != nil {
		return nil, err
	}
	rows := page.Items
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	shown := app.Practice.AuditLog(ctx, p, rows)
	if len(shown) > limit {
		shown = shown[:limit]
	}
	names := map[domain.ID]string{}
	out := make([]dashboard.AuditRow, 0, len(shown))
	for _, e := range shown {
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
		out = append(out, dashboard.AuditRow{At: e.At, Actor: name, Role: e.Role, Source: e.Source, Entity: e.Entity, Field: e.Field, Old: e.Old, New: e.New, Redacted: e.Redacted})
	}
	return out, nil
}
