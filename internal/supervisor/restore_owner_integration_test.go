//go:build integration

package supervisor

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
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
