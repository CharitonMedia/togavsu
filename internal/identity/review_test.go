package identity

import (
	"context"
	"reflect"
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

func TestLockoutLastsFromTheFailureThatReachedTheLimit(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, _ = b.enrollAdmin(t)
	gaps := []time.Duration{0, time.Minute, time.Minute, time.Minute, 11 * time.Minute}
	for i, gap := range gaps {
		b.clk.t = b.clk.t.Add(gap)
		_, err := b.svc.Login(ctx, "ada@example.test", "wrong-horse-battery")
		if i < 4 && codeOf(err) != domain.CodeUnauthenticated {
			t.Fatalf("guess %d: %v", i, err)
		}
		if i == 4 && codeOf(err) != "locked_out" {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	b.clk.t = b.clk.t.Add(2 * time.Minute)
	if _, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery"); codeOf(err) != "locked_out" {
		t.Fatal(err)
	}
	b.clk.t = b.clk.t.Add(14 * time.Minute)
	login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || login.TOTPToken == "" {
		t.Fatal(login, err)
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

func TestSetupRetryReusesRolledBackAdmin(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)}
	log := audit.NewMemory()
	aud, err := audit.New(log, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(settings.NewMemory(), &failOnceAudit{})
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
	in := SetupInput{
		FirmName: "North Firm", TimeZone: "UTC", AdminName: "Ada",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}
	if _, err := svc.Setup(ctx, in); err == nil {
		t.Fatal("expected the first setup to fail")
	}
	enroll, err := svc.Setup(ctx, in)
	if err != nil || enroll.Secret == "" || enroll.EnrollmentToken == "" {
		t.Fatal(enroll, err)
	}
	ready, err := svc.Ready(ctx)
	if err != nil || ready {
		t.Fatal(ready, err)
	}
}

type failOnceAudit struct{ n int }

func (f *failOnceAudit) SettingChanged(context.Context, settings.Change) error {
	f.n++
	if f.n == 1 {
		return settings.ErrInvalid
	}
	return nil
}

func TestSetupRetryAfterLaterSettingsWriteFails(t *testing.T) {
	ctx := context.Background()
	in := SetupInput{
		FirmName: "North Firm", TimeZone: "UTC", AdminName: "Ada",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}
	for _, failAt := range []int{2, 3} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			clk := &clock{t: time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)}
			log := audit.NewMemory()
			aud, err := audit.New(log, clk.Now)
			if err != nil {
				t.Fatal(err)
			}
			reg, err := settings.New(settings.NewMemory(), &failNthAudit{failAt: failAt})
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
			if _, err := svc.Setup(ctx, in); err == nil {
				t.Fatal("expected setup to fail")
			}
			name, err := reg.Get(ctx, "firm.name")
			if err != nil || name != in.FirmName {
				t.Fatal(name, err)
			}
			ready, err := svc.Ready(ctx)
			if err != nil || ready {
				t.Fatal(ready, err)
			}
			_, err = svc.Setup(ctx, SetupInput{
				FirmName: "Other Firm", TimeZone: "UTC", AdminName: "Eve",
				AdminEmail: "eve@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
			})
			if codeOf(err) != "already_set_up" {
				t.Fatal(err)
			}
			enroll, err := svc.Setup(ctx, in)
			if err != nil || enroll.Secret == "" || enroll.EnrollmentToken == "" {
				t.Fatal(enroll, err)
			}
			page, err := mem.Users().List(ctx, repository.Filter{})
			if err != nil || len(page.Items) != 1 || !page.Items[0].Active {
				t.Fatal(page, err)
			}
		})
	}
}

type failNthAudit struct {
	n      int
	failAt int
}

func (f *failNthAudit) SettingChanged(context.Context, settings.Change) error {
	f.n++
	if f.n == f.failAt {
		return settings.ErrInvalid
	}
	return nil
}

func TestNoCredentialRejectsDummyPassword(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	roles, err := domain.MarshalRoles([]domain.Role{domain.RoleClient})
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := b.clk.Now()
	user := domain.User{
		Meta: domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: id},
		Name: "Open", Email: "open@example.test", Roles: roles, Active: true,
	}
	if err := b.mem.Users().Insert(ctx, user); err != nil {
		t.Fatal(err)
	}
	login, err := b.svc.Login(ctx, "open@example.test", strings.Repeat("x", minPassword()))
	if codeOf(err) != domain.CodeUnauthenticated || login.SessionToken != "" || login.TOTPToken != "" || login.EnrollmentToken != "" {
		t.Fatal(login, err)
	}
}

