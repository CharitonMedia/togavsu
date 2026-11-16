package calendar

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

func TestListClientAppointments(t *testing.T) {
	f := newFixture(t)
	mine := f.matter("Casey Estate")
	other := f.matter("Other Client")
	ct, err := f.practice.CreateContact(f.ctx, f.admin, practice.ContactInput{Kind: "person", FullName: "Casey Client"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.practice.AddParty(f.ctx, f.admin, mine.ID, practice.PartyInput{ContactID: ct.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	u, err := f.pm.User(f.ctx, f.client.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.ContactID = ct.ID
	if err := f.pm.PutUser(f.ctx, u); err != nil {
		t.Fatal(err)
	}

	meeting := f.event(f.staff, EventInput{
		Title: "Intake", Kind: KindClientMeeting, MatterID: mine.ID,
		StartsAt: at(t, "2026-11-12 10:00"), EndsAt: at(t, "2026-11-12 11:00"), Location: "Zoom",
	})
	hearing := f.event(f.staff, EventInput{
		Title: "Probate hearing", Kind: KindCourtDate, MatterID: mine.ID,
		StartsAt: at(t, "2026-11-20 09:00"), EndsAt: at(t, "2026-11-20 10:30"), Location: "Courtroom 3",
	})
	f.event(f.staff, EventInput{
		Title: "Past meeting", Kind: KindClientMeeting, MatterID: mine.ID,
		StartsAt: at(t, "2026-11-01 10:00"), EndsAt: at(t, "2026-11-01 11:00"),
	})
	f.event(f.staff, EventInput{
		Title: "Internal block", Kind: KindBlock, MatterID: mine.ID,
		StartsAt: at(t, "2026-11-15 08:00"), EndsAt: at(t, "2026-11-15 17:00"),
	})
	f.event(f.staff, EventInput{
		Title: "Foreign meeting", Kind: KindClientMeeting, MatterID: other.ID,
		StartsAt: at(t, "2026-11-13 14:00"), EndsAt: at(t, "2026-11-13 15:00"),
	})
	f.event(f.staff, EventInput{
		Title: "No matter", Kind: KindClientMeeting,
		StartsAt: at(t, "2026-11-14 12:00"), EndsAt: at(t, "2026-11-14 13:00"),
	})

	if _, err := f.svc.ListClientAppointments(f.ctx, f.staff); code(err) != domain.CodeForbidden {
		t.Fatalf("staff: %v", err)
	}
	rows, err := f.svc.ListClientAppointments(f.ctx, f.client)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 got %d: %+v", len(rows), rows)
	}
	if rows[0].Title != meeting.Title || rows[0].Kind != KindClientMeeting || rows[0].Location != "Zoom" || rows[0].MatterID != mine.ID {
		t.Fatalf("first: %+v", rows[0])
	}
	if rows[1].Title != hearing.Title || rows[1].Kind != KindCourtDate {
		t.Fatalf("second: %+v", rows[1])
	}
	if !rows[0].Start.Before(rows[1].Start) {
		t.Fatalf("order: %v then %v", rows[0].Start, rows[1].Start)
	}
}
