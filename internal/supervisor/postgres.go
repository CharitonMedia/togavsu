package supervisor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/ledgerseal"
)

// DumpCommand streams a plain SQL dump. The database URL is passed to the
// client and scrubbed if the client fails.
func DumpCommand(bin, databaseURL string) func(context.Context, io.Writer) error {
	if bin == "" {
		bin = "pg_dump"
	}
	return func(ctx context.Context, w io.Writer) error {
		if databaseURL == "" {
			return fmt.Errorf("DATABASE_URL: %w", ErrBadRequest)
		}
		ctx, cancel := boundDocker(ctx, databaseIOTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "--format=plain", "--clean", "--if-exists", "--no-owner", "--no-privileges", "--dbname", databaseURL)
		cmd.Stdout = w
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pg_dump: %w: %s", err, Scrub(stderr.String()))
		}
		return nil
	}
}

// ApplyProductRestore is the callback cmd/supervisor ships. A database-only
// archive is loaded into a clean database and swapped in one transaction.
// File bytes, module volumes, and remote storage objects are not written
// back. An archive that carries file bytes is refused before that swap.
//
// ponytail: database-only restore, ceiling: file-bearing archives are rejected,
// upgrade: a storage-backend adapter that writes each verified RestoredFile,
// trigger: the files task registers that adapter and a restore test reads the
// bytes back from the backend.
func ApplyProductRestore(ctx context.Context, bin, adminURL, databaseURL string, payload RestorePayload) error {
	if len(payload.Files) > 0 || len(payload.Manifest.Files) > 0 {
		return ErrFileRestoreUnsupported
	}
	return RestoreIntoCleanDatabase(ctx, bin, adminURL, databaseURL, payload.Dump, payload.Manifest.RecordCounts)
}

// RestoreIntoCleanDatabase loads dump into a new database, checks expect, and
// only then renames it over databaseURL. The two renames commit or roll back
// together. An error before that commit leaves the original database in place,
// including tables and schemas that appeared after the backup.
func RestoreIntoCleanDatabase(ctx context.Context, bin, adminURL, databaseURL string, dump io.Reader, expect map[string]int64) error {
	if bin == "" {
		bin = "psql"
	}
	if adminURL == "" {
		return fmt.Errorf("ADMIN_DATABASE_URL: %w", ErrBadRequest)
	}
	orig, err := databaseName(databaseURL)
	if err != nil {
		return err
	}
	alloc, err := allocateDatabase(ctx, bin, adminURL, databaseURL, "togavsu_restore_")
	if err != nil {
		return err
	}
	fresh := alloc.Fresh
	suffix := alloc.Suffix
	dropFresh := alloc.FreshOID != ""
	defer func() {
		if dropFresh {
			clean, cancel := recoveryContext()
			defer cancel()
			_ = dropVerifiedDatabase(clean, bin, adminURL, alloc.OpID, fresh, alloc.FreshOID)
		}
	}()
	target, err := withDatabase(adminURL, fresh)
	if err != nil {
		return err
	}
	// Restored objects must belong to the core's own login role, not the admin, or a
	// non-superuser core cannot use them and audit_log privileges cannot hold.
	owner, err := restoreOwner(adminURL, databaseURL)
	if err != nil {
		return err
	}
	if owner != "" {
		if err := psqlExec(ctx, bin, adminURL, "GRANT CONNECT, CREATE ON DATABASE "+fresh+" TO "+owner); err != nil {
			return err
		}
		dump = io.MultiReader(strings.NewReader("SET ROLE "+owner+";\n"), dump)
	}
	// The dump is already the managed restore workspace. Streaming it avoids a
	// second plaintext file that SIGKILL would leave in the system temp dir.
	if err := ApplyCommand(bin, target)(ctx, dump); err != nil {
		return err
	}
	got, err := CountCommand(bin, target)(ctx)
	if err != nil {
		return err
	}
	if err := compareCounts(expect, got); err != nil {
		return err
	}
	if err := sealRestored(ctx, bin, target, owner); err != nil {
		return err
	}
	if err := swapDatabase(ctx, bin, adminURL, orig, fresh, suffix); err != nil {
		// The fresh name still holds the loaded copy when the swap did not
		// commit. After a commit, or when the catalog could not be read, leave
		// that database alone so a cleanup drop cannot remove the only copy.
		if errors.Is(err, ErrRestoreCleanup) || errors.Is(err, errSwapUnobserved) || errors.Is(err, errConfiguredDatabaseAbsent) || errors.Is(err, errCommitUnjournaled) || errors.Is(err, errRollbackUnjournaled) {
			dropFresh = false
		}
		return err
	}
	dropFresh = false
	return nil
}

// errSwapUnobserved means a rename command returned an ambiguous error and a
// fresh connection could not read pg_database. Callers must not claim the old
// database is still in place.
var errSwapUnobserved = errors.New("database catalog was not observed before restart")

// errConfiguredDatabaseAbsent means the configured name is missing and could
// not be restored before cores would restart.
var errConfiguredDatabaseAbsent = errors.New("configured database is absent")

// errCommitUnjournaled means the rename pair is committed and the previous
// database is still present, but that outcome was not fsynced. Callers must
// not describe the old data as unchanged and must not drop the previous name.
var errCommitUnjournaled = errors.New("committed restore was not recorded")

// errRollbackUnjournaled means the rename rolled back and the loaded database
// is still present, but that outcome was not fsynced. Callers must not drop
// the loaded name: it is the only catalog fact that distinguishes a rollback
// from a committed swap whose previous name was already removed.
var errRollbackUnjournaled = errors.New("rolled-back restore was not recorded")

const swapRecoveryTimeout = 30 * time.Second

// cleanupIOTimeout bounds a database cleanup that must run after the caller's
// context is already canceled. It is a fresh deadline, not the canceled one.
var cleanupIOTimeout = swapRecoveryTimeout

