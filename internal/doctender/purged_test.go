package doctender

import (
	"testing"
)

// A file an admin empties from the trash before Doc Tender reaches it leaves a queued
// job with nothing to read. The worker must not take it (or fail on it, which stopped
// Drain and retried forever), and must not wait on it; a live file queued after it is
// still processed.
func TestPurgedFileJobIsNeverClaimed(t *testing.T) {
	e := newEnv(t)
	gone := e.upload(e.staff, e.open, "to-be-purged.txt", "text/plain", []byte("NOTICE OF HEARING\nCase 23AR-CV00825\n"))
	if _, err := e.files.Trash(e.ctx, e.staff, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.files.Purge(e.ctx, e.admin, gone.ID, gone.Name); err != nil {
		t.Fatal(err)
	}
	live := e.upload(e.staff, e.open, "live.txt", "text/plain", []byte("NOTICE OF HEARING\nCase 23AR-CV00825\n"))
	e.drain()
	if j := e.jobFor(gone.ID); j.Status != StatusQueued || j.Attempts != 0 || j.LeaseOwner != "" {
		t.Fatalf("the purged file's job ran: %+v", j)
	}
	if j := e.jobFor(live.ID); j.Status != StatusPublished {
		t.Fatalf("the live file after it: %s %s", j.Status, j.LastError)
	}
	if ran, err := e.svc.RunOnce(e.ctx); err != nil || ran {
		t.Fatalf("a pass after the purge ran a job: %v %v", ran, err)
	}
	wait, err := e.svc.nextWait(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lease, _ := e.svc.duration(e.ctx, "doctender.lease_timeout"); wait != lease {
		t.Fatalf("the worker waits %v for a purged file's job, want the idle %v", wait, lease)
	}
}
