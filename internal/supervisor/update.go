package supervisor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

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
	ID     string
	Name   string
	Image  string
	Labels map[string]string
}

// Runtime is the Docker socket, held only by the supervisor.
type Runtime interface {
	ListCores(ctx context.Context) ([]ContainerInfo, error)
	StartCore(ctx context.Context, image, name string) (ContainerInfo, error)
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
	StopError    string `json:"stop_error,omitempty"`
}

// Updater runs the nightly swap. It refuses a bad signature or health check
// and leaves the previous core running.
type Updater struct {
	Now           func() time.Time
	Window        Window
	Paused        bool
	Registry      Registry
	Verifier      SignatureVerifier
	Runtime       Runtime
	Migrator      Migrator
	Backup        func(ctx context.Context, reason string) (BackupMeta, error)
	Restore       func(ctx context.Context, id string) error
	Proxy         ProxySwitcher
	Notify        func(ctx context.Context, subject, body string)
	Ledger        *Ledger
	HealthTimeout time.Duration
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
	cores, err := u.Runtime.ListCores(ctx)
	if err != nil {
		return UpdateResult{}, err
	}
	if len(cores) == 0 {
		return UpdateResult{Status: "refused", Reason: "no_core"}, ErrNoCore
	}
	current := cores[0]
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
	if err := u.Verifier.Verify(ctx, release.Image); err != nil {
		u.record("update", map[string]string{"status": "refused", "reason": "signature", "image": release.Image})
		u.notify(ctx, "Update refused", "The new core image failed signature verification. The running version was left in place.")
		return UpdateResult{Status: "refused", Reason: "signature", From: current.Image, To: release.Image}, ErrSignature
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
	ran, err = u.Migrator.Apply(ctx, release.Image)
	if err != nil {
		restored := u.abort(ctx, backupID, ran, nil)
		u.notify(ctx, "Update refused", "Migrations failed. The running version was left in place.")
		return UpdateResult{Status: "refused", Reason: "migration", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran, Restored: restored}, err
	}
	next, err := u.Runtime.StartCore(ctx, release.Image, "togavsu-core-next")
	if err != nil {
		restored := u.abort(ctx, backupID, ran, nil)
		u.notify(ctx, "Update refused", "The new core container did not start. The running version was left in place.")
		return UpdateResult{Status: "refused", Reason: "start", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran, Restored: restored}, err
	}
	timeout := u.HealthTimeout
	if timeout == 0 {
		timeout = time.Minute
	}
	if err := u.Runtime.WaitHealthy(ctx, next.ID, timeout); err != nil {
		restored := u.abort(ctx, backupID, ran, &next)
		u.notify(ctx, "Update refused", "The new core failed its health check. The running version was left in place.")
		return UpdateResult{Status: "refused", Reason: "health", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran, Restored: restored}, ErrHealth
	}
	upstream := next.Name + ":8080"
	if err := u.Proxy.Switch(ctx, upstream); err != nil {
		restored := u.abort(ctx, backupID, ran, &next)
		u.notify(ctx, "Update refused", "Caddy did not switch to the new core. The running version was left in place.")
		return UpdateResult{Status: "refused", Reason: "proxy", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran, Restored: restored}, err
	}
	result := UpdateResult{Status: "applied", From: current.Image, To: release.Image, BackupID: backupID, MigrationRan: ran}
	if err := u.Runtime.Stop(ctx, current.ID); err != nil {
		result.StopError = err.Error()
		u.notify(ctx, "Update needs attention", "The new core is serving, and the previous container did not stop.")
	} else {
		_ = u.Runtime.Remove(ctx, current.ID)
	}
	u.record("update", map[string]string{
		"status":   "applied",
		"from":     current.Image,
		"to":       release.Image,
		"backup":   backupID,
		"migrated": strconv.FormatBool(ran),
	})
	return result, nil
}

func (u *Updater) abort(ctx context.Context, backupID string, migrated bool, started *ContainerInfo) bool {
	if started != nil {
		_ = u.Runtime.Stop(ctx, started.ID)
		_ = u.Runtime.Remove(ctx, started.ID)
	}
	restored := false
	if migrated && backupID != "" && u.Restore != nil {
		if err := u.Restore(ctx, backupID); err == nil {
			restored = true
		}
	}
	u.record("update", map[string]string{"status": "refused", "backup": backupID, "restored": strconv.FormatBool(restored)})
	return restored
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
