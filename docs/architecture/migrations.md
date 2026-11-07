# Migration numbers

Core migrations live in `internal/db/migrations` and are embedded in the binary. Each file is `NNNN_name.sql`. The runner applies them in version order, one transaction each, and records `owner`, `version`, `checksum`, and `applied_at` in `schema_migrations`.

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

Postgres runs with `search_path` set to `core`. SQLite uses the same unqualified names. `db.Apply` rejects a schema name that is not a single identifier.

Tables that are not in section 5 stay in later ranges. That includes `matter_markers`, custom fields, home layouts, notifications, help sections, and fee items.
