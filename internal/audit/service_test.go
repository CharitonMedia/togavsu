package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func actor() domain.Principal {
	return domain.Principal{
		UserID: domain.SystemUserID,
		Roles:  []domain.Role{domain.RoleAttorney},
		Source: domain.SourceUI,
	}
}

func TestRecordKeepsActorAndRedactsSecrets(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	svc, err := New(mem, func() time.Time { return time.Unix(1_700_000_000, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	who := actor()
	who.TokenID = domain.SystemUserID
	row, err := svc.Record(ctx, who, Change{
		Entity: "matter", EntityID: domain.SystemUserID, Field: "name", Old: "Old", New: "New", Reason: "rename",
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.ActorID != who.UserID || row.Role != "attorney" || row.Source != string(domain.SourceUI) {
		t.Fatalf("provenance %+v", row)
	}
	if !strings.Contains(row.Reason, "token=") || !strings.Contains(row.Reason, "rename") {
		t.Fatal(row.Reason)
	}
	secret, err := svc.Record(ctx, who, Change{
		Entity: "user", EntityID: who.UserID, Field: "password", Old: "before-secret", New: "after-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if secret.OldValue != Redacted || secret.NewValue != Redacted {
		t.Fatal(secret)
	}
	blob := secret.OldValue + secret.NewValue + secret.Reason
	if strings.Contains(blob, "before-secret") || strings.Contains(blob, "after-secret") {
		t.Fatal(blob)
	}
	rows, err := svc.ForEntity(ctx, "matter", domain.SystemUserID)
	if err != nil || len(rows) != 1 || rows[0].NewValue != "New" {
		t.Fatal(rows, err)
	}
}

func TestMutateLeavesRows(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	svc, err := New(mem, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	row, err := svc.Record(ctx, actor(), Change{
		Entity: "matter", EntityID: domain.SystemUserID, Field: "name", New: "Kept",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Mutate(ctx, row.ID); !errors.Is(err, ErrAppendOnly) {
		t.Fatal(err)
	}
	if err := mem.Update(ctx, row); !errors.Is(err, ErrAppendOnly) {
		t.Fatal(err)
	}
	if err := mem.Delete(ctx, row.ID); !errors.Is(err, ErrAppendOnly) {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, row.ID)
	if err != nil || got.NewValue != "Kept" || mem.Len() != 1 {
		t.Fatal(got, err, mem.Len())
	}
}

func TestSettingChangeRecordsRedactedSecret(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	svc, err := New(mem, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	err = svc.SettingChanged(ctx, settings.Change{
		Key: "backups.passphrase", Old: settings.Redacted, New: settings.Redacted,
		Actor: domain.SystemUserID, Role: "admin", Source: domain.SourceUI, Redacted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := mem.List(ctx, repository.Filter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	if page.Items[0].Field != "backups.passphrase" || page.Items[0].NewValue != Redacted {
		t.Fatal(page.Items[0])
	}
}

func TestRecordRejectsAnonymousUser(t *testing.T) {
	svc, err := New(NewMemory(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Record(context.Background(), domain.Principal{Source: domain.SourceUI}, Change{
		Entity: "matter", EntityID: domain.SystemUserID, Field: "name", New: "x",
	})
	var api *domain.APIError
	if !errors.As(err, &api) || api.Code != domain.CodeUnauthenticated {
		t.Fatal(err)
	}
}
