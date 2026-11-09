# API contract proposal

Task `api`. Owned paths: `api/`, `docs/api/`, this file. No Go packages, no HTTP handlers, no change to the product scope or the review gate.

## What is published

- `api/openapi/v1/openapi.json` is OpenAPI 3.1.0 for every core feature in sections 6, 8, and 9, plus the marketplace client the core exposes and the backup, restore, and update operations from section 6.
- `api/openapi/marketplace/openapi.json` is the separate marketplace service: browse, signing keys, alert poll, operator flag, operator pull. The server URL is `https://marketplace.example.invalid` so this proposal does not name a live host.
- `api/openapi/v1/mcp-tools.json` and `docs/api/coverage.md` are generated from the core document.
- Conventions are in `docs/api/conventions.md`. The MCP argument shape is in `docs/api/mcp.md`.

The builder is `api/contract/`. Regenerate with `PYTHONPATH=api python3 -m contract.build`. The check is `PYTHONPATH=api python3 -m unittest api.tests.test_contract` after `pip install -r api/requirements-dev.txt`.

## Shared types for the contracts agent

These stay proposals until that agent adopts them. This branch does not edit `internal/` or `go.mod`.

- `Problem`: the problem+json object in schema `Problem`, including `code` and `errors[]`.
- `Page`: `limit` and `next_cursor`.
- `Principal`: user id, roles, actor kind (`staff`, `client`, `agent`, `module`, `system`), token scopes, module id when the caller is a module.
- Access decision: allow, `not_found` when a wall hides the row, redact attorney-only fields. The same decision for HTTP, MCP, search, reports, files, and widgets.
- Module calls: interactive work uses `X-Togavsu-Context-Token`. The decision intersects approved scopes with the viewer's role, walls, ownership, sharing, and field restrictions. Noninteractive work needs a core-issued grant delivered on `moduleEvent`. `file.version_created` carries `task_authorization` for the subscribed scanner. The scanner reads `FileGrantView` or `FileVersionGrantView`, downloads that version, and may quarantine it. `payment.processor_authorized` carries `processor_authorization` for one registered processor. The core reserves the payment id on that grant. `createPayment` binds it once. A repeat create replays the original payment or returns conflict and does not insert another payment or trust row, with or without an idempotency key. The callback may also `updatePaymentStatus` for that payment, with no viewer session. A client context token keeps that client's upload sequence, including `getFileUpload`, and every published client read of shared files and own invoices. Core file-byte responses, including the invoice PDF, write `file.downloaded`, highlight a client download, and expose a confirmation input before a quarantined file is returned. Each published version emits `file.version_created` after commit. Invoice, payment, and billing-report operations are the `time` replacement domain. A trust-linked payment reversal needs `trust:withdraw` before any mutation and then posts exactly one append-only trust row through the trust handler, on the payment's trust account. A completed trust-destination payment needs `trust:write` and an active authorized `trust_account_id`.
- Processor payment storage, for the contracts agent: one initialization reserves one payment id. The table needs a unique `payments.initialization_id`, `payments.id` equal to that reserved id, and the payment insert plus any trust row in one transaction. A concurrent create loses the unique insert and returns the original row or conflict. This proposal does not edit `internal/db`.
- Custom fields on contact, matter, time entry, and expense creates, including bulk creates, travel in `custom_fields` on the write body. The record and its values commit together. A failed item writes neither.
- Webhook delivery checks installation, subscription, read scope, matter wall, and attorney-only records before the envelope is serialized. A failed check sends no body.
- Background file publication for email, Zoom, drop folder, and importers is a core-issued `files:write` grant for one matter. It expires and does not grant admin.
- Settings registry keys and starting values: `x-togavsu-settings` on the core document. 133 keys. Locked key: `security.totp_required.admin`.

Seeds the data layer should load are `x-togavsu-seeds`: 17 Doc Tender categories, the party-role keys, the naming tokens, the default folder names, and LawCraft commit `770a8fab12392485c0c2789fb254da7d9296df4d`.

## Supervisor calls

Platform owns the supervisor API. The core will need authenticated local calls for backup create and status, restore start and status, update start, and module container start and stop. This proposal does not define that API.

## Acceptance

