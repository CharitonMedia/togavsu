# Core-brokered module contracts

Contracts use the admin-approved consumer/provider links, module bearer scopes,
limits and signed delivery described in [the module API](../api/modules.md).
A consumer never calls or learns a provider's address. `X-Togavsu-Sender` and its
separate v2 signature bind the caller's module id; payload identity is untrusted.
Every provider must verify the timestamped v1 and sender-bound v2 signatures,
check the contract/operation headers and durably deduplicate effects.

## `processor.payment.v1`

This is the binding operating-card authorization contract for paid intake before
there is a core matter. The consumer declares `consumes: processor.payment.v1`
and requests `contracts:call` and `payments:write`. The provider declares
`payment_processor` with `operating`, subscribes to `payment.processor_authorized`
(the existing processor registration rule), requests `contracts:provide` and
`payments:write`, and provides **both** `authorize` and `recover`. The admin must
approve those scopes and link the modules. The consumer's current bearer must
also hold both of its scopes; installation approvals cannot replace them.

Call `POST /api/v1/modules/contracts/processor.payment.v1/authorize` (MCP
`callModuleContract`, path `contract: processor.payment.v1`, `operation: authorize`)
with exactly:

```json
{"intent_id":"synthetic-intake-1","amount_cents":2500,"destination":"operating","payment_token":"synthetic-hosted-field-placeholder"}
```

`intent_id` is a durable consumer-generated id, 1 to 200 characters, beginning
with a letter or digit and containing letters, digits, `.`, `_`, `:` or `-`.
Use one id for the entire paid intake attempt and every retry. `amount_cents`
is a positive integer. `destination` is exactly `operating`: pre-matter trust
payments are unavailable. `payment_token` is an opaque token from processor-hosted
card fields. Never send card data to core or modules.

The provider returns HTTP 200 for a terminal result, or 202 for pending, with
exactly these fields (`reference_number` and `decline_code` are conditional):

```json
{"intent_id":"synthetic-intake-1","status":"approved","amount_cents":2500,"destination":"operating","reference_number":"synthetic-approval-1"}
```

The result must echo the intent, amount and destination. `status` is `approved`,
`declined` or `pending`. Approved requires a nonempty `reference_number` of at
most 200 characters and no decline code. Declined has no reference and may have a
`decline_code` using the intent-id character/length rules. Pending has neither
field. Unknown fields and mismatched results fail closed. A decline means the
consumer creates no intake matter, parties or payment. Pending means the
consumer waits and recovers; it must not continue the paid intake writes.
Only approved permits that continuation. The broker itself writes no core
matter, contact, payment or trust entry on any of these paths. Background receipts
and broker calls audit the module with source `service`.

Recover through the same URL with operation `recover` and exactly:

```json
{"intent_id":"synthetic-intake-1"}
```

The result has the same shape. An unknown core intent is `not_found`. On the
first authorize the core durably binds the original consumer, provider, request
digest, amount and destination **before** the provider request. It stores no
hosted payment token. A unique durable claim permits only one initial authorization across core
processes. Concurrent repeats may return pending while that request is in flight;
recover them. A terminal approval or decline cannot be overwritten by a stale
pending recovery. A
changed authorize body is `idempotency_conflict`, regardless of Idempotency-Key;
that header does not define financial identity. These two processor operations
bypass the generic HTTP response cache even when that header is supplied. The
header's 200-character bound still applies, but every retry checks the current
bearer's scopes, module approvals and provider link and reads the latest durable
receipt. A retry after recovery therefore returns the approval; it cannot replay
an earlier pending response or a response authorized before scope/link revocation.
Every other contract retains its existing HTTP idempotency behavior.
Approved and declined receipts
replay indefinitely without another provider request. There is no expiry that
could turn a reused intent into another charge. Token rotation does not change
the consumer identity. A retry of an ambiguous or pending authorization calls
only provider `recover`, never provider `authorize`. A provider error or 404
leaves that intent pending. Recovering an approval must therefore work after a
lost HTTP response and before the consumer has created any core record. Changing
the provider cannot move an existing intent; unlinking, disabling, revoking the
required approvals or removing the declared operations blocks future calls,
including cached recovery. Neither module receives the other's address.

The processor module is not supplied by core. It must implement:

1. `authorize` and `recover` on its declared paths, validating the real core
   signatures and binding durable intent state to the verified sender plus
   `intent_id`, not a caller-claimed consumer id.
2. A processor-hosted field flow and external charge adapter. Durably deduplicate
   authorization and bind its immutable amount/destination before external money
   movement. One consumer's id cannot replay another consumer's charge.
3. Durable lookup of approval/decline/pending and the reference after a lost
   response, restart or retry. `recover` must never initiate a charge. Never
   report a completed approval as unknown. Resolve a pending/unknown outcome by
   querying the external processor; do not ask intake to submit a new charge.
