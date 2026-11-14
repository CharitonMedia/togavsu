package supervisor

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Schedule decides when the nightly backup, update check, and monthly
// test-restore are due. Defaults match the PRD: backup at 01:00, updates
// from 02:00 to 04:00, test-restore on the 1st, firm zone until the settings
// registry is reachable.
type Schedule struct {
	Location       *time.Location
	BackupAt       time.Duration
	Window         Window
	TestRestoreDay int
}

// ScheduleState is rebuilt from the ledger on every tick. LastBackup is the
// last nightly backup only; a manual, pre-update, or safety backup does not
// stand in for it. The failure lists hold failed scheduled attempts since
// the last success of that task.
type ScheduleState struct {
	LastBackup      time.Time
	LastUpdate      time.Time
	LastTestRestore time.Time
	BackupFailures  []time.Time
	UpdateFailures  []time.Time
}

type Due struct {
	Backup      bool
	Update      bool
	TestRestore bool
}

// Retry backoff for a failed scheduled attempt. The delay doubles with each
// failure on the same firm-local day, starting at retryBase and capped at
// retryCap. The first attempt of a new day is not delayed by an earlier
// day's failures.
const (
	retryBase = 15 * time.Minute
	retryCap  = 4 * time.Hour
)

// Ledger event written by the scheduler for each scheduled attempt it
// finishes without a task event of its own: a failure, or an update check
// that found nothing newer.
const (
	eventScheduled   = "scheduled"
	scheduledBackup  = "backup"
	scheduledUpdate  = "update"
	scheduledFailed  = "failed"
	scheduledChecked = "ok"
)

func (s Schedule) location() *time.Location {
	if s.Location == nil {
		return time.UTC
	}
	return s.Location
}

func (s Schedule) Due(now time.Time, state ScheduleState, paused bool) Due {
	local := now.In(s.location())
	mins := local.Hour()*60 + local.Minute()
	var due Due
	if mins >= int(s.BackupAt/time.Minute) && !sameDate(state.LastBackup, local) && retryReady(state.BackupFailures, local) {
		due.Backup = true
	}
	if !paused && s.Window.Contains(now) && !sameDate(state.LastUpdate, local) && retryReady(state.UpdateFailures, local) {
		due.Update = true
	}
	day := s.TestRestoreDay
	if day == 0 {
		day = 1
	}
	if local.Day() == day && !sameMonth(state.LastTestRestore, local) && mins >= int(s.BackupAt/time.Minute) {
		due.TestRestore = true
	}
	return due
}

// failuresToday returns the failures on the same firm-local date as local.
func failuresToday(failures []time.Time, local time.Time) []time.Time {
	var out []time.Time
	for _, f := range failures {
		if sameDate(f, local) {
			out = append(out, f)
		}
	}
	return out
}

func retryDelay(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	d := retryBase
	for i := 1; i < n; i++ {
		d *= 2
		if d >= retryCap {
			return retryCap
		}
	}
	return d
}

func retryReady(failures []time.Time, local time.Time) bool {
	today := failuresToday(failures, local)
	if len(today) == 0 {
		return true
	}
	last := today[len(today)-1]
	return !local.Before(last.Add(retryDelay(len(today))))
}

func sameDate(prev time.Time, local time.Time) bool {
	if prev.IsZero() {
		return false
	}
	p := prev.In(local.Location())
	return p.Year() == local.Year() && p.YearDay() == local.YearDay()
}

func sameMonth(prev time.Time, local time.Time) bool {
	if prev.IsZero() {
		return false
	}
	p := prev.In(local.Location())
	return p.Year() == local.Year() && p.Month() == local.Month()
}

// StateFromLedger rebuilds schedule markers from append-only history. Events
// are in append order, so a success clears the failures recorded before it.
func StateFromLedger(events []Event) ScheduleState {
	var state ScheduleState
	for _, ev := range events {
		switch ev.Kind {
		case "backup":
			if ev.Fields["reason"] != reasonNightly {
				continue
			}
			if ev.Time.After(state.LastBackup) {
				state.LastBackup = ev.Time
			}
			state.BackupFailures = nil
		case "update":
			if ev.Time.After(state.LastUpdate) {
				state.LastUpdate = ev.Time
			}
			state.UpdateFailures = nil
		case "test-restore":
			if ev.Time.After(state.LastTestRestore) {
				state.LastTestRestore = ev.Time
			}
		case eventScheduled:
			switch {
			case ev.Fields["task"] == scheduledBackup && ev.Fields["status"] == scheduledFailed:
				state.BackupFailures = append(state.BackupFailures, ev.Time)
			case ev.Fields["task"] == scheduledUpdate && ev.Fields["status"] == scheduledFailed:
				state.UpdateFailures = append(state.UpdateFailures, ev.Time)
			case ev.Fields["task"] == scheduledUpdate && ev.Fields["status"] == scheduledChecked:
				if ev.Time.After(state.LastUpdate) {
					state.LastUpdate = ev.Time
				}
				state.UpdateFailures = nil
			}
		}
	}
	return state
}

