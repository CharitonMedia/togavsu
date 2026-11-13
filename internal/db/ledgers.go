package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/ledgerseal"
)

// LedgerOwner is the NOLOGIN role that owns the append-only tables once they are sealed.
// Nobody logs in as it and the application role is never a member, so the application
// role holds only the SELECT and INSERT the owner gave it: it cannot UPDATE, DELETE or
// TRUNCATE a row, GRANT those back to itself, or ALTER or DROP the table. No trigger is
// involved (PRD section 6).
const LedgerOwner = ledgerseal.Owner

// AppendOnlyTables are the tables in schema core that SealLedgers hands to LedgerOwner
// (PRD section 5: append-only tables).
var AppendOnlyTables = ledgerseal.Tables

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
// on id (ledgerseal.Script, which the supervisor's restore runs too). admin must be a
// superuser connection to the core's database. It fails if app can act as LedgerOwner
// through any membership. It is safe to run again.
func SealLedgers(ctx context.Context, admin *sql.DB, app string) error {
	if err := needSuper(ctx, admin); err != nil {
		return err
	}
	if _, err := admin.ExecContext(ctx, ledgerseal.Script(app)); err != nil {
		return fmt.Errorf("db: seal ledgers: %w", err)
	}
	return nil
}

// ErrUnsealed is returned by VerifySealed when the application role could change or
// alter an append-only table.
var ErrUnsealed = errors.New("db: the append-only tables are not sealed")

// VerifySealed checks, on the application role's own connection, that schema core and
// every append-only table exist and belong to LedgerOwner, that the role is not a
// member of LedgerOwner, so it cannot ALTER or DROP them, and that it holds no UPDATE,
// DELETE or TRUNCATE on them. core migrate runs it after sealing and the serving core
// refuses to start without it.
func VerifySealed(ctx context.Context, app interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) error {
	var problems []string
	var exists bool
	if err := app.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, LedgerOwner).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: role %s does not exist", ErrUnsealed, LedgerOwner)
	}
	var schemaOwner string
	var member bool
	err := app.QueryRowContext(ctx, `SELECT pg_get_userbyid(nspowner), pg_has_role(current_user, $1, 'MEMBER')
		FROM pg_namespace WHERE nspname = 'core'`, LedgerOwner).Scan(&schemaOwner, &member)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: schema core is missing", ErrUnsealed)
	}
	if err != nil {
		return err
	}
	if schemaOwner != LedgerOwner {
		problems = append(problems, "schema core is owned by "+schemaOwner)
	}
	if member {
		problems = append(problems, "the application role is a member of "+LedgerOwner)
	}
	for _, t := range AppendOnlyTables {
		var owner string
		var owns, upd, del, trunc bool
		err := app.QueryRowContext(ctx, `SELECT pg_get_userbyid(c.relowner), pg_has_role(current_user, c.relowner, 'USAGE'),
			has_table_privilege(c.oid, 'UPDATE'), has_table_privilege(c.oid, 'DELETE'), has_table_privilege(c.oid, 'TRUNCATE')
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'core' AND c.relname = $1 AND c.relkind IN ('r', 'p')`, t).Scan(&owner, &owns, &upd, &del, &trunc)
		if errors.Is(err, sql.ErrNoRows) {
			problems = append(problems, "core."+t+" is missing")
			continue
		}
		if err != nil {
			return err
		}
		if owner != LedgerOwner {
			problems = append(problems, "core."+t+" is owned by "+owner)
		}
		if owns {
			problems = append(problems, "the application role can alter core."+t)
		}
		if upd || del || trunc {
			problems = append(problems, "the application role can update, delete or truncate core."+t)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrUnsealed, strings.Join(problems, "; "))
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

func quoteIdent(s string) string { return ledgerseal.QuoteIdent(s) }