4. The exact bounded response schema above, without card data, hosted tokens,
   credentials, provider addresses or unrelated records in responses or logs.
5. Letting the consuming module record the stored approval through the core `record`
   continuation below. That operation never calls the provider again. Existing
   viewer/task processor workflows continue to work unchanged.

The intake consumer must persist its intent id and approved continuation state,
create core records only after approval, and recover/retry record creation with
stable core Idempotency-Key values. It must preserve and surface a charged but
not yet recorded result rather than asking for payment again. The pre-matter
contract does not open arbitrary service-principal payment writes or intercept unrelated createMatter callers. This call order is binding on the paid intake consumer, which persists its own continuation state.

### Record the approved receipt

After approval, the consuming module creates its matter/contact, then calls
`POST /api/v1/modules/contracts/processor.payment.v1/record` (`callModuleContract`
on MCP, contract `processor.payment.v1`, operation `record`) with:

```json
{"intent_id":"synthetic-intake-1","matter_id":"019b6968-4600-7001-8000-000000000001","contact_id":"019b6968-4600-7001-8000-000000000002"}
```

`intent_id` and `contact_id` are required. `matter_id` is optional: if supplied,
the matter must have been created by this consumer after approval, and the contact
must be a party on it; otherwise the contact must have been created by this consumer
after approval. Legacy 2f receipts without an approval timestamp use their append-only approval
audit timestamp; a missing approval audit fails closed. Scope `payments:write` is required on the current consumer bearer
in addition to `contracts:call`, with both modules' current approvals and link.
This is a service operation, with no viewer/context token. Only this consumer's stored
approved operating receipt grants this continuation. Pending/declined receipts conflict;
unknown/other-module intents are `not_found`. Amount, destination, provider, reference,
status and hosted tokens cannot be supplied in this request; unknown fields fail.
Trust recording remains unavailable.

HTTP 200 returns the normal `Payment`. Core uses only the stored receipt's amount,
operating destination, provider as `source`, reference, method `card` and status
`completed`. A contact-only payment has null `matter_id`. Binding, payment, core row,
a distinct append-only `binding` audit naming intent/payment/matter/contact IDs and
normal `payment.created`/`payment.completed` events commit together, under
the consumer module as actor with source `service`. `AuditEntry.actor_id` is a
string for every module service-principal audit row. A row lock serializes concurrent
recording across core processes. Repeats/recovery return the same payment without
another provider request or charge. A changed target conflicts. Live link/scope and
matter/contact visibility are rechecked even on replay. No arbitrary viewerless
`createPayment` write opens. The receipt has no expiry or alternate intent alias.

`modulekit.ProcessorPaymentRecord` and public `modulekit.ID` support typed callers;
`c.RecordProcessorPayment(consumerID, request)` exercises the real operation in coretest.
`c.PushContractEvent(providerID, modulekit.BindingContractEvent{…})` pushes the binding
envelope through the real queue; consumer handlers use `coretest.ReadDelivery`.

`coretest.NewProcessor()` supplies a deterministic, in-process provider for module
acceptance. Register `c.Serve(id, processor.Handler)`, install the manifest and
approve its link. `DeclineNext("card_declined")` scripts no charge;
`LoseNextApprovalResponse()` records an approval and returns HTTP 503 so the
consumer must recover. `Calls()` returns verified operation/consumer/intent
metadata without tokens; `Charges()` counts new approvals. The helper verifies
the real broker signatures and uses no outside service. Its in-memory provider
state is synthetic fixture state, not a replacement for production durable
processor idempotency.

## Batch 2 contract events

**Events. Every contract event is `{"contract": "<name>", "event": "<type>", "event_id": "<uuid>", "occurred_at": "<RFC 3339>", "data": {...}}`. `event_id` is unique; consumers dedupe on it permanently.** A provider pushes a contract event through the core the same way core events are delivered, and the consumer's events endpoint receives it.

This binding envelope applies to every contract, including `documents.generate.v1`
(documents → workflows), `signature.request.v1` (lawpay-docuseal → mo-estate-planning),
and `phone.provider.v1` / `meetings.provider.v1` (phone-zoom / phone-google → phone).
Provider selection and the manifest's `many` rule remain unchanged.

A documents provider posts this shape to
`POST /api/v1/modules/contracts/documents.generate.v1/events`:

```json
{"contract":"documents.generate.v1","event":"document.generated","event_id":"d210ad4a-3c92-4ed1-9684-8fb164155760","occurred_at":"2026-11-20T12:00:00Z","data":{"run_id":"d210ad4a-3c92-4ed1-9684-8fb164155761","matter_id":"d210ad4a-3c92-4ed1-9684-8fb164155762","template_id":"d210ad4a-3c92-4ed1-9684-8fb164155763","file_ids":["d210ad4a-3c92-4ed1-9684-8fb164155764"]}}
```

