package dashboard

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

type fakeUsers map[domain.ID]domain.User

func (f fakeUsers) Get(_ context.Context, id domain.ID) (domain.User, error) {
	u, ok := f[id]
	if !ok {
		return domain.User{}, repository.ErrNotFound
	}
	return u, nil
}

func (f fakeUsers) Update(_ context.Context, u domain.User) error { f[u.ID] = u; return nil }

type fakeRecorder struct{ rows []audit.Change }

func (r *fakeRecorder) Record(_ context.Context, _ domain.Principal, c audit.Change) (domain.AuditEntry, error) {
	r.rows = append(r.rows, c)
	return domain.AuditEntry{}, nil
}

func (r *fakeRecorder) Atomic(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type fakeTokens struct {
	IdentityAPI
	views []identity.TokenView
}

func (f fakeTokens) ListTokens(context.Context, identity.Caller) ([]identity.TokenView, error) {
	return f.views, nil
}

func (f fakeTokens) GetToken(_ context.Context, _ identity.Caller, id domain.ID) (identity.TokenView, error) {
	for _, v := range f.views {
		if v.ID == id {
			return v, nil
		}
	}
	return identity.TokenView{}, domain.NotFound("api token")
}

func (f fakeTokens) RevokeToken(context.Context, identity.Caller, domain.ID) error { return nil }

func accountsFixture(t *testing.T) (*Accounts, fakeUsers, *fakeRecorder) {
	t.Helper()
	users := fakeUsers{}
	for id, roles := range map[domain.ID]string{"adm": `["admin"]`, "att": `["attorney"]`, "stf": `["staff"]`} {
		users[id] = domain.User{Meta: domain.Meta{ID: id}, Name: string(id), Email: string(id) + "@firm.example", Roles: roles, Active: true}
	}
	rec := &fakeRecorder{}
	a := &Accounts{Users: users, Audit: rec, Profiles: NewMemory(), List: func(context.Context) ([]domain.User, error) {
		var out []domain.User
		for _, u := range users {
			out = append(out, u)
		}
		return out, nil
	}}
	return a, users, rec
}

var (
	pAdmin = domain.Principal{UserID: "adm", Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
	pStaff = domain.Principal{UserID: "stf", Roles: []domain.Role{domain.RoleStaff}, Source: domain.SourceUI}
)

func TestUpdateUserRulesAndAudit(t *testing.T) {
	ctx := context.Background()
	a, users, rec := accountsFixture(t)
	phone, rate := "314-555-0100", int64(25050)
	if _, err := a.UpdateUser(ctx, pStaff, "att", UserInput{Phone: &phone}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("staff edited a user: %v", err)
	}
	if _, err := a.UpdateUser(ctx, pAdmin, "att", UserInput{Phone: &phone, RateCents: &rate}); err != nil {
		t.Fatal(err)
	}
	// Regression: the phone was stored before it was set (Codex round 4 work).
	if pr, ok, _ := a.Profiles.Profile(ctx, "att"); !ok || pr.Phone != phone {
		t.Fatalf("phone not stored: %+v", pr)
	}
	if users["att"].DefaultHourlyRateCents != 25050 || len(rec.rows) != 2 {
		t.Fatalf("rate %d, audit rows %+v", users["att"].DefaultHourlyRateCents, rec.rows)
	}
	bar := "12345"
	if _, err := a.UpdateUser(ctx, pAdmin, "stf", UserInput{BarNumber: &bar}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("bar number on a staff user: %v", err)
	}
	staffOnly := []domain.Role{domain.RoleStaff}
	if _, err := a.UpdateUser(ctx, pAdmin, "adm", UserInput{Roles: &staffOnly}); !isCode(err, domain.CodeConflict) {
		t.Fatalf("the last admin lost the role: %v", err)
	}
	if err := a.DeactivateUser(ctx, pAdmin, "adm"); !isCode(err, domain.CodeValidation) {
		t.Fatalf("admin deactivated themselves: %v", err)
	}
	if err := a.DeactivateUser(ctx, pAdmin, "stf"); err != nil || users["stf"].Active {
		t.Fatalf("deactivate: %v", err)
	}
	if err := a.DeactivateUser(ctx, pAdmin, "nobody"); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestAccountFormParsing(t *testing.T) {
	for in, want := range map[string]int64{"250": 25000, "250.5": 25050, "$250.05": 25005, "0": 0} {
		if got, err := parseDollars(in); err != nil || got != want {
			t.Errorf("parseDollars(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "-1", "2.555", "abc", "1.", "."} {
		if _, err := parseDollars(in); err == nil {
			t.Errorf("parseDollars(%q) accepted", in)
		}
	}
	if dollars(25050) != "250.50" || dollars(25005) != "250.05" || dollars(30000) != "300.00" {
		t.Error("dollars")
	}
	if _, err := ParseRoleList([]string{"partner"}); err == nil {
		t.Error("unknown role accepted")
	}
	if _, err := ParseRoleList(nil); err == nil {
		t.Error("no roles accepted")
	}
	if checkPhone("<script>") == nil || checkPhone("+1 (314) 555-0101 x2") != nil || checkInitials("A1") == nil || checkInitials("ABCD") == nil {
		t.Error("phone or initials rule")
	}
}

func TestPictureIsSniffedAndCapped(t *testing.T) {
	ctx := context.Background()
	a, _, _ := accountsFixture(t)
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte("<svg/>"), []byte("GIF89a......"), make([]byte, fixed.PictureMaxBytes+1)} {
		if err := a.SetPicture(ctx, pStaff, bad); !isCode(err, domain.CodeValidation) {
			t.Fatalf("picture accepted: %v", err)
		}
	}
	if err := a.SetPicture(ctx, pStaff, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, ct, err := a.Picture(ctx, pAdmin, "stf"); err != nil || ct != "image/png" {
		t.Fatalf("picture: %q %v", ct, err)
	}
	client := domain.Principal{UserID: "cli", Roles: []domain.Role{domain.RoleClient}, Source: domain.SourceUI}
	if _, _, err := a.Picture(ctx, client, "stf"); err == nil {
		t.Fatal("a client user read a staff picture")
	}
}

func TestMyTokensAreOwnOnly(t *testing.T) {
	ctx := context.Background()
	a, _, _ := accountsFixture(t)
	a.Identity = fakeTokens{views: []identity.TokenView{{ID: "t1", UserID: "adm", Name: "mine"}, {ID: "t2", UserID: "stf", Name: "theirs"}}}
	mine, err := a.MyTokens(ctx, pAdmin)
	if err != nil || len(mine) != 1 || mine[0].Name != "mine" {
		t.Fatalf("admin's own list: %+v %v", mine, err)
	}
	if err := a.RevokeToken(ctx, pAdmin, "t2", true); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("revoked another user's token from My profile: %v", err)
	}
	if err := a.RevokeToken(ctx, pStaff, "t2", false); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("staff used the admin revoke: %v", err)
	}
	if err := a.RevokeToken(ctx, pAdmin, "t2", false); err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
}

// gatedUsers is fakeUsers behind a lock, with a hook on the first read of one user.
type gatedUsers struct {
	mu    sync.Mutex
	m     map[domain.ID]domain.User
	hook  domain.ID
	onGet func()
}

func (g *gatedUsers) Get(_ context.Context, id domain.ID) (domain.User, error) {
	g.mu.Lock()
	u, ok := g.m[id]
	fire := g.onGet
	if id == g.hook && fire != nil {
		g.onGet = nil
	} else {
		fire = nil
	}
	g.mu.Unlock()
	if fire != nil {
		fire()
	}
	if !ok {
		return domain.User{}, repository.ErrNotFound
	}
	return u, nil
}

func (g *gatedUsers) Update(_ context.Context, u domain.User) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.m[u.ID] = u
	return nil
}

func (g *gatedUsers) get(id domain.ID) domain.User {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.m[id]
}

type nopRecorder struct{}

func (r *nopRecorder) Record(context.Context, domain.Principal, audit.Change) (domain.AuditEntry, error) {
	return domain.AuditEntry{}, nil
}

func (r *nopRecorder) Atomic(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// Codex PR #35 round 6: UpdateMe read the users row outside the lock the admin writes
// take, then wrote it back whole, undoing a deactivation that landed in between. The
// hook starts the deactivation right after UpdateMe's read and gives it time to finish;
// with the lock it waits, then applies to the row UpdateMe wrote.
func TestUpdateMeKeepsConcurrentDeactivation(t *testing.T) {
	ctx := context.Background()
	g := &gatedUsers{m: map[domain.ID]domain.User{
		"adm": {Meta: domain.Meta{ID: "adm"}, Name: "adm", Roles: `["admin"]`, Active: true},
		"att": {Meta: domain.Meta{ID: "att"}, Name: "Ada", Roles: `["attorney"]`, Active: true},
	}}
	a := &Accounts{Users: g, Audit: &nopRecorder{}, Profiles: NewMemory(), List: func(context.Context) ([]domain.User, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		var out []domain.User
		for _, u := range g.m {
			out = append(out, u)
		}
		return out, nil
	}}
	done := make(chan error, 1)
	g.hook = "att"
	g.onGet = func() {
		go func() { done <- a.DeactivateUser(ctx, pAdmin, "att") }()
		select {
		case err := <-done:
			done <- err
		case <-time.After(150 * time.Millisecond):
		}
	}
	self := domain.Principal{UserID: "att", Roles: []domain.Role{domain.RoleAttorney}, Source: domain.SourceUI}
	name := "Ada Renamed"
	if _, err := a.UpdateMe(ctx, self, &name, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if u := g.get("att"); u.Active || u.Name != "Ada Renamed" {
		t.Fatalf("profile save and deactivation: active=%v name=%q", u.Active, u.Name)
	}
}

type countingIdentity struct {
	IdentityAPI
	calls int
}

func (c *countingIdentity) CreateUser(context.Context, identity.Caller, identity.NewUser) (domain.User, error) {
	c.calls++
	return domain.User{}, nil
}

// Codex PR #35 round 6: the add form's phone is checked before identity creates the user.
func TestCreateUserChecksPhoneFirst(t *testing.T) {
	a, _, _ := accountsFixture(t)
	id := &countingIdentity{}
	a.Identity = id
	name, email, phone := "Olive", "olive@firm.example", "call me"
	roles := []domain.Role{domain.RoleStaff}
	_, err := a.CreateUser(context.Background(), pAdmin, UserInput{Name: &name, Email: &email, Roles: &roles, Password: "synthetic-password-1", Phone: &phone})
	if !isCode(err, domain.CodeValidation) || id.calls != 0 {
		t.Fatalf("bad phone on add: err=%v identity calls=%d", err, id.calls)
	}
}
