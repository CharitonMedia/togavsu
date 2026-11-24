# Module API

The manifest `id` must identify a module. Core-only source and identity labels are
reserved: `core`, `upload`, `client_upload`, `editor`, `billing`,
`drop_folder`, `ui`, `api_token`, `module`, `service`, `system`, `api`,
`manual`, `timer`, `import`. Manifest/kit validation, local and marketplace installs,
catalog publishing, contract links and module sender signing refuse them. Validation
errors identify the reservation; only core's dedicated signer may sign as `core`.
Approved module IDs remain valid, including `email`, `zoom` and `importer`, even
when a file's `source` uses the same label. Ownership uses `owner_module_id`.
`TestModuleManifestReservedIDsContract` compares the published manifest description
with every `ReservedModuleID` switch case, including labels with underscores.

Public module API additions from `togavsu-initial-modules-prd.md` §4 and `docs/modules/CORE-CHANGES.md` (CORE-01–CORE-29). Operation shapes, scopes and `module_delegation` live in `api/openapi/v1/openapi.json` and `api/openapi/v1/mcp-tools.json`; MCP parity is described in [mcp.md](mcp.md). Conventions for walls, grants and service principals are in [conventions.md](conventions.md).

## Service principals (CORE-01)

A module bearer token with no `X-Togavsu-Context-Token` is the module's service principal. Only operations marked `service_principal`, `delegated_viewer_or_service_principal` or `delegated_viewer_or_noninteractive` may run that way; every other operation answers `scope_denied`. A junk `X-Togavsu-Task-Authorization` header does not open an unmarked write: only `delegated_viewer_or_noninteractive` skips the service-call gate so the handler can verify the grant.

## Books reader (CORE-02)

Scope `books:read` (module tokens only) elevates marked money and books reads to the accountant field set, firm-wide except walled matters. Walled matters are omitted from lists, `not_found` on gets, absent from trust balances and dropped from event delivery; lifting the wall returns the records on the next read with no catch-up events. Routes include `GET /api/v1/books/profile`, `/books/matters/{id}` and `/books/contacts/{id}`.

## Notifications (CORE-03)

`POST /api/v1/notifications` creates one row per resolved recipient. Manifest `notifications` kinds are `{module_id}.{kind}`. Recipients are `user_id`, `audience` on a matter, or `recipient_roles` (admin/accountant) on a service call. A service call that names a walled `matter_id` is `not_found` (no created count from wall membership).

## Module and system mail (CORE-04, CORE-05)

`POST /api/v1/mail/messages` (`mail:send`) resolves addresses in the core and never returns them; timeline `mail.sent` carries the subject only. System mail may route through a module with `container.system_mail` and `mail:system`, with SMTP fallback.

## Secrets, inbound webhooks, sidebar (CORE-06–CORE-08)

Module secrets are `PUT`/`GET`/`DELETE /api/v1/modules/{id}/secrets/{name}` (`module_secrets:write`), sealed with the core key. `PUT` with `If-None-Match: *` creates only when absent (409 `conflict` when the name exists) so `pkg/modulekit/forms.HTTPSecretStore` / `EnsureKey` cannot race-overwrite a sealed AES key. Public inbound webhooks are `POST /hooks/{module_id}/{hook_id}` only: the core strips inbound credentials, strips `Set-Cookie` on the response, and adds the same CSP sandbox as `/m/` so module HTML never runs in the core origin. Sidebar entries gain section, subgroup, icon, order and badge fields; `GET /api/v1/nav/context` fills badges as the viewer.

## Frame bridge (CORE-09)

Staff and client frames listen for `togavsu:editor.open`, `togavsu:navigate` and `togavsu:context.refresh`. Navigate allows a tab/widget/matter-item of the same module, a core page, or a staff tab of another enabled module with an admin-approved contract link in either direction (see core update 2i below). Context refresh uses `…/context-token` on the frame path and keeps the query string (including `matter_id`). Staff routes: `GET /modules/{module_id}/tabs|widgets|matter-items/…/context-token`. Client: `GET /portal/{module_id}/{tab_id}/context-token`. Neither frame kind gets `allow-same-origin` on the module iframe.

## Client portal (CORE-10–CORE-15)

