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

The factor-reset model reads verifier outcomes from trusted context only. It does not read a clock and does not consume a proof. Go identity must check owner, action, freshness, and that the proof is unused, then pass an explicit `valid` or match flag. A missing flag is not success. An existing record whose trusted matter id was omitted is not an unassigned record. A requested matter is denied before a replaced domain can route the call. A batch wall value counts only when it is a boolean. A known source uses that same boolean rule when the wall map is absent. A caller `task_authorization` object is not a grant. Cookie `tools/call` writes require a trusted CSRF result. The model does not check a clock or consume either credential.

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
