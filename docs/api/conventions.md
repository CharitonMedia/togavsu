# API conventions

The core speaks JSON at `/api/v1`. The document is OpenAPI 3.1.0, `info.version` 1.0.0, and will be served at `GET /api/v1/openapi.json`. Money is integer cents. Identifiers are UUIDv7. Timestamps are UTC. Calendar dates are `YYYY-MM-DD`. Display uses the firm time zone from settings.

## Auth

Two schemes, either one is enough:

- Cookie `togavsu_session` for the browser. HTTP-only, Secure, SameSite=Lax.
- `Authorization: Bearer` for agents and modules. The admin issues the token, ties it to a user or a module, and can revoke it.

A bearer token must hold every scope on the operation, and the role check still runs. A session is authorized by role. Sending both the cookie and a bearer token on one call returns `ambiguous_auth`.

Unsafe cookie calls send `X-CSRF-Token`. Bearer calls omit it. Missing CSRF on a cookie write returns `csrf_required`.

Public operations set `security: []`: OpenAPI, `/llms.txt`, health, first-run setup, login, TOTP, recovery, password reset, and the secret iCal URL.

Roles are admin, attorney, staff, accountant, and client. The service layer checks them. A matter wall removes the matter from lists, search, widgets, reports, and direct reads. The direct read returns `not_found`, the same body as a missing row.

Attorney-only values (SSN, EIN, conflict reasons) use `{ "redacted": true }` or `{ "redacted": false, "value": "..." }`. Search does not match those fields for a caller who would see them redacted.

An accountant can read billing, trust, reports, and file records that are receipts or invoice PDFs. Accountant is absent from billing and trust writes. A client reaches shared items on their own matters. The user guide returns `not_found` for a client.

`access_inherits` says which row carries the wall. `matter` hides a record whose matter is walled. `file` hides a record whose file is on a walled matter, and it also hides an attorney-only file from staff. File reads and file writes use `file`, including rename, move, status, trash, versions, locks, editor runs, and conversions. A conflict record with no matter stays visible. A bulk write checks every referenced record first. One inaccessible matter returns `not_found` and writes nothing.

A person with `files:write` may still rename a file or correct category and matter after those fields are person-locked. The lock stops Doc Tender from replacing the person's value. It does not stop the person.

Module help writes accept the module's own `help:write` token for its heading. That token is not an admin. A human session is forbidden on those three writes.

A trust post picks the bearer scope from `entry_type_scopes`. `trust:write` posts a deposit. A withdrawal, transfer to operating, refund, or adjustment needs `trust:withdraw`. A deposit-only token is `scope_denied` on those types, including inside a bulk batch, and the batch writes nothing. Role checks still apply. An adjustment amount is signed cents: positive credits the client and negative debits the client. Other entry types stay zero or greater.

A module token calls a core operation only when `module_delegation` is `delegated_viewer` and the module's approved scopes include that operation. The call also carries `X-Togavsu-Context-Token` for the viewer. The result is the intersection of the module scope, the viewer's role, and that viewer's walls. A scope does not open a walled matter or an attorney-only file the viewer cannot see, and it does not make the module an admin.

Replaceable domains are time, trust, and calendar (`x-togavsu-replaceable-domain`). Switching the handler does not change these paths. Core rows stay readable. A trust module still posts a below-zero entry and raises `trust.flag_raised`.

## Errors

Failures use `application/problem+json`:

```json
{
  "type": "https://firm.example/problems/validation_failed",
  "title": "Validation failed",
  "status": 422,
  "code": "validation_failed",
  "detail": "The batch was rejected.",
  "request_id": "018f...",
  "errors": [{ "path": "/items/0/amount_cents", "code": "required", "detail": "amount_cents is required" }]
}
```

`X-Request-Id` matches `request_id`. Rate limits add `Retry-After`. The code enum is on schema `Problem`. There is no `below_zero` code. A trust entry that leaves a negative balance posts, raises a flag, and emails the admins and the responsible attorney.

## Lists, writes, bulk

Lists take `cursor` and `limit` (default 50, max 200) and return:

```json
{ "data": [], "page": { "limit": 50, "next_cursor": null } }
```

`next_cursor` null is the last page. Some screens default the limit from settings (activity 100, notes 25). Reports and lists that set `format=csv` return `text/csv` up to `api.csv_export_max_rows`.

`include` is a comma-separated list of `module-id.view` names, for example `matter-clock.targets`. The core joins them inside the caller's access. A module table that is not keyed to the core record is rejected.

Unsafe methods accept `Idempotency-Key`. The same key and body replay the stored response for `api.idempotency_retention_hours` (start 24). The same key and a different body returns `idempotency_conflict`.

Bulk creates are `POST /api/v1/{collection}:bulk` with `{ "items": [ ... ] }`. The server validates every item, then writes one transaction. One failure writes nothing and returns `batch_rejected`. The cap is `api.bulk_batch_max` (start 500). File bulk items point at finished uploads. They do not carry bytes or storage keys.

Routers match a literal path before a parameter. `/api/v1/settings/appearance` is the appearance object, not setting key `appearance`. Colon paths such as `/api/v1/contacts:bulk` are one literal segment.

## Ledgers and files

Trust ledger entries, audit rows, events, and file versions have no update and no delete. A trust correction is `POST /api/v1/trust-ledger/{id}/reversals`. A payment correction is `POST /api/v1/payments/{id}/reverse`. Restoring a file version copies it forward as a new version.

Storage keys stay off the API. Downloads check SHA-256. A mismatch fails the read and emails the admins. A quarantined version downloads only with `confirm=true`.

`completeFileUpload` stores bytes and sets the upload to `staged`. It does not create a file or a Doc Tender job. `publishFile`, `addFileVersion`, and `bulkFile` attach staged uploads. Each of those is one transaction: a failure leaves the upload staged and creates no file.

`createBackupUpload` accepts an encrypted archive, including one returned by `downloadBackup`, and returns `upload_token`. `createRestore` requires `passphrase` and either `backup_id` or that token.

A draft invoice's lines, fee-item selection, line description, line amount, and adjustment are `updateInvoiceDraft`. The invoice must be `draft`. Metadata on an existing file, including `attorney_only`, `shared_with_client`, category, and matter, is `updateFileMetadata` and does not add a version. A person-locked field returns `field_locked`.

## Settings

`GET /api/v1/settings` lists every registry row: key, type, tab, value, default, help. `PATCH` and `POST /reset` are admin, apply on the next request, and land in the audit log. `security.totp_required.admin` is locked. Secret values are write-only; the row returns `configured`. The key list and starting values are `x-togavsu-settings` on the OpenAPI document. Where the product scope names a default, that default is the starting value. Where it names the setting and not the picture, the help text says the starting value is a contract choice.

## Events

A successful write lists its event types on `x-togavsu-events`. Modules subscribe in the manifest. Delivery is the webhook `moduleEvent`: HMAC-SHA256 of the raw body in `X-Togavsu-Signature` (`sha256=` + hex) and `X-Togavsu-Timestamp`. Retries use `webhooks.retry_attempts` and `webhooks.retry_window_hours` (start 8 attempts over 24 hours).