Client tabs use `placement: client`, `/portal` and `/portal/{module_id}/{tab_id}`. Client matter reads, trust reads (`/api/v1/me/trust`, `/me/trust-ledger`), contact self-edit (`/api/v1/me/contact`), client access management (`client_access:*`), and appointments (`/api/v1/me/appointments`) apply the same walls and the `client_access.days_after_close` window.

## Files, firm profile, directory, settings, archives (CORE-16–CORE-21)

Module publication may set file `origin`. Library files have no matter or contact. `firm:read` and `users:directory` expose firm profile and the user directory. County, practice area, party role and custom field definition reads accept a module viewer call. Archive `entries` are checked as the viewer before the first byte.

## Linkable records, processors, refunds, signatures (CORE-22–CORE-25)

Modules may link to `fee_agreement`, `payment_refund` and `trust_account`. Payment processors register via `payment_processor` and use delivered `processor_authorization` grants on `createPayment` / `updatePaymentStatus`. Refunds write `payment_refunds` (and payment refund columns), support pending `refund_authorization` grants, and post trust refunds under the below-zero rule. The signature module recorded on a fee agreement may move sent→signed; a service call on a walled matter is `not_found`.

## Mail match, filing grants, devices, harness (CORE-26–CORE-29)

`POST /api/v1/mail/matches` and `POST /api/v1/mail/filing-grants` omit walled matters for service calls. Device grants exchange for short-lived context tokens. `pkg/modulekit/coretest` is the composed test core for module repositories.

## Contributor gap fixes and wider service calls (CORE-36, CORE-30)

CORE-36 closes the public-kit gaps this batch hits: `/m/` forwards `X-Togavsu-Context-Token`; `GET /api/v1/me` accepts a context token as the viewer; a missing scope answers `scope_denied` naming the scope; webhook signatures authenticate the timestamp and raw body (`t=<unix>,v1=<hex>`) within a five-minute window; notes land in `core.notes`; invoices, payments and expenses dual-write into `core.invoices` / `core.payments` / `core.expenses`; conflict decisions emit `conflict_check.decided`; `getConflictCheck` accepts a service principal; `Accepts().core_items` lists built CORE items; coretest supplies Markdown renditions and an in-process converter stand-in.

CORE-30 marks the wider service-call set `delegated_viewer_or_service_principal` (contacts, matters, conflicts, calendar, deadlines, notes, timeline, markdown renditions). Each has `modules.service_call_limit.<operationId>` (default 120/minute). `listContact` supports exact `email` and `phone` filters. Service-principal writes record the module as author with audit source `service`. Walls still omit walled matters from lists and answer `not_found` on gets.

## Module service contracts (CORE-31)

Manifest `provides` and `consumes` name versioned contracts such as `phone.provider.v1`. The admin approves each consumer↔provider link (`POST /api/v1/modules/contract-links`, `DELETE …/contract-links/{link_id}`, `GET …/contract-links`) after both sides have approved `contracts:call` / `contracts:provide`. The consumer calls `POST /api/v1/modules/contracts/{contract}/{operation}` with scope `contracts:call`; the core proxies to the linked provider's declared path, HMAC-signed like events (`X-Togavsu-Signature`), carrying `X-Togavsu-Context-Token` when present. Operation names `events` and `providers` are reserved (they collide with `POST …/events` and `GET …/providers`) and are refused at manifest validation. On a many-provider contract the consumer lists linked provider module ids with `GET /api/v1/modules/contracts/{contract}/providers` and selects one with `X-Togavsu-Contract-Provider` (module id only; never an address). When the header is omitted the earliest link by `created_at`/`id` is used. Providers push events with `POST /api/v1/modules/contracts/{contract}/events` (`contracts:provide`); the binding batch 2 event envelope broadcasts unchanged to approved linked consumers; the existing targeted envelope is also accepted unchanged. See the event shapes below. One provider per contract per firm unless **every** linked provider declares `many: true` (a `many: false` provider already linked blocks a second provider even if the new one sets `many: true`). Modules never learn each other's address. Unlinked calls answer `contract_unlinked`. Every proxied call and pushed event writes an audit record (consumer, provider, contract, operation/action, result) without any address. Body size is limited by `modules.contract_call_max_bytes` (25 MiB). Disabling a module keeps its approved links; removing it deletes them.

