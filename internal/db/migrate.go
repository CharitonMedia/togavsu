package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// CoreOwner is the schema_migrations owner for core migrations.
const CoreOwner = "core"

var (
	// ErrChecksum means a migration that already ran was edited.
	ErrChecksum = errors.New("db: migration checksum changed")
	// ErrVersion means a migration version is missing, duplicated, or outside its range.
	ErrVersion = errors.New("db: migration version")
)

// Migration is one numbered SQL file.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

// ApplySpec selects which migrations to run.
type ApplySpec struct {
	Owner      string
	Schema     string
	Dialect    string
	Migrations []Migration
}

// ponytail: checksum ledger is schema_migrations rather than goose_db_version. Upgrade path: adopt goose when one table can store owner and checksum. trigger: sqlc and pgx land and the lead asks for the goose CLI.
//
// ponytail: Apply is exercised on SQLite in unit tests. Upgrade path: run the same Apply against Postgres 17. trigger: platform Compose publishes a Postgres service to CI.

var migrationName = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.sql$`)
var identName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const schemaMigrationsSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  owner TEXT NOT NULL,
  version INTEGER NOT NULL,
  checksum TEXT NOT NULL,
  applied_at BIGINT NOT NULL,
  PRIMARY KEY (owner, version)
)`

// Embedded loads the SQL files shipped in this package.
func Embedded() ([]Migration, error) {
	return LoadFS(migrationFS)
}

// SchemaSQL returns the embedded migration text.
func SchemaSQL() (string, error) {
	ms, err := Embedded()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, m := range ms {
		b.WriteString(m.SQL)
		if !strings.HasSuffix(m.SQL, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

// LoadFS reads numbered SQL files from fsys.
func LoadFS(fsys fs.FS) ([]Migration, error) {
	var out []Migration
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(name, ".sql") {
			return err
		}
		base := path.Base(name)
		m := migrationName.FindStringSubmatch(base)
		if m == nil {
			return fmt.Errorf("%w: %s", ErrVersion, base)
		}
		version, err := strconv.Atoi(m[1])
		if err != nil || version < 1 {
			return fmt.Errorf("%w: %s", ErrVersion, base)
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{
			Version:  version,
			Name:     strings.TrimSuffix(base, ".sql"),
			SQL:      string(body),
			Checksum: hex.EncodeToString(sum[:]),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Rebind converts ? placeholders to $1 style for Postgres.
func Rebind(query, dialect string) string {
	if dialect != "postgres" {
		return query
	}
	var b strings.Builder
	n := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			n++
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

// Apply runs each migration once in its own transaction and records owner, version, checksum, and time.
func Apply(ctx context.Context, db *sql.DB, spec ApplySpec) error {
	if spec.Owner == "" {
		return fmt.Errorf("%w: owner is required", ErrVersion)
	}
	if spec.Schema != "" {
		if !identName.MatchString(spec.Schema) {
			return fmt.Errorf("%w: schema %q", ErrVersion, spec.Schema)
		}
		if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+spec.Schema); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "SET search_path TO "+spec.Schema); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, schemaMigrationsSQL); err != nil {
		return err
	}
	ms := append([]Migration(nil), spec.Migrations...)
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	seen := map[int]bool{}
	for _, m := range ms {
		if m.Version < 1 || seen[m.Version] {
			return fmt.Errorf("%w: %d", ErrVersion, m.Version)
		}
		seen[m.Version] = true
		if err := applyOne(ctx, db, spec, m); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(ctx context.Context, db *sql.DB, spec ApplySpec, m Migration) error {
	sum := m.Checksum
	if sum == "" {
		raw := sha256.Sum256([]byte(m.SQL))
		sum = hex.EncodeToString(raw[:])
	}
	var existing string
	err := db.QueryRowContext(ctx, Rebind(`SELECT checksum FROM schema_migrations WHERE owner = ? AND version = ?`, spec.Dialect), spec.Owner, m.Version).Scan(&existing)
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return err
	case existing != sum:
		return fmt.Errorf("%w: %s version %d", ErrChecksum, spec.Owner, m.Version)
	default:
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range statements(m.SQL) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration %d: %w", m.Version, err)
		}
	}
	if _, err := tx.ExecContext(ctx, Rebind(`INSERT INTO schema_migrations (owner, version, checksum, applied_at) VALUES (?, ?, ?, ?)`, spec.Dialect), spec.Owner, m.Version, sum, time.Now().UTC().UnixMicro()); err != nil {
		return err
	}
	return tx.Commit()
}

func statements(sqlText string) []string {
	parts := strings.Split(sqlText, ";")
	var out []string
	for _, part := range parts {
		if strings.TrimSpace(stripLineComments(part)) == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func stripLineComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
