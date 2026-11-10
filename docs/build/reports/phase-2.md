# Phase 2 report: practice records on the composed core

Status: **phase case passes; full product acceptance pending.** The practice phase case passes on the composed core at `05fde772418cf3b9ffbcf56e521e2b02cae8f67b`, and GitHub Product CI run [42761536976](https://github.com/CharitonMedia/togavsu/actions/runs/42761536976) passed all six required jobs on that same SHA. Only AC-025 of the phase 2 criteria passes globally; AC-009, AC-020 and AC-023 stay pending below.

## Phase case at 05fde772418cf3b9ffbcf56e521e2b02cae8f67b (exit 0)

| Case | Criteria | Command |
| --- | --- | --- |
| practice-records | AC-009, AC-020, AC-023, AC-025 | `go test -count=1 -v -run '^(TestPracticeRecordsOnCore\|TestDurableSurvivesRestart\|TestDurableFailedCommitLeavesNoTrace\|TestAC009CloseWarnsAndStillCloses\|TestAC020SearchScopesAndWall\|TestAC023ReopenAndZeroFee\|TestAC025CustomFieldAreaCSVAndUI)$' ./internal/core/ ./internal/practice/` |

Every practice suite also runs over the Postgres `Durable` store when `TOGAVSU_TEST_DATABASE_URL` is set (`sh scripts/ci/test-core.sh`).

## Criteria

Passed globally: AC-025 (a Probate-only field is absent from a Family matter and refused there, saves through `PUT /api/v1/matters/{id}/custom-fields` and the matter screen form, and the UI-saved value is in the CSV export).

Pending globally, with what passes now:

- AC-003 and AC-024: see the phase 1 report.
- AC-009: an unpaid invoice warns in close-preview and the matter closes once acknowledged. The invoice is written into the practice store as a stand-in; billing and trust ledgers are not built.
- AC-020: matter number, phone and invoice number are found in their scope and in All, and walled matters never appear to staff outside the wall. Invoices come from the stand-in; file-text search waits for files.
- AC-023: reopening refuses an empty reason, restores the earlier status and appears on the timeline; the zero-fee marker is covered by the practice suite. AR aging over real billing data is pending.

## Persistence

Practice rows now live in Postgres: `practice_records` (migration 200) holds each row as a document, and each mutation commits with its `audit_log` rows in one transaction; a failed commit reloads from Postgres (`TestDurableFailedCommitLeavesNoTrace`), and a restart keeps matters, walls and history (`TestDurableSurvivesRestart`, `TestWallsRolesAuditAndRestart`). Ceiling: the section 5 relational tables are not yet the practice source of truth (PONYTAIL-DEBT.md, practice ceiling 1).