// databaseIOTimeout caps psql and pg_dump when the caller has no deadline.
// A scheduled backup uses the process lifetime context.
var databaseIOTimeout = 30 * time.Minute

type swapHookFunc func(configured, fresh, replaced string) error

type swapHookKey struct{}

type swapCommitKey struct{}

type swapRollbackKey struct{}

// WithSwapHook attaches a function that runs after the fresh database is
// loaded and before the rename transaction starts. The hook must return only
// after its own durable record is fsynced.
func WithSwapHook(ctx context.Context, hook func(configured, fresh, replaced string) error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, swapHookKey{}, swapHookFunc(hook))
}

func swapHook(ctx context.Context) swapHookFunc {
	if ctx == nil {
		return nil
	}
	hook, _ := ctx.Value(swapHookKey{}).(swapHookFunc)
	return hook
}

// WithSwapCommitted attaches a function that runs after the rename pair is
// visible and before the previous database is dropped. The hook must return
// only after that outcome is fsynced. A hook error leaves the old name in place.
func WithSwapCommitted(ctx context.Context, hook func(configured, fresh, replaced string) error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, swapCommitKey{}, swapHookFunc(hook))
}

func swapCommitted(ctx context.Context) swapHookFunc {
	if ctx == nil {
		return nil
	}
	hook, _ := ctx.Value(swapCommitKey{}).(swapHookFunc)
	return hook
}

func recordSwapCommitted(ctx context.Context, configured, fresh, replaced string) error {
	if hook := swapCommitted(ctx); hook != nil {
		return hook(configured, fresh, replaced)
	}
	return nil
}

// WithSwapRolledBack attaches a function that runs after the catalog shows
// the rename rolled back and before the loaded database may be dropped. The
// hook must return only after that outcome is fsynced. A hook error leaves
// the loaded database in place.
func WithSwapRolledBack(ctx context.Context, hook func(configured, fresh, replaced string) error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, swapRollbackKey{}, swapHookFunc(hook))
}

func swapRolledBack(ctx context.Context) swapHookFunc {
	if ctx == nil {
		return nil
	}
	hook, _ := ctx.Value(swapRollbackKey{}).(swapHookFunc)
	return hook
}

func recordSwapRolledBack(ctx context.Context, configured, fresh, replaced string) error {
	if hook := swapRolledBack(ctx); hook != nil {
		return hook(configured, fresh, replaced)
	}
	return nil
}

func recoveryContext() (context.Context, context.CancelFunc) {
	limit := cleanupIOTimeout
	if limit <= 0 {
		limit = swapRecoveryTimeout
	}
	return context.WithTimeout(context.Background(), limit)
}

// PostgresCatalog reads pg_database through the admin connection. Fence waits
// until this operation's rename transaction has ended. It does not signal a
// backend by pid.
type PostgresCatalog struct {
	Bin      string
	AdminURL string
}

// Cluster is the admin connection's pg_control system identifier.
func (p PostgresCatalog) Cluster(ctx context.Context) (string, error) {
	if p.AdminURL == "" {
		return "", errSwapUnobserved
	}
	bin := p.Bin
	if bin == "" {
		bin = "psql"
	}
	return clusterID(ctx, bin, p.AdminURL)
}

// DatabaseOID is the configured database oid on the admin connection.
func (p PostgresCatalog) DatabaseOID(ctx context.Context, name string) (string, error) {
	if p.AdminURL == "" {
		return "", errSwapUnobserved
	}
	bin := p.Bin
	if bin == "" {
		bin = "psql"
	}
	return databaseOID(ctx, bin, p.AdminURL, name)
}

