# Simplification debt

Each entry names the file, the deliberate ceiling, the upgrade path, and the concrete upgrade trigger.

1. `internal/db/seed.go` — synthetic seed inserts one Missouri county. Ceiling: the counties table has a row. Upgrade path: first-run loads every county of the firm's state. Trigger: setup stores the firm state.
2. `internal/domain/defaults.go` — marketplace address default is the sentinel `togavsu-marketplace`, not a hostname. Ceiling: the setting exists and can be changed. Upgrade path: replace the sentinel with the marketplace origin. Trigger: the marketplace task publishes the service URL.

Debt count: 2.
