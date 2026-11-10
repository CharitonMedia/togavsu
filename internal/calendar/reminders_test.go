package calendar

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// flakyUsers fails lookups for the listed users while down is set.
type flakyUsers struct {
	mu   sync.Mutex
	next Users
	down map[domain.ID]bool
}

func (u *flakyUsers) Get(ctx context.Context, id domain.ID) (domain.User, error) {
	u.mu.Lock()
	down := u.down[id]
	u.mu.Unlock()
	if down {
		return domain.User{}, errors.New("user directory unavailable")
	}
	return u.next.Get(ctx, id)
}

func (u *flakyUsers) set(id domain.ID, down bool) { u.mu.Lock(); u.down[id] = down; u.mu.Unlock() }

// flakyNotices fails PutNotice while fail is set.
type flakyNotices struct {
	*Memory
	mu   sync.Mutex
	fail bool
}

func (s *flakyNotices) PutNotice(ctx context.Context, n Notice) error {
	s.mu.Lock()
	fail := s.fail
	s.mu.Unlock()
	if fail {
		return errors.New("notice store unavailable")
	}
	return s.Memory.PutNotice(ctx, n)
}

func (f *fixture) rewire(edit func(d *Deps)) {
	f.t.Helper()
	d := f.svc.d
	edit(&d)
	svc, err := New(d)
	if err != nil {
		f.t.Fatal(err)
	}
	f.svc = svc
}

func countKind(ns []Notice, id domain.ID) int {
	n := 0
	for _, x := range ns {
		if x.RecordID == id {
			n++
		}
	}
	return n
}

