package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The release e2e test's restore landed while the 30-second tick's nightly
// backup and prune held the gate, and the admin was told another operation
// was running. Scheduled work now yields: an admin request waits for it.
func TestGateAdminRequestWaitsForScheduledWork(t *testing.T) {
	g := &Gate{ScheduledWait: 5 * time.Second}
	release, ok := g.Acquire(Scheduled(context.Background()))
	if !ok {
		t.Fatal("scheduled work could not take a free gate")
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		release()
	}()
	start := time.Now()
	adminRelease, ok := g.Acquire(context.Background())
	if !ok {
		t.Fatal("admin request was refused while only scheduled work held the gate")
	}
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Fatalf("admin request took the gate after %v, before the scheduled holder released it", waited)
	}
	adminRelease()
	adminRelease() // a second release is harmless
	if r, ok := g.Try(); !ok {
		t.Fatal("gate stayed held after release")
	} else {
		r()
	}
}

func TestGateAdminRequestIsRefusedAtOnceWhileAnotherAdminRequestRuns(t *testing.T) {
	g := &Gate{ScheduledWait: 5 * time.Second}
	release, ok := g.Acquire(context.Background())
	if !ok {
		t.Fatal("admin request could not take a free gate")
	}
	defer release()
	start := time.Now()
	if _, ok := g.Acquire(context.Background()); ok {
		t.Fatal("two admin operations held the gate at once")
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("admin request waited %v behind another admin request instead of being refused", waited)
	}
	if _, ok := g.Acquire(Scheduled(context.Background())); ok {
		t.Fatal("scheduled work took the gate from an admin request")
	}
}

func TestGateScheduledWorkNeverWaits(t *testing.T) {
	g := &Gate{ScheduledWait: 5 * time.Second}
	release, ok := g.Acquire(Scheduled(context.Background()))
	if !ok {
		t.Fatal("scheduled work could not take a free gate")
	}
	defer release()
	start := time.Now()
	if _, ok := g.Acquire(Scheduled(context.Background())); ok {
		t.Fatal("two scheduled tasks held the gate at once")
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("scheduled work waited %v for the gate", waited)
	}
}

func TestGateAdminWaitIsBounded(t *testing.T) {
	g := &Gate{ScheduledWait: 100 * time.Millisecond}
	release, _ := g.Acquire(Scheduled(context.Background()))
	defer release()
	start := time.Now()
	if _, ok := g.Acquire(context.Background()); ok {
		t.Fatal("admin request took a gate that scheduled work still held")
	}
	if waited := time.Since(start); waited < 100*time.Millisecond || waited > 2*time.Second {
		t.Fatalf("admin request gave up after %v, want about the 100ms bound", waited)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := (&Gate{ScheduledWait: time.Hour}).Acquire(ctx); !ok {
		t.Fatal("a free gate was refused to a cancelled request that did not need to wait")
	}
	g2 := &Gate{ScheduledWait: time.Hour}
	r2, _ := g2.Acquire(Scheduled(context.Background()))
	defer r2()
	start = time.Now()
	if _, ok := g2.Acquire(ctx); ok || time.Since(start) > time.Second {
		t.Fatal("a cancelled admin request kept waiting for scheduled work")
	}
}

// Operations: an admin backup that arrives while the scheduler holds the gate
// completes once the scheduled work is done; one that arrives during another
// admin operation is still refused with ErrBusy.
func TestAdminBackupWaitsForTheTickButNotForAnotherAdminOperation(t *testing.T) {
	s := testStore(t)
	ops := &Operations{Store: s, Gate: &Gate{ScheduledWait: 5 * time.Second}, Passphrase: func() (string, error) { return s.Passphrase, nil }}
	release, ok := ops.Gate.Acquire(Scheduled(context.Background()))
	if !ok {
		t.Fatal("scheduler could not take the gate")
	}
	time.AfterFunc(100*time.Millisecond, release)
	if _, err := ops.CreateBackup(context.Background(), reasonManual); err != nil {
		t.Fatalf("admin backup during a scheduler tick: %v", err)
	}
	hold, ok := ops.Gate.Try()
	if !ok {
		t.Fatal("gate stayed held after the backup")
	}
	defer hold()
	if _, err := ops.CreateBackup(context.Background(), reasonManual); !errors.Is(err, ErrBusy) {
		t.Fatalf("admin backup during another admin operation: got %v, want ErrBusy", err)
	}
	if err := ops.Prune(Scheduled(context.Background()), 0); !errors.Is(err, ErrBusy) {
		t.Fatalf("scheduled prune during an admin operation: got %v, want ErrBusy", err)
	}
}
