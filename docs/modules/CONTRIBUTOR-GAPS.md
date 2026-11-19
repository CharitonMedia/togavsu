# Contributor gaps

Every gap an outside module author hits when building a Togavsu marketplace module with
only the public kit (`pkg/modulekit`, `pkg/modulekit/coretest`, `cmd/togavsu-module`,
`MODULE-KIT.md`). The five first-party modules (documents, email, client-portal,
lawpay-docuseal, quickbooks) are the test of that path; add a row whenever a module, ours
or anyone's, works around something here. A gap is closed when an author no longer needs
a workaround in their own repository.

Status is `fixed` (with where) or `open`. "Follow-up" names the CORE item in
`CORE-CHANGES.md` or the change that closes it.

## Kit

| Gap | Found by | Status | Follow-up |
| --- | --- | --- | --- |
| No way to hold back the parts that wait on core changes: no module could build a signed, installable package against the pinned core, and each kept its own hold-back list | quickbooks (`internal/pendingcore`), all five | fixed in PR 57: `pending.yaml`, `Installable`, `BuildInstallable`, `package -installable` | none |
| `Check` stopped at the first refusal and returned only error text, so modules regex-matched messages | all five | fixed in PR 57: `Refusals` with kind, file, path, statement and stable codes; `CheckAll` | none |
| `Check` ignored scopes; an unknown scope surfaced only when the admin approved the install | quickbooks, email, documents, client-portal, lawpay-docuseal | fixed in PR 57: `Check` refuses scopes not in `Scopes()` | none |
| `Build` signed a package without checking it | quickbooks (wrapped `Build` in its own check) | fixed in PR 57: `Build` checks and refuses | none |
| No way to ask what a core accepts | all five | partly fixed in PR 57: `Accepts()` and `togavsu-module accepts` give the manifest fields and their digest, scopes and linkable core records | the rest is the next row |
| The kit can't report which CORE items a core has built | all five | fixed in core-update-2a: `modules.Accepts().core_items` from `internal/modules/core_items.txt`, tested against CORE-CHANGES.md / CORE-CHANGES-2.md headings | CORE-36 |
| The update check compared against the previous release's source, re-run through today's core, so a migration it held back counted as shipped | review of PR 57 | fixed in PR 57: `CheckUpdate` and `check -previous-file` read the shipped signed package | none |
| `check -json` printed nothing when the pending list or a hold-back failed | review of PR 57 | fixed in PR 57: every failure is the `{ok, refusals}` result | none |
| `package -dir` shipped every file in the package directory, such as the `files.go` that embeds it | email (`package/files.go`) | fixed in PR 57: `ReadDir` and `Files` read only the manifest, `pending.yaml` and `migrations/`; the core refuses any other file at install (`unknown_file`), and so do `Check` and `Build` | none |
| `Installable` returned no pending list or unused entries, so callers parsed `pending.yaml` again | documents | fixed in PR 57: `Installable` and `BuildInstallable` return an `InstallablePackage` with `Pending` and `Unused` | none |
| Nothing failed CI when a pending entry held nothing back after its core item landed | documents | fixed in PR 57: `-strict` on `check -installable` and `package -installable` (`pending_unused`); `held` and `unused` in `check -json` | none |
| A refusal no pending entry covered read like any other refusal | review of PR 57 | fixed in PR 57: its text ends `(not covered by pending.yaml)`, with the reason when an entry names the place but not that kind of refusal | none |
| `go:embed` of the package directory also embeds `pending.yaml` and the `.go` file, so code listing the embedded files ships them | documents | documented in PR 57: MODULE-KIT.md, What a package holds (read through `Files`, ship through `Installable`) | none |
| Pending field paths shifted as earlier items were held back, so holding `tabs[2]` to `tabs[6]` meant "each held in turn as the one before it goes" | client-portal | fixed in PR 57: paths name the manifest as written, and `[key=value]` selects items (`tabs[placement=client]`); client-portal's `tabs[2]` entry now holds only the third tab and needs that selector | none |
| `CheckAll` reported only the first refused manifest value, so authors fixed one per run | review of PR 57 | fixed in PR 57: every refused value (`ValidateAll`), and `Installable` repeats the check until nothing covered is left; the widget checks still report their first, and a value of the wrong type stops the manifest checks (documented) | none |
| A held part's own comment lines stayed in the manifest | review of PR 57 | fixed in PR 57: comment lines directly above a held part, at its indent, go with it | none |
| No way to see what `-installable` would ship without signing it | review of PR 57 | fixed in PR 57: `package -installable -unsigned -out DIR` | none |
| `Build` now checks before signing and refuses a package with `pending.yaml`; at core `3d9453e` it signed unchecked | review of PR 57 | documented in PR 57: MODULE-KIT.md upgrade note | none |
| A pending entry's `core` took one item, though some parts wait on two | lawpay-docuseal (`0005_payment_refund_link.sql`, CORE-22 and CORE-24) | fixed in PR 57: `core` is one item or a list | none |
| A held migration came back as soon as the relation it references was allowed by name, whether or not the core's schema had it | review of PR 57 | fixed in PR 57: the reference check also needs the relation in the core's schema (`core_relation_missing`), from `internal/modules/core_tables.txt`, which a test keeps equal to the core's migrations | none |
| A pending entry covered every refusal in its place, so a migration entry hid any other problem in its file | review of PR 57 | fixed in PR 57: each entry kind covers only its refusal kinds, and a held migration is checked as if the core relations it references existed | none |
| While a foreign key to a core table is held back, nothing checks the ids the module stores, so the later `ADD CONSTRAINT` can fail | review of PR 57 | documented in PR 57: MODULE-KIT.md (validate ids through the API until the constraint lands) | none |
| `Installable` looped forever when a field entry held back a value the core requires (`tabs[1].placement`), since removing it left the same refusal | Codex review of PR 57 | fixed in PR 57: a refusal of a part already held back stops with `pending_edit`, each pass must change the files, and the passes are bounded | none |
| No writer for `pending.yaml`, so module tests hand-write the YAML | review of PR 57 | fixed in this PR: `WritePending(entries)`, `AddPending(src, entry)` (in place, comments kept) and `togavsu-module pending`; each reads its result back with `ParsePending` | none |
| No way to keep a pending part in a build on purpose, to prove the live core still refuses it at approval | review of PR 57 | open | kit follow-up: an `Installable` option that keeps named parts, for acceptance tests that sign with `SignFile` |
| `coretest` has one client login and no stand-ins for mail, the rendition service, the converter or Doc Tender, so modules that need them build their own fakes | client-portal, documents, email, lawpay-docuseal | fixed in core-update-2a for mail (CORE-29) and Doc Tender markdown renditions (`SeedMarkdownRendition` / `OpenMarkdownRendition`); converter fixed in core-update-2d (this PR): `c.Converter`, `SetResult`, `Fail`, `Calls`; OCR stand-ins remain open | CORE-36 |
| Marketplace submission by an outside author has not been exercised end to end | kit review | fixed in this PR: documents went from `package -installable` with a throwaway key to a listed module on a local marketplace and was installed from that listing by a `coretest` core, using only the kit and docs; MODULE-KIT.md, Submitting to the marketplace, is that walk-through, run as written. The rows below are what it found | none |
| MODULE-KIT.md said nothing about submitting, and called `BuildPackage`'s plain tar what the marketplace reads | documents dry run | fixed in this PR: Submitting to the marketplace; the marketplace takes the same signed `Archive` bytes | none |
| The docs never said how to make a key file | documents dry run | fixed in this PR: `openssl rand -base64 32 > signing.key` (any 32 random bytes) | none |
| The public key PEM came only inside `package`'s output, though `verify -pub` and the marketplace's key registration take it alone | documents dry run | fixed in this PR: `togavsu-module key -key signing.key > signing.pub` | none |
| Nothing in the kit could sign the marketplace's key `proof_message`, so an author couldn't verify a key without writing Ed25519 code | documents dry run | fixed in this PR: `ProveKey(priv, proofMessage)` and `togavsu-module key -prove`, which sign only that message for that key | none |
| The upload refuses the kit's signed package file as its body (`listing_id`, `digest` and the rest are unknown fields) | documents dry run | fixed in marketplace-paid: `POST /v1/authors/me/packages` takes the kit's signed package file unchanged; MODULE-KIT.md no longer needs the jq step | CORE-CHANGES-2 §17.2 |
| The author password minimum (12) was undocumented | documents dry run | fixed in this PR: `marketplace/README.md` | none |
| The listing ignores the manifest's `price` (documents says `Free`, the listing showed no price) and reads only the upload's `price_note` | documents dry run | fixed in marketplace-paid + CORE-35: listing takes the manifest's string or structured price; core stores and shows it on install and never charges it | CORE-35 / CORE-CHANGES-2 §17.1 |
| There is no public marketplace to submit to: `modules.marketplace_address` defaults to the placeholder `togavsu-marketplace` | documents dry run | open; documented in this PR: run one locally for a dry run | deploy the marketplace, set its address as `domain.MarketplaceAddress`, and name it in MODULE-KIT.md |
| `check -installable` printed held-back parts in the order the passes found them (client-portal: `tabs[3]`, `[4]`, `[5]`, `[2]`, `[6]`) | client-portal | fixed in this PR: `Held` is in manifest order (manifest parts by the line they start on, then migrations by file name) | none |
| `Pending.Core` became `CoreItems` with no way to ask for one item, so modules compared joined strings | all five | fixed in this PR: `CoreItems.Has(item)` | none |
| The upgrade note didn't cover the pending kit's signature changes (`Installable` and `BuildInstallable` return an `InstallablePackage`; `Pending.Core` is `CoreItems`) | all five | fixed in this PR: MODULE-KIT.md, Upgrading | none |