// A failed email leaves the in-app notice delivered and retries only the email on
// the next run, exactly once, for both deadline and event reminders.
func TestRemindersRetryFailedEmail(t *testing.T) {
	f := newFixture(t)
	m := f.matter("Case")
	// Due Nov 12 with offsets 7 and 1: 7 is due now (Nov 10), 1 is not yet.
	d := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Answer", DueOn: "2026-11-12", ResponsibleUserID: f.staff.UserID})
	ev := f.event(f.staff, EventInput{Title: "Mediation", Kind: KindOther, StartsAt: at(t, "2026-11-11 09:00"), EndsAt: at(t, "2026-11-11 12:00"), Attendees: []domain.ID{f.staff.UserID}})

	f.mail.setFail(errors.New("smtp: 421 try again"))
	run, err := f.svc.RunReminders(f.ctx)
	if err == nil || !strings.Contains(err.Error(), "421") {
		t.Fatalf("mail failure not reported: %v", err)
	}
	if run.Sent != 2 || run.Emailed != 0 || f.mail.count() != 0 {
		t.Fatalf("failing run: %+v mails %d", run, f.mail.count())
	}
	for _, n := range mustNotices(t, f, f.staff) {
		if n.Emailed {
			t.Fatalf("notice marked emailed after mail failure: %+v", n)
		}
	}

	// Still failing: no duplicate in-app notices, still no mail.
	if run, err := f.svc.RunReminders(f.ctx); err == nil || run.Sent != 0 || run.Emailed != 0 {
		t.Fatalf("second failing run: %+v %v", run, err)
	}

	f.mail.setFail(nil)
	run, err = f.svc.RunReminders(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run.Sent != 0 || run.Emailed != 2 {
		t.Fatalf("recovery run: %+v", run)
	}
	got := strings.Join(f.mail.to("sam@firm.example"), ",")
	if len(f.mail.to("sam@firm.example")) != 2 || !strings.Contains(got, "Deadline: Answer") || !strings.Contains(got, "Event: Mediation") {
		t.Fatalf("recovered mails: %s", got)
	}
	ns := mustNotices(t, f, f.staff)
	if countKind(ns, d.ID) != 1 || countKind(ns, ev.ID) != 1 {
		t.Fatalf("in-app notices duplicated: %+v", ns)
	}
	// Exactly once: later runs send nothing more.
	if run, err := f.svc.RunReminders(f.ctx); err != nil || run.Sent != 0 || run.Emailed != 0 || f.mail.count() != 2 {
		t.Fatalf("after recovery: %+v %v mails %d", run, err, f.mail.count())
	}
}

// A failed user lookup sends nothing and claims nothing, including the superseded
// earlier deadline offsets; the next run delivers the reminder once on both channels.
func TestRemindersRetryFailedUserLookup(t *testing.T) {
	f := newFixture(t)
	m := f.matter("Case")
	users := &flakyUsers{next: f.svc.d.Users, down: map[domain.ID]bool{}}
	f.rewire(func(d *Deps) { d.Users = users })
	// Entered late: due Nov 11 with offsets 7 and 1, both already due. One reminder (1-day).
	late := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Late entry", DueOn: "2026-11-11", ResponsibleUserID: f.attorney.UserID})
	ev := f.event(f.staff, EventInput{Title: "Hearing", Kind: KindOther, StartsAt: at(t, "2026-11-11 09:00"), EndsAt: at(t, "2026-11-11 10:00"), Attendees: []domain.ID{f.attorney.UserID, f.staff.UserID}})

	users.set(f.attorney.UserID, true)
	run, err := f.svc.RunReminders(f.ctx)
	if err == nil || !strings.Contains(err.Error(), "user directory unavailable") {
		t.Fatalf("lookup failure not reported: %v", err)
	}
	// Staff's event reminder is unaffected by the attorney's failed lookup.
	if run.Sent != 1 || run.Emailed != 1 || len(f.mail.to("ada@firm.example")) != 0 {
		t.Fatalf("failing run: %+v", run)
	}
	for _, key := range []string{deadlineKey(late, 7), deadlineKey(late, 1)} {
		if f.store.sent[key] || f.store.sent[key+":app"] || f.store.sent[key+":email"] {
			t.Fatalf("claim %s held after failed lookup", key)
		}
	}

	users.set(f.attorney.UserID, false)
	run, err = f.svc.RunReminders(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run.Sent != 2 || run.Emailed != 2 {
		t.Fatalf("recovery run: %+v", run)
	}
	ns := mustNotices(t, f, f.attorney)
	if countKind(ns, late.ID) != 1 || countKind(ns, ev.ID) != 1 || !ns[0].Emailed || !ns[1].Emailed {
		t.Fatalf("attorney notices: %+v", ns)
	}
	if len(f.mail.to("ada@firm.example")) != 2 || len(f.mail.to("sam@firm.example")) != 1 {
		t.Fatalf("mails: %v", f.mail.sent)
	}
	// The earlier 7-day offset is now superseded, and nothing repeats.
	if !f.store.sent[deadlineKey(late, 7)] {
		t.Fatal("earlier offset not marked after delivery")
	}
	if run, err := f.svc.RunReminders(f.ctx); err != nil || run.Sent != 0 || run.Emailed != 0 {
		t.Fatalf("after recovery: %+v %v", run, err)
	}
}

// A failed in-app write is retried without re-sending the email that went out.
func TestRemindersRetryFailedNotice(t *testing.T) {
	f := newFixture(t)
	m := f.matter("Case")
	store := &flakyNotices{Memory: f.store, fail: true}
	f.rewire(func(d *Deps) { d.Store = store })
	d := f.deadline(f.staff, DeadlineInput{MatterID: m.ID, Title: "Answer", DueOn: "2026-11-12", ResponsibleUserID: f.staff.UserID})

	run, err := f.svc.RunReminders(f.ctx)
	if err == nil || run.Sent != 0 || run.Emailed != 1 {
		t.Fatalf("failing run: %+v %v", run, err)
	}
	store.mu.Lock()
	store.fail = false
	store.mu.Unlock()
	run, err = f.svc.RunReminders(f.ctx)
	if err != nil || run.Sent != 1 || run.Emailed != 0 {
		t.Fatalf("recovery run: %+v %v", run, err)
	}
	if countKind(mustNotices(t, f, f.staff), d.ID) != 1 || f.mail.count() != 1 {
		t.Fatalf("notices or mails duplicated: mails %d", f.mail.count())
	}
	if run, _ := f.svc.RunReminders(f.ctx); run.Sent != 0 || run.Emailed != 0 {
		t.Fatalf("after recovery: %+v", run)
	}
}
