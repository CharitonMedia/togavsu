package calendar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestCoreUpdate2IActiveAssignmentMemoryAndErrors(t *testing.T) {
	f := newFixture(t)
	walled := f.walled("Synthetic booking")
	start := at(t, "2026-11-24 10:00")
	f.event(f.attorney, EventInput{Title: "Private", Kind: KindBlock, MatterID: walled.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Attendees: []domain.ID{f.other.UserID}})
	p := domain.Principal{Source: domain.SourceService, ModuleID: "booking", Roles: []domain.Role{domain.RoleStaff}, Scopes: []string{"calendar:read"}}
	filter := EventFilter{From: start, To: start.Add(time.Hour)}
	read := func(want int) {
		t.Helper()
		rows, err := f.svc.ListOccupancy(f.ctx, p, filter)
		if err != nil || len(rows) != want {
			t.Fatalf("occupancy %v %v", rows, err)
		}
	}
	read(0)
	f.deactivate(f.other.UserID)
	read(1)
	u, err := f.pm.User(f.ctx, f.other.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.Active = true
	if err := f.pm.PutUser(f.ctx, u); err != nil {
		t.Fatal(err)
	}
	read(0)
	failure := errors.New("synthetic user lookup failure")
	f.svc.d.Users = UsersFunc(func(context.Context, domain.ID) (domain.User, error) { return domain.User{}, failure })
	if rows, err := f.svc.ListOccupancy(f.ctx, p, filter); !errors.Is(err, failure) || rows != nil {
		t.Fatalf("user lookup must fail closed: %v %v", rows, err)
	}
}

func TestCoreUpdate2IVideoMemoryOwnership(t *testing.T) {
	f := newFixture(t)
	start := at(t, "2026-11-24 10:00")
	provider := "synthetic-provider"
	video := &domain.CalendarVideo{Requested: true, Provider: &provider}
	e := f.event(f.staff, EventInput{Title: "Video", Kind: KindClientMeeting, StartsAt: start, EndsAt: start.Add(time.Hour), Video: video})
	provider = "mutated-input"
	*e.Video.Provider = "mutated-result"
	got, err := f.svc.GetEvent(f.ctx, f.staff, e.ID)
	if err != nil || *got.Video.Provider != "synthetic-provider" {
		t.Fatalf("caller mutated stored video: %+v %v", got.Video, err)
	}
	*got.Video.Provider = "mutated-read"
	got, err = f.svc.GetEvent(f.ctx, f.staff, e.ID)
	if err != nil || *got.Video.Provider != "synthetic-provider" {
		t.Fatal("read aliases stored video")
	}
}
