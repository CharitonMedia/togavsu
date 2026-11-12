# Simplification audit before the release tag

PRD 11 asks for a whole-repository audit before the phase 7 tag: apply only cuts that keep every requirement, rerun all tests, and finalize the debt ledger. Claude Code's Ponytail hooks and slash commands were not used; this is the explicit review that stands in for them.

## What was reviewed

- The whole repository, mechanically:
  - `python3 scripts/release/check_debt.py`: every `ponytail:` marker in tracked files names an upgrade trigger, and the ledger's count matches its entries.
  - The AC-030 source check (`TestModuleClean`): no environment read outside internal/bootstrap and no tunable literal outside the settings registry.
  - `gofmt` and `go vet ./...`.
- By hand: the release branch's own diff, and the three branches merged into it (sealed ledgers and the AI call log, backups with file bytes, the updater load test). Those three branches each built part of the same thing (`core migrate`, the image health check, the backup passphrase file), so that is where duplication came from.

## Cuts applied

| Cut | Why it keeps the requirement |
| --- | --- |
| Removed `logMailer` (internal/core/app.go). Password-reset tokens go by system mail (`resetMailer` over the SMTP mailer). | PRD 8.8 lists password resets as system mail. The ledger entry's trigger ("the core mounts the password-reset routes") had fired, so the entry was retired. |
| Removed the updater branch's second backup-passphrase writer (internal/core/passphrase.go and its test, plus the setup hooks). | The backup branch's `syncPassphrase` already writes the file when setup completes and again before each backup or restore. The Compose backup run and the update load test both need it. |
| One `core migrate` and one `core healthcheck` in cmd/core, one `HEALTHCHECK` in deploy/core.Dockerfile. | Kept the sealed migrate (it seals the append-only tables) and the updater's tested health check. An unknown subcommand now exits 2. |
| `MigrateSealed` counts applied migrations through `MigrateCount`, not with its own before-and-after `schema_migrations` queries. | Same `migrated=true/false` answer, from one place. |
| Practice bulk endpoints are one generic handler that replays each item through its record's own create handler inside one unit of work, not ten bulk implementations. | Each item keeps its record's access, wall, validation, audit and event rules; the batch is one transaction (PRD 7). |

## Fixed during the final acceptance run

- The supervisor's operation gate (`internal/supervisor/operations.go`) stays one mutex-like gate, not a queue. Scheduled work marks its context with `Scheduled`. An admin request waits at most 30 s for a scheduled holder and is refused at once while another admin operation runs. That cleared the backup e2e restore's 409 race with the 30-second tick (AC-018, AC-040, AC-050) without adding a dependency, a job queue or a ponytail ceiling.

## Reviewed and not cut

- Twelve packages each have their own `writeJSON` and `writeErr`. Each maps its own domain errors to HTTP. Collapsing them changes a shared interface across about twelve domain packages, the supervisor and the marketplace. That is an integration-lead change with its own review, not a release-time cut. It isn't a shortcut, so it carries no ponytail marker.
- The practice and billing document stores (`practice_records`, `billing_records`) are recorded ceilings (PONYTAIL-DEBT.md Practice 1, Finance 12) with concrete triggers. Their migrations are frozen by checksums, so their markers point at the ledger entries that name the triggers.

## Numbers

The report records the final values at its `tested_revision`: net line count against the release base 47e73ee1616933ac18707cc9c30912edc313f3ac, the debt ledger count, and the number of `ponytail:` markers. Most added lines are tests: the release adds executable acceptance for every criterion it closed.
