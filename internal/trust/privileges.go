package trust

import (
	"context"
	"database/sql"
)

// InstallPrivileges makes trust_ledger append-only at the database (AC-006). It revokes
// UPDATE, DELETE and TRUNCATE from PUBLIC and from the application role that runs it,
// which owns the table. The role keeps INSERT and SELECT, and UPDATE on row_guard only:
// Postgres needs one updatable column to run the FOR KEY SHARE foreign-key checks that
// reference ledger rows, and row_guard's CHECK pins it to 0, so no UPDATE can change a
// row. No trigger is added (PRD section 6). It is safe to run again; the core runs it at
// start next to audit.InstallPrivileges.
//
// ponytail: ceiling: the owning role keeps grant options and can GRANT UPDATE back to
// itself, ALTER the CHECK or DROP the table; a superuser bypasses privileges. Compose runs
// the core as the non-superuser togavsu_app. Upgrade path: a migration role owns
// trust_ledger and the application role is a non-owner with SELECT and INSERT. Trigger:
// the same change for audit_log (PONYTAIL-DEBT.md, contracts item 4).
func InstallPrivileges(ctx context.Context, pool *sql.DB) error {
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
