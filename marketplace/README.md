# Togavsu marketplace service

PRD section 10, plus batch 2 listing price and signed upload (CORE-CHANGES-2 §17 as ruled 2026-11-17). A small Go service that lists signed modules. It can run independently, or beside a firm's stack under the optional local marketplace Compose profile. The marketplace lists, installs and checks package signatures. It never sells, charges, subscribes, renews, refunds or issues a license — selling and licensing belong to each module's provider. The operator does not review, inspect, test or vouch for modules. Every listing stays private until Patrick releases a public marketplace.

Build and run (a module author's whole submission walk-through is in `docs/modules/MODULE-KIT.md`, Submitting to the marketplace):

```sh
docker build -f marketplace/Dockerfile -t togavsu-marketplace .
# or: go run ./marketplace/cmd/marketplace -config marketplace/config.example.json
```

Beside a firm stack, Compose runs this service under the `marketplace` profile (off by
default): `docker compose --profile marketplace up -d`. See `deploy/marketplace/README.md`.
The operator token is written once by bootstrap into the `marketsecrets` volume; the
committed `deploy/marketplace/config.json` is mounted read-only; state is in `marketdata`
at `/var/lib/togavsu-marketplace`; listen is internal-only on 8090 (no published port).

Configuration is a JSON file (`config.example.json` or `deploy/marketplace/config.json`);
omitted fields keep the defaults in `fixed.json`. The operator token is read from
`operator_token_file` (a Docker secret or the `marketsecrets` volume, at least 32 bytes),
never from the config file or the environment. State lives in `data_dir`: `state.json`
plus one file per package, named by its SHA-256.

There is no Stripe, license seed, SMTP or platform-fee setting. There is no core migration; the marketplace keeps its own store (`store.go`).

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
| POST | `/v1/authors` | `{name, email, password}`; the password has at least 12 characters, and name and email are each unique. |
| POST | `/v1/sessions` | `{email, password}` → `{token}` |
| GET/POST | `/v1/authors/me/keys` | Register `{public_key_pem}`; the response has a `challenge` and the exact `proof_message`. |
| POST | `/v1/authors/me/keys/{key_id}/verify` | `{signature}` of `proof_message` with the private key (`togavsu-module key -key <seed file> -prove <proof_message>`, or `modulekit.ProveKey`). Only verified keys sign uploads or are published. |
| POST | `/v1/authors/me/packages` | The kit's whole signed package file (`package_base64`, `signature`, `key_id`, `digest`, `listing_id`, `version`, `immutable`, and `manifest`) plus optional `price_note`. When `digest` is present it must match the package bytes (`digest_mismatch` otherwise). Unknown fields are refused. The old three-field body still works. The manifest's module id names the listing; the first author owns it; a listed version never changes. The first listing takes the manifest's string or structured `price`; `price_note` is a fallback only when the manifest has none. Later uploads preserve the author's listed price; the price endpoint changes it without replacing a package. Prefer `togavsu-module publish`. |
| PATCH | `/v1/authors/me/listings/{id}/price` | Change listed provider price and/or `sales_url` without a new package. |

Operator (bearer operator token): flag and pull only. No platform-fee settings.

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/operator/listings/{id}/flag` | `{note, version?}`. The note is required. Installs stay possible. |
| POST | `/v1/operator/listings/{id}/pull` | `{note?}`. Out of search; no marketplace installs. |

Pages: `/` (search), `/listings/{id}` (author, verified key, permissions, versions, provider price and warnings), `/operator` (CSRF-protected flag and pull forms).

The firm's `internal/marketplace` client browses, verifies installs, manages trusted local keys and alerts admins to flags and pulls. A pull never disables an existing install or blocks local-file verification.

There is no checkout, subscription, renewal, refund, payment-service, platform-fee or license-key route.

## Tests

```sh
go test -count=1 ./marketplace/
```

Test helpers refuse any outside host (`RequireLocalBaseURL` in the test package, called from `env.do` / `env.doURL`). Outside payment providers are never dialed.
