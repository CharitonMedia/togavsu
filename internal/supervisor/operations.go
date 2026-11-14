package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Gate lets one backup, restore, or update run at a time. Scheduled work
// (the 30-second tick's nightly backup, update check, test restore and prune)
// holds it for moments at a time, so an admin's request that arrives during a
// tick waits up to ScheduledWait for that work to finish instead of being
// refused. A request that finds another admin request holding the gate, and
// scheduled work that finds it held at all, is refused at once.
type Gate struct {
	mu        sync.Mutex
	held      bool
	scheduled bool
	free      chan struct{}
	// ScheduledWait overrides scheduledWait (tests).
	ScheduledWait time.Duration
}

// scheduledWait bounds how long an admin request waits for scheduled work.
const scheduledWait = 30 * time.Second

type scheduledKey struct{}

// Scheduled marks ctx as the scheduler's, so its gate holds yield to admin
// requests.
func Scheduled(ctx context.Context) context.Context {
	return context.WithValue(ctx, scheduledKey{}, true)
}

func isScheduled(ctx context.Context) bool {
	v, _ := ctx.Value(scheduledKey{}).(bool)
	return v
}

// Try takes the gate for an admin request without waiting.
func (g *Gate) Try() (func(), bool) {
	return g.take(false, nil)
}

// Acquire takes the gate for ctx. Scheduled work never waits. An admin
// request waits for scheduled work up to ScheduledWait or until ctx ends.
func (g *Gate) Acquire(ctx context.Context) (func(), bool) {
	if isScheduled(ctx) {
		return g.take(true, nil)
	}
	wait := g.ScheduledWait
	if wait <= 0 {
		wait = scheduledWait
	}
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return g.take(false, wctx.Done())
}

// take acquires the gate. stop, when non-nil, lets an admin request wait for
// a scheduled holder until stop closes.
func (g *Gate) take(scheduled bool, stop <-chan struct{}) (func(), bool) {
	if g == nil {
		return func() {}, true
	}
	for {
		g.mu.Lock()
		if !g.held {
			g.held, g.scheduled, g.free = true, scheduled, make(chan struct{})
			free := g.free
			g.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					g.mu.Lock()
					g.held, g.scheduled = false, false
					close(free)
					g.mu.Unlock()
				})
			}, true
		}
		if stop == nil || !g.scheduled {
			g.mu.Unlock()
			return nil, false
		}
		free := g.free
		g.mu.Unlock()
		select {
		case <-free:
		case <-stop:
			return nil, false
		}
	}
}

// RestoreRecord is the stable restore status. Polling returns this shape from
// memory, the journal, and the ledger.
type RestoreRecord struct {
	ID       string `json:"id"`
	BackupID string `json:"backup_id"`
	// Source is empty for an archive already on the local volume, or
	// "upload" or "destination:<id>" for one imported by this restore.
	Source   string    `json:"source,omitempty"`
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
	Store         *Store
	Updater       *Updater
	Runtime       Runtime
	Catalog       Catalog
	Gate          *Gate
	Ledger        *Ledger
	Journal       *Journal
	UpdateJournal *UpdateJournal
	Outbox        *Outbox
	ApplyDump     func(ctx context.Context, r io.Reader) error
	ApplyRestore  func(ctx context.Context, payload RestorePayload) error
	Measure       func(ctx context.Context, r io.Reader) (map[string]int64, error)
	Passphrase    func() (string, error)
	// RestoreFiles hands an archive's file export (Manifest.FileBytes) to the
	// restarted core once the database is restored (CoreFiles.Import). Nil
	// rejects an archive that carries file bytes before writers stop.
	RestoreFiles    func(ctx context.Context, path string) error
	Now             func() time.Time
	RestoreTimeout  time.Duration
	RecoveryTimeout time.Duration
	jobs            map[string]RestoreRecord
	jobsMu          sync.Mutex
	// held lists restores whose terminal record is not published yet; the
	// value is that record once it exists.
	held map[string]*RestoreRecord
	// files holds each running restore's kept file export until the core is
	// back to take it.
	files map[string]keptFiles
	// rollingBack counts file rollbacks in progress. Their record is already
	// failed, so the journal alone would let users back into the restored core
	// while the safety backup is still being put back.
	rollingBack int
	active      sync.WaitGroup
	shutting    atomic.Bool
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
	release, ok := o.Gate.Acquire(ctx)
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
	release, ok := o.Gate.Acquire(ctx)
	if !ok {
		return UpdateResult{}, ErrBusy
	}
	defer release()
	if o.Updater != nil && o.Updater.Journal == nil {
		o.Updater.Journal = o.UpdateJournal
	}
	if err := o.admit(ctx); err != nil {
		return UpdateResult{}, err
	}
	if o.Updater == nil {
		return UpdateResult{}, ErrReleaseIndexUnconfigured
	}
	if _, err := o.snapshotPass(); err != nil && !errors.Is(err, ErrPassphraseRequired) {
		return UpdateResult{}, err
	}
	if o.Updater.Journal == nil {
		o.Updater.Journal = o.UpdateJournal
	}
	// The request context cancels the wait. Cleanup uses its own context, and
	// the gate stays held until that attempt finishes.
	opCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-opCtx.Done():
		}
	}()
	return o.Updater.Apply(opCtx, force)
}

func (o *Operations) DeleteBackup(ctx context.Context, id string) error {
	if o == nil || o.Store == nil {
		return ErrNotFound
	}
	if o.shutting.Load() {
		return ErrShuttingDown
	}
	release, ok := o.Gate.Acquire(ctx)
	if !ok {
		return ErrBusy
	}
	defer release()
	if err := o.admit(ctx); err != nil {
		return err
	}
	return o.Store.Delete(ctx, id)
}

