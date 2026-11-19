# Togavsu marketplace service

PRD section 10, plus batch 2 listing price and signed upload (CORE-CHANGES-2 §17 as ruled 2026-11-17). A small Go service, deployed separately from any firm's stack, that lists signed modules. The marketplace lists, installs and checks package signatures. It never sells, charges, subscribes, renews, refunds or issues a license — selling and licensing belong to each module's provider. The operator does not review, inspect, test or vouch for modules. Every listing stays private until Patrick releases a public marketplace.

Build and run (a module author's whole submission walk-through is in `docs/modules/MODULE-KIT.md`, Submitting to the marketplace):

```sh
docker build -f marketplace/Dockerfile -t togavsu-marketplace .
# or: go run ./marketplace/cmd/marketplace -config marketplace/config.example.json
```

Configuration is a JSON file (`config.example.json`); omitted fields keep the defaults in `fixed.json`. The operator token is read from `operator_token_file` (a Docker secret, at least 32 bytes), never from the config file or the environment. There is no Stripe, license seed, SMTP or platform-fee setting. State lives in `data_dir`: `state.json` plus one file per package, named by its SHA-256. There is no core migration; the marketplace keeps its own store (`store.go`).

## Signing

Packages are tar archives (gzip optional) with `togavsu-module.yaml` at the root, signed with Ed25519 over the exact package bytes. The signature is standard base64. A key is named by its key id, `ed25519-` plus the first 16 bytes of the SHA-256 of the raw public key, as hex. `internal/marketplace` holds the shared code (`Sign`, `Verify`, `KeyID`, `ReadManifest`, `BuildPackage`) that the service and the core both use.

The manifest module id `core` is reserved for core's authenticated deliveries.
The manifest reader and catalog upload reject it with a reserved-id validation
error, as do local and marketplace installs. Choose a different id for a module.

The listing price is taken from the manifest's `price` field at first upload: either the old string or CORE-35's structured `{amount_cents, currency, interval, note}` (`interval` is `month` or `year`). It is shown as the provider's information beside: "Sold and licensed by {author}. Togavsu doesn't sell or bill this module." The author can change the listed price and an optional link to their own sales page with `PATCH /v1/authors/me/listings/{id}/price` without uploading a new package; that change shows at once and does not touch installs.

`POST /v1/authors/me/packages` takes the kit's signed package file as its body unchanged (`listing_id`, `version`, `immutable`, `manifest`, `digest`, `signature`, `key_id`, `package_base64`). A tampered digest or package bytes is refused. A priced module installs from the local marketplace with no key and no payment step: `GET /v1/listings/{id}/versions/{version}/package` returns the signed package.

## API

Read API (the core; no credentials):

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/listings?q=` | Search. Pulled listings never appear. `price_note` carries the rendered price and the provider disclaimer. Structured price and sales URL stay on the HTML listing page. |
| GET | `/v1/listings/{id}` | One listing, pulled or not. |
| GET | `/v1/listings/{id}/versions/{version}/package` | Signed package. 410 when pulled. No payment or license step. |
| GET | `/v1/alerts?module_id=&module_id=&since=` | Flags and pulls. |
| GET | `/v1/signing-keys` | Every verified author key. |

Authors (bearer token from `/v1/sessions`):

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/authors` | Register. |
| POST | `/v1/sessions` | Login → bearer token. |
| GET/POST | `/v1/authors/me/keys` | List / register a signing key. |
| POST | `/v1/authors/me/keys/{key_id}/verify` | Prove possession. |
| POST | `/v1/authors/me/packages` | Upload the kit's signed file unchanged. |
| PATCH | `/v1/authors/me/listings/{id}/price` | Change listed price and/or `sales_url` without a new package. |

Operator (bearer operator token): flag and pull only. No platform-fee settings.

There is no checkout, subscription, renewal, refund, payment-service, platform-fee or license-key route.

## Tests

```sh
go test -count=1 ./marketplace/
```

Test helpers refuse any outside host (`RequireLocalBaseURL` in the test package, called from `env.do` / `env.doURL`). Outside payment providers are never dialed.
