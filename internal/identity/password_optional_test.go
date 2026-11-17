package identity

import (
	"context"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// security.password_optional lets a known, active account sign in with its email alone
// (Patrick's test installs, 2026-11-17). Off, an empty password is still refused.
func TestPasswordOptionalSetting(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, admin := b.enrollAdmin(t)
	if _, err := b.reg.Set(ctx, "security.totp_required.staff", "false", admin.Principal); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Sam Staff", Email: "sam@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleStaff},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.Login(ctx, "sam@example.test", ""); err == nil {
		t.Fatal("empty password signed in with the setting off")
	}
	if _, err := b.reg.Set(ctx, "security.password_optional", "true", admin.Principal); err != nil {
		t.Fatal(err)
	}
	for _, pw := range []string{"", "anything at all"} {
		res, err := b.svc.Login(ctx, "SAM@example.test", pw)
		if err != nil || res.Status != statusAuthenticated || res.SessionToken == "" {
			t.Fatalf("password %q: %+v %v", pw, res, err)
		}
	}
	if _, err := b.svc.Login(ctx, "nobody@example.test", ""); err == nil {
		t.Fatal("unknown email signed in")
	}
	if _, err := b.reg.Set(ctx, "security.totp_required.client", "true", admin.Principal); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Cora Client", Email: "cora@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleClient},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := b.svc.Login(ctx, "cora@example.test", "")
	if err != nil || res.Status != statusEnroll {
		t.Fatalf("two-factor step skipped: %+v %v", res, err)
	}
}
