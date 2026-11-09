package supervisor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScheduleFiresOncePerLocalDay(t *testing.T) {
	loc := time.UTC
	s := Schedule{
		Location:       loc,
		BackupAt:       time.Hour,
		Window:         Window{Start: 2 * time.Hour, End: 4 * time.Hour, Location: loc},
		TestRestoreDay: 1,
	}
	morning := time.Date(2026, 3, 1, 0, 30, 0, 0, loc)
	if due := s.Due(morning, ScheduleState{}, false); due.Backup || due.Update || due.TestRestore {
		t.Fatalf("too early: %+v", due)
	}
	backupTime := time.Date(2026, 3, 1, 1, 5, 0, 0, loc)
	due := s.Due(backupTime, ScheduleState{}, false)
	if !due.Backup || !due.TestRestore || due.Update {
		t.Fatalf("01:05 %+v", due)
	}
	afterBackup := ScheduleState{LastBackup: backupTime, LastTestRestore: backupTime}
	again := s.Due(backupTime.Add(time.Minute), afterBackup, false)
	if again.Backup || again.TestRestore {
		t.Fatalf("second tick %+v", again)
	}
	updateTime := time.Date(2026, 3, 1, 2, 15, 0, 0, loc)
	due = s.Due(updateTime, afterBackup, false)
	if !due.Update {
		t.Fatal("expected the update window")
	}
	due = s.Due(updateTime, afterBackup, true)
	if due.Update {
		t.Fatal("paused updates still ran")
	}
	nextDay := time.Date(2026, 3, 2, 1, 5, 0, 0, loc)
	due = s.Due(nextDay, afterBackup, false)
	if !due.Backup || due.TestRestore {
		t.Fatalf("next day %+v", due)
	}
}

func TestLogPrune(t *testing.T) {
	dir := t.TempDir()
	old := dir + "/app-old.log"
	keep := dir + "/app-new.log"
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-500 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := PruneLogFiles(dir, time.Now(), 400*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d %v", n, err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("old log remains")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal(err)
	}
}

func scheduleOps(t *testing.T, clock *time.Time, dump func(context.Context, io.Writer) error) *Operations {
	t.Helper()
	s := testStore(t)
	s.Now = func() time.Time { return *clock }
	if dump != nil {
		s.Dump = dump
	}
	dir := t.TempDir()
	ledger := OpenLedger(filepath.Join(dir, "ledger.jsonl"))
	ledger.now = func() time.Time { return *clock }
	outbox := OpenOutbox(filepath.Join(dir, "mail-outbox.jsonl"))
	outbox.now = func() time.Time { return *clock }
	return &Operations{
		Store:      s,
		Gate:       &Gate{},
		Ledger:     ledger,
		Outbox:     outbox,
		Passphrase: func() (string, error) { return s.Passphrase, nil },
		Now:        func() time.Time { return *clock },
	}
}

func subjects(t *testing.T, o *Outbox, subject string) int {
	t.Helper()
	notes, err := o.List()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, note := range notes {
		if note.Subject == subject {
			n++
		}
	}
	return n
}

func testSchedule() Schedule {
	return Schedule{
		Location:       time.UTC,
		BackupAt:       time.Hour,
		Window:         Window{Start: 2 * time.Hour, End: 4 * time.Hour, Location: time.UTC},
		TestRestoreDay: 1,
	}
}

func countEvents(t *testing.T, l *Ledger, kind string, match map[string]string) int {
	t.Helper()
	all, err := l.List(kind)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range all {
		ok := true
		for k, v := range match {
			if ev.Fields[k] != v {
				ok = false
			}
		}
		if ok {
			n++
		}
	}
	return n
}

func TestManualBackupBeforeNightlyTimeDoesNotSuppressNightly(t *testing.T) {
	clock := time.Date(2026, 3, 4, 0, 30, 0, 0, time.UTC)
	ops := scheduleOps(t, &clock, nil)
	if _, err := ops.CreateBackup(context.Background(), reasonManual); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{reasonPreUpdate, reasonSafety} {
		if _, err := ops.Ledger.Append("backup", "supervisor", map[string]string{"id": "x", "reason": reason}); err != nil {
			t.Fatal(err)
		}
	}
	events, _ := ops.Ledger.List("")
	if st := StateFromLedger(events); !st.LastBackup.IsZero() {
		t.Fatalf("non-nightly backup set the nightly marker: %v", st.LastBackup)
	}
	sched := &Scheduler{Ops: ops, Schedule: testSchedule()}
	clock = time.Date(2026, 3, 4, 1, 5, 0, 0, time.UTC)
	if err := sched.Tick(context.Background(), clock); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, ops.Ledger, "backup", map[string]string{"reason": reasonNightly}); n != 1 {
		t.Fatalf("nightly backups after the manual one: %d", n)
	}
	clock = clock.Add(time.Minute)
	if err := sched.Tick(context.Background(), clock); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, ops.Ledger, "backup", map[string]string{"reason": reasonNightly}); n != 1 {
		t.Fatalf("nightly backup ran twice in one day: %d", n)
	}
}

