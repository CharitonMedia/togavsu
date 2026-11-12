package trust

import (
	"context"
	"database/sql"

	"github.com/CharitonMedia/togavsu/internal/db"
)

// InstallPrivileges makes trust_ledger append-only at the database (AC-006). It revokes
// UPDATE, DELETE and TRUNCATE from PUBLIC and from the application role that runs it,
// which owns the table. The role keeps INSERT and SELECT, and UPDATE on row_guard only:
// Postgres needs one updatable column to run the FOR KEY SHARE foreign-key checks that
// reference ledger rows, and row_guard's CHECK pins it to 0, so no UPDATE can change a
// row. No trigger is added (PRD section 6). It is safe to run again; the core runs it at
// start next to audit.InstallPrivileges.
//
// Once core migrate has sealed the ledger (db.SealLedgers), db.LedgerOwner owns it, the
// application role holds only SELECT, INSERT and REFERENCES (id), and this is a no-op.
func InstallPrivileges(ctx context.Context, pool *sql.DB) error {
	// A sealed ledger (db.SealLedgers) is owned by db.LedgerOwner and the application
	// role already holds only SELECT and INSERT; foreign-key checks run as that owner.
	if owns, err := db.OwnsTable(ctx, pool, "trust_ledger"); err != nil || !owns {
		return err
	}
	for _, stmt := range []string{
		`REVOKE UPDATE, DELETE, TRUNCATE ON TABLE core.trust_ledger FROM PUBLIC, CURRENT_USER`,
		`GRANT UPDATE (row_guard) ON TABLE core.trust_ledger TO CURRENT_USER`,
	} {
		if _, err := pool.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
