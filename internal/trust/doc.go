// Package trust implements PRD sections 5 (Trust) and 8.4: trust accounts, the
// append-only trust ledger with reversals, cached balances per client per matter, per
// client and per account, below-zero flags with notifications and email, evergreen
// replenishment requests, monthly reconciliation, flat-fee application from trust and
// the trust reports.
//
// Rules that are not settings (PRD 8.7):
//   - The ledger is append-only. The service has no update or delete, and
//     InstallPrivileges takes UPDATE, DELETE and TRUNCATE on trust_ledger away from the
//     application role, so raw SQL cannot change a row either. A correction is a reversal.
//   - A below-zero balance never blocks a post. The post commits, and the same
//     transaction raises a flag, queues the email to the admins, the responsible attorney
//     and any extra recipients, and adds an in-app notification for the admins and the
//     responsible attorney.
//   - Every read and write is checked in this service through domain.Policy, including
//     matter walls, so HTTP, MCP, widgets and reports get one answer.
//
// Every write runs in one Postgres transaction with its audit rows (audit.WithTx on the
// same pool). Trust writes are serialized by locking the trust account rows, so two
// withdrawals cannot both read the same balance.
package trust
