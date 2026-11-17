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
