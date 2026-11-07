package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Gate lets one backup, restore, or update run at a time.
type Gate struct{ mu sync.Mutex }

func (g *Gate) Try() (func(), bool) {
	if g == nil {
		return func() {}, true
	}
	if !g.mu.TryLock() {
		return nil, false
	}
	return g.mu.Unlock, true
}

// RestoreRecord is the result of one restore.
type RestoreRecord struct {
	ID       string    `json:"id"`
	BackupID string    `json:"backup_id"`
	Status   string    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
}

// Operations ties the archive store to the runtime. Restores stop the core
// first and bring it back when the data is in place.
type Operations struct {
	Store           *Store
	Updater         *Updater
	Runtime         Runtime
	Gate            *Gate
	Ledger          *Ledger
	Outbox          *Outbox
	ApplyDump       func(ctx context.Context, r io.Reader) error
	ApplyRestore    func(ctx context.Context, payload RestorePayload) error
	Measure         func(ctx context.Context, r io.Reader) (map[string]int64, error)
	Passphrase      func() (string, error)
	Now             func() time.Time
	RestoreTimeout  time.Duration
	RecoveryTimeout time.Duration
	jobs            map[string]RestoreRecord
	jobsMu          sync.Mutex
}

func (o *Operations) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Operations) refreshPass() error {
	if o.Passphrase == nil {
		if o.Store.Passphrase == "" {
			return ErrPassphraseRequired
		}
		return nil
	}
	pass, err := o.Passphrase()
	if err != nil {
		return err
	}
	o.Store.Passphrase = pass
	return nil
}

func (o *Operations) CreateBackup(ctx context.Context, reason string) (BackupMeta, error) {
	release, ok := o.Gate.Try()
	if !ok {
		return BackupMeta{}, ErrBusy
	}
	defer release()
	if err := o.refreshPass(); err != nil {
		return BackupMeta{}, err
	}
	meta, err := o.Store.Create(ctx, reason)
	if err != nil {
		return BackupMeta{}, err
	}
	if o.Ledger != nil {
		_, _ = o.Ledger.Append("backup", "supervisor", map[string]string{"id": meta.ID, "reason": reason})
	}
	return meta, nil
}

func (o *Operations) Update(ctx context.Context, force bool) (UpdateResult, error) {
	release, ok := o.Gate.Try()
	if !ok {
		return UpdateResult{}, ErrBusy
	}
	defer release()
	if o.Updater == nil {
		return UpdateResult{}, ErrReleaseIndexUnconfigured
	}
	if err := o.refreshPass(); err != nil && !errors.Is(err, ErrPassphraseRequired) {
		return UpdateResult{}, err
	}
	return o.Updater.Apply(ctx, force)
}

func (o *Operations) restoreTimeout() time.Duration {
	if o.RestoreTimeout > 0 {
		return o.RestoreTimeout
	}
	return 30 * time.Minute
}

func (o *Operations) recoveryTimeout() time.Duration {
	if o.RecoveryTimeout > 0 {
		return o.RecoveryTimeout
	}
	return time.Minute
}

// restartRecovery brings stopped cores back on a context that is not the
// restore deadline. A timed-out restore must still be able to start them.
func (o *Operations) restartRecovery(cores []ContainerInfo) error {
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	return o.restart(ctx, cores)
}

func (o *Operations) PutJob(rec RestoreRecord) {
	o.jobsMu.Lock()
	defer o.jobsMu.Unlock()
	if o.jobs == nil {
		o.jobs = map[string]RestoreRecord{}
	}
	o.jobs[rec.ID] = rec
}

func (o *Operations) Job(id string) (RestoreRecord, bool) {
	o.jobsMu.Lock()
	defer o.jobsMu.Unlock()
	rec, ok := o.jobs[id]
	return rec, ok
}

// Restore validates the archive before it stops writers or takes a recovery
// snapshot. The operation id is unique. The gate is held before the accepted
// record is published, so a second restore cannot replace this job. After the
// archive is accepted, work uses a timeout that is not derived from the
// caller's context, so a disconnected client cannot cancel recovery. Callers
// can poll Job or the ledger for status.
func (o *Operations) Restore(caller context.Context, id string) (RestoreRecord, error) {
	if err := o.rejectInvalid(caller, id); err != nil {
		return o.failedRestore(id, err)
	}
	rec, release, err := o.admitRestore(id)
	if err != nil {
		return RestoreRecord{}, err
	}
	defer release()
	return o.executeRestore(rec)
}

