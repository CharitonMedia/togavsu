package identity

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/access"
	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

type box struct {
	svc  *Service
	mem  *Memory
	log  *audit.Memory
	clk  *clock
	reg  *settings.Registry
	mail *mailer
}

type mailer struct{ token, email string }

func (m *mailer) SendPasswordReset(email, token string) error {
	m.email = email
	m.token = token
	return nil
}

func newBox(t *testing.T) box {
	t.Helper()
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
	key := bytes.Repeat([]byte{7}, 32)
	mail := &mailer{}
	svc, err := New(Deps{
		Users: mem.Users(), Sessions: mem.Sessions(), Passwords: mem.Passwords(),
		TOTP: mem.TOTP(), Recovery: mem.Recovery(), Tokens: mem.Tokens(),
		Audit: aud, Settings: reg, Key: key, Now: clk.Now, Mail: mail,
	})
	if err != nil {
		t.Fatal(err)
	}
	return box{svc: svc, mem: mem, log: log, clk: clk, reg: reg, mail: mail}
}

type countingLock struct{ repository.Users }

func (countingLock) WithUserLock(_ context.Context, _ domain.ID, fn func() error) error {
	return fn()
}

func TestWithUserDoesNotRepeatNotFound(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	svc, err := New(Deps{
		Users: countingLock{Users: b.mem.Users()}, Sessions: b.mem.Sessions(), Passwords: b.mem.Passwords(),
		TOTP: b.mem.TOTP(), Recovery: b.mem.Recovery(), Tokens: b.mem.Tokens(),
		Audit: mustAudit(t, b), Settings: b.reg, Key: bytes.Repeat([]byte{7}, 32), Now: b.clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = svc.withUser(ctx, domain.SystemUserID, func() error {
		n++
		return repository.ErrNotFound
	})
	if n != 1 || !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("calls %d err %v", n, err)
	}
}

func mustAudit(t *testing.T, b box) *audit.Service {
	t.Helper()
	svc, err := audit.New(b.log, b.clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestNarrowBearerCannotManageSessionsOrAccount(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	started, admin := b.enrollAdmin(t)
	staff, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Sam Staff", Email: "sam@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleStaff},
	})
	if err != nil {
		t.Fatal(err)
	}
	adminTok, err := b.svc.IssueToken(ctx, admin, Issue{Name: "read", UserID: admin.Principal.UserID, Scopes: []string{"matters:read"}})
	if err != nil {
		t.Fatal(err)
	}
	adminBearer, err := b.svc.Authenticate(ctx, Presented{Bearer: adminTok.Secret})
	if err != nil {
		t.Fatal(err)
	}
	staffTok, err := b.svc.IssueToken(ctx, admin, Issue{Name: "staff", UserID: staff.ID, Scopes: []string{"matters:read"}})
	if err != nil {
		t.Fatal(err)
	}
	staffBearer, err := b.svc.Authenticate(ctx, Presented{Bearer: staffTok.Secret})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := b.svc.IssueToken(ctx, admin, Issue{Name: "keep", UserID: admin.Principal.UserID, Scopes: []string{"matters:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.ListSessions(ctx, adminBearer); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if _, err := b.svc.ListSessions(ctx, staffBearer); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if err := b.svc.DeleteSession(ctx, adminBearer, started.SessionID); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if err := b.svc.DeleteSession(ctx, staffBearer, started.SessionID); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if _, err := b.svc.Authenticate(ctx, Presented{SessionToken: started.SessionToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.ListTokens(ctx, adminBearer); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if _, err := b.svc.GetToken(ctx, adminBearer, keep.Token.ID); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if err := b.svc.RevokeToken(ctx, adminBearer, keep.Token.ID); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if _, err := b.svc.Authenticate(ctx, Presented{Bearer: keep.Secret}); err != nil {
		t.Fatal(err)
	}
	if err := b.svc.ChangePassword(ctx, adminBearer, "correct-horse-battery", "correct-horse-updated"); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if _, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.ResetTOTP(ctx, adminBearer, Factor{Password: "correct-horse-battery"}); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if _, err := b.svc.RegenerateRecovery(ctx, adminBearer, Factor{Password: "correct-horse-battery"}); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if _, err := b.svc.Reauthenticate(ctx, adminBearer, Factor{Password: "correct-horse-battery"}, "resetTotp"); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	before, err := b.mem.TOTP().GetByUser(ctx, admin.Principal.UserID)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := b.svc.ResetTOTP(ctx, admin, Factor{Password: "correct-horse-battery"})
	if err != nil {
		t.Fatal(err)
	}
	code := TOTPCode(decodeSecret(t, reset.Secret), b.clk.Now())
	if err := b.svc.ConfirmReset(ctx, adminBearer, reset.EnrollmentToken, code); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	after, err := b.mem.TOTP().GetByUser(ctx, admin.Principal.UserID)
	if err != nil || after.Ciphertext != before.Ciphertext {
		t.Fatal(after, err)
	}
}

func codeOf(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func (b box) enrollAdmin(t *testing.T) (LoginResult, Caller) {
	t.Helper()
	ctx := context.Background()
	enroll, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	if enroll.Secret == "" || enroll.EnrollmentToken == "" || enroll.Status != statusEnrollChallenge {
		t.Fatal(enroll)
	}
	if _, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "Other", TimeZone: "UTC", AdminName: "Bob", AdminEmail: "bob@example.test",
		Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}); codeOf(err) != "already_set_up" {
		t.Fatal(err)
	}
	ready, err := b.svc.Ready(ctx)
	if err != nil || ready {
		t.Fatal(ready, err)
	}
	code := TOTPCode(decodeSecret(t, enroll.Secret), b.clk.Now())
	started, err := b.svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, code)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != statusAuthenticated || started.SessionToken == "" || len(started.RecoveryCodes) == 0 {
		t.Fatal(started)
	}
	ready, err = b.svc.Ready(ctx)
	if err != nil || !ready {
		t.Fatal(ready, err)
	}
	caller, err := b.svc.Authenticate(ctx, Presented{SessionToken: started.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	if caller.Principal.Source == domain.SourceSystem {
		t.Fatal("system principal")
	}
	return started, caller
}

func decodeSecret(t *testing.T, secret string) []byte {
	t.Helper()
	raw, err := base32Decode(secret)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSetupLoginLockoutAndRecovery(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	started, admin := b.enrollAdmin(t)
	cookie := SessionCookie(started.SessionToken)
	if cookie.Name != "togavsu_session" || !cookie.HTTPOnly || !cookie.Secure || cookie.SameSite != "Lax" {
		t.Fatal(cookie)
	}
	again, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || again.Status != statusTOTP || again.SessionToken != "" {
		t.Fatal(again, err)
	}
	if _, err := b.svc.VerifyTOTP(ctx, again.TOTPToken, "000000"); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	b.clk.t = b.clk.t.Add(31 * time.Second)
	secret := decodeSecret(t, mustSecret(t, b, admin))
	b.clk.t = b.clk.t.Add(31 * time.Second)
	again, err = b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	good := TOTPCode(secret, b.clk.Now())
	signed, err := b.svc.VerifyTOTP(ctx, again.TOTPToken, good)
	if err != nil || signed.SessionToken == "" {
		t.Fatal(signed, err)
	}
	if _, err := b.svc.VerifyTOTP(ctx, again.TOTPToken, good); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal("replay", err)
	}
	recoveryLogin, err := b.svc.Login(ctx, "Ada@Example.Test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := b.svc.VerifyRecovery(ctx, recoveryLogin.TOTPToken, started.RecoveryCodes[0])
	if err != nil || recovered.SessionToken == "" {
		t.Fatal(recovered, err)
	}
	if _, err := b.svc.VerifyRecovery(ctx, recoveryLogin.TOTPToken, started.RecoveryCodes[0]); err == nil {
		t.Fatal("recovery replay")
	}
	if _, err := b.svc.Login(ctx, "missing@example.test", "correct-horse-battery"); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	if _, err := b.svc.Login(ctx, "ada@example.test' OR 1=1 --", "correct-horse-battery"); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, err := b.svc.Login(ctx, "ada@example.test", "wrong-horse-battery")
		if i < 4 && codeOf(err) != domain.CodeUnauthenticated {
			t.Fatal(i, err)
		}
		if i == 4 && codeOf(err) != "locked_out" {
			t.Fatal(i, err)
		}
	}
	if _, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery"); codeOf(err) != "locked_out" {
		t.Fatal(err)
	}
	b.clk.t = b.clk.t.Add(16 * time.Minute)
	after, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || after.Status != statusTOTP {
		t.Fatal(after, err)
	}
	dump := auditText(t, b.log)
	for _, secret := range []string{"correct-horse-battery", "backup-passphrase", started.SessionToken, started.RecoveryCodes[0]} {
		if strings.Contains(dump, secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
}

func mustSecret(t *testing.T, b box, caller Caller) string {
	t.Helper()
	reset, err := b.svc.ResetTOTP(ctxBackground(), caller, Factor{Password: "correct-horse-battery"})
	if err != nil {
		t.Fatal(err)
	}
	old := TOTPCode(decodeSecret(t, reset.Secret), b.clk.Now())
	// The reset secret is not active yet. Confirm it so later logins use a known secret,
	// and prove the previous secret still worked up to this call by confirming the new one.
	if err := b.svc.ConfirmReset(ctxBackground(), caller, reset.EnrollmentToken, old); err != nil {
		t.Fatal(err)
	}
	return reset.Secret
}

func ctxBackground() context.Context { return context.Background() }

func TestResetKeepsOldSecretUntilConfirm(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	started, admin := b.enrollAdmin(t)
	row, err := b.mem.TOTP().GetByUser(ctx, admin.Principal.UserID)
	if err != nil {
		t.Fatal(err)
	}
	before := row.Ciphertext
	reset, err := b.svc.ResetTOTP(ctx, admin, Factor{Password: "correct-horse-battery"})
	if err != nil {
		t.Fatal(err)
	}
	row, err = b.mem.TOTP().GetByUser(ctx, admin.Principal.UserID)
	if err != nil || row.Ciphertext != before || !row.Confirmed {
		t.Fatal("secret changed early", err)
	}
	login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	// The original enrollment secret is not retained in the clear. A wrong new-secret code must not switch.
	if err := b.svc.ConfirmReset(ctx, admin, reset.EnrollmentToken, "000000"); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	row, err = b.mem.TOTP().GetByUser(ctx, admin.Principal.UserID)
	if err != nil || row.Ciphertext != before {
		t.Fatal("wrong code changed the secret", err)
	}
	// A wrong code spends the reset challenge, so the user starts a new reset.
	if err := b.svc.ConfirmReset(ctx, admin, reset.EnrollmentToken, TOTPCode(decodeSecret(t, reset.Secret), b.clk.Now())); codeOf(err) != domain.CodeForbidden {
		t.Fatal("spent reset token", err)
	}
	reset, err = b.svc.ResetTOTP(ctx, admin, Factor{Password: "correct-horse-battery"})
	if err != nil {
		t.Fatal(err)
	}
	other := admin
	other.Principal.UserID = "01936b8e-7c00-7000-8000-000000000099"
	if err := b.svc.ConfirmReset(ctx, other, reset.EnrollmentToken, TOTPCode(decodeSecret(t, reset.Secret), b.clk.Now())); codeOf(err) != domain.CodeForbidden {
		t.Fatal(err)
	}
	if err := b.svc.ConfirmReset(ctx, admin, reset.EnrollmentToken, TOTPCode(decodeSecret(t, reset.Secret), b.clk.Now())); err != nil {
		t.Fatal(err)
	}
	if err := b.svc.ConfirmReset(ctx, admin, reset.EnrollmentToken, TOTPCode(decodeSecret(t, reset.Secret), b.clk.Now())); codeOf(err) != domain.CodeForbidden {
		t.Fatal("replay", err)
	}
	b.clk.t = b.clk.t.Add(31 * time.Second)
	signed, err := b.svc.VerifyTOTP(ctx, login.TOTPToken, TOTPCode(decodeSecret(t, reset.Secret), b.clk.Now()))
	if err != nil || signed.SessionToken == "" {
		t.Fatal(signed, err)
	}
	_ = started
}

func TestStaffEnrollmentClientSessionAndTokens(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, admin := b.enrollAdmin(t)
	staff, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Sam Staff", Email: "sam@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleStaff},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.CreateUser(ctx, Caller{Principal: domain.Principal{UserID: staff.ID, Roles: []domain.Role{domain.RoleStaff}, Source: domain.SourceUI}}, NewUser{
		Name: "Nope", Email: "nope@example.test", Password: "correct-horse-battery", Roles: []domain.Role{domain.RoleStaff},
	}); codeOf(err) != domain.CodeForbidden {
		t.Fatal(err)
	}
	pending, err := b.svc.Login(ctx, "sam@example.test", "correct-horse-battery")
	if err != nil || pending.Status != statusEnroll || pending.SessionToken != "" {
		t.Fatal(pending, err)
	}
	enroll, err := b.svc.StartEnrollment(ctx, pending.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	staffSession, err := b.svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, TOTPCode(decodeSecret(t, enroll.Secret), b.clk.Now()))
	if err != nil {
		t.Fatal(err)
	}
	staffCaller, err := b.svc.Authenticate(ctx, Presented{SessionToken: staffSession.SessionToken, Write: true})
	if codeOf(err) != "csrf_required" {
		t.Fatal(err)
	}
	staffCaller, err = b.svc.Authenticate(ctx, Presented{SessionToken: staffSession.SessionToken, CSRF: staffSession.CSRFToken, Write: true})
	if err != nil {
		t.Fatal(err)
	}
	clientUser, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Cora Client", Email: "cora@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleClient},
	})
	if err != nil {
		t.Fatal(err)
	}
	clientLogin, err := b.svc.Login(ctx, "cora@example.test", "correct-horse-battery")
	if err != nil || clientLogin.Status != statusAuthenticated || clientLogin.SessionToken == "" {
		t.Fatal(clientLogin, err)
	}
	if _, err := b.svc.IssueToken(ctx, staffCaller, Issue{Name: "nope", UserID: staff.ID, Scopes: []string{"matters:read"}}); codeOf(err) != domain.CodeForbidden {
		t.Fatal(err)
	}
	issued, err := b.svc.IssueToken(ctx, admin, Issue{Name: "matters", UserID: staff.ID, Scopes: []string{"matters:read", "trust:withdraw"}})
	if codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	issued, err = b.svc.IssueToken(ctx, admin, Issue{Name: "matters", UserID: staff.ID, Scopes: []string{"matters:read"}})
	if err != nil || issued.Secret == "" {
		t.Fatal(issued, err)
	}
	if _, err := b.svc.IssueToken(ctx, admin, Issue{Name: "empty", UserID: staff.ID, Scopes: nil}); codeOf(err) != domain.CodeValidation {
		t.Fatal(err)
	}
	bearer, err := b.svc.Authenticate(ctx, Presented{Bearer: issued.Secret, Write: true})
	if err != nil || bearer.Principal.Source != domain.SourceAPIToken || bearer.Principal.Source == domain.SourceSystem {
		t.Fatal(bearer, err)
	}
	if err := bearer.RequireScopes("trust:withdraw"); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if err := bearer.RequireScopes("matters:read"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.Authenticate(ctx, Presented{SessionToken: staffSession.SessionToken, Bearer: issued.Secret}); codeOf(err) != "ambiguous_auth" {
		t.Fatal(err)
	}
	if _, err := b.svc.Authenticate(ctx, Presented{SessionToken: "not-a-session"}); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	if err := b.svc.RevokeToken(ctx, staffCaller, issued.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.Authenticate(ctx, Presented{Bearer: issued.Secret}); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	view, err := b.svc.GetToken(ctx, staffCaller, issued.Token.ID)
	if err != nil || !view.Revoked {
		t.Fatal(view, err)
	}
	if _, err := b.svc.GetToken(ctx, staffCaller, admin.Principal.UserID); codeOf(err) != domain.CodeNotFound {
		t.Fatal(err)
	}
	adminTok, err := b.svc.IssueToken(ctx, admin, Issue{Name: "admin", UserID: admin.Principal.UserID, Scopes: []string{"tokens:write", "matters:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.GetToken(ctx, staffCaller, adminTok.Token.ID); codeOf(err) != domain.CodeNotFound {
		t.Fatal(err)
	}
	adminBearer, err := b.svc.Authenticate(ctx, Presented{Bearer: adminTok.Secret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.IssueToken(ctx, adminBearer, Issue{Name: "too wide", UserID: staff.ID, Scopes: []string{"trust:withdraw"}}); codeOf(err) != "scope_denied" {
		t.Fatal(err)
	}
	if _, err := b.reg.Set(ctx, "security.api_rate_per_minute", "2", admin.Principal); err != nil {
		t.Fatal(err)
	}
	limited, err := b.svc.IssueToken(ctx, admin, Issue{Name: "limited", UserID: clientUser.ID, Scopes: []string{"profile:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.svc.Authenticate(ctx, Presented{Bearer: limited.Secret}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.svc.Authenticate(ctx, Presented{Bearer: limited.Secret}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.svc.Authenticate(ctx, Presented{Bearer: limited.Secret}); codeOf(err) != domain.CodeRateLimited {
		t.Fatal(err)
	}
	b.clk.t = b.clk.t.Add(13 * time.Hour)
	if _, err := b.svc.Authenticate(ctx, Presented{SessionToken: staffSession.SessionToken}); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
}

func TestPasswordResetChangeAndReauth(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	started, _ := b.enrollAdmin(t)
	if err := b.svc.RequestPasswordReset(ctx, "missing@example.test"); err != nil || b.mail.token != "" {
		t.Fatal(err, b.mail.token)
	}
	if err := b.svc.RequestPasswordReset(ctx, "ada@example.test"); err != nil || b.mail.token == "" {
		t.Fatal(err)
	}
	if err := b.svc.ConfirmPasswordReset(ctx, b.mail.token, "correct-horse-updated"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.Authenticate(ctx, Presented{SessionToken: started.SessionToken}); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal("old session", err)
	}
	if _, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery"); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	login, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-updated")
	if err != nil {
		t.Fatal(err)
	}
	// TOTP still enrolled from setup. Finish sign-in with a fresh authenticator is not available;
	// use a recovery code from the original enrollment.
	done, err := b.svc.VerifyRecovery(ctx, login.TOTPToken, started.RecoveryCodes[1])
	if err != nil {
		t.Fatal(err)
	}
	caller, err := b.svc.Authenticate(ctx, Presented{SessionToken: done.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.svc.ChangePassword(ctx, caller, "wrong-horse-updated", "correct-horse-thirdxx"); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
	if err := b.svc.ChangePassword(ctx, caller, "correct-horse-updated", "correct-horse-thirdxx"); err != nil {
		t.Fatal(err)
	}
	token, err := b.svc.Reauthenticate(ctx, caller, Factor{Password: "correct-horse-thirdxx"}, "resetTotp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.RegenerateRecovery(ctx, caller, Factor{ReauthToken: token}); codeOf(err) != domain.CodeForbidden {
		t.Fatal(err)
	}
	codes, err := b.svc.ResetTOTP(ctx, caller, Factor{ReauthToken: token})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.svc.ConfirmReset(ctx, caller, codes.EnrollmentToken, TOTPCode(decodeSecret(t, codes.Secret), b.clk.Now())); err != nil {
		t.Fatal(err)
	}
	if err := b.svc.Logout(ctx, caller); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.Authenticate(ctx, Presented{SessionToken: done.SessionToken}); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
}

func TestAuthenticatedStaffHitsMatterWall(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, admin := b.enrollAdmin(t)
	staff, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Sam Staff", Email: "sam@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleStaff},
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := b.svc.Login(ctx, "sam@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	enroll, err := b.svc.StartEnrollment(ctx, pending.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, TOTPCode(decodeSecret(t, enroll.Secret), b.clk.Now()))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := b.svc.Authenticate(ctx, Presented{SessionToken: session.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	const matter = domain.ID("matter-walled")
	f := &wallMem{walls: []domain.MatterWall{{Meta: domain.Meta{ID: "w"}, MatterID: matter, UserID: admin.Principal.UserID}}}
	gate := access.New(access.Reader{Walls: wallWalls{f}, Parties: wallParties{f}, Users: wallUsers{f}})
	if err := gate.VisibleMatter(ctx, caller.Principal, matter); !access.IsNotFound(err) {
		t.Fatal(err)
	}
	if err := gate.Allow(ctx, domain.Principal{UserID: staff.ID, Roles: []domain.Role{domain.RoleAccountant}, Source: domain.SourceUI}, domain.ResourceRef{
		Resource: domain.ResInvoice, Action: domain.ActionWrite,
	}); !access.IsForbidden(err) {
		t.Fatal(err)
	}
	_ = staff
}

type wallMem struct{ walls []domain.MatterWall }

func (w wallMem) Insert(context.Context, domain.MatterWall) error { return nil }
func (w wallMem) Delete(context.Context, domain.ID) error         { return nil }
func (w wallMem) List(_ context.Context, filter repository.Filter) (repository.Page[domain.MatterWall], error) {
	var items []domain.MatterWall
	for _, row := range w.walls {
		if filter.MatterID == "" || row.MatterID == filter.MatterID {
			items = append(items, row)
		}
	}
	return repository.Page[domain.MatterWall]{Items: items}, nil
}
func (w wallMem) InsertParty(context.Context, domain.MatterParty) error { return nil }
func (w wallMem) Update(context.Context, domain.MatterParty) error      { return nil }
func (w wallMem) Get(context.Context, domain.ID) (domain.MatterParty, error) {
	return domain.MatterParty{}, repository.ErrNotFound
}
func (w wallMem) ListParties(context.Context, repository.Filter) (repository.Page[domain.MatterParty], error) {
	return repository.Page[domain.MatterParty]{}, nil
}
func (w wallMem) InsertUser(context.Context, domain.User) error { return nil }
func (w wallMem) UpdateUser(context.Context, domain.User) error { return nil }
func (w wallMem) GetUser(context.Context, domain.ID) (domain.User, error) {
	return domain.User{}, repository.ErrNotFound
}
func (w wallMem) ListUsers(context.Context, repository.Filter) (repository.Page[domain.User], error) {
	return repository.Page[domain.User]{}, nil
}

type wallWalls struct{ *wallMem }
type wallParties struct{ *wallMem }
type wallUsers struct{ *wallMem }

func (w wallWalls) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.MatterWall], error) {
	return w.wallMem.List(ctx, filter)
}
func (w wallParties) Insert(ctx context.Context, row domain.MatterParty) error {
	return w.InsertParty(ctx, row)
}
func (w wallParties) Update(ctx context.Context, row domain.MatterParty) error {
	return w.wallMem.Update(ctx, row)
}
func (w wallParties) Get(ctx context.Context, id domain.ID) (domain.MatterParty, error) {
	return w.wallMem.Get(ctx, id)
}
func (w wallParties) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.MatterParty], error) {
	return w.ListParties(ctx, filter)
}
func (w wallUsers) Insert(ctx context.Context, row domain.User) error { return w.InsertUser(ctx, row) }
func (w wallUsers) Update(ctx context.Context, row domain.User) error { return w.UpdateUser(ctx, row) }
func (w wallUsers) Get(ctx context.Context, id domain.ID) (domain.User, error) {
	return w.GetUser(ctx, id)
}
func (w wallUsers) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.User], error) {
	return w.ListUsers(ctx, filter)
}

func TestModuleTokenCannotReadMatters(t *testing.T) {
	ctx := context.Background()
	b := newBox(t)
	_, admin := b.enrollAdmin(t)
	issued, err := b.svc.IssueToken(ctx, admin, Issue{
		Name: "clock", ModuleID: "matter-clock", Scopes: []string{"matters:read"}, Approved: []string{"matters:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := b.svc.Authenticate(ctx, Presented{Bearer: issued.Secret})
	if err != nil || caller.Principal.Source != domain.SourceModule || caller.Principal.ModuleID != "matter-clock" {
		t.Fatal(caller, err)
	}
	if caller.Principal.Source == domain.SourceSystem {
		t.Fatal("system")
	}
	gate := access.New(nil)
	if err := gate.Allow(ctx, caller.Principal, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); codeOf(err) != domain.CodeUnauthenticated && !access.IsForbidden(err) {
		t.Fatal(err)
	}
}

func auditText(t *testing.T, log *audit.Memory) string {
	t.Helper()
	page, err := log.List(context.Background(), repository.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, row := range page.Items {
		b.WriteString(row.Field)
		b.WriteString(row.OldValue)
		b.WriteString(row.NewValue)
		b.WriteString(row.Reason)
	}
	return b.String()
}

func base32Decode(secret string) ([]byte, error) {
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
}
