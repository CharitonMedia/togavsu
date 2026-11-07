# API conventions

The core speaks JSON at `/api/v1`. The document is OpenAPI 3.1.0, `info.version` 1.0.0, and will be served at `GET /api/v1/openapi.json`. Money is integer cents. Identifiers are UUIDv7. Timestamps are UTC. Calendar dates are `YYYY-MM-DD`. Display uses the firm time zone from settings.

## Auth

Two schemes, either one is enough:

- Cookie `togavsu_session` for the browser. HTTP-only, Secure, SameSite=Lax.
- `Authorization: Bearer` for agents and modules. The admin issues the token, ties it to a user or a module, and can revoke it.

A bearer token must hold every scope on the operation, and the role check still runs. An admin issues API tokens. The new token's scopes cannot exceed that issuing credential or the bound user's role or the module's approved permissions. The owner can still read and revoke their own token. Resetting two-factor returns a single-use enrollment token and keeps the current secret until that token's code is confirmed. A session is authorized by role. Sending both the cookie and a bearer token on one call returns `ambiguous_auth`.

Unsafe cookie calls send `X-CSRF-Token`. Bearer calls omit it. Missing CSRF on a cookie write returns `csrf_required`.

Public operations set `security: []`: OpenAPI, `/llms.txt`, health, first-run setup, login, TOTP, recovery, password reset, and the secret iCal URL.

Roles are admin, attorney, staff, accountant, and client. The service layer checks them. A matter wall removes the matter from lists, search, widgets, reports, and direct reads. The direct read returns `not_found`, the same body as a missing row. The audit log, webhook delivery history, activity feed, AI call log, file history, and report exports omit a row whose underlying record is walled or outside the caller's read scope. Admin does not bypass that wall. Sidebar badge counts use the same rule and drop a walled matter before the count. Replacing a wall uses that same check before the membership changes, so an off-wall admin, including a module acting for that admin, cannot clear the wall or add themselves. An open matter can gain its first wall. A denied replacement writes nothing and emits no event. Attorney-only audit values and summaries are `{ "redacted": true }` for a caller who cannot read them.

Attorney-only values (SSN, EIN, conflict reasons) use `{ "redacted": true }` or `{ "redacted": false, "value": "..." }`. Search does not match those fields for a caller who would see them redacted.

An accountant can read billing, trust, reports, and file records that are receipts or invoice PDFs. Accountant is absent from billing and trust writes. A client reaches shared items on their own matters. The user guide returns `not_found` for a client.

`access_inherits` says which row carries the wall. `matter` hides a record whose matter is walled. `file` hides a record whose file is on a walled matter, and it also hides an attorney-only file from staff. File reads and file writes use `file`, including rename, move, status, trash, versions, locks, editor runs, and conversions. A conflict record with no matter stays visible. A bulk write checks every referenced record first. One inaccessible matter returns `not_found` and writes nothing.

A person with `files:write` may still rename a file or correct category and matter after those fields are person-locked. The lock stops Doc Tender from replacing the person's value. It does not stop the person.

Module help writes accept the module's own `help:write` token for its heading. That token is not an admin. A human session is forbidden on those three writes.

A trust post picks the bearer scope from `entry_type_scopes`. `trust:write` posts a deposit. A withdrawal, transfer to operating, refund, or adjustment needs `trust:withdraw`. A deposit-only token is `scope_denied` on those types, including inside a bulk batch, and the batch writes nothing. Role checks still apply. An adjustment amount is signed cents: positive credits the client and negative debits the client. Other entry types stay zero or greater.

A module token calls a core operation only when `module_delegation` is set and the module's approved scopes include that operation. Interactive calls carry `X-Togavsu-Context-Token` for the viewer. The result is the intersection of the approved scopes and the viewer's role, matter wall, ownership, sharing, attorney-only flag, and field restrictions. A scope does not open a walled matter or an attorney-only file the viewer cannot see, and it does not make the module an admin.

Background work uses `delegated_viewer_or_noninteractive` only for a workflow the core issues and delivers. It does not require an iframe token, and it is not an ambient admin or firm-wide grant. For `file.version_created`, `moduleEvent` delivers `X-Togavsu-Task-Authorization` only to an installed module whose subscription includes that event. The grant names that module, event, file, version, operations, and fields. The module then calls `getFile`, `listFileVersions`, `downloadFileVersion`, and `quarantineFileVersion` for that version. `getFile` returns `FileGrantView`. `listFileVersions` and `quarantineFileVersion` return `FileVersionGrantView`. Those objects are complete: their required fields are granted metadata, and an ungranted field such as `origin`, `created_by`, `storage_backend_id`, or `saved_by` fails validation. An unrelated matter, file, or version is `not_found`. A missing scope is `scope_denied`. An invalid, expired, or revoked grant, or a different module id, is `forbidden`. A revoked module token is `unauthenticated`. A field outside the grant is `field_locked`. An attorney-only field stays `not_found`.

