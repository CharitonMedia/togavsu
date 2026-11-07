package supervisor

import (
	"os"
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