// Scheduler runs the scheduled supervisor work on each tick. A failed nightly
// backup or update check is written to the ledger and retried with backoff
// instead of on every tick. The first failure of a firm-local day goes to the
// admin outbox; later retries that day are in the ledger only. A nightly
// backup failure is always notified. An update check that failed only
// because updates are not configured is not.
type Scheduler struct {
	Ops      *Operations
	Schedule Schedule
	Paused   bool
}

// notAttempted errors mean the work did not start: another operation holds
// the gate, the process is stopping, or the update window or pause changed
// between Due and Apply. They are retried on the next tick and not counted.
func notAttempted(err error) bool {
	return errors.Is(err, ErrBusy) || errors.Is(err, ErrShuttingDown) ||
		errors.Is(err, ErrOutsideWindow) || errors.Is(err, ErrUpdatesPaused)
}

// updatesUnconfigured errors mean this stack has no release index or no
// release key yet. Signed releases are pending (AC-016), so the failure is
// recorded and backed off like any other, but no admin notice is sent.
func updatesUnconfigured(err error) bool {
	return errors.Is(err, ErrReleaseIndexUnconfigured) || errors.Is(err, ErrReleaseKeyUnconfigured)
}

// Tick runs whatever is due at now. It returns the ledger read error, if any;
// task failures are recorded and notified, not returned.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) error {
	ctx = Scheduled(ctx)
	o := s.Ops
	if o == nil || o.Ledger == nil {
		return errors.New("scheduler has no ledger")
	}
	events, err := o.Ledger.List("")
	if err != nil {
		return err
	}
	state := StateFromLedger(events)
	due := s.Schedule.Due(now, state, s.Paused)
	if due.Backup {
		if _, err := o.CreateBackup(ctx, reasonNightly); err != nil && !notAttempted(err) {
			s.failed(ctx, now, scheduledBackup, state.BackupFailures, err, true,
				"Nightly backup failed",
				"The nightly backup did not complete.")
		}
	}
	if due.Update {
		result, err := o.Update(ctx, false)
		switch {
		case err != nil && !notAttempted(err):
			s.failed(ctx, now, scheduledUpdate, state.UpdateFailures, err, !updatesUnconfigured(err),
				"Scheduled update check failed",
				"The scheduled update check did not complete. The running version was left in place.")
		case err == nil && result.Status == "up_to_date":
			// The updater writes no event for this result. Without one the
			// check would rerun on every tick for the rest of the window.
			_, _ = o.Ledger.Append(eventScheduled, "supervisor", map[string]string{
				"task": scheduledUpdate, "status": scheduledChecked, "result": result.Status,
			})
		}
	}
	if due.TestRestore {
		all, err := o.Store.List()
		if err == nil && len(all) > 0 {
			newest := all[0]
			for _, meta := range all[1:] {
				if meta.CreatedAt.After(newest.CreatedAt) {
					newest = meta
				}
			}
			_ = o.TestRestore(ctx, newest.ID)
		}
	}
	return nil
}

func (s *Scheduler) failed(ctx context.Context, now time.Time, task string, prior []time.Time, cause error, notify bool, subject, lead string) {
	o := s.Ops
	local := now.In(s.Schedule.location())
	attempt := len(failuresToday(prior, local)) + 1
	retry := now.Add(retryDelay(attempt)).In(s.Schedule.location())
	msg := Scrub(cause.Error())
	_, _ = o.Ledger.Append(eventScheduled, "supervisor", map[string]string{
		"task":        task,
		"status":      scheduledFailed,
		"attempt":     strconv.Itoa(attempt),
		"retry_after": retry.UTC().Format(time.RFC3339),
		"error":       msg,
	})
	if !notify || attempt != 1 || o.Outbox == nil {
		return
	}
	body := lead + " The supervisor will try again after " + retry.Format("2006-01-02 15:04 MST") +
		", backing off after each failure. Later attempts today are recorded in the supervisor ledger and do not send more mail. Cause: " + msg
	_ = o.Outbox.Notify(ctx, subject, body)
}
