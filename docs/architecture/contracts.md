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

Read and write tunables through `settings.Registry`. `Get` reads the store every time. `Set` validates the range, `Reset` deletes the override, and the value and audit record commit in one transaction. A cancelled context or a failed audit leaves the previous value in place. Secret values are AES-256-GCM sealed when the store is wrapped with `settings.NewSealingStore`, and the audit record stores `[redacted]`. A non-secret value is stored as written, including text that begins with the seal prefix.

The only environment variables are `TOGAVSU_DATABASE_URL`, `TOGAVSU_ENCRYPTION_KEY` (standard base64 of 32 bytes), `TOGAVSU_DOMAIN`, and `TOGAVSU_SUPERVISOR_SECRET`. `internal/bootstrap` is the only package that may call `os.Getenv`.

`settings.CheckModule` is the AC-030 check. Product CI should run `go test ./internal/settings/`.

Fixed rules are not settings: the trust ledger stays append-only, the audit log is kept, a below-zero trust entry is never blocked, and permission checks stay in the service layer.

## Repositories

`internal/repository` is the persistence surface for every section 5 record. `TrustLedger`, `AuditLog`, `Events`, `FileVersions`, `FileRenditions`, and `AICalls` are append-only. A reversal or a new file version is an insert. Setting writes go through the registry, not a second update API.

Reference seed fills settings defaults and the 17 Doc Tender categories. `db.ApplySynthetic` fills every section 5 table with obvious fake rows for tests. Production first-run does not call it. `db.ReadSetup` is complete only when the firm name, an active admin, and that admin's confirmed TOTP are stored.
