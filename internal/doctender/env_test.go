package doctender

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// testDB is a fresh database with the core, files and Doc Tender migrations.
func testDB(t *testing.T) *sql.DB {
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
	sum := sha256.Sum256([]byte("doctender/" + t.Name()))
	name := "dt" + hex.EncodeToString(sum[:8])
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
		t.Fatalf("files migrations: %v", err)
	}
	if err := Migrate(ctx, plain); err != nil {
		t.Fatalf("doc tender migrations: %v", err)
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

// directory is the files Directory, wall reader and Doc Tender Matters for a test.
type directory struct {
	mu      sync.Mutex
	matters map[domain.ID]MatterInfo
	walls   map[domain.ID][]domain.ID
}

func (d *directory) Matter(_ context.Context, id domain.ID) (MatterInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.matters[id]
	if !ok {
		return MatterInfo{}, repository.ErrNotFound
	}
	return m, nil
}

func (d *directory) All(context.Context) ([]MatterInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []MatterInfo
	for _, m := range d.matters {
		out = append(out, m)
	}
	return out, nil
}

type filesDir struct{ d *directory }

func (f filesDir) Matter(ctx context.Context, id domain.ID) (files.Matter, error) {
	m, err := f.d.Matter(ctx, id)
	return files.Matter{ID: m.ID, Number: m.Number, Status: m.Status}, err
}

func (f filesDir) OpenMatters(ctx context.Context) ([]files.Matter, error) {
	all, _ := f.d.All(ctx)
	var out []files.Matter
	for _, m := range all {
		if m.Status != "closed" {
			out = append(out, files.Matter{ID: m.ID, Number: m.Number, Status: m.Status})
		}
	}
	return out, nil
}

func (f filesDir) UserContact(context.Context, domain.ID) (domain.ID, error) { return "", nil }

func (d *directory) View(_ context.Context, p domain.Principal, matter domain.ID) (domain.WallView, error) {
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

func (d *directory) setStatus(id domain.ID, status string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.matters[id]
	m.Status = status
	d.matters[id] = m
}

type mailbox struct {
	mu   sync.Mutex
	sent []string
}

func (m *mailbox) MailAdmins(_ context.Context, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, "admins|"+subject+"\n"+body)
	return nil
}

func (m *mailbox) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+"|"+subject+"\n"+body)
	return nil
}

func (m *mailbox) all() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.sent...)
}

type notices struct {
	mu    sync.Mutex
	count int
}

