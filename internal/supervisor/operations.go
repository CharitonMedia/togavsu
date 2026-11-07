package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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

// RestoreRecord is the stable restore status. Polling returns this shape from
// memory, the journal, and the ledger.
type RestoreRecord struct {
	ID       string    `json:"id"`
	BackupID string    `json:"backup_id"`
	Status   string    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Recovery string    `json:"recovery,omitempty"`
	Cleanup  string    `json:"cleanup,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
}

// Catalog reads live database names. An error means the catalog was not
// observed. Callers must not treat that as "the old database is still there".
type Catalog interface {
	Observe(ctx context.Context, configured, fresh, replaced string) (map[string]bool, error)
}

// Operations ties the archive store to the runtime. Restores stop the core
// first and bring it back only when the live catalog says that is safe.
type Operations struct {
	Store           *Store
	Updater         *Updater
	Runtime         Runtime
	Catalog         Catalog
	Gate            *Gate
	Ledger          *Ledger
	Journal         *Journal
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
	active          sync.WaitGroup
	shutting        atomic.Bool
}

func (o *Operations) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
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

// snapshotPass reads the passphrase and publishes it on Store. The caller
// must already hold the operation gate. Busy, invalid, and shutting-down
// requests return before this runs, so they cannot race a backup or restore
// that is reading Store.Passphrase.
func (o *Operations) snapshotPass() (string, error) {
	var pass string
	if o.Passphrase != nil {
		got, err := o.Passphrase()
		if err != nil {
			return "", err
		}
		pass = got
	} else if o.Store != nil {
		pass = o.Store.Passphrase
	}
	if pass == "" {
		return "", ErrPassphraseRequired
	}
	if o.Store != nil {
		o.Store.Passphrase = pass
	}
	return pass, nil
}

func (o *Operations) CreateBackup(ctx context.Context, reason string) (BackupMeta, error) {
	if o.shutting.Load() {
		return BackupMeta{}, ErrShuttingDown
	}
	release, ok := o.Gate.Try()
	if !ok {
		return BackupMeta{}, ErrBusy
	}
	defer release()
	if _, err := o.snapshotPass(); err != nil {
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
	if o.shutting.Load() {
		return UpdateResult{}, ErrShuttingDown
	}
	release, ok := o.Gate.Try()
	if !ok {
		return UpdateResult{}, ErrBusy
	}
	defer release()
	if err := o.blockUnresolved(); err != nil {
		return UpdateResult{}, err
	}
	if o.Updater == nil {
		return UpdateResult{}, ErrReleaseIndexUnconfigured
	}
	if _, err := o.snapshotPass(); err != nil && !errors.Is(err, ErrPassphraseRequired) {
		return UpdateResult{}, err
	}
	return o.Updater.Apply(ctx, force)
}

// BeginShutdown refuses new backup, update, and restore admission.
func (o *Operations) BeginShutdown() {
	if o != nil {
		o.shutting.Store(true)
	}
}

// Wait blocks until accepted restore goroutines finish.
func (o *Operations) Wait(ctx context.Context) error {
	if o == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		o.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Drain refuses new destructive work, stops the HTTP server, and waits for
// accepted restores to leave writers and the journal in a durable state.
func (o *Operations) Drain(srv *http.Server) error {
	o.BeginShutdown()
	var httpErr error
	if srv != nil {
		httpCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		httpErr = srv.Shutdown(httpCtx)
		cancel()
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), o.restoreTimeout())
	defer cancel()
	return errors.Join(httpErr, o.Wait(drainCtx))
}

// Serve recovers durable restore state before it accepts connections. On
// shutdown it drains accepted restores instead of returning when the listener
// closes.
func Serve(ctx context.Context, srv *http.Server, ops *Operations) error {
	if ops != nil {
		if err := ops.Recover(ctx); err != nil {
			return err
		}
	}
	errc := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()
	select {
	case <-ctx.Done():
		if ops == nil {
			shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return srv.Shutdown(shut)
		}
		return ops.Drain(srv)
	case err := <-errc:
		return err
	}
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

// LookupRestore returns one stable record. Memory, the journal, and the
// ledger all decode to RestoreRecord.
func (o *Operations) LookupRestore(id string) (RestoreRecord, bool, error) {
	if rec, ok := o.Job(id); ok {
		return rec, true, nil
	}
	if o.Journal != nil {
		got, ok, err := o.Journal.Latest(id)
		if err != nil {
			return RestoreRecord{}, false, err
		}
		if ok {
			return got.Record, true, nil
		}
	}
	if o.Ledger == nil {
		return RestoreRecord{}, false, nil
	}
	events, err := o.Ledger.List("restore")
	if err != nil {
		return RestoreRecord{}, false, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Fields["id"] != id {
			continue
		}
		return recordFromEvent(events[i]), true, nil
	}
	return RestoreRecord{}, false, nil
}

func recordFromEvent(ev Event) RestoreRecord {
	rec := RestoreRecord{
		ID:       ev.Fields["id"],
		BackupID: ev.Fields["backup_id"],
		Status:   ev.Fields["status"],
		Error:    ev.Fields["error"],
		Recovery: ev.Fields["recovery"],
		Cleanup:  ev.Fields["cleanup"],
	}
	if rec.BackupID == "" {
		rec.BackupID = ev.Fields["backup"]
	}
	if rec.Started, _ = time.Parse(time.RFC3339Nano, ev.Fields["started"]); rec.Started.IsZero() {
		rec.Started = ev.Time
	}
	rec.Finished, _ = time.Parse(time.RFC3339Nano, ev.Fields["finished"])
	return rec
}

// Restore validates under the gate, before any writer is stopped. The
// accepted id is published only after that admission succeeds.
func (o *Operations) Restore(caller context.Context, id string) (RestoreRecord, error) {
	rec, release, err := o.admitRestore(caller, id)
	if err != nil {
		if rec.ID != "" {
			return rec, err
		}
		return RestoreRecord{}, err
	}
	defer release()
	return o.executeRestore(rec)
}

// admitRestore reserves the gate before it reads the passphrase or validates
// the archive. A busy or shutting-down result does not publish an id and
// does not write Store.Passphrase. The caller must release when the restore
// finishes; release also marks the tracked operation done.
func (o *Operations) admitRestore(ctx context.Context, backupID string) (RestoreRecord, func(), error) {
	if o.shutting.Load() {
		return RestoreRecord{}, nil, ErrShuttingDown
	}
	release, ok := o.Gate.Try()
	if !ok {
		return RestoreRecord{}, nil, ErrBusy
	}
	if o.shutting.Load() {
		release()
		return RestoreRecord{}, nil, ErrShuttingDown
	}
	if err := o.blockUnresolved(); err != nil {
		release()
		return RestoreRecord{}, nil, err
	}
	pass, err := o.snapshotPass()
	if err != nil {
		release()
		rec, ferr := o.failedRestore(backupID, err)
		return rec, nil, ferr
	}
	if o.ApplyDump == nil && o.ApplyRestore == nil {
		release()
		rec, ferr := o.failedRestore(backupID, errors.New("database restore is not configured"))
		return rec, nil, ferr
	}
	man, err := o.Store.Validate(backupID, pass)
	if err != nil {
		release()
		rec, ferr := o.failedRestore(backupID, err)
		return rec, nil, ferr
	}
	if len(man.Files) > 0 {
		release()
		rec, ferr := o.failedRestore(backupID, ErrFileRestoreUnsupported)
		return rec, nil, ferr
	}
	if err := ctx.Err(); err != nil {
		release()
		rec, ferr := o.failedRestore(backupID, err)
		return rec, nil, ferr
	}
	if o.shutting.Load() {
		release()
		return RestoreRecord{}, nil, ErrShuttingDown
	}
	opID, err := newRestoreOpID(o.now())
	if err != nil {
		release()
		return RestoreRecord{}, nil, err
	}
	rec := RestoreRecord{ID: opID, BackupID: backupID, Status: "accepted", Started: o.now().UTC()}
	if _, err := o.persist(rec, phaseAccepted, nil); err != nil {
		release()
		return RestoreRecord{}, nil, err
	}
	o.active.Add(1)
	return rec, func() {
		release()
		o.active.Done()
	}, nil
}

func (o *Operations) failedRestore(backupID string, cause error) (RestoreRecord, error) {
	opID, err := newRestoreOpID(o.now())
	if err != nil {
		return RestoreRecord{}, err
	}
	rec := RestoreRecord{ID: opID, BackupID: backupID, Started: o.now().UTC()}
	return o.finish(rec, cause), cause
}

// executeRestore runs a restore that admitRestore already reserved.
func (o *Operations) executeRestore(rec RestoreRecord) (done RestoreRecord, err error) {
	defer func() {
		if done.ID != "" {
			o.PutJob(done)
		}
	}()
	opCtx, cancel := context.WithTimeout(context.Background(), o.restoreTimeout())
	defer cancel()
	rec.Status = "running"
	rec, err = o.persist(rec, phaseRunning, nil)
	if err != nil {
		return o.finish(rec, err), err
	}
	stopped, stopErr := o.stopWriters(rec)
	rec = o.current(rec.ID, rec)
	if stopErr != nil {
		if strings.Contains(stopErr.Error(), "was not observed") {
			_ = o.restoreWriters(rec, stopped)
			return o.keepPending(rec, phaseStopping, "container_unobserved", stopErr)
		}
		if len(stopped) == 0 && !o.stopIntended(rec.ID) {
			return o.finish(rec, stopErr), stopErr
		}
		return o.closeStopped(rec, stopped, stopErr, "")
	}
	rec, err = o.persist(rec, phaseWritersStopped, nil)
	if err != nil {
		return o.closeStopped(rec, stopped, err, "journal_unpersisted")
	}
	safety, err := o.Store.Create(opCtx, reasonSafety)
	if err != nil {
		return o.closeStopped(rec, stopped, err, "")
	}
	_ = safety
	apply := func(ctx context.Context, payload RestorePayload) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ctx = WithSwapHook(ctx, func(configured, fresh, replaced string) error {
			_, herr := o.persist(o.current(rec.ID, rec), phaseSwapIntended, func(cur *journalRecord) {
				cur.Database = &databaseIntent{
					Configured:   configured,
					Fresh:        fresh,
					Replaced:     replaced,
					SwapIntended: true,
				}
			})
			return herr
		})
		ctx = WithSwapCommitted(ctx, func(configured, fresh, replaced string) error {
			_, herr := o.persist(o.current(rec.ID, rec), phaseApplied, func(cur *journalRecord) {
				if cur.Database == nil {
					cur.Database = &databaseIntent{Configured: configured, Fresh: fresh, Replaced: replaced}
				}
				cur.Database.Configured = configured
				cur.Database.Fresh = fresh
				cur.Database.Replaced = replaced
				cur.Database.SwapIntended = true
				cur.Database.SwapCommitted = true
			})
			return herr
		})
		if _, perr := o.persist(o.current(rec.ID, rec), phaseApplying, nil); perr != nil {
			return perr
		}
		if o.ApplyRestore != nil {
			return o.ApplyRestore(ctx, payload)
		}
		return o.ApplyDump(ctx, payload.Dump)
	}
	err = o.Store.Restore(opCtx, rec.BackupID, "", apply)
	rec = o.current(rec.ID, rec)
	if errors.Is(err, errSwapUnobserved) || errors.Is(err, errConfiguredDatabaseAbsent) {
		return o.finishUncertain(rec, stopped, err)
	}
	if errors.Is(err, ErrRestoreCleanup) {
		return o.finishCleanup(rec, stopped, err)
	}
	if err != nil {
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), "Restore failed", "The archive was not applied. Existing data was left in place. "+Scrub(err.Error()))
		}
		return o.closeStopped(rec, stopped, err, "")
	}
	if _, err := o.persist(rec, phaseApplied, nil); err != nil {
		return o.finishApplied(rec, stopped, err)
	}
	rec = o.current(rec.ID, rec)
	return o.finishApplied(rec, stopped, nil)
}

func (o *Operations) finishApplied(rec RestoreRecord, stopped []ContainerInfo, persistErr error) (RestoreRecord, error) {
	if startErr := o.restoreWriters(rec, stopped); startErr != nil {
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), "Restore needs attention", "The data was restored and the core did not come back healthy. "+Scrub(startErr.Error()))
		}
		return o.keepPending(rec, phaseApplied, writerRecoveryReason(startErr), errors.Join(persistErr, startErr))
	}
	rec.Status = "completed"
	rec.Recovery = ""
	rec.Finished = o.now().UTC()
	saved, perr := o.persist(rec, phaseCompleted, nil)
	if perr != nil {
		return o.keepPending(rec, phaseApplied, "journal_unpersisted", errors.Join(persistErr, perr))
	}
	return saved, nil
}

func (o *Operations) finishCleanup(rec RestoreRecord, stopped []ContainerInfo, err error) (RestoreRecord, error) {
	rec.Error = "cleanup_pending"
	rec.Cleanup = "pending"
	if startErr := o.restoreWriters(rec, stopped); startErr != nil {
		return o.keepPending(rec, phaseApplied, writerRecoveryReason(startErr), startErr)
	}
	rec.Status = "completed"
	rec.Recovery = ""
	rec.Finished = o.now().UTC()
	if o.Outbox != nil {
		_ = o.Outbox.Notify(context.Background(), "Restore cleanup failed", "The restored database is live. The previous database could not be dropped. "+Scrub(err.Error()))
	}
	saved, perr := o.persist(rec, phaseCompleted, nil)
	if perr != nil {
		return o.keepPending(rec, phaseApplied, "journal_unpersisted", perr)
	}
	return saved, err
}

func (o *Operations) finishUncertain(rec RestoreRecord, stopped []ContainerInfo, cause error) (RestoreRecord, error) {
	decision, observed := o.classifyDatabase(rec)
	switch decision {
	case decisionCleanup:
		rec.Recovery = ""
		return o.finishCleanup(rec, stopped, ErrRestoreCleanup)
	case decisionOldIntact:
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), "Restore needs attention", "The live catalog still has the previous database. Writers are restarted only after that check.")
		}
		return o.closeStopped(rec, stopped, cause, "catalog_old_intact")
	case decisionMissing:
		rec.Status = "running"
		rec.Recovery = "configured_database_absent"
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), "Restore needs attention", "The configured database name is absent. Writers were not restarted.")
		}
		rec, _ = o.persist(rec, phaseSwapIntended, nil)
		return rec, cause
	default:
		rec.Status = "running"
		if observed {
			rec.Recovery = "catalog_ambiguous"
		} else {
			rec.Recovery = "catalog_unobserved"
		}
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), "Restore needs attention", "The database catalog could not be confirmed. Writers were not restarted and neither database was treated as current.")
		}
		rec, _ = o.persist(rec, phaseSwapIntended, nil)
		return rec, cause
	}
}

const (
	decisionUnknown = iota
	decisionCleanup
	decisionOldIntact
	decisionMissing
	decisionAmbiguous
)

func (o *Operations) classifyDatabase(rec RestoreRecord) (int, bool) {
	db := o.databaseOf(rec.ID)
	if db == nil || !db.SwapIntended || o.Catalog == nil {
		return decisionUnknown, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	st, err := o.Catalog.Observe(ctx, db.Configured, db.Fresh, db.Replaced)
	if err != nil || st == nil {
		return decisionUnknown, false
	}
	switch {
	case !st[db.Configured]:
		return decisionMissing, true
	case st[db.Replaced] && !st[db.Fresh]:
		return decisionCleanup, true
	case st[db.Fresh] && !st[db.Replaced]:
		return decisionOldIntact, true
	default:
		return decisionAmbiguous, true
	}
}

func (o *Operations) stopWriters(rec RestoreRecord) ([]ContainerInfo, error) {
	if o.Runtime == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.restoreTimeout())
	defer cancel()
	cores, err := o.Runtime.ListAllCores(ctx)
	if err != nil {
		return nil, err
	}
	var restart []ContainerInfo
	for _, core := range cores {
		prior := core.State
		if prior == "" {
			prior = "running"
		}
		intent := containerIntent{ID: core.ID, Name: core.Name, Prior: prior}
		if prior != "running" {
			_, _ = o.persist(rec, phaseStopping, func(cur *journalRecord) {
				cur.Containers = append(cur.Containers, intent)
			})
			continue
		}
		intent.StopIntended = true
		if _, err := o.persist(rec, phaseStopping, func(cur *journalRecord) {
			cur.Containers = upsertContainer(cur.Containers, intent)
		}); err != nil {
			return restart, err
		}
		stopErr := o.Runtime.Stop(ctx, core.ID)
		observed, inspectErr := o.observeContainer(core.ID)
		intent.Observed = observed
		if _, err := o.persist(rec, phaseStopping, func(cur *journalRecord) {
			cur.Containers = upsertContainer(cur.Containers, intent)
		}); err != nil {
			return restart, err
		}
		switch {
		case inspectErr != nil || observed == "unknown":
			return restart, fmt.Errorf("stop %s: container state was not observed: %v", core.ID, inspectErr)
		case observed != "running":
			restart = append(restart, core)
			if stopErr != nil {
				continue
			}
		default:
			if stopErr != nil {
				return restart, fmt.Errorf("stop %s: %w", core.ID, stopErr)
			}
			return restart, fmt.Errorf("stop %s: container still running", core.ID)
		}
	}
	return restart, nil
}

func (o *Operations) observeContainer(id string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	info, err := o.Runtime.Inspect(ctx, id)
	if err != nil {
		return "unknown", err
	}
	if info.Running || info.State == "running" || info.State == "" {
		return "running", nil
	}
	return info.State, nil
}

func upsertContainer(list []containerIntent, item containerIntent) []containerIntent {
	for i := range list {
		if list[i].ID == item.ID {
			list[i] = item
			return list
		}
	}
	return append(list, item)
}

func (o *Operations) restartConfirmed(cores []ContainerInfo) error {
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	return o.restart(ctx, cores)
}

// Recover reconciles incomplete restores before new destructive work is
// admitted. It inspects live containers and, when a swap was intended, the
// live database catalog. It does not start writers when that state is
// missing or ambiguous, and it does not rename a restored database back.
func (o *Operations) Recover(ctx context.Context) error {
	if o == nil || o.Journal == nil {
		return nil
	}
	if o.shutting.Load() {
		return ErrShuttingDown
	}
	release, ok := o.Gate.Try()
	if !ok {
		return ErrBusy
	}
	defer release()
	pending, err := o.Journal.Incomplete()
	if err != nil {
		return err
	}
	var errs []error
	for _, item := range pending {
		if err := o.recoverOne(ctx, item); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (o *Operations) recoverOne(ctx context.Context, item journalRecord) error {
	rec := item.Record
	if item.Phase == phaseApplied || rec.Cleanup == "pending" || rec.Error == "cleanup_pending" {
		if err := o.restoreWriters(rec, nil); err != nil {
			_, perr := o.keepPending(rec, phaseApplied, writerRecoveryReason(err), err)
			return errors.Join(err, perr)
		}
		if o.leftUnobserved(ctx, item) {
			cause := errors.New("container state was not observed")
			_, perr := o.keepPending(rec, phaseApplied, "container_unobserved", cause)
			return errors.Join(cause, perr)
		}
		if rec.Cleanup == "pending" || rec.Error == "cleanup_pending" {
			_, err := o.finishCleanup(rec, nil, ErrRestoreCleanup)
			if err != nil && !errors.Is(err, ErrRestoreCleanup) {
				return err
			}
			return nil
		}
		rec.Status = "completed"
		rec.Recovery = ""
		rec.Finished = o.now().UTC()
		if _, err := o.persist(rec, phaseCompleted, nil); err != nil {
			_, perr := o.keepPending(rec, phaseApplied, "journal_unpersisted", err)
			return errors.Join(err, perr)
		}
		return nil
	}
	if item.Database == nil || !item.Database.SwapIntended {
		switch rec.Recovery {
		case "catalog_unobserved", "catalog_ambiguous", "configured_database_absent":
			return fmt.Errorf("restore %s: %s; writers were not restarted", rec.ID, rec.Recovery)
		}
	}
	if !stopWasIntended(item) && (item.Database == nil || !item.Database.SwapIntended) {
		rec.Recovery = "interrupted"
		saved, err := o.markFailed(rec, errors.New("restore interrupted before it changed writers or the database"))
		if err != nil {
			o.PutJob(rec)
			return err
		}
		o.PutJob(saved)
		return nil
	}
	if item.Database != nil && item.Database.SwapIntended {
		decision, _ := o.classifyDatabase(rec)
		switch decision {
		case decisionCleanup:
			if _, err := o.finishCleanup(rec, nil, ErrRestoreCleanup); err != nil && !errors.Is(err, ErrRestoreCleanup) {
				return err
			}
			return nil
		case decisionOldIntact:
			saved, err := o.closeStopped(rec, nil, errors.New("restore interrupted before the database rename committed"), "catalog_old_intact")
			if saved.Status == "running" {
				return err
			}
			return nil
		case decisionMissing:
			_, err := o.keepPending(rec, phaseSwapIntended, "configured_database_absent", errConfiguredDatabaseAbsent)
			return err
		default:
			reason := "catalog_unobserved"
			if decision == decisionAmbiguous {
				reason = "catalog_ambiguous"
			}
			_, err := o.keepPending(rec, phaseSwapIntended, reason, errSwapUnobserved)
			return err
		}
	}
	saved, err := o.closeStopped(rec, nil, errors.New("restore interrupted after writers stopped and before the database rename"), "restarted_writers")
	if saved.Status == "running" {
		return err
	}
	return nil
}

// closeStopped terminal-fails only after every previously running writer is
// healthy again. A restart or journal failure leaves the same job pending.
func (o *Operations) closeStopped(rec RestoreRecord, stopped []ContainerInfo, cause error, recovery string) (RestoreRecord, error) {
	if err := o.restoreWriters(rec, stopped); err != nil {
		return o.keepPending(rec, phaseWritersStopped, writerRecoveryReason(err), errors.Join(cause, err))
	}
	if recovery != "" {
		rec.Recovery = recovery
	}
	saved, err := o.markFailed(rec, cause)
	if err != nil {
		return o.keepPending(rec, phaseWritersStopped, "journal_unpersisted", errors.Join(cause, err))
	}
	return saved, cause
}

// keepPending leaves a durable non-terminal record so the next startup
// retries the same operation. It does not roll a database back.
func (o *Operations) keepPending(rec RestoreRecord, phase, recovery string, cause error) (RestoreRecord, error) {
	rec.Status = "running"
	rec.Recovery = recovery
	rec.Finished = time.Time{}
	saved, err := o.persist(rec, phase, nil)
	if err != nil {
		o.PutJob(rec)
		return rec, errors.Join(cause, err)
	}
	return saved, cause
}

func (o *Operations) blockUnresolved() error {
	if o == nil || o.Journal == nil {
		return nil
	}
	pending, err := o.Journal.Incomplete()
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return ErrRecoveryRequired
	}
	return nil
}

func (o *Operations) stopIntended(id string) bool {
	item, ok := o.journalItem(id)
	return ok && stopWasIntended(item)
}

func (o *Operations) journalItem(id string) (journalRecord, bool) {
	if o.Journal == nil {
		return journalRecord{}, false
	}
	got, ok, err := o.Journal.Latest(id)
	if err != nil || !ok {
		return journalRecord{}, false
	}
	return got, true
}

// restoreWriters reconciles every container this restore stopped. An inspect
// error is not treated as stopped or healthy, and a container that is already
// running still has to pass its health check before the job can finish.
func (o *Operations) restoreWriters(rec RestoreRecord, fallback []ContainerInfo) error {
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	item, ok := o.journalItem(rec.ID)
	if ok && stopWasIntended(item) {
		return o.reconcileWriters(ctx, item)
	}
	return o.restart(ctx, fallback)
}

func (o *Operations) reconcileWriters(ctx context.Context, item journalRecord) error {
	if o.Runtime == nil {
		return errors.New("inspect runtime: unknown")
	}
	var errs []error
	for _, c := range item.Containers {
		if c.Prior != "running" || !c.StopIntended {
			continue
		}
		info, err := o.Runtime.Inspect(ctx, c.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect %s: %w", c.ID, err))
			continue
		}
		if info.State == "" && !info.Running {
			errs = append(errs, fmt.Errorf("inspect %s: unknown state", c.ID))
			continue
		}
		if !info.Running && info.State != "running" {
			if err := o.Runtime.Start(ctx, c.ID); err != nil {
				errs = append(errs, fmt.Errorf("start %s: %w", c.ID, err))
				continue
			}
		}
		if err := o.Runtime.WaitHealthy(ctx, c.ID, time.Minute); err != nil {
			errs = append(errs, fmt.Errorf("health %s: %w", c.ID, err))
		}
	}
	return errors.Join(errs...)
}

func writerRecoveryReason(err error) string {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "inspect") || strings.Contains(msg, "unknown") {
		return "container_unobserved"
	}
	return "restart_failed"
}

func stopWasIntended(item journalRecord) bool {
	for _, c := range item.Containers {
		if c.StopIntended {
			return true
		}
	}
	return false
}

func (o *Operations) leftUnobserved(ctx context.Context, item journalRecord) bool {
	if o.Runtime == nil {
		return false
	}
	for _, c := range item.Containers {
		if c.Prior != "running" || !c.StopIntended {
			continue
		}
		if _, err := o.Runtime.Inspect(ctx, c.ID); err != nil {
			return true
		}
	}
	return false
}

func (o *Operations) persistRunning(rec RestoreRecord) {
	rec.Status = "running"
	_, _ = o.persist(rec, phaseRunning, nil)
}

func (o *Operations) current(id string, fallback RestoreRecord) RestoreRecord {
	if o.Journal != nil {
		got, ok, err := o.Journal.Latest(id)
		if err == nil && ok {
			return got.Record
		}
	}
	if rec, ok := o.Job(id); ok {
		return rec
	}
	return fallback
}

func (o *Operations) databaseOf(id string) *databaseIntent {
	if o.Journal == nil {
		return nil
	}
	got, ok, err := o.Journal.Latest(id)
	if err != nil || !ok {
		return nil
	}
	return got.Database
}

func (o *Operations) persist(rec RestoreRecord, phase string, mutate func(*journalRecord)) (RestoreRecord, error) {
	cur := journalRecord{Record: rec, Phase: phase}
	if o.Journal != nil {
		prev, ok, err := o.Journal.Latest(rec.ID)
		if err != nil {
			return rec, err
		}
		if ok {
			cur.Containers = prev.Containers
			cur.Database = prev.Database
		}
	}
	cur.Record = rec
	cur.Phase = phase
	cur.Recovery = rec.Recovery
	cur.Cleanup = rec.Cleanup
	if mutate != nil {
		mutate(&cur)
	}
	cur.Record.Recovery = rec.Recovery
	cur.Record.Cleanup = rec.Cleanup
	if cur.Recovery == "" {
		cur.Record.Recovery = cur.Recovery
	}
	if cur.Cleanup == "" {
		cur.Record.Cleanup = cur.Cleanup
	}
	rec = cur.Record
	if o.Journal != nil {
		if err := o.Journal.Put(cur); err != nil {
			return rec, err
		}
	}
	o.PutJob(rec)
	o.recordRestore(rec)
	return rec, nil
}

func (o *Operations) TestRestore(ctx context.Context, id string) error {
	if o.shutting.Load() {
		return ErrShuttingDown
	}
	release, ok := o.Gate.Try()
	if !ok {
		return ErrBusy
	}
	defer release()
	if _, err := o.snapshotPass(); err != nil {
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
		if err := o.startOne(ctx, core.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (o *Operations) startOne(ctx context.Context, id string) error {
	info, err := o.Runtime.Inspect(ctx, id)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", id, err)
	}
	if !info.Running && info.State != "running" && info.State != "" {
		if err := o.Runtime.Start(ctx, id); err != nil {
			return fmt.Errorf("start %s: %w", id, err)
		}
	}
	if err := o.Runtime.WaitHealthy(ctx, id, time.Minute); err != nil {
		return fmt.Errorf("health %s: %w", id, err)
	}
	return nil
}

func (o *Operations) markFailed(rec RestoreRecord, err error) (RestoreRecord, error) {
	rec.Status = "failed"
	if rec.Error == "" || rec.Error == "cleanup_pending" {
		code, _ := PublicCode(err)
		if rec.Error != "cleanup_pending" {
			rec.Error = code
		}
	}
	rec.Finished = o.now().UTC()
	return o.persist(rec, phaseFailed, nil)
}

func (o *Operations) finish(rec RestoreRecord, err error) RestoreRecord {
	saved, perr := o.markFailed(rec, err)
	if perr != nil {
		o.PutJob(rec)
		return rec
	}
	return saved
}

func (o *Operations) recordRestore(rec RestoreRecord) {
	if o.Ledger == nil {
		return
	}
	fields := map[string]string{
		"id":        rec.ID,
		"backup_id": rec.BackupID,
		"backup":    rec.BackupID,
		"status":    rec.Status,
	}
	if rec.Error != "" {
		fields["error"] = rec.Error
	}
	if rec.Recovery != "" {
		fields["recovery"] = rec.Recovery
	}
	if rec.Cleanup != "" {
		fields["cleanup"] = rec.Cleanup
	}
	if !rec.Started.IsZero() {
		fields["started"] = rec.Started.UTC().Format(time.RFC3339Nano)
	}
	if !rec.Finished.IsZero() {
		fields["finished"] = rec.Finished.UTC().Format(time.RFC3339Nano)
	}
	_, _ = o.Ledger.Append("restore", "supervisor", fields)
}
