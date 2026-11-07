# Shared contracts

Later waves program against these types. Do not invent a second principal, money type, or error body.

## Identity and access

`domain.Principal` carries the user, roles, and source (`ui`, `api_token`, `module`, or `system`). A user may hold more than one role. Permissions are the union of those roles. `SourceSystem` is only for the job runner; a request handler must not copy it from client input.

`domain.Decide` applies the rules once the caller knows whether a matter is walled. `domain.Policy.Authorize` asks a `WallReader` first and fails closed when a matter id is set and no reader is configured. Identity implements `WallReader`.

A walled matter is hidden from every principal who is not on the wall, including an admin. An accountant may read invoices, payments, trust, billing reports, receipts, and invoice PDFs, and may not write. An attorney-only file is hidden from staff and from an accountant, including a receipt or invoice PDF; it is not returned redacted. Staff may record trust deposits and may not withdraw, decide conflicts, or approve invoices. Attorney-only fields on other records are redacted for staff and refused for clients. Clients read shared items on their own matters.

## API errors

`domain.APIError` is the JSON body: `code`, `message`, and optional `field`. Status codes are 401, 403, 404, 400, 409, and 429. Append-only refusals use code `append_only` and status 409.

## Money, time, and ids

Money is `domain.Cents`. IDs are lowercase UUIDv7. Stored timestamps are unix microseconds UTC. Display uses `firm.time_zone`.

## Settings

Read and write tunables through `settings.Registry`. `Set` and `Reset` take a `domain.Principal` and require the admin role. A staff, accountant, client, attorney, or roleless caller gets `ErrForbidden` and does not change the stored value or the audit log. The audit row stores that caller's roles, source, and any token or module id. `Get` reads the store every time. The value and audit record commit in one transaction. A cancelled context or a failed audit leaves the previous value in place. Secret values are AES-256-GCM sealed when the store is wrapped with `settings.NewSealingStore`, and the audit record stores `[redacted]`. A non-secret value is stored as written, including text that begins with the seal prefix.

The only environment variables are `TOGAVSU_DATABASE_URL`, `TOGAVSU_ENCRYPTION_KEY` (standard base64 of 32 bytes), `TOGAVSU_DOMAIN`, and `TOGAVSU_SUPERVISOR_SECRET`. `internal/bootstrap` is the only package that may call `os.Getenv`.

`settings.CheckModule` is a conservative source heuristic for AC-030, not acceptance of that criterion. It flags an integer literal used as configuration — a const, assignment, composite field, comparison, return, call, or switch case — even when the number is not today's registry default. Changing 5 to 4 does not hide it. Arithmetic, shifts, a leading minus, empty `len`/`cap` checks, loop counters, and `return 0, err` are not configuration. Other fixed rules are named by file and function in `fixedSite`; a different function in that file is still reported. A listed function can hide a later literal, which is recorded in `PONYTAIL-DEBT.md`. Product CI should run `go test ./internal/settings/`. Full AC-030 acceptance waits on the release build.

Fixed rules are not settings: the trust ledger stays append-only, the audit log is kept, a below-zero trust entry is never blocked, and permission checks stay in the service layer.

## Repositories

`internal/repository` is the persistence surface for every section 5 record. `TrustLedger`, `AuditLog`, `Events`, `FileVersions`, `FileRenditions`, and `AICalls` omit update and delete. A reversal or a new file version is an insert. Setting writes go through the registry, not a second update API.

Omitting those methods does not stop raw SQL or the table owner. Version 1 has no trigger and no privilege revoke. `TestAppendOnlyNotEnforcedAtDatabase` rewrites a real row and shows DELETE is permitted for the application role. That test is the gap, not acceptance. Hostile UPDATE and DELETE must be rejected, including by a role that is not the table owner, before the owning task's acceptance. This branch does not implement those repositories.

| Table | Owner task | Acceptance | Required before acceptance |
| --- | --- | --- | --- |
| `audit_log` | identity | AC-024 | Reject UPDATE and DELETE of an audit row, including raw SQL from a non-owner role. |
| `events` | practice | AC-024 | Reject UPDATE and DELETE of a timeline row, including raw SQL from a non-owner role. |
| `trust_ledger` | trust | AC-006 | Reject UPDATE and DELETE of a ledger row through every path. Reversals stay inserts. |
| `file_versions`, `file_renditions` | files | AC-039 | Reject any change to a saved version or rendition. A new save is a new row. |
| `ai_call_log` | modules | AC-031 | Reject UPDATE and DELETE of a call-log row. Integration acceptance must not treat the interface as that proof. |

Reference seed fills settings defaults and the 17 Doc Tender categories. `db.ApplySynthetic` fills every section 5 table with obvious fake rows for tests. Production first-run does not call it. `db.ReadSetup` is complete only when the firm name, an active admin, and that admin's confirmed TOTP are stored.
