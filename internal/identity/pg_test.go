package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
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
