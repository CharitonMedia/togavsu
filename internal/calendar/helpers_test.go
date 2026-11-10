package calendar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

type quiet struct{}

func (quiet) SettingChanged(context.Context, settings.Change) error { return nil }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

type mail struct {
	mu   sync.Mutex
	sent []string
}

func (m *mail) Send(_ context.Context, to, subject, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+"|"+subject)
	return nil
}

func (m *mail) count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.sent) }

// fixture is a calendar service on the real practice service, so walls and matter
// access come from practice exactly as in the running core.
type fixture struct {
	t        *testing.T
	ctx      context.Context
	svc      *Service
	store    *Memory
	practice *practice.Service
	pm       *practice.Memory
	reg      *settings.Registry
	audit    *audit.Memory
	clock    *clock
	mail     *mail

	admin, attorney, other, staff, accountant, client domain.Principal
	area                                              domain.ID
	n                                                 uint64
}

func tid(n uint64) domain.ID {
	id, err := domain.ComposeV7(1_767_000_000_000, uint16(n), n+77)
	if err != nil {
		panic(err)
	}
	return id
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	reg, err := settings.New(settings.NewMemory(), quiet{})
	if err != nil {
		t.Fatal(err)
	}
	pm := practice.NewMemory()
	ps := practice.New(pm, reg)
	am := audit.NewMemory()
	f := &fixture{t: t, ctx: ctx, pm: pm, practice: ps, reg: reg, audit: am, store: NewMemory(),
		clock: &clock{t: time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)}, mail: &mail{}}
	aud, err := audit.New(am, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	f.admin = f.user(1, "Avery Admin", domain.RoleAdmin, domain.RoleAttorney)
	f.attorney = f.user(2, "Ada Attorney", domain.RoleAttorney)
	f.other = f.user(3, "Otto Other", domain.RoleAttorney)
	f.staff = f.user(4, "Sam Staff", domain.RoleStaff)
	f.accountant = f.user(5, "Cora Counts", domain.RoleAccountant)
	f.client = f.user(6, "Cal Client", domain.RoleClient)
	f.n = 100
	if _, err := reg.Set(ctx, "firm.time_zone", "America/Chicago", f.admin); err != nil {
		t.Fatal(err)
	}
	f.svc, err = New(Deps{
		Store: f.store, Policy: ps.Policy, Matters: PracticeMatters{Svc: ps}, Users: UsersFunc(pm.User),
		Settings: reg, Audit: aud, Mailer: f.mail, BaseURL: "https://firm.example", Now: f.clock.Now,
		IDs: func() (domain.ID, error) { f.n++; return tid(f.n), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	area, err := ps.CreateArea(ctx, f.admin, "Litigation", "", []string{"Intake"})
	if err != nil {
		t.Fatal(err)
	}
	f.area = area.ID
	return f
}

func (f *fixture) user(n uint64, name string, roles ...domain.Role) domain.Principal {
	f.t.Helper()
	p := domain.Principal{UserID: tid(n), Roles: roles, Source: domain.SourceUI}
	enc, err := domain.MarshalRoles(roles)
	if err != nil {
		f.t.Fatal(err)
	}
	email := strings.ToLower(strings.Fields(name)[0]) + "@firm.example"
	if err := f.pm.PutUser(f.ctx, domain.User{Meta: domain.Meta{ID: p.UserID}, Name: name, Email: email, Roles: enc, Active: true}); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fixture) matter(name string) domain.Matter {
	f.t.Helper()
	m, err := f.practice.CreateMatter(f.ctx, f.admin, practice.MatterInput{Name: name, PracticeAreaID: f.area, BillingType: practice.BillingHourly})
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

// walled creates a matter walled to the attorney only. Staff and the admin are not on it.
func (f *fixture) walled(name string) domain.Matter {
	f.t.Helper()
	m := f.matter(name)
	if err := f.practice.SetWall(f.ctx, f.admin, m.ID, []domain.ID{f.attorney.UserID}); err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) event(p domain.Principal, in EventInput) Event {
	f.t.Helper()
	e, err := f.svc.CreateEvent(f.ctx, p, in)
	if err != nil {
		f.t.Fatalf("create event %q: %v", in.Title, err)
	}
	return e
}

func (f *fixture) deadline(p domain.Principal, in DeadlineInput) Deadline {
	f.t.Helper()
	d, err := f.svc.CreateDeadline(f.ctx, p, in)
	if err != nil {
		f.t.Fatalf("create deadline %q: %v", in.Title, err)
	}
	return d
}

func chicago(t *testing.T) *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", s, chicago(t))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func code(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	if err == nil {
		return ""
	}
	return "other:" + err.Error()
}

// server mounts the handler with templates from the repository tree. The test
// principal comes from X-Test-User; no header is signed out.
func (f *fixture) server() *httptest.Server {
	f.t.Helper()
	users := map[string]domain.Principal{}
	for _, p := range []domain.Principal{f.admin, f.attorney, f.other, f.staff, f.accountant, f.client} {
		users[string(p.UserID)] = p
	}
	h, err := NewHandler(f.svc, os.DirFS("../../web/templates/calendar"), func(r *http.Request) (domain.Principal, error) {
		p, ok := users[r.Header.Get("X-Test-User")]
		if !ok {
			return domain.Principal{}, domain.Unauthenticated("sign in")
		}
		return p, nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	srv := httptest.NewServer(h.Routes())
	f.t.Cleanup(srv.Close)
	return srv
}
