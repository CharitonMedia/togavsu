package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const updateCleanupTimeout = 30 * time.Second

// Release is one signed core image advertised by the release index.
type Release struct {
	Image   string
	Version string
}

// Registry returns the newest core release. An empty index URL is not a
// permission to install an unsigned image.
type Registry interface {
	Latest(ctx context.Context) (Release, error)
}

// SignatureVerifier checks the Togavsu release key. A missing key rejects
// the image.
type SignatureVerifier interface {
	Verify(ctx context.Context, image string) error
}

// Migrator applies the new image's migrations. ran is true when a migration
// was applied, including a migration that later failed.
type Migrator interface {
	Apply(ctx context.Context, image string) (ran bool, err error)
}

// ContainerInfo is one container the supervisor started or found.
type ContainerInfo struct {
	ID      string
	Name    string
	Image   string
	Labels  map[string]string
	Aliases []string
	State   string
	Running bool
}

// Runtime is the Docker socket, held only by the supervisor.
type Runtime interface {
	ListCores(ctx context.Context) ([]ContainerInfo, error)
	ListAllCores(ctx context.Context) ([]ContainerInfo, error)
	Inspect(ctx context.Context, id string) (ContainerInfo, error)
	Pull(ctx context.Context, image string) error
	StartCore(ctx context.Context, image, name string) (ContainerInfo, error)
	Start(ctx context.Context, id string) error
	WaitHealthy(ctx context.Context, id string, timeout time.Duration) error
	Stop(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	StartModule(ctx context.Context, name string) error
	StopModule(ctx context.Context, name string) error
}

// ProxySwitcher points Caddy at the new core.
type ProxySwitcher interface {
	Switch(ctx context.Context, upstream string) error
}

// UpdateResult is the outcome of one update attempt.
type UpdateResult struct {
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	From         string `json:"from,omitempty"`
	To           string `json:"to,omitempty"`
	BackupID     string `json:"backup_id,omitempty"`
	MigrationRan bool   `json:"migration_ran"`
	Restored     bool   `json:"restored"`
	Rollback     string `json:"rollback,omitempty"`
	StopError    string `json:"stop_error,omitempty"`
}

// Updater runs the nightly swap. It refuses a bad signature or health check
// and leaves the previous core running.
type Updater struct {
	Now      func() time.Time
	Window   Window
	Paused   bool
	Registry Registry
	Verifier SignatureVerifier
	Runtime  Runtime
	Migrator Migrator
	Backup   func(ctx context.Context, reason string) (BackupMeta, error)
	// Rollback runs when a migration was applied and the update is aborted.
	// It must not replay the pre-update dump over the live database.
	// ErrPreserveWrites means the backup was kept and later commits stayed.
	Rollback func(ctx context.Context, backupID string) error
	// Restore is the destructive archive replay. Update rollback must not call
	// it while the previous core is still serving.
	Restore       func(ctx context.Context, id string) error
	Proxy         ProxySwitcher
	Notify        func(ctx context.Context, subject, body string)
	Ledger        *Ledger
	HealthTimeout time.Duration
	Journal       *UpdateJournal
	UpstreamFile  string
}

type migrationStatus struct {
	Known          bool
	Ran            bool
	Name           string
	ID             string
	CleanupPending bool
}

type migrationRunner interface {
	RunMigration(ctx context.Context, image, name string, before func(phase, id string) error) (migrationStatus, error)
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

// Apply checks the window, signature, backup, migration, health, and proxy
// switch, in that order.
func (u *Updater) Apply(ctx context.Context, force bool) (UpdateResult, error) {
	now := u.now()
	if !force && u.Paused {
		return UpdateResult{Status: "refused", Reason: "paused"}, ErrUpdatesPaused
	}
	if !force && !u.Window.Contains(now) {
		return UpdateResult{Status: "refused", Reason: "outside_window"}, ErrOutsideWindow
	}
	if u.Registry == nil || u.Verifier == nil || u.Runtime == nil || u.Proxy == nil {
		return UpdateResult{Status: "refused", Reason: "not_configured"}, ErrReleaseIndexUnconfigured
	}
	if err := u.Recover(ctx); err != nil {
		return UpdateResult{Status: "refused", Reason: "cleanup_unreconciled", Rollback: "cleanup_unreconciled"}, err
	}
	current, err := u.servingCore(ctx)
	if err != nil {
		return UpdateResult{Status: "refused", Reason: "proxy_unknown"}, err
	}
	release, err := u.Registry.Latest(ctx)
	if err != nil {
		return UpdateResult{Status: "refused", From: current.Image}, err
	}
	curVer := versionOf(current)
	if release.Version != "" && curVer != "" {
		if !newer(release.Version, curVer) {
			return UpdateResult{Status: "up_to_date", From: curVer, To: release.Version}, nil
		}
	} else if release.Image == "" || release.Image == current.Image {
		return UpdateResult{Status: "up_to_date", From: current.Image, To: release.Image}, nil
	}
	if err := ImmutableImage(release.Image); err != nil {
		u.notify(ctx, "Update refused", "The release index did not name an immutable image digest. The running version was left in place.")
		return UpdateResult{Status: "refused", Reason: "digest", From: current.Image, To: release.Image}, err
	}
	if err := u.Verifier.Verify(ctx, release.Image); err != nil {
		u.record("update", map[string]string{"status": "refused", "reason": "signature", "image": release.Image})
		u.notify(ctx, "Update refused", "The new core image failed signature verification. The running version was left in place.")
		return UpdateResult{Status: "refused", Reason: "signature", From: current.Image, To: release.Image}, ErrSignature
	}
	if err := u.Runtime.Pull(ctx, release.Image); err != nil {
		u.notify(ctx, "Update refused", "The verified image digest could not be pulled. The running version was left in place.")
		return UpdateResult{Status: "refused", Reason: "pull", From: current.Image, To: release.Image}, err
	}
	var backupID string
	if u.Backup != nil {
		meta, err := u.Backup(ctx, reasonPreUpdate)
		if err != nil {
			u.notify(ctx, "Update refused", "The pre-update backup failed. The running version was left in place.")
			return UpdateResult{Status: "refused", Reason: "backup", From: current.Image, To: release.Image}, err
		}
		backupID = meta.ID
	}
	ran := false
	if u.Migrator == nil {
		u.abort(ctx, backupID, ran, nil)
		return UpdateResult{Status: "refused", Reason: "migrator", From: current.Image, To: release.Image, BackupID: backupID}, ErrMigratorUnconfigured
	}
	opID := backupID
	if opID == "" {
		opID, err = newBackupID(u.now())
		if err != nil {
			return UpdateResult{Status: "refused", Reason: "migration", From: current.Image, To: release.Image}, err
		}
	}
	st, err := u.runMigration(ctx, opID, release.Image, current)
	ran = st.Known && st.Ran
	if st.CleanupPending || errors.Is(err, ErrMigrationUnknown) {
		_ = u.note(opID, func(rec *updateIntent) {
			rec.Status = "running"
			rec.Phase = "migration_unknown"
			rec.Cleanup = "migration"
			rec.MigrationName = st.Name
			rec.MigrationID = st.ID
			rec.MigrationRan = "unknown"
			rec.BackupID = backupID
			rec.CurrentID = current.ID
			rec.CurrentName = current.Name
		})
		return UpdateResult{Status: "refused", Reason: "migration", From: current.Image, To: release.Image, BackupID: backupID, Rollback: "cleanup_unreconciled"}, err
	}
	if err != nil {
		rolled := u.abort(ctx, backupID, ran, nil)
		if cerr := u.confirmMigrationGone(opID, st); cerr != nil {
			_ = u.keepPending(opID, "migration", "migration_intended")
		} else if rolled.Mode == "cleanup_unreconciled" {
			_ = u.keepPending(opID, "migration", "migration_cleanup")
		} else {
			_ = u.finalize(opID, "failed", "failed")
		}
		u.notify(ctx, "Update refused", "Migrations failed. The running version was left in place.")
		return mergeRollback(UpdateResult{Status: "refused", Reason: "migration", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran}, rolled, err)
	}
	nextName, err := nextCoreName(u.now())
	if err != nil {
		rolled := u.abort(ctx, backupID, ran, nil)
		_ = u.finalize(opID, "failed", "failed")
		return mergeRollback(UpdateResult{Status: "refused", Reason: "start", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran}, rolled, err)
	}
	next, err := u.startCandidate(ctx, opID, release.Image, nextName, current, backupID)
	if err != nil {
		rolled := u.abort(ctx, backupID, ran, nil)
		if cerr := u.confirmCandidateGone(opID, next); cerr != nil {
			_ = u.keepPending(opID, "replacement", "candidate_cleanup")
			if next.Name != "" || next.ID != "" {
				_ = u.note(opID, func(rec *updateIntent) {
					if next.ID != "" {
						rec.NextID = next.ID
					}
					if next.Name != "" {
						rec.NextName = next.Name
					}
				})
			}
		} else {
			_ = u.finalize(opID, "failed", "failed")
		}
		u.notify(ctx, "Update refused", "The new core container did not start. The running version was left in place.")
		return mergeRollback(UpdateResult{Status: "refused", Reason: "start", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran}, rolled, err)
	}
	timeout := u.HealthTimeout
	if timeout == 0 {
		timeout = time.Minute
	}
	if err := u.Runtime.WaitHealthy(ctx, next.ID, timeout); err != nil {
		rolled := u.abort(ctx, backupID, ran, &next)
		if rolled.Mode == "cleanup_unreconciled" {
			_ = u.keepPending(opID, "replacement", "candidate_started")
		} else {
			_ = u.finalize(opID, "failed", "failed")
		}
		u.notify(ctx, "Update refused", "The new core failed its health check. The running version was left in place.")
		return mergeRollback(UpdateResult{Status: "refused", Reason: "health", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran, Rollback: rolled.Mode}, rolled, ErrHealth)
	}
	upstream := next.Name + ":8080"
	if err := u.note(opID, func(rec *updateIntent) {
		rec.Status = "running"
		rec.Phase = "switch_intended"
		rec.SwitchTo = upstream
		rec.NextID = next.ID
		rec.NextName = next.Name
		rec.CurrentID = current.ID
		rec.CurrentName = current.Name
		rec.BackupID = backupID
	}); err != nil {
		return UpdateResult{Status: "refused", Reason: "proxy", From: current.Image, To: release.Image, BackupID: backupID}, err
	}
	switchErr := u.Proxy.Switch(ctx, upstream)
	if errors.Is(switchErr, ErrProxyUnreconciled) {
		_ = u.note(opID, func(rec *updateIntent) {
			rec.Status = "running"
			rec.Phase = "proxy_unreconciled"
			rec.Cleanup = "proxy_unreconciled"
			rec.SwitchTo = upstream
			rec.NextID = next.ID
			rec.NextName = next.Name
		})
		u.notify(ctx, "Update needs attention", "Caddy may still route to the new core, so that container was left running.")
		return UpdateResult{Status: "refused", Reason: "proxy", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran, Rollback: "proxy_unreconciled"}, switchErr
	}
	routed, routeErr := u.finishSwitch(ctx, opID, current, next, upstream, switchErr)
	if routeErr != nil {
		return UpdateResult{Status: "refused", Reason: "proxy", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran, Rollback: routed}, routeErr
	}
	result := UpdateResult{Status: "applied", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran}
	_ = u.note(opID, func(rec *updateIntent) {
		rec.Status = "completed"
		rec.Phase = "completed"
		rec.Cleanup = ""
		rec.Finished = u.now().UTC()
	})
	u.record("update", map[string]string{
		"status":   "applied",
		"from":     current.Image,
		"to":       release.Image,
		"backup":   backupID,
		"migrated": strconv.FormatBool(ran),
	})
	return result, nil
}

type rollbackOutcome struct {
	Restored bool
	Mode     string
	Err      error
}

func (u *Updater) abort(ctx context.Context, backupID string, migrated bool, started *ContainerInfo) rollbackOutcome {
	// The HTTP request may already be canceled. Cleanup and the rollback
	// notice use a fresh bounded context so Stop and Remove still run.
	cleanCtx, cancel := context.WithTimeout(context.Background(), updateCleanupTimeout)
	defer cancel()
	out := rollbackOutcome{}
	if started != nil {
		if err := confirmAbsent(started.ID, u.Runtime); err != nil {
			out.Mode = "cleanup_unreconciled"
			out.Err = fmt.Errorf("%w: %v", ErrCleanupUnreconciled, err)
			u.notify(cleanCtx, "Update needs attention", "The aborted core container was not removed. It is not the serving version, and another update is refused until cleanup finishes.")
		}
	}
	if migrated && backupID != "" {
		rolled := u.rollback(cleanCtx, backupID)
		if out.Mode == "cleanup_unreconciled" {
			if rolled.Err != nil {
				out.Err = errors.Join(out.Err, rolled.Err)
			}
		} else {
			out = rolled
		}
	}
	u.record("update", map[string]string{
		"status":   "refused",
		"backup":   backupID,
		"restored": strconv.FormatBool(out.Restored),
		"rollback": out.Mode,
	})
	return out
}

// rollback refuses to replay the pre-update dump while the old core is still
// the writer. That replay would drop tables under live requests and discard
// commits made after the snapshot. ErrPreserveWrites is the normal result.
//
// ponytail: automatic migration rollback is this preserve-writes result,
// ceiling: a failed migration's schema changes stay, upgrade: a quiesced
// rollback that reverses only the migration, trigger: that rollback preserves
// rows committed after the pre-update snapshot.
func (u *Updater) rollback(ctx context.Context, backupID string) rollbackOutcome {
	fn := u.Rollback
	if fn == nil {
		fn = func(context.Context, string) error { return ErrPreserveWrites }
	}
	err := fn(ctx, backupID)
	if errors.Is(err, ErrPreserveWrites) {
		u.notify(ctx, "Update rollback kept later writes", "Backup "+backupID+" was not replayed. Commits made after that snapshot are still in the database. Restore it only through the quiesced restore path.")
		return rollbackOutcome{Mode: "preserved_writes"}
	}
	if err != nil {
		u.notify(ctx, "Update rollback failed", "The pre-update backup was not applied. "+Scrub(err.Error()))
		return rollbackOutcome{Mode: "failed", Err: err}
	}
	u.notify(ctx, "Update rollback applied a coordinated restore", "Backup "+backupID+" was applied by the rollback coordinator.")
	return rollbackOutcome{Restored: true, Mode: "coordinated"}
}

func mergeRollback(result UpdateResult, rolled rollbackOutcome, primary error) (UpdateResult, error) {
	result.Restored = rolled.Restored
	result.Rollback = rolled.Mode
	if rolled.Err != nil {
		if primary != nil {
			primary = fmt.Errorf("%w; rollback failed: %v", primary, rolled.Err)
		} else {
			primary = rolled.Err
		}
	}
	return result, primary
}

func (u *Updater) notify(ctx context.Context, subject, body string) {
	if u.Notify != nil {
		u.Notify(ctx, subject, body)
	}
}

func (u *Updater) record(kind string, fields map[string]string) {
	if u.Ledger != nil {
		_, _ = u.Ledger.Append(kind, "supervisor", fields)
	}
}

func nextMigrateName(now time.Time) (string, error) {
	id, err := newBackupID(now)
	if err != nil {
		return "", err
	}
	return "togavsu-migrate-" + id[len(id)-12:], nil
}

func nextCoreName(now time.Time) (string, error) {
	id, err := newBackupID(now)
	if err != nil {
		return "", err
	}
	return "togavsu-core-" + id[len(id)-12:], nil
}

// ImmutableImage requires name@sha256:<64 hex> so a mutable tag cannot select
// a different image from the local cache after verification.
func ImmutableImage(ref string) error {
	at := strings.LastIndex(ref, "@")
	if at <= 0 || !strings.HasPrefix(ref[at+1:], "sha256:") {
		return fmt.Errorf("%w: release image must be name@sha256:<64 hex chars>", ErrBadRequest)
	}
	sum := ref[at+len("@sha256:"):]
	if len(sum) != 64 {
		return fmt.Errorf("%w: release image digest length", ErrBadRequest)
	}
	for _, c := range sum {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("%w: release image digest", ErrBadRequest)
		}
	}
	if strings.ContainsAny(ref[:at], " \t\r\n") {
		return fmt.Errorf("%w: release image name", ErrBadRequest)
	}
	return nil
}

type liveProxy interface {
	ActiveUpstream(context.Context) (string, error)
}

func (u *Updater) servingCore(ctx context.Context) (ContainerInfo, error) {
	proxy, ok := u.Proxy.(liveProxy)
	if !ok {
		return ContainerInfo{}, ErrProxyUnknown
	}
	dial, err := proxy.ActiveUpstream(ctx)
	if err != nil || dial == "" {
		if err == nil {
			err = ErrProxyUnknown
		}
		return ContainerInfo{}, err
	}
	host, port, err := splitUpstream(dial)
	if err != nil || port == "" {
		return ContainerInfo{}, ErrProxyUnknown
	}
	hits, err := u.containersForHost(ctx, host)
	if err != nil {
		return ContainerInfo{}, err
	}
	if len(hits) != 1 {
		return ContainerInfo{}, ErrProxyUnknown
	}
	return hits[0], nil
}

func (u *Updater) containersForHost(ctx context.Context, host string) ([]ContainerInfo, error) {
	cores, err := u.Runtime.ListCores(ctx)
	if err != nil {
		return nil, err
	}
	var hits []ContainerInfo
	for _, core := range cores {
		if containerMatchesHost(core, host) {
			hits = append(hits, core)
		}
	}
	return hits, nil
}

func containerMatchesHost(core ContainerInfo, host string) bool {
	if host == "" {
		return false
	}
	if core.Name == host || core.ID == host {
		return true
	}
	for _, alias := range core.Aliases {
		if alias == host {
			return true
		}
	}
	return false
}

func sameCore(a, b ContainerInfo) bool {
	if a.ID != "" && b.ID != "" {
		return a.ID == b.ID
	}
	return a.Name != "" && a.Name == b.Name
}

func splitUpstream(dial string) (string, string, error) {
	return net.SplitHostPort(dial)
}

func (u *Updater) note(id string, mutate func(*updateIntent)) error {
	if u.Journal == nil || id == "" {
		return nil
	}
	rec, ok, err := u.Journal.Latest(id)
	if err != nil {
		return err
	}
	if !ok {
		rec.ID = id
		rec.Status = "running"
		rec.Started = u.now().UTC()
	}
	if mutate != nil {
		mutate(&rec)
	}
	rec.ID = id
	return u.Journal.Put(rec)
}

func (u *Updater) runMigration(ctx context.Context, opID, image string, current ContainerInfo) (migrationStatus, error) {
	name, err := nextMigrateName(u.now())
	if err != nil {
		return migrationStatus{}, err
	}
	if err := u.note(opID, func(rec *updateIntent) {
		rec.Status = "running"
		rec.Phase = "migration_intended"
		rec.MigrationName = name
		rec.CurrentID = current.ID
		rec.CurrentName = current.Name
	}); err != nil {
		return migrationStatus{}, err
	}
	if runner, ok := u.Migrator.(migrationRunner); ok {
		return runner.RunMigration(ctx, image, name, func(phase, id string) error {
			return u.note(opID, func(rec *updateIntent) {
				rec.Phase = phase
				rec.MigrationName = name
				if id != "" {
					rec.MigrationID = id
				}
			})
		})
	}
	ran, err := u.Migrator.Apply(ctx, image)
	if errors.Is(err, ErrMigrationUnknown) {
		return migrationStatus{Name: name, CleanupPending: true}, err
	}
	return migrationStatus{Known: true, Ran: ran, Name: name}, err
}

func (u *Updater) startCandidate(ctx context.Context, opID, image, name string, current ContainerInfo, backupID string) (ContainerInfo, error) {
	if err := u.note(opID, func(rec *updateIntent) {
		rec.Status = "running"
		rec.Phase = "candidate_intended"
		rec.NextName = name
		rec.CurrentID = current.ID
		rec.CurrentName = current.Name
		rec.BackupID = backupID
	}); err != nil {
		return ContainerInfo{}, err
	}
	factory, ok := u.Runtime.(interface {
		CreateCore(context.Context, string, string) (ContainerInfo, error)
	})
	var info ContainerInfo
	var err error
	if ok {
		info, err = factory.CreateCore(ctx, image, name)
		if err != nil {
			found, ferr := u.inspectFresh(name)
			if ferr == nil && found.ID != "" {
				info = found
				err = nil
			} else if errors.Is(ferr, ErrNotFound) {
				return ContainerInfo{}, err
			} else {
				_ = u.keepPending(opID, "replacement", "candidate_intended")
				return ContainerInfo{Name: name}, err
			}
		}
	} else {
		info, err = u.Runtime.StartCore(ctx, image, name)
		if err != nil {
			if info.Name == "" {
				info.Name = name
			}
			return info, err
		}
	}
	if info.Name == "" {
		info.Name = name
	}
	if err := u.note(opID, func(rec *updateIntent) {
		rec.Phase = "candidate_created"
		rec.NextID = info.ID
		rec.NextName = info.Name
	}); err != nil {
		return info, err
	}
	if ok {
		if err := u.note(opID, func(rec *updateIntent) { rec.Phase = "candidate_starting" }); err != nil {
			return info, err
		}
		if serr := u.Runtime.Start(ctx, info.ID); serr != nil {
			found, ferr := u.Runtime.Inspect(ctx, info.ID)
			if ferr != nil || !found.Running {
				if cerr := confirmAbsent(info.ID, u.Runtime); cerr != nil {
					_ = u.note(opID, func(rec *updateIntent) {
						rec.Status = "running"
						rec.Cleanup = "replacement"
						rec.NextID = info.ID
						rec.NextName = info.Name
					})
				}
				return info, serr
			}
		}
	}
	return info, nil
}

func (u *Updater) finishSwitch(ctx context.Context, opID string, current, next ContainerInfo, upstream string, switchErr error) (string, error) {
	if errors.Is(switchErr, ErrProxyUnreconciled) {
		_ = u.note(opID, func(rec *updateIntent) {
			rec.Status = "running"
			rec.Cleanup = "proxy_unreconciled"
			rec.Phase = "proxy_unknown"
		})
		return "proxy_unreconciled", switchErr
	}
	if switchErr != nil && !errors.Is(switchErr, ErrProxyUnreconciled) {
		live, lerr := u.readLive(ctx)
		if lerr != nil || live == "" {
			_ = u.note(opID, func(rec *updateIntent) {
				rec.Status = "running"
				rec.Cleanup = "proxy_unreconciled"
				rec.Phase = "proxy_unknown"
			})
			return "proxy_unknown", ErrProxyUnknown
		}
		return u.route(ctx, opID, current, next, live, switchErr)
	}
	if switchErr == nil {
		live, lerr := u.readLive(ctx)
		if lerr != nil {
			_ = u.note(opID, func(rec *updateIntent) {
				rec.Status = "running"
				rec.Cleanup = "proxy_unreconciled"
				rec.Phase = "proxy_unknown"
			})
			return "proxy_unknown", ErrProxyUnknown
		}
		return u.route(ctx, opID, current, next, live, nil)
	}
	return "", nil
}

func (u *Updater) route(ctx context.Context, opID string, current, next ContainerInfo, live string, cause error) (string, error) {
	host, port, err := splitUpstream(live)
	hits, herr := u.containersForHost(ctx, host)
	if err != nil || herr != nil || port == "" || len(hits) != 1 {
		_ = u.note(opID, func(rec *updateIntent) {
			rec.Status = "running"
			rec.Cleanup = "proxy_unreconciled"
			rec.Phase = "proxy_unknown"
		})
		if err == nil {
			err = herr
		}
		if err == nil {
			err = ErrProxyUnknown
		}
		return "proxy_unknown", err
	}
	hit := hits[0]
	switch {
	case sameCore(hit, next):
		if err := u.persistObservedRoute(live); err != nil {
			_ = u.keepPending(opID, "route_file", "route_unpersisted")
			return "route_unpersisted", err
		}
		if err := confirmAbsent(current.ID, u.Runtime); err != nil {
			_ = u.note(opID, func(rec *updateIntent) {
				rec.Status = "running"
				rec.Cleanup = "previous"
				rec.Phase = "candidate_serving"
				rec.CurrentID = current.ID
			})
			return "cleanup_unreconciled", err
		}
		_ = u.note(opID, func(rec *updateIntent) {
			rec.Status = "completed"
			rec.Phase = "completed"
			rec.Cleanup = ""
			rec.Finished = u.now().UTC()
		})
		return "", nil
	case sameCore(hit, current):
		if err := u.persistObservedRoute(live); err != nil {
			_ = u.keepPending(opID, "route_file", "route_unpersisted")
			return "route_unpersisted", err
		}
		if err := confirmAbsent(next.ID, u.Runtime); err != nil {
			_ = u.note(opID, func(rec *updateIntent) {
				rec.Status = "running"
				rec.Cleanup = "replacement"
				rec.NextID = next.ID
				rec.NextName = next.Name
			})
			return "cleanup_unreconciled", err
		}
		_ = u.note(opID, func(rec *updateIntent) {
			rec.Status = "failed"
			rec.Phase = "failed"
			rec.Cleanup = ""
			rec.Finished = u.now().UTC()
		})
		if cause == nil {
			cause = errors.New("caddy still serves the previous core")
		}
		return "previous_serving", cause
	default:
		_ = u.note(opID, func(rec *updateIntent) {
			rec.Status = "running"
			rec.Cleanup = "proxy_unreconciled"
			rec.Phase = "proxy_unknown"
		})
		return "proxy_unknown", ErrProxyUnknown
	}
}

func (u *Updater) persistObservedRoute(live string) error {
	if u == nil || u.UpstreamFile == "" {
		return nil
	}
	return writeFileAtomic(u.UpstreamFile, []byte(strings.TrimSpace(live)+"\n"), 0o644)
}

// confirmCandidateGone removes a container that may exist because its
// unique name was stored before create. A nil create reply is not proof
// the container is absent. An inspect error leaves the attempt pending.
func (u *Updater) confirmMigrationGone(opID string, st migrationStatus) error {
	id, name := st.ID, st.Name
	if u != nil && u.Journal != nil && opID != "" {
		rec, ok, err := u.Journal.Latest(opID)
		if err != nil {
			return err
		}
		if ok {
			if id == "" {
				id = rec.MigrationID
			}
			if name == "" {
				name = rec.MigrationName
			}
			if id == "" && name == "" && strings.HasPrefix(rec.Phase, "migration") {
				return errors.New("migration name was not observed")
			}
		}
	}
	target := id
	if target == "" {
		target = name
	}
	if target == "" {
		return nil
	}
	if u == nil || u.Runtime == nil {
		return errors.New("migration was not observed")
	}
	return confirmAbsent(target, u.Runtime)
}

func (u *Updater) confirmCandidateGone(opID string, info ContainerInfo) error {
	id, name := info.ID, info.Name
	if u != nil && u.Journal != nil && opID != "" {
		rec, ok, err := u.Journal.Latest(opID)
		if err != nil {
			return err
		}
		if ok {
			if id == "" {
				id = rec.NextID
			}
			if name == "" {
				name = rec.NextName
			}
			if id == "" && name == "" && strings.HasPrefix(rec.Phase, "candidate") {
				return errors.New("candidate name was not observed")
			}
		}
	}
	target := id
	if target == "" {
		target = name
	}
	if target == "" || u == nil || u.Runtime == nil {
		if target == "" {
			return nil
		}
		return errors.New("candidate was not observed")
	}
	return confirmAbsent(target, u.Runtime)
}

func (u *Updater) inspectFresh(id string) (ContainerInfo, error) {
	if u == nil || u.Runtime == nil || id == "" {
		return ContainerInfo{}, errors.New("container was not observed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateCleanupTimeout)
	defer cancel()
	return u.Runtime.Inspect(ctx, id)
}

func (u *Updater) pending(opID string) bool {
	if u == nil || u.Journal == nil || opID == "" {
		return false
	}
	rec, ok, err := u.Journal.Latest(opID)
	return err == nil && ok && rec.Cleanup != "" && !rec.terminal()
}

func (u *Updater) finalize(opID, status, phase string) error {
	return u.note(opID, func(rec *updateIntent) {
		rec.Status = status
		rec.Phase = phase
		rec.Cleanup = ""
		rec.Finished = u.now().UTC()
	})
}

func (u *Updater) keepPending(opID, cleanup, phase string) error {
	return u.note(opID, func(rec *updateIntent) {
		rec.Status = "running"
		rec.Phase = phase
		rec.Cleanup = cleanup
		rec.Finished = time.Time{}
	})
}

func (u *Updater) readLive(ctx context.Context) (string, error) {
	proxy, ok := u.Proxy.(liveProxy)
	if !ok {
		return "", ErrProxyUnknown
	}
	return proxy.ActiveUpstream(ctx)
}

func (u *Updater) Recover(ctx context.Context) error {
	if u == nil || u.Journal == nil {
		return nil
	}
	pending, err := u.Journal.Incomplete()
	if err != nil {
		return err
	}
	var errs []error
	for _, item := range pending {
		if err := u.reconcileUpdate(ctx, item); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (u *Updater) reconcileUpdate(ctx context.Context, item updateIntent) error {
	if item.Cleanup == "migration" || item.Phase == "migration_unknown" || item.Phase == "migration_starting" || item.Phase == "migration_created" || item.Phase == "migration_intended" {
		id := item.MigrationID
		if id == "" {
			id = item.MigrationName
		}
		if id == "" {
			item.Status = "running"
			item.Cleanup = "migration"
			item.Phase = "migration_intended"
			_ = u.Journal.Put(item)
			return errors.New("migration name was not observed")
		}
		if err := confirmAbsent(id, u.Runtime); err != nil {
			item.Status = "running"
			item.Cleanup = "migration"
			item.Phase = "migration_intended"
			_ = u.Journal.Put(item)
			return err
		}
		item.Cleanup = ""
		if item.NextID == "" && item.NextName == "" {
			item.Status = "failed"
			item.Phase = "failed"
			item.MigrationRan = "unknown"
			item.Finished = u.now().UTC()
			return u.Journal.Put(item)
		}
	}
	live, lerr := u.readLive(ctx)
	if item.NextID != "" || item.NextName != "" {
		if lerr != nil || live == "" {
			return ErrProxyUnknown
		}
		host, port, herr := splitUpstream(live)
		hits, merr := u.containersForHost(ctx, host)
		if herr != nil || merr != nil || port == "" || len(hits) != 1 {
			item.Cleanup = "proxy_unreconciled"
			item.Status = "running"
			_ = u.Journal.Put(item)
			return ErrProxyUnknown
		}
		hit := hits[0]
		switch {
		case hit.ID == item.NextID || (item.NextName != "" && hit.Name == item.NextName):
			if err := u.persistObservedRoute(live); err != nil {
				item.Status = "running"
				item.Cleanup = "route_file"
				item.Phase = "route_unpersisted"
				_ = u.Journal.Put(item)
				return err
			}
			if item.CurrentID != "" {
				if err := confirmAbsent(item.CurrentID, u.Runtime); err != nil {
					item.Cleanup = "previous"
					_ = u.Journal.Put(item)
					return err
				}
			}
			item.Status = "completed"
			item.Phase = "completed"
			item.Cleanup = ""
			item.Finished = u.now().UTC()
			return u.Journal.Put(item)
		case hit.ID == item.CurrentID || (item.CurrentName != "" && (hit.Name == item.CurrentName || containerMatchesHost(hit, item.CurrentName))):
			if err := u.persistObservedRoute(live); err != nil {
				item.Status = "running"
				item.Cleanup = "route_file"
				item.Phase = "route_unpersisted"
				_ = u.Journal.Put(item)
				return err
			}
			id := item.NextID
			if id == "" {
				id = item.NextName
			}
			if err := confirmAbsent(id, u.Runtime); err != nil {
				item.Cleanup = "replacement"
				_ = u.Journal.Put(item)
				return err
			}
			item.Status = "failed"
			item.Phase = "failed"
			item.Cleanup = ""
			item.Finished = u.now().UTC()
			return u.Journal.Put(item)
		default:
			item.Cleanup = "proxy_unreconciled"
			item.Status = "running"
			_ = u.Journal.Put(item)
			return ErrProxyUnknown
		}
	}
	if item.Cleanup == "proxy_unreconciled" {
		return ErrProxyUnknown
	}
	item.Status = "failed"
	item.Cleanup = ""
	item.Finished = u.now().UTC()
	return u.Journal.Put(item)
}

func confirmAbsent(id string, rt Runtime) error {
	if id == "" || rt == nil {
		return errors.New("container was not observed")
	}
	clean, cancel := context.WithTimeout(context.Background(), updateCleanupTimeout)
	defer cancel()
	_ = rt.Stop(clean, id)
	_ = rt.Remove(clean, id)
	_, err := rt.Inspect(clean, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("container %s still present", id)
}

func versionOf(c ContainerInfo) string {
	if c.Labels != nil && c.Labels["togavsu.version"] != "" {
		return c.Labels["togavsu.version"]
	}
	return ""
}

func newer(latest, current string) bool {
	if latest == "" {
		return false
	}
	if current == "" {
		return true
	}
	lp, okL := parseSemver(latest)
	cp, okC := parseSemver(current)
	if !okL || !okC {
		return latest != current
	}
	for i := 0; i < 3; i++ {
		if lp[i] != cp[i] {
			return lp[i] > cp[i]
		}
	}
	return false
}

func parseSemver(v string) ([3]int, bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// CLIVerifier runs cosign against the release public key. Without a key it
// rejects the image and does not claim the image is signed.
type CLIVerifier struct {
	Cosign  string
	KeyPath string
	Run     func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (v CLIVerifier) Verify(ctx context.Context, image string) error {
	if v.KeyPath == "" {
		return fmt.Errorf("%w: release key is not configured", ErrSignature)
	}
	if image == "" {
		return ErrSignature
	}
	bin := v.Cosign
	if bin == "" {
		bin = "cosign"
	}
	run := v.Run
	if run == nil {
		run = execCombined
	}
	out, err := run(ctx, bin, "verify", "--key", v.KeyPath, image)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrSignature, Scrub(strings.TrimSpace(string(out))))
	}
	return nil
}
