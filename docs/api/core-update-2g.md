# Core update 2g: remaining documents and intake continuations

CORE-23, CORE-29, CORE-30, CORE-31 and CORE-36 remain in `Accepts().core_items`.
This update finishes their remaining batch 2 behaviors without a version bump.

| Gap | Implemented behavior | Core regression evidence |
| --- | --- | --- |
| Binding event returns 400 | Every contract accepts contract/event/event_id/occurred_at/data. Original fields fan out through the durable signed queue to all enabled, approved linked consumers. Core namespaces, malformed UUID/time/data and mixed envelopes fail. Targeted 2f envelopes retain their rules and delivery. | `TestCoreUpdate2GBindingEnvelope`, retained 2d/2e/2f contract tests |
| Client frame cannot render session CSRF | Authenticated /m/ GET passes module-bound `X-Togavsu-Session-CSRF`; hidden `csrf` works with a browser cookie jar. Responses are no-store; missing and other-session tokens still fail before POST reaches the module. | `TestCoreUpdate2GFrameSessionCSRF`, retained body-restoration test |
| Firm occupancy for service callers | Service-only calendar `view=occupancy` under approved calendar:read returns starts_at/ends_at only for visible assigned/unassigned events; every walled matter is omitted until its wall is lifted. Existing user busy and detail reads retain their policy. | `TestCoreUpdate2GFirmOccupancy`, retained CU2-02 and busy tests |
| Approved public charge cannot be recorded without a viewer | Consumer `processor.payment.v1/record` binds its stored approval once to its newly created matter/contact. Stored money fields alone record one completed operating card payment. Atomic binding/payment/audit/events and same-payment replay never call the provider. | `TestCoreUpdate2GProcessorRecording`, `TestCoreUpdate2GProcessorContactAndRefusals`, `TestCoreUpdate2GLegacyReceiptApprovalOrder` |

New runtime criteria: `contract.binding_events`, `module.frame_session_csrf`,
`calendar.firm_occupancy`, `processor.receipt_recording`. The public kit exposes
`BindingContractEvent`, `ProcessorPaymentRecord`, `ID`, `FrameSessionCSRFHeader`,
`c.PushContractEvent` and `c.RecordProcessorPayment`; `ReadDelivery` verifies the
real signed event bytes. No external provider is used by acceptance fixtures.

The recording receipt stays owned by its original consumer and provider, with
current bearer scopes, install approvals and link checked on every call. No
viewer context or caller-supplied amount/destination/reference/provider is accepted.
A changed target conflicts; unknown/other-module, pending and declined receipts
cannot record. Trust recording remains unavailable. Existing viewer/task processor
workflows and ordinary payment gates remain in place. Practice's immutable creation
event proves target ownership and creation after approval; legacy 2f receipts
without an approval timestamp use their append-only approval audit timestamp,
failing closed when that evidence is unavailable.

Migration 1600 expands payments.matter_id to nullable for contact-only receipts,
retaining the foreign key on matter-bound payments. `internal/db/ranges.go`
assigns 1550–1599 specifically to **core-update-2d**, rather than batch 2 as a
whole; this assignment uses the next free block, **1600–1649**, allocated to
core-update-2g. The former unmerged 1551 migration is now 1600. Its Down is a
comment-only expand-only no-op, consistent with 1550: nullable matter_id and all
contact-only money records remain on downgrade. The integration lead must retain
unique numbering. Contract queue keys have a fixed `contract:` prefix that core UUID event IDs
cannot carry. Inserts also check provider-created legacy raw keys, preserving
deduplication for delivered or pending rows from before the upgrade.

Contact-only payment reads, reports, feed/events API, MCP event reads, module
pump and receipt replay use billing's current payment wall policy. A contact
joining a walled matter hides its existing payment and refund events immediately. Allowed
users retain visibility; viewerless modules remain outside every wall.

`AuditEntry.actor_id` widened from UUID to **string for every module service-principal
audit row**, not only processor rows; background rows identify the module actor.
The receipt binding writes a distinct append-only `binding` audit containing
intent, payment, matter (when set) and contact IDs; it no longer repeats an
`approved` status transition. Payment metadata remains UUID-compatible.

## Fix pass 1

- Frame tokens are HMAC-SHA256 over fixed purpose, session ID and module ID, using
  identity's existing secret. Only cookie writes to that same `/m/{id}/` accept
  them. Core `/api/v1` writes, other modules, and changed sessions refuse them.
  Modules never receive the core token; authenticated GET/request and responses
  remain no-store and inbound token headers are stripped.
- Binding events reserve every core action/root through the shared generated
  `x-togavsu-core-events` registry, including custom_field, conflict, county,
  practice_area and referral_source. The generator includes published operation
  events and core emit sites, assignments, changedFields actions and constants; an AST regression checks actual emit sites
  and every registry name. Legacy `core.*`/`calendar.*` guards remain in the same
  registry. Existing contract event names remain accepted.
- Binding and targeted deliveries use per-recipient dedupe keys
  in a separate namespace from core event IDs. Wire IDs/bodies are unchanged.
  First acceptance retains its sender and payload across providers/contracts, as
  before. Replays insert no delivery and audit `consumers=0`, using RowsAffected.
  Duplicate top-level JSON keys in either envelope return HTTP 400.
- Firm occupancy uses approved `calendar:read` on REST and MCP. Both stores
  filter walled matters and exact interval overlap before pagination. Removing
  the wall returns those events immediately, without revealing wall membership.

Regression evidence: `TestCoreUpdate2GContactPaymentPumpWalls`,
`TestCoreUpdate2GFrameSessionCSRF`, `TestFrameCSRFSessionRotation`,
`TestCoreUpdate2GBindingEnvelope`, `TestCoreEventRegistryCoversEmitSites`,
`TestCoreUpdate2GEventDedupeNamespaces`, `TestCoreUpdate2GOccupancyScopeRefusal`,
`TestCoreUpdate2GFirmOccupancy`, `TestCoreUpdate2GOccupancyMemoryWindow`,
`TestCoreUpdate2GMigrationAllocation`, and `TestCoreUpdate2GProcessorRecording`.
The PR depends on the integration lead's control change adding the core-update-2g
assignment being merged into batch2. This worker does not change those files.

The implementation is exercised against synthetic Postgres data. The new four
primary regression tests failed against starting revision
`4408f8d892f93f7ea005ce8c0ba20f90930de878`; independent module suites, visual
browser QC, Docker/Compose/supervisor image and clean-install/release checks remain
separate acceptance. See `docs/build/reports/core-update-2g.json` and the PR body
for the exact tested code revision, actual check results and pending criteria.

## Fix pass 2

Occupancy follows service-call walls under calendar:read. Refund visibility resolves
its parent payment for Activities, REST/MCP event reads and module subscriptions.
Core append refuses every reserved namespace root, including future actions.
The registry scans action assignments, changedFields calls and constant declarations.
Contract inserts preserve legacy raw-key deduplication across both envelope formats.
Frame regressions assert csrf_required on core event writes. Migration 1600 and
its expand-only no-op Down remain unchanged in the allocated 1600–1649 block.