func (p PostgresCatalog) Drop(ctx context.Context, name string) error {
	if p.AdminURL == "" {
		return errSwapUnobserved
	}
	if !ident(name) {
		return fmt.Errorf("database name: %w", ErrBadRequest)
	}
	bin := p.Bin
	if bin == "" {
		bin = "psql"
	}
	_, _ = psqlLines(ctx, bin, p.AdminURL, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '"+name+"' AND pid <> pg_backend_pid()")
	return dropDatabase(ctx, bin, p.AdminURL, name)
}

func (p PostgresCatalog) Observe(ctx context.Context, configured, fresh, replaced string) (map[string]bool, error) {
	if p.AdminURL == "" {
		return nil, errSwapUnobserved
	}
	bin := p.Bin
	if bin == "" {
		bin = "psql"
	}
	return observeDatabases(ctx, bin, p.AdminURL, configured, fresh, replaced)
}

// Fence cancels a still-pending restore operation, or reports that the rename
// transaction already committed. It locks that operation row. A timeout or a
// missing row does not declare rollback.
func (p PostgresCatalog) Fence(ctx context.Context, configured, fresh, replaced string) (string, error) {
	if p.AdminURL == "" {
		return "", errSwapUnobserved
	}
	bin := p.Bin
	if bin == "" {
		bin = "psql"
	}
	opID, err := lookupRestoreOp(ctx, bin, p.AdminURL, configured, fresh, replaced)
	if err != nil {
		return "", err
	}
	return fenceRestoreOp(ctx, bin, p.AdminURL, opID)
}

// Reconcile drops temp databases whose recorded oid still names that fresh
// database. It does not drop by name prefix, and it does not drop a database
// whose oid was never recorded or no longer matches.
func (p PostgresCatalog) Reconcile(ctx context.Context) error {
	if p.AdminURL == "" {
		return errSwapUnobserved
	}
	bin := p.Bin
	if bin == "" {
		bin = "psql"
	}
	return reconcileOwnedDatabases(ctx, bin, p.AdminURL)
}

// DropVerified drops fresh only when opID owns that exact oid and the oid
// still bears the fresh name.
func (p PostgresCatalog) DropVerified(ctx context.Context, opID, fresh, oid string) error {
	if p.AdminURL == "" {
		return errSwapUnobserved
	}
	bin := p.Bin
	if bin == "" {
		bin = "psql"
	}
	return dropVerifiedDatabase(ctx, bin, p.AdminURL, opID, fresh, oid)
}

// swapDatabase renames orig to replaced and fresh to orig in one Postgres
// transaction, then drops replaced. DROP DATABASE cannot run inside a
// transaction block (verified on Postgres 17), so the drop stays outside.
// A lost acknowledgment is reconciled by reading pg_database on a fresh
// bounded context before this function returns. The configured name is not
// left absent: a lone committed rename is undone, and a committed pair is
// finished. Cores restart only after this returns.
func swapDatabase(ctx context.Context, bin, adminURL, orig, fresh, suffix string) error {
	replaced := orig + "_replaced_" + suffix
	for _, name := range []string{orig, fresh, replaced} {
		if !ident(name) {
			return fmt.Errorf("database name: %w", ErrBadRequest)
		}
	}
	rec, cancel := recoveryContext()
	defer cancel()
	if hook := swapHook(ctx); hook != nil {
		if err := hook(orig, fresh, replaced); err != nil {
			return err
		}
	}
	if _, err := clusterID(ctx, bin, adminURL); err != nil {
		return err
	}
	opID := restoreOpID(ctx)
	if opID == "" {
		var idErr error
		opID, idErr = newRestoreOpID(time.Now())
		if idErr != nil {
			return idErr
		}
	}
	if err := ensureRestoreOp(ctx, bin, adminURL, opID, orig, fresh, replaced); err != nil {
		return err
	}
	_, _ = psqlLines(rec, bin, adminURL, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '"+orig+"' AND pid <> pg_backend_pid()")
	txn, err := swapTransactionSQL(opID, orig, fresh, replaced)
	if err != nil {
		return err
	}
	txnErr := psqlExec(ctx, bin, adminURL, txn)
	if txnErr != nil {
		return reconcileSwap(ctx, bin, adminURL, opID, orig, fresh, replaced, txnErr)
	}
	if err := recordSwapCommitted(ctx, orig, fresh, replaced); err != nil {
		return fmt.Errorf("%w: %v", errCommitUnjournaled, err)
	}
	dropErr := psqlExec(ctx, bin, adminURL, "DROP DATABASE "+replaced)
	if dropErr != nil {
		return reconcileSwap(ctx, bin, adminURL, opID, orig, fresh, replaced, dropErr)
	}
	return reconcileSwap(ctx, bin, adminURL, opID, orig, fresh, replaced, nil)
}

func reconcileSwap(hookCtx context.Context, bin, adminURL, opID, orig, fresh, replaced string, stepErr error) error {
	ctx, cancel := recoveryContext()
	defer cancel()
	last := stepErr
	fenced := false
	committedRow := false
	for attempt := 0; attempt < 4; attempt++ {
		st, err := observeDatabases(ctx, bin, adminURL, orig, fresh, replaced)
		if err != nil {
			last = fmt.Errorf("%w: %v", errSwapUnobserved, err)
			continue
		}
		switch {
		case st[orig] && !st[fresh] && !st[replaced]:
			// The fresh name is gone. That is a committed swap only when this
			// operation row says so. Recovery may already have dropped a
			// cancelled allocation, and that must not look like success.
			opLit, lerr := sqlText(opID)
			if lerr != nil {
				return lerr
			}
			lines, qerr := psqlLines(ctx, bin, adminURL, "SELECT state FROM togavsu_restore_op WHERE op_id = "+opLit)
			if qerr != nil || len(lines) != 1 || lines[0] != "committed" {
				return fmt.Errorf("%w: restore operation did not commit", errSwapUnobserved)
			}
			return nil
		case st[orig] && !st[fresh] && st[replaced]:
			if err := recordSwapCommitted(hookCtx, orig, fresh, replaced); err != nil {
				return fmt.Errorf("%w: %v", errCommitUnjournaled, err)
			}
			if err := dropDatabase(ctx, bin, adminURL, replaced); err != nil {
				last = err
				continue
			}
			return nil
		case st[orig] && st[fresh] && !st[replaced]:
			// Unchanged names are not a rollback until this operation row is
			// cancelled. The cancel commits before any caller restarts writers.
			// The rename batch locks that same row and refuses a cancelled or
			// missing operation, so a command that has not locked yet cannot
			// commit afterwards. A lock timeout leaves the operation pending.
			if !fenced {
				state, err := fenceRestoreOp(ctx, bin, adminURL, opID)
				if err != nil {
					return fmt.Errorf("%w: rename transaction is still in flight: %v", errSwapUnobserved, err)
				}
				if state == "absent" {
					return fmt.Errorf("%w: restore operation is absent", errSwapUnobserved)
				}
				fenced = true
				committedRow = state == "committed"
				continue
			}
			if committedRow {
				return fmt.Errorf("%w: restore operation committed but the catalog did not", errSwapUnobserved)
			}
			if stepErr != nil {
				if err := recordSwapRolledBack(hookCtx, orig, fresh, replaced); err != nil {
					return fmt.Errorf("%w: %v", errRollbackUnjournaled, err)
				}
				return stepErr
			}
			return nil
		case !st[orig] && st[fresh] && st[replaced]:
			// Only the first rename is visible. Put the previous database
			// back under the configured name. If that fails, publish the
			// loaded database so the name still exists, then finish cleanup.
			if err := renameDatabase(ctx, bin, adminURL, replaced, orig); err != nil {
				if err2 := renameDatabase(ctx, bin, adminURL, fresh, orig); err2 != nil {
					last = fmt.Errorf("%w: rollback %v; publish %v", errConfiguredDatabaseAbsent, err, err2)
				}
				continue
			}
			if stepErr != nil {
				return stepErr
			}
			return fmt.Errorf("restore rename did not commit")
		case !st[orig] && !st[fresh] && st[replaced]:
			if err := renameDatabase(ctx, bin, adminURL, replaced, orig); err != nil {
				last = fmt.Errorf("%w: %v", errConfiguredDatabaseAbsent, err)
				continue
			}
			if stepErr != nil {
				return stepErr
			}
			return nil
		case !st[orig] && st[fresh] && !st[replaced]:
			if err := renameDatabase(ctx, bin, adminURL, fresh, orig); err != nil {
				last = fmt.Errorf("%w: %v", errConfiguredDatabaseAbsent, err)
				continue
			}
			continue
		default:
			if st[orig] {
				if stepErr != nil {
					return stepErr
				}
				return nil
			}
			last = fmt.Errorf("%w: %s", errConfiguredDatabaseAbsent, orig)
		}
	}
	st, err := observeDatabases(ctx, bin, adminURL, orig, fresh, replaced)
	if err != nil {
		return fmt.Errorf("%w: %v", errSwapUnobserved, err)
	}
	if !st[orig] {
		if last == nil || !errors.Is(last, errConfiguredDatabaseAbsent) {
			return fmt.Errorf("%w: %v", errConfiguredDatabaseAbsent, last)
		}
		return last
	}
	if st[replaced] && !st[fresh] {
		return fmt.Errorf("%w: %s", ErrRestoreCleanup, Scrub(fmt.Sprint(last)))
	}
	if stepErr != nil && st[fresh] && !st[replaced] {
		if !fenced {
			return fmt.Errorf("%w: rename transaction was not confirmed finished", errSwapUnobserved)
		}
		if err := recordSwapRolledBack(hookCtx, orig, fresh, replaced); err != nil {
			return fmt.Errorf("%w: %v", errRollbackUnjournaled, err)
		}
		return stepErr
	}
	return nil
}

// swapFenceTimeout is how long recovery waits to lock the operation row.
// A timeout is not evidence of rollback.
var swapFenceTimeout = 5 * time.Second

type restoreOpIDKey struct{}

type allocHookKey struct{}

// RestoreAlloc is one temp database this process owns.
type RestoreAlloc struct {
	OpID          string
	ClusterID     string
	Configured    string
	Fresh         string
	Replaced      string
	Suffix        string
	ConfiguredOID string
	FreshOID      string
}

// WithRestoreOpID binds CREATE and the rename batch to one journaled operation.
func WithRestoreOpID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, restoreOpIDKey{}, id)
}

