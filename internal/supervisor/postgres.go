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
	"os"
	"os/exec"
	"strings"
	"time"
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
	tmp, err := os.CreateTemp("", "togavsu-restore-*.sql")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, dump); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	suffix := hex.EncodeToString(raw[:])
	fresh := "togavsu_restore_" + suffix
	if _, err := psqlLines(ctx, bin, adminURL, "CREATE DATABASE "+fresh); err != nil {
		return err
	}
	dropFresh := true
	defer func() {
		if dropFresh {
			_, _ = psqlLines(context.Background(), bin, adminURL, "DROP DATABASE IF EXISTS "+fresh)
		}
	}()
	target, err := withDatabase(adminURL, fresh)
	if err != nil {
		return err
	}
	f, err := os.Open(tmp.Name())
	if err != nil {
		return err
	}
	err = ApplyCommand(bin, target)(ctx, f)
	f.Close()
	if err != nil {
		return err
	}
	got, err := CountCommand(bin, target)(ctx)
	if err != nil {
		return err
	}
	if err := compareCounts(expect, got); err != nil {
		return err
	}
	if err := swapDatabase(ctx, bin, adminURL, orig, fresh, suffix); err != nil {
		// The fresh name still holds the loaded copy when the swap did not
		// commit. After a commit, or when the catalog could not be read, leave
		// that database alone so a cleanup drop cannot remove the only copy.
		if errors.Is(err, ErrRestoreCleanup) || errors.Is(err, errSwapUnobserved) || errors.Is(err, errConfiguredDatabaseAbsent) {
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

const swapRecoveryTimeout = 30 * time.Second

type swapHookFunc func(configured, fresh, replaced string) error

type swapHookKey struct{}

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

func recoveryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), swapRecoveryTimeout)
}

// PostgresCatalog reads pg_database through the admin connection. It does not
// rename or drop databases.
type PostgresCatalog struct {
	Bin      string
	AdminURL string
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
	_, _ = psqlLines(rec, bin, adminURL, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '"+orig+"' AND pid <> pg_backend_pid()")
	txn := "BEGIN; ALTER DATABASE " + orig + " RENAME TO " + replaced + "; ALTER DATABASE " + fresh + " RENAME TO " + orig + "; COMMIT;"
	txnErr := psqlExec(ctx, bin, adminURL, txn)
	if txnErr != nil {
		return reconcileSwap(bin, adminURL, orig, fresh, replaced, txnErr)
	}
	dropErr := psqlExec(ctx, bin, adminURL, "DROP DATABASE "+replaced)
	if dropErr != nil {
		return reconcileSwap(bin, adminURL, orig, fresh, replaced, dropErr)
	}
	return reconcileSwap(bin, adminURL, orig, fresh, replaced, nil)
}

func reconcileSwap(bin, adminURL, orig, fresh, replaced string, stepErr error) error {
	ctx, cancel := recoveryContext()
	defer cancel()
	last := stepErr
	for attempt := 0; attempt < 4; attempt++ {
		st, err := observeDatabases(ctx, bin, adminURL, orig, fresh, replaced)
		if err != nil {
			last = fmt.Errorf("%w: %v", errSwapUnobserved, err)
			continue
		}
		switch {
		case st[orig] && !st[fresh] && !st[replaced]:
			// The pair committed and the previous database is gone, including
			// when the final acknowledgment was lost.
			return nil
		case st[orig] && !st[fresh] && st[replaced]:
			if err := dropDatabase(ctx, bin, adminURL, replaced); err != nil {
				last = err
				continue
			}
			return nil
		case st[orig] && st[fresh] && !st[replaced]:
			// Nothing from the pair is visible. The transaction rolled back
			// or never started. The configured name still has its writes.
			if stepErr != nil {
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
	if stepErr != nil && st[fresh] {
		return stepErr
	}
	return nil
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
		tmp, err := os.CreateTemp("", "togavsu-measure-*.sql")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, r); err != nil {
			tmp.Close()
			return nil, err
		}
		if err := tmp.Close(); err != nil {
			return nil, err
		}
		var raw [6]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		name := "togavsu_check_" + hex.EncodeToString(raw[:])
		if _, err := psqlLines(ctx, bin, adminURL, "CREATE DATABASE "+name); err != nil {
			return nil, err
		}
		defer psqlLines(context.Background(), bin, adminURL, "DROP DATABASE "+name)
		target, err := withDatabase(adminURL, name)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(tmp.Name())
		if err != nil {
			return nil, err
		}
		err = ApplyCommand(bin, target)(ctx, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		return CountCommand(bin, target)(ctx)
	}
}

func withDatabase(rawURL, name string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

func psqlLines(ctx context.Context, bin, databaseURL, sql string) ([]string, error) {
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
