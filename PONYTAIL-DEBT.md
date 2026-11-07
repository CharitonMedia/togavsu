# Simplification debt

Each entry names the file, the deliberate ceiling, the upgrade path, and the concrete upgrade trigger.

1. `internal/db/seed.go` — synthetic seed inserts one Missouri county. Ceiling: the counties table has a row. Upgrade path: first-run loads every county of the firm's state. Trigger: setup stores the firm state.
2. `internal/domain/defaults.go` — marketplace address default is the sentinel `togavsu-marketplace`, not a hostname. Ceiling: the setting exists and can be changed. Upgrade path: replace the sentinel with the marketplace origin. Trigger: the marketplace task publishes the service URL.
3. `internal/settings/check.go` — some integers that equal a registry default are fixed rules, listed in `excludedNumber`: version bands, the cents fraction and sign, the whole UUID layout in `id.go`, synthetic seed rows, the ledger baseline, migration index 0 and version 1, confidence's upper bound of 1, process status 2, strconv bases, and this scanner file itself. Ceiling: those files and values only. A sibling path is still checked. Upgrade path: delete the matching entry. Trigger: a tunable literal is added in one of those files.

Debt count: 3.
