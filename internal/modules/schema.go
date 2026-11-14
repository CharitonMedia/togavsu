package modules

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schemas owns module schemas, their database logins and their migrations (PRD 9.9).
// DB is a pool with no search_path restriction; core tables are named core.<table>.
type Schemas struct {
	DB  *sql.DB
	Now func() time.Time
}

var (
	// ErrChangedMigration is a migration that already ran and whose text changed.
	ErrChangedMigration = errors.New("modules: a migration that already ran was changed")
	// ErrMissingMigration is a package that drops a migration that already ran.
	ErrMissingMigration = errors.New("modules: a migration that already ran is missing from the package")
	// ErrMigrationFailed is a migration that failed and rolled back.
	ErrMigrationFailed = errors.New("modules: migration failed and rolled back")
)

// Owner is the schema_migrations owner for a module's migrations.
func Owner(module string) string { return SchemaFor(module) }

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func quoteLit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// RoleName is the module's database login. Roles are cluster-wide, so the name carries a
// tag of the database too; one firm's database is one install.
func (s *Schemas) RoleName(ctx context.Context, module string) (string, error) {
	return s.roleForSchema(ctx, SchemaFor(module))
}

func (s *Schemas) roleForSchema(ctx context.Context, schema string) (string, error) {
	var name string
	if err := s.DB.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(name))
	return schema + "_" + hex.EncodeToString(sum[:limits().RoleTagBytes]), nil
}

// AsModule runs fn in a read-only transaction as the module's own role. A module owns
// its schema and can replace a table with a view or add functions and triggers through
// its login at any time; whatever it put there runs with the module's privileges when the
// core reads it (include=, views, holders), never with the core's.
func (s *Schemas) AsModule(ctx context.Context, schema string, fn func(tx *sql.Tx) error) error {
	role, err := s.roleForSchema(ctx, schema)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+quoteIdent(role)); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func randomSecret() (string, error) {
	b := make([]byte, limits().SecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Provision creates (or unlocks) the module's schema and login. The login owns only its
// schema, may reference core record ids for foreign keys, and has no other grant, so it
// can't read core tables or another module's schema. It returns the role and its new
// password, which is handed to the module as a Docker secret.
func (s *Schemas) Provision(ctx context.Context, module string) (role, password string, err error) {
	role, err = s.RoleName(ctx, module)
	if err != nil {
		return "", "", err
	}
	password, err = randomSecret()
	if err != nil {
		return "", "", err
	}
	schema := SchemaFor(module)
	var exists bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return "", "", err
	}
	stmts := []string{}
	if !exists {
		stmts = append(stmts, `CREATE ROLE `+quoteIdent(role)+` LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD `+quoteLit(password))
	} else {
		stmts = append(stmts, `ALTER ROLE `+quoteIdent(role)+` LOGIN PASSWORD `+quoteLit(password))
	}
	stmts = append(stmts,
		`GRANT `+quoteIdent(role)+` TO CURRENT_USER`,
		`CREATE SCHEMA IF NOT EXISTS `+quoteIdent(schema)+` AUTHORIZATION `+quoteIdent(role),
		`ALTER SCHEMA `+quoteIdent(schema)+` OWNER TO `+quoteIdent(role),
		`REVOKE ALL ON SCHEMA `+quoteIdent(schema)+` FROM PUBLIC`,
		`ALTER ROLE `+quoteIdent(role)+` SET search_path TO `+quoteIdent(schema),
		`GRANT USAGE ON SCHEMA core TO `+quoteIdent(role),
	)
	tables := make([]string, 0, len(coreRecords))
	for _, t := range coreRecords {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		// Column-level REFERENCES on id only: enough for a foreign key, no read.
		stmts = append(stmts, `GRANT REFERENCES (id) ON core.`+quoteIdent(t)+` TO `+quoteIdent(role))
	}
	for _, st := range stmts {
		if _, err := s.DB.ExecContext(ctx, st); err != nil {
			return "", "", fmt.Errorf("modules: provision %s: %w", module, err)
		}
	}
	return role, password, nil
}

// SchemaExists reports whether the module's schema is present (kept data).
func (s *Schemas) SchemaExists(ctx context.Context, module string) (bool, error) {
	var ok bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, SchemaFor(module)).Scan(&ok)
	return ok, err
}

