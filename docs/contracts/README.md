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
matter, contact, payment or trust entry on any of these paths.

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
that header does not define financial identity. Approved and declined receipts
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
5. Recording an approved charge on the subsequently created matter using the
   existing core payment workflow and applicable viewer/task authorization. The
   pre-matter receipt itself is not an unrestricted payment-write grant.

The intake consumer must persist its intent id and approved continuation state,
create core records only after approval, and recover/retry record creation with
stable core Idempotency-Key values. It must preserve and surface a charged but
not yet recorded result rather than asking for payment again. The pre-matter
contract does not open arbitrary service-principal payment writes or intercept unrelated createMatter callers. This call order is binding on the paid intake consumer, which persists its own continuation state.

`coretest.NewProcessor()` supplies a deterministic, in-process provider for module
acceptance. Register `c.Serve(id, processor.Handler)`, install the manifest and
approve its link. `DeclineNext("card_declined")` scripts no charge;
`LoseNextApprovalResponse()` records an approval and returns HTTP 503 so the
consumer must recover. `Calls()` returns verified operation/consumer/intent
metadata without tokens; `Charges()` counts new approvals. The helper verifies
the real broker signatures and uses no outside service. Its in-memory provider
state is synthetic fixture state, not a replacement for production durable
processor idempotency.

## `documents.generate.v1` event delivery

Provider-pushed `document.generated` uses the same targeted envelope as phone and
workflow contracts. The following is the complete outer shape; `payload` is the
provider's generated-result data and is preserved as JSON, without core adding
another event envelope:

```json
{
  "id": "synthetic-generation-event-1",
  "contract": "documents.generate.v1",
  "consumer_id": "synthetic-consumer",
  "occurred_at": "2026-11-20T12:00:00Z",
  "action": "document.generated",
  "payload": {"request_id": "synthetic-request-1", "file_id": "synthetic-file-id"}
}
```

`id`, `contract`, `consumer_id`, `occurred_at` and `action` are required. `payload`
is arbitrary JSON; the example's inner fields illustrate generated-result data,
not a second core schema. The provider posts to
`/api/v1/modules/contracts/documents.generate.v1/events` with its own approved
`contracts:provide` bearer. The consumer must declare the contract, be enabled,
hold approved `contracts:call`, and have an admin-approved link to this provider.
The route and body contract match and the timestamp is RFC3339.

The exact action `document.generated` is an exception to the generic
`<contract>.…` action prefix for this contract only. A provider still cannot emit
`file.trashed` or impersonate a core event. Other contract action rules and
phone/phone-zoom/phone-google/workflow deliveries remain unchanged.

Only the named consumer receives the original envelope values. Repeated
recipient/event ids enqueue once. Delivery uses the normal durable retry queue
and the consumer's webhook secret. `X-Togavsu-Sender` is the authenticated pushing
provider, persisted at acceptance; the 2e timestamped v1 signature and separate
sender-bound v2 signature are unchanged. Unlinking refuses new pushes without
reattributing queued retries. Use `modulekit.VerifyWebhookSender` or
`coretest.ReadDelivery`, then durably deduplicate the event id before effects.
Delivered file ids confer no core read permission.

Top-level `type`, `event`, `event_type`, `data` and `provider_id` are not fields in
this envelope. In particular `action` cannot be renamed to `type`, and `payload`
cannot be renamed to `data`. A module expecting those fields needs a module-side
fix; core does not emit two envelopes. The base revision lacked this directory,
so separately held contract definitions and external module acceptance still
require integration comparison.
