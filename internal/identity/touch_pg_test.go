package identity

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// TestTouchCommitsWithoutFlushWaitAndKeepsRevocation: a session touch on its own commits
// with synchronous_commit off (AC-017), so it must still slide a live session's idle time,
// refuse a revoked or unknown session, leave the pooled connection's setting as it was,
// and inside a caller's transaction keep that transaction synchronous and roll back with it.
func TestTouchCommitsWithoutFlushWaitAndKeepsRevocation(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := audit.InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyReference(ctx, sqlDB, "postgres"); err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)}
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
	// From here every statement shares one connection, so SHOW reads the connection the
	// touch ran on. A touch in WithTx uses that transaction, never a second connection.
	sqlDB.SetMaxOpenConns(1)
	sessions := store.Sessions().(pgSessions)
	id := started.SessionID
	idle := func() time.Time {
		t.Helper()
		row, err := sessions.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return row.IdleExpiresAt.UTC()
	}
	// On its own: the slide applies and the pool's connection is synchronous again after.
	slid := clk.Now().Add(2 * time.Hour).Truncate(time.Microsecond)
	if err := sessions.Touch(ctx, id, slid, slid, clk.Now()); err != nil {
		t.Fatal(err)
	}
	if got := idle(); !got.Equal(slid) {
		t.Fatalf("idle expiry %s after touch, want %s", got, slid)
	}
	var setting string
	if err := sqlDB.QueryRowContext(ctx, `SHOW synchronous_commit`).Scan(&setting); err != nil || setting != "on" {
		t.Fatalf("pooled connection synchronous_commit %q after a touch (%v)", setting, err)
	}

	// Inside a caller's transaction: that transaction stays synchronous, and the touch
	// rolls back with it.
	rolled := slid.Add(time.Hour)
	errRollback := errors.New("roll back")
	err = audit.WithTx(ctx, sqlDB, func(ctx context.Context) error {
		if err := sessions.Touch(ctx, id, rolled, rolled, clk.Now()); err != nil {
			return err
		}
		var in string
		if err := audit.Tx(ctx, sqlDB).QueryRowContext(ctx, `SHOW synchronous_commit`).Scan(&in); err != nil {
			return err
		}
		if in != "on" {
			t.Errorf("outer transaction synchronous_commit %q after a touch", in)
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	if got := idle(); !got.Equal(slid) {
		t.Fatalf("idle expiry %s after a rolled-back touch, want %s", got, slid)
	}

	// A revoked session and an unknown one are refused, and the revoked one keeps its
	// idle expiry.
	row, err := sessions.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	revoked := clk.Now()
	row.RevokedAt, row.UpdatedAt = &revoked, revoked
	if err := sessions.Update(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Touch(ctx, id, rolled, rolled, clk.Now()); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("touch on a revoked session: %v", err)
	}
	if got := idle(); !got.Equal(slid) {
		t.Fatalf("revoked session's idle expiry moved to %s", got)
	}
	if err := sessions.Touch(ctx, "019b6968-4600-7001-8000-0000000000ff", rolled, rolled, clk.Now()); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("touch on an unknown session: %v", err)
	}
	if _, err := svc.Authenticate(ctx, Presented{SessionToken: started.SessionToken}); err == nil {
		t.Fatal("a revoked session authenticated")
	}
}
