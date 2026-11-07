# Architecture contracts

This package layout is the shared Go contract for later waves. It is not the finished product.

## Packages

| Path | Responsibility |
| --- | --- |
| `cmd/core` | Process entry. Loads bootstrap config and exits. It does not listen for HTTP. |
| `internal/domain` | IDs, money, time, section 5 records, principal, access policy, API errors. No SQL. |
| `internal/repository` | Persistence interfaces. Append-only ledgers have no update or delete method. |
| `internal/settings` | Settings registry, validation, audit, secret sealing, and the AC-030 source check. |
| `internal/bootstrap` | The only package that reads the process environment. |
| `internal/db` | Numbered SQL migrations, the section 5 schema, reference data, and synthetic seed. |
| `docs/architecture` | These notes, including migration number ranges. |

`go.mod` pins pgx and goose. Postgres connections are opened with `db.OpenPostgres`. sqlc generates typed queries when repository implementations land; this branch has no query files yet.

## What this branch tests

- UUIDv7, integer cents, UTC time, and E.164 phones, including rejected values.
- Role and matter-wall decisions, including accountant reads, staff trust deposits, and walled matters.
- Settings changes apply on the next read, out-of-range values are rejected, reset restores the default, and secret values are redacted in the audit record and encrypted at rest.
- The source check rejects environment reads outside `internal/bootstrap` and named tunables outside the registry. `go test ./internal/settings/` passes on this tree.
- Migration order, checksum refusal, rollback of a failed migration, and a second owner at the same version, executed on Postgres 17.
- A migration that sets `search_path` does not leave that path on other pooled connections.
- After the synthetic seed, every section 5 table has a row. Reference data alone leaves first-run setup incomplete.

The SQL stays free of Postgres-only features so a later SQLite dialect can reuse it. These tests do not claim that dialect exists.

## Pending

- Compose, Caddy, the supervisor, and product CI calling `go test ./...` (platform).
- HTTP, OpenAPI, and MCP (api).
- Argon2id, sessions, TOTP, and the wall reader behind `domain.Policy` (identity).
- Practice, billing, trust, calendar, files, editor, and Doc Tender behavior beyond the records and interfaces.
- Admin screens that edit the registry (ui).
- A real marketplace hostname. The setting default is the sentinel `togavsu-marketplace`.
- Loading every county for the firm's state at first run. Synthetic seed inserts one county.
- Product acceptance of AC-001, AC-029, AC-030, and AC-035. Those IDs are covered here only as far as the contracts and unit tests go.
