# Migration numbers

Core migrations live in `internal/db/migrations` and are embedded in the binary. Each file is `NNNN_name.sql` with goose Up and Down sections. Goose applies them in version order, one transaction each, through a custom store that records `owner`, `version`, `checksum`, and `applied_at` in `schema_migrations`. Goose's own version table cannot store the owner or the checksum, so the store is the PRD ledger and goose still parses and runs the SQL. `db.Apply` rejects a `-- +goose NO TRANSACTION` directive, in any letter case, before running the file. A migration cannot commit a successful statement and then fail a later one.

Core rows use owner `core`. A module uses its own owner. The same version number may exist for two owners. A changed checksum on a version that already ran stops the update.

Do not edit `0001_core_schema.sql` after it has been applied in a shared database. Add a new file in the task's range. Schema changes to this package go through the integration lead.

| Versions | Task |
| --- | --- |
| 1–99 | contracts |
| 100–199 | identity |
| 200–299 | practice |
| 300–399 | billing |
| 400–499 | trust |
| 500–599 | calendar |
| 600–699 | files |
| 700–799 | editor |
| 800–899 | doc-tender |
| 900–999 | ui |
| 1000–1099 | modules |
| 1100–1199 | marketplace |
| 1200–1299 | release |

Version 1 creates the section 5 tables in portable SQL: `TEXT` identifiers, `BIGINT` cents and unix microseconds, `INTEGER` flags, and whole JSON documents in `TEXT`. There are no triggers. Append-only behavior is the repository interface.

`db.Apply` checks out one pool connection, sets `search_path` to `core` for that connection, and runs `RESET search_path` before the connection returns to the pool. Other connections keep the default search path. Open the application pool with pgx and set `search_path` on each new connection when the core schema should be the default. `db.Apply` rejects a schema name that is not a single identifier.

Tables that are not in section 5 stay in later ranges. That includes `matter_markers`, custom fields, home layouts, notifications, help sections, and fee items.
