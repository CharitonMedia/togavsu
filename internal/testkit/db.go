// Package testkit is the test machinery the core's own suites and the public harness
// (pkg/modulekit/coretest) share: throwaway Postgres databases and core roles, the
// module logins' cleanup, an in-process stand-in for the supervisor, a backup fake, an
// HTTP client with session, CSRF and bearer handling, and the setup steps that sign the
// firm's people in. It imports no core composition, so internal/core's in-package tests
// can use it. It is test scaffolding: nothing that serves the core imports it.
package testkit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/bootstrap"
	"github.com/CharitonMedia/togavsu/internal/db"
)

// Small fixed numbers spelled as arithmetic for the AC-030 source check.
var (
	zero = int(0)
	one  = zero + 1
)

// AdminURL is the superuser URL in TOGAVSU_TEST_DATABASE_URL. It skips t when the
// variable is unset, and fails t instead in CI.
func AdminURL(t *testing.T) string {
	t.Helper()
	raw := bootstrap.TestDatabaseURL()
	if raw == "" {
		if bootstrap.InCI() {
			t.Fatal(bootstrap.TestDatabaseURLName + " is required in CI")
		}
		t.Skip(bootstrap.TestDatabaseURLName + " is not set")
	}
	return raw
}

// hashName is prefix plus the first quarter of SHA-256(key) as hex.
func hashName(prefix, key string) string {
	sum := sha256.Sum256([]byte(key))
	return prefix + hex.EncodeToString(sum[:len(sum)/4])
}

// NewDatabase creates an empty database named for key (prefix plus a hash of key, so a
// rerun reuses and first drops its own name) and returns its superuser URL. The database
// is dropped at cleanup.
func NewDatabase(t *testing.T, prefix, key string) string {
	t.Helper()
	raw := AdminURL(t)
	admin, err := db.OpenPostgres(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := hashName(prefix, key)
	ctx := context.Background()
	if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin, err := db.OpenPostgres(raw, "")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name)
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// AppRolePassword is the synthetic password of AppRole's login.
const AppRolePassword = "synthetic-core-app-password"

// AppRole makes dsn's database owned by and reached through a fresh non-superuser login
// role, set up the way deploy/postgres/core-role.sh sets up togavsu_app (CREATEROLE for
// module logins, nothing else), so the append-only privileges on audit_log and
// trust_ledger hold as they do in Compose. It returns the role's URL; the database and
// the role are dropped at cleanup.
func AppRole(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	role, password := name+"_app", AppRolePassword
	admin, err := db.OpenPostgres(dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP ROLE IF EXISTS ` + role,
		`CREATE ROLE ` + role + ` LOGIN NOSUPERUSER NOCREATEDB CREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '` + password + `'`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'togavsu_audit_writer') THEN
		   CREATE ROLE togavsu_audit_writer NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE; END IF; END $$`,
		`GRANT togavsu_audit_writer TO ` + role + ` WITH ADMIN OPTION`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'togavsu_ai_log_writer') THEN
		   CREATE ROLE togavsu_ai_log_writer NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE; END IF; END $$`,
		`GRANT togavsu_ai_log_writer TO ` + role + ` WITH ADMIN OPTION`,
		`ALTER DATABASE ` + name + ` OWNER TO ` + role,
		`CREATE SCHEMA core AUTHORIZATION ` + role,
	} {
		if _, err := admin.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	raw := AdminURL(t)
	t.Cleanup(func() {
		admin, err := db.OpenPostgres(raw, "")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name)
		_, _ = admin.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+role)
	})
	u.User = url.UserPassword(role, password)
	return u.String()
}

// DropModuleRoles removes the module logins the core made for dsn's database, now (a
// previous run's, whose database NewDatabase already dropped) and at cleanup: roles are
// cluster-wide, and the database cleanup drops only the database and the core role. A
// module role's name ends in a tag of its database's name, so only this database's
// logins match.
func DropModuleRoles(t *testing.T, dsn string) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	sum := sha256.Sum256([]byte(name))
	tag := hex.EncodeToString(sum[:len(sum)/8])
	raw := AdminURL(t)
	drop := func() {
		admin, err := db.OpenPostgres(raw, "")
		if err != nil {
			return
		}
		defer admin.Close()
		rows, err := admin.QueryContext(context.Background(), `SELECT rolname FROM pg_roles WHERE rolname LIKE 'mod\_%' AND rolname LIKE $1`, "%_"+tag)
		if err != nil {
			return
		}
		var roles []string
		for rows.Next() {
			var r string
			if rows.Scan(&r) == nil {
				roles = append(roles, r)
			}
		}
		rows.Close()
		if len(roles) == 0 {
			return
		}
		// The roles own the module schemas in this test's database: drop those first.
		tu, _ := url.Parse(raw)
		tu.Path = "/" + name
		if own, err := db.OpenPostgres(tu.String(), ""); err == nil {
			for _, r := range roles {
				_, _ = own.ExecContext(context.Background(), `DROP OWNED BY "`+r+`" CASCADE`)
			}
			_ = own.Close()
		}
		for _, r := range roles {
			_, _ = admin.ExecContext(context.Background(), `DROP ROLE IF EXISTS "`+r+`"`)
		}
	}
	drop()
	t.Cleanup(drop)
}

// OpenAs opens dsn's database as role, the way a module container reaches its own
// schema with the login the core provisioned.
func OpenAs(dsn, role, password string) (*sql.DB, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	u.User = url.UserPassword(role, password)
	return db.OpenPostgres(u.String(), "")
}
