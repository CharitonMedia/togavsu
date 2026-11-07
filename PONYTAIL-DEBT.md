# Simplification debt

Each entry names the file, the deliberate ceiling, the upgrade path, and the concrete upgrade trigger.

1. `internal/db/seed.go` — synthetic seed inserts one Missouri county. Ceiling: the counties table has a row. Upgrade path: first-run loads every county of the firm's state. Trigger: setup stores the firm state.
2. `internal/domain/defaults.go` — marketplace address default is the sentinel `togavsu-marketplace`, not a hostname. Ceiling: the setting exists and can be changed. Upgrade path: replace the sentinel with the marketplace origin. Trigger: the marketplace task publishes the service URL.
3. `internal/settings/check.go` — `fixedSite` skips integer literals inside enumerated functions (cents, UUID layout, synthetic seed builders, version bands, migration indexes, the ledger baseline, bootstrap key length and process status, sealing key width, and setting upsert). Ceiling: a new configurable literal inside one of those functions is not reported. A different function in the same file is reported. The check is not AC-030 acceptance. Upgrade path: delete that function from `fixedSite`. Trigger: a configurable literal is added inside a listed function.
4. `internal/db/migrations/0001_core_schema.sql` — append-only tables are not immutable at the database. Interfaces omit update and delete, and there is no trigger because PRD section 6 forbids business-logic triggers. Ceiling: `TestAppendOnlyNotEnforcedAtDatabase` shows the application role can UPDATE and DELETE those rows. Upgrade path: the owning task rejects hostile UPDATE and DELETE, including raw SQL from a non-owner role, and replaces that gap test. Owners: identity `audit_log` (AC-024), practice `events` (AC-024), trust `trust_ledger` (AC-006), files `file_versions` and `file_renditions` (AC-039), modules `ai_call_log` (AC-031). Trigger: that task's acceptance claims the ledger cannot be edited or deleted.

Debt count: 4.
