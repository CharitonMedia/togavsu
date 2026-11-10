package editor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// editorDB is a fresh database with the core, files and editor migrations, or a skip
// when TOGAVSU_TEST_DATABASE_URL is unset outside CI.
func editorDB(t *testing.T) *sql.DB {
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
	sum := sha256.Sum256([]byte("editor/" + t.Name()))
	name := "e" + hex.EncodeToString(sum[:8])
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
	if err := files.Migrate(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, plain); err != nil {
		t.Fatalf("editor migrations: %v", err)
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

// dir is the files Directory and wall reader, and the editor's Matters.
type dir struct {
	mu      sync.Mutex
	matters map[domain.ID]MatterInfo
	walls   map[domain.ID][]domain.ID
}

func (d *dir) Matter(_ context.Context, id domain.ID) (files.Matter, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.matters[id]
	if !ok {
		return files.Matter{}, repository.ErrNotFound
	}
	return files.Matter{ID: m.ID, Number: m.Number, Status: "open"}, nil
}

func (d *dir) OpenMatters(context.Context) ([]files.Matter, error) { return nil, nil }

func (d *dir) UserContact(context.Context, domain.ID) (domain.ID, error) { return "", nil }

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
	return v, nil
}

// matterAs is the editor's Matters: not found behind a wall, as practice answers.
type matterAs struct{ d *dir }

func (m matterAs) Matter(ctx context.Context, p domain.Principal, id domain.ID) (MatterInfo, error) {
	v, _ := m.d.View(ctx, p, id)
	if v.Walled && !v.Allows {
		return MatterInfo{}, domain.NotFound("matter")
	}
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	info, ok := m.d.matters[id]
	if !ok {
		return MatterInfo{}, domain.NotFound("matter")
	}
	return info, nil
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

type nomail struct{}

func (nomail) MailAdmins(context.Context, string, string) error { return nil }

// env is one synthetic install: users of every role, an open matter with a caption, a
// walled matter, a local-disk backend, the file store and the editor.
type env struct {
	t     *testing.T
	ctx   context.Context
	pool  *sql.DB
	files *files.Service
	svc   *Service
	cli   *CLI
	reg   *settings.Registry
	aud   *audit.Service
	dir   *dir
	clock *clock

	admin, attorney, staff, other, accountant, client, outsider domain.Principal
	matter, walled                                              domain.ID
}

func tid(t *testing.T) domain.ID {
	t.Helper()
	v, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func newEnv(t *testing.T) *env {
	t.Helper()
	cli := testCLI(t)
	pool := editorDB(t)
	ctx := context.Background()
	e := &env{t: t, ctx: ctx, pool: pool, cli: cli, clock: &clock{t: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, db.Rebind(q, "postgres"), args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	at := domain.UnixMicroUTC(e.clock.t)
	sys := string(domain.SystemUserID)
	user := func(name, roles, bar string, rs ...domain.Role) domain.Principal {
		u := tid(t)
		exec(`INSERT INTO users (id, created_at, updated_at, created_by, name, email, roles, active, default_hourly_rate_cents, contact_id, bar_number, bar_state)
VALUES (?,?,?,?,?,?,?,1,0,NULL,?,'MO')`, u, at, at, sys, name, string(u)+"@example.invalid", roles, bar)
		return domain.Principal{UserID: u, Roles: rs, Source: domain.SourceUI}
	}
	e.admin = user("Avery Admin", `["admin"]`, "", domain.RoleAdmin)
	e.attorney = user("Ada Example", `["attorney"]`, "12345", domain.RoleAttorney)
	e.staff = user("Sam Staff", `["staff"]`, "", domain.RoleStaff)
	e.other = user("Olive Other", `["staff"]`, "", domain.RoleStaff)
	e.outsider = user("Owen Outsider", `["staff"]`, "", domain.RoleStaff)
	e.accountant = user("Cara Counts", `["accountant"]`, "", domain.RoleAccountant)
	e.client = user("Jane Roe", `["client"]`, "", domain.RoleClient)
	area := tid(t)
	exec(`INSERT INTO practice_areas (id, created_at, updated_at, created_by, name, matter_types, stages, active) VALUES (?,?,?,?,'Family','[]','[]',1)`, area, at, at, sys)
	matter := func(number string) domain.ID {
		m := tid(t)
		exec(`INSERT INTO matters (id, created_at, updated_at, created_by, number, name, practice_area_id, status, stage, case_number, court, client_position,
opened_on, closed_on, close_reason, statute_of_limitations, billing_type, flat_fee_cents, matter_rate_cents, responsible_attorney_id)
VALUES (?,?,?,?,?,?,?,'open','','','','','','','','','hourly',0,0,?)`, m, at, at, sys, number, "Matter "+number, area, e.attorney.UserID)
		return m
	}
	e.matter, e.walled = matter("2026-0001"), matter("2026-0002")
	e.dir = &dir{
		matters: map[domain.ID]MatterInfo{
			e.matter: {ID: e.matter, Number: "2026-0001", Name: "Roe v. Roe", Court: "Circuit Court", County: "Adair", CaseNumber: "26AR-DR00123",
				ClientPosition: "petitioner", ResponsibleAttorneyID: e.attorney.UserID,
				Parties: []Party{{Role: "client", Name: "Jane Roe"}, {Role: "opposing party", Name: "John Roe"}, {Role: "opposing counsel", Name: "Pat Counsel"}}},
			e.walled: {ID: e.walled, Number: "2026-0002", Name: "Walled", Court: "Circuit Court", County: "Macon", CaseNumber: "26MA-CV00001",
				ResponsibleAttorneyID: e.attorney.UserID, Parties: []Party{{Role: "client", Name: "Walled Client"}, {Role: "opposing party", Name: "Walled Other"}}},
		},
		walls: map[domain.ID][]domain.ID{e.walled: {e.attorney.UserID, e.staff.UserID}},
	}
	reg, err := settings.NewWithClock(settings.NewMemory(), quiet{}, e.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	e.reg = reg
	for k, v := range map[string]string{"firm.name": "Synthetic Law LLC", "firm.address": "1 Main St, Kirksville, MO 63501", "firm.phone": "+16605550100", "firm.email": "office@example.invalid"} {
		e.set(k, v)
	}
	aud, err := audit.New(audit.NewPostgres(pool), e.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	e.aud = aud
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	e.files, err = files.New(files.Config{Pool: pool, Audit: aud, Settings: reg, Directory: e.dir, Walls: e.dir, Mail: nomail{}, Key: key,
		StagingDir: filepath.Join(root, "staging"), Now: e.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.files.EnsureLocalDisk(ctx, e.admin, filepath.Join(root, "local")); err != nil {
		t.Fatal(err)
	}
	e.svc, err = New(Config{Files: e.files, Pool: pool, Audit: aud, Settings: reg, Users: identity.NewPG(pool).Users(),
		Matters: matterAs{e.dir}, CLI: cli, Now: e.clock.now})
	if err != nil {
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

// doc makes a DOCX with wordcraft-cli itself from LawCraft commands.
func (e *env) doc(cmds ...Command) []byte {
	e.t.Helper()
	if len(cmds) == 0 {
		cmds = []Command{{ID: "text.insert", Parameters: map[string]any{"text": "Synthetic document."}}}
	}
	b, err := e.cli.Run(e.ctx, RunInput{Commands: cmds, Out: "docx"})
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) create(p domain.Principal, name string, matter domain.ID, body []byte, attorneyOnly bool) files.FileView {
	e.t.Helper()
	f, err := e.files.Create(e.ctx, p, files.NewFile{Name: name, MatterID: matter, AttorneyOnly: attorneyOnly}, strings.NewReader(string(body)), int64(len(body)))
	if err != nil {
		e.t.Fatalf("create %s: %v", name, err)
	}
	return f
}

// current reads a file's current version bytes as the admin.
func (e *env) current(id domain.ID) (files.FileView, files.VersionView, []byte) {
	e.t.Helper()
	f, err := e.files.Get(e.ctx, e.admin, id)
	if err != nil {
		e.t.Fatal(err)
	}
	v, err := e.files.Version(e.ctx, e.admin, id, *f.CurrentVersionID)
	if err != nil {
		e.t.Fatal(err)
	}
	d, err := e.files.OpenForProcessing(e.ctx, e.admin, id, v.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer d.Close()
	var sb strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := d.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return f, v, []byte(sb.String())
}

// text is the document's plain text as wordcraft-cli reads it.
func (e *env) text(b []byte, ext string) string {
	e.t.Helper()
	dir := e.t.TempDir()
	in := filepath.Join(dir, "doc."+ext)
	if err := os.WriteFile(in, b, 0o600); err != nil {
		e.t.Fatal(err)
	}
	out, err := exec.Command(e.cli.Path, "text", in).Output()
	if err != nil {
		e.t.Fatalf("wordcraft-cli text: %v", err)
	}
	return string(out)
}

func (e *env) versions(id domain.ID) []files.VersionView {
	e.t.Helper()
	vs, err := e.files.Versions(e.ctx, e.admin, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return vs
}

func (e *env) audits(entity string, id domain.ID, field string) []domain.AuditEntry {
	e.t.Helper()
	rows, err := e.aud.Rows(e.ctx, audit.RowQuery{Entity: entity, EntityID: id, Field: field})
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func code(err error) string {
	var held *LockHeld
	if errors.As(err, &held) {
		return CodeEditLockHeld
	}
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}
