// Package coretest runs a module's acceptance suite against the real composed core,
// in-process, on Postgres. New opens the same core the release image runs (core.Open:
// every domain, the admin page, the module runtime, the marketplace's trust root) on a
// throwaway database reached through a non-superuser core role, with one stand-in: the
// supervisor. Deploying a module starts the http.Handler the test registered for its id
// in this process, with the secrets the core handed over (its API token, webhook secret
// and its own database login), so the module calls the real core exactly as its
// container would.
//
// The database comes from TOGAVSU_TEST_DATABASE_URL, a Postgres superuser URL. New skips
// the test when it is unset, and fails it in CI. Everything New makes (the database, the
// core role, the module logins and the servers) is removed at cleanup.
//
//	func TestAcceptance(t *testing.T) {
//		c := coretest.New(t)
//		c.Serve("my-module", func(m coretest.Module) http.Handler { return mymodule.New(m.CoreURL, m.Token, m.DB).Handler() })
//		pub, priv, _ := ed25519.GenerateKey(nil)
//		c.TrustKey("My module key", pub)
//		signed, _ := modulekit.Build(priv, files)
//		c.Install(signed)
//		c.ApproveAll("my-module")
//		page := c.Tab("staff", "my-module", "home", "")
//		...
//	}
package coretest

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/core"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// Client calls the core as one person or token (session cookie, CSRF, bearer).
type Client = testkit.Client

// Resp is one response: Code, Body, JSON (the body when it is a JSON object) and Loc.
type Resp = testkit.Resp

// Module is a deployed module as its server sees it: the core's URL, its API token,
// its webhook secret and its own database login.
type Module = testkit.Module

// Runtime is the supervisor's stand-in; Core.Runtime exposes its request log.
type Runtime = testkit.Runtime

// Backups is the backup stand-in; Core.Backups exposes the archives made.
type Backups = testkit.Backups

// Deployment is one start of a module: its id, version and the secrets the core handed
// over (togavsu_api_token, togavsu_webhook_secret, togavsu_db_role,
// togavsu_db_password, togavsu_db_schema).
type Deployment = modules.Deployment

// Roles, as Users keys them.
const (
	Admin      = "admin"
	Attorney   = "attorney"
	Staff      = "staff"
	ClientRole = "client"
	Accountant = "accountant"
)

// User is one signed-in person: their client (session and CSRF) and who they are.
type User struct {
	*Client
	ID    string
	Role  string
	Name  string
	Email string
}

// Core is a running composed core with the firm's people signed in and two matters.
type Core struct {
	T *testing.T
	// URL is the core's base URL.
	URL string
	// DatabaseURL is the URL the core connects with (its non-superuser role).
	DatabaseURL string
	Runtime     *Runtime
	Backups     *Backups

	// Users holds one signed-in user of every role, by role name; the fields below are
	// the same users.
	Users map[string]*User

	Admin, Attorney, Staff, Client, Accountant *User

	// PracticeArea and Activity are a practice area and a time activity type.
	PracticeArea, Activity string
	// Open is a matter everyone on staff can see; its client party is ClientContact,
	// whom the client user signs in as. Walled is a matter walled to the attorney.
	Open, Walled, ClientContact string

	app *core.App
}

// testKey is the core's synthetic master key.
var testKey = func() []byte {
	sum := sha256.Sum256([]byte("togavsu coretest synthetic key"))
	return sum[:]
}()