func (o *Operations) Prune(ctx context.Context, local time.Duration) error {
	if o == nil || o.Store == nil {
		return nil
	}
	if o.shutting.Load() {
		return ErrShuttingDown
	}
	release, ok := o.Gate.Acquire(ctx)
	if !ok {
		return ErrBusy
	}
	defer release()
	if err := o.admit(ctx); err != nil {
		return err
	}
	var errs []error
	if local > 0 {
		if _, err := o.Store.Prune(ctx, local); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := o.Store.PruneDestinations(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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
	if _, ok := o.held[rec.ID]; ok && rec.ID != "" && terminalRestore(rec.Status) {
		copied := rec
		o.held[rec.ID] = &copied
		return
	}
	if o.jobs == nil {
		o.jobs = map[string]RestoreRecord{}
	}
	o.jobs[rec.ID] = rec
}

func terminalRestore(status string) bool {
	return status == "completed" || status == "failed"
}

// holdRestore keeps id's terminal record unpublished until unholdRestore.
// Each restore has its own entry, so a restore admitted the moment the
// gate frees cannot drop an earlier restore's record.
func (o *Operations) holdRestore(id string) {
	o.jobsMu.Lock()
	defer o.jobsMu.Unlock()
	if o.held == nil {
		o.held = map[string]*RestoreRecord{}
	}
	o.held[id] = nil
}

// unholdRestore publishes the terminal record that holdRestore kept back.
// Call it only after the gate is free.
func (o *Operations) unholdRestore(id string) {
	o.jobsMu.Lock()
	defer o.jobsMu.Unlock()
	rec, ok := o.held[id]
	if !ok {
		return
	}
	delete(o.held, id)
	if rec != nil {
		if o.jobs == nil {
			o.jobs = map[string]RestoreRecord{}
		}
		o.jobs[id] = *rec
	}
}

// runRestore executes an admitted restore and frees its gate. A client that
// polls "completed" or "failed" can start the next operation at once: the
// terminal record is published only after release. Wait still waits for
// that publication. A panic still releases the gate and publishes any
// terminal record.
func (o *Operations) runRestore(rec RestoreRecord, release func()) (RestoreRecord, error) {
	o.holdRestore(rec.ID)
	o.active.Add(1)
	var once sync.Once
	finish := func() {
		once.Do(func() {
			release()
			o.unholdRestore(rec.ID)
			o.active.Done()
		})
	}
	defer finish()
	done, err := o.executeRestore(rec)
	finish()
	return done, err
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
		Source:   ev.Fields["source"],
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
	return o.runRestore(rec, release)
}

// admitRestore reserves the gate before it reads the passphrase or validates
// the archive. A busy or shutting-down result does not publish an id and
// does not write Store.Passphrase. The caller must release when the restore
// finishes; release also marks the tracked operation done.
func (o *Operations) admitRestore(ctx context.Context, backupID string) (RestoreRecord, func(), error) {
	return o.admitRestoreFrom(ctx, backupID, nil)
}

// restoreSource is an archive that is not on the local volume yet. Stage
// brings it there and validates it with the passphrase admission already
// read. It returns the backup id as soon as that is known, also on failure,
// so the failed record names it. Origin is "upload" or "destination:<id>".
type restoreSource struct {
	Origin string
	Stage  func(ctx context.Context, passphrase string) (string, Manifest, error)
}

// admitRestoreFrom is admitRestore for an archive from src. Stage runs under
// the gate, after recovery and the passphrase read and before the accepted
// id is published. It replaces Store.Validate: Store.Import runs that same
// decrypt and checksum work before it publishes the archive. A nil src
// validates an archive already on the local volume.
func (o *Operations) admitRestoreFrom(ctx context.Context, backupID string, src *restoreSource) (RestoreRecord, func(), error) {
	source := ""
	if src != nil {
		source = src.Origin
	}
	if o.shutting.Load() {
		return RestoreRecord{}, nil, ErrShuttingDown
	}
	release, ok := o.Gate.Acquire(ctx)
	if !ok {
		return RestoreRecord{}, nil, ErrBusy
	}
	if o.shutting.Load() {
		release()
		return RestoreRecord{}, nil, ErrShuttingDown
	}
	if err := o.admit(ctx); err != nil {
		release()
		return RestoreRecord{}, nil, err
	}
	pass, err := o.snapshotPass()
	if err != nil {
		release()
		rec, ferr := o.failedRestore(backupID, source, err)
		return rec, nil, ferr
	}
	if o.ApplyDump == nil && o.ApplyRestore == nil {
		release()
		rec, ferr := o.failedRestore(backupID, source, errors.New("database restore is not configured"))
		return rec, nil, ferr
	}
	var man Manifest
	if src != nil {
		var id string
		id, man, err = src.Stage(ctx, pass)
		if id != "" {
			backupID = id
		}
	} else {
		man, err = o.Store.Validate(backupID, pass)
	}
	if err != nil {
		release()
		rec, ferr := o.failedRestore(backupID, source, err)
		return rec, nil, ferr
	}
	if len(man.Files) > 0 || (man.FileBytes != nil && o.RestoreFiles == nil) {
		release()
		rec, ferr := o.failedRestore(backupID, source, ErrFileRestoreUnsupported)
		return rec, nil, ferr
	}
	if err := ctx.Err(); err != nil {
		release()
		rec, ferr := o.failedRestore(backupID, source, err)
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
	rec := RestoreRecord{ID: opID, BackupID: backupID, Source: source, Status: "accepted", Started: o.now().UTC()}
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

func (o *Operations) failedRestore(backupID, source string, cause error) (RestoreRecord, error) {
	opID, err := newRestoreOpID(o.now())
	if err != nil {
		return RestoreRecord{}, err
	}
	rec := RestoreRecord{ID: opID, BackupID: backupID, Source: source, Started: o.now().UTC()}
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
	apply := func(ctx context.Context, payload RestorePayload) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The file set is checked whole (manifest complete, every object's size and
		// SHA-256) and spooled before the database is touched, so a damaged or partial
		// export fails here with the existing data in place.
		if payload.FileBytes != "" {
			if err := checkFileExport(payload.FileBytes); err != nil {
				return err
			}
			if err := o.keepFiles(rec.ID, payload.FileBytes, safety.ID); err != nil {
				return err
			}
		}
		ctx = WithRestoreOpID(ctx, rec.ID)
		ctx = WithAllocHook(ctx, func(a RestoreAlloc) error {
			_, herr := o.persist(o.current(rec.ID, rec), phaseApplying, func(cur *journalRecord) {
				cur.Database = &databaseIntent{
					Configured: a.Configured, Fresh: a.Fresh, Replaced: a.Replaced,
					Allocated: true, OpID: a.OpID, ClusterID: a.ClusterID,
					FreshOID: a.FreshOID, ConfiguredOID: a.ConfiguredOID,
				}
			})
			return herr
		})
		ctx = WithSwapHook(ctx, func(configured, fresh, replaced string) error {
			_, herr := o.persist(o.current(rec.ID, rec), phaseSwapIntended, func(cur *journalRecord) {
				prev := cur.Database
				cur.Database = &databaseIntent{
					Configured:   configured,
					Fresh:        fresh,
					Replaced:     replaced,
					SwapIntended: true,
				}
				if prev != nil {
					cur.Database.Allocated = prev.Allocated
					cur.Database.OpID = prev.OpID
					cur.Database.ClusterID = prev.ClusterID
					cur.Database.FreshOID = prev.FreshOID
					cur.Database.ConfiguredOID = prev.ConfiguredOID
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
				cur.Database.SwapRolledBack = false
			})
			return herr
		})
		ctx = WithSwapRolledBack(ctx, func(configured, fresh, replaced string) error {
			_, herr := o.persist(o.current(rec.ID, rec), phaseRolledBack, func(cur *journalRecord) {
				if cur.Database == nil {
					cur.Database = &databaseIntent{Configured: configured, Fresh: fresh, Replaced: replaced}
				}
				cur.Database.Configured = configured
				cur.Database.Fresh = fresh
				cur.Database.Replaced = replaced
				cur.Database.SwapIntended = true
				cur.Database.SwapCommitted = false
				cur.Database.SwapRolledBack = true
			})
			return herr
		})
		var spooled func(*journalRecord)
		if payload.FileBytes != "" {
			spooled = func(cur *journalRecord) {
				cur.Files = filesPending
				cur.FilesSafety = safety.ID
			}
		}
		if _, perr := o.persist(o.current(rec.ID, rec), phaseApplying, spooled); perr != nil {
			return perr
		}
		if o.ApplyRestore != nil {
			return o.ApplyRestore(ctx, payload)
		}
		return o.ApplyDump(ctx, payload.Dump)
	}
	err = o.Store.Restore(opCtx, rec.BackupID, "", apply)
	rec = o.current(rec.ID, rec)
	if errors.Is(err, errCommitUnjournaled) {
		return o.finishCommittedUnjournaled(rec, stopped, err)
	}
	if errors.Is(err, errRollbackUnjournaled) {
		return o.finishRollbackUnjournaled(rec, stopped, err)
	}
	if errors.Is(err, errSwapUnobserved) || errors.Is(err, errConfiguredDatabaseAbsent) {
		return o.finishUncertain(rec, stopped, err)
	}
	if errors.Is(err, ErrRestoreCleanup) {
		return o.finishCleanup(rec, stopped, err)
	}
	if err != nil {
		if kept, ok := o.takeFiles(rec.ID); ok {
			kept.cleanup()
		}
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
	rec, done, ferr := o.importFiles(rec)
	if done {
		return rec, ferr
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

// importFiles writes a committed restore's file export through the running core.
// Every path that ends a committed swap calls it once the writers are back, so a
// restore never ends completed while the bytes it spooled are unwritten. Only the
// running core can write them: it holds the storage backends' credentials and reads
// where each object lives from the restored database. The set was checked and spooled
// before the swap, so what is left to fail is the backend write itself, and that
// rolls back to the safety backup. The spool exists only in the process that made it;
// a recovery that finds the journal's Files still pending without it (the supervisor
// restarted after the swap) cannot write the bytes and rolls back the same way.
// done is true when the record was finished or left pending here.
func (o *Operations) importFiles(rec RestoreRecord) (RestoreRecord, bool, error) {
	item, _ := o.journalItem(rec.ID)
	kept, ok := o.takeFiles(rec.ID)
	if !ok {
		if item.Files != filesPending {
			return rec, false, nil
		}
		saved, err := o.rollBackFiles(rec, item.FilesSafety, errFilesLost)
		return saved, true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.restoreTimeout())
	ferr := o.RestoreFiles(ctx, kept.path)
	cancel()
	kept.cleanup()
	if ferr != nil {
		saved, err := o.rollBackFiles(rec, kept.safety, ferr)
		return saved, true, err
	}
	if item.Files == "" {
		return rec, false, nil
	}
	phase := item.Phase
	if phase == "" {
		phase = phaseApplied
	}
	saved, perr := o.persist(rec, phase, func(cur *journalRecord) {
		cur.Files = ""
		cur.FilesSafety = ""
	})
	if perr != nil {
		// The journal still says pending, so a later recovery rolls back rather
		// than trust bytes it cannot see were written.
		saved, err := o.keepPending(rec, phaseApplied, "journal_unpersisted", perr)
		return saved, true, err
	}
	return saved, false, nil
}

func (o *Operations) finishCleanup(rec RestoreRecord, stopped []ContainerInfo, err error) (RestoreRecord, error) {
	rec.Error = "cleanup_pending"
	rec.Cleanup = "pending"
	if startErr := o.restoreWriters(rec, stopped); startErr != nil {
		return o.keepPending(rec, phaseApplied, writerRecoveryReason(startErr), startErr)
	}
	cleared, derr := o.retryReplacedDrop(rec)
	if !cleared {
		if db := o.databaseOf(rec.ID); db == nil || !db.SwapCommitted {
			if derr != nil {
				return o.keepPending(rec, phaseSwapIntended, "commit_unjournaled", derr)
			}
		}
	}
	rec, done, ferr := o.importFiles(rec)
	if done {
		return rec, ferr
	}
	rec.Status = "completed"
	rec.Recovery = ""
	rec.Finished = o.now().UTC()
	if cleared {
		rec.Error = ""
		rec.Cleanup = ""
		saved, perr := o.persist(rec, phaseCompleted, nil)
		if perr != nil {
			rec.Error = "cleanup_pending"
			rec.Cleanup = "pending"
			return o.keepPending(rec, phaseApplied, "journal_unpersisted", perr)
		}
		if o.Outbox != nil {
			body := "The restored database is live. The previous database is gone."
			if errors.Is(err, errCommitUnjournaled) {
				body = "The restored database is live. The commit was not recorded. The previous database is gone."
			}
			_ = o.Outbox.Notify(context.Background(), "Restore cleanup finished", body)
		}
		return saved, nil
	}
	if o.Outbox != nil {
		body := "The restored database is live. The previous database could not be dropped. "
		if errors.Is(err, errCommitUnjournaled) {
			body = "The restored database is live. The commit was not recorded, so the previous database was left in place. "
		}
		cause := err
		if derr != nil {
			cause = derr
		}
		if cause == nil {
			cause = ErrRestoreCleanup
		}
		_ = o.Outbox.Notify(context.Background(), "Restore cleanup failed", body+Scrub(cause.Error()))
	}
	saved, perr := o.persist(rec, phaseCompleted, nil)
	if perr != nil {
		return o.keepPending(rec, phaseApplied, "journal_unpersisted", perr)
	}
	if err == nil {
		err = ErrRestoreCleanup
	}
	return saved, err
}

// retryReplacedDrop removes only the replaced database after the catalog
// shows the swap committed. The commit is persisted before that delete. An
// unreadable or ambiguous catalog drops nothing. Absence, including a lost
// drop acknowledgment, completes cleanup. The configured database is never
// renamed back.
func (o *Operations) retryReplacedDrop(rec RestoreRecord) (bool, error) {
	db := o.databaseOf(rec.ID)
	if db == nil || !db.SwapIntended || db.SwapRolledBack || db.Replaced == "" || db.Replaced == db.Configured {
		return false, nil
	}
	if o.Catalog == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	// Cluster identity is the journal's recorded cluster, not the oid of
	// whatever database currently bears the configured name. A later restore
	// may replace that live oid while this cleanup target is still owed.
	if db.ClusterID != "" {
		if err := o.confirmMaintenanceCluster(ctx, db); err != nil {
			return false, err
		}
	}
	st, observed := o.observeSwap(rec)
	if !observed {
		return false, errSwapUnobserved
	}
	if !db.SwapCommitted {
		if _, err := o.persist(rec, phaseApplied, func(cur *journalRecord) {
			if cur.Database == nil {
				copied := *db
				cur.Database = &copied
			}
			cur.Database.Configured = db.Configured
			cur.Database.Fresh = db.Fresh
			cur.Database.Replaced = db.Replaced
			cur.Database.SwapIntended = true
			cur.Database.SwapCommitted = true
			cur.Database.SwapRolledBack = false
		}); err != nil {
			return false, err
		}
		db = o.databaseOf(rec.ID)
		if db == nil || !db.SwapCommitted {
			return false, errors.New("commit was not recorded")
		}
	}
	if !st.replaced {
		if !st.configured || st.fresh {
			return false, errSwapUnobserved
		}
		return true, nil
	}
	if !st.configured || st.fresh {
		return false, errSwapUnobserved
	}
	var dropErr error
	if db.ClusterID != "" || db.ConfiguredOID != "" {
		if db.OpID == "" || db.ConfiguredOID == "" {
			return false, errSwapUnobserved
		}
		look, ok := o.Catalog.(interface {
			DatabaseOID(context.Context, string) (string, error)
		})
		if !ok {
			return false, errSwapUnobserved
		}
		replacedOID, err := look.DatabaseOID(ctx, db.Replaced)
		if err != nil || replacedOID == "" || replacedOID != db.ConfiguredOID {
			return false, errSwapUnobserved
		}
		dropper, ok := o.Catalog.(interface {
			DropVerifiedReplaced(context.Context, string, string, string) error
		})
		if !ok {
			return false, errSwapUnobserved
		}
		dropErr = dropper.DropVerifiedReplaced(ctx, db.OpID, db.Replaced, db.ConfiguredOID)
	} else {
		cleaner, ok := o.Catalog.(interface {
			Drop(context.Context, string) error
		})
		if !ok {
			return false, errors.New("catalog cannot drop the replaced database")
		}
		dropErr = cleaner.Drop(ctx, db.Replaced)
	}
	st, observed = o.observeSwap(rec)
	if observed && st.configured && !st.fresh && !st.replaced {
		return true, nil
	}
	if dropErr != nil {
		return false, dropErr
	}
	return false, errSwapUnobserved
}

// finishCommittedUnjournaled keeps a committed rename out of the ordinary
// unchanged-data failure. The previous database is not dropped. Writers
// restart only when the catalog shows the restored name is live.
func (o *Operations) finishCommittedUnjournaled(rec RestoreRecord, stopped []ContainerInfo, cause error) (RestoreRecord, error) {
	st, observed := o.observeSwap(rec)
	switch {
	case observed && st.configured && st.replaced && !st.fresh:
		return o.finishCleanup(rec, stopped, cause)
	case observed && st.configured && !st.replaced && !st.fresh:
		return o.finishApplied(rec, stopped, cause)
	default:
		reason := "commit_unjournaled"
		if observed {
			reason = "catalog_ambiguous"
		} else if o.Catalog != nil {
			reason = "catalog_unobserved"
		}
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), "Restore needs attention", "The restore commit was not recorded. Writers were not restarted and neither recovery database was deleted. "+Scrub(cause.Error()))
		}
		return o.keepPending(rec, phaseSwapIntended, reason, cause)
	}
}

// finishRollbackUnjournaled leaves the loaded database in place. The
// configured data is unchanged, and the poll says so without deleting the
// name that proves the rollback.
func (o *Operations) finishRollbackUnjournaled(rec RestoreRecord, stopped []ContainerInfo, cause error) (RestoreRecord, error) {
	if kept, ok := o.takeFiles(rec.ID); ok {
		kept.cleanup()
	}
	if o.Outbox != nil {
		_ = o.Outbox.Notify(context.Background(), "Restore failed", "The archive was not applied. Existing data was left in place. The loaded database was kept because the rollback was not recorded. "+Scrub(cause.Error()))
	}
	return o.closeStopped(rec, stopped, cause, "rollback_unjournaled")
}

type swapNames struct {
	configured bool
	fresh      bool
	replaced   bool
}

func (o *Operations) observeSwap(rec RestoreRecord) (swapNames, bool) {
	db := o.databaseOf(rec.ID)
	if db == nil || o.Catalog == nil {
		return swapNames{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	st, err := o.Catalog.Observe(ctx, db.Configured, db.Fresh, db.Replaced)
	if err != nil || st == nil {
		return swapNames{}, false
	}
	return swapNames{configured: st[db.Configured], fresh: st[db.Fresh], replaced: st[db.Replaced]}, true
}

func phaseForgetsCommit(phase string) bool {
	switch phase {
	case phaseAccepted, phaseRunning, phaseStopping, phaseWritersStopped, phaseApplying, phaseSwapIntended, phaseRestarting, phaseRolledBack:
		return true
	default:
		return false
	}
}

func (o *Operations) finishUncertain(rec RestoreRecord, stopped []ContainerInfo, cause error) (RestoreRecord, error) {
	if db := o.databaseOf(rec.ID); db != nil && db.SwapCommitted && !db.SwapRolledBack {
		return o.finishCommittedUncertain(rec, stopped, cause)
	}
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
	if err := o.confirmCluster(ctx, db); err != nil {
		return decisionUnknown, false
	}
	st, err := o.Catalog.Observe(ctx, db.Configured, db.Fresh, db.Replaced)
	if err != nil || st == nil {
		return decisionUnknown, false
	}
	// Unchanged names are not a rollback until this operation row is
	// cancelled. That cancel commits before writers restart. A lock timeout
	// or a missing row stays pending. A committed row is never cancelled.
	// The same fence applies after reconciliation has already removed the
	// fresh database: identity, not the missing name, decides the outcome.
	state := ""
	removedFresh := st[db.Configured] && !st[db.Fresh] && !st[db.Replaced] && db.ConfiguredOID != ""
	if (st[db.Configured] && st[db.Fresh] && !st[db.Replaced]) || removedFresh {
		var ferr error
		state, ferr = o.fenceSwap(ctx, db)
		if ferr != nil || state == "absent" {
			return decisionUnknown, false
		}
		st, err = o.Catalog.Observe(ctx, db.Configured, db.Fresh, db.Replaced)
		if err != nil || st == nil {
			return decisionUnknown, false
		}
	}
	decision := decisionFromCatalog(st, db)
	if state == "cancelled" && st[db.Configured] && !st[db.Fresh] && !st[db.Replaced] && o.originalDatabase(ctx, db) {
		decision = decisionOldIntact
	}
	if state != "" && state != "cancelled" && state != "committed" {
		return decisionUnknown, false
	}
	if state == "cancelled" && decision != decisionOldIntact {
		return decisionUnknown, false
	}
	if state == "committed" && decision == decisionOldIntact {
		return decisionUnknown, false
	}
	if state == "" && decision == decisionOldIntact {
		return decisionUnknown, false
	}
	return decision, true
}

func (o *Operations) finishAllocation(rec RestoreRecord, db *databaseIntent) error {
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	if err := o.confirmCluster(ctx, db); err != nil {
		_, perr := o.keepPending(rec, phaseApplying, "allocation_unverified", err)
		return errors.Join(err, perr)
	}
	if db.FreshOID != "" && db.OpID != "" {
		dropper, ok := o.Catalog.(interface {
			DropVerified(context.Context, string, string, string) error
		})
		if !ok {
			_, err := o.keepPending(rec, phaseApplying, "allocation_unverified", errSwapUnobserved)
			return err
		}
		if err := dropper.DropVerified(ctx, db.OpID, db.Fresh, db.FreshOID); err != nil {
			_, perr := o.keepPending(rec, phaseApplying, "allocation_unverified", err)
			return errors.Join(err, perr)
		}
		return nil
	}
	// The first allocation record is written before CREATE, so it has no oid.
	// Fence the operation, then restart. A database whose oid was never
	// recorded stays in place. A missing row and a missing fresh name means
	// CREATE did not happen.
	state, ferr := o.fenceSwap(ctx, db)
	st, oerr := o.Catalog.Observe(ctx, db.Configured, db.Fresh, db.Replaced)
	if oerr != nil || st == nil {
		_, perr := o.keepPending(rec, phaseApplying, "allocation_unverified", errors.Join(ferr, oerr, errSwapUnobserved))
		return perr
	}
	if !st[db.Configured] {
		_, err := o.keepPending(rec, phaseApplying, "configured_database_absent", errConfiguredDatabaseAbsent)
		return err
	}
	if ferr != nil {
		if st[db.Fresh] || !errors.Is(ferr, errSwapUnobserved) {
			_, err := o.keepPending(rec, phaseApplying, "allocation_unverified", ferr)
			return err
		}
		return nil
	}
	switch state {
	case "cancelled", "absent", "committed":
		return nil
	default:
		_, err := o.keepPending(rec, phaseApplying, "allocation_unverified", errSwapUnobserved)
		return err
	}
}

// confirmMaintenanceCluster checks only the recorded cluster identifier.
// It does not compare the live configured database oid. Active restore
// recovery still uses confirmCluster for that check.
func (o *Operations) confirmMaintenanceCluster(ctx context.Context, db *databaseIntent) error {
	if db == nil || db.ClusterID == "" || o == nil || o.Catalog == nil {
		return nil
	}
	idn, ok := o.Catalog.(interface {
		Cluster(context.Context) (string, error)
	})
	if !ok {
		return fmt.Errorf("%w: cluster identity", errSwapUnobserved)
	}
	got, err := idn.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", errSwapUnobserved, err)
	}
	if got != db.ClusterID {
		return fmt.Errorf("admin connection is a different database cluster: %w", ErrBadRequest)
	}
	return nil
}

// confirmCluster rejects recovery when the admin connection is a different
// cluster, or the configured name is neither the original database nor the
// restored one. A committed rename puts the fresh oid under the configured
// name, so that oid is a match. An empty cluster id is an older record and is
// left to the catalog row check.
func (o *Operations) confirmCluster(ctx context.Context, db *databaseIntent) error {
	if db == nil || db.ClusterID == "" || o == nil || o.Catalog == nil {
		return nil
	}
	idn, ok := o.Catalog.(interface {
		Cluster(context.Context) (string, error)
	})
	if !ok {
		return fmt.Errorf("%w: cluster identity", errSwapUnobserved)
	}
	got, err := idn.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", errSwapUnobserved, err)
	}
	if got != db.ClusterID {
		return fmt.Errorf("admin connection is a different database cluster: %w", ErrBadRequest)
	}
	if db.ConfiguredOID == "" && db.FreshOID == "" {
		return nil
	}
	look, ok := o.Catalog.(interface {
		DatabaseOID(context.Context, string) (string, error)
	})
	if !ok {
		return fmt.Errorf("%w: database identity", errSwapUnobserved)
	}
	oid, err := look.DatabaseOID(ctx, db.Configured)
	if err != nil || oid == "" {
		return fmt.Errorf("%w: configured database identity", errSwapUnobserved)
	}
	if db.FreshOID != "" && oid == db.FreshOID {
		return nil
	}
	if db.ConfiguredOID != "" && oid == db.ConfiguredOID {
		return nil
	}
	return fmt.Errorf("%w: configured database identity", errSwapUnobserved)
}

// originalDatabase reports that the configured name still has the oid from
// before the rename. A missing oid record is not that proof.
func (o *Operations) originalDatabase(ctx context.Context, db *databaseIntent) bool {
	if db == nil || db.ConfiguredOID == "" || o == nil || o.Catalog == nil {
		return false
	}
	look, ok := o.Catalog.(interface {
		DatabaseOID(context.Context, string) (string, error)
	})
	if !ok {
		return false
	}
	oid, err := look.DatabaseOID(ctx, db.Configured)
	return err == nil && oid != "" && oid == db.ConfiguredOID
}

func (o *Operations) fenceSwap(ctx context.Context, db *databaseIntent) (string, error) {
	fencer, ok := o.Catalog.(interface {
		Fence(context.Context, string, string, string) (string, error)
	})
	if !ok || db == nil {
		return "", errSwapUnobserved
	}
	return fencer.Fence(ctx, db.Configured, db.Fresh, db.Replaced)
}

func decisionFromCatalog(st map[string]bool, db *databaseIntent) int {
	switch {
	case !st[db.Configured]:
		return decisionMissing
	case st[db.Replaced] && !st[db.Fresh]:
		return decisionCleanup
	case st[db.Fresh] && !st[db.Replaced]:
		return decisionOldIntact
	default:
		return decisionAmbiguous
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
	for _, core := range cores {
		if _, reject := writerQuiescence(core); reject {
			return nil, fmt.Errorf("stop %s: state %q is not a quiescent writer", core.ID, core.State)
		}
	}
	var restart []ContainerInfo
	for _, core := range cores {
		prior := strings.ToLower(strings.TrimSpace(core.State))
		if prior == "" {
			prior = "running"
		}
		intent := containerIntent{ID: core.ID, Name: core.Name, Prior: prior}
		stop, _ := writerQuiescence(core)
		if !stop {
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
		case inspectErr != nil || observed == "unknown" || observed == "restarting" || observed == "paused":
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
	if o == nil {
		return nil
	}
	if o.shutting.Load() {
		return ErrShuttingDown
	}
	release, ok := o.Gate.Acquire(ctx)
	if !ok {
		return ErrBusy
	}
	defer release()
	return o.recoverLocked(ctx)
}

// admit retries durable pending work while the operation gate is already
// held, then refuses a new destructive operation when that work is still
// unresolved. A failed recovery that leaves a record pending is the same
// refusal as finding the record before the attempt.
func (o *Operations) admit(ctx context.Context) error {
	err := o.recoverLocked(ctx)
	if blocked := o.blockUnresolved(); blocked != nil {
		return blocked
	}
	return err
}

func (o *Operations) recoverLocked(ctx context.Context) error {
	var errs []error
	if catalog, ok := o.Catalog.(interface {
		Reconcile(context.Context) error
	}); ok {
		if err := catalog.Reconcile(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if o.Store != nil {
		if err := o.Store.RecoverPlaintext(); err != nil {
			return err
		}
	}
	if o.Journal != nil {
		pending, err := o.Journal.Incomplete()
		if err != nil {
			return err
		}
		for _, item := range pending {
			if err := o.recoverOne(ctx, item); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if o.Journal != nil {
		maint, err := o.Journal.Maintenance()
		if err != nil {
			return err
		}
		for _, item := range maint {
			if err := o.recoverMaintenance(item); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := o.recoverUpdates(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// recoverMaintenance retries only the replaced-database drop. The core that
// was current during the restore may already have been replaced, so this
// path does not inspect those container ids. A failed drop leaves cleanup
// pending and does not fail startup or turn the restore back into running.
func (o *Operations) recoverMaintenance(item journalRecord) error {
	_, _ = o.finishMaintenance(item.Record, ErrRestoreCleanup)
	return nil
}

func (o *Operations) finishMaintenance(rec RestoreRecord, err error) (RestoreRecord, error) {
	cleared, _ := o.retryReplacedDrop(rec)
	rec.Status = "completed"
	rec.Recovery = ""
	rec.Finished = o.now().UTC()
	if cleared {
		rec.Error = ""
		rec.Cleanup = ""
	} else {
		rec.Error = "cleanup_pending"
		rec.Cleanup = "pending"
	}
	saved, perr := o.persist(rec, phaseCompleted, nil)
	if perr != nil {
		return rec, perr
	}
	if !cleared {
		if err == nil {
			err = ErrRestoreCleanup
		}
		return saved, err
	}
	return saved, nil
}

func (o *Operations) finishCommittedUncertain(rec RestoreRecord, stopped []ContainerInfo, cause error) (RestoreRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	if err := o.confirmCluster(ctx, o.databaseOf(rec.ID)); err != nil {
		return o.keepPending(rec, phaseApplied, "catalog_unobserved", err)
	}
	st, observed := o.observeSwap(rec)
	if !observed {
		return o.keepPending(rec, phaseApplied, "catalog_unobserved", cause)
	}
	if st.configured && !st.fresh && st.replaced {
		return o.finishCleanup(rec, stopped, ErrRestoreCleanup)
	}
	if st.configured && !st.fresh && !st.replaced {
		return o.finishApplied(rec, stopped, nil)
	}
	reason := "catalog_ambiguous"
	if !st.configured {
		reason = "configured_database_absent"
	}
	return o.keepPending(rec, phaseApplied, reason, cause)
}

func (o *Operations) recoverUpdates(ctx context.Context) error {
	if o.Updater == nil {
		return nil
	}
	if o.Updater.Journal == nil {
		o.Updater.Journal = o.UpdateJournal
	}
	return o.Updater.Recover(ctx)
}

func (o *Operations) recoverOne(ctx context.Context, item journalRecord) error {
	rec := item.Record
	if item.Database != nil && item.Database.SwapCommitted && !item.Database.SwapRolledBack {
		return o.recoverCommitted(item)
	}
	if item.Database != nil && item.Database.SwapRolledBack {
		if err := o.confirmCluster(ctx, item.Database); err != nil {
			_, perr := o.keepPending(rec, phaseRolledBack, "catalog_unobserved", err)
			return errors.Join(err, perr)
		}
		o.dropRolledBackFresh(item)
		saved, err := o.closeStopped(rec, nil, errors.New("restore rolled back before the database rename committed"), "rolled_back")
		if saved.Status == "running" {
			return err
		}
		return nil
	}
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
		if item.Database != nil && item.Database.SwapCommitted {
			st, observed := o.observeSwap(rec)
			if !observed {
				_, perr := o.keepPending(rec, phaseApplied, "catalog_unobserved", errSwapUnobserved)
				return perr
			}
			if st.replaced {
				_, err := o.finishCleanup(rec, nil, ErrRestoreCleanup)
				if err != nil && !errors.Is(err, ErrRestoreCleanup) {
					return err
				}
				return nil
			}
		}
		rec, done, ferr := o.importFiles(rec)
		if done {
			if rec.Status == "running" {
				return ferr
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
	// An allocation that never reached the rename is reconciled before any
	// catalog-uncertainty guard. A lost CREATE acknowledgment is not terminal
	// while the original database is still verified and the operation can be
	// fenced.
	if item.Database != nil && item.Database.Allocated && !item.Database.SwapCommitted && !item.Database.SwapIntended {
		return o.recoverOneTail(rec)
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
			if o.Outbox != nil {
				_ = o.Outbox.Notify(context.Background(), "Restore failed", "The archive was not applied. Existing data was left in place. The loaded database is still present.")
			}
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
	return o.recoverOneTail(rec)
}

func (o *Operations) recoverCommitted(item journalRecord) error {
	rec := item.Record
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	if err := o.confirmCluster(ctx, item.Database); err != nil {
		_, perr := o.keepPending(rec, phaseApplied, "catalog_unobserved", err)
		return errors.Join(err, perr)
	}
	st, observed := o.observeSwap(rec)
	if !observed {
		_, err := o.keepPending(rec, phaseApplied, "catalog_unobserved", errSwapUnobserved)
		return err
	}
	if !st.configured || st.fresh {
		reason := "catalog_ambiguous"
		cause := errSwapUnobserved
		if !st.configured {
			reason = "configured_database_absent"
			cause = errConfiguredDatabaseAbsent
		}
		_, err := o.keepPending(rec, phaseApplied, reason, cause)
		return err
	}
	if st.replaced {
		saved, err := o.finishCleanup(rec, nil, ErrRestoreCleanup)
		if saved.Status == "running" {
			return err
		}
		if err != nil && !errors.Is(err, ErrRestoreCleanup) {
			return err
		}
		return nil
	}
	if err := o.restoreWriters(rec, nil); err != nil {
		_, perr := o.keepPending(rec, phaseApplied, writerRecoveryReason(err), err)
		return errors.Join(err, perr)
	}
	rec, done, ferr := o.importFiles(rec)
	if done {
		if rec.Status == "running" {
			return ferr
		}
		return nil
	}
	rec.Status = "completed"
	rec.Recovery = ""
	rec.Cleanup = ""
	rec.Error = ""
	rec.Finished = o.now().UTC()
	if _, err := o.persist(rec, phaseCompleted, nil); err != nil {
		_, perr := o.keepPending(rec, phaseApplied, "journal_unpersisted", err)
		return errors.Join(err, perr)
	}
	return nil
}

func (o *Operations) freshRemains(rec RestoreRecord) bool {
	db := o.databaseOf(rec.ID)
	if db == nil || db.Fresh == "" || o.Catalog == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	st, err := o.Catalog.Observe(ctx, db.Configured, db.Fresh, db.Replaced)
	return err == nil && st != nil && st[db.Fresh]
}

func (o *Operations) recoverOneTail(rec RestoreRecord) error {
	if db := o.databaseOf(rec.ID); db != nil && db.Allocated && !db.SwapCommitted {
		if err := o.finishAllocation(rec, db); err != nil {
			return err
		}
		if o.freshRemains(rec) {
			rec.Cleanup = "pending"
			saved, err := o.closeStopped(rec, nil, errors.New("unverified restore database was retained"), "allocation_retained")
			if saved.Status == "running" {
				return err
			}
			return nil
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

// dropRolledBackFresh removes the loaded database only after the journal
// already says the rename rolled back, and only when the recorded oid still
// bears that fresh name. A name with no oid, or a different oid, is left in
// place. A drop error does not block writer recovery.
func (o *Operations) dropRolledBackFresh(item journalRecord) {
	db := item.Database
	if db == nil || db.Fresh == "" || db.Fresh == db.Configured || db.OpID == "" || db.FreshOID == "" || o.Catalog == nil {
		return
	}
	dropper, ok := o.Catalog.(interface {
		DropVerified(context.Context, string, string, string) error
	})
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.recoveryTimeout())
	defer cancel()
	_ = dropper.DropVerified(ctx, db.OpID, db.Fresh, db.FreshOID)
}

func (o *Operations) blockUnresolved() error {
	if o == nil {
		return nil
	}
	if o.Journal != nil {
		pending, err := o.Journal.Incomplete()
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			return ErrRecoveryRequired
		}
	}
	journal := o.UpdateJournal
	if journal == nil && o.Updater != nil {
		journal = o.Updater.Journal
	}
	if journal != nil {
		pending, err := journal.Incomplete()
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			return ErrRecoveryRequired
		}
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
	if o.Journal != nil {
		item, ok, err := o.Journal.Latest(rec.ID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("inspect %s: journal record missing", rec.ID)
		}
		if stopWasIntended(item) {
			return o.reconcileWriters(ctx, item)
		}
	}
	return o.restart(ctx, fallback)
}

func (o *Operations) reconcileWriters(ctx context.Context, item journalRecord) error {
	if o.Runtime == nil {
		return errors.New("inspect runtime: unknown")
	}
	var errs []error
	for _, c := range item.Containers {
		if !c.StopIntended || !writerShouldReturn(c.Prior) {
			continue
		}
		info, err := o.Runtime.Inspect(ctx, c.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect %s: %w", c.ID, err))
			continue
		}
		if unobservedState(info) {
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

func unobservedState(info ContainerInfo) bool {
	state := strings.ToLower(strings.TrimSpace(info.State))
	if state == "unknown" {
		return true
	}
	return state == "" && !info.Running
}

func writerRecoveryReason(err error) string {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "inspect") || strings.Contains(msg, "unknown") {
		return "container_unobserved"
	}
	return "restart_failed"
}

func writerQuiescence(info ContainerInfo) (stop, reject bool) {
	switch strings.ToLower(strings.TrimSpace(info.State)) {
	case "", "running", "restarting", "paused":
		return true, false
	case "exited", "dead", "created":
		return false, false
	default:
		return false, true
	}
}

func writerShouldReturn(prior string) bool {
	switch strings.ToLower(strings.TrimSpace(prior)) {
	case "running", "restarting", "paused":
		return true
	default:
		return false
	}
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
	if !stopWasIntended(item) {
		return false
	}
	return o.reconcileWriters(ctx, item) != nil
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
	var prev journalRecord
	var ok bool
	if o.Journal != nil {
		var err error
		prev, ok, err = o.Journal.Latest(rec.ID)
		if err != nil {
			return rec, err
		}
		if ok {
			cur.Containers = prev.Containers
			cur.Database = prev.Database
			cur.Files = prev.Files
			cur.FilesSafety = prev.FilesSafety
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
	// A durable commit outranks a later catalog miss. Do not persist a phase
	// or database flag that forgets it.
	if ok && prev.Database != nil && prev.Database.SwapCommitted && !prev.Database.SwapRolledBack {
		if cur.Database == nil {
			copied := *prev.Database
			cur.Database = &copied
		}
		cur.Database.SwapCommitted = true
		cur.Database.SwapRolledBack = false
		if phaseForgetsCommit(cur.Phase) {
			cur.Phase = phaseApplied
		}
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
	release, ok := o.Gate.Acquire(ctx)
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
	if rec.Source != "" {
		fields["source"] = rec.Source
	}
	if !rec.Started.IsZero() {
		fields["started"] = rec.Started.UTC().Format(time.RFC3339Nano)
	}
	if !rec.Finished.IsZero() {
		fields["finished"] = rec.Finished.UTC().Format(time.RFC3339Nano)
	}
	_, _ = o.Ledger.Append("restore", "supervisor", fields)
}

type keptFiles struct {
	path    string
	cleanup func()
	// safety is the backup this restore took before the swap, for rollBackFiles.
	safety string
}

// ponytail: ceiling: a rollback that also fails lifts the restore hold over the restored
// database without its files; upgrade path and trigger are PONYTAIL-DEBT.md Platform 18.
// rollBackFiles handles a restored database whose file bytes the core could not write.
// Users have been held out of the restored core since it started (FileHold), and stay
// out until this returns, so the rollback drops nothing they did. The record is failed
// with files_not_restored first, so a crash during the rollback leaves it terminal. Then the safety backup taken before the swap is restored as its
// own journaled restore (source "rollback:<id>"), through the same stop, swap and
// restart path, so the previous database is live again over the files it names. When
// that completes the record's error becomes files_rolled_back.
func (o *Operations) rollBackFiles(rec RestoreRecord, safetyID string, ferr error) (RestoreRecord, error) {
	o.jobsMu.Lock()
	o.rollingBack++
	o.jobsMu.Unlock()
	defer func() {
		o.jobsMu.Lock()
		o.rollingBack--
		o.jobsMu.Unlock()
	}()
	rec.Error = "files_not_restored"
	saved, perr := o.markFailed(rec, ferr)
	if perr != nil {
		return o.keepPending(rec, phaseApplied, "journal_unpersisted", errors.Join(ferr, perr))
	}
	notify := func(subject, body string) {
		if o.Outbox != nil {
			_ = o.Outbox.Notify(context.Background(), subject, body+Scrub(ferr.Error()))
		}
	}
	if safetyID == "" {
		notify("Restore needs attention", "The database was restored and the core is running. The file bytes were not restored, and there is no safety backup to roll back to. ")
		return saved, ferr
	}
	opID, err := newRestoreOpID(o.now())
	if err != nil {
		notify("Restore needs attention", "The database was restored and the core is running. The file bytes were not restored, and the rollback did not start. ")
		return saved, errors.Join(ferr, err)
	}
	rb := RestoreRecord{ID: opID, BackupID: safetyID, Source: "rollback:" + rec.ID, Status: "accepted", Started: o.now().UTC()}
	if _, err := o.persist(rb, phaseAccepted, nil); err != nil {
		notify("Restore needs attention", "The database was restored and the core is running. The file bytes were not restored, and the rollback did not start. ")
		return saved, errors.Join(ferr, err)
	}
	done, rerr := o.executeRestore(rb)
	if rerr != nil || done.Status != "completed" {
		notify("Restore needs attention", "The database was restored and the core is running. The file bytes were not restored, and putting the previous database back failed (restore "+opID+"). ")
		return saved, errors.Join(ferr, rerr)
	}
	saved.Error = "files_rolled_back"
	if again, err := o.persist(saved, phaseFailed, nil); err == nil {
		saved = again
	}
	notify("Restore rolled back", "The file bytes could not be restored, so the database was put back from the safety backup taken just before the restore. Existing data is in place; changes made after the restored core started are not kept. ")
	return saved, ferr
}

// keepFiles moves a restore's file export out of the decrypted workspace,
// which is removed when apply returns, into a scratch workspace of its own.
// Startup recovery removes it if this process dies before the core takes it.
func (o *Operations) keepFiles(id, path, safety string) error {
	if o.RestoreFiles == nil {
		return ErrFileRestoreUnsupported
	}
	dir, cleanup, err := o.Store.plaintextWorkspace("restore-files-")
	if err != nil {
		return err
	}
	kept := filepath.Join(dir, "bytes.tar")
	if err := os.Rename(path, kept); err != nil {
		cleanup()
		return err
	}
	o.jobsMu.Lock()
	if o.files == nil {
		o.files = map[string]keptFiles{}
	}
	o.files[id] = keptFiles{path: kept, cleanup: cleanup, safety: safety}
	o.jobsMu.Unlock()
	return nil
}

// FileHold reports whether users must stay out of the core: a restore has put
// its database in place and its file bytes are not written yet, or a failed
// file write is being rolled back. The core serves only its health check and
// the supervisor's file import until this is false, so nothing a user does in
// that window can be lost to a rollback. An unreadable journal holds.
func (o *Operations) FileHold() (bool, error) {
	o.jobsMu.Lock()
	busy := len(o.files) > 0 || o.rollingBack > 0
	o.jobsMu.Unlock()
	if busy || o.Journal == nil {
		return busy, nil
	}
	open, err := o.Journal.Incomplete()
	if err != nil {
		return true, err
	}
	for _, rec := range open {
		if rec.Files == filesPending {
			return true, nil
		}
	}
	return false, nil
}

func (o *Operations) takeFiles(id string) (keptFiles, bool) {
	o.jobsMu.Lock()
	defer o.jobsMu.Unlock()
	k, ok := o.files[id]
	delete(o.files, id)
	return k, ok && o.RestoreFiles != nil
}