func restoreOpID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(restoreOpIDKey{}).(string)
	return id
}

// WithAllocHook runs before CREATE DATABASE and again after the oid is verified.
// The hook must return only after its record is fsynced.
func WithAllocHook(ctx context.Context, hook func(RestoreAlloc) error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, allocHookKey{}, hook)
}

func allocHook(ctx context.Context) func(RestoreAlloc) error {
	if ctx == nil {
		return nil
	}
	hook, _ := ctx.Value(allocHookKey{}).(func(RestoreAlloc) error)
	return hook
}

func clusterID(ctx context.Context, bin, databaseURL string) (string, error) {
	lines, err := psqlLines(ctx, bin, databaseURL, "SELECT system_identifier FROM pg_control_system()")
	if err != nil {
		return "", err
	}
	if len(lines) != 1 || strings.Trim(lines[0], "0123456789") != "" {
		return "", fmt.Errorf("database cluster: %w", errSwapUnobserved)
	}
	return lines[0], nil
}

func assertSameCluster(ctx context.Context, bin, adminURL, databaseURL string) (string, error) {
	adminID, err := clusterID(ctx, bin, adminURL)
	if err != nil {
		return "", err
	}
	dbID, err := clusterID(ctx, bin, databaseURL)
	if err != nil {
		return "", err
	}
	if adminID != dbID {
		return "", fmt.Errorf("admin connection is a different database cluster: %w", ErrBadRequest)
	}
	name, err := databaseName(databaseURL)
	if err != nil {
		return "", err
	}
	oid, err := databaseOID(ctx, bin, adminURL, name)
	if err != nil {
		return "", err
	}
	if oid == "" {
		return "", fmt.Errorf("configured database is not on the admin cluster: %w", ErrBadRequest)
	}
	return adminID, nil
}

func sqlText(value string) (string, error) {
	if value == "" || len(value) > 80 {
		return "", fmt.Errorf("operation id: %w", ErrBadRequest)
	}
	for _, c := range value {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return "", fmt.Errorf("operation id: %w", ErrBadRequest)
		}
	}
	return "'" + value + "'", nil
}

