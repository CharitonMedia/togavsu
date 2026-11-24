package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
)

// TestWrapLedgerMigration: a migration that names an append-only table gets the handoff
// and the seal inside its own Up section, before any Down section; one that names none
// comes back unchanged, so it takes no lock on the ledgers.
func TestWrapLedgerMigration(t *testing.T) {
	plain := "-- +goose Up\nCREATE TABLE notes (id TEXT);\n"
	if got := wrapLedgerMigration(plain, "togavsu_app"); got != plain {
		t.Fatalf("a migration naming no ledger was wrapped:\n%s", got)
	}
	src := "-- +goose Up\nALTER TABLE audit_log ADD COLUMN x TEXT;\n-- +goose Down\nALTER TABLE audit_log DROP COLUMN x;\n"
	got := wrapLedgerMigration(src, "togavsu_app")
	up := strings.Index(got, "-- +goose Up")
	unseal := strings.Index(got, "$unseal$")
	stmt := strings.Index(got, "ADD COLUMN x")
	seal := strings.Index(got, "OWNER TO "+LedgerOwner)
	down := strings.Index(got, "-- +goose Down")
	if !(up >= 0 && up < unseal && unseal < stmt && stmt < seal && seal < down) {
		t.Fatalf("wrapped order up=%d unseal=%d stmt=%d seal=%d down=%d:\n%s", up, unseal, stmt, seal, down, got)
	}
	if !strings.Contains(got, "ARRAY['audit_log']") || strings.Contains(got, "ARRAY['audit_log', 'trust_ledger']") {
		t.Fatalf("handoff should name only audit_log:\n%s", got)
	}
	// Every role switch ends back on the application role, so goose's version row is
	// written as it, and the Down section is untouched.
	if !strings.HasSuffix(got, "-- +goose Down\nALTER TABLE audit_log DROP COLUMN x;\n") {
		t.Fatalf("down section changed:\n%s", got)
	}
	upPart := got[:down]
	if strings.LastIndex(upPart, `SET ROLE "togavsu_app"`) < strings.LastIndex(upPart, "RESET ROLE") {
		t.Fatalf("the Up section does not end as the application role:\n%s", upPart)
	}
	// A migration with no annotation is wrapped in the Up section goose adds.
	bare := wrapLedgerMigration("INSERT INTO events (id) VALUES ('x');", "togavsu_app")
	if !strings.HasPrefix(bare, "-- +goose Up\n") || !strings.Contains(bare, "ARRAY['events']") {
		t.Fatalf("bare migration:\n%s", bare)
	}
}

// sealedApp creates a CREATEROLE application login the way Compose sets up togavsu_app,
// applies the core schema as that role (ApplySealed), seals the ledgers, and returns the
// superuser connection plus an application-role pool.
func sealedApp(t *testing.T) (ctx context.Context, admin, appDB *sql.DB, appRole string) {
	t.Helper()
	ctx = context.Background()
	admin = openPostgres(t, "")
	sum := sha256.Sum256([]byte(t.Name()))
	appRole = "seal_app_" + hex.EncodeToString(sum[:4])
	depTag := hex.EncodeToString(sum[4:8])
	const password = "synthetic-seal-app-password"
	var dbName string
	if err := admin.QueryRowContext(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP ROLE IF EXISTS ` + appRole,
		`CREATE ROLE ` + appRole + ` LOGIN NOSUPERUSER CREATEROLE NOCREATEDB NOREPLICATION NOBYPASSRLS PASSWORD '` + password + `'`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'togavsu_audit_writer') THEN
		   CREATE ROLE togavsu_audit_writer NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE; END IF; END $$`,
		`GRANT togavsu_audit_writer TO ` + appRole + ` WITH ADMIN OPTION`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'togavsu_ai_log_writer') THEN
		   CREATE ROLE togavsu_ai_log_writer NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE; END IF; END $$`,
		`GRANT togavsu_ai_log_writer TO ` + appRole + ` WITH ADMIN OPTION`,
		`ALTER DATABASE ` + dbName + ` OWNER TO ` + appRole,
		`CREATE SCHEMA core AUTHORIZATION ` + appRole,
	} {
		if _, err := admin.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		// Drop the dependent role first: it holds REFERENCES granted by app, and a
		// leftover from an earlier run is only droppable by a superuser (PG 16+).
		_, _ = admin.ExecContext(context.Background(), `DROP ROLE IF EXISTS seal_dep_`+depTag)
		_, _ = admin.ExecContext(context.Background(), `REASSIGN OWNED BY `+appRole+` TO CURRENT_USER`)
		_, _ = admin.ExecContext(context.Background(), `DROP OWNED BY `+appRole)
		_, _ = admin.ExecContext(context.Background(), `DROP ROLE IF EXISTS `+appRole)
	})
	ms, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySealed(ctx, admin, appRole, ApplySpec{Owner: CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatalf("ApplySealed core schema: %v", err)
	}
	if err := SealLedgers(ctx, admin, appRole); err != nil {
		t.Fatalf("initial SealLedgers: %v", err)
	}
	u, err := url.Parse(testAdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + dbName
	u.User = url.UserPassword(appRole, password)
	appDB, err = OpenPostgres(u.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = appDB.Close() })
	return ctx, admin, appDB, appRole
}

// grantTrustReferencesAsApp creates a dependent role and, as the application role, passes
// REFERENCES (id) on core.trust_ledger to it — the grant module install makes.
func grantTrustReferencesAsApp(t *testing.T, ctx context.Context, admin, appDB *sql.DB) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name()))
	dep := "seal_dep_" + hex.EncodeToString(sum[4:8])
	// Superuser drops any leftover: PG 16+ lets a CREATEROLE login drop only roles it
	// still holds ADMIN OPTION on, so a prior run's orphan is not droppable as app.
	if _, err := admin.ExecContext(ctx, `DROP ROLE IF EXISTS `+dep); err != nil {
		t.Fatalf("DROP ROLE %s: %v", dep, err)
	}
	for _, stmt := range []string{
		`CREATE ROLE ` + dep + ` NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE`,
		`GRANT REFERENCES (id) ON core.trust_ledger TO ` + dep,
	} {
		if _, err := appDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return dep
}

func hasColumnPrivilege(t *testing.T, ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, role, table, column, priv string) bool {
	t.Helper()
	var ok bool
	if err := q.QueryRowContext(ctx, `SELECT has_column_privilege($1, $2, $3, $4)`, role, table, column, priv).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

func appHasGrantOptionOnSelectOrInsert(t *testing.T, ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, appRole, table string) bool {
	t.Helper()
	var ok bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			CROSS JOIN LATERAL aclexplode(c.relacl) a
			JOIN pg_roles g ON g.oid = a.grantee
			WHERE n.nspname = 'core' AND c.relname = $1
			  AND g.rolname = $2
			  AND a.privilege_type IN ('SELECT', 'INSERT')
			  AND a.is_grantable
		)`, table, appRole).Scan(&ok)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func assertAppCannotMutateLedgers(t *testing.T, ctx context.Context, appDB *sql.DB, appRole string) {
	t.Helper()
	for _, tbl := range AppendOnlyTables {
		var upd, del, trunc bool
		if err := appDB.QueryRowContext(ctx, `SELECT has_table_privilege($1, 'core.'||$2, 'UPDATE'),
			has_table_privilege($1, 'core.'||$2, 'DELETE'), has_table_privilege($1, 'core.'||$2, 'TRUNCATE')`,
			appRole, tbl).Scan(&upd, &del, &trunc); err != nil {
			t.Fatal(err)
		}
		if upd || del || trunc {
			t.Fatalf("application role can mutate core.%s: update=%v delete=%v truncate=%v", tbl, upd, del, trunc)
		}
		if appHasGrantOptionOnSelectOrInsert(t, ctx, appDB, appRole, tbl) {
			t.Fatalf("application role still has grant option on SELECT or INSERT for core.%s", tbl)
		}
	}
}