// Applied returns the module's recorded migrations: version to checksum.
func (s *Schemas) Applied(ctx context.Context, module string) (map[int]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT version, checksum FROM core.schema_migrations WHERE owner = $1 AND version > 0`, Owner(module))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, err
		}
		out[v] = sum
	}
	return out, rows.Err()
}

// CheckHistory refuses a package whose migrations rewrite or drop one that already ran
// (PRD 6, 9.9). It runs before anything is applied.
func (s *Schemas) CheckHistory(ctx context.Context, module string, ms []db.Migration) error {
	applied, err := s.Applied(ctx, module)
	if err != nil {
		return err
	}
	byVersion := map[int]db.Migration{}
	for _, m := range ms {
		byVersion[m.Version] = m
	}
	versions := make([]int, 0, len(applied))
	for v := range applied {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	for _, v := range versions {
		m, ok := byVersion[v]
		if !ok {
			return fmt.Errorf("%w: version %d", ErrMissingMigration, v)
		}
		if m.Checksum != applied[v] {
			return fmt.Errorf("%w: %s", ErrChangedMigration, m.Name)
		}
	}
	return nil
}

// upSection drops a goose Down section, so only the Up statements run.
func upSection(sqlText string) string {
	var out []string
	for _, line := range strings.Split(sqlText, "\n") {
		if strings.EqualFold(strings.TrimSpace(strings.ReplaceAll(line, "--", "")), "+goose down") {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Migrate applies every migration not yet recorded, in order, each in its own
// transaction, as the module's role with search_path set to its schema. A failure rolls
// that migration back and stops; earlier migrations of the same run stay applied, which
// expand-then-contract keeps compatible with the running version (PRD 9.9).
func (s *Schemas) Migrate(ctx context.Context, module, role string, ms []db.Migration) (int, error) {
	if err := s.CheckHistory(ctx, module, ms); err != nil {
		return 0, err
	}
	applied, err := s.Applied(ctx, module)
	if err != nil {
		return 0, err
	}
	ran := zero
	for _, m := range ms {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		if err := s.applyOne(ctx, module, role, m); err != nil {
			return ran, err
		}
		ran++
	}
	return ran, nil
}

func (s *Schemas) applyOne(ctx context.Context, module, role string, m db.Migration) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	sum := sha256.Sum256([]byte("togavsu-module-migrations:" + module))
	lockKey := int64(binary.BigEndian.Uint64(sum[:]))
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return err
	}
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM core.schema_migrations WHERE owner = $1 AND version = $2)`, Owner(module), m.Version).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+quoteIdent(role)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path TO `+quoteIdent(SchemaFor(module))); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, upSection(m.SQL)); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrMigrationFailed, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx, `RESET ROLE`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path TO core`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO core.schema_migrations (owner, version, checksum, applied_at) VALUES ($1, $2, $3, $4)`,
		Owner(module), m.Version, m.Checksum, s.now().UnixMicro()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Schemas) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Lock turns off the module's login and ends its sessions; the schema stays, so a
// reinstall picks it up (uninstall with Keep data).
func (s *Schemas) Lock(ctx context.Context, module string) error {
	role, err := s.RoleName(ctx, module)
	if err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `ALTER ROLE `+quoteIdent(role)+` NOLOGIN`); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, role)
	return err
}

// Unlock turns the module's login back on with its current password, undoing Lock
// when a Delete data backup fails.
func (s *Schemas) Unlock(ctx context.Context, module string) error {
	role, err := s.RoleName(ctx, module)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `ALTER ROLE `+quoteIdent(role)+` LOGIN`)
	return err
}

// Drop deletes the module's schema, its migration records and its login. The caller has
// already confirmed and backed up (Service.Uninstall).
func (s *Schemas) Drop(ctx context.Context, module string) error {
	role, err := s.RoleName(ctx, module)
	if err != nil {
		return err
	}
	if err := s.Lock(ctx, module); err != nil && !isUndefinedRole(err) {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, st := range []string{
		`DROP SCHEMA IF EXISTS ` + quoteIdent(SchemaFor(module)) + ` CASCADE`,
	} {
		if _, err := tx.ExecContext(ctx, st); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM core.schema_migrations WHERE owner = $1`, Owner(module)); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return err
	}
	if exists {
		for _, st := range []string{`DROP OWNED BY ` + quoteIdent(role), `DROP ROLE ` + quoteIdent(role)} {
			if _, err := tx.ExecContext(ctx, st); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func isUndefinedRole(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "42704"
}

// coreLink is one module column that points at a core table's id.
type coreLink struct {
	Schema, Table, Column string
	PrimaryKey            bool
}

func (s *Schemas) links(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, coreTable string) ([]coreLink, error) {
	rows, err := q.QueryContext(ctx, `
SELECT n.nspname, c.relname, a.attname,
       EXISTS (SELECT 1 FROM pg_constraint pk WHERE pk.conrelid = c.oid AND pk.contype = 'p' AND pk.conkey = con.conkey)
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_class t ON t.oid = con.confrelid
JOIN pg_namespace tn ON tn.oid = t.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = con.conkey[1]
WHERE con.contype = 'f' AND tn.nspname = 'core' AND t.relname = $1 AND n.nspname LIKE 'mod\_%'
ORDER BY n.nspname, c.relname, a.attname`, coreTable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []coreLink
	for rows.Next() {
		var l coreLink
		if err := rows.Scan(&l.Schema, &l.Table, &l.Column, &l.PrimaryKey); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// moduleForSchema maps mod_<id> back to the installed module id.
func (s *Schemas) moduleForSchema(ctx context.Context, schema string) string {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM core.modules`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil && SchemaFor(id) == schema {
				return id
			}
		}
	}
	return strings.TrimPrefix(schema, "mod_")
}

// Holders names the modules whose rows point at core record id in coreTable. A core
// record they point to can't be hard-deleted (ON DELETE RESTRICT); the core shows these
// names (PRD 9.9).
func (s *Schemas) Holders(ctx context.Context, coreTable string, id domain.ID) ([]string, error) {
	links, err := s.links(ctx, s.DB, coreTable)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range links {
		var hit bool
		q := `SELECT EXISTS (SELECT 1 FROM ` + quoteIdent(l.Schema) + `.` + quoteIdent(l.Table) + ` WHERE ` + quoteIdent(l.Column) + ` = $1)`
		if err := s.AsModule(ctx, l.Schema, func(tx *sql.Tx) error { return tx.QueryRowContext(ctx, q, string(id)).Scan(&hit) }); err != nil {
			return nil, err
		}
		if hit {
			mod := s.moduleForSchema(ctx, l.Schema)
			if !seen[mod] {
				seen[mod] = true
				out = append(out, mod)
			}
		}
	}
	return out, nil
}

// DeleteBlocked turns the foreign-key error Postgres raises when a core record with
// module rows is deleted into a conflict that names the module.
func (s *Schemas) DeleteBlocked(ctx context.Context, err error) (error, bool) {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23503" {
		return err, false
	}
	var schema string
	qerr := s.DB.QueryRowContext(ctx, `SELECT n.nspname FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace WHERE con.conname = $1 AND n.nspname LIKE 'mod\_%' LIMIT 1`, pg.ConstraintName).Scan(&schema)
	if qerr != nil {
		return err, false
	}
	return domain.Conflict("module " + s.moduleForSchema(ctx, schema) + " has rows linked to this record, so it can't be deleted"), true
}

// Relinked is how many rows of one module a Relink moved.
type Relinked struct {
	Module string
	Rows   int
}

// Relink moves module rows from one core record to another in the caller's transaction,
// as a core merge does (PRD 9.9), and reports the rows it moved per module, in module
// order. An extension row keyed by the record can't be moved onto a record that already
// has one; the merge is refused naming the module.
func (s *Schemas) Relink(ctx context.Context, tx *sql.Tx, coreTable string, from, to domain.ID) ([]Relinked, error) {
	links, err := s.links(ctx, tx, coreTable)
	if err != nil {
		return nil, err
	}
	var out []Relinked
	for _, l := range links {
		// The module's rows are changed as the module's role, so a trigger it added runs
		// with its privileges; RESET ROLE returns the transaction to the core after.
		role, err := s.roleForSchema(ctx, l.Schema)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+quoteIdent(role)); err != nil {
			return nil, err
		}
		target := quoteIdent(l.Schema) + `.` + quoteIdent(l.Table)
		if l.PrimaryKey {
			var both bool
			q := `SELECT EXISTS (SELECT 1 FROM ` + target + ` WHERE ` + quoteIdent(l.Column) + ` = $1) AND EXISTS (SELECT 1 FROM ` + target + ` WHERE ` + quoteIdent(l.Column) + ` = $2)`
			if err := tx.QueryRowContext(ctx, q, string(from), string(to)).Scan(&both); err != nil {
				return nil, err
			}
			if both {
				return nil, domain.Conflict("module " + s.moduleForSchema(ctx, l.Schema) + " has an extension row on both records; resolve it in the module before merging")
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE `+target+` SET `+quoteIdent(l.Column)+` = $1 WHERE `+quoteIdent(l.Column)+` = $2`, string(to), string(from))
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		mod := s.moduleForSchema(ctx, l.Schema)
		if len(out) > 0 && out[len(out)-1].Module == mod {
			out[len(out)-1].Rows += int(n)
		} else {
			out = append(out, Relinked{Module: mod, Rows: int(n)})
		}
		if _, err := tx.ExecContext(ctx, `RESET ROLE`); err != nil {
			return nil, err
		}
	}
	return out, nil
}
