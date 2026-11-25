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

// failCaseAudit fails one external notification after the values commit.
type failCaseAudit struct {
	failKey string
	keys    []string
}

func (a *failCaseAudit) SettingChanged(_ context.Context, change settings.Change) error {
	a.keys = append(a.keys, change.Key)
	if change.Key == a.failKey {
		return errors.New("external audit unavailable")
	}
	return nil
}

func TestSetCaseNumberExamplesCommittedAuditFailure(t *testing.T) {
	for _, key := range []string{"practice.case_number_examples", "practice.case_number_patterns"} {
		t.Run(key, func(t *testing.T) {
			ctx := context.Background()
			audit := &failCaseAudit{failKey: key}
			reg, err := settings.New(settings.NewMemory(), audit)
			if err != nil {
				t.Fatal(err)
			}
			who := domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
			examples := `["2026-CV-1"]`
			change, err := reg.Set(ctx, "practice.case_number_examples", examples, who)
			if err != nil || change.Key != "practice.case_number_examples" {
				t.Fatalf("committed save reported as failed: %+v %v", change, err)
			}
			for k, want := range map[string]string{
				"practice.case_number_examples": examples,
				"practice.case_number_patterns": `["^\\d{4}-[A-Z]{2}-\\d+$"]`,
			} {
				if got, err := reg.Get(ctx, k); err != nil || got != want {
					t.Fatalf("%s: %q, want %q: %v", k, got, want, err)
				}
			}
			if len(audit.keys) != 2 || audit.keys[0] != "practice.case_number_examples" || audit.keys[1] != "practice.case_number_patterns" {
				t.Fatalf("both notifications must be attempted: %v", audit.keys)
			}
			if _, err := reg.Reset(ctx, "practice.case_number_examples", who); err != nil {
				t.Fatalf("committed reset reported as failed: %v", err)
			}
			for _, def := range settings.Definitions() {
				if def.Key == "practice.case_number_examples" || def.Key == "practice.case_number_patterns" {
					if got, err := reg.Get(ctx, def.Key); err != nil || got != def.Default {
						t.Fatalf("reset %s: %q %v", def.Key, got, err)
					}
				}
			}
		})
	}
}
