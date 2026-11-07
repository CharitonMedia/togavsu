package supervisor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
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
// only then renames it over databaseURL. An error leaves the original database
// in place, including tables and schemas that appeared after the backup.
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
		return err
	}
	dropFresh = false
	return nil
}

func swapDatabase(ctx context.Context, bin, adminURL, orig, fresh, suffix string) error {
	replaced := orig + "_replaced_" + suffix
	_, _ = psqlLines(ctx, bin, adminURL, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '"+orig+"' AND pid <> pg_backend_pid()")
	if _, err := psqlLines(ctx, bin, adminURL, "ALTER DATABASE "+orig+" RENAME TO "+replaced); err != nil {
		return err
	}
	if _, err := psqlLines(ctx, bin, adminURL, "ALTER DATABASE "+fresh+" RENAME TO "+orig); err != nil {
		_, _ = psqlLines(context.Background(), bin, adminURL, "ALTER DATABASE "+replaced+" RENAME TO "+orig)
		return err
	}
	if _, err := psqlLines(ctx, bin, adminURL, "DROP DATABASE "+replaced); err != nil {
		return fmt.Errorf("%w: %s", ErrRestoreCleanup, Scrub(err.Error()))
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
