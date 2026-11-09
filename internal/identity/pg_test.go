package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func TestSetupReachesReadSetup(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := audit.InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyReference(ctx, sqlDB, "postgres"); err != nil {
		t.Fatal(err)
	}
	before, err := db.ReadSetup(ctx, sqlDB)
	if err != nil || before.Complete {
		t.Fatal(before, err)
	}
	store := NewPG(sqlDB)
	aud, err := audit.New(audit.NewPostgres(sqlDB), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(db.SettingsStore{DB: sqlDB, Dialect: "postgres"}, aud)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Deps{
		Users: store.Users(), Sessions: store.Sessions(), Passwords: store.Passwords(),
		TOTP: store.TOTP(), Recovery: store.Recovery(), Tokens: store.Tokens(),
		Audit: aud, Settings: reg, Key: bytes.Repeat([]byte{9}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	enroll, err := svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base32Decode(enroll.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, TOTPCode(raw, time.Now())); err != nil {
		t.Fatal(err)
	}
	got, err := db.ReadSetup(ctx, sqlDB)
	if err != nil || !got.Complete || got.FirmName != "North Firm" || got.AdminUsers != 1 || got.AdminTOTP != 1 {
		t.Fatal(got, err)
	}
	var leaked int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE new_value LIKE '%correct-horse-battery%' OR new_value LIKE '%backup-passphrase%' OR old_value LIKE '%backup-passphrase%'`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal(leaked)
	}
}

func TestDuplicateEmailIsConflict(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	store := NewPG(sqlDB)
	roles, err := domain.MarshalRoles([]domain.Role{domain.RoleStaff})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	row := domain.User{
		Meta: domain.Meta{ID: first, CreatedAt: now, UpdatedAt: now, CreatedBy: first},
		Name: "Ada", Email: "ada@example.test", Roles: roles, Active: true,
	}
	if err := store.Users().Insert(ctx, row); err != nil {
		t.Fatal(err)
	}
	row.ID = second
	row.CreatedBy = second
	if err := store.Users().Insert(ctx, row); !errors.Is(err, repository.ErrConflict) {
		t.Fatal(err)
	}
}

func TestConcurrentRecoveryAndTOTPAreSingleUse(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := audit.InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyReference(ctx, sqlDB, "postgres"); err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)}
	store := NewPG(sqlDB)
	aud, err := audit.New(audit.NewPostgres(sqlDB), clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(db.SettingsStore{DB: sqlDB, Dialect: "postgres"}, aud)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Deps{
		Users: store.Users(), Sessions: store.Sessions(), Passwords: store.Passwords(),
		TOTP: store.TOTP(), Recovery: store.Recovery(), Tokens: store.Tokens(),
		Audit: aud, Settings: reg, Key: bytes.Repeat([]byte{9}, 32), Now: clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	enroll, err := svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base32Decode(enroll.Secret)
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, TOTPCode(raw, clk.Now()))
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	wins := race(func() error {
		_, err := svc.VerifyRecovery(ctx, first.TOTPToken, started.RecoveryCodes[0])
		return err
	}, func() error {
		_, err := svc.VerifyRecovery(ctx, second.TOTPToken, started.RecoveryCodes[0])
		return err
	})
	if wins != 1 {
		t.Fatalf("recovery wins %d", wins)
	}
	clk.t = clk.t.Add(31 * time.Second)
	code := TOTPCode(raw, clk.Now())
	left, err := svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	right, err := svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	wins = race(func() error {
		_, err := svc.VerifyTOTP(ctx, left.TOTPToken, code)
		return err
	}, func() error {
		_, err := svc.VerifyTOTP(ctx, right.TOTPToken, code)
		return err
	})
	if wins != 1 {
		t.Fatalf("totp wins %d", wins)
	}
}

func TestConcurrentPasswordResetIsSingleUse(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := audit.InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyReference(ctx, sqlDB, "postgres"); err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 11, 9, 17, 0, 0, 0, time.UTC)}
	store := NewPG(sqlDB)
	aud, err := audit.New(audit.NewPostgres(sqlDB), clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(db.SettingsStore{DB: sqlDB, Dialect: "postgres"}, aud)
	if err != nil {
		t.Fatal(err)
	}
	mail := &mailer{}
	svc, err := New(Deps{
		Users: store.Users(), Sessions: store.Sessions(), Passwords: store.Passwords(),
		TOTP: store.TOTP(), Recovery: store.Recovery(), Tokens: store.Tokens(),
		Audit: aud, Settings: reg, Key: bytes.Repeat([]byte{9}, 32), Now: clk.Now, Mail: mail,
	})
	if err != nil {
		t.Fatal(err)
	}
	enroll, err := svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base32Decode(enroll.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, TOTPCode(raw, clk.Now())); err != nil {
		t.Fatal(err)
	}
	if err := svc.RequestPasswordReset(ctx, "ada@example.test"); err != nil || mail.token == "" {
		t.Fatal(err)
	}
	var before int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity = 'password' AND reason = 'user.password_changed'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	wins := race(func() error {
		return svc.ConfirmPasswordReset(ctx, mail.token, "correct-horse-updated")
	}, func() error {
		return svc.ConfirmPasswordReset(ctx, mail.token, "correct-horse-another")
	})
	if wins != 1 {
		t.Fatalf("reset wins %d", wins)
	}
	var after int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity = 'password' AND reason = 'user.password_changed'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after-before != 1 {
		t.Fatalf("password writes %d, want 1", after-before)
	}
}

func TestOneChallengeAdmitsOneGuess(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := audit.InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyReference(ctx, sqlDB, "postgres"); err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 11, 9, 16, 0, 0, 0, time.UTC)}
	store := NewPG(sqlDB)
	aud, err := audit.New(audit.NewPostgres(sqlDB), clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(db.SettingsStore{DB: sqlDB, Dialect: "postgres"}, aud)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Deps{
		Users: store.Users(), Sessions: store.Sessions(), Passwords: store.Passwords(),
		TOTP: store.TOTP(), Recovery: store.Recovery(), Tokens: store.Tokens(),
		Audit: aud, Settings: reg, Key: bytes.Repeat([]byte{9}, 32), Now: clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	enroll, err := svc.Setup(ctx, SetupInput{
		FirmName: "North Firm", TimeZone: "America/Chicago", AdminName: "Ada Admin",
		AdminEmail: "ada@example.test", Password: "correct-horse-battery", BackupPassphrase: "backup-passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base32Decode(enroll.Secret)
	if err != nil {
		t.Fatal(err)
	}
	started, err := svc.ConfirmEnrollment(ctx, enroll.EnrollmentToken, TOTPCode(raw, clk.Now()))
	if err != nil {
		t.Fatal(err)
	}
	login, err := svc.Login(ctx, "ada@example.test", "correct-horse-battery")
	if err != nil || login.TOTPToken == "" {
		t.Fatal(login, err)
	}
	const guesses = 32
	var wg sync.WaitGroup
	wg.Add(guesses)
	for i := 0; i < guesses; i++ {
		go func() {
			defer wg.Done()
			_, _ = svc.VerifyTOTP(ctx, login.TOTPToken, "000000")
		}()
	}
	wg.Wait()
	var failed, succeeded int
	if err := sqlDB.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE new_value = 'failed'),
		COUNT(*) FILTER (WHERE new_value = 'succeeded')
		FROM audit_log WHERE entity = 'user' AND entity_id = $1 AND field = 'sign_in'`, string(started.User.ID)).Scan(&failed, &succeeded); err != nil {
		t.Fatal(err)
	}
	if failed != 1 {
		t.Fatalf("failures %d, want 1", failed)
	}
	if succeeded != 1 {
		t.Fatalf("successes %d, want the enrollment session only", succeeded)
	}
	caller := Caller{Principal: domain.Principal{UserID: started.User.ID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}}
	wg.Add(guesses)
	for i := 0; i < guesses; i++ {
		go func() {
			defer wg.Done()
			_, _ = svc.Reauthenticate(ctx, caller, Factor{TOTPCode: "000000"}, "reset-totp")
		}()
	}
	wg.Wait()
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity = 'user' AND entity_id = $1 AND field = 'sign_in' AND new_value = 'failed'`, string(started.User.ID)).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != 5 {
		t.Fatalf("failures %d, want the lockout limit", failed)
	}
}

func TestSignInLockDoesNotWaitForever(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	lock, ok := NewPG(sqlDB).Users().(userLock)
	if !ok {
		t.Fatal("postgres users do not lock")
	}
	const id = "user-1"
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var held bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1::text, 0))`, id).Scan(&held); err != nil || !held {
		t.Fatal(held, err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1::text, 0))`, id)
	done := make(chan error, 1)
	go func() {
		done <- lock.WithUserLock(context.Background(), domain.ID(id), func() error { return nil })
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errLockBusy) {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("sign-in lock waited without a deadline")
	}
}

func race(a, b func() error) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	run := func(fn func() error) {
		defer wg.Done()
		if err := fn(); err == nil {
			mu.Lock()
			wins++
			mu.Unlock()
		}
	}
	wg.Add(2)
	go run(a)
	go run(b)
	wg.Wait()
	return wins
}

func openCoreDB(t *testing.T) *sql.DB {
	t.Helper()
	const dsn = "postgres://togavsu:togavsu@127.0.0.1:5432/togavsu?sslmode=disable"
	admin, err := db.OpenPostgres(dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := admin.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(t.Name()))
	name := "t" + hex.EncodeToString(sum[:8])
	if _, err := admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin, err := db.OpenPostgres(dsn, "")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name)
	})
	plain, err := db.OpenPostgres(fmt.Sprintf("postgres://togavsu:togavsu@127.0.0.1:5432/%s?sslmode=disable", name), "")
	if err != nil {
		t.Fatal(err)
	}
	ms, err := db.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(context.Background(), plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
	_ = plain.Close()
	out, err := db.OpenPostgres(fmt.Sprintf("postgres://togavsu:togavsu@127.0.0.1:5432/%s?sslmode=disable", name), "core")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	return out
}