## Public module pages (CORE-32)

Manifest `public_pages` are served at `/p/{id}/…` with no sign-in under `modules.public_page_rate_per_minute` (60) and `modules.public_page_max_bytes` (10 MiB). The per-IP rate limit runs before the body is read and before opaque-origin OPTIONS preflights are answered, so a client already over the limit gets `429` without the core buffering up to `modules.public_page_max_bytes`. The core sets an HttpOnly CSRF cookie and passes a matching HMAC-signed token to the module as `X-Togavsu-Public-CSRF` on every proxied request so the module can embed it in forms, HTMX (`X-CSRF-Token` / `csrf`), and offline autosave replays. **Every** proxied response (HTML, SVG, XML, empty Content-Type) carries a CSP sandbox **without** `allow-same-origin`. Opaque-origin `Origin: null` preflight (`OPTIONS`) and responses get CORS (`Access-Control-Allow-Origin: null`) on `/p/` and on `/m/` (frame proxy only) so HTMX autosave works. Domain-replacement routes never answer that CORS: they mint a viewer context token from the staff session, and sandboxed frames must not read those responses. POSTs are checked by verifying the signed CSRF token; the cookie is optional defense-in-depth. Each public-page request issues a **core-signed** public credential (`X-Togavsu-Public-Step`). Public-page server calls must present that credential as `Authorization: Bearer` (preferred) or as `X-Togavsu-Public-Step` beside the module install token; the core limits those calls to `public_operations` on every route (including module runtime secrets/settings, contract calls, and viewer-delegated calls) **and** still requires each operation's scopes against the module's approved install scopes (CU2-01). A webhook-secret-forged step token is refused. Scope `public_pages:serve` is required at install; the admin Modules tab lists public pages and operations. Helpers: `modules.IssueStepToken` / `VerifyStepToken` / `IssueSignedPublicCSRF` / `CheckSignedPublicCSRF` (also via `pkg/modulekit`).

## Forms kit (CORE-33)

`pkg/modulekit/forms` is the public form definition, validation, HTMX renderer, autosave/offline queue, "I don't know" answers, and masked-field encryption through the module secret store. Answers stay in the calling module's schema. Whole-form autosave uses one wire shape for the HTMX renderer, `OfflineQueueJS`, and `AutosaveHandler` / `AutosaveHandlerForm`: the `<form>` carries one `hx-post` with `hx-include="this"` (debounced change/input) plus CSRF (`csrf` / `X-CSRF-Token`). `ParseFormAnswers(form, values)` is the single schema-driven parser for autosave and full submit (numbers → float64, booleans → bool with a hidden `false`, multi-select → `[]string`, dates/date-times parsed, repeating groups → ordered row arrays). An untouched masked control is stripped from the post and never overwrites stored ciphertext (`ClearMaskedValue` clears; use `AutosaveHandlerForm` with the form definition). Offline replay posts the same urlencoded whole-form body (`X-Togavsu-Forms-Offline: 1`). On opaque-origin pages the offline queue falls back to same-tab memory when `localStorage` throws, and `AutosaveHandler` answers CORS for `Origin: null`. `MergeFormSubmit` keeps a stored masked value when the posted field is empty (and still applies `Unknown=false` so unchecking "I don't know" on a masked field clears the flag without wiping ciphertext). "I don't know" renders a hidden `name.__unknown=0` plus checkbox `=1` so unchecking posts an explicit clear; `ParseFormAnswers` turns Unknown off on `0`/`false`, and `UnknownToggleJS` re-enables disabled controls. `SealAnswers` skips values already sealed under the field key; a typed `enc:v1:` prefix that does not decrypt is encrypted as plaintext. Repeating-row indices above `MaxRepeatingIndex` (`domain.FormsMaxRepeatingIndex`) are ignored. `EnsureKey` creates a key only on `ErrSecretNotFound`, preferring `CreateSecretIfAbsent` when the store implements it (`HTTPSecretStore` uses `If-None-Match: *` against the core secret API), otherwise put-then-re-read; `EncryptMasked` verifies decrypt under the stored key and retries across a racing key replace.

## Staff banner slot (CORE-34)

