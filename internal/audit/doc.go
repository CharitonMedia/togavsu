// Package audit records append-only core writes. Rows keep the actor, role,
// and source. Secrets are stored as [redacted]. Update and delete are refused.
//
// InstallPrivileges makes core.audit_log append-only in the database: the
// non-owner writer role may only SELECT and INSERT, the application role loses
// UPDATE, DELETE and TRUNCATE, and a guard refuses those statements from any
// role, the owner and a superuser included. It cannot live in a goose
// migration: CREATE ROLE is not allowed inside a transaction, and db.Apply
// rejects NO TRANSACTION. The integration lead runs it from bootstrap.
//
// Service.Atomic holds a write and its audit rows in one transaction, so a
// change is never committed without its history row, nor a row without its change.
package audit
