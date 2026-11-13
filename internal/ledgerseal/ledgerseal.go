// Package ledgerseal is the one SQL script that seals the append-only tables (PRD
// section 6). core migrate (db.SealLedgers) and the supervisor's restore both run it
// as the Postgres superuser, so a restored database comes back sealed before any core
// starts on it. It has no imports so the supervisor image can build it on its own.
package ledgerseal

import "strings"

// Owner is the NOLOGIN role that owns schema core and the append-only tables once
// they are sealed. Nobody logs in as it and the application role is never a member.
const Owner = "togavsu_ledger_owner"

// Tables are the append-only tables in schema core (PRD section 5).
var Tables = []string{"audit_log", "trust_ledger", "events", "ai_call_log", "file_versions", "file_renditions"}

// Writers are the NOINHERIT writer roles the core switches to for one statement.
// The owner grants them SELECT and INSERT directly, since the application role holds
// no grant option.
var Writers = map[string]string{"audit_log": "togavsu_audit_writer", "ai_call_log": "togavsu_ai_log_writer"}

// writerOrder keeps the script stable.
var writerOrder = []string{"audit_log", "ai_call_log"}

// Script returns the seal as one SQL script for a superuser session on the core's
// database. It creates Owner and the writer roles when missing, takes Owner away from
// app, hands schema core and every append-only table that exists to Owner, and leaves
// app SELECT and INSERT without grant option plus REFERENCES on id. It is safe to run
// again. app must already be a valid role name; it is quoted here.
func Script(app string) string {
	a := QuoteIdent(app)
	var b strings.Builder
	b.WriteString(`DO $seal$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + Owner + `') THEN
    CREATE ROLE ` + Owner + ` NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
  END IF;
END $seal$;
ALTER ROLE ` + Owner + ` NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
REVOKE ` + Owner + ` FROM ` + a + `;
-- A schema's owner may DROP any table in it, so schema core moves to the ledger owner.
-- The application role keeps USAGE (with grant option, for the module logins it
-- provisions) and CREATE, so it still creates and owns every other core table.
ALTER SCHEMA core OWNER TO ` + Owner + `;
GRANT USAGE ON SCHEMA core TO ` + a + ` WITH GRANT OPTION;
GRANT CREATE ON SCHEMA core TO ` + a + `;
`)
	for _, t := range writerOrder {
		w := Writers[t]
		b.WriteString(`DO $seal$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + w + `') THEN
    CREATE ROLE ` + w + ` NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE;
  END IF;
END $seal$;
GRANT USAGE ON SCHEMA core TO ` + w + `;
`)
	}
	for _, t := range Tables {
		q := "core." + t
		b.WriteString(`DO $seal$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE n.nspname = 'core' AND c.relname = '` + t + `' AND c.relkind IN ('r', 'p')) THEN
    ALTER TABLE ` + q + ` OWNER TO ` + Owner + `;
    REVOKE ALL ON TABLE ` + q + ` FROM PUBLIC, ` + a + `;
    GRANT SELECT, INSERT ON TABLE ` + q + ` TO ` + a + `;
    GRANT REFERENCES (id) ON TABLE ` + q + ` TO ` + a + ` WITH GRANT OPTION;
`)
		if w, ok := Writers[t]; ok {
			b.WriteString(`    REVOKE ALL ON TABLE ` + q + ` FROM ` + w + `;
    GRANT SELECT, INSERT ON TABLE ` + q + ` TO ` + w + `;
`)
		}
		b.WriteString(`  END IF;
END $seal$;
`)
	}
	// Fail the whole script if app can still act as the owner through another role.
	b.WriteString(`DO $seal$ BEGIN
  IF pg_has_role('` + strings.ReplaceAll(app, "'", "''") + `', '` + Owner + `', 'MEMBER') THEN
    RAISE EXCEPTION 'ledger seal: application role is a member of ` + Owner + `';
  END IF;
END $seal$;
`)
	return b.String()
}

// QuoteIdent double-quotes a Postgres identifier.
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
