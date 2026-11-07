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

## Settings

`GET /api/v1/settings` lists every registry row: key, type, tab, value, default, help. `PATCH` and `POST /reset` are admin, apply on the next request, and land in the audit log. `security.totp_required.admin` is locked. Secret values are write-only; the row returns `configured`. The key list and starting values are `x-togavsu-settings` on the OpenAPI document. Where the product scope names a default, that default is the starting value. Where it names the setting and not the picture, the help text says the starting value is a contract choice.

## Events

A successful write lists its event types on `x-togavsu-events`. Modules subscribe in the manifest. Delivery is the webhook `moduleEvent`: HMAC-SHA256 of the raw body in `X-Togavsu-Signature` (`sha256=` + hex) and `X-Togavsu-Timestamp`. Retries use `webhooks.retry_attempts` and `webhooks.retry_window_hours` (start 8 attempts over 24 hours).