Manifest `staff_banner` reserves a fixed-height sandboxed frame above every staff page. Scope `banner:show`. Admin order is `modules.staff_banner_order` (`PUT /api/v1/modules/banner-order`, `GET /api/v1/modules/banners`, and the Modules tab form). Height is set with the iframe `height` attribute and stepped `.staff-banner-h-N` classes in `web/static/app.css` (staff CSP is `style-src 'self'`; no inline `style` attributes). The frame bridge accepts `togavsu:banner.show` / `togavsu:banner.hide` only from that module's banner iframe on the same origin.

## Manifest price (CORE-35)

The manifest `price` field accepts the legacy string or a structured value `{amount_cents, currency, interval, note}` (`interval` is `month` or `year`). The core stores it on the module record, returns it on `Module` (`price`, `price_display`), and shows `price_display` on the install screen. The core never charges, checks or enforces a price. Marketplace listings take the same field at upload (see CORE-CHANGES-2 §17.1).

## Opening invoice import (CORE-37)

Migration import (core PRD 8.22 / CORE-CHANGES-2 §6): the admin **Migration import** tab writes opening balances through the existing `bulkInvoice` operation (`POST /api/v1/invoices:bulk`, also on `/mcp`). Each `InvoiceWrite` item may carry `source` (`form`, `csv` or `api`), `external_ref` (unique per firm; a re-run skips already-imported rows), `document_of_record_file_id` and optional `due_on`. Do not send response-only `imported` / `import_source` on write. `preview=true` returns HTTP 200 with per-row results and writes nothing; a commit returns 201 with `data` and the same per-row `results` (zero-based `index`, one-based `row`, `ok`, optional `error` or skip reason). Rejected commits also return every row result and write nothing. Duplicate `external_ref` values inside one file are row errors; only earlier runs qualify for a skip. Preview executes the real commit transaction, then rolls it back, including file sharing, audit and events. Opening imports land as `sent` with one adjustment line `Balance forward as of [date]`, `imported: true`, optional amount-paid as one `other` payment with `Payment.imported` true (no money moved), and emit `invoice.imported` (and `invoice.created`). Numbers at or above `billing.invoice_number_start`, duplicates, non-positive amounts and missing matters are refused with a named reason and write nothing. When `billing.invoice_number_start` is 0, the floor is the lowest non-imported invoice number already in the practice; with no normal invoices yet, opening import is refused until the admin sets that value. Preview runs the same date, recompute and imported-payment checks as commit. Imported numbers never advance the normal sequence; the next draft still skips any number already taken. The client portal, statements and matter billing tab show the document of record for an imported invoice.

An imported invoice pins `document_of_record_version_id` at import time; later uploads or restores of the file never change the invoice PDF. These file/version IDs are response fields on staff and client invoice reads. `getInvoicePdf` answers 303 with a `Location` naming that version by default, or 200 PDF bytes with `Accept: application/pdf` (also used by MCP). Both forms enforce the viewer's invoice and file access. Existing imports are backfilled to the latest file version saved by the invoice's creation time; unavailable historical bytes stay unavailable.

## Core-update-2d background work

The public kit's [background-work guide](../modules/MODULE-KIT.md#core-update-2d-background-reads-and-callback-work)
now documents service-principal deadline/calendar/note gets and lists, file lists, contact-filtered matter
parties, addresses, roles/field definitions, firm time zone and the user directory.
Each retains its existing read scope, gains a per-operation service-call limit, and
writes an audit row under the module id. `user-directory?matter_id=` checks caller and
candidate access; non-client rows include `work_email`.

Walled gets require a context token for a viewer inside the wall. A service principal
gets `not_found` and its lists omit those records, including after event delivery.
Events confer no extra read access, and responses disclose no wall membership.
`mail:send` allows service sends to contact parties on a non-walled named matter.

Module own-settings setup writes use `PUT /api/v1/modules/{id}/own-settings`
(`putOwnModuleSettings`, `module_settings:write`, admin viewer). `modules:write` stays
forbidden to modules. Own reads stay at `/settings`; module `settings:read` exposes only
applicable contract runtime settings, including `modules.contract_call_max_bytes`,
when the module has an approved contract permission.

