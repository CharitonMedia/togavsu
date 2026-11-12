package core

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
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
		// Failure path: the seal needs a superuser; the application role is refused.
		if _, err := MigrateSealed(ctx, appDSN, appDSN); !errors.Is(err, db.ErrNotSuperuser) {
			t.Fatalf("MigrateSealed with a non-superuser admin: %v", err)
		}
		ran, err := MigrateSealed(ctx, appDSN, super)
		if err != nil || !ran {
			t.Fatalf("first core migrate: ran=%v err=%v", ran, err)
		}
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
	// An expand migration that ALTERs a sealed table applies through the same unseal,
	// migrate as the application role, seal sequence core migrate uses.
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
	if err := db.UnsealLedgers(ctx, admin, appRole); err != nil {
		t.Fatal(err)
	}
	plain, err := db.OpenPostgres(appDSN, "")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	probe := db.Migration{Version: 1, Name: "seal_probe", SQL: "-- +goose Up\nALTER TABLE audit_log ADD COLUMN seal_probe TEXT;\nALTER TABLE trust_ledger ADD COLUMN seal_probe TEXT;\n"}
	if err := db.Apply(ctx, plain, db.ApplySpec{Owner: "seal-probe", Schema: "core", Dialect: "postgres", Migrations: []db.Migration{probe}}); err != nil {
		t.Fatalf("expand migration on unsealed ledgers: %v", err)
	}
	if err := db.SealLedgers(ctx, admin, appRole); err != nil {
		t.Fatal(err)
	}
	f.restart()
	if n := f.count(`SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND column_name = 'seal_probe'`); n != 2 {
		t.Fatalf("probe columns: %d", n)
	}
	f.post(f.admin, matter, clientID, "deposit", 25_00)
	sealed("after a second boot and a migration")
}
