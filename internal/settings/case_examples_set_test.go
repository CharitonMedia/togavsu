package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func TestSetCaseNumberExamplesDerivesPatterns(t *testing.T) {
	ctx := context.Background()
	mem := settings.NewMemory()
	audit := &recordAudit{}
	reg, err := settings.NewWithClock(mem, audit, func() time.Time {
		return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	examples, _ := json.Marshal([]string{"23AR-CV00825", "2026-CV-1"})
	who := domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
	if _, err := reg.Set(ctx, "practice.case_number_examples", string(examples), who); err != nil {
		t.Fatal(err)
	}
	gotEx, err := reg.Get(ctx, "practice.case_number_examples")
	if err != nil || gotEx != string(examples) {
		t.Fatalf("examples: %q %v", gotEx, err)
	}
	gotPat, err := reg.Get(ctx, "practice.case_number_patterns")
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	if err := json.Unmarshal([]byte(gotPat), &patterns); err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 2 || patterns[0] != domain.DefaultCasePattern || patterns[1] != `^\d{4}-[A-Z]{2}-\d+$` {
		t.Fatalf("patterns: %#v", patterns)
	}
	if len(audit.changes) < 2 {
		t.Fatalf("want audit for examples and patterns, got %d", len(audit.changes))
	}
	keys := map[string]bool{}
	for _, c := range audit.changes {
		keys[c.Key] = true
	}
	if !keys["practice.case_number_examples"] || !keys["practice.case_number_patterns"] {
		t.Fatalf("audit keys: %v", keys)
	}
}

// failPatternsStore refuses the derived-pattern write, so the paired change rolls back.
type failPatternsStore struct{ settings.Store }

func (s failPatternsStore) Put(ctx context.Context, key, value string, at time.Time, actor domain.ID) error {
	if key == "practice.case_number_patterns" {
		return errors.New("store down")
	}
	return s.Store.Put(ctx, key, value, at, actor)
}

func (s failPatternsStore) Transact(ctx context.Context, fn func(settings.Store) error) error {
	return s.Store.Transact(ctx, func(tx settings.Store) error { return fn(failPatternsStore{tx}) })
}

func TestSetCaseNumberExamplesFailedPairRecordsNoAudit(t *testing.T) {
	ctx := context.Background()
	audit := &recordAudit{}
	reg, err := settings.New(failPatternsStore{settings.NewMemory()}, audit)
	if err != nil {
		t.Fatal(err)
	}
	examples, _ := json.Marshal([]string{"2026-CV-1"})
	who := domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
	if _, err := reg.Set(ctx, "practice.case_number_examples", string(examples), who); err == nil {
		t.Fatal("want the paired write to fail")
	}
	if len(audit.changes) != 0 {
		t.Fatalf("a failed pair must record no audit event, got %d", len(audit.changes))
	}
	got, err := reg.Get(ctx, "practice.case_number_examples")
	if err != nil {
		t.Fatal(err)
	}
	if got == string(examples) {
		t.Fatal("examples were stored though the pair failed")
	}
}