func (n *notices) NotifyReviewers(context.Context, string, string, string, domain.ID) error {
	n.mu.Lock()
	n.count++
	n.mu.Unlock()
	return nil
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

// env is one test install: users of every role, an open client matter, a walled
// matter, a closed matter, the files store on local disk, the settings registry, a
// test model provider on the OpenAI wire, and Tesseract as the second reader.
type env struct {
	t      *testing.T
	ctx    context.Context
	pool   *sql.DB
	files  *files.Service
	svc    *Service
	deps   Deps
	dir    *directory
	mail   *mailbox
	notes  *notices
	clock  *clock
	reg    *settings.Registry
	ai     *fakeAI
	router *Router

	admin, attorney, staff, outsider, system domain.Principal
	open, walled, closed, other               domain.ID
}

func id(t *testing.T) domain.ID {
	t.Helper()
	v, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func secondReader(t *testing.T) SecondReader {
	if _, err := exec.LookPath("tesseract"); err == nil {
		return Tesseract{}
	}
	if os.Getenv("DOCTENDER_REQUIRE_TESSERACT") != "" {
		t.Fatal("tesseract is required (DOCTENDER_REQUIRE_TESSERACT is set)")
	}
	t.Log("tesseract is not installed: the second read uses the test glyph reader")
	return glyphReader{}
}

// glyphReader stands in for Tesseract only where the binary is absent.
type glyphReader struct{}

func (glyphReader) Name() string { return "glyph-reader" }
func (glyphReader) Read(_ context.Context, img []byte) (string, error) {
	return readGlyphs(img), nil
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	pool := testDB(t)
	e := &env{t: t, ctx: ctx, pool: pool, mail: &mailbox{}, notes: &notices{}, clock: &clock{t: time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)}}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, db.Rebind(q, "postgres"), args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	at := domain.UnixMicroUTC(e.clock.t)
	sys := string(domain.SystemUserID)
	user := func(name, roles string, rs ...domain.Role) domain.Principal {
		u := id(t)
		exec(`INSERT INTO users (id, created_at, updated_at, created_by, name, email, roles, active, default_hourly_rate_cents, contact_id, bar_number, bar_state)
VALUES (?,?,?,?,?,?,?,1,0,NULL,'','')`, u, at, at, sys, name, string(u)+"@example.invalid", roles)
		return domain.Principal{UserID: u, Roles: rs, Source: domain.SourceUI}
	}
	e.admin = user("Admin", `["admin","attorney"]`, domain.RoleAdmin, domain.RoleAttorney)
	e.attorney = user("Attorney", `["attorney"]`, domain.RoleAttorney)
	e.staff = user("Staff", `["staff"]`, domain.RoleStaff)
	e.outsider = user("Outsider", `["attorney"]`, domain.RoleAttorney)
	dt := user("Doc Tender", `["admin"]`, domain.RoleAdmin)
	e.system = domain.Principal{UserID: dt.UserID, Source: domain.SourceSystem}
	area := id(t)
	exec(`INSERT INTO practice_areas (id, created_at, updated_at, created_by, name, matter_types, stages, active) VALUES (?,?,?,?,'Family','[]','[]',1)`, area, at, at, sys)
	matter := func(number, status, caseNo string) domain.ID {
		m := id(t)
		exec(`INSERT INTO matters (id, created_at, updated_at, created_by, number, name, practice_area_id, status, stage, case_number, court, client_position,
opened_on, closed_on, close_reason, statute_of_limitations, billing_type, flat_fee_cents, matter_rate_cents, responsible_attorney_id)
VALUES (?,?,?,?,?,?,?,?,'',?,'','','','','','','hourly',0,0,?)`, m, at, at, sys, number, "Matter "+number, area, status, caseNo, e.attorney.UserID)
		return m
	}
	e.open = matter("2026-0001", "open", "23AR-CV00825")
	e.walled = matter("2026-0002", "open", "")
	e.closed = matter("2025-0099", "closed", "")
	e.other = matter("2026-0003", "open", "24JE-DR01234")
	e.dir = &directory{
		matters: map[domain.ID]MatterInfo{
			e.open:   {ID: e.open, Number: "2026-0001", Name: "Example dissolution", Status: "open", CaseNumber: "23AR-CV00825", ClientFirst: "Jordan", ClientLast: "Example", Parties: []string{"Jordan Example", "Riley Example"}},
			e.walled: {ID: e.walled, Number: "2026-0002", Name: "Sealed matter", Status: "open", ClientFirst: "Casey", ClientLast: "Sample", Parties: []string{"Casey Sample"}},
			e.closed: {ID: e.closed, Number: "2025-0099", Name: "Old matter", Status: "closed", ClientLast: "Archive", Parties: []string{"Pat Archive"}},
			e.other:  {ID: e.other, Number: "2026-0003", Name: "Acme estate", Status: "open", CaseNumber: "24JE-DR01234", ClientOrg: "Acme Holding Company of Missouri LLC", Parties: []string{"Morgan Testperson"}},
		},
		walls: map[domain.ID][]domain.ID{e.walled: {e.attorney.UserID, e.staff.UserID, e.system.UserID}},
	}
	reg, err := settings.New(settings.NewMemory(), quiet{})
	if err != nil {
		t.Fatal(err)
	}
	e.reg = reg
	aud, err := audit.New(audit.NewPostgres(pool), e.clock.now)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	var svc *Service
	e.files, err = files.New(files.Config{
		Pool: pool, Audit: aud, Settings: reg, Directory: filesDir{e.dir}, Walls: e.dir, Mail: e.mail, Key: key,
		StagingDir: root + "/staging", Now: e.clock.now,
		OnVersion: func(ctx context.Context, ev files.VersionCreated) {
			if svc != nil {
				svc.OnVersion(ctx, ev)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.files.EnsureLocalDisk(ctx, e.admin, root+"/local"); err != nil {
		t.Fatal(err)
	}
	e.ai = newFakeAI(t)
	e.set("ai.openai_api_key", "test-openai-key")
	e.set("ai.primary_provider", "openai")
	e.set("ai.primary_strong_model", "gpt-test-strong")
	e.set("ai.primary_fast_model", "gpt-test-fast")
	e.set("ai.speech_to_text_model", "whisper-test")
	e.router = &Router{Settings: reg, BaseURLs: map[string]string{ProviderOpenAI: e.ai.srv.URL, ProviderAnthropic: e.ai.srv.URL}}
	e.deps = Deps{Pool: pool, Files: e.files, Settings: reg, Models: e.router, Matters: e.dir, Mail: e.mail, Notify: e.notes, Audit: aud,
		Second: secondReader(t), System: e.system, Worker: "worker-1", Now: e.clock.now}
	svc, err = New(e.deps)
	if err != nil {
		t.Fatal(err)
	}
	e.router.Log = svc.CallLog()
	e.svc = svc
	return e
}

func (e *env) set(key, value string) {
	e.t.Helper()
	if _, err := e.reg.Set(context.Background(), key, value, e.admin); err != nil {
		e.t.Fatal(err)
	}
}

// worker is a second service on the same database, as another core process would be.
func (e *env) worker(name string) *Service {
	e.t.Helper()
	d := e.deps
	d.Worker = name
	s, err := New(d)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) upload(p domain.Principal, matter domain.ID, name, mediaType string, body []byte) files.FileView {
	e.t.Helper()
	return e.uploadFrom(p, matter, name, mediaType, "upload", body)
}

func (e *env) uploadFrom(p domain.Principal, matter domain.ID, name, mediaType, source string, body []byte) files.FileView {
	e.t.Helper()
	f, err := e.files.Create(e.ctx, p, files.NewFile{Name: name, MatterID: matter, MediaType: mediaType, Source: source}, strings.NewReader(string(body)), int64(len(body)))
	if err != nil {
		e.t.Fatalf("upload %s: %v", name, err)
	}
	return f
}

func (e *env) drain() {
	e.t.Helper()
	if err := e.svc.Drain(e.ctx); err != nil {
		e.t.Fatalf("drain: %v", err)
	}
}

func (e *env) jobFor(file domain.ID) Job {
	e.t.Helper()
	jobs, err := e.svc.jobsWhere(e.ctx, `file_id = ? AND kind = 'file' ORDER BY created_at DESC, id DESC`, file)
	if err != nil || len(jobs) == 0 {
		e.t.Fatalf("no job for %s: %v", file, err)
	}
	return jobs[0]
}

func (e *env) unitsFor(job domain.ID) []Unit {
	e.t.Helper()
	us_, err := e.svc.units(e.ctx, job)
	if err != nil {
		e.t.Fatal(err)
	}
	return us_
}

func (e *env) get(p domain.Principal, f domain.ID) files.FileView {
	e.t.Helper()
	v, err := e.files.Get(e.ctx, p, f)
	if err != nil {
		e.t.Fatalf("get %s: %v", f, err)
	}
	return v
}

func (e *env) aiCalls(where string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRowContext(e.ctx, db.Rebind(`SELECT COUNT(*) FROM ai_call_log WHERE `+where, "postgres"), args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) openExceptions(file domain.ID) []Exception {
	e.t.Helper()
	out, err := e.svc.exceptionsWhere(e.ctx, `file_id = ? AND status = 'open' ORDER BY opened_at`, file)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) markdown(p domain.Principal, f domain.ID) (files.RenditionView, string) {
	e.t.Helper()
	r, body, err := e.files.MarkdownCopy(e.ctx, p, f)
	if err != nil {
		e.t.Fatalf("markdown copy: %v", err)
	}
	return r, string(body)
}

func isCode(err error, code string) bool {
	var api *domain.APIError
	return errors.As(err, &api) && api.Code == code
}