func TestCreateUserRejectsRuneShortPassword(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, admin := b.enrollAdmin(t)
	_, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Eve", Email: "eve@example.test", Password: "éééééééé",
		Roles: []domain.Role{domain.RoleStaff},
	})
	if codeOf(err) != domain.CodeValidation {
		t.Fatal(err)
	}
	page, err := b.mem.Users().List(ctx, repository.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range page.Items {
		if user.Email == "eve@example.test" {
			t.Fatal(user)
		}
	}
}

func TestConfirmedAdminClosesSetup(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)}
	log := audit.NewMemory()
	aud, err := audit.New(log, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(settings.NewMemory(), &failOnceAudit{})
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
	first := SetupInput{
		FirmName: "North Firm", TimeZone: "UTC", AdminName: "Ada",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}
	if _, err := svc.Setup(ctx, first); err == nil {
		t.Fatal("expected the first setup to fail")
	}
	second := SetupInput{
		FirmName: "North Firm", TimeZone: "UTC", AdminName: "Bea",
		AdminEmail: "bea@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}
	enroll, err := svc.Setup(ctx, second)
	if err != nil || enroll.Secret == "" {
		t.Fatal(enroll, err)
	}
	if _, err := svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, TOTPCode(decodeSecret(t, enroll.Secret), clk.Now())); err != nil {
		t.Fatal(err)
	}
	page, err := mem.Users().List(ctx, repository.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range page.Items {
		if user.Email != "bea@example.test" {
			continue
		}
		user.Active = false
		if err := mem.Users().Update(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	_, err = svc.Setup(ctx, first)
	if codeOf(err) != "already_set_up" {
		t.Fatal(err)
	}
	name, err := reg.Get(ctx, "firm.name")
	if err != nil || name != "North Firm" {
		t.Fatal(name, err)
	}
	page, err = mem.Users().List(ctx, repository.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range page.Items {
		if user.Email == "ada@example.test" && user.Active {
			t.Fatal("rolled-back admin was reactivated")
		}
	}
}

func TestUnknownEmailsDoNotGrowLockMap(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	for i := 0; i < 40; i++ {
		email := strings.Repeat("u", i+1) + "@example.test"
		_, _ = b.svc.Login(ctx, email, strings.Repeat("x", minPassword()))
	}
	gates := reflect.ValueOf(b.svc).Elem().FieldByName("gates")
	if gates.IsValid() {
		t.Fatal("per-id mutex map is back")
	}
}

func TestSetupRefusedWhenFirmNameExists(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, admin := b.enrollAdmin(t)
	user, err := b.mem.Users().Get(ctx, admin.Principal.UserID)
	if err != nil {
		t.Fatal(err)
	}
	user.Active = false
	if err := b.mem.Users().Update(ctx, user); err != nil {
		t.Fatal(err)
	}
	_, err = b.svc.Setup(ctx, SetupInput{
		FirmName: "Other Firm", TimeZone: "UTC", AdminName: "Eve",
		AdminEmail: "eve@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	})
	if codeOf(err) != "already_set_up" {
		t.Fatal(err)
	}
	still, err := b.mem.Users().Get(ctx, user.ID)
	if err != nil || still.Active {
		t.Fatal(still, err)
	}
}

func TestInactiveStaffIsNotPromotedBySetup(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)}
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
	roles, err := domain.MarshalRoles([]domain.Role{domain.RoleStaff})
	if err != nil {
		t.Fatal(err)
	}
	staffID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	staff := domain.User{
		Meta: domain.Meta{ID: staffID, CreatedAt: clk.Now(), UpdatedAt: clk.Now(), CreatedBy: staffID},
		Name: "Sam", Email: "sam@example.test", Roles: roles, Active: false,
	}
	if err := mem.Users().Insert(ctx, staff); err != nil {
		t.Fatal(err)
	}
	svc, err := New(Deps{
		Users: mem.Users(), Sessions: mem.Sessions(), Passwords: mem.Passwords(),
		TOTP: mem.TOTP(), Recovery: mem.Recovery(), Tokens: mem.Tokens(),
		Audit: aud, Settings: reg, Key: bytesRepeat(), Now: clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "UTC", AdminName: "Sam",
		AdminEmail: "sam@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	})
	if codeOf(err) != domain.CodeConflict {
		t.Fatal(err)
	}
	got, err := mem.Users().Get(ctx, staffID)
	if err != nil || got.Active || !strings.Contains(got.Roles, "staff") || strings.Contains(got.Roles, "admin") {
		t.Fatal(got, err)
	}
}

func TestHeldChallengeCannotFinishLockedSignIn(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	started, admin := b.enrollAdmin(t)
	b.clk.t = b.clk.t.Add(31 * time.Second)
	reset, err := b.svc.ResetTOTP(ctx, admin, Factor{Password: "correct-horse-battery"})
	if err != nil {
		t.Fatal(err)
	}
	secret := decodeSecret(t, reset.Secret)
	if err := b.svc.ConfirmReset(ctx, admin, reset.EnrollmentToken, TOTPCode(secret, b.clk.Now())); err != nil {
		t.Fatal(err)
	}
	b.clk.t = b.clk.t.Add(31 * time.Second)
	code := TOTPCode(secret, b.clk.Now())
	var tokens []string
	for i := 0; i < 6; i++ {
		login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
		if err != nil || login.TOTPToken == "" {
			t.Fatalf("issue %d: %+v %v", i, login, err)
		}
		tokens = append(tokens, login.TOTPToken)
	}
	for i := 0; i < 5; i++ {
		_, err := b.svc.VerifyTOTP(ctx, tokens[i], "000000")
		if i < 4 && codeOf(err) != domain.CodeUnauthenticated {
			t.Fatalf("guess %d: %v", i, err)
		}
		if i == 4 && codeOf(err) != "locked_out" {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	got, err := b.svc.VerifyTOTP(ctx, tokens[5], code)
	if codeOf(err) != "locked_out" || got.SessionToken != "" {
		t.Fatalf("held token: %+v %v", got, err)
	}
	b.clk.t = b.clk.t.Add(16 * time.Minute)
	login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || login.TOTPToken == "" {
		t.Fatal(login, err)
	}
	recovered, err := b.svc.VerifyRecovery(ctx, login.TOTPToken, started.RecoveryCodes[0])
	if err != nil || recovered.SessionToken == "" {
		t.Fatal(recovered, err)
	}
}

func TestStepUpFailuresShareLockout(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, admin := b.enrollAdmin(t)
	for i := 0; i < 5; i++ {
		_, err := b.svc.ResetTOTP(ctx, admin, Factor{TOTPCode: "000000"})
		if i < 4 && codeOf(err) != domain.CodeUnauthenticated {
			t.Fatalf("factor %d: %v", i, err)
		}
		if i == 4 && codeOf(err) != "locked_out" {
			t.Fatalf("factor %d: %v", i, err)
		}
	}
	if err := b.svc.ChangePassword(ctx, admin, "correct-horse-battery", "correct-horse-updated"); codeOf(err) != "locked_out" {
		t.Fatal(err)
	}
	b.clk.t = b.clk.t.Add(16 * time.Minute)
	login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || login.Status != statusTOTP {
		t.Fatal(login, err)
	}
	if _, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-updated"); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
}

func TestCreateUserAuditsRoleGrant(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, admin := b.enrollAdmin(t)
	user, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Sam Staff", Email: "sam@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleStaff}, ContactID: "contact-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	page, err := b.log.List(ctx, repository.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Items {
		if row.EntityID != user.ID || !strings.Contains(row.Reason, "user.created") {
			continue
		}
		seen[row.Field] = row.NewValue
	}
	for field, want := range map[string]string{
		"email": "sam@example.test", "name": "Sam Staff", "roles": user.Roles,
		"active": "true", "contact_id": "contact-1",
	} {
		if seen[field] != want {
			t.Fatalf("%s: got %q want %q", field, seen[field], want)
		}
	}
	adminRoles := ""
	for _, row := range page.Items {
		if row.EntityID == admin.Principal.UserID && row.Field == "roles" {
			adminRoles = row.NewValue
		}
	}
	if !strings.Contains(adminRoles, "admin") {
		t.Fatal(adminRoles)
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
