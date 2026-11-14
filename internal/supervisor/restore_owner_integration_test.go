//go:build integration

package supervisor

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/ledgerseal"
)

// TestRestoreKeepsCoreRoleOwnership: when the core logs in as a role other than the
// admin, a restore leaves every restored object owned by that role, so the core can
// still write its tables and audit_log privileges can still be taken from it.
func TestRestoreKeepsCoreRoleOwnership(t *testing.T) {
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("TOGAVSU_TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	psql := os.Getenv("TOGAVSU_PSQL")
	admin, err := withDatabase(raw, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	const role, db, pass = "togavsu_ci_app", "togavsu_owner_ci", "synthetic-app-password"
	for _, stmt := range []string{
		"DROP DATABASE IF EXISTS " + db + " WITH (FORCE)",
		"DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '" + role + "') THEN CREATE ROLE " + role + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE; END IF; END $$",
		"ALTER ROLE " + role + " PASSWORD '" + pass + "'",
		"CREATE DATABASE " + db,
		"GRANT CONNECT, CREATE ON DATABASE " + db + " TO " + role,
	} {
		if err := psqlExec(ctx, psql, admin, stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	t.Cleanup(func() { _ = psqlExec(context.Background(), psql, admin, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") })
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, pass)
	u.Path = "/" + db
	app := u.String()
	seed := `
CREATE SCHEMA core;
CREATE TABLE core.synthetic_items (id integer PRIMARY KEY, name text);
INSERT INTO core.synthetic_items (id, name) VALUES (1, 'alpha'), (2, 'beta');
`
	if err := ApplyCommand(psql, app)(ctx, strings.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	store := &Store{Dir: t.TempDir(), Passphrase: "synthetic-passphrase", Dump: DumpCommand(os.Getenv("TOGAVSU_PG_DUMP"), app)}
	meta, err := store.Create(ctx, reasonPreUpdate)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Restore(ctx, meta.ID, "", func(ctx context.Context, payload RestorePayload) error {
		return RestoreIntoCleanDatabase(ctx, psql, admin, app, payload.Dump, payload.Manifest.RecordCounts)
	})
	if err != nil {
		t.Fatal(err)
	}
	owners, err := psqlLines(ctx, psql, app, "SELECT DISTINCT tableowner FROM pg_tables WHERE schemaname = 'core' UNION SELECT DISTINCT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'core'")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(owners, ",") != role {
		t.Fatalf("restored objects are owned by %v, want only %s", owners, role)
	}
	if err := psqlExec(ctx, psql, app, "INSERT INTO core.synthetic_items (id, name) VALUES (3, 'after restore')"); err != nil {
		t.Fatal("core role cannot write its restored table:", err)
	}
}

// TestRestoreSealsLedgers (PRD section 6, AC-006, AC-024): a restored core database
// comes back with its append-only tables sealed to ledgerseal.Owner before the swap,
// so the core role can insert and read them but cannot update, delete, alter or drop
// them, and its other tables stay its own. A core restarted on it does not refuse it.
func TestRestoreSealsLedgers(t *testing.T) {
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("TOGAVSU_TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	psql := os.Getenv("TOGAVSU_PSQL")
	admin, err := withDatabase(raw, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	const role, db, pass = "togavsu_ci_seal_app", "togavsu_seal_ci", "synthetic-app-password"
	for _, stmt := range []string{
		"DROP DATABASE IF EXISTS " + db + " WITH (FORCE)",
		"DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '" + role + "') THEN CREATE ROLE " + role + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE; END IF; END $$",
		"ALTER ROLE " + role + " PASSWORD '" + pass + "'",
		"CREATE DATABASE " + db,
		"GRANT CONNECT, CREATE ON DATABASE " + db + " TO " + role,
	} {
		if err := psqlExec(ctx, psql, admin, stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	t.Cleanup(func() { _ = psqlExec(context.Background(), psql, admin, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") })
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, pass)
	u.Path = "/" + db
	app := u.String()
	seed := `
CREATE SCHEMA core;
CREATE TABLE core.audit_log (id integer PRIMARY KEY, created_by text);
CREATE TABLE core.trust_ledger (id integer PRIMARY KEY, created_by text);
CREATE TABLE core.synthetic_items (id integer PRIMARY KEY, name text);
INSERT INTO core.audit_log VALUES (1, 'synthetic');
INSERT INTO core.trust_ledger VALUES (1, 'synthetic');
INSERT INTO core.synthetic_items VALUES (1, 'alpha');
`
	if err := ApplyCommand(psql, app)(ctx, strings.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	store := &Store{Dir: t.TempDir(), Passphrase: "synthetic-passphrase", Dump: DumpCommand(os.Getenv("TOGAVSU_PG_DUMP"), app)}
	meta, err := store.Create(ctx, reasonPreUpdate)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Restore(ctx, meta.ID, "", func(ctx context.Context, payload RestorePayload) error {
		return RestoreIntoCleanDatabase(ctx, psql, admin, app, payload.Dump, payload.Manifest.RecordCounts)
	})
	if err != nil {
		t.Fatal(err)
	}
	owners, err := psqlLines(ctx, psql, app, "SELECT tablename || '=' || tableowner FROM pg_tables WHERE schemaname = 'core' UNION ALL SELECT 'schema=' || pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'core' ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	want := "audit_log=" + ledgerseal.Owner + ",schema=" + ledgerseal.Owner + ",synthetic_items=" + role + ",trust_ledger=" + ledgerseal.Owner
	if strings.Join(owners, ",") != want {
		t.Fatalf("restored owners %v, want %s", owners, want)
	}
	if err := psqlExec(ctx, psql, app, "INSERT INTO core.audit_log VALUES (2, 'after restore'); INSERT INTO core.synthetic_items VALUES (2, 'beta')"); err != nil {
		t.Fatal("core role cannot insert after restore:", err)
	}
	for _, stmt := range []string{
		"UPDATE core.audit_log SET created_by = 'tampered'",
		"DELETE FROM core.trust_ledger",
		"TRUNCATE core.audit_log",
		"ALTER TABLE core.trust_ledger ADD COLUMN tampered text",
		"DROP TABLE core.audit_log",
		"ALTER SCHEMA core OWNER TO " + role,
	} {
		if err := psqlExec(ctx, psql, app, stmt); err == nil {
			t.Fatalf("core role ran %s on a restored ledger", stmt)
		}
	}
	if rows, err := psqlLines(ctx, psql, app, "SELECT count(*) FROM core.audit_log WHERE created_by <> 'tampered'"); err != nil || strings.Join(rows, "") != "2" {
		t.Fatalf("audit rows after the refused writes: %v %v", rows, err)
	}
}
