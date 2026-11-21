package calendar

import (
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestCoreUpdate2GOccupancyMemoryWindow(t *testing.T) {
	f := newFixture(t)
	start := at(t, "2026-11-12 09:00")
	for _, offset := range []time.Duration{-2 * time.Hour, 0, 2 * time.Hour} {
		f.event(f.staff, EventInput{Title: "Synthetic interval", Kind: KindBlock, StartsAt: start.Add(offset), EndsAt: start.Add(offset + time.Hour)})
	}
	walled := f.walled("Synthetic walled occupancy")
	f.event(f.attorney, EventInput{Title: "Hidden", Kind: KindBlock, MatterID: walled.ID, StartsAt: start, EndsAt: start.Add(time.Hour)})
	p := domain.Principal{Source: domain.SourceService, ModuleID: "occupancy-fixture", Roles: []domain.Role{domain.RoleStaff}, Scopes: []string{"calendar:read"}}
	filter := EventFilter{From: start, To: start.Add(time.Hour)}
	rows, err := f.svc.ListOccupancy(f.ctx, p, filter)
	if err != nil || len(rows) != 1 || rows[0].StartsAt != start.UTC().Format(time.RFC3339) {
		t.Fatalf("window: %+v %v", rows, err)
	}
	if err := f.practice.SetWall(f.ctx, f.admin, walled.ID, nil); err != nil {
		t.Fatal(err)
	}
	rows, err = f.svc.ListOccupancy(f.ctx, p, filter)
	if err != nil || len(rows) != 2 {
		t.Fatalf("lifting wall: %+v %v", rows, err)
	}
	p.Scopes = nil
	if _, err := f.svc.ListOccupancy(f.ctx, p, filter); code(err) != domain.CodeForbidden {
		t.Fatalf("missing calendar:read: %v", err)
	}
}