`payment.processor_authorized` is the other delivered grant. `initiateProcessorPayment` is a staff call. It does not create a payment. The core delivers `processor_authorization` to the registered processor named in the request. The callback then calls `createPayment` and `updatePaymentStatus` with no viewer session. The grant binds module, initialization, payment, matter, contact, invoice, amount, and destination. Operating needs `payments:write` on the caller and the module. A trust destination also needs `trust:write` on both before the grant is delivered. An unregistered processor gets no grant. A missing, expired, or revoked grant, a different module, or a different amount or destination is denied and writes nothing. The core reserves the payment id when it authorizes the processor. `createPayment` binds that id once. A later create for the same initialization replays the original payment when the body matches, and returns `conflict` when it does not, whether the idempotency key is the same, different, or omitted. The same `Idempotency-Key` and a different body still return `idempotency_conflict`. Neither path inserts a second payment or trust row. A repeat completion callback does not post a second trust row. A create or bulk item that omits status is pending and does not post trust. `payment.created` is emitted when the row is inserted. `payment.completed` is emitted only when the resulting status becomes completed, including a later status update and each completed bulk item. A pending or failed create does not emit it. A replay emits nothing. A create that omits status starts at pending and does not post trust. Pending may become completed or failed. The same status replays. Completed, failed, and reversed do not regress through `updatePaymentStatus`. A settled correction is `reversePayment`, which needs `trust:withdraw` when trust was posted, and status `failed` is not that correction.

A completed payment whose destination is trust, including the first completion through `updatePaymentStatus` and any such item in `bulkPayment`, needs `trust:write` in addition to `payments:write`, and it needs `trust_account_id` of an active authorized account at this firm. A missing, inactive, unauthorized, mismatched, or other-firm account fails before any write. Operating payments omit `trust_account_id`. Two trust payments name their own accounts and the ledger rows follow those accounts. The check runs before any payment or ledger write. One failed item rejects the batch and writes nothing. A trust-linked `reversePayment` also needs `trust:withdraw` before the payment status changes and before the trust handler appends the one reversal on that same account. An operating reversal needs only `payments:write`. A below-zero reversal still posts and flags. The original trust row is not edited.

A client context token is for the caller's own shared matter. It keeps the upload sequence, including `getFileUpload` for that caller's upload, and it also allows every published client read: shared files, their downloads, and the client's own invoices. Another client, an unshared file, a walled matter, or an attorney-only file is `not_found`. The module still needs the operation's scopes. The token does not grant firm roles or admin writes.

Every core file-byte response writes `file.downloaded` to the audit log and the case timeline. That includes version bytes, renditions (Markdown, PDF preview, grouped PDF), zip archives, and the invoice PDF. A client download is highlighted. A quarantined file requires confirmation, and the operation exposes that input: `confirm` or `confirm_quarantine` on the query, or `confirm_quarantine` on an archive body. Confirmation does not bypass a wall, an unshared file, or an attorney-only file. The rule follows the response, not a `/files` path prefix. Backup archives and the user guide are not matter-file downloads.

Contact, matter, time entry, and expense creates take `custom_fields` on the same body as the record. Bulk creates use that same write schema. Required, active, practice-area, type, pick-list, and contact-link checks run before the transaction. One invalid or missing value rejects the batch and writes no record and no custom value. Unknown and retired definitions are rejected. A later custom-field PUT is not the create path.

A published file version emits `file.version_created` after the transaction commits, one event per version. That covers upload publication, a new version, a copied-forward version, conversion, an editor run, and bulk publication, which is also how an importer publishes. A rejected or rolled-back transaction emits nothing and issues no scan grant. Staging bytes with `completeFileUpload` does not emit `file.version_created`.

When time is replaced, invoice and payment operations follow that module. A billing post that also moves trust still posts exactly one append-only trust row through the trust handler. The time module does not post a second row. A below-zero result still flags.

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

`completeFileUpload` stores bytes and sets the upload to `staged`. It does not create a file, a version, or a Doc Tender job, and it does not emit `file.version_created`. `publishFile`, `addFileVersion`, `restoreFileVersion`, `convertFile`, `runEditor`, and `bulkFile` publish versions. Each publication is one transaction. After it commits, every new version emits `file.version_created`. A failure leaves the upload staged and emits no version event. Importers use `bulkFile`.

`createBackupUpload` accepts an encrypted archive, including one returned by `downloadBackup`, and returns `upload_token`. `createRestore` requires `passphrase` and either `backup_id` or that token.