The provider needs its own approved `contracts:provide` bearer and a matching
manifest `provides` declaration. Body `contract` must equal the route; `event`
is a nonempty type outside **every core event namespace**, including `file.*`,
`matter.*`, `contact.*`, `calendar.*`, `calendar_event.*`, `payment.*`, `session.*`
`custom_field.*`, `conflict.*`, `county.*`, `practice_area.*`, `referral_source.*`,
and all namespaces in the shared published `x-togavsu-core-events` registry. `event_id` is a UUID,
`occurred_at` is RFC3339, and `data` is a JSON object. Unknown outer fields fail.
Data is contract-owned and conveys no core record access.

Every enabled consumer declaring this contract, holding approved `contracts:call`
and linked by the admin to this provider receives the envelope's original fields
and values unchanged, without another core envelope. No link means no delivery.
Normal durable signed retries apply, with permanent per-recipient/event-id
dedupe in a `contract:` queue namespace separate from core UUID event IDs. A core
event and a contract event with identical wire IDs both deliver. Replays insert no
rows and audit `consumers=0`; forwarded wire IDs remain unchanged. The first
accepted ID retains its original sender and payload across providers/contracts,
as before. Duplicate
top-level JSON keys return 400 before forwarding.
`X-Togavsu-Sender` is the authenticated pushing provider, persisted in the queue;
2e v1 and sender-bound v2 signatures remain unchanged. Unlinking blocks new pushes
to that consumer; already queued retries retain their original sender and body.
Use `modulekit.VerifyWebhookSender` or `coretest.ReadDelivery`, then durably dedupe.

### Also accepted: targeted envelope

The complete 2f targeted envelope stays accepted and delivered unchanged. Existing
phone, phone-zoom, phone-google and workflows callers need no migration:

```json
{"id":"synthetic-event-1","contract":"documents.generate.v1","consumer_id":"synthetic-consumer","occurred_at":"2026-11-20T12:00:00Z","action":"document.generated","payload":{"request_id":"synthetic-request-1","file_id":"synthetic-file-id"}}
```

`id`, `contract`, `consumer_id`, `occurred_at`, `action` are required; `payload`
is arbitrary JSON. Only that enabled, approved, linked consumer receives it.
Existing action prefixes remain: `<contract>.…`, with `document.generated` an
exception only for `documents.generate.v1`. Actions also stay outside every core
event namespace, even if their contract prefix matches. Dedupe remains per recipient/`id`.
The two formats cannot be mixed. Neither format uses top-level `type`.

Core update 2g also provides service occupancy under `calendar:read`
and module-bound frame CSRF. See [module APIs](../api/modules.md) and
[the complete continuation notes](../api/core-update-2g.md). Contract event inserts
check legacy provider-created raw event keys as well as the `contract:` key, so
upgrading does not redeliver an already accepted event. Contact-only payment and
refund events follow the parent payment's contact walls on reads and delivery.

Ordinary timeline appends under shared roots are classified by `record_type`:
`payment`, `payment_refund` and `processor_initialization` use the current money
record visibility checks regardless of their action name. An appended
`payment.future_event` on a `fee_agreement` follows ordinary matter visibility
on Activities and REST/MCP event reads and remains eligible for subscribed
module delivery. Exact emitted core actions such as `payment.created` remain
reserved from module appends. Binding contract envelopes still reserve core roots.


## Core updates 2h/2i: occupancy, video and navigation

On REST `GET /api/v1/calendar-events?view=occupancy` and MCP
`listCalendarEvent`, approved `calendar:read` service principals receive walled
intervals with no **active** attendee user. In 2i, deactivated attendees no longer
count as assigned; reactivation hides the walled interval on the next read.
`view=busy` also includes these unassigned walled intervals. Each interval is
exactly `starts_at`, `ends_at`, `all_day`. The all-day flag is the **only widening
of 2h's times-only disclosure**: no event id, title, matter, kind, attendee,
video or wall metadata. Ordering, window/range limits and pagination stay the
same. Other service reads and user detail reads retain walls.

Video bookings carry optional `video` with required boolean `requested`, nullable
`provider` (module id), nullable active `host_user_id`, and nullable opaque
`provider_meeting_id` (at most 200 characters). Location carries the join link.
Create/update use the existing event write policy. On PATCH, omission preserves,
null clears and an object replaces video, with requested still required. Calendar
notifications retain the existing record-reference envelope; modules read video
with the event's other fields through the scoped get operation.

A frame may navigate to another enabled module's declared staff tab only when
an admin-approved contract link connects them in either direction. Live server
resolution filters query/message params to the target's `tabs[].params`.
Unlinked/disabled modules, client tabs and other cross-module surfaces are refused.
See [module APIs](../api/modules.md) for the message and bridge route, and public
`modulekit.CalendarVideo` / `CalendarOccupancyBlock` / `CalendarBusyBlock` and
`c.CalendarOccupancy` for typed callers and real-core acceptance fixtures.
