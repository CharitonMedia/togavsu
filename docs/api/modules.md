# Module API

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

Module secrets are `PUT`/`GET`/`DELETE /api/v1/modules/{id}/secrets/{name}` (`module_secrets:write`), sealed with the core key. Public inbound webhooks are `POST /hooks/{module_id}/{hook_id}` only: the core strips inbound credentials, strips `Set-Cookie` on the response, and adds the same CSP sandbox as `/m/` so module HTML never runs in the core origin. Sidebar entries gain section, subgroup, icon, order and badge fields; `GET /api/v1/nav/context` fills badges as the viewer.

## Frame bridge (CORE-09)

Staff and client frames listen for `togavsu:editor.open`, `togavsu:navigate` and `togavsu:context.refresh`. Navigate uses the widget-target allowlist (a tab/widget/matter-item of the same module, or a core page). Context refresh uses `…/context-token` on the frame path and keeps the query string (including `matter_id`). Staff routes: `GET /modules/{module_id}/tabs|widgets|matter-items/…/context-token`. Client: `GET /portal/{module_id}/{tab_id}/context-token`. Neither frame kind gets `allow-same-origin` on the module iframe.

## Client portal (CORE-10–CORE-15)

Client tabs use `placement: client`, `/portal` and `/portal/{module_id}/{tab_id}`. Client matter reads, trust reads (`/api/v1/me/trust`, `/me/trust-ledger`), contact self-edit (`/api/v1/me/contact`), client access management (`client_access:*`), and appointments (`/api/v1/me/appointments`) apply the same walls and the `client_access.days_after_close` window.

## Files, firm profile, directory, settings, archives (CORE-16–CORE-21)

Module publication may set file `origin`. Library files have no matter or contact. `firm:read` and `users:directory` expose firm profile and the user directory. County, practice area, party role and custom field definition reads accept a module viewer call. Archive `entries` are checked as the viewer before the first byte.

## Linkable records, processors, refunds, signatures (CORE-22–CORE-25)

Modules may link to `fee_agreement`, `payment_refund` and `trust_account`. Payment processors register via `payment_processor` and use delivered `processor_authorization` grants on `createPayment` / `updatePaymentStatus`. Refunds write `payment_refunds` (and payment refund columns), support pending `refund_authorization` grants, and post trust refunds under the below-zero rule. The signature module recorded on a fee agreement may move sent→signed; a service call on a walled matter is `not_found`.

## Mail match, filing grants, devices, harness (CORE-26–CORE-29)

`POST /api/v1/mail/matches` and `POST /api/v1/mail/filing-grants` omit walled matters for service calls. Device grants exchange for short-lived context tokens. `pkg/modulekit/coretest` is the composed test core for module repositories.

## Contributor gap fixes and wider service calls (CORE-36, CORE-30)

CORE-36 closes the public-kit gaps this batch hits: `/m/` forwards `X-Togavsu-Context-Token`; `GET /api/v1/me` accepts a context token as the viewer; a missing scope answers `scope_denied` naming the scope; webhook signatures are `sha256=` HMAC of the raw body; notes land in `core.notes`; invoices, payments and expenses dual-write into `core.invoices` / `core.payments` / `core.expenses`; conflict decisions emit `conflict_check.decided`; `getConflictCheck` accepts a service principal; `Accepts().core_items` lists built CORE items; coretest seeds markdown renditions without converters.

CORE-30 marks the wider service-call set `delegated_viewer_or_service_principal` (contacts, matters, conflicts, calendar, deadlines, notes, timeline, markdown renditions). Each has `modules.service_call_limit.<operationId>` (default 120/minute). `listContact` supports exact `email` and `phone` filters. Service-principal writes record the module as author with audit source `service`. Walls still omit walled matters from lists and answer `not_found` on gets.

## Module service contracts (CORE-31)

Manifest `provides` and `consumes` name versioned contracts such as `phone.provider.v1`. The admin approves each consumer↔provider link (`POST /api/v1/modules/contract-links`, `DELETE …/contract-links/{link_id}`, `GET …/contract-links`) after both sides have approved `contracts:call` / `contracts:provide`. The consumer calls `POST /api/v1/modules/contracts/{contract}/{operation}` with scope `contracts:call`; the core proxies to the linked provider's declared path, HMAC-signed like events (`X-Togavsu-Signature`), carrying `X-Togavsu-Context-Token` when present. Providers push events with `POST /api/v1/modules/contracts/{contract}/events` (`contracts:provide`); event actions are always scoped under the contract name (bare names are prefixed; names with a different namespace are refused). One provider per contract per firm unless **every** linked provider declares `many: true` (a `many: false` provider already linked blocks a second provider even if the new one sets `many: true`; when several `many` providers are linked, the earliest link by `created_at`/`id` is chosen for calls). Modules never learn each other's address. Unlinked calls answer `contract_unlinked`. Every proxied call and pushed event writes an audit record (consumer, provider, contract, operation/action, result) without any address. Body size is limited by `modules.contract_call_max_bytes` (25 MiB). Disabling a module keeps its approved links; removing it deletes them.

## Public module pages (CORE-32)

Manifest `public_pages` are served at `/p/{id}/…` with no sign-in under `modules.public_page_rate_per_minute` (60) and `modules.public_page_max_bytes` (10 MiB). The core sets an HttpOnly CSRF cookie and passes a matching HMAC-signed token to the module as `X-Togavsu-Public-CSRF` on every proxied request so the module can embed it in forms, HTMX (`X-CSRF-Token` / `csrf`), and offline autosave replays. HTML responses use a CSP sandbox **without** `allow-same-origin` (opaque origin, same as staff frames). POSTs are checked by verifying the signed token; the cookie is optional defense-in-depth when the browser sends it. HMAC step tokens (`X-Togavsu-Public-Step`) are issued on each public-page request. Scope `public_pages:serve` is required at install; the admin Modules tab shows declared public pages and labels `public_operations` as step-header-enforced / bearer-advisory. Server calls that carry a valid step token run as the module service principal and only on `public_operations`. Helpers: `modules.IssueStepToken` / `VerifyStepToken` / `IssueSignedPublicCSRF` / `CheckSignedPublicCSRF` (also via `pkg/modulekit`).

**Step-token / `public_operations` (advisory for bare bearer):** step tokens are HMAC-signed with the module's webhook secret (so the module can verify them), and `public_operations` is enforced only when the caller sends `X-Togavsu-Public-Step`. A module bearer token used without that header is gated by its approved scopes alone. The install view states this limit. See PONYTAIL-DEBT.md.

## Forms kit (CORE-33)

`pkg/modulekit/forms` is the public form definition, validation, HTMX renderer, autosave/offline queue, "I don't know" answers, and masked-field encryption through the module secret store. Answers stay in the calling module's schema. Autosave accepts JSON or the `application/x-www-form-urlencoded` bodies the HTMX renderer emits; HTMX autosave includes the form `csrf` field and `X-CSRF-Token`, and `OfflineQueueJS` replays with the same header. Repeating-row indices above `MaxRepeatingIndex` (`domain.FormsMaxRepeatingIndex`) are ignored. `EnsureKey` creates a key only on `ErrSecretNotFound`.

## Staff banner slot (CORE-34)

Manifest `staff_banner` reserves a fixed-height sandboxed frame above every staff page. Scope `banner:show`. Admin order is `modules.staff_banner_order` (`PUT /api/v1/modules/banner-order`, `GET /api/v1/modules/banners`, and the Modules tab form). The frame bridge accepts `togavsu:banner.show` / `togavsu:banner.hide` only from that module's banner iframe on the same origin.
