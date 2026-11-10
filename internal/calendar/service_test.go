package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestEventWallsInService(t *testing.T) {
	f := newFixture(t)
	open := f.matter("Open")
	secret := f.walled("Sealed")
	vis := f.event(f.staff, EventInput{Title: "Open meeting", Kind: KindClientMeeting, MatterID: open.ID, StartsAt: at(t, "2026-11-12 09:00"), EndsAt: at(t, "2026-11-12 10:00")})
	hid := f.event(f.attorney, EventInput{Title: "Sealed hearing", Kind: KindCourtDate, MatterID: secret.ID, StartsAt: at(t, "2026-11-12 11:00"), EndsAt: at(t, "2026-11-12 12:00")})
	free := f.event(f.staff, EventInput{Title: "Office closed", Kind: KindBlock, AllDay: true, StartsAt: at(t, "2026-11-26 00:00"), EndsAt: at(t, "2026-11-26 00:00")})

	for _, p := range []domain.Principal{f.staff, f.admin} {
		rows, err := f.svc.ListEvents(f.ctx, p, EventFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].ID != vis.ID || rows[1].ID != free.ID {
			t.Fatalf("%v list: %+v", p.Roles, rows)
		}
		if _, err := f.svc.GetEvent(f.ctx, p, hid.ID); code(err) != domain.CodeNotFound {
			t.Fatalf("%v get walled: %v", p.Roles, err)
		}
		title := "x"
		if _, err := f.svc.UpdateEvent(f.ctx, p, hid.ID, EventPatch{Title: &title}); code(err) != domain.CodeNotFound {
			t.Fatalf("%v update walled: %v", p.Roles, err)
		}
		if err := f.svc.DeleteEvent(f.ctx, p, hid.ID); code(err) != domain.CodeNotFound {
			t.Fatalf("%v delete walled: %v", p.Roles, err)
		}
		if _, err := f.svc.ListEvents(f.ctx, p, EventFilter{MatterID: secret.ID}); code(err) != domain.CodeNotFound {
			t.Fatalf("%v filter by walled matter: %v", p.Roles, err)
		}
		// Neither creating on the walled matter nor moving an event onto it works.
		if _, err := f.svc.CreateEvent(f.ctx, p, EventInput{Title: "sneak", Kind: KindOther, MatterID: secret.ID, StartsAt: at(t, "2026-11-12 09:00"), EndsAt: at(t, "2026-11-12 10:00")}); code(err) == "" {
			t.Fatalf("%v created on walled matter", p.Roles)
		}
		mv := secret.ID
		if _, err := f.svc.UpdateEvent(f.ctx, p, vis.ID, EventPatch{MatterID: &mv}); code(err) == "" {
			t.Fatalf("%v moved event behind a wall", p.Roles)
		}
	}
	rows, _ := f.svc.ListEvents(f.ctx, f.attorney, EventFilter{})
	if len(rows) != 3 {
		t.Fatalf("attorney on the wall sees %d", len(rows))
	}
	// A wall added later hides an existing event at once.
	if err := f.practice.SetWall(f.ctx, f.admin, open.ID, []domain.ID{f.attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.GetEvent(f.ctx, f.staff, vis.ID); code(err) != domain.CodeNotFound {
		t.Fatalf("new wall not applied: %v", err)
	}
	// Accountant and client roles cannot read or write the calendar at all.
	for _, p := range []domain.Principal{f.accountant, f.client} {
		if _, err := f.svc.ListEvents(f.ctx, p, EventFilter{}); code(err) != domain.CodeForbidden {
			t.Fatalf("%v list: %v", p.Roles, err)
		}
		if _, err := f.svc.CreateEvent(f.ctx, p, EventInput{Title: "x", Kind: KindOther, StartsAt: at(t, "2026-11-12 09:00"), EndsAt: at(t, "2026-11-12 10:00")}); code(err) != domain.CodeForbidden {
			t.Fatalf("%v create: %v", p.Roles, err)
		}
	}
}

func TestEventAuditTrail(t *testing.T) {
	f := newFixture(t)
	e := f.event(f.staff, EventInput{Title: "Depo", Kind: KindOther, StartsAt: at(t, "2026-11-12 09:00"), EndsAt: at(t, "2026-11-12 10:00"), Location: "Room A"})
	loc := "Room B"
	if _, err := f.svc.UpdateEvent(f.ctx, f.staff, e.ID, EventPatch{Location: &loc}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteEvent(f.ctx, f.staff, e.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := f.audit.Rows(f.ctx, audit.RowQuery{Entity: "calendar_event", EntityID: e.ID})
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, r := range rows {
		fields = append(fields, r.Field+"="+r.OldValue+">"+r.NewValue)
		if r.ActorID != f.staff.UserID {
			t.Fatalf("actor %s", r.ActorID)
		}
	}
	joined := strings.Join(fields, ";")
	for _, want := range []string{"title=>Depo", "location=Room A>Room B", "location=Room B>", "deleted=false>true"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("audit lacks %q in %s", want, joined)
		}
	}
	var actions []string
	for _, ev := range f.store.Stream() {
		actions = append(actions, ev.Action)
	}
	if strings.Join(actions, ",") != "calendar_event.created,calendar_event.updated,calendar_event.deleted" {
		t.Fatalf("activity: %v", actions)
	}
}

func TestAllDayAndTimeZones(t *testing.T) {
	f := newFixture(t)
	chi := chicago(t)
	// US daylight saving ends 2026-11-01. A one-day all-day event that day spans 25 hours.
	e := f.event(f.staff, EventInput{Title: "Fall back", Kind: KindOther, AllDay: true,
		StartsAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)})
	if !e.Start().Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, chi)) || !e.End().Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, chi)) {
		t.Fatalf("all-day bounds %s %s", e.Start(), e.End())
	}
	if e.End().Sub(e.Start()) != 25*time.Hour {
		t.Fatalf("DST day length %s", e.End().Sub(e.Start()))
	}
	// The written date wins over the offset: 2026-11-03 with a +09:00 offset is still Nov 3.
	tok := time.FixedZone("JST", 9*3600)
	e2 := f.event(f.staff, EventInput{Title: "Written date", Kind: KindOther, AllDay: true,
		StartsAt: time.Date(2026, 11, 3, 0, 0, 0, 0, tok), EndsAt: time.Date(2026, 11, 5, 0, 0, 0, 0, tok)})
	if got := localDate(e2.Start(), chi).Format(dateLayout); got != "2026-11-03" {
		t.Fatalf("start date %s", got)
	}
	// End at midnight is exclusive: Nov 3 and Nov 4 only.
	if days := eventDaysAllDay(e2, chi); len(days) != 2 || days[1].Format(dateLayout) != "2026-11-04" {
		t.Fatalf("days %v", days)
	}
	// Day view on Nov 1 in Chicago includes the DST event and not an event at 23:30 CST Oct 31.
	f.event(f.staff, EventInput{Title: "Late Halloween", Kind: KindOther, StartsAt: time.Date(2026, 10, 31, 23, 30, 0, 0, chi), EndsAt: time.Date(2026, 10, 31, 23, 50, 0, 0, chi)})
	start, end, err := viewWindow("day", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), chi)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := f.svc.ListEvents(f.ctx, f.staff, EventFilter{From: start, To: end})
	if len(rows) != 1 || rows[0].ID != e.ID {
		t.Fatalf("day view across DST: %+v", rows)
	}
	// Week starts Monday; month covers the calendar month in firm time.
	ws, we, _ := viewWindow("week", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), chi)
	if ws.In(chi).Format("2006-01-02 Mon") != "2026-10-26 Mon" || we.In(chi).Format("2006-01-02") != "2026-11-02" {
		t.Fatalf("week window %s %s", ws.In(chi), we.In(chi))
	}
	ms, me, _ := viewWindow("month", time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), chi)
	if !ms.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, chi)) || !me.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, chi)) {
		t.Fatalf("month window %s %s", ms, me)
	}
	// Patching the title keeps the all-day dates.
	title := "Fall back (renamed)"
	up, err := f.svc.UpdateEvent(f.ctx, f.staff, e.ID, EventPatch{Title: &title})
	if err != nil || up.StartsAt != e.StartsAt || up.EndsAt != e.EndsAt {
		t.Fatalf("patch moved all-day bounds: %v %s %s", err, up.Start(), up.End())
	}
	// Turning all-day off with explicit times stores those instants.
	off := false
	s2, e3 := at(t, "2026-11-01 09:00"), at(t, "2026-11-01 10:00")
	up, err = f.svc.UpdateEvent(f.ctx, f.staff, e.ID, EventPatch{AllDay: &off, StartsAt: &s2, EndsAt: &e3})
	if err != nil || !up.Start().Equal(s2) || up.AllDay {
		t.Fatalf("all-day off: %v", err)
	}
}

