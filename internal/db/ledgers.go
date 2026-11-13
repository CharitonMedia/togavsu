package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// ErrNotSuperuser is returned when a seal or migration admin connection is not a superuser.
var ErrNotSuperuser = errors.New("db: ledger ownership needs a superuser connection")

// ApplySealed applies spec's migrations as app without ever leaving an append-only table
// owned by app where another session could see it. admin must be a superuser
// connection to the core's database. It holds one admin session, switches it to app
// with SET ROLE, and runs the migrations there, so every object they create belongs to
// app as before. A migration whose SQL names an append-only table gets those tables
// handed to app at the start of its own transaction and sealed again (ledgerseal.Script)
// before that transaction commits. Postgres DDL is transactional, so other sessions
// only ever see the ledgers owned by LedgerOwner: a failed, cancelled or killed
// migration rolls the handoff back with everything else. A migration that names no
// append-only table runs as app with no handoff and takes no lock on the ledgers.
func ApplySealed(ctx context.Context, admin *sql.DB, app string, spec ApplySpec) (int, error) {
	if err := needSuper(ctx, admin); err != nil {
		return 0, err
	}
	conn, err := admin.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET ROLE `+quoteIdent(app)); err != nil {
		return 0, err
	}
	// The session goes back to the pool as the superuser it came out as.
	defer func() { _, _ = conn.ExecContext(context.Background(), `RESET ROLE`) }()
	sealed := spec
	sealed.Migrations = make([]Migration, len(spec.Migrations))
	for i, m := range spec.Migrations {
		if m.Checksum == "" {
			raw := sha256.Sum256([]byte(m.SQL))
			m.Checksum = hex.EncodeToString(raw[:])
		}
		m.SQL = wrapLedgerMigration(m.SQL, app)
		sealed.Migrations[i] = m
	}
	return ApplyCountConn(ctx, conn, sealed)
}

// ledgersNamed lists the append-only tables whose names appear in sqlText. Matching a
// substring may name a table the migration does not touch; that costs only a brief lock.
func ledgersNamed(sqlText string) []string {
	lower := strings.ToLower(sqlText)
	var out []string
	for _, t := range AppendOnlyTables {
		if strings.Contains(lower, t) {
			out = append(out, t)
		}
	}
	return out
}

// wrapLedgerMigration puts the ledger handoff inside the Up section of a migration that
// names an append-only table: RESET ROLE to the superuser, hand the named tables that
// LedgerOwner owns to app, SET ROLE app for the migration's own statements, then RESET
// ROLE, run the seal and SET ROLE app again for goose's version row. Other migrations
// come back unchanged. The checksum is taken before wrapping.
func wrapLedgerMigration(sqlText, app string) string {
	named := ledgersNamed(sqlText)
	if len(named) == 0 {
		return sqlText
	}
	body := ensureGoose(sqlText)
	lines := strings.SplitAfter(body, "\n")
	up, down := -1, len(lines)
	for i, l := range lines {
		if up < 0 && strings.Contains(l, "+goose Up") {
			up = i
		} else if up >= 0 && strings.Contains(l, "+goose Down") {
			down = i
			break
		}
	}
	if up < 0 {
		return sqlText
	}
	a := quoteIdent(app)
	lit := "'" + strings.ReplaceAll(app, "'", "''") + "'"
	quoted := make([]string, len(named))
	for i, t := range named {
		quoted[i] = "'" + t + "'"
	}
	pre := "\nRESET ROLE;\n-- +goose StatementBegin\nDO $unseal$ DECLARE t text; BEGIN\n" +
		"  FOREACH t IN ARRAY ARRAY[" + strings.Join(quoted, ", ") + "] LOOP\n" +
		"    IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace\n" +
		"               WHERE n.nspname = 'core' AND c.relname = t AND c.relkind IN ('r', 'p')\n" +
		"                 AND pg_get_userbyid(c.relowner) = '" + LedgerOwner + "') THEN\n" +
		"      EXECUTE format('ALTER TABLE core.%I OWNER TO %I', t, " + lit + ");\n" +
		"    END IF;\n  END LOOP;\nEND $unseal$;\n-- +goose StatementEnd\nSET ROLE " + a + ";\n"
	post := "\nRESET ROLE;\n-- +goose StatementBegin\n" + ledgerseal.Script(app) + "-- +goose StatementEnd\nSET ROLE " + a + ";\n"
	var b strings.Builder
	for i, l := range lines {
		if i == down {
			b.WriteString(post)
		}
		b.WriteString(l)
		if i == up {
			if !strings.HasSuffix(l, "\n") {
				b.WriteString("\n")
			}
			b.WriteString(pre)
		}
	}
	if down == len(lines) {
		b.WriteString(post)
	}
	return b.String()
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
