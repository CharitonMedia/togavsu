package access

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// unitStore is an audit store with transactions: writes staged inside Atomic are
// kept only when the unit returns no error.
type unitStore struct {
	*audit.Memory
	failAppend bool
	staged     []string
	kept       []string
}

func (u *unitStore) Atomic(ctx context.Context, fn func(context.Context) error) error {
	u.staged = nil
	if err := fn(ctx); err != nil {
		u.staged = nil
		return err
	}
	u.kept = append(u.kept, u.staged...)
	return nil
}

func (u *unitStore) Append(ctx context.Context, row domain.AuditEntry) error {
	if u.failAppend {
		return errors.New("audit append failed")
	}
	return u.Memory.Append(ctx, row)
}

func TestApplyKeepsWriteOnlyWithItsAuditRow(t *testing.T) {
	ctx := context.Background()
	g, _ := newGate(t)
	store := &unitStore{Memory: audit.NewMemory()}
	log, err := audit.New(store, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	attorney := domain.Principal{UserID: "atty", Roles: []domain.Role{domain.RoleAttorney}, Source: domain.SourceUI}
	ref := domain.ResourceRef{Resource: domain.ResInvoice, Action: domain.ActionApproveInvoice, MatterID: openMatter}
	change := audit.Change{Entity: "invoice", EntityID: openMatter, Field: "status", Old: "draft", New: "approved"}
	write := func(context.Context) error {
		store.staged = append(store.staged, "approved")
		return nil
	}
	store.failAppend = true
	if err := g.Apply(ctx, attorney, ref, change, log, write); err == nil {
		t.Fatal("apply succeeded with a failed audit append")
	}
	if len(store.kept) != 0 {
		t.Fatalf("write kept without its audit row: %v", store.kept)
	}
	store.failAppend = false
	if err := g.Apply(ctx, attorney, ref, change, log, write); err != nil {
		t.Fatal(err)
	}
	rows, err := log.ForEntity(ctx, "invoice", openMatter)
	if err != nil || len(store.kept) != 1 || len(rows) != 1 {
		t.Fatal(store.kept, rows, err)
	}
	if err := g.Apply(ctx, attorney, ref, change, nil, write); err == nil {
		t.Fatal("apply wrote without an audit log")
	}
}