A draft invoice's lines, fee-item selection, line description, line amount, and adjustment are `updateInvoiceDraft`. The invoice must be `draft`. Metadata on an existing file, including `attorney_only`, `shared_with_client`, category, and matter, is `updateFileMetadata` and does not add a version. A person-locked field returns `field_locked`.

## Settings

`GET /api/v1/settings` lists every registry row: key, type, tab, value, default, help. `PATCH` and `POST /reset` are admin, apply on the next request, and land in the audit log. `security.totp_required.admin` is locked. Secret values are write-only; the row returns `configured`. The key list and starting values are `x-togavsu-settings` on the OpenAPI document. Where the product scope names a default, that default is the starting value. Where it names the setting and not the picture, the help text says the starting value is a contract choice.

## Events

A successful record mutation lists its event types on `x-togavsu-events`. Those events are emitted only after the transaction commits. A denied or rolled-back call emits nothing. `putFileChunk` is the one write exemption: it transports bytes and does not commit a file. `completeFileUpload` emits `file_upload.staged`, and publish emits `file.version_created`.

Modules subscribe in their manifest. Delivery is the webhook `moduleEvent`. Scope, matter wall, and attorney-only checks run before the body is serialized. The read scope is the one derived from the operation that emits the event (for example `calendar_event.created` requires `calendar:read`). An event with no derived domain read is not delivered, and `events:read` is not a substitute. `doc_tender.exception_opened` is delivered with `doc_tender:read` even though no HTTP write emits it. An unknown event is not delivered. A module without one of those scopes, or off the matter wall, or facing an attorney-only conflict record, receives no POST. An attorney-only summary is `{ "redacted": true }`, the shared `RedactedText` object used on events, webhooks, and history. A webhook that carries `task_authorization`, `processor_authorization`, or `background_file_authorization` is serialized only when that grant's `module_id` is the recipient. A different module, or an unknown capability, gets no POST. `issueFilePublicationGrant` is a service-principal issuance. The module bearer, `files:write`, and the firm's configured matter list are enough for the first grant and for a renewal after expiry. It does not require a viewer or an existing task grant, and an existing grant cannot mint another one. The same call can instead ask for unassigned ingestion: the file is created with no matter, owned by the module, and the module cannot assign a matter or read another module's unassigned files. `file.background_authorized` delivers that grant to the same module. Sidebar entries, matter-screen panels and actions, and quick-create items stay module-local: `/m/{module_id}/{target}`, refused when the path leaves the module or the caller lacks the entry's role or scope. A widget target is that same module-local page or an allowlisted core page with typed filters. Opening the core page checks that page's role, scope, and matter wall, not only whether the widget is visible. External, `javascript:`, `data:`, network, traversal, and other-module targets are refused. Help pages name a heading, Markdown, keywords, and the widget, tab, or screen they cover. The owner namespace is the module id. Tabs are home-screen or matter-screen, with a module-local target. Clients receive not_found for guides. The core sanitizes Markdown when it renders it. Update replaces the module's pages, disable hides them, and uninstall removes them. Registering those manifest pages at install remains pending. Optional `data` on the manifest names migrations, extension tables keyed by the core record id, custom fields owned by the signed module id, and published views. A module that stores nothing omits `data`. Applying those migrations and registering the custom fields are pending. Manifest permissions are `{scope, required}` after signature verification. A string in the signed manifest is shorthand for required true. A published view scope `views:{module}.{view}:read` is accepted only for a registered provider view, with no wildcard. A declared extension table with no published view is included for its owning module or for a viewer who can already read the core record. Another module cannot include that table. Declining a required scope cancels the install. Declining an optional scope installs a token that contains only the approved scopes. An update keeps the running version's approved scopes until the new version is activated. A proposed manifest that drops one scope and adds another does not change that running token while approval is pending. An explicit decline removes that scope from the running token. After activation, scopes the new manifest no longer requests are gone. A new scope is not approved until a decision says so. A required flag sent with the approval is ignored. Include checks the current approved scopes on every call. Keyed HMAC-SHA256 of the raw body bytes is `X-Togavsu-Signature` (`sha256=` plus lowercase hex). A plain hash of the body is not a signature. `X-Togavsu-Timestamp` is unix seconds. Retries use `webhooks.retry_attempts` and `webhooks.retry_window_hours` (start 8 attempts over 24 hours).

Email, Zoom, drop-folder, and importer modules publish files with a core-issued background grant after the viewer context expires. The grant is `files:write` for one matter and the upload sequence through `publishFile` or `bulkFile`. It is not an admin grant. An expired grant or another matter creates no file and emits no event.

`applyTrustToInvoice` and `applyFlatFee` name `client_id` and `trust_account_id`. The ledger entry debits that account for that client. Another account on the same matter is left alone.