func TestDeadlineLifecycle(t *testing.T) {
	f := newFixture(t)
	m := f.matter("Case")
	d := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Reply brief", DueOn: "2026-11-20", ResponsibleUserID: f.attorney.UserID})
	if joinInts(d.Offsets) != "7,1" || d.ReminderOffsets != "[7,1]" || d.Status != StatusOpen {
		t.Fatalf("defaults: %+v", d)
	}
	// The firm default is a live setting.
	if _, err := f.reg.Set(f.ctx, settingDeadlineOffsets, "14,3", f.admin); err != nil {
		t.Fatal(err)
	}
	d2 := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Expert list", DueOn: "2026-12-01", ResponsibleUserID: f.staff.UserID})
	if joinInts(d2.Offsets) != "14,3" {
		t.Fatalf("setting not live: %v", d2.Offsets)
	}
	d3 := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Custom", DueOn: "2026-12-01", ResponsibleUserID: f.staff.UserID, Offsets: []int{1, 10, 1}})
	if joinInts(d3.Offsets) != "10,1" {
		t.Fatalf("offsets not normalized: %v", d3.Offsets)
	}
	done := StatusDone
	got, err := f.svc.UpdateDeadline(f.ctx, f.staff, d.ID, DeadlinePatch{Status: &done})
	if err != nil || got.DoneBy != f.staff.UserID || got.DoneAt != domain.UnixMicroUTC(f.clock.Now()) {
		t.Fatalf("done: %+v %v", got, err)
	}
	reopen := StatusOpen
	got, err = f.svc.UpdateDeadline(f.ctx, f.attorney, d.ID, DeadlinePatch{Status: &reopen})
	if err != nil || got.DoneBy != "" || got.DoneAt != 0 {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	bad := []struct {
		name string
		in   DeadlineInput
	}{
		{"no matter", DeadlineInput{Title: "x", DueOn: "2026-12-01", ResponsibleUserID: f.staff.UserID}},
		{"no title", DeadlineInput{MatterID: m.ID, Title: "  ", DueOn: "2026-12-01", ResponsibleUserID: f.staff.UserID}},
		{"bad date", DeadlineInput{MatterID: m.ID, Title: "x", DueOn: "2026-02-30", ResponsibleUserID: f.staff.UserID}},
		{"bad status", DeadlineInput{MatterID: m.ID, Title: "x", DueOn: "2026-12-01", ResponsibleUserID: f.staff.UserID, Status: "late"}},
		{"bad offset", DeadlineInput{MatterID: m.ID, Title: "x", DueOn: "2026-12-01", ResponsibleUserID: f.staff.UserID, Offsets: []int{-1}}},
		{"no user", DeadlineInput{MatterID: m.ID, Title: "x", DueOn: "2026-12-01"}},
		{"unknown user", DeadlineInput{MatterID: m.ID, Title: "x", DueOn: "2026-12-01", ResponsibleUserID: tid(999)}},
		{"unknown matter", DeadlineInput{MatterID: tid(998), Title: "x", DueOn: "2026-12-01", ResponsibleUserID: f.staff.UserID}},
	}
	for _, c := range bad {
		if _, err := f.svc.CreateDeadline(f.ctx, f.staff, c.in); code(err) != domain.CodeValidation {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// Overdue is judged on the firm's date: 03:00 UTC Nov 21 is still Nov 20 in Chicago.
	f.clock.Set(time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC))
	if f.svc.IsOverdue(f.ctx, got) {
		t.Fatal("overdue too early in firm time")
	}
	f.clock.Set(time.Date(2026, 11, 21, 7, 0, 0, 0, time.UTC))
	if !f.svc.IsOverdue(f.ctx, got) {
		t.Fatal("not overdue the next firm day")
	}
	over, _ := f.svc.Overdue(f.ctx, f.staff)
	if len(over) != 1 || over[0].ID != d.ID {
		t.Fatalf("overdue list: %+v", over)
	}
	// Waived drops it from the overdue list.
	waive := StatusWaived
	if _, err := f.svc.UpdateDeadline(f.ctx, f.staff, d.ID, DeadlinePatch{Status: &waive}); err != nil {
		t.Fatal(err)
	}
	if over, _ := f.svc.Overdue(f.ctx, f.staff); len(over) != 0 {
		t.Fatal("waived deadline still overdue")
	}
}

func TestReminders(t *testing.T) {
	f := newFixture(t)
	m := f.matter("Case")
	secret := f.walled("Sealed")
	// Today is Tue 2026-11-10 (Chicago). Due Nov 17 with 7,1: the 7-day reminder is due now.
	d := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Answer", DueOn: "2026-11-17", ResponsibleUserID: f.staff.UserID})
	// Entered late: due Nov 12, offsets 7 and 1 both passed or not yet; only one reminder goes out.
	late := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Late entry", DueOn: "2026-11-12", ResponsibleUserID: f.attorney.UserID})
	f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Already done", DueOn: "2026-11-11", ResponsibleUserID: f.staff.UserID, Status: StatusDone})
	f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Far off", DueOn: "2027-01-30", ResponsibleUserID: f.staff.UserID})
	// Staff is responsible for a deadline on a matter later walled away from them.
	walledFor := f.deadline(f.attorney, DeadlineInput{MatterID: secret.ID, Title: "Sealed filing", DueOn: "2026-11-11", ResponsibleUserID: f.staff.UserID})
	ev := f.event(f.staff, EventInput{Title: "Mediation", Kind: KindOther, StartsAt: at(t, "2026-11-11 09:00"), EndsAt: at(t, "2026-11-11 12:00"), Attendees: []domain.ID{f.staff.UserID, f.attorney.UserID}})
	f.event(f.staff, EventInput{Title: "Next week", Kind: KindOther, StartsAt: at(t, "2026-11-18 09:00"), EndsAt: at(t, "2026-11-18 10:00"), Attendees: []domain.ID{f.staff.UserID}})

	run, err := f.svc.RunReminders(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Answer (7-day) to staff; Late entry (7 passed, 1 not yet) to the attorney; Mediation to
	// both attendees. Nothing for done, far-off, the walled deadline, or next week's event.
	if run.Sent != 4 || run.Emailed != 4 || f.mail.count() != 4 {
		t.Fatalf("first run: %+v mails %d", run, f.mail.count())
	}
	staffN, _ := f.svc.Notices(f.ctx, f.staff)
	var titles []string
	for _, n := range staffN {
		titles = append(titles, n.Title)
	}
	got := strings.Join(titles, "|")
	if !strings.Contains(got, "Deadline: Answer") || !strings.Contains(got, "Event: Mediation") || strings.Contains(got, "Sealed") {
		t.Fatalf("staff notices: %s", got)
	}
	// Idempotent: a second run at the same time sends nothing.
	if run, _ := f.svc.RunReminders(f.ctx); run.Sent != 0 {
		t.Fatalf("second run sent %d", run.Sent)
	}
	// Nov 11 local: Late entry's 1-day reminder fires once; Answer has nothing new.
	f.clock.Set(at(t, "2026-11-11 08:00"))
	run, _ = f.svc.RunReminders(f.ctx)
	if run.Sent != 1 {
		t.Fatalf("day two: %+v", run)
	}
	attN, _ := f.svc.Notices(f.ctx, f.attorney)
	if len(attN) != 3 || attN[0].RecordID != late.ID || !strings.Contains(attN[0].Body, "Thursday, November 12, 2026") {
		t.Fatalf("attorney notices: %+v", attN)
	}
	// Nov 16: Answer's 1-day reminder.
	f.clock.Set(at(t, "2026-11-16 00:05"))
	run, _ = f.svc.RunReminders(f.ctx)
	if run.Sent != 1 {
		t.Fatalf("Nov 16: %+v", run)
	}
	// Rescheduling the due date re-arms reminders: the 7-day offset for Nov 20 is due.
	due := "2026-11-20"
	if _, err := f.svc.UpdateDeadline(f.ctx, f.staff, d.ID, DeadlinePatch{DueOn: &due}); err != nil {
		t.Fatal(err)
	}
	if run, _ := f.svc.RunReminders(f.ctx); run.Sent != 1 {
		t.Fatalf("re-armed: %+v", run)
	}
	// Marking done stops further deadline reminders; the Nov 18 event's reminder still fires.
	done := StatusDone
	if _, err := f.svc.UpdateDeadline(f.ctx, f.staff, d.ID, DeadlinePatch{Status: &done}); err != nil {
		t.Fatal(err)
	}
	f.clock.Set(at(t, "2026-11-17 08:00"))
	run, _ = f.svc.RunReminders(f.ctx)
	if run.Sent != 1 {
		t.Fatalf("Nov 17: %+v", run)
	}
	newest := mustNotices(t, f, f.staff)[0]
	if newest.Kind != NoticeEvent || newest.Title != "Event: Next week" {
		t.Fatalf("newest notice %+v", newest)
	}
	// A wall added after a notice was sent hides that notice from the dashboard.
	if err := f.practice.SetWall(f.ctx, f.admin, m.ID, []domain.ID{f.attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	for _, n := range mustNotices(t, f, f.staff) {
		if n.MatterID == m.ID {
			t.Fatal("notice on a newly walled matter still shown")
		}
	}
	_, _ = ev, walledFor
}

func mustNotices(t *testing.T, f *fixture, p domain.Principal) []Notice {
	n, err := f.svc.Notices(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A PATCH that turns a timed event all-day without new times keeps the firm-local
// date: 7 PM Chicago on Nov 10 is 01:00Z on Nov 11 and must stay Nov 10.
func TestTimedToAllDayPatchKeepsLocalDate(t *testing.T) {
	f := newFixture(t)
	chi := chicago(t)
	e := f.event(f.staff, EventInput{Title: "Evening hearing", Kind: KindCourtDate, StartsAt: at(t, "2026-11-10 19:00"), EndsAt: at(t, "2026-11-10 20:30"), Attendees: []domain.ID{f.attorney.UserID}})
	on := true
	up, err := f.svc.UpdateEvent(f.ctx, f.staff, e.ID, EventPatch{AllDay: &on})
	if err != nil {
		t.Fatal(err)
	}
	if !up.AllDay || !up.Start().Equal(time.Date(2026, 11, 10, 0, 0, 0, 0, chi)) || !up.End().Equal(time.Date(2026, 11, 11, 0, 0, 0, 0, chi)) {
		t.Fatalf("timed->all-day moved the date: %s .. %s", up.Start().In(chi), up.End().In(chi))
	}
	// A timed event spanning local midnight becomes all-day on both local dates.
	e2 := f.event(f.staff, EventInput{Title: "Overnight", Kind: KindBlock, StartsAt: at(t, "2026-11-12 22:00"), EndsAt: at(t, "2026-11-13 02:00")})
	up, err = f.svc.UpdateEvent(f.ctx, f.staff, e2.ID, EventPatch{AllDay: &on})
	if err != nil || !up.Start().Equal(time.Date(2026, 11, 12, 0, 0, 0, 0, chi)) || !up.End().Equal(time.Date(2026, 11, 14, 0, 0, 0, 0, chi)) {
		t.Fatalf("overnight: %v %s .. %s", err, up.Start().In(chi), up.End().In(chi))
	}
	// The available-dates tool now sees Nov 10 (not Nov 11) as busy for the attorney.
	res, err := f.svc.AvailableDates(f.ctx, f.staff, AvailableRequest{UserID: f.attorney.UserID, StartDate: "2026-11-10", EndDate: "2026-11-11"})
	if err != nil || len(res.Dates) != 1 || res.Dates[0].Date != "2026-11-11" {
		t.Fatalf("available after patch: %v %+v", err, res.Dates)
	}
}
