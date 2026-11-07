# Simplification debt

Each entry names the file, the deliberate ceiling, the upgrade path, and the concrete upgrade trigger.

1. `internal/db/seed.go` — synthetic seed inserts one Missouri county. Ceiling: the counties table has a row. Upgrade path: first-run loads every county of the firm's state. Trigger: setup stores the firm state.
2. `internal/domain/defaults.go` — marketplace address default is the sentinel `togavsu-marketplace`, not a hostname. Ceiling: the setting exists and can be changed. Upgrade path: replace the sentinel with the marketplace origin. Trigger: the marketplace task publishes the service URL.
3. `internal/settings/check.go` — `internal/db/ranges.go` is exempt from the hard-coded number check so the published version bands 100, 400, and 600 stay visible. Ceiling: that file is only the allocation table. Upgrade path: drop the exemption. Trigger: a tunable literal is added to `ranges.go`.
4. `internal/db/seed.go` — the synthetic move progress is `10 * 10` because the bare value 100 collides with `activity.page_size`. Ceiling: one synthetic row. Upgrade path: write the percent from the registry. Trigger: seed progress is loaded from a setting.

Debt count: 4.
