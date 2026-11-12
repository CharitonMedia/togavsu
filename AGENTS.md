# Togavsu build instructions

Read togavsu-core-prd.md first. It is the approved product scope. Module tasks also read togavsu-initial-modules-prd.md, the approved scope for the initial modules (core PRD section 9.10). Read docs/build/WORKFLOW.md and your assignment in docs/build/tasks.json before editing.

## Implementation
Implementation agents work in isolated hosted workspaces, one branch and workspace per assignment: Cursor Cloud Agents in Cursor-hosted VMs, or, when docs/build/controller-plan.json sets worker_backend to claude-actions, Claude Code lane jobs on GitHub-hosted runners with a read-only token. Patrick approved the Claude Code lanes on 2026-11-09 while Cursor usage was exhausted. Start with at most four implementation agents plus one integration lead. Codex is the independent checkpoint reviewer. Never use client data, production services, or Patrick's machines for builds or test installs.

The integration lead owns shared contracts, dependency pins, migration numbering, integration and merges. Domain agents own only their assigned paths. Propose shared changes in the PR rather than silently changing another agent's interface. No direct pushes to main, force pushes, self-merges, or skipped checks. Commit and push explicit changed paths.

Every task maps to PRD sections and acceptance IDs in docs/build/acceptance.json. Deliver working behavior and meaningful positive and failure-path tests. Never mark a phase complete from a scaffold, mock, skipped test, or passing documentation checks. Distinguish implemented, tested, and pending.

Use the Go standard library and existing dependencies before adding abstractions or dependencies. Do not cut a PRD requirement to simplify code. Known deliberate shortcuts carry a ponytail comment naming ceiling and upgrade trigger; update PONYTAIL-DEBT.md. Cursor does not assume Claude Code slash commands or hooks run; do the equivalent simplification review explicitly and record net line count and debt count.

## Code Review Rules
- Verify assigned requirements and acceptance cases against the actual diff and evidence. Report missing required behavior and tests as actionable findings; separate unfinished scope from regressions.
- Matter walls, roles and attorney-only redaction must apply in services across UI, API, MCP, search, reports, file access and module extensions. UI checks alone do not satisfy access control.
- Preserve append-only trust/audit ledgers and immutable file versions; use reversals, typed file relationships and page/timestamp citations. Respect person-locked Doc Tender fields on reprocessing.

Review a named base SHA and head SHA. A pass applies only to those SHAs; changes require another review. Fail closed on unavailable reviews, invalid output, missing evidence or unresolved findings. The implementation agent must not write its own passing review. Codex review complements executable tests, visual QC and final clean-install acceptance.
