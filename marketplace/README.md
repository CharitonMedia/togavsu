# Togavsu marketplace service

PRD section 10. A small Go service, deployed separately from any firm's stack, that lists signed modules. The operator does not review, inspect, test or vouch for modules: signing proves who published a module, not that it works.

Build and run:

```sh
docker build -f marketplace/Dockerfile -t togavsu-marketplace .
# or: go run ./marketplace/cmd/marketplace -config marketplace/config.example.json
```

Configuration is a JSON file (`config.example.json`); omitted fields keep the defaults in `fixed.json`. The operator token is read from `operator_token_file` (a Docker secret, at least 32 bytes), never from the config file or the environment. State lives in `data_dir`: `state.json` plus one file per package, named by its SHA-256.

## Signing

Packages are tar archives (gzip optional) with `togavsu-module.yaml` at the root, signed with Ed25519 over the exact package bytes. The signature is standard base64. A key is named by its key id, `ed25519-` plus the first 16 bytes of the SHA-256 of the raw public key, as hex. `internal/marketplace` holds the shared code (`Sign`, `Verify`, `KeyID`, `ReadManifest`, `BuildPackage`) that the service and the core both use.

The manifest is read with `encoding/json`, so it must be written in YAML's JSON-compatible flow form until the module runtime's YAML parser ships (see PONYTAIL-DEBT.md). The listing uses `id`, `name`, `version` and `permissions` (scope strings, or objects with a `scope`).

## API

Read API (the core; no credentials):

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/listings?q=` | Search. Pulled listings never appear. |
| GET | `/v1/listings/{id}` | One listing, pulled or not, with its flag. |
| GET | `/v1/listings/{id}/versions/{version}/package` | Signed package (`package_base64`, `digest`, `signature`, `key_id`, `manifest`). 410 when pulled, 404 when missing. Never substitutes another version. |
| GET | `/v1/alerts?module_id=&module_id=&since=` | Flags and pulls, oldest first, each with an `id` the core uses to deliver once. |
| GET | `/v1/signing-keys` | Every verified author key. |

Authors (bearer token from `/v1/sessions`):

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/authors` | `{name, email, password}` |
| POST | `/v1/sessions` | `{email, password}` → `{token}` |
| GET/POST | `/v1/authors/me/keys` | Register `{public_key_pem}`; the response has a `challenge` and the exact `proof_message`. |
| POST | `/v1/authors/me/keys/{key_id}/verify` | `{signature}` of `proof_message` with the private key. Only verified keys sign uploads or are published. |
| POST | `/v1/authors/me/packages` | `{package_base64, signature, key_id, price_note?}`. The manifest's module id names the listing; the first author owns it; a listed version never changes. |

Operator (bearer operator token):

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/v1/operator/listings/{id}/flag` | `{note, version?}`. The note is required. Installs stay possible. |
| POST | `/v1/operator/listings/{id}/pull` | `{note?}`. Out of search; no marketplace installs. |

Pages: `/` (search), `/listings/{id}` (author, verified key, permissions, version history, price, the notice and any warning), `/operator` (sign in with the operator token, then flag and pull with CSRF-protected forms).

## The core side

`internal/marketplace` is the firm's client: browse (`/api/v1/marketplace/listings`), verified installs from the marketplace (`FetchForInstall`, refused for a pulled module), local-file verification (`VerifyLocal`, `VerifyLocalFile`, never blocked by a pull), trusted local keys (`/api/v1/trusted-signing-keys`), and flag/pull alerts for installed modules (`Check`, `RunDue`, `Start`, `OpenModulesScreen`, `/api/v1/module-alerts`, and the `module-alerts` widget through `WidgetSource`). Alerts notify and email every active admin, by their notification preferences, carrying the warning and the operator's note. Nothing is switched off remotely.
