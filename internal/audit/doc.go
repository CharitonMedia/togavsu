// Package audit records append-only core writes. Rows keep the actor, role,
// and source. Secrets are stored as [redacted]. Update and delete are refused.
//
// InstallPrivileges creates the non-owner writer role with only SELECT and
// INSERT. The table owner and a superuser can still UPDATE and DELETE. A
// trigger is not added: PRD section 6 forbids business-logic triggers. The
// statements cannot live in a goose migration: CREATE ROLE is not allowed
// inside a transaction, and db.Apply rejects NO TRANSACTION. The integration
// lead runs them from bootstrap.
//
// Service.Atomic holds a write and its audit rows in one transaction, so a
// change is never committed without its history row, nor a row without its change.
package audit
