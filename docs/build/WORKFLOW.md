# Parallel build workflow

Cursor Cloud Agents implement in isolated Cursor-hosted VMs. GitHub holds the canonical source, small PRs, tests and recovery tags. Codex independently reviews the exact base and head revision before merging. Render-1 holds only the control checkout; do not build or test-install the product on Patrick's machines. Use synthetic data only.

## Launch and ownership
Start with four workers: data/contracts, platform scaffolding, API contracts and QA acceptance mapping. Their assignments and owned paths are in tasks.json. Each starts from the same pushed revision in its own VM and branch. The integration lead owns merges, shared interfaces, dependency pins, migration allocation and router composition. Initially delegate the shared contract files to the data/contracts task; other workers submit proposed shared changes in their PR.

Run `python3 scripts/cursor_agents.py launch bootstrap` with CURSOR_API_KEY available. Agent IDs are saved before submission so an uncertain response can be reconciled without duplicate agents. Rerunning launch reuses IDs; it does not launch replacements. Use `status` to inspect. No agent inherits Render-1 credentials, MCPs or production access. Use Cursor's configured default model unless explicitly selected.

## Dependency waves
1. Bootstrap contracts, Platform, API and QA work in parallel. Platform may draft against proposed contracts but cannot claim integrated phase completion until reconciled.
2. Identity and Practice follow accepted shared contracts. Their integration establishes phase 1/2 behavior.
3. Billing and Trust follow identity/practice APIs. Calendar can start when its prerequisites pass.
4. Storage, LawCraft and Doc Tender follow file/auth/settings contracts, in separate bounded PRs. UI works against real APIs as they become ready.
5. Module runtime and marketplace separate after their dependencies pass.
6. Release integrates all seven original PRD phases, signed images, updates, backups and clean Docker Desktop acceptance.

Later tasks in tasks.json are assignments, not claims of completion. The persistent controller launches ready assignments after protected dependency merges. Integration assignments must additionally supply their passing phase report and all named product CI jobs. The manual launcher remains a recovery tool; do not run it alongside the controller.

## PR checkpoint
Open a small PR using the template with task ID, PRD sections, acceptance IDs, test commands/results and pending checks. Include screenshots and console checks for every changed page. Do not label scaffolding as shipped functionality.

Setup checks validate metadata only. Platform must add real Go/unit/integration, Postgres and end-to-end CI. Product phases require those executable tests; a metadata pass does not establish product acceptance.

The required Codex checkpoint reads the trusted base instructions and candidate diff with read-only tools (Read, Glob, Grep). Deny rules keep those tools away from process data, runner files and git credentials, and a result that contains a credential is withheld. It emits JSON with base/head SHAs, verdict and actionable findings; the report remains an Actions artifact and summary. Missing keys, malformed output, unavailable reviews, timeouts and fail verdicts block merging. Cursor fixes findings and every new commit reruns the gate. The implementation agent never approves itself.

The integration lead updates the branch against current main, resolves integration issues and merges only when all required checks pass and findings are resolved. The strict branch gate requires retesting after main changes. Never force-push, directly push main, or bypass protection.

## Phase completion
Run combined acceptance cases on the integrated SHA at each of the original seven phase boundaries. Report PRs, SHAs, acceptance IDs, exact commands/results, UI QC, simplification net line count and debt ledger count in docs/build/reports/phase-N.md and on the tracking issue mentioning @pattalkslaw-del. Tag phase-N only after all assigned criteria pass. Routine milestones do not wait for Patrick.

Final acceptance additionally requires the full suite on a fresh Docker Desktop installation, updater load testing, backup/restore and signed-image verification. Linux Compose CI alone does not verify Windows/macOS Docker Desktop compatibility.

## Simplification
Cursor does not assume Ponytail's Claude Code slash commands or hooks work. Preserve their substance explicitly: reuse existing code and the Go standard library first, review each phase diff for unnecessary complexity, apply only cuts that preserve PRD requirements, rerun tests, and record net lines and PONYTAIL-DEBT.md count. Every deliberate shortcut needs a ceiling, upgrade path and concrete upgrade trigger. Perform a whole-repo audit before the release tag.

## Credentials and cost
The review gate runs Opus through the Claude Code GitHub Action, authenticated by the CLAUDE_CODE_OAUTH_TOKEN repository secret (a `claude setup-token` token on the Max subscription). The check keeps the name Codex checkpoint so branch protection and the build controller are unchanged.
Keys remain in the control environment or GitHub Secrets. Do not copy local credential files into cloud VMs or the repository.

## Repository enforcement
The source repository is public as required by the PRD. Main requires pull requests, current-base Setup checks and Codex checkpoint success, resolved conversations, and protection for admins; force pushes and branch deletion are disabled. The native Codex app is not required for the Actions-based checkpoint.

## Automatic integration lead
See [AUTOMATION.md](AUTOMATION.md). The persistent controller dispatches corrections, refreshes branches, verifies exact-revision Codex artifacts and all checks, merges through GitHub protection and launches ready assignments. Dedicated cloud integration tasks wire the accepted packages into a running product and test phase behavior. A bounded merge is not full acceptance.