// admitRestore reserves the operation gate and publishes the accepted record.
// The caller must release the gate when the restore finishes. A busy result
// does not write a job.
func (o *Operations) admitRestore(backupID string) (RestoreRecord, func(), error) {
	release, ok := o.Gate.Try()
	if !ok {
		return RestoreRecord{}, nil, ErrBusy
	}
	opID, err := newRestoreOpID(o.now())
	if err != nil {
		release()
		return RestoreRecord{}, nil, err
	}
	rec := RestoreRecord{ID: opID, BackupID: backupID, Status: "accepted", Started: o.now().UTC()}
	o.PutJob(rec)
	return rec, release, nil
}

func (o *Operations) failedRestore(backupID string, cause error) (RestoreRecord, error) {
	opID, err := newRestoreOpID(o.now())
	if err != nil {
		return RestoreRecord{}, err
	}
	rec := RestoreRecord{ID: opID, BackupID: backupID, Started: o.now().UTC()}
	return o.finish(rec, cause), cause
}

func (o *Operations) rejectInvalid(ctx context.Context, id string) error {
	if err := o.refreshPass(); err != nil {
		return err
	}
	if o.ApplyDump == nil && o.ApplyRestore == nil {
		return errors.New("database restore is not configured")
	}
	if _, err := o.Store.Validate(id, ""); err != nil {
		return err
	}
	return ctx.Err()
}

// executeRestore runs a restore that admitRestore already reserved. It does
// not take the gate again.
func (o *Operations) executeRestore(rec RestoreRecord) (done RestoreRecord, err error) {
	defer func() {
		if done.ID != "" {
			o.PutJob(done)
		}
	}()
	opCtx, cancel := context.WithTimeout(context.Background(), o.restoreTimeout())
	defer cancel()
	id := rec.BackupID
	rec.Status = "running"
	o.PutJob(rec)
	var stopped []ContainerInfo
	if o.Runtime != nil {
		cores, err := o.Runtime.ListCores(opCtx)
		if err != nil {
			return o.finish(rec, err), err
		}
		for _, core := range cores {
			if err := o.Runtime.Stop(opCtx, core.ID); err != nil {
				if startErr := o.restartRecovery(stopped); startErr != nil {
					joined := fmt.Errorf("stop %s: %w; restart failed: %v", core.ID, err, startErr)
					return o.finish(rec, joined), joined
				}
				return o.finish(rec, err), err
			}
			stopped = append(stopped, core)
		}
	}
	// Writers are stopped, so this snapshot includes commits made before shutdown.
	safety, err := o.Store.Create(opCtx, reasonSafety)
	if err != nil {
		if startErr := o.restartRecovery(stopped); startErr != nil {
			joined := fmt.Errorf("safety backup: %w; restart failed: %v", err, startErr)
			return o.finish(rec, joined), joined
		}
		return o.finish(rec, err), err
	}
	_ = safety
	apply := func(ctx context.Context, payload RestorePayload) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if o.ApplyRestore != nil {
			return o.ApplyRestore(ctx, payload)
		}
		return o.ApplyDump(ctx, payload.Dump)
	}
	err = o.Store.Restore(opCtx, id, "", apply)
	if errors.Is(err, errSwapUnobserved) || errors.Is(err, errConfiguredDatabaseAbsent) {
		return o.finishUncertain(rec, stopped, err)
	}
	if errors.Is(err, ErrRestoreCleanup) {
		if startErr := o.restartRecovery(stopped); startErr != nil {
			joined := fmt.Errorf("%w; restart failed: %v", err, startErr)
			rec = o.finish(rec, joined)
			o.PutJob(rec)
			return rec, joined
		}
		rec.Status = "completed"
		rec.Error = "cleanup_pending"
		rec.Finished = o.now().UTC()
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), "Restore cleanup failed", "The restored database is live. The previous database could not be dropped. "+Scrub(err.Error()))
		}
		o.recordRestore(rec)
		o.PutJob(rec)
		return rec, err
	}
	if err != nil {
		// A clean-target apply that returns an error has not replaced the
		// live database. Restart the writers on that unchanged data.
		if startErr := o.restartRecovery(stopped); startErr != nil {
			joined := fmt.Errorf("restore failed: %w; restart failed: %v", err, startErr)
			if o.Outbox != nil {
				_ = o.Outbox.Notify(context.Background(), "Restore failed", "The database was not replaced and the core did not restart. "+Scrub(joined.Error()))
			}
			rec = o.finish(rec, joined)
			o.PutJob(rec)
			return rec, joined
		}
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), "Restore failed", "The archive was not applied. Existing data was left in place and the core was restarted. "+Scrub(err.Error()))
		}
		rec = o.finish(rec, err)
		o.PutJob(rec)
		return rec, err
	}
	if startErr := o.restartRecovery(stopped); startErr != nil {
		if o.Outbox != nil {
			_ = o.Outbox.Notify(opCtx, "Restore needs attention", "The data was restored and the core did not come back healthy. "+Scrub(startErr.Error()))
		}
		rec = o.finish(rec, startErr)
		o.PutJob(rec)
		return rec, startErr
	}
	rec.Status = "completed"
	rec.Finished = o.now().UTC()
	o.recordRestore(rec)
	o.PutJob(rec)
	return rec, nil
}

