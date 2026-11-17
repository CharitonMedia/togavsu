package access

import (
	"context"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

type matterFake struct {
	rows map[domain.ID]domain.Matter
}

func (m matterFake) Insert(context.Context, domain.Matter) error { return nil }
func (m matterFake) Update(context.Context, domain.Matter) error { return nil }
func (m matterFake) Get(_ context.Context, id domain.ID) (domain.Matter, error) {
	row, ok := m.rows[id]
	if !ok {
		return domain.Matter{}, repository.ErrNotFound
	}
	return row, nil
}
func (m matterFake) List(context.Context, repository.Filter) (repository.Page[domain.Matter], error) {
	return repository.Page[domain.Matter]{}, nil
}

func TestClientMatterAccessWindow(t *testing.T) {
	f := &fake{}
	f.seed()
	now := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	matters := matterFake{rows: map[domain.ID]domain.Matter{
		openMatter: {
			Meta: domain.Meta{ID: openMatter}, Status: "open", OpenedOn: "2025-12-01",
		},
		"matter-closed-fresh": {
			Meta: domain.Meta{ID: "matter-closed-fresh"}, Status: "closed", OpenedOn: "2025-11-01", ClosedOn: "2026-01-10",
		},
		"matter-closed-stale": {
			Meta: domain.Meta{ID: "matter-closed-stale"}, Status: "closed", OpenedOn: "2025-10-01", ClosedOn: "2025-12-01",
		},
	}}
	f.parties = append(f.parties,
		domain.MatterParty{Meta: domain.Meta{ID: "party-2"}, MatterID: "matter-closed-fresh", ContactID: contactID, Role: "client"},
		domain.MatterParty{Meta: domain.Meta{ID: "party-3"}, MatterID: "matter-closed-stale", ContactID: contactID, Role: "client"},
	)
	reader := Reader{
		Walls: wallsFake{f}, Parties: partyFake{f}, Users: userFake{f},
		Matters: matters, Now: func() time.Time { return now },
		Days: func(context.Context) int { return domain.ClientAccessDaysAfterClose },
	}
	g := New(reader)
	ctx := context.Background()

	if err := g.Allow(ctx, client(), domain.ResourceRef{
		Resource: domain.ResMatter, Action: domain.ActionRead, MatterID: openMatter, SharedWithClient: true,
	}); err != nil {
		t.Fatalf("open matter: %v", err)
	}
	if err := g.Allow(ctx, client(), domain.ResourceRef{
		Resource: domain.ResMatter, Action: domain.ActionRead, MatterID: "matter-closed-fresh", SharedWithClient: true,
	}); err != nil {
		t.Fatalf("fresh closed: %v", err)
	}
	if err := g.Allow(ctx, client(), domain.ResourceRef{
		Resource: domain.ResMatter, Action: domain.ActionRead, MatterID: "matter-closed-stale", SharedWithClient: true,
	}); !IsNotFound(err) {
		t.Fatalf("stale closed: %v", err)
	}

	// Reopening restores access: status open even with a prior closed_on.
	matters.rows["matter-closed-stale"] = domain.Matter{
		Meta: domain.Meta{ID: "matter-closed-stale"}, Status: "open", OpenedOn: "2025-10-01", ClosedOn: "2025-12-01",
	}
	if err := g.Allow(ctx, client(), domain.ResourceRef{
		Resource: domain.ResMatter, Action: domain.ActionRead, MatterID: "matter-closed-stale", SharedWithClient: true,
	}); err != nil {
		t.Fatalf("reopened: %v", err)
	}
}