func ensureRestoreSchema(ctx context.Context, bin, adminURL string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS togavsu_restore_op (
  op_id text PRIMARY KEY,
  configured text NOT NULL,
  fresh text NOT NULL,
  replaced text NOT NULL,
  cluster_id text NOT NULL,
  state text NOT NULL,
  fresh_oid text,
  configured_oid text
);
CREATE OR REPLACE FUNCTION togavsu_fence_op(op text, wait_ms integer)
RETURNS text
LANGUAGE plpgsql
AS $fn$
DECLARE s text; live text; row_cluster text; row_oid text; live_oid text; row_name text;
BEGIN
  PERFORM set_config('lock_timeout', wait_ms::text || 'ms', true);
  SELECT system_identifier::text INTO live FROM pg_control_system();
  SELECT state, cluster_id, configured_oid, configured INTO s, row_cluster, row_oid, row_name
    FROM togavsu_restore_op WHERE op_id = op FOR UPDATE;
  IF NOT FOUND THEN
    RETURN 'absent';
  END IF;
  SELECT oid::text INTO live_oid FROM pg_database WHERE datname = row_name;
  IF row_cluster IS DISTINCT FROM live OR (row_oid IS NOT NULL AND row_oid IS DISTINCT FROM live_oid) THEN
    IF s IN ('owned', 'loading', 'allocating') THEN
      UPDATE togavsu_restore_op SET state = 'cancelled' WHERE op_id = op AND state = s;
    END IF;
    RETURN 'absent';
  END IF;
  IF s IN ('owned', 'loading', 'allocating') THEN
    UPDATE togavsu_restore_op SET state = 'cancelled' WHERE op_id = op AND state = s;
    RETURN 'cancelled';
  END IF;
  RETURN s;
END;
$fn$;`
	return psqlExec(ctx, bin, adminURL, ddl)
}

func allocateDatabase(ctx context.Context, bin, adminURL, databaseURL, prefix string) (RestoreAlloc, error) {
	var zero RestoreAlloc
	cluster, err := assertSameCluster(ctx, bin, adminURL, databaseURL)
	if err != nil {
		return zero, err
	}
	configured, err := databaseName(databaseURL)
	if err != nil {
		return zero, err
	}
	if err := ensureRestoreSchema(ctx, bin, adminURL); err != nil {
		return zero, err
	}
	opID := restoreOpID(ctx)
	if opID == "" {
		opID, err = newRestoreOpID(time.Now())
		if err != nil {
			return zero, err
		}
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return zero, err
	}
	suffix := hex.EncodeToString(raw[:])
	fresh := prefix + suffix
	if !ident(fresh) {
		return zero, fmt.Errorf("database name: %w", ErrBadRequest)
	}
	replaced := configured + "_replaced_" + suffix
	if !ident(replaced) {
		return zero, fmt.Errorf("database name: %w", ErrBadRequest)
	}
	cfgOID, err := databaseOID(ctx, bin, adminURL, configured)
	if err != nil {
		return zero, err
	}
	if cfgOID == "" {
		return zero, fmt.Errorf("configured database is not on the admin cluster: %w", ErrBadRequest)
	}
	alloc := RestoreAlloc{
		OpID: opID, ClusterID: cluster, Configured: configured, Fresh: fresh,
		Replaced: replaced, Suffix: suffix, ConfiguredOID: cfgOID,
	}
	if hook := allocHook(ctx); hook != nil {
		if err := hook(alloc); err != nil {
			return zero, err
		}
	}
	opLit, err := sqlText(opID)
	if err != nil {
		return zero, err
	}
	insert := "INSERT INTO togavsu_restore_op (op_id, configured, fresh, replaced, cluster_id, state, configured_oid) VALUES (" +
		opLit + ", '" + configured + "', '" + fresh + "', '" + replaced + "', '" + cluster + "', 'allocating', '" + cfgOID + "')"
	if err := psqlExec(ctx, bin, adminURL, insert); err != nil {
		return zero, err
	}
	absent, err := databaseOID(ctx, bin, adminURL, fresh)
	if err != nil {
		return zero, err
	}
	if absent != "" {
		_ = psqlExec(ctx, bin, adminURL, "UPDATE togavsu_restore_op SET state = 'cancelled' WHERE op_id = "+opLit+" AND fresh_oid IS NULL")
		return zero, fmt.Errorf("fresh database already exists: %w", ErrBadRequest)
	}
	createErr := psqlExec(ctx, bin, adminURL, "CREATE DATABASE "+fresh)
	oid, oidErr := databaseOID(ctx, bin, adminURL, fresh)
	if oidErr != nil {
		return zero, fmt.Errorf("%w: %v", errSwapUnobserved, oidErr)
	}
	if oid == "" {
		_ = psqlExec(ctx, bin, adminURL, "UPDATE togavsu_restore_op SET state = 'cancelled' WHERE op_id = "+opLit+" AND state = 'allocating' AND fresh_oid IS NULL")
		if createErr != nil {
			return zero, createErr
		}
		return zero, fmt.Errorf("%w: fresh database was not created", errSwapUnobserved)
	}
	if createErr != nil && !ambiguousExec(createErr) {
		return zero, createErr
	}
	own := "UPDATE togavsu_restore_op SET state = 'loading', fresh_oid = '" + oid + "' WHERE op_id = " + opLit + " AND state = 'allocating' AND fresh_oid IS NULL"
	if err := psqlExec(ctx, bin, adminURL, own); err != nil {
		return zero, fmt.Errorf("%w: %v", errSwapUnobserved, err)
	}
	alloc.FreshOID = oid
	if hook := allocHook(ctx); hook != nil {
		if err := hook(alloc); err != nil {
			return alloc, err
		}
	}
	if createErr != nil {
		return alloc, fmt.Errorf("%w: %v", errSwapUnobserved, createErr)
	}
	return alloc, nil
}

func ambiguousExec(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "already exists") || strings.Contains(msg, "syntax error") {
		return false
	}
	return true
}

func databaseOID(ctx context.Context, bin, adminURL, name string) (string, error) {
	if !ident(name) {
		return "", fmt.Errorf("database name: %w", ErrBadRequest)
	}
	lines, err := psqlLines(ctx, bin, adminURL, "SELECT oid::text FROM pg_database WHERE datname = '"+name+"'")
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", nil
	}
	if len(lines) != 1 || strings.Trim(lines[0], "0123456789") != "" {
		return "", fmt.Errorf("database oid: %w", errSwapUnobserved)
	}
	return lines[0], nil
}

func ensureRestoreOp(ctx context.Context, bin, adminURL, opID, configured, fresh, replaced string) error {
	if err := ensureRestoreSchema(ctx, bin, adminURL); err != nil {
		return err
	}
	opLit, err := sqlText(opID)
	if err != nil {
		return err
	}
	cluster, err := clusterID(ctx, bin, adminURL)
	if err != nil {
		return err
	}
	cfgOID, err := databaseOID(ctx, bin, adminURL, configured)
	if err != nil {
		return err
	}
	freshOID, err := databaseOID(ctx, bin, adminURL, fresh)
	if err != nil {
		return err
	}
	// A row written before CREATE stays. Direct swap callers insert owned.
	// A cancelled or committed row is terminal: a late command must not promote
	// it and must not start the rename.
	present, err := psqlLines(ctx, bin, adminURL, "SELECT state || '|' || cluster_id || '|' || COALESCE(configured_oid, '') FROM togavsu_restore_op WHERE op_id = "+opLit)
	if err != nil {
		return err
	}
	if len(present) == 1 {
		parts := strings.Split(present[0], "|")
		if len(parts) != 3 {
			return fmt.Errorf("%w: restore operation row", errSwapUnobserved)
		}
		state, rowCluster, rowOID := parts[0], parts[1], parts[2]
		if rowCluster != cluster {
			return fmt.Errorf("admin connection is a different database cluster: %w", ErrBadRequest)
		}
		if rowOID != "" && cfgOID != "" && rowOID != cfgOID {
			return fmt.Errorf("configured database identity changed: %w", ErrBadRequest)
		}
		switch state {
		case "cancelled", "committed":
			return fmt.Errorf("restore operation rejected: %w", ErrBadRequest)
		case "allocating", "loading":
			if freshOID == "" {
				return fmt.Errorf("%w: fresh database identity", errSwapUnobserved)
			}
			return psqlExec(ctx, bin, adminURL, "UPDATE togavsu_restore_op SET state = 'owned', fresh_oid = '"+freshOID+"' WHERE op_id = "+opLit+" AND state IN ('allocating', 'loading')")
		case "owned":
			return nil
		default:
			return fmt.Errorf("%w: restore operation", errSwapUnobserved)
		}
	}
	if freshOID == "" {
		freshOID = "0"
	}
	return psqlExec(ctx, bin, adminURL, "INSERT INTO togavsu_restore_op (op_id, configured, fresh, replaced, cluster_id, state, configured_oid, fresh_oid) VALUES ("+
		opLit+", '"+configured+"', '"+fresh+"', '"+replaced+"', '"+cluster+"', 'owned', '"+cfgOID+"', '"+freshOID+"')")
}

func restoreOpGuardSQL(opID string) (string, error) {
	opLit, err := sqlText(opID)
	if err != nil {
		return "", err
	}
	return "DO $$ DECLARE s text; want text; cfg text; live text; BEGIN SELECT state, configured_oid, configured INTO s, want, cfg FROM togavsu_restore_op WHERE op_id = " + opLit +
		" FOR UPDATE; SELECT oid::text INTO live FROM pg_database WHERE datname = cfg; IF s IS DISTINCT FROM 'owned' OR (want IS NOT NULL AND want IS DISTINCT FROM live) THEN RAISE EXCEPTION 'togavsu restore operation rejected: %', COALESCE(s, 'absent'); END IF; END $$;", nil
}

func swapTransactionSQL(opID, orig, fresh, replaced string) (string, error) {
	guard, err := restoreOpGuardSQL(opID)
	if err != nil {
		return "", err
	}
	opLit, err := sqlText(opID)
	if err != nil {
		return "", err
	}
	return "BEGIN; " + guard + " ALTER DATABASE " + orig + " RENAME TO " + replaced + "; ALTER DATABASE " + fresh + " RENAME TO " + orig + "; UPDATE togavsu_restore_op SET state = 'committed' WHERE op_id = " + opLit + " AND state = 'owned'; COMMIT;", nil
}

func fenceRestoreOp(ctx context.Context, bin, adminURL, opID string) (string, error) {
	if err := ensureRestoreSchema(ctx, bin, adminURL); err != nil {
		return "", err
	}
	opLit, err := sqlText(opID)
	if err != nil {
		return "", err
	}
	ms := swapFenceTimeout.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	lines, err := psqlLines(ctx, bin, adminURL, fmt.Sprintf("SELECT togavsu_fence_op(%s, %d)", opLit, ms))
	if err != nil {
		return "", err
	}
	if len(lines) != 1 {
		return "", fmt.Errorf("%w: restore operation fence", errSwapUnobserved)
	}
	switch lines[0] {
	case "cancelled", "committed", "absent", "owned", "allocating":
		return lines[0], nil
	default:
		return "", fmt.Errorf("%w: restore operation fence", errSwapUnobserved)
	}
}

func lookupRestoreOp(ctx context.Context, bin, adminURL, configured, fresh, replaced string) (string, error) {
	lines, err := psqlLines(ctx, bin, adminURL, "SELECT op_id FROM togavsu_restore_op WHERE configured = '"+configured+"' AND fresh = '"+fresh+"' AND replaced = '"+replaced+"' ORDER BY op_id DESC")
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", errSwapUnobserved
	}
	return lines[0], nil
}

func dropVerifiedDatabase(ctx context.Context, bin, adminURL, opID, fresh, oid string) error {
	if oid == "" || strings.Trim(oid, "0123456789") != "" {
		return fmt.Errorf("database oid: %w", ErrBadRequest)
	}
	if !ident(fresh) {
		return fmt.Errorf("database name: %w", ErrBadRequest)
	}
	opLit, err := sqlText(opID)
	if err != nil {
		return err
	}
	lines, err := psqlLines(ctx, bin, adminURL, "SELECT datname FROM pg_database WHERE oid = "+oid+"::oid")
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		_ = psqlExec(ctx, bin, adminURL, "UPDATE togavsu_restore_op SET state = 'cancelled' WHERE op_id = "+opLit+" AND state = 'owned' AND fresh_oid = '"+oid+"'")
		return nil
	}
	if len(lines) != 1 || lines[0] != fresh {
		// The recorded oid now names something else, or the lookup was
		// ambiguous. Leave that database alone.
		opLit, lerr := sqlText(opID)
		if lerr != nil {
			return lerr
		}
		_ = psqlExec(ctx, bin, adminURL, "UPDATE togavsu_restore_op SET state = 'cancelled' WHERE op_id = "+opLit+" AND fresh_oid = '"+oid+"' AND state = 'loading' AND fresh = '"+fresh+"'")
		return nil
	}
	owned, err := psqlLines(ctx, bin, adminURL, "SELECT op_id FROM togavsu_restore_op WHERE op_id = "+opLit+" AND fresh = '"+fresh+"' AND fresh_oid = '"+oid+"' AND state IN ('owned', 'cancelled', 'allocating', 'loading')")
	if err != nil {
		return err
	}
	if len(owned) != 1 {
		return fmt.Errorf("%w: fresh database is not owned", errSwapUnobserved)
	}
	_, _ = psqlLines(ctx, bin, adminURL, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '"+fresh+"' AND pid <> pg_backend_pid()")
	if err := psqlExec(ctx, bin, adminURL, "DROP DATABASE "+fresh); err != nil {
		return err
	}
	return psqlExec(ctx, bin, adminURL, "UPDATE togavsu_restore_op SET state = 'cancelled' WHERE op_id = "+opLit+" AND fresh_oid = '"+oid+"' AND state IN ('owned', 'allocating', 'loading')")
}

// DropVerifiedReplaced drops name only when that oid is the operation's
// original configured database and the connected cluster matches the row.
// A mismatch drops nothing and does not complete cleanup.
func (p PostgresCatalog) DropVerifiedReplaced(ctx context.Context, opID, name, oid string) error {
	if p.AdminURL == "" {
		return errSwapUnobserved
	}
	bin := p.Bin
	if bin == "" {
		bin = "psql"
	}
	return dropVerifiedReplaced(ctx, bin, p.AdminURL, opID, name, oid)
}

func dropVerifiedReplaced(ctx context.Context, bin, adminURL, opID, name, oid string) error {
	if oid == "" || strings.Trim(oid, "0123456789") != "" {
		return fmt.Errorf("database oid: %w", ErrBadRequest)
	}
	if !ident(name) {
		return fmt.Errorf("database name: %w", ErrBadRequest)
	}
	opLit, err := sqlText(opID)
	if err != nil {
		return err
	}
	live, err := clusterID(ctx, bin, adminURL)
	if err != nil {
		return err
	}
	lines, err := psqlLines(ctx, bin, adminURL, "SELECT datname FROM pg_database WHERE oid = "+oid+"::oid")
	if err != nil {
		return err
	}
	if len(lines) != 1 || lines[0] != name {
		return errSwapUnobserved
	}
	owned, err := psqlLines(ctx, bin, adminURL, "SELECT op_id FROM togavsu_restore_op WHERE op_id = "+opLit+" AND replaced = '"+name+"' AND configured_oid = '"+oid+"' AND cluster_id = '"+live+"' AND state = 'committed'")
	if err != nil {
		return err
	}
	if len(owned) != 1 {
		return fmt.Errorf("%w: replaced database is not owned", errSwapUnobserved)
	}
	_, _ = psqlLines(ctx, bin, adminURL, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '"+name+"' AND pid <> pg_backend_pid()")
	return psqlExec(ctx, bin, adminURL, "DROP DATABASE "+name)
}

func reconcileOwnedDatabases(ctx context.Context, bin, adminURL string) error {
	if err := ensureRestoreSchema(ctx, bin, adminURL); err != nil {
		return err
	}
	live, err := clusterID(ctx, bin, adminURL)
	if err != nil {
		return err
	}
	lines, err := psqlLines(ctx, bin, adminURL, "SELECT op_id || ' ' || fresh || ' ' || COALESCE(fresh_oid, '') || ' ' || state || ' ' || cluster_id FROM togavsu_restore_op WHERE state IN ('loading', 'allocating')")
	if err != nil {
		return err
	}
	var unresolved error
	for _, line := range lines {
		parts := strings.Split(line, " ")
		if len(parts) != 5 {
			return fmt.Errorf("%w: restore operation row", errSwapUnobserved)
		}
		opID, fresh, oid, state, rowCluster := parts[0], parts[1], parts[2], parts[3], parts[4]
		if rowCluster != live {
			unresolved = fmt.Errorf("%w: cluster identity", errSwapUnobserved)
			continue
		}
		if oid == "" {
			current, oerr := databaseOID(ctx, bin, adminURL, fresh)
			if oerr != nil {
				unresolved = fmt.Errorf("%w: %v", errSwapUnobserved, oerr)
				continue
			}
			if current == "" && state == "allocating" {
				opLit, lerr := sqlText(opID)
				if lerr != nil {
					return lerr
				}
				if err := psqlExec(ctx, bin, adminURL, "UPDATE togavsu_restore_op SET state = 'cancelled' WHERE op_id = "+opLit+" AND state = 'allocating' AND fresh_oid IS NULL"); err != nil {
					return err
				}
			}
			continue
		}
		if err := dropVerifiedDatabase(ctx, bin, adminURL, opID, fresh, oid); err != nil {
			if errors.Is(err, errSwapUnobserved) {
				unresolved = err
				continue
			}
			return err
		}
	}
	return unresolved
}

func observeDatabases(ctx context.Context, bin, adminURL string, names ...string) (map[string]bool, error) {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		if !ident(name) {
			return nil, fmt.Errorf("database name: %w", ErrBadRequest)
		}
		quoted = append(quoted, "'"+name+"'")
	}
	lines, err := psqlLines(ctx, bin, adminURL, "SELECT datname FROM pg_database WHERE datname IN ("+strings.Join(quoted, ",")+")")
	if err != nil {
		return nil, err
	}
	got := make(map[string]bool, len(lines))
	for _, line := range lines {
		got[line] = true
	}
	return got, nil
}

func renameDatabase(ctx context.Context, bin, adminURL, from, to string) error {
	if !ident(from) || !ident(to) {
		return fmt.Errorf("database name: %w", ErrBadRequest)
	}
	return psqlExec(ctx, bin, adminURL, "ALTER DATABASE "+from+" RENAME TO "+to)
}

func dropDatabase(ctx context.Context, bin, adminURL, name string) error {
	if !ident(name) {
		return fmt.Errorf("database name: %w", ErrBadRequest)
	}
	return psqlExec(ctx, bin, adminURL, "DROP DATABASE "+name)
}

func psqlExec(ctx context.Context, bin, databaseURL, sql string) error {
	ctx, cancel := boundDocker(ctx, databaseIOTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--set", "ON_ERROR_STOP=1", "--dbname", databaseURL, "--command", sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql: %w: %s", err, Scrub(stderr.String()))
	}
	return nil
}

func databaseName(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !ident(name) {
		return "", fmt.Errorf("database name: %w", ErrBadRequest)
	}
	return name, nil
}

// ApplyCommand restores a plain SQL dump.
func ApplyCommand(bin, databaseURL string) func(context.Context, io.Reader) error {
	if bin == "" {
		bin = "psql"
	}
	return func(ctx context.Context, r io.Reader) error {
		if databaseURL == "" {
			return fmt.Errorf("DATABASE_URL: %w", ErrBadRequest)
		}
		ctx, cancel := boundDocker(ctx, databaseIOTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "--set", "ON_ERROR_STOP=1", "--dbname", databaseURL)
		cmd.Stdin = r
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("psql: %w: %s", err, Scrub(stderr.String()))
		}
		return nil
	}
}

// CountCommand returns portable row counts for ordinary tables.
func CountCommand(bin, databaseURL string) func(context.Context) (map[string]int64, error) {
	if bin == "" {
		bin = "psql"
	}
	return func(ctx context.Context) (map[string]int64, error) {
		if databaseURL == "" {
			return nil, fmt.Errorf("DATABASE_URL: %w", ErrBadRequest)
		}
		names, err := psqlLines(ctx, bin, databaseURL, `
SELECT n.nspname || '.' || c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
ORDER BY 1`)
		if err != nil {
			return nil, err
		}
		counts := make(map[string]int64, len(names))
		for _, name := range names {
			if !safeTable(name) {
				return nil, fmt.Errorf("table %q: %w", name, ErrBadRequest)
			}
			quoted, err := quoteTable(name)
			if err != nil {
				return nil, err
			}
			lines, err := psqlLines(ctx, bin, databaseURL, "SELECT count(*) FROM "+quoted)
			if err != nil {
				return nil, err
			}
			if len(lines) != 1 {
				return nil, fmt.Errorf("count %s: %w", name, ErrArchive)
			}
			var n int64
			if _, err := fmt.Sscan(lines[0], &n); err != nil {
				return nil, err
			}
			counts[name] = n
		}
		return counts, nil
	}
}

// MeasureCommand restores a dump into a throwaway database and counts rows.
// The primary database is left alone. An empty admin URL refuses the check.
func MeasureCommand(bin, adminURL string) func(context.Context, io.Reader) (map[string]int64, error) {
	if bin == "" {
		bin = "psql"
	}
	return func(ctx context.Context, r io.Reader) (map[string]int64, error) {
		if adminURL == "" {
			return nil, fmt.Errorf("ADMIN_DATABASE_URL: %w", ErrBadRequest)
		}
		alloc, err := allocateDatabase(ctx, bin, adminURL, adminURL, "togavsu_check_")
		if err != nil {
			return nil, err
		}
		defer func() {
			clean, cancel := recoveryContext()
			defer cancel()
			_ = dropVerifiedDatabase(clean, bin, adminURL, alloc.OpID, alloc.Fresh, alloc.FreshOID)
		}()
		target, err := withDatabase(adminURL, alloc.Fresh)
		if err != nil {
			return nil, err
		}
		if err := ApplyCommand(bin, target)(ctx, r); err != nil {
			return nil, err
		}
		return CountCommand(bin, target)(ctx)
	}
}

var roleName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// restoreOwner is the core login role in databaseURL when it differs from the admin
// user in adminURL, and "" when both are the same role.
func restoreOwner(adminURL, databaseURL string) (string, error) {
	admin, err := url.Parse(adminURL)
	if err != nil {
		return "", err
	}
	app, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	if app.User == nil || admin.User == nil || app.User.Username() == admin.User.Username() {
		return "", nil
	}
	name := app.User.Username()
	if !roleName.MatchString(name) {
		return "", fmt.Errorf("DATABASE_URL role %q: %w", name, ErrBadRequest)
	}
	return name, nil
}

func withDatabase(rawURL, name string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

// sealRestored runs ledgerseal.Script, the seal core migrate runs, as the admin on the
// loaded copy before the swap, so a core restarted on restored data finds its
// append-only tables owned by ledgerseal.Owner and does not refuse to start (PRD
// section 6). The dump loaded every object as owner, the core's role. A copy with no
// core.audit_log is not a core database and is left as loaded; an admin-only install
// (owner "") has no application role to seal against.
func sealRestored(ctx context.Context, bin, adminTarget, owner string) error {
	if owner == "" {
		return nil
	}
	found, err := psqlLines(ctx, bin, adminTarget, "SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'core' AND c.relname = 'audit_log'")
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return nil
	}
	if err := psqlExec(ctx, bin, adminTarget, ledgerseal.Script(owner)); err != nil {
		return fmt.Errorf("seal restored ledgers: %w", err)
	}
	return nil
}

func psqlLines(ctx context.Context, bin, databaseURL, sql string) ([]string, error) {
	ctx, cancel := boundDocker(ctx, databaseIOTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--tuples-only", "--no-align", "--dbname", databaseURL, "--command", sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("psql: %w: %s", err, Scrub(stderr.String()))
	}
	var lines []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func safeTable(name string) bool {
	schema, table, ok := strings.Cut(name, ".")
	if !ok || strings.Contains(table, ".") {
		return false
	}
	return ident(schema) && ident(table)
}

func ident(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, c := range s {
		ok := c >= 'a' && c <= 'z' || c == '_' || (i > 0 && c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

func quoteTable(name string) (string, error) {
	if !safeTable(name) {
		return "", ErrBadRequest
	}
	schema, table, _ := strings.Cut(name, ".")
	return `"` + schema + `"."` + table + `"`, nil
}