// TestSealLedgersKeepsDependentReferences: after the application role has passed
// REFERENCES (id) on trust_ledger to another role (as module install does), SealLedgers
// succeeds again without CASCADE, the dependent grant remains, and VerifySealed still
// passes with SELECT/INSERT only and no grant option on those.
func TestSealLedgersKeepsDependentReferences(t *testing.T) {
	ctx, admin, appDB, appRole := sealedApp(t)
	dep := grantTrustReferencesAsApp(t, ctx, admin, appDB)

	if err := SealLedgers(ctx, admin, appRole); err != nil {
		t.Fatalf("re-seal with dependent REFERENCES: %v", err)
	}
	if !hasColumnPrivilege(t, ctx, admin, dep, "core.trust_ledger", "id", "REFERENCES") {
		t.Fatalf("%s lost REFERENCES (id) on core.trust_ledger after re-seal", dep)
	}
	if err := VerifySealed(ctx, appDB); err != nil {
		t.Fatalf("VerifySealed after re-seal: %v", err)
	}
	assertAppCannotMutateLedgers(t, ctx, appDB, appRole)
}

// TestApplySealedAfterDependentReferences: the same dependent REFERENCES setup, then a
// migration whose SQL names trust_ledger applies through ApplySealed and leaves the
// table sealed again afterwards.
func TestApplySealedAfterDependentReferences(t *testing.T) {
	ctx, admin, appDB, appRole := sealedApp(t)
	dep := grantTrustReferencesAsApp(t, ctx, admin, appDB)

	m := Migration{Version: 1, Name: "seal_dep_trust", SQL: "-- +goose Up\nALTER TABLE trust_ledger ADD COLUMN seal_dep_probe TEXT;\n"}
	n, err := ApplySealed(ctx, admin, appRole, ApplySpec{Owner: "seal-dep", Schema: "core", Dialect: "postgres", Migrations: []Migration{m}})
	if err != nil || n != 1 {
		t.Fatalf("ApplySealed naming trust_ledger with dependent REFERENCES: n=%d err=%v", n, err)
	}
	if !hasColumnPrivilege(t, ctx, admin, dep, "core.trust_ledger", "id", "REFERENCES") {
		t.Fatalf("%s lost REFERENCES (id) on core.trust_ledger after ApplySealed", dep)
	}
	if err := VerifySealed(ctx, appDB); err != nil {
		t.Fatalf("VerifySealed after ApplySealed: %v", err)
	}
	var owner string
	if err := admin.QueryRowContext(ctx, `SELECT tableowner FROM pg_tables WHERE schemaname = 'core' AND tablename = 'trust_ledger'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != LedgerOwner {
		t.Fatalf("trust_ledger owner after ApplySealed: %q", owner)
	}
	assertAppCannotMutateLedgers(t, ctx, appDB, appRole)
}
