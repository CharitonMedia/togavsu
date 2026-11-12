package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LedgerOwner is the NOLOGIN role that owns the append-only tables once they are sealed.
// Nobody logs in as it and the application role is never a member, so the application
// role holds only the SELECT and INSERT the owner gave it: it cannot UPDATE, DELETE or
// TRUNCATE a row, GRANT those back to itself, or ALTER or DROP the table. No trigger is
// involved (PRD section 6).
const LedgerOwner = "togavsu_ledger_owner"

// AppendOnlyTables are the tables in schema core that SealLedgers hands to LedgerOwner
// (PRD section 5: append-only tables).
var AppendOnlyTables = []string{"audit_log", "trust_ledger", "events", "ai_call_log", "file_versions", "file_renditions"}

// ledgerWriters are the NOINHERIT writer roles the core switches to for one statement.
// The sealed owner grants them SELECT and INSERT directly, since the application role
// holds no grant option.
var ledgerWriters = map[string]string{"audit_log": "togavsu_audit_writer", "ai_call_log": "togavsu_ai_log_writer"}

// ErrNotSuperuser is returned when a seal or unseal connection is not a superuser.
var ErrNotSuperuser = errors.New("db: ledger ownership needs a superuser connection")

// UnsealLedgers hands every sealed append-only table back to app so app's migrations
// can ALTER it (expand-then-contract). admin must be a superuser connection to the
// core's database. SealLedgers must run after the migrations; MigrateSealed does both.
func UnsealLedgers(ctx context.Context, admin *sql.DB, app string) error {
	if err := needSuper(ctx, admin); err != nil {
		return err
	}
	for _, t := range AppendOnlyTables {
		owner, ok, err := tableOwner(ctx, admin, t)
		if err != nil {
			return err
		}
		if !ok || owner != LedgerOwner {
			continue
		}
		if _, err := admin.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE core.%s OWNER TO %s`, t, quoteIdent(app))); err != nil {
			return err
		}
	}
	return nil
}

// SealLedgers makes LedgerOwner own schema core and every append-only table that exists
// and leaves app with SELECT and INSERT on them, without grant option, plus REFERENCES
// on id. admin must be a superuser
// connection to the core's database. It fails if app can act as LedgerOwner through any
// membership. It is safe to run again.
func SealLedgers(ctx context.Context, admin *sql.DB, app string) error {
	if err := needSuper(ctx, admin); err != nil {
		return err
	}
	stmts := []string{`DO $body$ BEGIN
	  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + LedgerOwner + `') THEN
	    CREATE ROLE ` + LedgerOwner + ` NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
	  END IF;
	END $body$`,
		`ALTER ROLE ` + LedgerOwner + ` NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`,
		`REVOKE ` + LedgerOwner + ` FROM ` + quoteIdent(app),
		// A schema's owner may DROP any table in it, so schema core moves to the ledger
		// owner as well. The application role keeps USAGE (with grant option, for the
		// module logins it provisions) and CREATE, so it still creates and owns every
		// other core table and its migrations still run.
		`ALTER SCHEMA core OWNER TO ` + LedgerOwner,
		`GRANT USAGE ON SCHEMA core TO ` + quoteIdent(app) + ` WITH GRANT OPTION`,
		`GRANT CREATE ON SCHEMA core TO ` + quoteIdent(app),
	}
	for _, w := range ledgerWriters {
		stmts = append(stmts, `DO $body$ BEGIN
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+w+`') THEN
		    CREATE ROLE `+w+` NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE;
		  END IF;
		END $body$`, `GRANT USAGE ON SCHEMA core TO `+w)
	}
	for _, s := range stmts {
		if _, err := admin.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	var member bool
	if err := admin.QueryRowContext(ctx, `SELECT pg_has_role($1, $2, 'MEMBER')`, app, LedgerOwner).Scan(&member); err != nil {
		return err
	}
	if member {
		return fmt.Errorf("db: %s is a member of %s through another role", app, LedgerOwner)
	}
	for _, t := range AppendOnlyTables {
		if _, ok, err := tableOwner(ctx, admin, t); err != nil {
			return err
		} else if !ok {
			continue
		}
		q := "core." + t
		ts := []string{
			`ALTER TABLE ` + q + ` OWNER TO ` + LedgerOwner,
			`REVOKE ALL ON TABLE ` + q + ` FROM PUBLIC, ` + quoteIdent(app),
			`GRANT SELECT, INSERT ON TABLE ` + q + ` TO ` + quoteIdent(app),
			// REFERENCES on id alone lets a module schema or a later table point a
			// foreign key at a ledger row (modules.Schemas grants it on); it changes no row.
			`GRANT REFERENCES (id) ON TABLE ` + q + ` TO ` + quoteIdent(app) + ` WITH GRANT OPTION`,
		}
		if w, ok := ledgerWriters[t]; ok {
			ts = append(ts, `REVOKE ALL ON TABLE `+q+` FROM `+w, `GRANT SELECT, INSERT ON TABLE `+q+` TO `+w)
		}
		for _, s := range ts {
			if _, err := admin.ExecContext(ctx, s); err != nil {
				return fmt.Errorf("db: seal %s: %w", t, err)
			}
		}
	}
	return nil
}

// OwnsTable reports whether the current role owns core.table, or can act as its owner.
// A missing table reports false. The privilege installers skip a sealed table.
func OwnsTable(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, table string) (bool, error) {
	var owns bool
	err := q.QueryRowContext(ctx, `SELECT pg_has_role(current_user, c.relowner, 'USAGE')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'core' AND c.relname = $1`, table).Scan(&owns)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return owns, err
}

func needSuper(ctx context.Context, q *sql.DB) error {
	var super bool
	if err := q.QueryRowContext(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		return err
	}
	if !super {
		return ErrNotSuperuser
	}
	return nil
}

func tableOwner(ctx context.Context, q *sql.DB, table string) (string, bool, error) {
	var owner string
	err := q.QueryRowContext(ctx, `SELECT pg_get_userbyid(c.relowner) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'core' AND c.relname = $1 AND c.relkind IN ('r', 'p')`, table).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return owner, err == nil, err
}

func quoteIdent(s string) string {
	out := []byte{'"'}
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			out = append(out, '"')
		}
		out = append(out, s[i])
	}
	return string(append(out, '"'))
}
