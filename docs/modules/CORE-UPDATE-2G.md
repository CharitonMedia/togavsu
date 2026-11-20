# Core update 2g: remaining documents and intake continuations

CORE-23, CORE-29, CORE-30, CORE-31 and CORE-36 remain in `Accepts().core_items`.
This update finishes their remaining batch 2 behaviors without a version bump.

| Gap | Implemented behavior | Core regression evidence |
| --- | --- | --- |
| Binding event returns 400 | Every contract accepts contract/event/event_id/occurred_at/data. Original fields fan out through the durable signed queue to all enabled, approved linked consumers. Core namespaces, malformed UUID/time/data and mixed envelopes fail. Targeted 2f envelopes retain their rules and delivery. | `TestCoreUpdate2GBindingEnvelope`, retained 2d/2e/2f contract tests |
| Client frame cannot render session CSRF | Authenticated /m/ GET passes trusted `X-Togavsu-Session-CSRF`; hidden `csrf` works with a browser cookie jar. Responses are no-store; missing and other-session tokens still fail before POST reaches the module. | `TestCoreUpdate2GFrameSessionCSRF`, retained body-restoration test |
| Walled event without attendees never appears busy | Service-only calendar `view=occupancy` under calendar:read returns starts_at/ends_at only for all events, including unassigned/walled events. Existing user busy and detail reads retain their policy. | `TestCoreUpdate2GFirmOccupancy`, retained CU2-02 and busy tests |
| Approved public charge cannot be recorded without a viewer | Consumer `processor.payment.v1/record` binds its stored approval once to its newly created matter/contact. Stored money fields alone record one completed operating card payment. Atomic binding/payment/audit/events and same-payment replay never call the provider. | `TestCoreUpdate2GProcessorRecording`, `TestCoreUpdate2GProcessorContactAndRefusals` |

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
without an approval timestamp use intent creation as the lower bound.

Migration 1551 uses an unused version within the already allocated 1550–1599 block:
it expands payments.matter_id to nullable for contact-only receipts, retaining the
foreign key on every matter-bound payment. The integration lead must retain unique
migration numbering when integrating. Contact-only payment reads, reports, event
delivery and receipt replay follow the contact's current walls. Background audit
rows now name the service module as actor, matching service event attribution;
legacy module-source audit attribution retains its existing behavior.

The implementation is exercised against synthetic Postgres data. The new four
primary regression tests failed against starting revision
`4408f8d892f93f7ea005ce8c0ba20f90930de878`; independent module suites, visual
browser QC, Docker/Compose/supervisor image and clean-install/release checks remain
separate acceptance. See `docs/build/reports/core-update-2g.json` and the PR body
for the exact tested code revision, actual check results and pending criteria.
