package core

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestLedgersSealedForApplicationRole (AC-024-F4, AC-006): after "core migrate" the
// append-only tables belong to the NOLOGIN ledger owner. The core, running as a
// non-superuser application role the way Compose runs togavsu_app, still inserts and
// reads every ledger, posts a trust reversal and raises a flag that references a ledger
// row, but every UPDATE, DELETE, TRUNCATE, ALTER, DROP, GRANT-back and SET ROLE to the
// owner fails and no row changes. A later migration that ALTERs a sealed table applies
// on the next boot, and the tables are sealed again afterwards.
func TestLedgersSealedForApplicationRole(t *testing.T) {
	ctx := context.Background()
	super := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	var appDSN string
	var u *url.URL
	f := newFin(t, func(c *Config) {
		appDSN = c.DatabaseURL
		// Failure path: without the superuser URL core migrate applies nothing and fails
		// instead of leaving the ledgers unsealed.
		if ran, err := MigrateSealed(ctx, appDSN, ""); !errors.Is(err, ErrNoAdminDatabaseURL) || ran {
			t.Fatalf("MigrateSealed without a superuser url: ran=%v err=%v", ran, err)
		}
		// Failure path: the serving core refuses a database core migrate never sealed.
		strict := *c
		strict.RequireSealed = true
		if app, err := Open(ctx, strict); !errors.Is(err, db.ErrUnsealed) {
			if app != nil {
				_ = app.Close()
			}
			t.Fatalf("Open(RequireSealed) on an unsealed database: %v", err)
		}
		// Failure path: the seal needs a superuser; the application role is refused.
		if _, err := MigrateSealed(ctx, appDSN, appDSN); !errors.Is(err, db.ErrNotSuperuser) {
			t.Fatalf("MigrateSealed with a non-superuser admin: %v", err)
		}
		ran, err := MigrateSealed(ctx, appDSN, super)
		if err != nil || !ran {
			t.Fatalf("first core migrate: ran=%v err=%v", ran, err)
		}
		// The serving core opens once the database is sealed.
		c.RequireSealed = true
	})
	appRole := ""
	if err := f.app.Pool.QueryRow(`SELECT current_user`).Scan(&appRole); err != nil {
		t.Fatal(err)
	}
	matter, clientID := f.matter("Estate of Sealy", "sealy@client.example", nil)
	f.post(f.admin, matter, clientID, "deposit", 100_00)
	wd := f.post(f.atty, matter, clientID, "withdrawal", 150_00) // below zero: a flag references it
	rev := f.atty.must(http.StatusCreated, "POST", "/api/v1/trust-ledger/"+wd+"/reversals", map[string]any{"reason": "keyed twice"})
	if rev.json["entry"].(map[string]any)["entry_type"] != "reversal" {
		t.Fatalf("reversal: %s", rev.body)
	}
	if f.count(`SELECT count(*) FROM trust_flags WHERE trust_entry_id = $1`, wd) == 0 {
		t.Fatal("no flag references the withdrawal")
	}
	evID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	ev := domain.Event{Meta: domain.Meta{ID: evID, CreatedAt: time.Now(), UpdatedAt: time.Now()}, ActorKind: "system",
		Action: "probe.sealed", RecordType: "matter", RecordID: domain.ID(matter), Summary: "seal probe"}
	if err := db.PublishEvent(ctx, f.app.Pool, ev); err != nil {
		t.Fatalf("insert into events as the application role: %v", err)
	}

	sealed := func(stage string) {
		t.Helper()
		su := f.superuser()
		for _, tbl := range db.AppendOnlyTables {
			var owner string
			if err := su.QueryRow(`SELECT tableowner FROM pg_tables WHERE schemaname = 'core' AND tablename = $1`, tbl).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if owner != db.LedgerOwner {
				t.Fatalf("%s: %s owned by %q", stage, tbl, owner)
			}
			before := f.count(`SELECT count(*) FROM core.` + tbl)
			if tbl == "audit_log" || tbl == "trust_ledger" || tbl == "events" {
				if before == 0 {
					t.Fatalf("%s: %s has no rows to protect", stage, tbl)
				}
			}
			for _, stmt := range []string{
				`UPDATE core.` + tbl + ` SET created_by = created_by`,
				`DELETE FROM core.` + tbl,
				`TRUNCATE core.` + tbl,
				`ALTER TABLE core.` + tbl + ` ADD COLUMN tampered TEXT`,
				`ALTER TABLE core.` + tbl + ` OWNER TO ` + appRole,
				`DROP TABLE core.` + tbl,
				`SET ROLE ` + db.LedgerOwner,
				`GRANT ` + db.LedgerOwner + ` TO CURRENT_USER`,
				`ALTER ROLE ` + db.LedgerOwner + ` LOGIN PASSWORD 'synthetic-takeover'`,
				`ALTER SCHEMA core OWNER TO ` + appRole,
				`DROP SCHEMA core CASCADE`,
			} {
				if _, err := f.app.Pool.ExecContext(ctx, stmt); err == nil {
					t.Fatalf("%s: the application role ran %s", stage, stmt)
				}
			}
			// The writer roles the application role administers cannot change rows either.
			for _, w := range []string{"togavsu_audit_writer", "togavsu_ai_log_writer"} {
				for _, stmt := range []string{`UPDATE core.` + tbl + ` SET created_by = created_by`, `DELETE FROM core.` + tbl} {
					tx, err := f.app.Pool.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+w); err != nil {
						t.Fatalf("set role %s: %v", w, err)
					}
					_, err = tx.ExecContext(ctx, stmt)
					_ = tx.Rollback()
					if err == nil {
						t.Fatalf("%s: %s ran %s", stage, w, stmt)
					}
				}
			}
			// GRANT-back is refused or grants nothing; the privilege stays absent.
			_, _ = f.app.Pool.ExecContext(ctx, `GRANT UPDATE, DELETE, TRUNCATE ON core.`+tbl+` TO CURRENT_USER`)
			var upd, del, trunc, ins, sel bool
			if err := f.app.Pool.QueryRow(`SELECT has_table_privilege('core.'||$1, 'UPDATE'), has_table_privilege('core.'||$1, 'DELETE'),
				has_table_privilege('core.'||$1, 'TRUNCATE'), has_table_privilege('core.'||$1, 'INSERT'), has_table_privilege('core.'||$1, 'SELECT')`, tbl).
				Scan(&upd, &del, &trunc, &ins, &sel); err != nil {
				t.Fatal(err)
			}
			if upd || del || trunc || !ins || !sel {
				t.Fatalf("%s: %s privileges update=%v delete=%v truncate=%v insert=%v select=%v", stage, tbl, upd, del, trunc, ins, sel)
			}
			if _, err := f.app.Pool.ExecContext(ctx, `UPDATE core.`+tbl+` SET created_by = created_by`); err == nil {
				t.Fatalf("%s: UPDATE ran on %s after a GRANT-back", stage, tbl)
			}
			if after := f.count(`SELECT count(*) FROM core.` + tbl); after != before {
				t.Fatalf("%s: %s rows %d -> %d", stage, tbl, before, after)
			}
		}
		var member bool
		if err := su.QueryRow(`SELECT pg_has_role($1, $2, 'MEMBER')`, appRole, db.LedgerOwner).Scan(&member); err != nil || member {
			t.Fatalf("%s: application role is a member of the ledger owner: %v %v", stage, member, err)
		}
	}
	sealed("first boot")

	// Second boot: core migrate again applies nothing and seals again; the core opens.
	if err := f.app.Close(); err != nil {
		t.Fatal(err)
	}
	if ran, err := MigrateSealed(ctx, appDSN, super); err != nil || ran {
		t.Fatalf("second core migrate: ran=%v err=%v", ran, err)
	}
	u, err = url.Parse(super)
	if err != nil {
		t.Fatal(err)
	}
	au, _ := url.Parse(appDSN)
	u.Path = au.Path
	admin, err := db.OpenPostgres(u.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	plain, err := db.OpenPostgres(appDSN, "")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	columns := func(name string) int {
		t.Helper()
		var n int
		if err := admin.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND column_name = $1`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Failure path: a migration that ALTERs a ledger and then fails leaves the ledgers
	// sealed and the column absent; the handoff rolled back with it.
	bad := db.Migration{Version: 1, Name: "seal_fail", SQL: "-- +goose Up\nALTER TABLE audit_log ADD COLUMN seal_fail TEXT;\nSELECT 1/0;\n"}
	if _, err := db.ApplySealed(ctx, admin, appRole, db.ApplySpec{Owner: "seal-fail", Schema: "core", Dialect: "postgres", Migrations: []db.Migration{bad}}); err == nil {
		t.Fatal("a failing ledger migration applied")
	}
	if err := db.VerifySealed(ctx, plain); err != nil {
		t.Fatalf("ledgers after a failed migration: %v", err)
	}
	if n := columns("seal_fail"); n != 0 {
		t.Fatalf("failed migration left %d columns", n)
	}
	// Failure path: the migrating backend dies mid-migration (the migrate container
	// killed during an update). While it runs, the serving role still sees every ledger
	// sealed; after it dies nothing was handed over and no column was added.
	slow := db.Migration{Version: 1, Name: "seal_kill", SQL: "-- +goose Up\nALTER TABLE trust_ledger ADD COLUMN seal_kill TEXT;\nSELECT pg_sleep(60);\n"}
	done := make(chan error, 1)
	go func() {
		_, err := db.ApplySealed(ctx, admin, appRole, db.ApplySpec{Owner: "seal-kill", Schema: "core", Dialect: "postgres", Migrations: []db.Migration{slow}})
		done <- err
	}()
	su := admin
	var pid int
	for deadline := time.Now().Add(20 * time.Second); ; {
		err := su.QueryRow(`SELECT pid FROM pg_stat_activity WHERE query LIKE 'SELECT pg_sleep(60)%' AND pid <> pg_backend_pid()`).Scan(&pid)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the slow migration never started: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := db.VerifySealed(ctx, plain); err != nil {
		t.Fatalf("ledgers seen by the serving role while a migration runs: %v", err)
	}
	if _, err := su.Exec(`SELECT pg_terminate_backend($1)`, pid); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a killed migration reported success")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the killed migration did not return")
	}
	if err := db.VerifySealed(ctx, plain); err != nil {
		t.Fatalf("ledgers after the migrating backend died: %v", err)
	}
	if n := columns("seal_kill"); n != 0 {
		t.Fatalf("killed migration left %d columns", n)
	}
	// An expand migration that ALTERs sealed tables applies through ApplySealed, the path
	// core migrate uses, and the tables are sealed when it commits.
	probe := db.Migration{Version: 1, Name: "seal_probe", SQL: "-- +goose Up\nALTER TABLE audit_log ADD COLUMN seal_probe TEXT;\nALTER TABLE trust_ledger ADD COLUMN seal_probe TEXT;\n"}
	if n, err := db.ApplySealed(ctx, admin, appRole, db.ApplySpec{Owner: "seal-probe", Schema: "core", Dialect: "postgres", Migrations: []db.Migration{probe}}); err != nil || n != 1 {
		t.Fatalf("expand migration on sealed ledgers: n=%d err=%v", n, err)
	}
	if err := db.VerifySealed(ctx, plain); err != nil {
		t.Fatalf("ledgers after the expand migration: %v", err)
	}
	// The session ApplySealed used went back to the pool as the superuser.
	var who string
	if err := admin.QueryRow(`SELECT current_user`).Scan(&who); err != nil || who == appRole {
		t.Fatalf("admin pool session after ApplySealed: %q %v", who, err)
	}
	f.restart()
	if !f.cfg.RequireSealed {
		t.Fatal("the restarted core did not require a sealed database")
	}
	if n := f.count(`SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND column_name = 'seal_probe'`); n != 2 {
		t.Fatalf("probe columns: %d", n)
	}
	f.post(f.admin, matter, clientID, "deposit", 25_00)
	sealed("after a second boot and a migration")

	// Ownership is checked, not assumed: a ledger handed back to the application role
	// fails db.VerifySealed, the serving core refuses to start, and core migrate seals
	// it again.
	if err := f.app.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, `ALTER TABLE core.trust_ledger OWNER TO `+appRole); err != nil {
		t.Fatal(err)
	}
	if err := db.VerifySealed(ctx, plain); !errors.Is(err, db.ErrUnsealed) || !strings.Contains(err.Error(), "core.trust_ledger") {
		t.Fatalf("VerifySealed with trust_ledger owned by the application role: %v", err)
	}
	if app, err := Open(ctx, f.cfg); !errors.Is(err, db.ErrUnsealed) {
		if app != nil {
			_ = app.Close()
		}
		t.Fatalf("Open(RequireSealed) with trust_ledger unsealed: %v", err)
	}
	if _, err := MigrateSealed(ctx, appDSN, super); err != nil {
		t.Fatalf("core migrate after the tamper: %v", err)
	}
	if err := db.VerifySealed(ctx, plain); err != nil {
		t.Fatalf("VerifySealed after core migrate: %v", err)
	}
	f.restart()
	sealed("after a tampered owner was sealed again")
}
