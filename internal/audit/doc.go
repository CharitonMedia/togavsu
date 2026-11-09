// Package audit records append-only core writes. Rows keep the actor, role,
// and source. Secrets are stored as [redacted]. Update and delete are refused.
//
// A non-owner database role is refused raw UPDATE and DELETE after
// InstallPrivileges. That statement cannot live in a goose migration: CREATE
// ROLE is not allowed inside a transaction, and db.Apply rejects NO TRANSACTION.
// The integration lead needs to create the role from bootstrap. Until the app
// connects as a role that is not the table owner, a superuser can still change
// a row outside this package.
package audit
