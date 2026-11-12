package core

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// SupervisorURL is the supervisor's local API on the Compose network (compose.yaml
// service supervisor, SUPERVISOR_LISTEN :8081). The core reaches it with the bootstrap
// supervisor secret; it never touches the Docker socket (PRD 6).
const SupervisorURL = "http://supervisor:8081"

// ModuleURL is a module container's in-stack address from its id and manifest port: the
// supervisor names each module container togavsu-mod-{id} on the stack network.
const ModuleURL = "http://togavsu-mod-%s:%d"

// extensions is the module runtime and the marketplace client side of the running core
// (PRD 9 and 10): the trust root, signed installs, permissions, module schemas, include=
// reads and views, domain replacement, framed pages, events, AI calls and alerts.
type extensions struct {
	Modules *modules.Service
	Market  *marketplace.Service
	modH    *modules.Handler
	stop    context.CancelFunc
	done    chan struct{}
}

// openMarketplace wires the marketplace component on the core pool: the firm's trust
// root (trusted local keys and keys published through the marketplace), browsing,
// verified downloads and the flag and pull alerts for installed modules.
func (a *App) openMarketplace(now func() time.Time) (*extensions, error) {
	users, ok := a.Users.(marketplace.Users)
	if !ok {
		return nil, errors.New("core: the user store cannot list users")
	}
	x := &extensions{}
	// The marketplace asks the module runtime which modules are installed; the runtime
	// is built next, so the lister reads x.Modules when a check runs.
	installed := installedFunc(func(ctx context.Context) ([]marketplace.InstalledModule, error) {
		if x.Modules == nil {
			return nil, nil
		}
		return x.Modules.InstalledModules(ctx)
	})
	market, err := marketplace.New(marketplace.Deps{DB: a.Pool, Audit: a.Audit, Settings: a.Settings, Users: users, Installed: installed, Now: now,
		Logf: log.Printf})
	if err != nil {
		return nil, err
	}
	x.Market = market
	return x, nil
}

// openModules wires the module runtime on the core pool, with the ui's help center,
// widget catalog and sidebar as the places a module shows, the marketplace component as
// its signature check, and the supervisor as its container runtime and backups.
func (x *extensions) openModules(a *App, cfg Config, ui *UI, now func() time.Time) error {
	rt, backups := cfg.ModuleRuntime, cfg.ModuleBackups
	if rt == nil || backups == nil {
		sup := &modules.Supervisor{URL: cfg.SupervisorURL, Secret: cfg.SupervisorSecret, ModuleURL: ModuleURL, Ports: x.port}
		if sup.URL == "" {
			sup.URL = SupervisorURL
		}
		if rt == nil {
			rt = sup
		}
		if backups == nil {
			backups = sup
		}
	}
	svc, err := modules.New(modules.Deps{DB: a.Pool, Key: cfg.Key, Audit: a.Audit, Settings: a.Settings, Walls: a.Practice.Policy.Walls,
		Contacts: a.Practice, Signing: x.Market, Users: a.Users, Tokens: modules.IdentityTokens{Svc: a.Identity}, Help: ui.Help,
		Widgets: ui.Catalog, Nav: ui.Dashboard, Runtime: rt, Backups: backups, Now: now})
	if err != nil {
		return err
	}
	svc.AIBaseURLs = cfg.ProviderURLs
	x.Modules = svc
	x.modH = modules.NewHandler(svc, a.moduleCaller)
	return nil
}

type installedFunc func(ctx context.Context) ([]marketplace.InstalledModule, error)

func (f installedFunc) InstalledModules(ctx context.Context) ([]marketplace.InstalledModule, error) {
	return f(ctx)
}

// port is a module's container port from its installed manifest.
func (x *extensions) port(module string) (port int) {
	if x.Modules == nil {
		return port
	}
	if in, err := x.Modules.Installed(context.Background(), module); err == nil {
		port = in.Manifest.Container.Port
	}
	return port
}

// start re-registers enabled modules' widgets and sidebar entries and, with background,
// starts the event pump and webhook delivery every interval and the marketplace alert
// check.
func (x *extensions) start(ctx context.Context, background bool, every time.Duration) error {
	if err := x.Modules.Start(ctx); err != nil {
		return err
	}
	if !background {
		return nil
	}
	bg, stop := context.WithCancel(context.Background())
	x.stop, x.done = stop, make(chan struct{})
	x.Market.Start(bg, log.Printf)
	go func() {
		defer close(x.done)
		x.Modules.Run(bg, every, log.Printf)
	}()
	return nil
}

func (x *extensions) close() {
	if x == nil || x.stop == nil {
		return
	}
	x.stop()
	<-x.done
}

// moduleCaller is the module routes' caller: the same memoized authentication as every
// core route (session with CSRF on writes, or a bearer token held to the route's
// contract scopes). The module handler applies a context token itself.
func (a *App) moduleCaller(r *http.Request) (modules.Caller, error) {
	c, err := a.caller(r)
	if err != nil {
		return modules.Caller{}, err
	}
	return modules.Caller{Principal: c.Principal, Scopes: c.Scopes, Bearer: bearerOf(r) != ""}, nil
}

