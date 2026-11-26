package modules_test

import (
	"context"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/core"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// TestPGCoreMigrateTwiceAfterLedgerExtendingModule: after a module that extends
// trust_ledger is provisioned (REFERENCES (id) granted from the sealed application
// role, then an extension table with a foreign key), the core migrate path runs
// twice and both succeed. Desk failed here with seal ledgers: dependent privileges
// exist (SQLSTATE 2BP01).
func TestPGCoreMigrateTwiceAfterLedgerExtendingModule(t *testing.T) {
	ctx := context.Background()
	adminURL := testkit.AdminURL(t)
	base := testkit.NewDatabase(t, "m", "modules/"+t.Name())
	testkit.DropModuleRoles(t, base)
	appDSN := testkit.AppRole(t, base)

	if ran, err := core.MigrateSealed(ctx, appDSN, adminURL); err != nil || !ran {
		t.Fatalf("core migrate before module: ran=%v err=%v", ran, err)
	}

	appDB, err := db.OpenPostgres(appDSN, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = appDB.Close() })

	schemas := &modules.Schemas{DB: appDB}
	role, password, err := schemas.Provision(ctx, "ledger-marks")
	if err != nil {
		t.Fatalf("provision ledger-extending module: %v", err)
	}
	modDB, err := testkit.OpenAs(appDSN, role, password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = modDB.Close() })
	// The module extends trust_ledger the way data.extends[trust_entry] migrations do.
	if _, err := modDB.ExecContext(ctx, `CREATE TABLE ledger_marks (
		trust_entry_id TEXT PRIMARY KEY REFERENCES core.trust_ledger (id) ON DELETE RESTRICT,
		note TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create ledger extension table: %v", err)
	}
	var refs bool
	if err := appDB.QueryRowContext(ctx, `SELECT has_column_privilege($1, 'core.trust_ledger', 'id', 'REFERENCES')`, role).Scan(&refs); err != nil || !refs {
		t.Fatalf("module role REFERENCES on trust_ledger: err=%v ok=%v", err, refs)
	}

	for i := 1; i <= 2; i++ {
		ran, err := core.MigrateSealed(ctx, appDSN, adminURL)
		if err != nil {
			t.Fatalf("core migrate %d after ledger-extending module: %v", i, err)
		}
		if ran {
			t.Fatalf("core migrate %d after module applied unexpected migrations", i)
		}
	}
	if err := db.VerifySealed(ctx, appDB); err != nil {
		t.Fatalf("VerifySealed after post-module migrates: %v", err)
	}
	if err := appDB.QueryRowContext(ctx, `SELECT has_column_privilege($1, 'core.trust_ledger', 'id', 'REFERENCES')`, role).Scan(&refs); err != nil || !refs {
		t.Fatalf("module role lost REFERENCES after re-seal: err=%v ok=%v", err, refs)
	}
}