AC-002 is not met as product behavior. The OpenAPI documents validate, and every required feature has an HTTP operation and an MCP tool (the MCP transport itself is `POST /mcp`). Handlers, the MCP server, and `/llms.txt` rendering are pending. A later implementation should generate the served document from the router and diff it against `api/openapi/v1/openapi.json`.

## Simplification

One builder emits the document, the tool list, and the coverage matrix, so the three cannot drift. CRUD, cursor pages, problem responses, idempotency, and the MCP extension go through `Contract.resource` and `Contract.add`.

Net lines are in the PR. Debt count for this task: 1. The MCP adapter correction does not add a debt entry. `PONYTAIL-DEBT.md` still lists 6.

## Stage 1 split

Baseline `b4350f8af3803b8dc8439b2b2e7a431f198583d9`. Builder metadata is `api/contract/metadata.py`. The call simulator remains `api/contract/policy.py` and its tests still run. `docs/api/scenario-manifest.json` is a source index. It counts methods, assertion-call sites, and literal rows. It does not mark any method converted, and every behavioral method stays pending. `docs/api/method-acceptance.json` is the reviewed method map. `docs/api/runtime-fixtures.json` stays `runtime_pending`. The fourteen grant excerpts are incomplete and are not replayable. Deletion stays blocked.

A presented bearer must hold a published view scope on the token, not only in the approval list. `createApiToken` requires `tokens:write` in addition to the child-scope ceiling. `initiateProcessorPayment` applies the declared processor policy, so `payments:write` alone cannot initialize a trust payment. Custom acceptance does not skip an operation's own scopes. Entry-type and per-item scope lists stay alternatives.

The simulator and its tests are retained. They are not byte-identical to baseline `b4350f8a` after the later authorization corrections. At `8370ed4`, only the core OpenAPI document changed, to `c8938e788bd4a1ef60a58c3016313c40ac02236a83de01a011e2e6bd3c34a452`. The marketplace document, MCP catalog, and coverage file stayed at the baseline hashes. `docs/api/stage1-inventory.json` keeps those baseline hashes separately from the current hashes. Later named corrections may change the current documents again; they do not rewrite the baseline hashes.

The factor-reset model reads verifier outcomes from trusted context only. It does not read a clock and does not consume a proof. Go identity must check owner, action, freshness, and that the proof is unused, then pass an explicit `valid` or match flag. A missing flag is not success. An existing record whose trusted matter id was omitted is not an unassigned record. A requested matter is denied before a replaced domain can route the call. A batch wall value counts only when it is a boolean. A known source uses that same boolean rule when the wall map is absent. A caller `task_authorization` object is not a grant. Cookie `tools/call` writes require the captured HTTP `X-CSRF-Token` and a trusted CSRF result. A tool argument is not that header. A matter publication grant needs the matter in trusted `principal_matter_ids`, including when the matter is unwalled. An explicit null `matter_walls` is unresolved and does not fall through to a scalar. A concrete matter id, including a read, needs that same resolved boolean. `createFileLink` requires both trusted files. `createFileArchive` authorizes every selected file or folder member. An existing record with a parent matter needs that matter from trusted state, including when the response is a oneOf, a wrapper, or a history page. Explicit null is unassigned only when that record schema allows it. A client file on an assigned matter must be that client's matter. A matching contact applies only when the file is unassigned. Every selected file needs a boolean `quarantined` value. The model does not check a clock or consume either credential.

Trusted resource facts for the current parent, file, and invoice boundary are in the matrix below. The reference simulator reads those facts. It does not treat a path parameter, a response wrapper, or an omitted caller flag as a grant.

## Trusted resource matrix

This is the reference-model boundary for the families named in the current findings. It is not a proof of every matter-wall route, and it is not AC-002. Handlers are still pending. Expectations in `api/tests/test_contract.py` are written from these route and schema rules. They are not computed by `_parent_matter_mode`.

