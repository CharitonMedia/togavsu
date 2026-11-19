package practice

import (
	"context"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestDelegatedModuleActionsInPersonFeedAndCounts(t *testing.T) {
	svc, store := newSvc(t)
	ctx := context.Background()
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, store, staff, "Sam Staff")
	area := makeArea(t, svc, person(tid(1), domain.RoleAdmin), "Probate")
	before, err := svc.Activity(ctx, staff, ActivityQuery{Tab: "staff", UserID: staff.UserID})
	if err != nil {
		t.Fatal(err)
	}
	delegated := staff
	delegated.Source, delegated.ModuleID = domain.SourceModule, "synthetic-module"
	matter := makeMatter(t, svc, delegated, area, "Delegated matter", staff.UserID)
	after, err := svc.Activity(ctx, staff, ActivityQuery{Tab: "staff", UserID: staff.UserID})
	if err != nil {
		t.Fatal(err)
	}
	count := func(view ActivityView) int {
		for _, p := range view.People {
			if p.ID == staff.UserID {
				return p.Count
			}
		}
		return 0
	}
	if count(after) <= count(before) {
		t.Fatalf("person chip excluded delegated actions: before %d after %d", count(before), count(after))
	}
	found := false
	for _, group := range after.Groups {
		for _, row := range group.Rows {
			if row.RecordID == matter.ID {
				found = true
				if row.ActorID != staff.UserID || row.ActorName != "Sam Staff" || row.ModuleID != delegated.ModuleID || row.ActorKind != "staff" {
					t.Fatalf("delegated row: %+v", row)
				}
			}
		}
	}
	if !found {
		t.Fatal("person filter omitted delegated action")
	}
	timeline, err := svc.Timeline(ctx, staff, matter.ID)
	if err != nil || len(timeline) == 0 {
		t.Fatalf("timeline: %v %v", timeline, err)
	}
	for _, row := range timeline {
		if row.ActorID != staff.UserID || row.ModuleID != delegated.ModuleID {
			t.Fatalf("timeline actor/via: %+v", row)
		}
	}
}

// A delegated write belongs to the person's tab and to the Agents/module filters.
func TestDelegatedModuleActionsInAgentAndModuleFilters(t *testing.T) {
	svc, store := newSvc(t)
	ctx := context.Background()
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, store, staff, "Sam Staff")
	for i, kind := range []string{"staff", "client"} {
		event := domain.Event{Meta: domain.Meta{ID: tid(uint64(80 + i)), CreatedAt: svc.clock()}, ActorKind: kind, ActorID: staff.UserID, ModuleID: "synthetic-module", Summary: kind + " via module", Action: "module.did"}
		if err := store.AppendAudit(ctx, nil, []domain.Event{event}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tab := range []string{"agents", "module", "staff", "clients"} {
		view, err := svc.Activity(ctx, staff, ActivityQuery{Tab: tab, UserID: staff.UserID})
		if err != nil {
			t.Fatal(err)
		}
		if view.Agents != 2 {
			t.Fatalf("%s Agents count: %+v", tab, view)
		}
		for _, kind := range []string{"staff", "client"} {
			want := tab == "agents" || tab == "module" || tab == "staff" && kind == "staff" || tab == "clients" && kind == "client"
			if feedHas(view.Groups, kind+" via module") != want {
				t.Fatalf("%s lost/added %s row: %+v", tab, kind, view.Groups)
			}
		}
	}
}
