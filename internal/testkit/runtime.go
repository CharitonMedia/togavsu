package testkit

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// Module is what a deployed module's server is built from: the secrets the core handed
// the supervisor for it, the core's address and the module's own database login.
type Module struct {
	ID string
	// CoreURL is the running core's base URL.
	CoreURL string
	// Token is the module's scoped API token (togavsu_api_token).
	Token string
	// WebhookSecret signs the core's event webhooks (togavsu_webhook_secret).
	WebhookSecret string
	// DB is the module's own login on its own schema; nil when the deployment carries no
	// database role.
	DB         *sql.DB
	Deployment modules.Deployment
}

// Runtime stands in for the supervisor (modules.Runtime): Deploy starts the module's
// server in this process with the secrets the core handed over, so the module really
// calls the core with its token, verifies its webhooks with its secret and uses its own
// database login. A module id is routed to the handler registered for it (Handle or
// Serve), else to Default; with neither, the module answers 404.
type Runtime struct {
	// Default builds the server of a module with no handler of its own.
	Default func(Module) http.Handler

	coreURL func() string
	dsn     string

	mu          sync.Mutex
	servers     map[string]*httptest.Server
	handlers    map[string]func(Module) http.Handler
	deployments []modules.Deployment
	requests    map[string][]*http.Request
	stopped     []string
	dbs         []*sql.DB
}

// NewRuntime is a runtime for the core at coreURL() on the database at dsn (the URL the
// core connects with; module logins replace its user). Close it at cleanup.
func NewRuntime(coreURL func() string, dsn string) *Runtime {
	return &Runtime{coreURL: coreURL, dsn: dsn, servers: map[string]*httptest.Server{},
		handlers: map[string]func(Module) http.Handler{}, requests: map[string][]*http.Request{}}
}

// Handle routes module to h from its next deployment on.
func (f *Runtime) Handle(module string, h http.Handler) {
	f.Serve(module, func(Module) http.Handler { return h })
}

// Serve routes module to the server build makes from the deployment, from the next
// deployment on.
func (f *Runtime) Serve(module string, build func(Module) http.Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[module] = build
}

// OpenDB opens the core's database as role; Close closes it.
func (f *Runtime) OpenDB(role, password string) (*sql.DB, error) {
	d, err := OpenAs(f.dsn, role, password)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.dbs = append(f.dbs, d)
	f.mu.Unlock()
	return d, nil
}

// Deploy implements modules.Runtime.
func (f *Runtime) Deploy(_ context.Context, d modules.Deployment) error {
	f.mu.Lock()
	build, ok := f.handlers[d.Module]
	if !ok {
		build = f.Default
	}
	f.mu.Unlock()
	h := http.NotFoundHandler()
	if build != nil {
		m := Module{ID: d.Module, CoreURL: f.coreURL(), Token: d.Secrets["togavsu_api_token"],
			WebhookSecret: d.Secrets["togavsu_webhook_secret"], Deployment: d}
		if role := d.Secrets["togavsu_db_role"]; role != "" {
			mdb, err := f.OpenDB(role, d.Secrets["togavsu_db_password"])
			if err != nil {
				return err
			}
			m.DB = mdb
		}
		h = build(m)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deployments = append(f.deployments, d)
	if old := f.servers[d.Module]; old != nil {
		old.Close()
	}
	module := d.Module
	f.servers[d.Module] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests[module] = append(f.requests[module], r.Clone(context.Background()))
		f.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	return nil
}

// Stop implements modules.Runtime.
func (f *Runtime) Stop(_ context.Context, module string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, module)
	if s := f.servers[module]; s != nil {
		s.Close()
		delete(f.servers, module)
	}
	return nil
}

// Remove implements modules.Runtime.
func (f *Runtime) Remove(ctx context.Context, module string) error { return f.Stop(ctx, module) }

// BaseURL implements modules.Runtime: the running server's URL, or an address that
// refuses connections when the module is not running.
func (f *Runtime) BaseURL(module string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.servers[module]; s != nil {
		return s.URL
	}
	return "http://127.0.0.1:1"
}

// Last is module's latest deployment (its version and secrets), or the zero value.
func (f *Runtime) Last(module string) modules.Deployment {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.deployments) - 1; i >= 0; i-- {
		if f.deployments[i].Module == module {
			return f.deployments[i]
		}
	}
	return modules.Deployment{}
}

// Deployments is every deployment so far, oldest first.
func (f *Runtime) Deployments() []modules.Deployment {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]modules.Deployment(nil), f.deployments...)
}

// Stopped is every module id stopped or removed so far, in order.
func (f *Runtime) Stopped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stopped...)
}

// Requests is every request module's servers got, oldest first (bodies not kept).
func (f *Runtime) Requests(module string) []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.requests[module]...)
}

// Paths is the URL path of every request module's servers got.
func (f *Runtime) Paths(module string) []string {
	var out []string
	for _, r := range f.Requests(module) {
		out = append(out, r.URL.Path)
	}
	return out
}

// Headers is the headers of every request module's servers got.
func (f *Runtime) Headers(module string) []http.Header {
	var out []http.Header
	for _, r := range f.Requests(module) {
		out = append(out, r.Header)
	}
	return out
}

// Close stops every server and closes the module logins it opened.
func (f *Runtime) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, s := range f.servers {
		s.Close()
		delete(f.servers, id)
	}
	for _, d := range f.dbs {
		_ = d.Close()
	}
	f.dbs = nil
}

// Backups stands in for the supervisor's backups (modules.Backups): Create records a
// synthetic archive. Fail and During are fault hooks; set them only while no module call
// is running.
type Backups struct {
	// Fail, when set, is Create's error.
	Fail error
	// During runs inside Create, before the archive is recorded.
	During func()

	mu   sync.Mutex
	made []modules.Backup
}

// Add records an archive as if the supervisor held it (an upload, say).
func (b *Backups) Add(bk modules.Backup) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.made = append(b.made, bk)
}

// Made is every archive recorded so far, oldest first.
func (b *Backups) Made() []modules.Backup {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]modules.Backup(nil), b.made...)
}

// Create implements modules.Backups.
func (b *Backups) Create(_ context.Context, _ string) (modules.Backup, error) {
	if b.During != nil {
		b.During()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Fail != nil {
		return modules.Backup{}, b.Fail
	}
	id, err := domain.NewID()
	if err != nil {
		return modules.Backup{}, err
	}
	bk := modules.Backup{ID: string(id), CreatedAt: time.Now().UTC(), SHA256: "synthetic-sha", Bytes: int64(one)}
	b.made = append(b.made, bk)
	return bk, nil
}

// Get implements modules.Backups.
func (b *Backups) Get(_ context.Context, id string) (modules.Backup, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, bk := range b.made {
		if bk.ID == id {
			return bk, nil
		}
	}
	return modules.Backup{}, domain.NotFound("backup")
}