| Family | Operations | Kind | Trusted parent | Client rule |
| --- | --- | --- | --- | --- |
| File | `getFile`, `updateFileMetadata`, `historyFile`, `listFileVersions`, `downloadFileVersion`, `downloadFileRendition` | existing read, existing write, history, child list, download | One trusted row whose id is the path file. Its `matter_id` (null allowed), `matter_walled`, and `attorney_only` are the parent. A missing, extra, malformed, or different caller `matter_id` is `not_found`. The row's wall is authoritative: a trusted `matter_walls` map that is malformed, or that gives the row's matter a different or non-boolean wall, is `not_found` before any effect. A map that omits the row's matter does not replace the row. | Shared file: assigned matter must be in `client_matter_ids`. Unassigned file matches `client_contact_id`. Staff and client are `not_found` when the row is attorney-only. Attorney and admin may read it. |
| File selection | `createFileArchive`, `createFileLink` | create over existing files | Every selected or linked row uses that same parent. A caller `matter_id` that disagrees with the source file denies the link. | Same file relationship. One hidden row denies the call. |
| Invoice | `listInvoice`, `getInvoice`, `historyInvoice`, `getInvoicePdf` | list, existing read, history, download | Invoice `matter_id` is required on an existing invoice. | Client needs that matter in trusted `client_matter_ids`. Missing, null, foreign, duplicate, or non-string membership is `not_found`. `owner` and `shared` on the call are not evidence. Staff, attorney, and accountant do not use that list. No separate invoice share flag is granted. |
| Doc Tender job and exception | `getDocTenderJob`, `retryDocTenderJob`, `getDocTenderException`, `resolveDocTenderException` | existing read, action | The trusted stored job or exception row must name a `file_id`, and that file's one trusted row is the parent, by the same rule as the File row above: its `matter_id` (null allowed), `matter_walled`, and `attorney_only` decide. A missing record, a missing or null `file_id`, a missing, extra, or different file row, an unresolved wall, a disagreeing `matter_walls` map, or a different caller `matter_id` is `not_found` before any effect or event. Caller `attorney_only` and `matter_walled` flags are ignored. | Clients are not in the roles. |
| Trust flag | `getTrustFlag`, `resolveTrustFlag` | existing read, action | `TrustFlag.scope` chooses the flag parent. `matter` requires `matter_id`. `client` requires `client_id` and no matter. `account` requires `trust_account_id` and no matter. Every scope also requires trusted `source.trust_entry_id` equal to the flag and a visible `source.matter_id`. A body source does not count. An `account` flag also needs exactly one trusted `trust_accounts` row for its `trust_account_id` that is active, authorized, and in the caller's `firm_id`. An inactive, unauthorized, other-firm, missing, or duplicate account, or a missing firm on either side, is `not_found` for the read and the resolve, the same rule the list applies. | Clients are not in the roles. |
| Trust flag list | `listTrustFlag` | list | The list is not itself matter-required. Each row uses the same source-matter rule and the same account rule as a direct read. A hidden source or a non-current account is omitted. | Clients are not in the roles. |
| Module context | `createModuleContextToken` | create | Parent is `Module`, which has no matter. Body `matter_id` is optional scope. `{}` and null do not look up a stored matter. | Staff follow the matter wall when a matter is requested. A client requested matter must be in trusted `client_matter_ids` and `client_shared_matter_ids`. Missing, foreign, unshared, or malformed evidence is `not_found`. Caller `owner` and `shared` are not evidence. |
| Extension targets | `CORE_EXTENSION_TARGETS` in `api/contract/metadata.py` | list and get reads named there (39 records) | Every section 5 record with a published read or list, because section 5 is the base 9.9 extends: contacts and their addresses, emails, phones and tags; matters and parties; practice areas, counties, referral sources; users; notes; fee agreements; time entries and timers; expenses; invoices; payments; calendar events; deadlines; files, versions, renditions and links; court profiles; Doc Tender categories, jobs, results and exceptions; conflict records, conflict checks and conflict reviews (each `ConflictHit`); trust accounts, ledger entries, flags, replenishment requests and reconciliations. Folders (PRD 8.19) and matter markers (PRD 8.12) are also targets: they are matter records, and 9.9 says module tables point to "contacts, matters, users, invoices, time entries, files and the rest". `CORE_EXTENSION_EXCLUSIONS` names every other record read, and each reason quotes the PRD text it rests on: credentials (PRD 5 "sessions, password credentials (Argon2id), TOTP secrets, recovery codes, API tokens"), settings-table values (PRD 5 "Settings live in a settings table, one row per setting"; activity and expense types; party roles, a fixed list on matter_parties; custom field definitions, which 9.9 names as the other extension point), storage infrastructure (PRD 5 storage_backends and storage_moves, and uploads), append-only streams (PRD 5 "audit_log: append-only" and "events: the append-only stream", the AI call log, PRD 7 webhook deliveries), `matter_walls` (PRD 5 "matter and the users allowed in", published as the matter's member list with no row id), computed trust balances (PRD 5 "computed from the ledger and cached for display"), computed widget and command lists (PRD 8.6, 8.20), per-user and catalog records (PRD 8.5 iCal feeds, 8.6 layouts, 8.9 notifications, 8.11 help, 8.12 marker definitions, 8.15 fee items), and system records (PRD 6 backups and updates, 8.7 modules, keys and problem reports, 10.2 listings). `edit_locks` and `doc_tender_units` have no read. A contract test fails when a GET returns a record with an id that is in neither map, and another checks every quoted phrase. | `include` on every catalog read, through the same dispatch as the read, so the record's scope, roles, matter wall, client sharing, attorney-only files, and the trust-flag source and account rules all apply first. A published view needs `views:{module}.{view}:read` approved and on the token. The client invoice allowlist and file grant views do not carry `included`. |
| Preserved matter records | `getInvoice`, `getTrustEntry`, `stopTimer`, `historyTimeEntry`, `getTimeEntry` | existing read, action, history | Stored matter is required. Omitted, null, or unresolved state is `not_found` before a replacement route. | Unchanged. |
| Preserved nullable and free routes | `getCalendarEvent`, `getFile` with an unassigned file, `listInvoice` for staff | existing read or list | Null matter is allowed only where that record schema allows it. A collection list has no stored parent. | Unchanged for staff. |

Coverage stops at this table. Other matter-wall routes keep the previous stored-matter rule. A count of required, nullable, and absent routes is not evidence that those routes were judged. The source index now has 63 behavioral methods, including `test_trusted_resource_facts_match_the_route` and `test_canonical_file_flag_source_and_context_matter`. Every one stays pending. `converted_for_retirement` stays 0. The fourteen grant excerpts stay incomplete and were not executed.

The generated change in this round is `api/openapi/v1/openapi.json`, now `c6f7d7a5800fff61f2280ad761bc59d31c953c57bb77d7422b4b5fd3e1a74fff`. It adds the 22 new records (20 from section 5, plus folders and matter markers) to the extension and view enums, optional `included` on their read schemas, `include` on their list and get reads, and a required `id` on `DocTenderResult`. Marketplace, the MCP catalog, and coverage keep their bytes from the previous revision. The MCP catalog describes query as one object, and coverage does not list parameters.

Denials from a missing file row, a missing invoice relationship, an unknown flag variant, an unknown requested matter, or a missing stored matter return before effects, events, and domain routing. Quarantine still needs the confirmation input. Confirmation does not bypass the file row.

Phase 1 still means the OpenAPI documents validate. Run `python3 -m pip install -r api/requirements-dev.txt` and then `sh api/check.sh`. That command does not execute runtime fixtures and does not mean product CI already calls it. REST and MCP behavior stays with the Go work. The platform owner wires the command.

```
file: api/contract/model.py
marker: ponytail
ceiling: the OpenAPI file is the proposal, not the document the running core generates
upgrade path: generate OpenAPI from the Go router and fail CI when it differs from api/openapi/v1/openapi.json
trigger: the core serves GET /api/v1/openapi.json
```

`PONYTAIL-DEBT.md` is outside this task's paths and was not edited. The accepted main ledger has 6 entries. This task proposes 1 additional entry for the integration lead to reconcile into that ledger.

## Pending dependencies

- Contracts agent: Go `Problem`, `Principal`, repository IDs, and the settings registry keys above.
- Platform: supervisor local API, health check wiring, backup and update execution.
- Identity, practice, billing, trust, calendar, files, Doc Tender, modules: handlers that implement these paths and the role, wall, and scope checks.
- Marketplace service: `api/openapi/marketplace/openapi.json`.
- UI: calls these operations. Widget click-through targets are on each widget payload.
- AC-002 runtime, including a live `/mcp` server generated from this document.
