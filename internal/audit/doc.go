// Package audit records append-only core writes. Rows keep the actor, role,
// and source. Secrets are stored as [redacted]. Update and delete are refused.
//
// InstallPrivileges makes core.audit_log append-only for every role but a
// superuser: the non-owner writer role may only SELECT and INSERT, and the
// application role that owns the table loses UPDATE, DELETE and TRUNCATE. No
// trigger is used. A superuser bypasses privileges, so the application role must
// not be one. It cannot live in a goose migration: CREATE ROLE is not allowed
// inside a transaction, and db.Apply rejects NO TRANSACTION. The integration lead
// runs it from bootstrap.
//
// Service.Atomic holds a write and its audit rows in one transaction (WithTx), so a
// change is never committed without its history row, nor a row without its change.
package audit
