package identity

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func pgSetupService(t *testing.T, sqlDB *sql.DB, store audit.Store) *Service {
	t.Helper()
	aud, err := audit.New(store, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(db.SettingsStore{DB: sqlDB, Dialect: "postgres"}, aud)
	if err != nil {
		t.Fatal(err)
	}
	pg := NewPG(sqlDB)
	svc, err := New(Deps{
		Users: pg.Users(), Sessions: pg.Sessions(), Passwords: pg.Passwords(),
		TOTP: pg.TOTP(), Recovery: pg.Recovery(), Tokens: pg.Tokens(),
		Audit: aud, Settings: reg, Key: bytes.Repeat([]byte{9}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func setupInput(email string) SetupInput {
	return SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: email, Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	}
}

func countRows(t *testing.T, sqlDB *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestConcurrentSetupCreatesOneAdmin is the first-admin race: setups with different
// emails arrive at once and only one of them may create an admin.
func TestConcurrentSetupCreatesOneAdmin(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := audit.InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	// Two services on one pool stand in for two core processes.
	services := []*Service{
		pgSetupService(t, sqlDB, audit.NewPostgres(sqlDB)),
		pgSetupService(t, sqlDB, audit.NewPostgres(sqlDB)),
	}
	// Every caller pauses after the checks, so without the lock all of them pass.
	beforeFirstAdminInsert = func() { time.Sleep(50 * time.Millisecond) }
	t.Cleanup(func() { beforeFirstAdminInsert = func() {} })
	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = services[i%len(services)].Setup(ctx, setupInput(fmt.Sprintf("admin%d@example.test", i)))
		}(i)
	}
	close(start)
	wg.Wait()
	won := 0
	for i, err := range errs {
		if err == nil {
			won++
			continue
		}
		var api *domain.APIError
		if !errors.As(err, &api) || api.Code != "already_set_up" {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d setups succeeded", won)
	}
	if n := countRows(t, sqlDB, `SELECT count(*) FROM users WHERE active = 1`); n != 1 {
		t.Fatalf("%d active admins", n)
	}
	if n := countRows(t, sqlDB, `SELECT count(*) FROM users`); n != 1 {
		t.Fatalf("%d users", n)
	}
}

// TestSetupLockNeedsTransaction keeps the lock from silently doing nothing outside one.
func TestSetupLockNeedsTransaction(t *testing.T) {
	sqlDB := openCoreDB(t)
	users := NewPG(sqlDB).Users().(pgUsers)
	if err := users.LockSetup(context.Background()); !errors.Is(err, errSetupLockNeedsTx) {
		t.Fatal(err)
	}
}

// failingAppend is the Postgres audit store with one append that fails.
type failingAppend struct {
	*audit.Postgres
	fail func(domain.AuditEntry) bool
}

func (f failingAppend) Append(ctx context.Context, row domain.AuditEntry) error {
	if f.fail(row) {
		return errors.New("audit append failed")
	}
	return f.Postgres.Append(ctx, row)
}

// TestSetupAuditFailureLeavesNoAdmin: an audit append that fails after the admin and
// password were written rolls both back, so the next setup is not refused.
func TestSetupAuditFailureLeavesNoAdmin(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := audit.InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		name string
		fail func(domain.AuditEntry) bool
	}{
		{"user created", func(r domain.AuditEntry) bool { return r.Entity == "user" && r.Field == "email" }},
		{"enrollment", func(r domain.AuditEntry) bool { return r.Entity == "totp" && r.Field == "ciphertext" }},
	} {
		svc := pgSetupService(t, sqlDB, failingAppend{Postgres: audit.NewPostgres(sqlDB), fail: step.fail})
		if _, err := svc.Setup(ctx, setupInput("first@example.test")); err == nil {
			t.Fatalf("%s: setup succeeded with a failed audit append", step.name)
		}
		for _, table := range []string{"users", "password_credentials", "totp_secrets", "audit_log"} {
			if n := countRows(t, sqlDB, `SELECT count(*) FROM `+table); n != 0 {
				t.Fatalf("%s: %d rows left in %s", step.name, n, table)
			}
		}
	}
	svc := pgSetupService(t, sqlDB, audit.NewPostgres(sqlDB))
	enroll, err := svc.Setup(ctx, setupInput("second@example.test"))
	if err != nil || enroll.EnrollmentToken == "" {
		t.Fatal(enroll, err)
	}
}

func TestFailedAuditKeepsResetSecretAndToken(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := audit.InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	in := setupInput("ada@example.test")
	svc := pgSetupService(t, sqlDB, audit.NewPostgres(sqlDB))
	enroll, err := svc.Setup(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base32Decode(enroll.Secret)
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, TOTPCode(raw, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := svc.Authenticate(ctx, Presented{SessionToken: started.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	before, err := NewPG(sqlDB).TOTP().GetByUser(ctx, started.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := svc.ResetTOTP(ctx, caller, Factor{Password: in.Password})
	if err != nil {
		t.Fatal(err)
	}
	broken := pgSetupService(t, sqlDB, failingAppend{
		Postgres: audit.NewPostgres(sqlDB),
		fail: func(row domain.AuditEntry) bool {
			return strings.Contains(row.Reason, "user.totp_reset_confirmed")
		},
	})
	if _, err := broken.ConfirmReset(ctx, caller, reset.EnrollmentToken, TOTPCode(decodeSecret(t, reset.Secret), time.Now())); err == nil {
		t.Fatal("confirm reset kept a secret without its audit row")
	}
	after, err := NewPG(sqlDB).TOTP().GetByUser(ctx, started.User.ID)
	if err != nil || after.Ciphertext != before.Ciphertext {
		t.Fatal(after, err)
	}
	issued, err := svc.IssueToken(ctx, caller, Issue{Name: "keep", UserID: started.User.ID, Scopes: []string{"matters:read"}})
	if err != nil {
		t.Fatal(err)
	}
	broken = pgSetupService(t, sqlDB, failingAppend{
		Postgres: audit.NewPostgres(sqlDB),
		fail: func(row domain.AuditEntry) bool {
			return row.Entity == "api_token" && row.Field == "revoked_at"
		},
	})
	if err := broken.RevokeToken(ctx, caller, issued.Token.ID); err == nil {
		t.Fatal("revoke kept a spent token without its audit row")
	}
	token, err := NewPG(sqlDB).Tokens().Get(ctx, issued.Token.ID)
	if err != nil || token.RevokedAt != nil {
		t.Fatal(token, err)
	}
}

// TestConcurrentConfirmResetIsSingleUse: two confirms of one reset token with the same
// code replace the secret once and append one confirmation row.
func TestConcurrentConfirmResetIsSingleUse(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := audit.InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	in := setupInput("ada@example.test")
	svc := pgSetupService(t, sqlDB, audit.NewPostgres(sqlDB))
	enroll, err := svc.Setup(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base32Decode(enroll.Secret)
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, TOTPCode(raw, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := svc.Authenticate(ctx, Presented{SessionToken: started.SessionToken})
	if err != nil {
		t.Fatal(err)
	}
	reset, err := svc.ResetTOTP(ctx, caller, Factor{Password: in.Password})
	if err != nil {
		t.Fatal(err)
	}
	code := TOTPCode(decodeSecret(t, reset.Secret), time.Now())
	confirm := func() error {
		_, err := svc.ConfirmReset(ctx, caller, reset.EnrollmentToken, code)
		return err
	}
	wins := race(confirm, confirm)
	if wins != 1 {
		t.Fatalf("confirm reset wins %d", wins)
	}
	if n := countRows(t, sqlDB, `SELECT count(*) FROM audit_log WHERE reason LIKE '%user.totp_reset_confirmed%'`); n != 1 {
		t.Fatalf("%d confirmation rows", n)
	}
	if _, err := svc.ConfirmReset(ctx, caller, reset.EnrollmentToken, code); codeOf(err) != "forbidden" {
		t.Fatalf("spent reset token: %v", err)
	}
}