func bearerOf(r *http.Request) string {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(token)
}

// delegate applies a module's context token: a module calling with its own token and the
// signed context of the user looking at its frame runs as that user, so walls, roles and
// attorney-only redaction are the viewer's (PRD 9.4). Without the header p is unchanged.
func (a *App) delegate(r *http.Request, p domain.Principal) (domain.Principal, error) {
	tok := r.Header.Get(modules.ContextHeader)
	if tok == "" || a.ext == nil {
		return p, nil
	}
	return a.ext.Modules.Delegate(r.Context(), p, tok)
}

// extend wraps h in the module runtime's domain-replacement router and include= reader,
// so a module switched in for calendar, time or trust takes those screens and APIs and
// an include= read adds module rows the caller may see. A replaced domain's request
// clears the core's own gates before it reaches the module (gateReplaced).
func (a *App) extend(h http.Handler) http.Handler {
	if a.ext == nil {
		return h
	}
	routed := a.ext.Modules.Router(a.principal, a.ext.modH.Include(h))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d := modules.DomainOf(r.URL.Path); d != "" {
			if module, err := a.ext.Modules.Replacement(r.Context(), d); err == nil && module != "" {
				r = memoCaller(r)
				if !a.gateReplaced(w, r) {
					return
				}
			}
		}
		routed.ServeHTTP(w, r)
	})
}

// gateReplaced holds a request for a module-replaced domain to the gates every core route
// has: first-run setup, sign-in (a signed-out browser screen goes to /login, an API call
// gets 401), CSRF on cookie writes and a bearer token's contract scopes. It writes the
// core's answer and reports false when the request may not go on. The iCal feed keeps
// its own credential, the secret in its path, as the core's feed route does.
func (a *App) gateReplaced(w http.ResponseWriter, r *http.Request) bool {
	ok, err := a.Ready(r.Context())
	if err != nil {
		writeErr(w, err)
		return false
	}
	api := strings.HasPrefix(r.URL.Path, "/api/")
	if !ok {
		if api {
			writeErr(w, &domain.APIError{Code: "setup_required", Message: "first-run setup is not complete"})
		} else {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
		}
		return false
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/ical/") {
		return true
	}
	if _, err := a.principal(r); err != nil {
		if !api && safeMethod(r.Method) && status(err) == http.StatusUnauthorized {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return false
		}
		writeErr(w, err)
		return false
	}
	return true
}

// matterPanels is what the matter screen shows from enabled modules: each matter tab as a
// sandboxed frame page, each declared matter item (panel or action) through the same
// framed page, and each extension's rows for this matter through the same
// include= read the API serves, as the viewer.
func (a *App) matterPanels(r *http.Request, p domain.Principal, matter domain.ID) ([]practice.ModulePanel, error) {
	if a.ext == nil {
		return nil, nil
	}
	mods, err := a.ext.Modules.Enabled(r.Context())
	if err != nil {
		return nil, err
	}
	var out []practice.ModulePanel
	for _, in := range mods {
		var names []string
		for _, e := range in.Manifest.Data.Extends {
			if e.Record == "matter" {
				names = append(names, in.ID+"."+e.Name)
			}
		}
		var rows []practice.ModuleRow
		if len(names) > 0 {
			got, err := a.ext.Modules.Include(r.Context(), p, nil, "matter", []domain.ID{matter}, names)
			if err != nil {
				return nil, err
			}
			for _, name := range names {
				for _, row := range got[matter][name] {
					rows = append(rows, moduleRow(name, row))
				}
			}
		}
		var tabs []practice.ModuleTab
		for _, t := range in.Manifest.Tabs {
			if t.Placement == "matter" {
				tabs = append(tabs, practice.ModuleTab{Title: t.Title,
					Path: "/modules/" + url.PathEscape(in.ID) + "/tabs/" + url.PathEscape(t.ID) + "?matter_id=" + url.QueryEscape(string(matter))})
			}
		}
		var items []practice.ModuleItem
		for _, it := range in.Manifest.MatterItems {
			items = append(items, practice.ModuleItem{ID: it.ID, Kind: it.Kind, Title: it.Title,
				Path: modules.MatterItemPage(url.PathEscape(in.ID), url.PathEscape(it.ID)) + "?matter_id=" + url.QueryEscape(string(matter))})
		}
		if len(rows) == 0 && len(tabs) == 0 && len(items) == 0 {
			continue
		}
		out = append(out, practice.ModulePanel{Module: in.ID, Name: in.Manifest.Name, Tabs: tabs, Items: items, Rows: rows})
	}
	return out, nil
}

func moduleRow(name string, row modules.Row) practice.ModuleRow {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := practice.ModuleRow{Extension: name}
	for _, k := range keys {
		out.Fields = append(out.Fields, practice.ModuleField{Name: k, Value: row[k]})
	}
	return out
}
