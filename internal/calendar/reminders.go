package calendar

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ReminderRun summarizes one reminder pass.
type ReminderRun struct {
	Sent    int
	Emailed int
}

// userPrincipal is how a reminder recipient's access is evaluated: the user's own
// roles and walls, so a wall added after the deadline was set still hides it.
func userPrincipal(u domain.User) (domain.Principal, bool) {
	roles, err := domain.ParseRoles(u.Roles)
	if err != nil || !u.Active {
		return domain.Principal{}, false
	}
	return domain.Principal{UserID: u.ID, Roles: roles, Source: domain.SourceUI}, true
}

// RunReminders sends every reminder that is due at the current time. It is the
// reminders job: safe to run repeatedly, because each reminder is claimed once.
//
// A deadline reminder for offset N fires from firm-local midnight N days before the
// due date until the due date passes, and only while the deadline is open. When
// several offsets are already due (a deadline entered late), one reminder goes out
// for the nearest offset and the earlier offsets are marked sent.
// An event reminder fires from firm-local midnight calendar.event_reminder_days
// before the event's date until the event ends. Rescheduling re-arms it.
//
// ponytail: ceiling: called by the core job runner; there is no Postgres job queue
// row per reminder yet, and the user's own lead time from My settings and the
// notification preferences grid are not consulted (firm defaults only, in-app plus
// email). Upgrade path: enqueue per-reminder jobs and read notification_preferences.
// Trigger: the notifications task ships notification_preferences and the job queue.
func (s *Service) RunReminders(ctx context.Context) (ReminderRun, error) {
	var run ReminderRun
	var errs []error
	loc := s.Zone(ctx)
	now := s.now()
	today := localDate(now, loc)

	deadlines, err := s.d.Store.Deadlines(ctx)
	if err != nil {
		return run, err
	}
	for _, d := range deadlines {
		if d.Status != StatusOpen {
			continue
		}
		due, err := parseDate(d.DueOn)
		if err != nil || today.After(due) {
			continue
		}
		var eligible []int
		for _, off := range d.Offsets {
			if !addDays(due, -off).After(today) {
				eligible = append(eligible, off)
			}
		}
		if len(eligible) == 0 {
			continue
		}
		sort.Ints(eligible)
		nearest := eligible[0]
		claimed, err := s.d.Store.ClaimReminder(ctx, deadlineKey(d, nearest))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, off := range eligible {
			if off == nearest {
				continue
			}
			if _, err := s.d.Store.ClaimReminder(ctx, deadlineKey(d, off)); err != nil {
				errs = append(errs, err)
			}
		}
		if !claimed {
			continue
		}
		title := "Deadline: " + d.Title
		body := fmt.Sprintf("%s is due %s.", d.Title, longDate(due))
		if due.Equal(today) {
			body = fmt.Sprintf("%s is due today, %s.", d.Title, longDate(due))
		}
		s.deliver(ctx, &run, &errs, d.ResponsibleUserID, Notice{
			Kind: NoticeDeadline, Title: title, Body: body, Path: "/calendar/deadlines/" + string(d.ID),
			MatterID: d.MatterID, RecordID: d.ID,
		})
	}

	lead, err := s.settingInt(ctx, settingEventOffset)
	if err != nil {
		errs = append(errs, err)
		return run, errors.Join(errs...)
	}
	events, err := s.d.Store.Events(ctx)
	if err != nil {
		return run, err
	}
	for _, e := range events {
		over := e.End()
		if over.Before(e.Start()) {
			over = e.Start()
		}
		if !now.Before(over) {
			continue
		}
		fireAt := dayStart(addDays(localDate(e.Start(), loc), -lead), loc)
		if now.Before(fireAt) {
			continue
		}
		for _, user := range e.Attendees {
			key := fmt.Sprintf("event:%s:%d:%d:%s", e.ID, e.StartsAt, lead, user)
			claimed, err := s.d.Store.ClaimReminder(ctx, key)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !claimed {
				continue
			}
			when := e.Start().In(loc).Format("Monday, January 2, 2006 at 3:04 PM MST")
			if e.AllDay {
				when = longDate(localDate(e.Start(), loc))
			}
			s.deliver(ctx, &run, &errs, user, Notice{
				Kind: NoticeEvent, Title: "Event: " + e.Title, Body: fmt.Sprintf("%s is on %s.", e.Title, when),
				Path: "/calendar/events/" + string(e.ID), MatterID: e.MatterID, RecordID: e.ID,
			})
		}
	}
	return run, errors.Join(errs...)
}

func deadlineKey(d Deadline, off int) string {
	return "deadline:" + string(d.ID) + ":" + d.DueOn + ":" + strconv.Itoa(off)
}

func longDate(d time.Time) string { return d.Format("Monday, January 2, 2006") }

// deliver stores the in-app notice and, with a mailer and an address, emails it.
// A recipient who can no longer see the matter gets nothing.
func (s *Service) deliver(ctx context.Context, run *ReminderRun, errs *[]error, userID domain.ID, n Notice) {
	u, err := s.d.Users.Get(ctx, userID)
	if err != nil {
		*errs = append(*errs, err)
		return
	}
	p, ok := userPrincipal(u)
	if !ok {
		return
	}
	if !s.visible(ctx, p, n.MatterID) {
		return
	}
	id, err := s.d.IDs()
	if err != nil {
		*errs = append(*errs, err)
		return
	}
	n.ID, n.UserID, n.At = id, userID, s.now()
	if s.d.Mailer != nil && u.Email != "" {
		if err := s.d.Mailer.Send(ctx, u.Email, n.Title, n.Body); err != nil {
			*errs = append(*errs, err)
		} else {
			n.Emailed = true
			run.Emailed++
		}
	}
	if err := s.d.Store.PutNotice(ctx, n); err != nil {
		*errs = append(*errs, err)
		return
	}
	run.Sent++
}

// Notices returns the caller's own reminders, newest first, dropping any whose
// matter the caller can no longer see. This feeds the dashboard and the bell.
func (s *Service) Notices(ctx context.Context, p domain.Principal) ([]Notice, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	rows, err := s.d.Store.Notices(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	var out []Notice
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].MatterID.IsZero() || s.visible(ctx, p, rows[i].MatterID) {
			out = append(out, rows[i])
		}
	}
	return out, nil
}
