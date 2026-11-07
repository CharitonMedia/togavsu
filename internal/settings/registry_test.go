package settings_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func actor() domain.Principal {
	return domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
}

func TestRegistrySetResetAndReload(t *testing.T) {
	ctx := context.Background()
	mem := settings.NewMemory()
	audit := &recordAudit{}
	at := time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	reg, err := settings.NewWithClock(mem, audit, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	other, err := settings.NewWithClock(mem, audit, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "no.such", "1", actor()); !errors.Is(err, settings.ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "billing.time_increment_minutes", "5", actor()); !errors.Is(err, settings.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "billing.time_increment_minutes", "1", domain.Principal{}); !errors.Is(err, settings.ErrActor) {
		t.Fatal(err)
	}
	if got, err := reg.Get(ctx, "billing.time_increment_minutes"); err != nil || got != "6" {
		t.Fatalf("unchanged %s %v", got, err)
	}
	change, err := reg.Set(ctx, "billing.time_increment_minutes", "1", actor())
	if err != nil {
		t.Fatal(err)
	}
	if change.Old != "6" || change.New != "1" || len(audit.changes) != 1 {
		t.Fatalf("%+v audits %d", change, len(audit.changes))
	}
	if got, err := other.Get(ctx, "billing.time_increment_minutes"); err != nil || got != "1" {
		t.Fatalf("next reader got %s %v", got, err)
	}
	if _, err := reg.Reset(ctx, "billing.time_increment_minutes", actor()); err != nil {
		t.Fatal(err)
	}
	if got, err := reg.Get(ctx, "billing.time_increment_minutes"); err != nil || got != "6" {
		t.Fatal(got, err)
	}
	if _, ok, err := mem.Get(ctx, "billing.time_increment_minutes"); err != nil || ok {
		t.Fatal("reset left an override", ok, err)
	}
	if _, err := reg.Set(ctx, "security.totp_required.admin", "false", actor()); !errors.Is(err, settings.ErrReadOnly) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "files.encrypt_s3", "false", actor()); !errors.Is(err, settings.ErrReadOnly) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "doctender.naming_pattern", "<status>.<ext>", actor()); !errors.Is(err, settings.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "doctender.naming_pattern", "<clientlast>-<what-it-is>.<ext>", actor()); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "doctender.review_confidence", "1.5", actor()); !errors.Is(err, settings.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "practice.conflict_score_minimum", "101", actor()); !errors.Is(err, settings.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "home.max_saved_layouts", "0", actor()); !errors.Is(err, settings.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "trust.reconciliation_reminder_day", "29", actor()); !errors.Is(err, settings.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "firm.time_zone", "Not/AZone", actor()); !errors.Is(err, settings.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := reg.Set(ctx, "firm.phone", "555", actor()); !errors.Is(err, settings.ErrInvalid) {
		t.Fatal(err)
	}
	for _, bad := range []string{"NaN", "nan", "+Inf", "-Inf", "Inf"} {
		if _, err := reg.Set(ctx, "doctender.review_confidence", bad, actor()); !errors.Is(err, settings.ErrInvalid) {
			t.Fatalf("%s accepted: %v", bad, err)
		}
	}
}

func TestRegistryAtomicAudit(t *testing.T) {
	ctx := context.Background()
	mem := settings.NewMemory()
	audit := &recordAudit{}
	reg, err := settings.New(mem, audit)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	entered := make(chan struct{})
	gate := &gateAudit{next: audit, entered: entered, release: release}
	regA, err := settings.New(mem, gate)
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := regA.Set(ctx, "billing.time_increment_minutes", "10", actor())
		errCh <- err
	}()
	<-entered
	gate.fail = true
	close(release)
	if err := <-errCh; err == nil {
		t.Fatal("expected audit failure")
	}
	if got, err := reg.Get(ctx, "billing.time_increment_minutes"); err != nil || got != "6" {
		t.Fatalf("failed write visible: %s %v", got, err)
	}
	if _, err := reg.Set(ctx, "billing.time_increment_minutes", "15", actor()); err != nil {
		t.Fatal(err)
	}
	if got, err := reg.Get(ctx, "billing.time_increment_minutes"); err != nil || got != "15" {
		t.Fatalf("later write lost: %s %v", got, err)
	}
	if len(audit.changes) != 1 || audit.changes[0].New != "15" {
		t.Fatalf("audits %+v", audit.changes)
	}
}

