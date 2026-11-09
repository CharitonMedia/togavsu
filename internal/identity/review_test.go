package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func TestTOTPFailuresLockAcrossLogins(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, _ = b.enrollAdmin(t)
	for i := 0; i < 5; i++ {
		login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
		if err != nil || login.TOTPToken == "" {
			t.Fatalf("login %d: %+v %v", i, login, err)
		}
		_, err = b.svc.VerifyTOTP(ctx, login.TOTPToken, "000000")
		if i < 4 && codeOf(err) != domain.CodeUnauthenticated {
			t.Fatalf("guess %d: %v", i, err)
		}
		if i == 4 && codeOf(err) != "locked_out" {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	if _, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery"); codeOf(err) != "locked_out" {
		t.Fatal(err)
	}
}

func TestUnknownEmailLockoutMatchesKnownAccount(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, _ = b.enrollAdmin(t)
	for i := 0; i < 5; i++ {
		_, err := b.svc.Login(ctx, "nobody@example.test", "correct-horse-battery")
		if i < 4 && codeOf(err) != domain.CodeUnauthenticated {
			t.Fatalf("unknown %d: %v", i, err)
		}
		if i == 4 && codeOf(err) != "locked_out" {
			t.Fatalf("unknown %d: %v", i, err)
		}
	}
	if _, err := b.svc.Login(ctx, "other@example.test", "correct-horse-battery"); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	dump := auditText(t, b.log)
	if strings.Contains(dump, "nobody@example.test") {
		t.Fatal("email leaked into the audit log")
	}
}

func TestTOTPCodeCannotBeReused(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	started, admin := b.enrollAdmin(t)
	_ = started
	b.clk.t = b.clk.t.Add(31 * time.Second)
	reset, err := b.svc.ResetTOTP(ctx, admin, Factor{Password: "correct-horse-battery"})
	if err != nil {
		t.Fatal(err)
	}
	secret := decodeSecret(t, reset.Secret)
	code := TOTPCode(secret, b.clk.Now())
	if err := b.svc.ConfirmReset(ctx, admin, reset.EnrollmentToken, code); err != nil {
		t.Fatal(err)
	}
	login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.VerifyTOTP(ctx, login.TOTPToken, code); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	b.clk.t = b.clk.t.Add(31 * time.Second)
	login, err = b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.VerifyTOTP(ctx, login.TOTPToken, TOTPCode(secret, b.clk.Now())); err != nil {
		t.Fatal(err)
	}
}

func TestStaffTOTPStaysRequiredWhenSettingIsOff(t *testing.T) {
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
	pending, err := b.svc.Login(ctx, "sam@example.test", "correct-horse-battery")
	if err != nil || pending.Status != statusEnroll || pending.SessionToken != "" {
		t.Fatal(pending, err)
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
	clientLogin, err := b.svc.Login(ctx, "cora@example.test", "correct-horse-battery")
	if err != nil || clientLogin.Status != statusEnroll {
		t.Fatal(clientLogin, err)
	}
}

func TestSecurityWritesAreAudited(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	started, admin := b.enrollAdmin(t)
	reset, err := b.svc.ResetTOTP(ctx, admin, Factor{Recovery: started.RecoveryCodes[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !auditHas(t, b.log, "recovery_code", "recovery.spent") {
		t.Fatal("spent recovery code was not audited")
	}
	secret := decodeSecret(t, reset.Secret)
	if err := b.svc.ConfirmReset(ctx, admin, reset.EnrollmentToken, TOTPCode(secret, b.clk.Now())); err != nil {
		t.Fatal(err)
	}
	if !auditHas(t, b.log, "ciphertext", "user.totp_reset_confirmed") {
		t.Fatal("confirmed reset was not audited")
	}
	dump := auditText(t, b.log)
	if strings.Contains(dump, reset.Secret) {
		t.Fatal("totp secret leaked")
	}
	other, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	b.clk.t = b.clk.t.Add(31 * time.Second)
	second, err := b.svc.VerifyTOTP(ctx, other.TOTPToken, TOTPCode(secret, b.clk.Now()))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := b.svc.Authenticate(ctx, Presented{SessionToken: second.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.svc.ChangePassword(ctx, caller, "correct-horse-battery", "correct-horse-updated"); err != nil {
		t.Fatal(err)
	}
	if !auditHas(t, b.log, "revoked_at", "session.ended") {
		t.Fatal("revoked session was not audited")
	}
}

func TestSetupRollbackIsAudited(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)}
	log := audit.NewMemory()
	aud, err := audit.New(log, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(settings.NewMemory(), failAudit{})
	if err != nil {
		t.Fatal(err)
	}
	mem := NewMemory()
	svc, err := New(Deps{
		Users: mem.Users(), Sessions: mem.Sessions(), Passwords: mem.Passwords(),
		TOTP: mem.TOTP(), Recovery: mem.Recovery(), Tokens: mem.Tokens(),
		Audit: aud, Settings: reg, Key: bytesRepeat(), Now: clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "UTC", AdminName: "Ada",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	})
	if err == nil {
		t.Fatal("expected setup to fail")
	}
	if !auditHas(t, log, "active", "setup rolled back") {
		t.Fatal("inactive user was not audited")
	}
}

type failAudit struct{}

func (failAudit) SettingChanged(context.Context, settings.Change) error {
	return errAuditDown
}

var errAuditDown = settings.ErrInvalid

func TestHarmlessFieldNamesStayVisible(t *testing.T) {
	ctx := context.Background()
	log := audit.NewMemory()
	svc, err := audit.New(log, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	who := domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAttorney}, Source: domain.SourceUI}
	for _, field := range []string{"reinstated_on", "secretary_id"} {
		row, err := svc.Record(ctx, who, audit.Change{
			Entity: "matter", EntityID: domain.SystemUserID, Field: field, Old: "old", New: "new",
		})
		if err != nil || row.OldValue != "old" || row.NewValue != "new" {
			t.Fatal(field, row, err)
		}
	}
	secret, err := svc.Record(ctx, who, audit.Change{
		Entity: "user", EntityID: domain.SystemUserID, Field: "password", Old: "old", New: "new",
	})
	if err != nil || secret.NewValue != audit.Redacted {
		t.Fatal(secret, err)
	}
}

func TestSettingHistoryUsesStableID(t *testing.T) {
	ctx := context.Background()
	log := audit.NewMemory()
	svc, err := audit.New(log, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	change := settings.Change{
		Key: "firm.name", Old: "", New: "North", Actor: domain.SystemUserID,
		Role: "admin", Source: domain.SourceUI,
	}
	if err := svc.SettingChanged(ctx, change); err != nil {
		t.Fatal(err)
	}
	change.Old, change.New = "North", "South"
	if err := svc.SettingChanged(ctx, change); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.ForEntity(ctx, "setting", audit.SettingEntityID("firm.name"))
	if err != nil || len(rows) != 2 || rows[1].NewValue != "South" {
		t.Fatal(rows, err)
	}
}

func auditHas(t *testing.T, log *audit.Memory, field, reason string) bool {
	t.Helper()
	page, err := log.List(ctxBackground(), repository.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Items {
		if row.Field == field && strings.Contains(row.Reason, reason) {
			return true
		}
	}
	return false
}

func bytesRepeat() []byte {
	return []byte{
		1, 2, 3, 4, 5, 6, 7, 8,
		1, 2, 3, 4, 5, 6, 7, 8,
		1, 2, 3, 4, 5, 6, 7, 8,
		1, 2, 3, 4, 5, 6, 7, 8,
	}
}