## Core behavior modules work around

| Gap | Found by | Status | Follow-up |
| --- | --- | --- | --- |
| The `/m/` proxy drops the `X-Togavsu-Context-Token` header, so modules pass the token another way | client-portal | fixed in core-update-2a: `/m/` proxy forwards inbound `X-Togavsu-Context-Token` | CORE-36 |
| `GET /api/v1/me` refuses a module token with a context token | client-portal, lawpay-docuseal | fixed in core-update-2a: `getMe` uses `principal()` so a context token answers as the viewer | CORE-36 |
| A missing scope answers 403 `forbidden`, not `scope_denied` | documents, email | fixed in core-update-2a: JSON keeps `scope_denied` and names the missing scope | CORE-36 |
| The core emits `conflict.decided`; the OpenAPI document says `conflict_check.decided` | lawpay-docuseal | fixed in core-update-2a: emits `conflict_check.decided` | CORE-36 |
| Webhook signature format differs between what the core sends and what the contract documents | documents, email, lawpay-docuseal, quickbooks | fixed in core-update-2d (this PR), superseding core-update-2a: v1.0.0 t=<unix>,v1=<hex> HMAC over timestamp + dot + body, five-minute window, dual-format kit verifier and empty-secret checks | CORE-36 |
| `POST /api/v1/notes` answers 201 but `core.notes` stays empty | email | fixed in core-update-2a: notes project into `core.notes` on write and on `EnableProjection` backfill for upgraded installs | CORE-36 |
| Billing records are stored as JSON, so `core.invoices`, `core.payments` and `core.expenses` stay empty and module foreign keys to them can't be exercised | quickbooks, lawpay-docuseal | fixed in core-update-2a: dual-write + `RebuildCoreRows` with cent reconciliation | CORE-36 |
| File purge can erase bytes while module RESTRICT links retain a file | documents | fixed in core-update-2d (this PR): purge names retaining modules and refuses until links are released; immutable file/version metadata stays | `TestCoreUpdate2DPurgeRestrictAndConverter` |
| CORE-01 lacks `getConflictCheck` and `createEvent` for service calls | lawpay-docuseal, documents, client-portal | fixed in core-update-2a for `getConflictCheck` (createEvent already landed with CORE-01) | CORE-36 |
| No timeline events written by a client (portal) principal | client-portal, lawpay-docuseal | open | core follow-up: let a client principal write a timeline event |
| A refused permission carries no reason the module can show | documents, quickbooks | open | core follow-up: permission reasons in the refusal |

