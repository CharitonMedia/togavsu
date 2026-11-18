package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestPasswordOptionalOffRefusesEmptyPassword(t *testing.T) {
	ctx := context.Background()
	b := newDefaultBox(t)
	if _, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}); err != nil {
		t.Fatal(err)
	}
	on, err := b.svc.PasswordOptional(ctx)
	if err != nil || on {
		t.Fatalf("default must be off: on=%v err=%v", on, err)
	}
	_, err = b.svc.Login(ctx, "ada@example.test", "")
	if codeOf(err) != domain.CodeValidation || !strings.Contains(err.Error(), "email and password are required") {
		t.Fatalf("off must refuse an empty password: %v", err)
	}
}

func TestPasswordOptionalOnSignsInWithoutPassword(t *testing.T) {
	ctx := context.Background()
	b := newDefaultBox(t)
	if _, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}); err != nil {
		t.Fatal(err)
	}
	adminLogin, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := b.svc.Authenticate(ctx, Presented{SessionToken: adminLogin.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.reg.Set(ctx, "security.password_optional", "true", admin.Principal); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Sam Staff", Email: "sam@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleStaff},
	}); err != nil {
		t.Fatal(err)
	}

	for _, password := range []string{"", "not-the-password", "correct-horse-battery"} {
		got, err := b.svc.Login(ctx, "Sam@Example.Test", password)
		if err != nil || got.Status != statusAuthenticated || got.SessionToken == "" {
			t.Fatalf("password %q: %+v %v", password, got, err)
		}
		if !signInSkippedPassword(t, b.log, got.User.ID) {
			t.Fatalf("password %q: successful sign-in must record password_skipped", password)
		}
	}

	if _, err := b.svc.Login(ctx, "missing@example.test", ""); codeOf(err) != domain.CodeUnauthenticated {
		t.Fatalf("unknown email must still fail: %v", err)
	}
}

func TestPasswordOptionalEnrollmentStillChecksPassword(t *testing.T) {
	ctx := context.Background()
	b := newDefaultBox(t)
	if _, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}); err != nil {
		t.Fatal(err)
	}
	adminLogin, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := b.svc.Authenticate(ctx, Presented{SessionToken: adminLogin.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.reg.Set(ctx, "security.password_optional", "true", admin.Principal); err != nil {
		t.Fatal(err)
	}
	if _, err := b.reg.Set(ctx, "security.totp_required.client", "true", admin.Principal); err != nil {
		t.Fatal(err)
	}
	client, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Cora Client", Email: "cora@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleClient},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, password := range []string{"", "wrong-password"} {
		before := failedSignIns(t, b.log, client.ID)
		got, err := b.svc.Login(ctx, "cora@example.test", password)
		if codeOf(err) != domain.CodeUnauthenticated {
			t.Fatalf("password %q: want failed sign-in, got %+v %v", password, got, err)
		}
		if got.EnrollmentToken != "" || got.SessionToken != "" {
			t.Fatalf("password %q must not issue enrollment: %+v", password, got)
		}
		if failedSignIns(t, b.log, client.ID) != before+1 {
			t.Fatalf("password %q must count toward lockout", password)
		}
	}

	enroll, err := b.svc.Login(ctx, "cora@example.test", "correct-horse-battery")
	if err != nil || enroll.Status != statusEnroll || enroll.EnrollmentToken == "" || enroll.SessionToken != "" {
		t.Fatalf("right password must get enrollment: %+v %v", enroll, err)
	}
}

func TestPasswordOptionalConfirmedTOTPStillNeedsCode(t *testing.T) {
	ctx := context.Background()
	b := newDefaultBox(t)
	if _, err := b.svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}); err != nil {
		t.Fatal(err)
	}
	adminLogin, err := b.svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := b.svc.Authenticate(ctx, Presented{SessionToken: adminLogin.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.reg.Set(ctx, "security.password_optional", "true", admin.Principal); err != nil {
		t.Fatal(err)
	}
	if _, err := b.reg.Set(ctx, "security.totp_required.staff", "true", admin.Principal); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.CreateUser(ctx, admin, NewUser{
		Name: "Sam Staff", Email: "sam@example.test", Password: "correct-horse-battery",
		Roles: []domain.Role{domain.RoleStaff},
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := b.svc.Login(ctx, "sam@example.test", "correct-horse-battery")
	if err != nil || pending.Status != statusEnroll {
		t.Fatal(pending, err)
	}
	start, err := b.svc.StartEnrollment(ctx, pending.EnrollmentToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.ConfirmEnrollment(ctx, start.EnrollmentToken, TOTPCode(decodeSecret(t, start.Secret), b.clk.Now())); err != nil {
		t.Fatal(err)
	}

	got, err := b.svc.Login(ctx, "sam@example.test", "")
	if err != nil || got.Status != statusTOTP || got.SessionToken != "" || got.TOTPToken == "" {
		t.Fatalf("confirmed TOTP must get the code step: %+v %v", got, err)
	}
}

func signInSkippedPassword(t *testing.T, log *audit.Memory, userID domain.ID) bool {
	t.Helper()
	rows, err := log.Rows(context.Background(), audit.RowQuery{Entity: "user", EntityID: userID, Field: signInField})
	if err != nil {
		t.Fatal(err)
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].NewValue != "succeeded" {
			continue
		}
		return strings.Contains(rows[i].Reason, "password_skipped: true")
	}
	return false
}

func failedSignIns(t *testing.T, log *audit.Memory, userID domain.ID) int {
	t.Helper()
	rows, err := log.Rows(context.Background(), audit.RowQuery{Entity: "user", EntityID: userID, Field: signInField})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, row := range rows {
		if row.NewValue == "failed" {
			n++
		}
	}
	return n
}
