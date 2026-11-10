package calendar

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func (f *fixture) deactivate(id domain.ID) {
	f.t.Helper()
	u, err := f.pm.User(f.ctx, id)
	if err != nil {
		f.t.Fatal(err)
	}
	u.Active = false
	if err := f.pm.PutUser(f.ctx, u); err != nil {
		f.t.Fatal(err)
	}
}

// PRD 8.5: an overdue deadline shows until marked done or waived. A departed
// (deactivated) responsible attorney must not block closing it.
func TestDeadlineInactiveResponsibleCanClose(t *testing.T) {
	f := newFixture(t)
	m := f.matter("Case")
	d := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Answer", DueOn: "2026-11-05", ResponsibleUserID: f.other.UserID})
	w := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Reply", DueOn: "2026-11-06", ResponsibleUserID: f.other.UserID})
	f.deactivate(f.other.UserID)
	if over, _ := f.svc.Overdue(f.ctx, f.staff); len(over) != 2 {
		t.Fatalf("setup overdue: %+v", over)
	}

	done := StatusDone
	got, err := f.svc.UpdateDeadline(f.ctx, f.staff, d.ID, DeadlinePatch{Status: &done})
	if err != nil {
		t.Fatalf("mark done with inactive responsible: %v", err)
	}
	if got.Status != StatusDone || got.DoneBy != f.staff.UserID || got.ResponsibleUserID != f.other.UserID {
		t.Fatalf("done stored wrong: %+v", got)
	}
	waive := StatusWaived
	if _, err := f.svc.UpdateDeadline(f.ctx, f.staff, w.ID, DeadlinePatch{Status: &waive}); err != nil {
		t.Fatalf("waive with inactive responsible: %v", err)
	}
	if over, _ := f.svc.Overdue(f.ctx, f.staff); len(over) != 0 {
		t.Fatalf("closed deadlines still overdue: %+v", over)
	}
	// Other unrelated edits work too; explicitly keeping the same user is not a reassignment.
	title, same := "Answer (amended)", f.other.UserID
	if _, err := f.svc.UpdateDeadline(f.ctx, f.staff, d.ID, DeadlinePatch{Title: &title, ResponsibleUserID: &same}); err != nil {
		t.Fatalf("title edit: %v", err)
	}

	// Failure paths: assigning an inactive user is still refused, on update and create.
	open := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Other", DueOn: "2026-12-01", ResponsibleUserID: f.staff.UserID})
	gone := f.other.UserID
	if _, err := f.svc.UpdateDeadline(f.ctx, f.staff, open.ID, DeadlinePatch{ResponsibleUserID: &gone}); code(err) != domain.CodeValidation {
		t.Fatalf("reassign to inactive: %v", err)
	}
	empty := domain.ID("")
	if _, err := f.svc.UpdateDeadline(f.ctx, f.staff, open.ID, DeadlinePatch{ResponsibleUserID: &empty}); code(err) != domain.CodeValidation {
		t.Fatalf("clear responsible: %v", err)
	}
	if _, err := f.svc.CreateDeadline(f.ctx, f.staff, DeadlineInput{MatterID: m.ID, Title: "New", DueOn: "2026-12-01", ResponsibleUserID: gone}); code(err) != domain.CodeValidation {
		t.Fatalf("create with inactive: %v", err)
	}
	if _, err := f.svc.BulkDeadlines(f.ctx, f.staff, []DeadlineInput{{MatterID: m.ID, Title: "Bulk", DueOn: "2026-12-01", ResponsibleUserID: gone}}); code(err) != domain.CodeValidation {
		t.Fatalf("bulk create with inactive: %v", err)
	}
	// Reassigning the closed deadline to an active user works.
	staff := f.staff.UserID
	if got, err := f.svc.UpdateDeadline(f.ctx, f.staff, d.ID, DeadlinePatch{ResponsibleUserID: &staff}); err != nil || got.ResponsibleUserID != staff {
		t.Fatalf("reassign to active: %+v %v", got, err)
	}
	// Walls still apply: an off-wall user cannot close a walled deadline of a departed attorney.
	secret := f.walled("Sealed")
	sd := f.deadline(f.attorney, DeadlineInput{MatterID: secret.ID, Title: "Sealed", DueOn: "2026-11-05", ResponsibleUserID: f.attorney.UserID})
	f.deactivate(f.attorney.UserID)
	if _, err := f.svc.UpdateDeadline(f.ctx, f.staff, sd.ID, DeadlinePatch{Status: &done}); code(err) != domain.CodeNotFound {
		t.Fatalf("walled close: %v", err)
	}
}

