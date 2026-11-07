# Simplification debt

Each entry names the file, the deliberate ceiling, the upgrade path, and the concrete upgrade trigger.

1. `internal/db/migrate.go` — checksum ledger is `schema_migrations` rather than goose's version table. Ceiling: one runner records owner, version, checksum, and applied time. Upgrade path: adopt goose when one table can store owner and checksum. Trigger: sqlc and pgx land and the lead asks for the goose CLI.
2. `internal/db/migrate.go` — `Apply` is exercised on SQLite in unit tests. Ceiling: portable SQL is executed, not only parsed. Upgrade path: run the same `Apply` against Postgres 17. Trigger: platform Compose publishes a Postgres service to CI.
3. `internal/db/seed.go` — synthetic seed inserts one Missouri county. Ceiling: the counties table has a row. Upgrade path: first-run loads every county of the firm's state. Trigger: setup stores the firm state.
4. `internal/domain/defaults.go` — marketplace address default is the sentinel `togavsu-marketplace`, not a hostname. Ceiling: the setting exists and can be changed. Upgrade path: replace the sentinel with the marketplace origin. Trigger: the marketplace task publishes the service URL.

Debt count: 4.
