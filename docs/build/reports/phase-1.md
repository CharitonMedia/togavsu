# Phase 1 report: foundation, identity, settings and audit

Status: **phase cases pass; full product acceptance pending.** Every phase 1 case passes on the composed core at `4b3541d8abdd308854734e524b2f48486f066478`, and local runs of all six required CI jobs (Core tests, API contract tests, QA harness tests, Supervisor tests, Backup integration, Supervisor image) pass there. GitHub Product CI on that SHA is pending; it last passed all six jobs at 05fde77 ([run 42761536976](https://github.com/CharitonMedia/togavsu/actions/runs/42761536976)), before the audit-log wall fix and the review-comment fixes. Only AC-030 and AC-035 of the phase 1 criteria pass globally; the rest are pending below, so this is phase evidence, not global acceptance of those criteria.

Inputs: contracts, platform, api, qa (bootstrap wave), identity (#21) and practice (#20), merged on main at `a7d0526`. Integration branch: `claude/togavsu-integrate-practice-ee9793df`.

## Phase cases at 4b3541d8abdd308854734e524b2f48486f066478 (all exit 0)

| Case | Criteria | Command |
| --- | --- | --- |
| foundation-first-run | AC-001 | `sh scripts/ci/test-compose-first-run.sh` |
| identity-access | AC-003, AC-004, AC-018, AC-024 | `go test -count=1 -v -run '^(TestWallsRolesAuditAndRestart\|TestFirstRunGate\|TestBrowserSetupAndSignIn\|TestAC003WallHidesMatter\|TestAC024HistoryRedactsAttorneyOnly\|TestAC024AppendOnlyRefusesMutation\|TestAuditLogKeepsWallAndRedaction\|TestApplicationRoleCannotChangeAuditRows\|TestServiceMutationIsOneTransaction\|TestConcurrentClosesSerialize)$' ./internal/core/ ./internal/practice/ ./internal/audit/` |
| api-mcp-practice | AC-002 | `sh scripts/ci/test-api.sh` |
| settings-audit | AC-029, AC-030, AC-035 | `go test -count=1 -v -run '^(TestBearerScopesMCPAndSettings\|TestFreshInstallSchemaAndSeed\|TestPRDDefaults\|TestModuleClean\|TestAC030Fixtures\|TestScanSource\|TestRegistrySetResetAndReload\|TestRegistryAtomicAudit\|TestSeedAndSetup\|TestSchemaContract)$' ./internal/core/ ./internal/settings/ ./internal/db/` |

The full command list with exit codes is `docs/build/reports/integrate-practice.json`.

## Criteria

Passed globally: AC-030 (the AC-030 source check passes on the whole module and fails its fixtures), AC-035 (a fresh install has all 54 section 5 tables and the synthetic seed fills each).

Pending globally, with what passes now:

- AC-001: one Linux Compose up with no edits reaches setup, two-factor completes, the install is ready, and a core restart keeps the session and data. A clean Docker Desktop install has not run.
- AC-002: 99 of 427 contract operations are mounted on `/api/v1` and offered on `/mcp`; every one refuses anonymous callers and tokens need the contract scopes. Later-wave features are not built, and two served practice routes are missing from the contract.
- AC-003: walls hold across list, search, widget, report, screens, API and MCP, and after a restart. Files, calendar, billing and trust surfaces are not built.
- AC-004: the accountant reads trust and AR reports and cannot write practice records, areas or settings. Billing and trust records are not built.
- AC-016: not exercised by this assignment.
- AC-003 / AC-024 on the audit log: `GET /api/v1/audit-log` and its MCP tool drop rows about a matter behind a wall the caller is not on (and its party contacts and notes) and redact attorney-only values as history does (`TestWallsRolesAuditAndRestart`, `TestAuditLogKeepsWallAndRedaction`).
- AC-018: every identity, settings and practice write reaches the append-only `audit_log` with actor and source; Compose runs the core as `togavsu_app`, which cannot UPDATE, DELETE or TRUNCATE it, including after dbinit takes over a volume whose `core` schema the Postgres superuser owned (`test-compose-first-run.sh`). Backup to the admin's computer, S3 and a network path with a verified restore of the running core is not demonstrated here (the supervisor suites and backup integration pass).
- AC-024: history shows old and new values; attorney-only values are redacted to staff. The application role can still change `events` (AC-024-F4).
- AC-029: PRD defaults are settings on admin page tabs; a change applies on the next request, is logged with old and new values, and Reset restores it. Settings read by unbuilt modules cannot show their effect yet.

## Visual QC

Repeated at `05fde772418cf3b9ffbcf56e521e2b02cae8f67b` (still current at `4b3541d`: no template, page handler or web asset changed after it; all 14 steps; previous record was at 527a5a1, and no screen template changed since). Chrome headless screenshots of setup, the setup validation error, matters, settings (security tab, changed, reset, invalid), contacts, sign-out, sign-in and two-factor are in `docs/build/reports/screens/integrate-practice/`, with `qc.json`: no page errors; the only error responses are the two deliberate 400 submissions. Fixed during QC: every setting showed "changed" because the seed stores defaults; practice screens had no sign-out; a signed-out browser on a practice screen got a JSON 401 instead of the sign-in page. Open: the invalid-setting message repeats the registry text.

## Simplification and debt

Go diff from the PR base `b3b6165` to `4b3541d`: +3952 / -96 (net 3856; non-test net 2386). No new modules. Debt ledger entries: 33 before, 37 after (four integration ceilings; the fifth, superuser-owned volumes, was removed when `deploy/postgres/core-role.sh` began handing schema `core` to `togavsu_app`; the practice memory-store and process-local-key entries were narrowed because the core now persists practice rows and passes the installer key).
