package dashboard

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"

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
