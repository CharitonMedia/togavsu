package dashboard

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

type nopAudit struct {
	mu      sync.Mutex
	changes []settings.Change
}

func (a *nopAudit) SettingChanged(_ context.Context, c settings.Change) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.changes = append(a.changes, c)
	return nil
}

func newTestService(t *testing.T, sources map[string]Source) (*Service, *settings.Registry, *nopAudit) {
	t.Helper()
	aud := &nopAudit{}
	reg, err := settings.New(settings.NewMemory(), aud)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	svc, err := New(Deps{Store: NewMemory(), Settings: reg, Catalog: NewCatalog(sources), IDs: func() (domain.ID, error) {
		n++
		return domain.ID(fmt.Sprintf("0190c000-0000-7000-8000-%012d", n)), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, reg, aud
}

var (
	adminP = domain.Principal{UserID: "u-admin", Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
	attyP  = domain.Principal{UserID: "u-atty", Roles: []domain.Role{domain.RoleAttorney}, Source: domain.SourceUI}
	staffP = domain.Principal{UserID: "u-staff", Roles: []domain.Role{domain.RoleStaff}, Source: domain.SourceUI}
	acctP  = domain.Principal{UserID: "u-acct", Roles: []domain.Role{domain.RoleAccountant}, Source: domain.SourceUI}
	clntP  = domain.Principal{UserID: "u-client", Roles: []domain.Role{domain.RoleClient}, Source: domain.SourceUI}
)

func code(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

// AC-010 at the service: a named default restores exactly after a reset, the admin's
// role default stays offered, and the 11th saved layout is refused with nothing written.
func TestLayoutsSaveDefaultResetAndCap(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t, nil)
	roleWs := []LayoutWidget{{WidgetID: "upcoming-deadlines", X: 0, Y: 0, W: 6, H: 4}, {WidgetID: "trust-alerts", X: 6, Y: 0, W: 6, H: 3}}
	if _, err := svc.SetRoleLayout(ctx, attyP, "attorney", roleWs); code(err) != domain.CodeForbidden {
		t.Fatalf("attorney set a role layout: %v", err)
	}
	if _, err := svc.SetRoleLayout(ctx, adminP, "attorney", roleWs); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Working(ctx, attyP)
	if err != nil || !reflect.DeepEqual(got, roleWs) {
		t.Fatalf("a new attorney does not start from the role default: %v %v", got, err)
	}
	mine := []LayoutWidget{{WidgetID: "my-time", X: 0, Y: 0, W: 4, H: 3}, {WidgetID: "upcoming-deadlines", X: 4, Y: 0, W: 8, H: 5, Filters: map[string]string{"from_date": "2026-11-01", "to_date": "2026-11-30"}}}
	saved, err := svc.SaveLayout(ctx, attyP, "Trial prep", mine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetDefaultLayout(ctx, attyP, saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveLayout(ctx, attyP, "Billing day", []LayoutWidget{{WidgetID: "unbilled", X: 0, Y: 0, W: 12, H: 3}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PutWorking(ctx, attyP, []LayoutWidget{{WidgetID: "running-timer", X: 3, Y: 2, W: 2, H: 2}}); err != nil {
		t.Fatal(err)
	}
	opts, err := svc.ResetOptions(ctx, attyP)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 3 || opts[0].Name != "Trial prep" || !opts[0].Default || opts[1].Name != "Billing day" || opts[2].ID != RoleOptionID {
		t.Fatalf("reset options are not default first, then others, then role: %+v", opts)
	}
	if _, err := svc.ApplyLayout(ctx, attyP, opts[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Working(ctx, attyP); !reflect.DeepEqual(got, mine) {
		t.Fatalf("restore is not exact:\n got %+v\nwant %+v", got, mine)
	}
	if _, err := svc.ApplyLayout(ctx, attyP, RoleOptionID); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Working(ctx, attyP); !reflect.DeepEqual(got, roleWs) {
		t.Fatalf("role default does not restore: %+v", got)
	}
	for i := 3; i <= 10; i++ {
		if _, err := svc.SaveLayout(ctx, attyP, fmt.Sprintf("Layout %d", i), mine); err != nil {
			t.Fatalf("layout %d: %v", i, err)
		}
	}
	_, err = svc.SaveLayout(ctx, attyP, "Eleventh", mine)
	if code(err) != CodeLayoutLimit {
		t.Fatalf("11th layout: want layout_limit, got %v", err)
	}
	if ls, _ := svc.Layouts(ctx, attyP); len(ls) != 10 {
		t.Fatalf("refused save wrote a layout: %d saved", len(ls))
	}
	// Deleting one frees a place; duplicate names are refused.
	if err := svc.DeleteLayout(ctx, attyP, opts[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveLayout(ctx, attyP, "trial PREP", mine); code(err) != domain.CodeConflict {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := svc.SaveLayout(ctx, attyP, "Eleventh", mine); err != nil {
		t.Fatalf("freed place not reusable: %v", err)
	}
	// Another user's layout is not reachable.
	if _, err := svc.ApplyLayout(ctx, staffP, saved.ID); code(err) != domain.CodeNotFound {
		t.Fatalf("staff applied the attorney's layout: %v", err)
	}
}

func TestLayoutCapFollowsTheSetting(t *testing.T) {
	ctx := context.Background()
	svc, reg, _ := newTestService(t, nil)
	if _, err := reg.Set(ctx, "home.max_saved_layouts", "2", adminP); err != nil {
		t.Fatal(err)
	}
	ws := []LayoutWidget{{WidgetID: "my-time", X: 0, Y: 0, W: 4, H: 3}}
	for _, n := range []string{"a", "b"} {
		if _, err := svc.SaveLayout(ctx, staffP, n, ws); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SaveLayout(ctx, staffP, "c", ws); code(err) != CodeLayoutLimit {
		t.Fatalf("cap of 2 not applied on the next request: %v", err)
	}
}

func TestConcurrentSavesNeverPassTheCap(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t, nil)
	ws := []LayoutWidget{{WidgetID: "my-time", X: 0, Y: 0, W: 4, H: 3}}
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = svc.SaveLayout(ctx, staffP, fmt.Sprintf("L%d", i), ws)
		}(i)
	}
	wg.Wait()
	if ls, _ := svc.Layouts(ctx, staffP); len(ls) != 10 {
		t.Fatalf("concurrent saves stored %d layouts", len(ls))
	}
}

func TestLayoutValidationAndDroppedWidgets(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t, nil)
	for _, bad := range [][]LayoutWidget{
		{{WidgetID: "nope", X: 0, Y: 0, W: 4, H: 3}},
		{{WidgetID: "my-time", X: 10, Y: 0, W: 4, H: 3}},
		{{WidgetID: "my-time", X: 0, Y: -1, W: 4, H: 3}},
		{{WidgetID: "module-alerts", X: 0, Y: 0, W: 4, H: 3}}, // admin-only widget for staff
	} {
		if _, err := svc.PutWorking(ctx, staffP, bad); code(err) != domain.CodeValidation {
			t.Fatalf("accepted %+v: %v", bad, err)
		}
	}
	// A widget whose module was removed drops out on load; the others keep their place.
	cat := svc.Catalog()
	if err := cat.AddModule("matter-clock", []ManifestWidget{{ID: "clock", Title: "Matter clock", Target: "/modules/matter-clock/tab"}}, nil); err != nil {
		t.Fatal(err)
	}
	ws := []LayoutWidget{{WidgetID: "my-time", X: 0, Y: 0, W: 4, H: 3}, {WidgetID: "matter-clock.clock", X: 4, Y: 0, W: 4, H: 3}, {WidgetID: "unbilled", X: 8, Y: 0, W: 4, H: 3}}
	if _, err := svc.PutWorking(ctx, staffP, ws); err != nil {
		t.Fatal(err)
	}
	cat.RemoveModule("matter-clock")
	got, _ := svc.Working(ctx, staffP)
	want := []LayoutWidget{ws[0], ws[2]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("removed module widget handling: %+v", got)
	}
	// A role change drops the widgets the new role can't see.
	asAcct := domain.Principal{UserID: staffP.UserID, Roles: []domain.Role{domain.RoleAccountant}}
	got, _ = svc.Working(ctx, asAcct)
	if len(got) != 1 || got[0].WidgetID != "unbilled" || got[0].X != 8 {
		t.Fatalf("role change: %+v", got)
	}
	if _, err := svc.Working(ctx, clntP); code(err) != domain.CodeForbidden {
		t.Fatalf("client reached the home screen: %v", err)
	}
}

// AC-011 at install: a module widget with no click-through target is refused.
func TestManifestWidgetsNeedAClickThroughTarget(t *testing.T) {
	for _, tc := range []struct {
		target string
		ok     bool
	}{{"/modules/matter-clock/tab?view=today", true}, {"", false}, {"   ", false}, {"https://evil.example/x", false}, {"//evil.example", false}, {"relative/path", false}} {
		err := ValidateManifestWidgets("matter-clock", []ManifestWidget{{ID: "clock", Title: "Clock", Target: tc.target}})
		if (err == nil) != tc.ok {
			t.Fatalf("target %q: err %v", tc.target, err)
		}
	}
	if err := ValidateManifestWidgets("Bad Slug", []ManifestWidget{{ID: "a", Title: "A", Target: "/x"}}); err == nil {
		t.Fatal("bad module id accepted")
	}
	c := NewCatalog(nil)
	if err := c.AddModule("matter-clock", []ManifestWidget{{ID: "ok", Title: "OK", Target: "/m/ok"}, {ID: "bad", Title: "Bad"}}, nil); err == nil {
		t.Fatal("a manifest with one target-less widget installed")
	}
	if _, ok := c.Def("matter-clock.ok"); ok {
		t.Fatal("a refused manifest left a widget in the picker")
	}
	if err := c.AddModule("matter-clock", []ManifestWidget{{ID: "ok", Title: "OK", Target: "/m/ok?tab=1"}}, func(id string) Source {
		return func(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
			return Widget{Title: "OK", Target: Target{Path: "/m/ok", Query: map[string]string{"tab": "1", "matter_id": f["matter_id"]}}}, nil
		}
	}); err != nil {
		t.Fatal(err)
	}
	w, err := c.Load(context.Background(), staffP, "matter-clock.ok", map[string]string{"matter_id": "m1"})
	if err != nil || w.Target.URL() != "/m/ok?matter_id=m1&tab=1" {
		t.Fatalf("module widget target: %v %+v", err, w)
	}
}

func TestWidgetLoadRespectsRolesAndPassesFilters(t *testing.T) {
	var seen map[string]string
	src := map[string]Source{"upcoming-deadlines": func(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
		seen = f
		return Widget{Title: "Upcoming deadlines", Target: Target{Path: "/calendar/deadlines", Query: map[string]string{"from_date": f["from_date"], "to_date": f["to_date"]}},
			Rows: []WidgetRow{{ID: "d1", Title: "Answer due", TargetPath: "/calendar/deadlines/d1"}}}, nil
	}}
	svc, _, _ := newTestService(t, src)
	ctx := context.Background()
	w, err := svc.LoadWidget(ctx, staffP, "upcoming-deadlines", map[string]string{"from_date": "2026-11-01", "to_date": "2026-11-08"})
	if err != nil {
		t.Fatal(err)
	}
	if seen["from_date"] != "2026-11-01" || w.Target.URL() != "/calendar/deadlines?from_date=2026-11-01&to_date=2026-11-08" || w.Rows[0].TargetPath != "/calendar/deadlines/d1" {
		t.Fatalf("click-through lost the range: %+v", w)
	}
	if _, err := svc.LoadWidget(ctx, acctP, "upcoming-deadlines", nil); code(err) != domain.CodeForbidden {
		t.Fatalf("accountant loaded the deadlines widget: %v", err)
	}
	if _, err := svc.LoadWidget(ctx, staffP, "todays-calendar", nil); !IsUnavailable(err) {
		t.Fatalf("missing source: %v", err)
	}
}

// AC-019 at the service: entries follow role and modules; sections with nothing visible
// hide; badges count; collapse state is stored with the user.
func TestNavEntriesBadgesAndCollapse(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t, nil)
	svc.d.Badges = map[string]func(context.Context, domain.Principal) (int, error){
		"matters": func(_ context.Context, p domain.Principal) (int, error) {
			if p.UserID == staffP.UserID {
				return 2, nil
			}
			return 3, nil
		},
	}
	ids := func(nc NavContext) string {
		var out []string
		for _, e := range nc.Entries {
			out = append(out, e.ID)
		}
		return strings.Join(out, ",")
	}
	a, err := svc.Nav(ctx, adminP)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ids(a), "admin") || !a.IsAdmin || a.Badges["matters"] != 3 {
		t.Fatalf("admin nav: %s %+v", ids(a), a.Badges)
	}
	s, _ := svc.Nav(ctx, staffP)
	if strings.Contains(ids(s), "admin") || s.Badges["matters"] != 2 {
		t.Fatalf("staff nav: %s %+v", ids(s), s.Badges)
	}
	acct, _ := svc.Nav(ctx, acctP)
	for _, hidden := range []string{"matters", "contacts", "notes", "time", "activity", "admin", "calendar"} {
		for _, e := range acct.Entries {
			if e.ID == hidden {
				t.Fatalf("accountant sees %s", hidden)
			}
		}
	}
	for _, g := range acct.Groups {
		if g.ID == SectionCommunications || g.ID == SectionActivity {
			t.Fatalf("accountant sees empty section %s", g.ID)
		}
	}
	if !strings.Contains(ids(acct), "invoices") || !strings.Contains(ids(acct), "trust-ledger") {
		t.Fatalf("accountant nav lacks money entries: %s", ids(acct))
	}
	if err := svc.AddModuleNav("messages", []ModuleNavEntry{{ID: "inbox", Section: SectionCommunications, Label: "Messages", Path: "/modules/messages", Tone: "warning"}},
		func(_ context.Context, p domain.Principal, id string) (int, bool, error) { return 4, true, nil }); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddModuleNav("bad", []ModuleNavEntry{{ID: "x", Section: "nowhere", Label: "X", Path: "/x"}}, nil); err == nil {
		t.Fatal("unknown section accepted")
	}
	s, _ = svc.Nav(ctx, staffP)
	if !strings.Contains(ids(s), "messages.inbox") || s.Badges["module:inbox"] != 4 {
		t.Fatalf("module entry: %s %+v", ids(s), s.Badges)
	}
	svc.RemoveModuleNav("messages")
	if s, _ = svc.Nav(ctx, staffP); strings.Contains(ids(s), "messages.inbox") {
		t.Fatal("removed module still in the sidebar")
	}
	if _, err := svc.ToggleSubgroup(ctx, staffP, "money.trust", true); err != nil {
		t.Fatal(err)
	}
	s, _ = svc.Nav(ctx, staffP)
	collapsed := false
	for _, g := range s.Groups {
		for _, sg := range g.Subgroups {
			if sg.ID == "money.trust" {
				collapsed = sg.Collapsed
			}
		}
	}
	if !collapsed || len(s.Subgroups) != 1 {
		t.Fatalf("subgroup collapse not kept: %+v", s.Subgroups)
	}
	if _, err := svc.PutSidebar(ctx, staffP, SidebarState{Collapsed: true, Subgroups: []SubgroupState{{ID: "a"}, {ID: "a"}}}); code(err) != domain.CodeValidation {
		t.Fatalf("duplicate subgroup accepted: %v", err)
	}
	if _, err := svc.Nav(ctx, clntP); code(err) != domain.CodeForbidden {
		t.Fatalf("client nav: %v", err)
	}
}

func TestMySettingsAndNotificationPrefs(t *testing.T) {
	ctx := context.Background()
	svc, reg, _ := newTestService(t, nil)
	m, err := svc.MySettings(ctx, staffP)
	if err != nil || m.Mode != "firm" || !reflect.DeepEqual(m.DeadlineReminderDays, []int{7, 1}) || m.IdleTimeoutSeconds != 12*3600 {
		t.Fatalf("defaults: %+v %v", m, err)
	}
	long := 13 * 3600
	if _, err := svc.UpdateMySettings(ctx, staffP, MeSettingsPatch{IdleTimeoutSeconds: &long}); code(err) != domain.CodeValidation {
		t.Fatalf("longer idle than the firm accepted: %v", err)
	}
	short, mode, zone := 1800, "dark", "America/Chicago"
	days := []int{3}
	if m, err = svc.UpdateMySettings(ctx, staffP, MeSettingsPatch{IdleTimeoutSeconds: &short, Mode: &mode, TimeZone: &zone, DeadlineReminderDays: &days}); err != nil {
		t.Fatal(err)
	}
	if m.IdleTimeoutSeconds != 1800 || m.Mode != "dark" || m.TimeZone != zone || !reflect.DeepEqual(m.DeadlineReminderDays, days) {
		t.Fatalf("patch: %+v", m)
	}
	bad := "Mars/Base"
	if _, err := svc.UpdateMySettings(ctx, staffP, MeSettingsPatch{TimeZone: &bad}); code(err) != domain.CodeValidation {
		t.Fatalf("bad zone: %v", err)
	}
	// A firm that lowers its idle limit caps a user's longer choice.
	if _, err := reg.Set(ctx, "security.idle_timeout", "10m", adminP); err != nil {
		t.Fatal(err)
	}
	if m, _ = svc.MySettings(ctx, staffP); m.IdleTimeoutSeconds != 600 {
		t.Fatalf("firm cap not applied: %d", m.IdleTimeoutSeconds)
	}
	prefs, err := svc.NotificationPrefs(ctx, staffP)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]bool{"trust_flag": {true, true}, "deadline_reminder": {true, true}, "replenishment": {true, false}, "matter_assignment": {true, false}, "update_failure": {true, true}, "module_alert": {true, true}}
	for _, p := range prefs {
		if w := want[p.Kind]; p.InApp != w[0] || p.Email != w[1] {
			t.Fatalf("default %s: %+v", p.Kind, p)
		}
		if p.EmailRequired != (p.Kind == "trust_flag") {
			t.Fatalf("locked email: %+v", p)
		}
	}
	if _, err := svc.PutNotificationPrefs(ctx, staffP, []NotificationPref{{Kind: "trust_flag", InApp: true, Email: false}}); code(err) != domain.CodeValidation {
		t.Fatalf("turned off the trust flag email: %v", err)
	}
	if _, err := svc.PutNotificationPrefs(ctx, staffP, []NotificationPref{{Kind: "deadline_reminder", InApp: false, Email: false}}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := svc.Wants(ctx, staffP.UserID, "deadline_reminder", "email"); ok {
		t.Fatal("deadline email still wanted")
	}
	if ok, _ := svc.Wants(ctx, staffP.UserID, "trust_flag", "email"); !ok {
		t.Fatal("trust flag email not wanted")
	}
}

// AC-028 at the service: an appearance change is audited and is what the next load reads.
func TestAppearanceIsAuditedAndLive(t *testing.T) {
	ctx := context.Background()
	svc, _, aud := newTestService(t, nil)
	if _, err := svc.SetAppearance(ctx, staffP, Appearance{Preset: "forest_cream"}); code(err) != domain.CodeForbidden {
		t.Fatalf("staff changed appearance: %v", err)
	}
	if th := svc.ThemeFor(ctx, staffP); th.Preset != "navy_gold" || th.EffectiveMode != "device" {
		t.Fatalf("default theme: %+v", th)
	}
	if _, err := svc.SetAppearance(ctx, adminP, Appearance{Preset: "charcoal_copper", HeadingFont: "Lora", TextSize: "lg", Mode: "dark"}); err != nil {
		t.Fatal(err)
	}
	th := svc.ThemeFor(ctx, staffP)
	if th.StaffSidebar != "#2b2b2b" || th.HeadingFont != "Lora" || th.EffectiveSize != "lg" || th.EffectiveMode != "dark" {
		t.Fatalf("next load does not see the change: %+v", th)
	}
	found := false
	for _, c := range aud.changes {
		if c.Key == "appearance.preset" && c.Old == "" && c.New == "Charcoal and copper" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no audited old/new for the preset: %+v", aud.changes)
	}
	if _, err := svc.SetAppearance(ctx, adminP, Appearance{Preset: "custom", StaffSidebar: "#123456", StaffAccent: "#abcdef"}); err != nil {
		t.Fatal(err)
	}
	if th := svc.ThemeFor(ctx, staffP); th.StaffSidebar != "#123456" || th.StaffAccent != "#abcdef" {
		t.Fatalf("custom colors: %+v", th)
	}
	if _, err := svc.SetAppearance(ctx, adminP, Appearance{StaffSidebar: "red;}"}); code(err) != domain.CodeValidation {
		t.Fatalf("css injection accepted: %v", err)
	}
	light := "light"
	if _, err := svc.UpdateMySettings(ctx, staffP, MeSettingsPatch{Mode: &light}); err != nil {
		t.Fatal(err)
	}
	if th := svc.ThemeFor(ctx, staffP); th.EffectiveMode != "light" {
		t.Fatalf("user mode not applied: %+v", th)
	}
}

type fakeMail struct {
	to   []string
	fail bool
}

func (m *fakeMail) Send(_ context.Context, to, subject, body string) error {
	if m.fail {
		return errors.New("refused")
	}
	m.to = append(m.to, to)
	return nil
}

func TestProblemReportsSaveAndMailTheAdmins(t *testing.T) {
	ctx := context.Background()
	svc, reg, _ := newTestService(t, nil)
	mail := &fakeMail{}
	svc.d.Mailer = mail
	svc.d.Staff = func(context.Context) ([]domain.User, error) {
		return []domain.User{
			{Email: "admin@firm.example", Roles: `["admin"]`, Active: true},
			{Email: "old@firm.example", Roles: `["admin"]`, Active: false},
			{Email: "staff@firm.example", Roles: `["staff"]`, Active: true},
		}, nil
	}
	r, mailErr, err := svc.ReportProblem(ctx, staffP, "/billing/time", "The timer froze.")
	if err != nil || mailErr != nil {
		t.Fatal(err, mailErr)
	}
	if !reflect.DeepEqual(mail.to, []string{"admin@firm.example"}) {
		t.Fatalf("recipients: %v", mail.to)
	}
	if _, _, err := svc.ReportProblem(ctx, staffP, "/", "  "); code(err) != domain.CodeValidation {
		t.Fatalf("empty report: %v", err)
	}
	if _, err := reg.Set(ctx, "mail.problem_recipients", "admins, it@firm.example", adminP); err != nil {
		t.Fatal(err)
	}
	mail.fail = true
	if _, mailErr, err := svc.ReportProblem(ctx, staffP, "/", "Second"); err != nil || mailErr == nil {
		t.Fatalf("failed mail: %v %v", err, mailErr)
	}
	rows, err := svc.Problems(ctx, adminP)
	if err != nil || len(rows) != 2 || rows[1].ID != r.ID {
		t.Fatalf("reports kept: %+v %v", rows, err)
	}
	if _, err := svc.Problems(ctx, staffP); code(err) != domain.CodeForbidden {
		t.Fatalf("staff read problem reports: %v", err)
	}
	to, _ := svc.ProblemRecipients(ctx)
	if !reflect.DeepEqual(to, []string{"admin@firm.example", "it@firm.example"}) {
		t.Fatalf("recipients: %v", to)
	}
}

func TestSafeLinksAndTargets(t *testing.T) {
	for in, want := range map[string]string{"/trust/flags/1": "/trust/flags/1", "//evil": "/", "https://evil": "/", "/\\evil": "/", "": "/"} {
		if got := safeLink(in); got != want {
			t.Fatalf("safeLink(%q) = %q", in, got)
		}
	}
	if got := (Target{Path: "/calendar/deadlines", Query: map[string]string{"to_date": "b", "from_date": "a"}}).URL(); got != "/calendar/deadlines?from_date=a&to_date=b" {
		t.Fatal(got)
	}
	_ = time.Now
}