func TestDeadlineStatusFormInactiveResponsible(t *testing.T) {
	f := newFixture(t)
	srv := f.server()
	m := f.matter("Case")
	d := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Answer", DueOn: "2026-11-05", ResponsibleUserID: f.other.UserID})
	w := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Reply", DueOn: "2026-11-06", ResponsibleUserID: f.other.UserID})
	f.deactivate(f.other.UserID)
	for _, c := range []struct {
		id     domain.ID
		status string
	}{{d.ID, StatusDone}, {w.ID, StatusWaived}} {
		status, loc := postForm(t, srv.URL, f.staff, DeadlinesPath+"/"+string(c.id)+"/status", url.Values{"status": {c.status}})
		if status != http.StatusSeeOther || loc != DeadlinesPath+"/"+string(c.id) {
			t.Fatalf("%s form: %d %s", c.status, status, loc)
		}
		got, err := f.svc.GetDeadline(f.ctx, f.staff, c.id)
		if err != nil || got.Status != c.status {
			t.Fatalf("%s not stored: %+v %v", c.status, got, err)
		}
	}
	if over, _ := f.svc.Overdue(f.ctx, f.staff); len(over) != 0 {
		t.Fatalf("still overdue after form: %+v", over)
	}
	// A bad status through the form is still refused.
	if status, _ := postForm(t, srv.URL, f.staff, DeadlinesPath+"/"+string(d.ID)+"/status", url.Values{"status": {"late"}}); status != http.StatusBadRequest {
		t.Fatalf("bad status form: %d", status)
	}
}

func TestEventInactiveAttendeeEdits(t *testing.T) {
	f := newFixture(t)
	e := f.event(f.staff, EventInput{Title: "Mediation", Kind: KindOther, StartsAt: at(t, "2026-11-12 09:00"), EndsAt: at(t, "2026-11-12 10:00"),
		Attendees: []domain.ID{f.staff.UserID, f.other.UserID}})
	f.deactivate(f.other.UserID)
	title := "Mediation (moved room)"
	got, err := f.svc.UpdateEvent(f.ctx, f.staff, e.ID, EventPatch{Title: &title})
	if err != nil || got.Title != title || len(got.Attendees) != 2 {
		t.Fatalf("title edit with inactive attendee: %+v %v", got, err)
	}
	// Keeping the inactive attendee while adding an active one is fine.
	keep := []domain.ID{f.staff.UserID, f.other.UserID, f.attorney.UserID}
	if got, err := f.svc.UpdateEvent(f.ctx, f.staff, e.ID, EventPatch{Attendees: &keep}); err != nil || len(got.Attendees) != 3 {
		t.Fatalf("add active attendee: %+v %v", got, err)
	}
	// Dropping the inactive attendee works.
	drop := []domain.ID{f.staff.UserID}
	if got, err := f.svc.UpdateEvent(f.ctx, f.staff, e.ID, EventPatch{Attendees: &drop}); err != nil || len(got.Attendees) != 1 {
		t.Fatalf("drop attendee: %+v %v", got, err)
	}
	// Failure: re-adding a now-removed inactive user is a new addition and is refused.
	readd := []domain.ID{f.staff.UserID, f.other.UserID}
	if _, err := f.svc.UpdateEvent(f.ctx, f.staff, e.ID, EventPatch{Attendees: &readd}); code(err) != domain.CodeValidation {
		t.Fatalf("re-add inactive: %v", err)
	}
	// Failure: adding an inactive attendee to another event, and creating with one.
	e2 := f.event(f.staff, EventInput{Title: "Call", Kind: KindOther, StartsAt: at(t, "2026-11-13 09:00"), EndsAt: at(t, "2026-11-13 10:00")})
	add := []domain.ID{f.other.UserID}
	if _, err := f.svc.UpdateEvent(f.ctx, f.staff, e2.ID, EventPatch{Attendees: &add}); code(err) != domain.CodeValidation {
		t.Fatalf("add inactive: %v", err)
	}
	if _, err := f.svc.CreateEvent(f.ctx, f.staff, EventInput{Title: "New", Kind: KindOther, StartsAt: at(t, "2026-11-14 09:00"), EndsAt: at(t, "2026-11-14 10:00"), Attendees: add}); code(err) != domain.CodeValidation {
		t.Fatalf("create with inactive: %v", err)
	}
	if _, err := f.svc.BulkEvents(f.ctx, f.staff, []EventInput{{Title: "Bulk", Kind: KindOther, StartsAt: at(t, "2026-11-14 09:00"), EndsAt: at(t, "2026-11-14 10:00"), Attendees: add}}); code(err) != domain.CodeValidation {
		t.Fatalf("bulk create with inactive: %v", err)
	}
}
