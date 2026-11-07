package supervisor

import "time"

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

type ScheduleState struct {
	LastBackup      time.Time
	LastUpdate      time.Time
	LastTestRestore time.Time
}

type Due struct {
	Backup      bool
	Update      bool
	TestRestore bool
}

func (s Schedule) Due(now time.Time, state ScheduleState, paused bool) Due {
	loc := s.Location
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	mins := local.Hour()*60 + local.Minute()
	var due Due
	if mins >= int(s.BackupAt/time.Minute) && !sameDate(state.LastBackup, local) {
		due.Backup = true
	}
	if !paused && s.Window.Contains(now) && !sameDate(state.LastUpdate, local) {
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

// StateFromLedger rebuilds schedule markers from append-only history.
func StateFromLedger(events []Event) ScheduleState {
	var state ScheduleState
	for _, ev := range events {
		switch ev.Kind {
		case "backup":
			if ev.Time.After(state.LastBackup) {
				state.LastBackup = ev.Time
			}
		case "update":
			if ev.Time.After(state.LastUpdate) {
				state.LastUpdate = ev.Time
			}
		case "test-restore":
			if ev.Time.After(state.LastTestRestore) {
				state.LastTestRestore = ev.Time
			}
		}
	}
	return state
}