func TestFailedNightlyBackupIsRecordedBackedOffAndNotified(t *testing.T) {
	clock := time.Date(2026, 3, 4, 1, 5, 0, 0, time.UTC)
	calls := 0
	failing := true
	ops := scheduleOps(t, &clock, func(_ context.Context, w io.Writer) error {
		calls++
		if failing {
			return errors.New("pg_dump: connection refused")
		}
		_, err := io.WriteString(w, "COPY core.synthetic_items (id) FROM stdin;\n1\n\\.\n")
		return err
	})
	sched := &Scheduler{Ops: ops, Schedule: testSchedule()}
	tick := func(at time.Time) {
		t.Helper()
		clock = at
		if err := sched.Tick(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	day := func(h, m, s int) time.Time { return time.Date(2026, 3, 4, h, m, s, 0, time.UTC) }
	failed := map[string]string{"task": scheduledBackup, "status": scheduledFailed}

	tick(day(1, 5, 0))
	if calls != 1 || countEvents(t, ops.Ledger, eventScheduled, failed) != 1 {
		t.Fatalf("first failure: calls %d", calls)
	}
	notes, err := ops.Outbox.List()
	if err != nil || len(notes) != 1 || notes[0].Subject != "Nightly backup failed" {
		t.Fatalf("notice %+v %v", notes, err)
	}
	if !strings.Contains(notes[0].Body, "connection refused") || !strings.Contains(notes[0].Body, "2026-03-04 01:20") {
		t.Fatalf("notice body %q", notes[0].Body)
	}
	evs, _ := ops.Ledger.List(eventScheduled)
	if evs[0].Fields["attempt"] != "1" || evs[0].Fields["retry_after"] != "2026-03-04T01:20:00Z" {
		t.Fatalf("ledger fields %v", evs[0].Fields)
	}

	// Ticks inside the backoff do not rerun the dump.
	tick(day(1, 5, 30))
	tick(day(1, 19, 59))
	if calls != 1 {
		t.Fatalf("retried inside backoff: %d calls", calls)
	}
	// Second attempt after 15 minutes, third only after a further 30.
	tick(day(1, 20, 0))
	tick(day(1, 49, 59))
	if calls != 2 || countEvents(t, ops.Ledger, eventScheduled, failed) != 2 {
		t.Fatalf("second attempt: calls %d", calls)
	}
	tick(day(1, 50, 0))
	if calls != 3 {
		t.Fatalf("third attempt: calls %d", calls)
	}
	if n := subjects(t, ops.Outbox, "Nightly backup failed"); n != 1 {
		t.Fatalf("retries on the same day sent more mail: %d", n)
	}
	evs, _ = ops.Ledger.List(eventScheduled)
	if got := evs[len(evs)-1].Fields["attempt"]; got != "3" {
		t.Fatalf("attempt %s", got)
	}

	// A success after the backoff records the nightly and stops retries.
	failing = false
	tick(day(2, 50, 0))
	if calls != 4 || countEvents(t, ops.Ledger, "backup", map[string]string{"reason": reasonNightly}) != 1 {
		t.Fatalf("recovery: calls %d", calls)
	}
	tick(day(6, 0, 0))
	if calls != 4 {
		t.Fatalf("backup ran again after success: %d", calls)
	}

	// A new day's first failure runs at the nightly time without waiting on
	// yesterday's backoff and mails again.
	failing = true
	tick(time.Date(2026, 3, 5, 1, 0, 0, 0, time.UTC))
	if calls != 5 {
		t.Fatalf("next day: calls %d", calls)
	}
	if n := subjects(t, ops.Outbox, "Nightly backup failed"); n != 2 {
		t.Fatalf("next day's failure was not mailed: %d", n)
	}
}

type failingRegistry struct{ err error }

func (f failingRegistry) Latest(context.Context) (Release, error) { return Release{}, f.err }

func TestScheduledUpdateFailureIsRecordedBackedOffAndNotifiedOnlyWhenConfigured(t *testing.T) {
	cases := map[string]struct {
		updater func(ops *Operations, clock *time.Time) *Updater
		notices int
	}{
		// No updater wired: Operations.Update returns ErrReleaseIndexUnconfigured.
		"no updater": {func(*Operations, *time.Time) *Updater { return nil }, 0},
		// The shipped IndexRegistry with no TOGAVSU_RELEASE_INDEX.
		"no release index": {func(ops *Operations, clock *time.Time) *Updater {
			var order []string
			rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Image: "togavsu-core:1.0.0", Labels: map[string]string{"togavsu.version": "1.0.0"}}}}
			up := testUpdater(t, rt, &order)
			up.Now = func() time.Time { return *clock }
			up.Ledger = ops.Ledger
			up.Registry = IndexRegistry{}
			return up
		}, 0},
		// A configured index that cannot be read is a real failure.
		"release index unreachable": {func(ops *Operations, clock *time.Time) *Updater {
			var order []string
			rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Image: "togavsu-core:1.0.0", Labels: map[string]string{"togavsu.version": "1.0.0"}}}}
			up := testUpdater(t, rt, &order)
			up.Now = func() time.Time { return *clock }
			up.Ledger = ops.Ledger
			up.Registry = failingRegistry{errors.New("release index: connection refused")}
			return up
		}, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			clock := time.Date(2026, 3, 4, 2, 5, 0, 0, time.UTC)
			ops := scheduleOps(t, &clock, nil)
			ops.Updater = tc.updater(ops, &clock)
			sched := &Scheduler{Ops: ops, Schedule: testSchedule()}
			failed := map[string]string{"task": scheduledUpdate, "status": scheduledFailed}
			for _, at := range []time.Time{clock, clock.Add(30 * time.Second), clock.Add(14 * time.Minute)} {
				clock = at
				if err := sched.Tick(context.Background(), at); err != nil {
					t.Fatal(err)
				}
			}
			if n := countEvents(t, ops.Ledger, eventScheduled, failed); n != 1 {
				t.Fatalf("update attempts inside backoff: %d", n)
			}
			clock = clock.Add(time.Minute)
			if err := sched.Tick(context.Background(), clock); err != nil {
				t.Fatal(err)
			}
			if n := countEvents(t, ops.Ledger, eventScheduled, failed); n != 2 {
				t.Fatalf("update retry after backoff: %d", n)
			}
			if n := subjects(t, ops.Outbox, "Scheduled update check failed"); n != tc.notices {
				t.Fatalf("update failure notices %d, want %d", n, tc.notices)
			}
		})
	}
}