func TestRegistryCancelBeforeCommit(t *testing.T) {
	mem := settings.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	reg, err := settings.New(mem, cancelAudit{cancel: cancel})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Set(ctx, "billing.time_increment_minutes", "10", actor())
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got, err := reg.Get(context.Background(), "billing.time_increment_minutes"); err != nil || got != "6" {
		t.Fatalf("cancelled write visible: %s %v", got, err)
	}
}

type gateAudit struct {
	next    *recordAudit
	entered chan struct{}
	release chan struct{}
	fail    bool
	once    bool
}

func (g *gateAudit) SettingChanged(ctx context.Context, c settings.Change) error {
	if !g.once {
		g.once = true
		g.entered <- struct{}{}
		<-g.release
	}
	if g.fail {
		return errors.New("audit down")
	}
	return g.next.SettingChanged(ctx, c)
}

type cancelAudit struct {
	cancel context.CancelFunc
}

func (c cancelAudit) SettingChanged(ctx context.Context, _ settings.Change) error {
	c.cancel()
	return ctx.Err()
}

func TestSecretAuditAndList(t *testing.T) {
	ctx := context.Background()
	mem := settings.NewMemory()
	audit := &recordAudit{}
	reg, err := settings.New(mem, audit)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "smtp-password-value"
	change, err := reg.Set(ctx, "mail.smtp_password", secret, actor())
	if err != nil {
		t.Fatal(err)
	}
	if !change.Redacted || change.Old != settings.Redacted || change.New != settings.Redacted || strings.Contains(change.Old+change.New, secret) {
		t.Fatal(change)
	}
	if len(audit.changes) != 1 || strings.Contains(audit.changes[0].Old+audit.changes[0].New, secret) {
		t.Fatal(audit.changes)
	}
	got, err := reg.Get(ctx, "mail.smtp_password")
	if err != nil || got != secret {
		t.Fatal(got, err)
	}
	list, err := reg.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range list {
		if entry.Key == "mail.smtp_password" {
			if entry.Value != settings.Redacted || !entry.SecretSet || strings.Contains(entry.Value, secret) {
				t.Fatal(entry)
			}
		}
		if strings.Contains(entry.Value, secret) {
			t.Fatal(entry.Key)
		}
	}
	audit.fail = true
	if _, err := reg.Set(ctx, "mail.smtp_password", "next-secret", actor()); err == nil {
		t.Fatal("audit failure stored")
	}
	got, err = reg.Get(ctx, "mail.smtp_password")
	if err != nil || got != secret {
		t.Fatalf("rollback got %s %v", got, err)
	}
}

func TestSealingStore(t *testing.T) {
	ctx := context.Background()
	mem := settings.NewMemory()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	secretKey := func(k string) bool { return k == "mail.smtp_password" }
	sealed, err := settings.NewSealingStore(mem, key, secretKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settings.NewSealingStore(mem, key[:16], secretKey); err == nil {
		t.Fatal("short key accepted")
	}
	reg, err := settings.New(sealed, &recordAudit{})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "live-api-key"
	if _, err := reg.Set(ctx, "mail.smtp_password", secret, actor()); err != nil {
		t.Fatal(err)
	}
	raw, ok, err := sealed.Raw(ctx, "mail.smtp_password")
	if err != nil || !ok || strings.Contains(raw, secret) || !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("raw %q %v", raw, err)
	}
	got, err := reg.Get(ctx, "mail.smtp_password")
	if err != nil || got != secret {
		t.Fatal(got, err)
	}
	const literal = "enc:v1:example"
	if _, err := reg.Set(ctx, "llms.firm_paragraph", literal, actor()); err != nil {
		t.Fatal(err)
	}
	if got, err := reg.Get(ctx, "llms.firm_paragraph"); err != nil || got != literal {
		t.Fatalf("non-secret prefix: %q %v", got, err)
	}
	list, err := reg.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range list {
		if entry.Key == "llms.firm_paragraph" && entry.Value != literal {
			t.Fatal(entry.Value)
		}
	}
	if _, err := reg.Set(ctx, "llms.firm_paragraph", "firm text", actor()); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Reset(ctx, "llms.firm_paragraph", actor()); err != nil {
		t.Fatal(err)
	}
	wrong, err := settings.NewSealingStore(mem, bytesKey(9), secretKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wrong.Get(ctx, "mail.smtp_password"); err == nil {
		t.Fatal("wrong key opened the secret")
	}
}

func bytesKey(fill byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = fill
	}
	return key
}

type recordAudit struct {
	changes []settings.Change
	fail    bool
}

func (a *recordAudit) SettingChanged(_ context.Context, c settings.Change) error {
	if a.fail {
		return errors.New("audit down")
	}
	a.changes = append(a.changes, c)
	return nil
}
