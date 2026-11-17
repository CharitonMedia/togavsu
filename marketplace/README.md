# Togavsu marketplace service

PRD section 10, plus batch 2 paid listings (CORE-CHANGES-2 §17). A small Go service, deployed separately from any firm's stack, that lists signed modules. The operator does not review, inspect, test or vouch for modules: signing proves who published a module, not that it works. Every listing stays private until Patrick releases a public marketplace.

Build and run (a module author's whole submission walk-through is in `docs/modules/MODULE-KIT.md`, Submitting to the marketplace):

```sh
docker build -f marketplace/Dockerfile -t togavsu-marketplace .
# or: go run ./marketplace/cmd/marketplace -config marketplace/config.example.json
```

Configuration is a JSON file (`config.example.json`); omitted fields keep the defaults in `fixed.json`. The operator token is read from `operator_token_file` (a Docker secret, at least 32 bytes), never from the config file or the environment. Stripe secrets and the license seed are likewise read from files at runtime and never written into `state.json`. State lives in `data_dir`: `state.json` plus one file per package, named by its SHA-256. There is no core migration; the marketplace keeps its own store (`store.go`).

## Signing

Packages are tar archives (gzip optional) with `togavsu-module.yaml` at the root, signed with Ed25519 over the exact package bytes. The signature is standard base64. A key is named by its key id, `ed25519-` plus the first 16 bytes of the SHA-256 of the raw public key, as hex. `internal/marketplace` holds the shared code (`Sign`, `Verify`, `KeyID`, `ReadManifest`, `BuildPackage`) that the service and the core both use.

The manifest is read with the module runtime's YAML subset parser (`internal/modules/yamlsubset`), so block YAML and YAML's JSON-compatible flow form both work. The listing uses `id`, `name`, `version` and `permissions` (scope strings, or objects with a `scope`). The listing price is taken from the manifest's `price` field at first upload: either the old string or CORE-35's structured `{amount_cents, currency, interval, note}` (`interval` is `month` or `year`).

## License key encoding (CORE-CHANGES-2 §15.5 / §17.3)

`license.go` defines the exact key format. **core-update-2c** and **mo-estate-planning** must check the same bytes with `CheckLicense` (or a byte-identical copy).

A license key is one string:

```text
togavsu-license-v1.<payload_b64url>.<sig_b64url>
```

`payload_b64url` is the unpadded Raw URL-safe base64 encoding of this exact UTF-8 statement (LF endings, trailing newline after the last field):

```text
togavsu-license-v1
install_id=<install id>
module=<module id>
paid_through=<YYYY-MM-DD>
```

`sig_b64url` is the unpadded Raw URL-safe base64 encoding of the Ed25519 signature of those exact statement bytes, made with the marketplace's license signing key.

The marketplace public key is a PKIX `PUBLIC KEY` PEM block holding Ed25519 (same form as package signing keys). `GET /v1/license-public-key` returns it. `CheckLicense(key, pub, installID, moduleID, now)` verifies the signature, requires matching install and module, and accepts the key when `now`'s UTC calendar date is on or before `paid_through`. Grace periods belong to the module, not this check.

Daily renewal: `POST /v1/licenses/renew` with `{install_id, module_id, signature}` where `signature` is the install's Ed25519 signature (standard base64) of:

```text
togavsu-license-renew-v1
install_id=<install id>
module=<module id>
```

That endpoint only re-serves the current key. `paid_through` moves forward only when Stripe sends a signed `invoice.paid` webhook (`POST /v1/stripe/webhook`). A client `extend` flag is refused.

Cancel: `POST /v1/subscriptions/cancel` with the same fields plus `signature` of:

```text
togavsu-license-cancel-v1
install_id=<install id>
module=<module id>
```

Checkout completion (`POST /v1/stripe/checkout/complete`) confirms payment by fetching the Checkout Session from Stripe and requiring `status=complete` and `payment_status=paid`. The Stripe subscription id is taken from that session, never from the caller.

The license signing seed (`license_seed_file`) is required whenever Stripe is configured. `Open` never mints a random key; without a seed, paid and license endpoints fail closed so modules that embed the marketplace public key keep verifying across restarts.

Price-change notices are queued in state with delivery status (`pending` / `sent` / `failed`) and delivered through `smtp_addr` (or a test `RecordingMailer`). A price change that would notice active subscribers is refused when no mailer is configured. Once saved, the author API returns success with per-notice delivery status even if SMTP fails. Failed deliveries stay queued for `RetryPendingMail` (periodic loop in `cmd/marketplace`, and `POST /v1/operator/mail/retry`) and do not restart the notice period. A pending price change does not update Stripe until its notice mail is `sent` and the notice period has ended. `paid_through` advances from Stripe `invoice.paid` line-item `period.end` (not the invoice top-level `period_end`, which is the previous usage period), deduped by event and invoice id.

## API

Read API (the core; no credentials):

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/listings?q=` | Search. Pulled listings never appear. Includes `price` when set. |
| GET | `/v1/listings/{id}` | One listing, pulled or not, with its flag and `price`. |
| GET | `/v1/listings/{id}/versions/{version}/package` | Signed package (`package_base64`, `digest`, `signature`, `key_id`, `manifest`). 410 when pulled, 404 when missing. Never substitutes another version. |
| GET | `/v1/alerts?module_id=&module_id=&since=` | Flags and pulls, oldest first, each with an `id` the core uses to deliver once. |
| GET | `/v1/signing-keys` | Every verified author key. |
| GET | `/v1/license-public-key` | Marketplace license public key PEM. |

Authors (bearer token from `/v1/sessions`):

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/authors` | `{name, email, password}`; the password has at least 12 characters, and name and email are each unique. |
| POST | `/v1/sessions` | `{email, password}` → `{token}` |
| GET/POST | `/v1/authors/me/keys` | Register `{public_key_pem}`; the response has a `challenge` and the exact `proof_message`. |
| POST | `/v1/authors/me/keys/{key_id}/verify` | `{signature}` of `proof_message` with the private key (`togavsu-module key -key <seed file> -prove <proof_message>`, or `modulekit.ProveKey`). Only verified keys sign uploads or are published. |
| POST | `/v1/authors/me/packages` | The kit's signed package file as the body, unchanged (`listing_id`, `version`, `immutable`, `manifest`, `digest`, `signature`, `key_id`, `package_base64`). A digest or signature that does not match the bytes is refused. The three-field body still works for older callers. Price comes from the manifest on first listing. |
| POST | `/v1/authors/me/stripe/connect` | Creates a Stripe Connect account for the author; stores only `acct_…`, never a secret. |
| PATCH | `/v1/authors/me/listings/{id}/price` | Author price change (`amount_cents`, `currency`, `interval`, `note`). New subscriptions use it at once; existing ones get email and apply it after the notice period once the notice is delivered. Response includes `notices` with delivery status. |

Paid subscriptions (fake Stripe in every test; helpers refuse non-local Stripe hosts):

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/listings/{id}/checkout` | `{install_id, admin_email, install_public_key_pem, success_url, cancel_url}` → Checkout session naming the install id. `success_url` must include `{CHECKOUT_SESSION_ID}`. Paid listings only; pulled and free listings are refused. |
| POST | `/v1/stripe/checkout/complete` | `{session_id}` only. Confirms with Stripe (`complete` + `paid`), then issues the license key once. |
| POST | `/v1/stripe/webhook` | Signed Stripe events. `invoice.paid` sets `paid_through` from the subscription line’s `period.end` (deduped). Due price changes update Stripe when the notice was delivered and the notice period ends, before renewal billing. |
| POST | `/v1/licenses/renew` | Daily key fetch with install signature. Never extends `paid_through`; `extend: true` is refused. |
| POST | `/v1/subscriptions/cancel` | `{install_id, module_id, signature}` — install-signed cancel proof. Stops renewal; key runs to paid-through. |

Operator (bearer operator token):

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/operator/listings/{id}/flag` | `{note, version?}`. The note is required. Installs stay possible. |
| POST | `/v1/operator/listings/{id}/pull` | `{note?}`. Out of search; no marketplace installs. |
| GET/PATCH | `/v1/operator/settings` | `platform_fee_percent` (default 0). |
| POST | `/v1/operator/mail/retry` | Redeliver queued price-change notices (`pending` / `failed`). The service also retries on `mail_retry_interval_seconds`. |

Pages: `/` (search), `/listings/{id}` (author, verified key, permissions, version history, price, the notice and any warning), `/operator` (sign in with the operator token, then flag and pull with CSRF-protected forms).

## The core side

`internal/marketplace` is the firm's client: browse (`/api/v1/marketplace/listings`), verified installs from the marketplace (`FetchForInstall`, refused for a pulled module), local-file verification (`VerifyLocal`, `VerifyLocalFile`, never blocked by a pull), trusted local keys (`/api/v1/trusted-signing-keys`), and flag/pull alerts for installed modules (`Check`, `RunDue`, `Start`, `OpenModulesScreen`, `/api/v1/module-alerts`, and the `module-alerts` widget through `WidgetSource`). Alerts notify and email every active admin, by their notification preferences, carrying the warning and the operator's note. Nothing is switched off remotely.

Taking an issued license key into a module's secret settings is **core-update-2c**, not this service.