func TestNightlyBackupFailureNotifiesEvenWhenUpdatesAreUnconfigured(t *testing.T) {
	clock := time.Date(2026, 3, 4, 2, 5, 0, 0, time.UTC)
	ops := scheduleOps(t, &clock, func(context.Context, io.Writer) error { return errors.New("pg_dump: disk full") })
	sched := &Scheduler{Ops: ops, Schedule: testSchedule()}
	if err := sched.Tick(context.Background(), clock); err != nil {
		t.Fatal(err)
	}
	if n := subjects(t, ops.Outbox, "Nightly backup failed"); n != 1 {
		t.Fatalf("backup notices %d", n)
	}
	if n := subjects(t, ops.Outbox, "Scheduled update check failed"); n != 0 {
		t.Fatalf("unconfigured update mailed: %d", n)
	}
	if n := countEvents(t, ops.Ledger, eventScheduled, map[string]string{"task": scheduledUpdate, "status": scheduledFailed}); n != 1 {
		t.Fatalf("unconfigured update not recorded: %d", n)
	}
}

func TestUpToDateCheckMarksTheWindowDone(t *testing.T) {
	at := time.Date(2026, 3, 4, 2, 5, 0, 0, time.UTC)
	events := []Event{
		{Time: at.Add(-time.Minute), Kind: eventScheduled, Fields: map[string]string{"task": scheduledUpdate, "status": scheduledFailed}},
		{Time: at, Kind: eventScheduled, Fields: map[string]string{"task": scheduledUpdate, "status": scheduledChecked, "result": "up_to_date"}},
	}
	st := StateFromLedger(events)
	if !st.LastUpdate.Equal(at) || len(st.UpdateFailures) != 0 {
		t.Fatalf("state %+v", st)
	}
	if testSchedule().Due(at.Add(time.Minute), st, false).Update {
		t.Fatal("update check reran after an up-to-date result")
	}
}

func TestRetryDelayDoublesToCap(t *testing.T) {
	want := []time.Duration{0, 15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 4 * time.Hour}
	for n, d := range want {
		if got := retryDelay(n); got != d {
			t.Fatalf("retryDelay(%d) = %v want %v", n, got, d)
		}
	}
}
