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
- Settings changes apply on the next read, out-of-range values are rejected, reset restores the default, and secret values are redacted in the audit record and encrypted at rest. `Set` and `Reset` require the admin role. A denied caller leaves the stored value and the audit log unchanged.
- The source check rejects environment reads outside `internal/bootstrap`, including aliased `os` and `time` imports. It rejects every registered integer default, including values below 25, in declarations, assignments, composite fields, returns, calls, and either side of a comparison. Arithmetic, shifts, and a leading minus stay legal. Fixed rules that happen to use the same integers are listed in `excludedNumber`: cents, UUID layout, version bands, synthetic seed, and a few single-value sites. A sibling file is still checked.
- Database tests open PostgreSQL 17 with pgx. They are not a SQLite suite. The command log for a revision belongs in that revision's pull request, not in this file.
- Goose applies each migration on one checked-out connection, inside a transaction. `Apply` rejects a goose `NO TRANSACTION` directive. `search_path` is restored before that connection returns to the pool, including when preparation fails.
- After the synthetic seed, every section 5 table has a row. Reference data alone leaves first-run setup incomplete.

The SQL stays free of Postgres-only features so a later SQLite dialect can reuse it. These tests do not claim that dialect exists. Product acceptance remains pending.

## Pending

- Compose, Caddy, the supervisor, and product CI calling `go test ./...` (platform).
- HTTP, OpenAPI, and MCP (api).
- Argon2id, sessions, TOTP, and the wall reader behind `domain.Policy` (identity).
- Practice, billing, trust, calendar, files, editor, and Doc Tender behavior beyond the records and interfaces.
- Admin screens that edit the registry (ui).
- A real marketplace hostname. The setting default is the sentinel `togavsu-marketplace`.
- Loading every county for the firm's state at first run. Synthetic seed inserts one county.
- Product acceptance of AC-001, AC-029, AC-030, and AC-035. Those IDs are covered here only as far as the contracts and unit tests go.
