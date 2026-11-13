package identity

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// newDefaultBox uses the shipped settings: two-factor is optional for every role.
func newDefaultBox(t *testing.T) box {
	t.Helper()
	clk := &clock{t: time.Date(2026, 11, 13, 16, 0, 0, 0, time.UTC)}
	log := audit.NewMemory()
	aud, err := audit.New(log, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(settings.NewMemory(), aud)
	if err != nil {
		t.Fatal(err)
	}
	mem := NewMemory()
	svc, err := New(Deps{
		Users: mem.Users(), Sessions: mem.Sessions(), Passwords: mem.Passwords(),
		TOTP: mem.TOTP(), Recovery: mem.Recovery(), Tokens: mem.Tokens(),
		Audit: aud, Settings: reg, Key: bytes.Repeat([]byte{7}, 32), Now: clk.Now, Mail: &mailer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return box{svc: svc, mem: mem, log: log, clk: clk, reg: reg}
}

func TestDefaultsSignInWithPasswordOnly(t *testing.T) {
	ctx := context.Background()
	b := newDefaultBox(t)
	enroll, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !enroll.Optional || enroll.OTPAuthURI == "" || enroll.Secret == "" {
		t.Fatalf("setup must offer an optional enrollment: %+v", enroll)
	}
	ready, err := b.svc.Ready(ctx)
	if err != nil || !ready {
		t.Fatal("setup is complete without two-factor", ready, err)
	}
	if _, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "Other", TimeZone: "UTC", AdminName: "Bob", AdminEmail: "bob@example.test",
		Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}); codeOf(err) != "already_set_up" {
		t.Fatal(err)
	}
	// The admin skipped two-factor: email and password open a session.
	login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || login.Status != statusAuthenticated || login.SessionToken == "" {
		t.Fatal(login, err)
	}
	admin, err := b.svc.Authenticate(ctx, Presented{SessionToken: login.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []domain.Role{domain.RoleAttorney, domain.RoleStaff, domain.RoleAccountant, domain.RoleClient} {
		email := string(role) + "@example.test"
		if _, err := b.svc.CreateUser(ctx, admin, NewUser{
			Name: "User " + string(role), Email: email, Password: "correct-horse-battery", Roles: []domain.Role{role},
		}); err != nil {
			t.Fatal(err)
		}
		got, err := b.svc.Login(ctx, email, "correct-horse-battery")
		if err != nil || got.Status != statusAuthenticated || got.SessionToken == "" {
			t.Fatalf("%s: %+v %v", role, got, err)
		}
	}
}

func TestAdminCanRequireAdminTOTP(t *testing.T) {
	ctx := context.Background()
	b := newDefaultBox(t)
	if _, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}); err != nil {
		t.Fatal(err)
	}
	login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := b.svc.Authenticate(ctx, Presented{SessionToken: login.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.reg.Set(ctx, "security.totp_required.admin", "true", admin.Principal); err != nil {
		t.Fatal("the admin requirement is a normal setting now:", err)
	}
	pending, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || pending.Status != statusEnroll || pending.SessionToken != "" {
		t.Fatal(pending, err)
	}
	enroll, err := b.svc.StartEnrollment(ctx, pending.EnrollmentToken)
	if err != nil || enroll.Optional {
		t.Fatal("a required enrollment is not optional", enroll, err)
	}
	if _, err := b.reg.Set(ctx, "security.totp_required.admin", "false", admin.Principal); err != nil {
		t.Fatal(err)
	}
	again, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || again.Status != statusAuthenticated {
		t.Fatal(again, err)
	}
}

func TestUserOptsIntoTOTP(t *testing.T) {
	ctx := context.Background()
	b := newDefaultBox(t)
	if _, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}); err != nil {
		t.Fatal(err)
	}
	login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	me, err := b.svc.Authenticate(ctx, Presented{SessionToken: login.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	start, err := b.svc.ResetTOTP(ctx, me, Factor{Password: "correct-horse-battery"})
	if err != nil || start.OTPAuthURI == "" {
		t.Fatal(start, err)
	}
	if err := b.svc.ConfirmReset(ctx, me, start.EnrollmentToken, TOTPCode(decodeSecret(t, start.Secret), b.clk.Now())); err != nil {
		t.Fatal(err)
	}
	next, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || next.Status != statusTOTP || next.SessionToken != "" {
		t.Fatal("a user who turned two-factor on is asked for a code", next, err)
	}
}
