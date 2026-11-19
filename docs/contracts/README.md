# Core-brokered contract events

The provider sends `POST /api/v1/modules/contracts/{contract}/events` using its approved
`contracts:provide` bearer. `ContractEventWrite` and OpenAPI accept this envelope:

```json
{
  "id": "provider-stable-event-id",
  "contract": "phone.provider.v1",
  "consumer_id": "phone",
  "occurred_at": "2026-11-19T12:00:00Z",
  "action": "phone.provider.v1.ringing",
  "payload": {"call_id": "synthetic-example"}
}
```

`id`, `contract`, `consumer_id`, `occurred_at` and `action` are required. The route and
body contract must match. The timestamp is RFC3339. Actions stay in the contract's
namespace, so providers cannot forge core events. `payload` is arbitrary JSON. The core
preserves the envelope's values, targets only the named enabled consumer's approved
link to the provider, and deduplicates by recipient/event id. An unlinked target fails.
The core signs delivery with the recipient's secret and timestamped HMAC; verify it with
`modulekit.VerifyWebhook`. No contract payload grants access to core records.

Contract calls carry a freshly signed provider-audience context when the consumer sent
a viewer context. The provider verifies/delegates it with its own bearer; live links,
roles, matter walls and both modules' scopes remain binding. Provider addresses never
reach consumers. `modules.contract_call_max_bytes` bounds requests and responses;
oversized provider responses return HTTP 413, code `too_large`.

These definitions document the core surface at core-update-2d. The assignment referred
to `docs/contracts/`, but that directory was absent from its base revision. Integration
must compare any separately held provider/consumer contract definitions before claiming
compatibility with them.
