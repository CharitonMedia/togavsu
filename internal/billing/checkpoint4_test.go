package billing

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Regression test for the fourth Codex checkpoint finding on PR #27: Service.Events must apply
// client visibility in the service.

func eventActions(evs []domain.Event) map[string]int {
	out := map[string]int{}
	for _, e := range evs {
		out[e.Action]++
	}
	return out
}

func TestEventsHideStaffOnlyFromClient(t *testing.T) {
	f := newFx(t)
	m, contact := f.matter("Events wall", "hourly", 0, 0)
	cl := f.clientUser(contact)
	f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil)
	f.expense(m.ID, 25_00, 0)
	inv := f.sent(m.ID)
	if _, err := f.svc.Invoice(f.ctx, cl, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.WriteOff(f.ctx, f.attorney, inv.ID, 1_00, "courtesy"); err != nil {
		t.Fatal(err)
	}

	// Staff see everything, including staff-only events.
	staff, err := f.svc.Events(f.ctx, f.staff, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	all := eventActions(staff)
	hidden := 0
	for _, e := range staff {
		if !e.ClientVisible {
			hidden++
		}
	}
	if all["invoice.sent"] != 1 || all["invoice.viewed"] != 1 || all["expense.created"] == 0 || hidden == 0 {
		t.Fatalf("staff events: %v (staff-only %d)", all, hidden)
	}

	// The client sees only client-visible events: sent and viewed, never expense or write-off rows.
	got, err := f.svc.Events(f.ctx, cl, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := eventActions(got)
	if seen["invoice.sent"] != 1 || seen["invoice.viewed"] != 1 {
		t.Fatalf("client missing shared events: %v", seen)
	}
	for _, e := range got {
		if !e.ClientVisible {
			t.Fatalf("client got staff-only event %s: %q", e.Action, e.Summary)
		}
	}
	if len(got) != len(staff)-hidden {
		t.Fatalf("client events %d, want %d", len(got), len(staff)-hidden)
	}

	// A client who is not on the matter is refused outright.
	stranger := person(f.id(), domain.RoleClient)
	if _, err := f.svc.Events(f.ctx, stranger, m.ID); err == nil {
		t.Fatal("stranger read matter events")
	}
}
