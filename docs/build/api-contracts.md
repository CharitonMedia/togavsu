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
- Settings registry keys and starting values: `x-togavsu-settings` on the core document. 133 keys. Locked key: `security.totp_required.admin`.

Seeds the data layer should load are `x-togavsu-seeds`: 17 Doc Tender categories, the party-role keys, the naming tokens, the default folder names, and LawCraft commit `770a8fab12392485c0c2789fb254da7d9296df4d`.

## Supervisor calls

Platform owns the supervisor API. The core will need authenticated local calls for backup create and status, restore start and status, update start, and module container start and stop. This proposal does not define that API.

## Acceptance

AC-002 is not met as product behavior. The OpenAPI documents validate, and every required feature has an HTTP operation and an MCP tool (the MCP transport itself is `POST /mcp`). Handlers, the MCP server, and `/llms.txt` rendering are pending. A later implementation should generate the served document from the router and diff it against `api/openapi/v1/openapi.json`.

## Simplification

One builder emits the document, the tool list, and the coverage matrix, so the three cannot drift. CRUD, cursor pages, problem responses, idempotency, and the MCP extension go through `Contract.resource` and `Contract.add`.

Net lines are in the PR. Debt count for this task: 1.

```
file: api/contract/model.py
marker: ponytail
ceiling: the OpenAPI file is the proposal, not the document the running core generates
upgrade path: generate OpenAPI from the Go router and fail CI when it differs from api/openapi/v1/openapi.json
trigger: the core serves GET /api/v1/openapi.json
```

`PONYTAIL-DEBT.md` is outside this task's paths. The lead can append that entry. The ledger in the repo still says no implementation debt.

## Pending dependencies

- Contracts agent: Go `Problem`, `Principal`, repository IDs, and the settings registry keys above.
- Platform: supervisor local API, health check wiring, backup and update execution.
- Identity, practice, billing, trust, calendar, files, Doc Tender, modules: handlers that implement these paths and the role, wall, and scope checks.
- Marketplace service: `api/openapi/marketplace/openapi.json`.
- UI: calls these operations. Widget click-through targets are on each widget payload.
- AC-002 runtime, including a live `/mcp` server generated from this document.
