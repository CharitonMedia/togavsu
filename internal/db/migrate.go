package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
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
	"testing/fstest"

	"github.com/pressly/goose/v3"
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

// Apply runs migrations with goose on one connection. When Schema is set, that connection uses it
// for the whole run and RESET search_path before the connection returns to the pool.
func Apply(ctx context.Context, db *sql.DB, spec ApplySpec) error {
	if spec.Owner == "" {
		return fmt.Errorf("%w: owner is required", ErrVersion)
	}
	if spec.Schema != "" && !identName.MatchString(spec.Schema) {
		return fmt.Errorf("%w: schema %q", ErrVersion, spec.Schema)
	}
	if len(spec.Migrations) == 0 {
		return nil
	}
	seen := map[int]bool{}
	for _, m := range spec.Migrations {
		if m.Version < 1 || seen[m.Version] {
			return fmt.Errorf("%w: %d", ErrVersion, m.Version)
		}
		seen[m.Version] = true
	}
	files, sums, err := gooseFS(spec.Migrations)
	if err != nil {
		return err
	}
	store := &ledgerStore{owner: spec.Owner, dialect: spec.Dialect, checksums: sums}
	return migrateOnHeldConn(ctx, db, spec, store, files)
}

// migrationAfterSearchPath, when set, runs after search_path changes and before goose.
// Tests use it to force a preparation failure. Production leaves it nil.
var migrationAfterSearchPath func(context.Context, *sql.Conn) error

// migrateOnHeldConn checks one connection out of the caller's pool and does not put it
// back until search_path has been restored, including when preparation fails.
func migrateOnHeldConn(ctx context.Context, db *sql.DB, spec ApplySpec, store *ledgerStore, files fs.FS) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	changed := false
	defer func() {
		if changed {
			_, _ = conn.ExecContext(context.Background(), "RESET search_path")
		}
	}()
	if spec.Schema != "" {
		if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+spec.Schema); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "SET search_path TO "+spec.Schema); err != nil {
			return err
		}
		changed = true
		if migrationAfterSearchPath != nil {
			if err := migrationAfterSearchPath(ctx, conn); err != nil {
				return err
			}
		}
	}
	return conn.Raw(func(raw any) error {
		dc, ok := raw.(driver.Conn)
		if !ok {
			return fmt.Errorf("db: driver connection is %T", raw)
		}
		private := sql.OpenDB(&heldConnector{conn: dc})
		private.SetMaxOpenConns(1)
		private.SetMaxIdleConns(1)
		defer private.Close()
		if err := ensureLedger(ctx, private, store); err != nil {
			return err
		}
		provider, err := goose.NewProvider("", private, files,
			goose.WithStore(store),
			goose.WithSessionLocker(schemaSession{schema: spec.Schema}),
			goose.WithDisableGlobalRegistry(true),
		)
		if err != nil {
			return err
		}
		_, err = provider.Up(ctx)
		return err
	})
}

func ensureLedger(ctx context.Context, db *sql.DB, store *ledgerStore) error {
	exists, err := store.TableExists(ctx, db)
	if err != nil || exists {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := store.CreateVersionTable(ctx, tx); err != nil {
		return err
	}
	if err := store.ensureBaseline(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// heldConnector serves one already checked-out driver connection to a private pool.
type heldConnector struct {
	conn driver.Conn
}

func (c *heldConnector) Connect(context.Context) (driver.Conn, error) {
	return &heldConn{raw: c.conn}, nil
}

func (c *heldConnector) Driver() driver.Driver { return heldDriver{} }

type heldDriver struct{}

func (heldDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("db: migration driver is not opened by name")
}

// heldConn delegates to the checked-out session and refuses to close it.
// Close is a no-op so the private pool cannot return the session to the caller early.
type heldConn struct {
	raw driver.Conn
}

func (h *heldConn) Prepare(query string) (driver.Stmt, error) { return h.raw.Prepare(query) }
func (h *heldConn) Begin() (driver.Tx, error)                 { return h.raw.Begin() }
func (h *heldConn) Close() error                              { return nil }

func (h *heldConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := h.raw.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	return h.raw.Prepare(query)
}

func (h *heldConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := h.raw.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return h.raw.Begin()
}

func (h *heldConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := h.raw.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (h *heldConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := h.raw.(driver.QueryerContext); ok {
		return q.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (h *heldConn) Ping(ctx context.Context) error {
	if p, ok := h.raw.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (h *heldConn) CheckNamedValue(v *driver.NamedValue) error {
	if c, ok := h.raw.(driver.NamedValueChecker); ok {
		return c.CheckNamedValue(v)
	}
	return driver.ErrSkip
}

func gooseFS(ms []Migration) (fs.FS, map[int64]string, error) {
	files := fstest.MapFS{}
	sums := map[int64]string{}
	for _, m := range ms {
		body := m.SQL
		sum := m.Checksum
		if sum == "" {
			raw := sha256.Sum256([]byte(body))
			sum = hex.EncodeToString(raw[:])
		}
		sums[int64(m.Version)] = sum
		name := fmt.Sprintf("%05d_migration.sql", m.Version)
		files[name] = &fstest.MapFile{Data: []byte(ensureGoose(body))}
	}
	return files, sums, nil
}

func ensureGoose(sqlText string) string {
	if strings.Contains(sqlText, "+goose Up") {
		return sqlText
	}
	return "-- +goose Up\n" + sqlText + "\n-- +goose Down\nSELECT 1;\n"
}

// schemaSession pins search_path to the migration connection goose checks out, then resets it.
type schemaSession struct {
	schema string
}

func (s schemaSession) SessionLock(ctx context.Context, conn *sql.Conn) error {
	if s.schema == "" {
		return nil
	}
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+s.schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+s.schema); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}
	return nil
}

func (s schemaSession) SessionUnlock(ctx context.Context, conn *sql.Conn) error {
	if s.schema == "" {
		return nil
	}
	_, err := conn.ExecContext(context.WithoutCancel(ctx), "RESET search_path")
	return err
}