Forwarded context is signed for the selected provider and invalidated by unlinking.
Provider events use the [binding envelope or accepted targeted envelope](#core-brokered-contract-events), preserving their fields and values. Oversized provider responses are 413
`too_large`. Events, contract calls and routed system mail use
`X-Togavsu-Signature: t=<unix>,v1=<hex>`: HMAC-SHA256 of `"<unix>." + raw body`,
plus `X-Togavsu-Timestamp` with the same Unix seconds. Receivers check the authenticated
timestamp within five minutes and compare the HMAC in constant time; empty secrets fail.
Inbound hooks forward Authorization only with their per-hook `forward_authorization`
flag and approved `webhooks:authorization`. Provider Authorization is never core auth.

`GET /api/v1/modules/contact-phone-items?phone_id=` (`listContactPhoneItems`) exposes
paginated viewer-only actions. Manifest `contact_phone_items` requires `contacts:read`;
contact screens and sandboxed action frames check the same service access. Module events
are named in the shared matter timeline and Activities Feed. Purge refuses retained
module file links while keeping immutable version/audit metadata.

Every module call, including delegated calls, is audited under the module id. Delegated
actions keep the person as `actor_id`; `module_id` records the module and timeline/feed rows
show the person via the module. Background actions use the module as actor.

## Core-brokered contract events

The binding batch 2 envelope is `contract`, `event`, `event_id` (UUID),
`occurred_at` (RFC3339), `data` (JSON object), posted by the provider to
`/api/v1/modules/contracts/{contract}/events` with approved `contracts:provide`.
The body contract equals the route and event cannot use any core event namespace.
Every enabled consumer declaring that contract with approved `contracts:call` and
an admin-approved link receives the original fields/values through the durable
signed retry queue. Deduplicate permanently per recipient and `event_id`.
The 2f targeted `id`, `contract`, `consumer_id`, `occurred_at`, `action`, `payload`
envelope is also accepted unchanged, with its existing action-prefix rules and
single linked recipient. Its action must also stay outside every core event
namespace; choosing a contract with a core root does not bypass that reservation. See [full schemas and examples](../contracts/README.md).
Both deliveries bind `X-Togavsu-Sender` to the authenticated provider using the
unchanged 2e v1/v2 signatures, including after unlinking and retry. Payload data
never grants core read permission.

Contract calls carry a freshly signed provider-audience context when the consumer sent
a viewer context. The provider verifies/delegates it with its own bearer; live links,
roles, matter walls and both modules' scopes remain binding. Provider addresses never
reach consumers. `modules.contract_call_max_bytes` bounds requests and responses;
oversized provider responses return HTTP 413, code `too_large`.

These definitions document the core surface through core-update-2e. The assignment referred
to `docs/contracts/`, but that directory was absent from its base revision. Integration
must compare any separately held provider/consumer contract definitions before claiming
compatibility with them.

A service-principal `GET /api/v1/matter-parties?contact_id=…` simply leaves out walled
matters, with no indication that a wall omitted anything. Lifting the wall returns the
matter on the next read. Delegated reads apply the viewer's current access.

The kit keeps every batch 2 exported name and signature. `SignWebhook` keeps its
`sha256=<hex>` raw-body output; `SignTimestampedWebhook` signs the core's `t=,v1=` form.
`SignWebhookHeaders` returns that timestamped form and its matching timestamp header.
`VerifyWebhook` accepts both forms, checking the authenticated timestamp in `t=,v1=`.
`VerifyWebhookHeaders` also checks `X-Togavsu-Timestamp` when present; with `t=,v1=` it
must match the signed timestamp. Both use the same five-minute window and constant-time
HMAC comparison. The compatibility `sha256=` form has an unsigned optional timestamp and
cannot provide timestamp replay protection. Durably deduplicate event ids for both forms,
including retries and repetitions within the window.

## Core update 2e: authenticated delivery sender

Every core-to-module event, contract call and routed system mail now carries the
header field `X-Togavsu-Sender`: the pushing provider's module id for contract
events, the calling consumer's module id for contract calls, and `core` for
core-emitted events (including grants) and system mail. This is separate from
payload fields: `module` in a core event still names the recipient. Payload
`sender` or `provider_id` values are untrusted claims.

The core reads queued senders from `core.module_deliveries.created_by`, recorded
when the push was accepted. Historical empty values mean `core`. Retries retain
that identity and body even after provider links change. An unlink prevents new
pushes/calls; it does not reattribute a queued delivery to another provider.

The existing headers and exact v1 signature remain unchanged:

```text
X-Togavsu-Timestamp: <unix>
X-Togavsu-Signature: t=<unix>,v1=<hex>
X-Togavsu-Sender: <sender>
X-Togavsu-Sender-Signature: t=<unix>,v2=<hex>
```

Both digests are lowercase hex HMAC-SHA256 keyed by the recipient module's
nonempty webhook secret. Input bytes are UTF-8/ASCII prefixes followed by the
exact raw body, without a trailing newline:

- v1: `<unix>.<raw body>` (unchanged).
- v2: `v2.<unix>.<sender>.<raw body>` (the literal `v2.` separates signature versions).

`<unix>` is canonical decimal Unix seconds, identical in all three timestamp
positions. `<sender>` is `core` or a module id matching
`^[a-z0-9][a-z0-9-]{0,63}$`; it cannot contain a dot. The v2 signature lives in a
separate header because the 2d verifier requires exactly `t=…,v1=…` in the
original header. Modules pinned to 2d continue verifying that same delivery.

New receivers use
`sender, err := modulekit.VerifyWebhookSender(secret, request.Header, body, now, 0)`.
It requires valid timestamped v1 and v2 signatures, compares MACs in constant time,
and returns an identity only on success. Missing/duplicate sender or signature
headers, legacy signatures without binding, changed identity/body/timestamps,
empty secrets and expired signatures fail. Zero tolerance means five minutes in
either direction; a positive tolerance can only shorten that window.
`VerifyWebhookHeaders` keeps its existing behavior and does not authenticate
sender identity. Do not use an unverified sender to select a provider, matter or
record. Sender identity grants no access to core records. Deduplicate event ids
durably, including across retries, which receive fresh timestamps/signatures.

For synthetic sender-aware requests, use
`modulekit.SignWebhookSenderHeaders(secret, now, sender, body)`; it returns all
four headers, or nil for an empty secret or invalid/reserved module id. For a
synthetic core emission use `modulekit.SignCoreWebhookHeaders(secret, now, body)`.
Production core emissions use that separate signer; nonempty `created_by=core`
is a reserved module sender and is refused. In `coretest.Serve` handlers,
`sender, body, err := coretest.ReadDelivery(m, request)` reads and verifies using
the real deployment secret. `c.DeliverEvents()` uses the production queue and
sender signer for contract events, core events and retries.


## Core-update-2f document and intake operations

The public [kit guide](../modules/MODULE-KIT.md#core-update-2f-intake-and-document-generation)
explains permission `reason`, restored CSRF form bodies, occupancy-only
`calendar-events?view=busy`, additional conflict `names`, scoped service county
gets, file status and optional validated `on_behalf_of_user_id` publication.
OpenAPI and MCP describe the same input and output shapes. County reads,
conflict creation and file status each have a live per-module service-call limit.

The real `file.trashed` delivery requires `events:read`, the derived `files:read`
scope and a subscription. Its normal queued delivery is signed with sender `core`.
All non-targeted `file.*` events identifying a file (including `file.updated`,
`file.downloaded`, `file.trashed` and `file.purged`) require approved `events:read`,
a matching subscription, approved `files:read` and the module service
read policy on the retained row’s current placement, evaluated before enqueue.
Purging does not suppress `file.purged` or older pending events: the placement check
ignores only the purge marker. Matter walls, contact placement and attorney-only
restrictions still apply; hidden files yield no delivery. Ordinary file reads and
byte access still return `not_found` after purge. A missing file service or row
fails closed; other visibility errors abort enqueue. Targeted publication grants
and books-reader filtering retain their existing policies.

**Behavior change for existing subscribers:** without approved `files:read`, a
subscriber stops receiving `file.updated` and `file.downloaded`. Those events also
stop for walled, attorney-only and non-matter (contact-only or library) files that
the subscriber's service principal cannot read. A delegated viewer's access does
not grant background delivery access. The [batch 1 subscription inventory](../modules/MODULE-KIT.md#batch-1-file-event-subscriptions)
records which module specs request these events and `files:read`; it does not
replace real-package compatibility acceptance.

A module service status write is limited to its own published files (plus the
existing signed-copy callback). Ownership comes from the retained first-version
upload's explicit `owner_module_id`, committed with publication; `source` is only
provenance. Uploading a later version confers no ownership. Ownership refusals use
`scope_denied` for both existing and nonexistent IDs.

Publication checks the caller's placement access before looking up a named author.
A hidden matter/contact/file returns exactly the same `not_found` with or without
an author. After the caller may write, every author failure returns the same
`validation` error on `on_behalf_of_user_id`. The active staff/attorney/admin author
must independently be able to write the actual matter/contact and attorney-only
placement, including contact-only and library publications.

Additional conflict names are limited by live admin settings
`practice.conflict_additional_names_max` (default 20) and
`practice.conflict_name_max_chars` (default 200 Unicode characters per name).
Over-limit input returns `validation` on `names` before storing or scoring a check;
whitespace does not bypass the character limit. REST, MCP and direct service calls
share these checks.

The named `document.generated` action is accepted only for
`documents.generate.v1`, in the existing `action`/`payload` targeted envelope
above. [Contract definitions](../contracts/README.md) document it and the
binding `processor.payment.v1` pre-matter authorization/recovery protocol.
The binding `event`/`data` envelope is now accepted for every contract as well. Phone and workflow targeted deliveries and 2e sender binding remain unchanged.

Processor authorize/recover use `callModuleContract` on REST/MCP, conditional
`payments:write` on the current consumer bearer and approvals of both modules,
and admin-linked providers declaring operating payment processing. Durable
receipts bind consumer, provider, amount, destination and original request digest;
pending/uncertain attempts only recover and terminal results replay indefinitely.
This contract uses `intent_id` for durable idempotency. `Idempotency-Key` retains
its length limit but does not memoize the HTTP response: every repeat validates
current scopes and links and reads the current recovered result.
The core stores no hosted payment token and writes no matter/contact/payment in
this pre-matter broker. The consumer must continue only after approval and use
stable Idempotency-Key values for core record writes. The external processor
must durably deduplicate charges and recover results after response loss.

Processor receipt audit transitions use the stable entity ID
`module-processor/{consumer_module_id}/{intent_id}`. The audit status and existing
provenance are unchanged; no card token, card data or amount is added.


## Core update 2g: module continuations

On a successfully authenticated `/m/{module_id}/…` **GET**, the module receives
`X-Togavsu-Session-CSRF` (`modulekit.FrameSessionCSRFHeader`) derived from the
current browser session and this module ID using identity’s existing secret. It is a
frame-scoped HMAC token, never the core API CSRF token. Render it as `<input type="hidden" name="csrf" value="…">`
with HTML escaping, or use `forms.RenderOptions.CSRFToken`. An ordinary cookie-jar
form POST then passes the session-and-module frame CSRF check. `X-CSRF-Token` remains
accepted on POST. Missing/other-session/other-module tokens fail before proxying. Core `/api/v1`
writes refuse frame tokens. Core strips
caller-supplied CSRF headers, cookies and Authorization before forwarding; a
bearer-only or unauthenticated request gets no session CSRF header. Never put this
token in a URL or shared storage. Frame responses are `Cache-Control: no-store`.
This does not replace module context verification or public `/p/` CSRF.

A calendar service principal with approved **`calendar:read`** can read
`GET /api/v1/calendar-events?view=occupancy&from_time=…&to_time=…`
(or `listCalendarEvent` on MCP). Assigned and unassigned events on unwalled
matters, and events with no matter, are included. The approved 2h exception also
includes walled events with no assigned attendee user. **Core update 2i defines
assigned as having at least one active attendee user**. Deactivated users do not
count. Reactivating any attendee hides that walled interval on the next read.
The installer's wall membership grants no access. Removing a wall leaves
unassigned occupancy unchanged and returns assigned events on the next read.

Each interval contains **only `starts_at`, `ends_at`, `all_day`**. `all_day` is
true when the underlying event is all-day. This flag is the **only widening of
the times-only shape** approved in 2h: no id, title, matter, kind, attendee,
video, walled flag or other event detail is disclosed. Every interval uses the
same ordering and pagination, without merging, separate counts or wall-dependent
cache/error handling. Deletion contributes no interval. Existing window/range
and pagination limits, scope and service-only checks remain unchanged.
User/matter/kind filters are refused; a viewer cannot use occupancy.

`view=busy&user_id=…` still requires an active user and returns only start, end
and all_day across walls. It also includes walled events with no active attendee,
even when the requested user is not an attendee. Events with active attendees
retain the existing user filter. Ordinary event list/get reads retain all walls.

### Core update 2i: calendar video metadata and linked navigation

Calendar create/update/get/list (REST and MCP) accept or return optional `video`:

```json
{"requested":true,"provider":"phone-zoom","host_user_id":null,"provider_meeting_id":null}
```

`requested` is required on create when video is an object. On PATCH, omission
preserves video, null clears the whole object, and an object merges only its
supplied fields. `requested` is optional on PATCH. Null clears a field (for
`requested`, it resets the boolean to false); an object patched onto absent video
starts with false and null fields. Concurrent writers to different fields merge
against the current row under the event's transaction lock.
`provider` is null or a manifest module id matching
`^[a-z][a-z0-9-]{0,40}[a-z0-9]$` (2 to 42 ASCII characters).
`host_user_id` is null or a core user's UUID. New or changed hosts must be active;
an unchanged host may remain after deactivation so unrelated edits still work.
Malformed, missing or newly assigned deactivated hosts return `invalid_input`.
`provider_meeting_id` is an opaque string of at most 200 characters or null.
Setting it does not infer or change `requested`. **`location` holds the join
link.** Staff and approved `calendar:write` service principals use the existing
CORE-30 write/delegation rules. Video is readable wherever the event is readable;
occupancy/busy disclose none of it. Existing signed `calendar_event.created` and
`calendar_event.updated` notifications identify the record; consumers fetch
`video` through getCalendarEvent, just like the other event fields. Notifications
retain their existing envelope and wall checks.

For a staff frame to hand off a call, post:

```javascript
parent.postMessage({type: "togavsu:navigate", page: "/modules/intake/tabs/start", params: {phone: "+15555550123"}}, "*");
```

The target must be a declared staff tab (`home` or `matter`) of an enabled module,
and an admin-approved contract link must currently connect source and target,
in either direction. Both frame bridges resolve this through authenticated
`GET /modules/{source}/navigate?page=…&params=<JSON string map>`, returning
`{"url":"…"}`. Source must also be enabled and the viewer must be staff,
attorney or admin. Missing/unlinked/disabled modules, client tabs, cross-module
widgets/panels and malformed/external paths are refused. Unlinking or disabling
is effective on the next navigation. Params from both `page`'s query and `params`
are retained only if named in the **target tab's `tabs[].params`**; undeclared
params are dropped. For a `matter` placement tab, `matter_id` is also passed
through without a declaration, only after checking that the viewer may read that
matter. Missing, malformed or inaccessible matter context is refused. `params`
overrides matching query keys. Navigation grants no record access: target context-token issuance and matter walls still apply.
Same-module and core-page navigation retain their existing allowlist.

The public kit exposes `CalendarVideo`, `CalendarVideoPatch`, `CalendarOccupancyBlock` and
`CalendarBusyBlock`; `c.CalendarOccupancy(moduleID, from, to)` uses the real
service endpoint in coretest. Core migration **1700** stores video as a nullable
whole JSON object without changing old event rows.

An approved `processor.payment.v1` intent may be recorded once by its consumer,
without a viewer, through the [record continuation](../contracts/README.md#record-the-approved-receipt).
`record` takes `ProcessorPaymentRecord`, returns `Payment` (200), checks current
`contracts:call`/`payments:write`, approvals and link, binds the newly created target
and records stored money fields atomically. It never calls the processor. Replays
return the same payment; changed targets, unknown/other-module intents,
pending/declined/trust receipts and supplied money fields are refused. Normal
viewer/task processor and arbitrary-payment write checks retain their policy.

See [Core update 2g](core-update-2g.md) for the complete continuation and regression inventory.

Contact-only payment extensions returned by `include=` and published module views
use billing's current contact-party wall check, just like `GET /payments/{id}`.
A contact subsequently placed only on a matter the viewer cannot read hides its
existing payment's module rows; an allowed viewer retains access and removing the
wall restores the rows on the next read. Matter-bound payments retain their own
matter's policy.