## Batch 2 background-work gaps closed by core-update-2d (this PR)

| Gap | Found by | Status | Evidence |
| --- | --- | --- | --- |
| Background modules cannot read deadlines/calendar, contact matters, addresses, firm zone, roles, custom-field definitions or directory/work email | batch 2 | fixed in this PR under existing approved read scopes | `TestCoreUpdate2DReadsWallsMailSettingsAndPhone` |
| Background access and directory filters need consistent core wall enforcement | batch 2 | fixed in this PR: service principals remain outside every wall; inside-wall viewer context required; no membership metadata; directory checks caller before candidates | Same test; `TestCoreUpdate2DServiceWallsEveryRecord` |
| A background module cannot mail contact parties on its named matter | batch 2, CORE-04 | fixed in this PR under mail:send | Public-kit read/mail/settings/phone test |
| A setup module cannot save its own settings or read applicable contract limits without firm-admin module scope | batch 2, CORE-20/31/36 | fixed in this PR: module_settings:write with admin viewer, allowlisted settings reads | Same public-kit test |
| Provider cannot delegate forwarded consumer context; event push broadcasts/rewrites; oversize response has wrong code | batch 2, CORE-31 | fixed in this PR, subject to comparing separately held contract definitions absent from the base | `TestCoreUpdate2DContractForwardingTargetAndLimit` |
| Core webhook timestamp check is missing, accepts empty key, and public hook strips provider Authorization with no opt-in | batch 2, CORE-07/36 | fixed in this PR: v1.0.0 timestamp-authenticated signature, dual-format kit verification and five-minute window and approved per-hook opt-in; public CRC/HMAC helpers | Webhook public-kit and helper tests |
| Module timeline events do not appear in shared practice feed or name module actors | batch 2 | fixed in this PR: shared stream; delegated user actor plus module attribution | Public-kit read/mail/settings/phone test |
| Manifest/migration inventory tokenizes Goose Down; runtime refusals cannot honestly be held back | batch 2, CORE-36 | fixed in this PR: Up inventory and runtime pending criterion/strict retirement | `TestRuntimePendingCriteriaAndGooseUpInventory` |
| Module conversion tests must alter the host converter | batch 2, CORE-29/36 | fixed in this PR: coretest in-process converter stand-in | `TestCoreUpdate2DPurgeRestrictAndConverter` |
| No click-to-call phone-row slot | phone | fixed in this PR: contact_phone_items with approved contacts:read and viewer frames | Public-kit read/mail/settings/phone test |