func (o *Operations) finishUncertain(rec RestoreRecord, stopped []ContainerInfo, err error) (RestoreRecord, error) {
	note := "The database rename could not be observed. The core was restarted without treating either database as confirmed."
	if errors.Is(err, errConfiguredDatabaseAbsent) {
		note = "The configured database name was absent after the rename. The core was restarted and may be unable to serve until that name exists."
	}
	if startErr := o.restartRecovery(stopped); startErr != nil {
		joined := fmt.Errorf("%w; restart failed: %v", err, startErr)
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), "Restore needs attention", Scrub(note+" "+joined.Error()))
		}
		rec = o.finish(rec, joined)
		o.PutJob(rec)
		return rec, joined
	}
	if o.Outbox != nil {
		_ = o.Outbox.Notify(context.Background(), "Restore needs attention", note)
	}
	rec = o.finish(rec, err)
	o.PutJob(rec)
	return rec, err
}

func (o *Operations) TestRestore(ctx context.Context, id string) error {
	release, ok := o.Gate.Try()
	if !ok {
		return ErrBusy
	}
	defer release()
	if err := o.refreshPass(); err != nil {
		return err
	}
	if o.Measure == nil {
		return errors.New("test restore is not configured")
	}
	err := o.Store.TestRestore(ctx, id, o.Measure)
	status := "ok"
	if err != nil {
		status = "failed"
		if o.Outbox != nil {
			_ = o.Outbox.Notify(ctx, "Test restore failed", Scrub(err.Error()))
		}
	}
	if o.Ledger != nil {
		_, _ = o.Ledger.Append("test-restore", "supervisor", map[string]string{"id": id, "status": status})
	}
	return err
}

func (o *Operations) restart(ctx context.Context, cores []ContainerInfo) error {
	if o.Runtime == nil || len(cores) == 0 {
		return nil
	}
	var errs []error
	for _, core := range cores {
		if err := o.Runtime.Start(ctx, core.ID); err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", core.ID, err))
			continue
		}
		if err := o.Runtime.WaitHealthy(ctx, core.ID, time.Minute); err != nil {
			errs = append(errs, fmt.Errorf("health %s: %w", core.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (o *Operations) finish(rec RestoreRecord, err error) RestoreRecord {
	rec.Status = "failed"
	code, _ := PublicCode(err)
	rec.Error = code
	rec.Finished = o.now().UTC()
	o.recordRestore(rec)
	return rec
}

func (o *Operations) recordRestore(rec RestoreRecord) {
	if o.Ledger == nil {
		return
	}
	_, _ = o.Ledger.Append("restore", "supervisor", map[string]string{
		"id":     rec.ID,
		"backup": rec.BackupID,
		"status": rec.Status,
	})
}
