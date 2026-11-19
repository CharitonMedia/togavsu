# Core update 2e

Contract consumers can identify the provider that pushed each event, including a
queued event retried after that provider is unlinked. Core signs the sender stored
in `module_deliveries.created_by`, independently of provider-controlled JSON.
Contract calls identify the calling consumer; core events, grants and routed mail
identify `core`.

`X-Togavsu-Sender` and `X-Togavsu-Sender-Signature` add a timestamped v2 HMAC while
preserving the exact 2d `t=,v1=` signature header. No schema migration, dependency
change, release/version bump or module package change is required.
`modulekit.VerifyWebhookSender` requires sender authentication;
`coretest.ReadDelivery` exposes the verified sender in acceptance handlers.

The exact wire format, downgrade refusal and retry semantics are documented in
[MODULE-KIT.md](MODULE-KIT.md#core-update-2e-authenticated-delivery-sender),
[the module API](../api/modules.md#core-update-2e-authenticated-delivery-sender),
[API conventions](../api/conventions.md#module-delivery-sender-binding),
[shared contracts](../architecture/contracts.md#module-contract-delivery-identity)
and the generated OpenAPI module webhook and contract descriptions.
