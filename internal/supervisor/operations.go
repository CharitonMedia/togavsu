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
	Store      *Store
	Updater    *Updater
	Runtime    Runtime
	Gate       *Gate
	Ledger     *Ledger
	Outbox     *Outbox
	ApplyDump  func(ctx context.Context, r io.Reader) error
	Measure    func(ctx context.Context, r io.Reader) (map[string]int64, error)
	Passphrase func() (string, error)
	Now        func() time.Time
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

// Restore stops the core, applies the dump, and starts the core again.
//
// ponytail: the HTTP call stays open until restore finishes, ceiling: proxy
// timeouts on a very large archive, upgrade: a restore job id polled with
// GET /v1/restores/{id}, trigger: a restore test runs longer than 60s.
func (o *Operations) Restore(ctx context.Context, id string) (RestoreRecord, error) {
	release, ok := o.Gate.Try()
	if !ok {
		return RestoreRecord{}, ErrBusy
	}
	defer release()
	if err := o.refreshPass(); err != nil {
		return RestoreRecord{}, err
	}
	if o.ApplyDump == nil {
		return RestoreRecord{}, errors.New("database restore is not configured")
	}
	rec := RestoreRecord{ID: id + "-restore", BackupID: id, Status: "running", Started: o.now().UTC()}
	safety, err := o.Store.Create(ctx, reasonSafety)
	if err != nil {
		return o.finish(rec, err), err
	}
	var stopped []ContainerInfo
	if o.Runtime != nil {
		cores, err := o.Runtime.ListCores(ctx)
		if err != nil {
			return o.finish(rec, err), err
		}
		for _, core := range cores {
			if err := o.Runtime.Stop(ctx, core.ID); err != nil {
				if startErr := o.restart(ctx, stopped); startErr != nil {
					joined := fmt.Errorf("stop %s: %w; restart failed: %v", core.ID, err, startErr)
					return o.finish(rec, joined), joined
				}
				return o.finish(rec, err), err
			}
			stopped = append(stopped, core)
		}
	}
	apply := func(ctx context.Context, payload RestorePayload) error {
		return o.ApplyDump(ctx, payload.Dump)
	}
	err = o.Store.Restore(ctx, id, "", apply)
	if err != nil {
		recErr := o.Store.Restore(ctx, safety.ID, "", apply)
		if recErr != nil {
			joined := fmt.Errorf("restore failed: %w; safety restore failed: %v", err, recErr)
			if o.Outbox != nil {
				_ = o.Outbox.Notify(ctx, "Restore failed", "The restore failed and the safety backup could not be applied. The core was left stopped. "+Scrub(joined.Error()))
			}
			rec = o.finish(rec, joined)
			return rec, joined
		}
		if startErr := o.restart(ctx, stopped); startErr != nil {
			joined := fmt.Errorf("restore failed: %w; safety backup applied; restart failed: %v", err, startErr)
			if o.Outbox != nil {
				_ = o.Outbox.Notify(ctx, "Restore failed", Scrub(joined.Error()))
			}
			rec = o.finish(rec, joined)
			return rec, joined
		}
		if o.Outbox != nil {
			_ = o.Outbox.Notify(ctx, "Restore failed", "The restore failed and the safety backup was applied. "+Scrub(err.Error()))
		}
		rec = o.finish(rec, err)
		return rec, err
	}
	if startErr := o.restart(ctx, stopped); startErr != nil {
		if o.Outbox != nil {
			_ = o.Outbox.Notify(ctx, "Restore needs attention", "The data was restored and the core did not come back healthy. "+Scrub(startErr.Error()))
		}
		rec = o.finish(rec, startErr)
		return rec, startErr
	}
	rec.Status = "completed"
	rec.Finished = o.now().UTC()
	o.recordRestore(rec)
	return rec, nil
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
