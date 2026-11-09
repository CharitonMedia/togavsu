// Package audit records append-only core writes. Rows keep the actor, role,
// and source. Secrets are stored as [redacted]. Update and delete are refused.
//
// InstallPrivileges creates the non-owner writer role with only SELECT and
// INSERT, and revokes UPDATE, DELETE and TRUNCATE from PUBLIC and from the
// role that runs it. That role still owns the table, so it can grant those
// privileges back to itself and then change or delete rows, and it can ALTER
// or DROP the table. A superuser bypasses privileges. No trigger is added:
// PRD section 6 forbids business-logic triggers. The statements cannot live
// in a goose migration: CREATE ROLE is not allowed inside a transaction, and
// db.Apply rejects NO TRANSACTION. The integration lead runs them from bootstrap.
//
// Service.Atomic holds a write and its audit rows in one transaction (WithTx), so a
// change is never committed without its history row, nor a row without its change.
package audit