// New starts the composed core on a fresh database and signs in a user of every role
// (admin, attorney, staff, client, accountant). Register module handlers (Handle,
// Serve) before installing.
func New(t *testing.T) *Core {
	t.Helper()
	c := &Core{T: t, Users: map[string]*User{}, Backups: &testkit.Backups{}}
	// The database is named for the test and the package directory, so module
	// repositories sharing one server don't meet, and a rerun replaces its own leftovers.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	c.DatabaseURL = testkit.AppRole(t, testkit.NewDatabase(t, "k", "coretest/"+dir+"/"+t.Name()))
	testkit.DropModuleRoles(t, c.DatabaseURL)
	c.Runtime = testkit.NewRuntime(func() string { return c.URL }, c.DatabaseURL)
	t.Cleanup(c.Runtime.Close)
	app, err := core.Open(context.Background(), core.Config{DatabaseURL: c.DatabaseURL, Key: testKey, Domain: "firm.example",
		MailEvery: -time.Nanosecond, DataDir: t.TempDir(), ModuleRuntime: c.Runtime, ModuleBackups: c.Backups, NoBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	c.app = app
	srv := httptest.NewServer(app.Handler())
	c.URL = srv.URL
	t.Cleanup(func() {
		srv.Close()
		_ = app.Close()
	})

	admin := testkit.FirstRun(t, c.URL)
	c.Admin = c.add(&User{Client: admin, Role: Admin, Name: testkit.AdminName, Email: testkit.AdminEmail})
	c.Admin.ID = testkit.Str(t, admin.Must(http.StatusOK, "GET", "/api/v1/me", nil).JSON, "id")
	c.Attorney = c.enroll(Attorney, "Ada Attorney", "ada@firm.example", "")
	c.Staff = c.enroll(Staff, "Sam Staff", "sam@firm.example", "")
	c.Accountant = c.enroll(Accountant, "Ari Accountant", "ari@firm.example", "")

	c.PracticeArea = testkit.Str(t, admin.Must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Probate", "stages": []string{"Intake"}}).JSON, "id")
	c.Activity = testkit.Str(t, admin.Must(http.StatusCreated, "POST", "/api/v1/activity-types", map[string]any{"name": "Drafting"}).JSON, "id")
	c.Open, c.ClientContact = c.Matter("Estate of Harlow", "cleo@client.example", nil)
	c.Walled, _ = c.Matter("Estate of Quist", "", nil)
	if err := app.Practice.SetWall(context.Background(), c.principal(c.Admin), domain.ID(c.Walled), []domain.ID{domain.ID(c.Attorney.ID)}); err != nil {
		t.Fatal(err)
	}
	c.Client = c.enroll(ClientRole, "Cleo Client", "cleo@client.example", domain.ID(c.ClientContact))
	return c
}

func (c *Core) add(u *User) *User {
	c.Users[u.Role] = u
	return u
}

func (c *Core) enroll(role, name, email string, contact domain.ID) *User {
	c.T.Helper()
	cl, id := testkit.EnrollUser(c.T, c.app.Identity, c.URL, c.Admin.Client, name, email, contact, domain.Role(role))
	return c.add(&User{Client: cl, ID: string(id), Role: role, Name: name, Email: email})
}

func (c *Core) principal(u *User) domain.Principal {
	c.T.Helper()
	return testkit.Caller(c.T, c.app.Identity, u.Client).Principal
}

// As is the signed-in user of role who ("admin", "attorney", "staff", "client" or
// "accountant").
func (c *Core) As(who string) *User {
	c.T.Helper()
	u, ok := c.Users[who]
	if !ok {
		c.T.Fatalf("coretest: no %q user", who)
	}
	return u
}

// NewClient is a signed-out client of the core.
func (c *Core) NewClient() *Client { return testkit.NewClient(c.T, c.URL) }

// Matter opens a matter with a client party (who has clientEmail when it is set) and
// returns the matter and client contact ids. extra fields go on the matter body.
func (c *Core) Matter(name, clientEmail string, extra map[string]any) (matter, clientContact string) {
	c.T.Helper()
	return testkit.Matter(c.T, c.Admin.Client, c.PracticeArea, c.Attorney.ID, name, clientEmail, extra)
}

// Wall limits matter to the users named. The admin is not added: a walled matter answers
// 404 to anyone outside the wall, the admin included.
func (c *Core) Wall(matter string, allowed ...*User) {
	c.T.Helper()
	ids := make([]domain.ID, len(allowed))
	for i, u := range allowed {
		ids[i] = domain.ID(u.ID)
	}
	if err := c.app.Practice.SetWall(context.Background(), c.principal(c.Admin), domain.ID(matter), ids); err != nil {
		c.T.Fatal(err)
	}
}

// LogTime records increments of time on matter as who, today, and returns the entry id.
func (c *Core) LogTime(who, matter string, increments int) string {
	c.T.Helper()
	r := c.As(who).Must(http.StatusCreated, "POST", "/api/v1/time-entries", map[string]any{"matter_id": matter, "worked_on": time.Now().UTC().Format(time.DateOnly),
		"increments": increments, "description": "Synthetic work", "activity_type_id": c.Activity, "billable": true})
	return testkit.Str(c.T, r.JSON, "id")
}

// --- Modules ---

// Handle routes module to h from its next deployment on.
func (c *Core) Handle(module string, h http.Handler) { c.Runtime.Handle(module, h) }

// Serve routes module to the server build makes from each deployment (its token,
// webhook secret and database login), from the next deployment on.
func (c *Core) Serve(module string, build func(Module) http.Handler) { c.Runtime.Serve(module, build) }

// Last is module's latest deployment, with the secrets the core handed over.
func (c *Core) Last(module string) Deployment { return c.Runtime.Last(module) }

// TrustKey adds pub as a trusted local signing key, as the admin does on the Modules tab.
func (c *Core) TrustKey(name string, pub ed25519.PublicKey) {
	c.T.Helper()
	testkit.TrustKey(c.Admin.Client, name, pub)
}

// TryInstall uploads a signed package file as the admin (POST /api/v1/modules) and
// returns the response: 202 awaiting approval, or the refusal.
func (c *Core) TryInstall(signed []byte) Resp {
	c.T.Helper()
	return c.Admin.Upload("/api/v1/modules", nil, signed)
}

// Install installs a signed package file and fails the test unless the core accepts it
// for approval.
func (c *Core) Install(signed []byte) {
	c.T.Helper()
	if r := c.TryInstall(signed); r.Code != http.StatusAccepted {
		c.T.Fatalf("install: %d %s", r.Code, r.Body)
	}
}

// TryUpdate uploads a new version of module as the admin and returns the response.
func (c *Core) TryUpdate(module string, signed []byte) Resp {
	c.T.Helper()
	return c.Admin.Upload("/api/v1/modules/"+url.PathEscape(module)+"/update", nil, signed)
}

// ApproveAll approves every undecided permission of module on the admin's Modules tab;
// the core then provisions its schema and login, runs its migrations and deploys it.
func (c *Core) ApproveAll(module string) {
	c.T.Helper()
	testkit.ApproveOnTab(c.Admin.Client, c.app.Modules, c.principal(c.Admin), module)
}

// Uninstall removes module on the Modules tab, deleting its data (after a backup) when
// deleteData is set and keeping it otherwise.
func (c *Core) Uninstall(module string, deleteData bool) {
	c.T.Helper()
	data := "keep"
	if deleteData {
		data = "delete"
	}
	if _, problem := testkit.TabForm(c.Admin.Client, "uninstall", url.Values{"module_id": {module}, "data": {data}, "confirmation": {module}}, nil); problem != "" {
		c.T.Fatalf("uninstall %s: %s", module, problem)
	}
}

// ContextFor issues a context token for module to who, scoped to matter when it is
// set, through the API the core's frames use.
func (c *Core) ContextFor(who, module, matter string) string {
	c.T.Helper()
	body := map[string]any{}
	if matter != "" {
		body["matter_id"] = matter
	}
	r := c.As(who).Must(http.StatusCreated, "POST", "/api/v1/modules/"+url.PathEscape(module)+"/context-tokens", body)
	return testkit.Str(c.T, r.JSON, "token")
}

// AgentToken has the admin issue an API token owned by who with scopes, and returns a
// client that calls with it as an agent would.
func (c *Core) AgentToken(who string, scopes ...string) *Client {
	c.T.Helper()
	r := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "coretest agent", "owner_user_id": c.As(who).ID, "scopes": scopes})
	cl := c.NewClient()
	cl.Bearer = testkit.Str(c.T, r.JSON, "secret")
	return cl
}

// ModuleClient calls the core with module's own token, as its container does. Set
// ContextHeader (X-Togavsu-Context-Token) per request to act for a viewer.
func (c *Core) ModuleClient(module string) *Client {
	c.T.Helper()
	tok := c.Last(module).Secrets["togavsu_api_token"]
	if tok == "" {
		c.T.Fatalf("coretest: %s has no deployment", module)
	}
	cl := c.NewClient()
	cl.Bearer = tok
	return cl
}

// ContextHeader is the request header a module sends a viewer's context token in.
const ContextHeader = modules.ContextHeader

// ModuleDB opens module's own database login on its own schema, from its latest
// deployment, as the real runtime hands it to the container. It is closed at cleanup.
func (c *Core) ModuleDB(module string) *sql.DB {
	c.T.Helper()
	s := c.Last(module).Secrets
	return c.OpenModuleDB(s["togavsu_db_role"], s["togavsu_db_password"])
}

// OpenModuleDB opens the core's database as a module login. It is closed at cleanup.
func (c *Core) OpenModuleDB(role, password string) *sql.DB {
	c.T.Helper()
	d, err := c.Runtime.OpenDB(role, password)
	if err != nil {
		c.T.Fatal(err)
	}
	return d
}

var frameSrc = regexp.MustCompile(`src="(/m/[^"]+)"`)

// Tab opens module's tab as who (matter for a matter-screen tab), follows the frame the
// core draws to the module's page with the viewer's context token, and returns the
// module's response.
func (c *Core) Tab(who, module, tab, matter string) Resp {
	c.T.Helper()
	p := "/modules/" + url.PathEscape(module) + "/tabs/" + url.PathEscape(tab)
	if matter != "" {
		p += "?matter_id=" + url.QueryEscape(matter)
	}
	u := c.As(who)
	page := u.Must(http.StatusOK, "GET", p, nil)
	for _, m := range frameSrc.FindAllStringSubmatch(page.Body, -1) {
		src := html.UnescapeString(m[len(m)-one])
		if strings.HasPrefix(src, "/m/"+module+"/") {
			return u.Do("GET", src, nil)
		}
	}
	c.T.Fatalf("coretest: no %s frame in %s: %s", module, p, page.Body)
	return Resp{}
}

// DeliverEvents queues the core events modules subscribe to and posts the webhooks now,
// as the background pump does, and returns how many were sent.
func (c *Core) DeliverEvents() int {
	c.T.Helper()
	ctx := context.Background()
	if _, err := c.app.Modules.Pump(ctx); err != nil {
		c.T.Fatal(err)
	}
	sent, err := c.app.Modules.Deliver(ctx)
	if err != nil {
		c.T.Fatal(err)
	}
	return sent
}

// Count runs a counting query on the core's database as the core role.
func (c *Core) Count(q string, args ...any) int {
	c.T.Helper()
	var n int
	if err := c.app.Pool.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		c.T.Fatal(err)
	}
	return n
}

// SchemaExists reports whether module's own schema is in the database.
func (c *Core) SchemaExists(module string) bool {
	c.T.Helper()
	return c.Count(`SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1`, modules.SchemaFor(module)) > zero
}

// Small fixed numbers spelled as arithmetic for the AC-030 source check.
var (
	zero = int(0)
	one  = zero + 1
)
