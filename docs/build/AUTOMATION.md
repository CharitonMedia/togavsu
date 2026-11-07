# Unattended build controller

The controller runs on Render-1 as Patrick's systemd user timer, togavsu-build-controller.timer. It starts after boot and runs again two minutes after each completed tick. Linger is enabled, so neither an open chat nor a logged-in desktop is required. Builds, tests and application installs remain in Cursor cloud VMs and GitHub-hosted CI; Render-1 runs only control operations.

## Automatic cycle
- Adopt the four existing Cursor agents and their two already-used correction rounds.
- Wait while an agent or CI is active.
- Read the independent Codex artifact from the GitHub Actions check for that exact PR head and current main. Reject another PR's artifact, a stale base/head, malformed data or a spoofed non-Actions check.
- Send failures back to the same Cursor agent and PR, with regression tests and replacement of stale original PR evidence required. Persist the delivery intent before submission. A lost response is reconciled against Cursor's run identity; it is never blindly resubmitted.
- Refresh branches against main. Conflicts go back to the owning worker; successful refreshes trigger fresh tests/review. No forced updates or protection bypasses.
- Merge only after every reported Actions check succeeds, all named required jobs exist, the exact-revision Codex report passes with no findings, and main/PR head still match. Verify that branch protection still requires PRs, strict checks and enforcement for admins. GitHub makes the final protected merge decision.
- Record the protected merge as a bounded dependency. Launch ready assignments automatically, with at most four occupied implementation slots and a durable UUID per new agent.
- Cloud integration assignments compose the separate packages into the actual running product and must provide real product CI and passing integration reports. The final release remains subject to all 52 criteria; merged scaffolding is never full product acceptance.

## Limits and stops
The standing limit is three correction rounds per assignment, including rounds already dispatched manually. At that limit, pause the affected assignment and record its reason; independent tasks may continue. Routine Git conflict resolution has a separate three-attempt bound and does not consume code-defect correction rounds. An unavailable review gets one infrastructure rerun for a given base/head. An ambiguous prompt delivery, closed unmerged PR, protected control-file edit, missing/pending integration evidence or failed protection check never gets a fabricated pass.

A failed control request is recorded and retried by later observation, not by bypassing checks. External service failures may prevent progress. Status is persisted in .build-state/status.json and a dedicated comment on GitHub build issue #1 is updated when task state changes.

## Freeze and recovery
Immediate freeze: stop both togavsu-build-controller.timer and togavsu-build-controller.service through systemctl --user. Create .build-state/STOP to keep it paused across a later timer start. A STOP file is checked at the beginning of each tick.

State lives in the control checkout's ignored .build-state directory: controller.json contains durable IDs, delivery/merge intents, correction budgets and results; agents.json preserves the original launches. Do not delete state to retry a task. Investigate a stopped item, reconcile its external run and PR, then deliberately clear the affected halt or grant a new budget only with Patrick's direction.

## Deployment and credentials
The running controller and plan are copied from a reviewed commit into /home/patrick/.local/share/togavsu-controller. They are pinned; a worker changing main cannot replace the running controller. The installed receipt records the source commit and file hashes.

The entrypoint reads only the existing CURSOR_API_KEY from Patrick's local credential file. GitHub uses the existing authenticated gh configuration. No key values are in the repository or unit files, and no control credentials are injected into build VMs. Codex reviews use the GitHub secret already configured and API billing.

The deterministic controller tests cover feedback dispatch/deduplication, ambiguous requests, retry exhaustion, branch conflicts, stale or forged reviews, required CI, merge races, protection rejection, dependency launches and pause behavior. A live dry-run against existing PRs precedes enabling the timer.

Integration evidence is bound to a tested 40-character code revision; only report-file commits may follow that revision. Reports must exhaustively partition assigned acceptance IDs and pass every named phase case with its test command and mapped criteria. Final release permits no pending criterion. Integration leads are serialized through the dependency graph to preserve shared-file ownership. Correction dispatch and new launches share the same pre-observed four-worker capacity limit.
