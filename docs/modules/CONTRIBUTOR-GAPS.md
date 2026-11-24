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
| No writer for `pending.yaml`, so module tests hand-write the YAML | review of PR 57 | fixed: `WritePending(entries)`, `AddPending(src, entry)` (in place, comments kept) and `togavsu-module pending`; each reads its result back with `ParsePending` | none |
| No way to keep a pending part in a build on purpose, to prove the live core still refuses it at approval | review of PR 57 | open | kit follow-up: an `Installable` option that keeps named parts, for acceptance tests that sign with `SignFile` |
| `coretest` has one client login and no stand-ins for mail, the rendition service, the converter or Doc Tender, so modules that need them build their own fakes | client-portal, documents, email, lawpay-docuseal | fixed in core-update-2a for mail (CORE-29) and Doc Tender markdown renditions (`SeedMarkdownRendition` / `OpenMarkdownRendition`); converter fixed in core-update-2d: `c.Converter`, `SetResult`, `Fail`, `Calls`; Doc Tender jobs/OCR/document-reader stand-ins fixed in core-update-2f: `c.DocTender` and `RunDocTender`, `TestCoreUpdate2FDocTenderCategoryHint` | CORE-36 |
| Marketplace submission by an outside author has not been exercised end to end | kit review | fixed: documents went from `package -installable` with a throwaway key to a listed module on a local marketplace and was installed from that listing by a `coretest` core, using only the kit and docs; MODULE-KIT.md, Submitting to the marketplace, is that walk-through, run as written. The rows below are what it found | none |
| MODULE-KIT.md said nothing about submitting, and called `BuildPackage`'s plain tar what the marketplace reads | documents dry run | fixed: Submitting to the marketplace; the marketplace takes the same signed `Archive` bytes | none |
| The docs never said how to make a key file | documents dry run | fixed: `openssl rand -base64 32 > signing.key` (any 32 random bytes) | none |
| The public key PEM came only inside `package`'s output, though `verify -pub` and the marketplace's key registration take it alone | documents dry run | fixed: `togavsu-module key -key signing.key > signing.pub` | none |
| Nothing in the kit could sign the marketplace's key `proof_message`, so an author couldn't verify a key without writing Ed25519 code | documents dry run | fixed: `ProveKey(priv, proofMessage)` and `togavsu-module key -prove`, which sign only that message for that key | none |
| The upload refuses the kit's signed package file as its body (`listing_id`, `digest` and the rest are unknown fields) | documents dry run | fixed: upload takes the kit's whole signed package file; `togavsu-module publish` | none |
| The author password minimum (12) was undocumented | documents dry run | fixed: `marketplace/README.md` | none |
| The listing ignores the manifest's `price` (documents says `Free`, the listing showed no price) and reads only the upload's `price_note` | documents dry run | fixed: CORE-35 and marketplace-paid keep the manifest's string or structured price as provider information; the legacy price_note is a fallback only, and Togavsu never charges | none |
| There is no public marketplace to submit to: `modules.marketplace_address` defaults to the placeholder `togavsu-marketplace` | documents dry run | partly fixed: Compose profile `marketplace` runs `togavsu-marketplace` on the firm network (`deploy/marketplace/`); Lane A sets the core address and catalog. Still no public internet marketplace | a public marketplace address when one is operated |
| `check -installable` printed held-back parts in the order the passes found them (client-portal: `tabs[3]`, `[4]`, `[5]`, `[2]`, `[6]`) | client-portal | fixed: `Held` is in manifest order (manifest parts by the line they start on, then migrations by file name) | none |
| `Pending.Core` became `CoreItems` with no way to ask for one item, so modules compared joined strings | all five | fixed: `CoreItems.Has(item)` | none |
| The upgrade note didn't cover the pending kit's signature changes (`Installable` and `BuildInstallable` return an `InstallablePackage`; `Pending.Core` is `CoreItems`) | all five | fixed: MODULE-KIT.md, Upgrading | none |

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
| Background modules cannot read deadlines/calendar, contact matters, addresses, firm zone, roles, custom-field definitions or directory/work email | batch 2 | fixed under existing approved read scopes | `TestCoreUpdate2DReadsWallsMailSettingsAndPhone` |
| Background access and directory filters need consistent core wall enforcement | batch 2 | fixed: service principals remain outside every wall; inside-wall viewer context required; no membership metadata; directory checks caller before candidates | Same test; `TestCoreUpdate2DServiceWallsEveryRecord` |
| A background module cannot mail contact parties on its named matter | batch 2, CORE-04 | fixed under mail:send | Public-kit read/mail/settings/phone test |
| A setup module cannot save its own settings or read applicable contract limits without firm-admin module scope | batch 2, CORE-20/31/36 | fixed: module_settings:write with admin viewer, allowlisted settings reads | Same public-kit test |
| Provider cannot delegate forwarded consumer context; event push broadcasts/rewrites; oversize response has wrong code | batch 2, CORE-31 | fixed, subject to comparing separately held contract definitions absent from the base | `TestCoreUpdate2DContractForwardingTargetAndLimit` |
| Core webhook timestamp check is missing, accepts empty key, and public hook strips provider Authorization with no opt-in | batch 2, CORE-07/36 | fixed: v1.0.0 timestamp-authenticated signature, dual-format kit verification and five-minute window and approved per-hook opt-in; public CRC/HMAC helpers | Webhook public-kit and helper tests |
| Module timeline events do not appear in shared practice feed or name module actors | batch 2 | fixed: shared stream; delegated user actor plus module attribution | Public-kit read/mail/settings/phone test |
| Manifest/migration inventory tokenizes Goose Down; runtime refusals cannot honestly be held back | batch 2, CORE-36 | fixed: Up inventory and runtime pending criterion/strict retirement | `TestRuntimePendingCriteriaAndGooseUpInventory` |
| Module conversion tests must alter the host converter | batch 2, CORE-29/36 | fixed: coretest in-process converter stand-in | `TestCoreUpdate2DPurgeRestrictAndConverter` |
| No click-to-call phone-row slot | phone | fixed: contact_phone_items with approved contacts:read and viewer frames | Public-kit read/mail/settings/phone test |


## Batch 2 documents and intake gaps closed by core-update-2f

| Gap | Found by | Status | Evidence |
| --- | --- | --- | --- |
| Modules approval tab omits manifest permission reasons | documents MOD-DOC-01; intake MOD-ITK-01 | fixed: signed declared reason returned and escaped beside every decision, including candidate updates | `TestCoreUpdate2FPermissionReasons` |
| CSRF parsing consumes urlencoded /m/ form body and proxy returns 502 | intake client autosave | fixed: exact bytes restored, original parser ceiling and session CSRF enforced | `TestCoreUpdate2FProxyRestoresFormAfterCSRF` |
| Trashing a core file emits no subscribed file.trashed delivery | intake cleanup | fixed: production event/queue, approved derived read scopes and sender core | `TestCoreUpdate2FFileTrashDelivery` |
| Availability cannot return occupancy across matter walls | intake busy review | fixed: explicit view=busy has only starts_at/ends_at/all_day; detail reads retain not_found | `TestCoreUpdate2FBusyAcrossWalls` |
| Additional named people cannot be checked or registered by a service | intake MOD-ITK-14 | fixed: names input retained/checked; createConflictRecord service author is module, source service | `TestCoreUpdate2FNamedConflictService`, `TestCoreUpdate2FAdditionalConflictNames` |
| County get refuses an approved settings:read service | documents | fixed: scoped getCounty service read with live operation limit | `TestCoreUpdate2FCountyService` |
| Background draft cannot publish status/human author or update its status | documents contract publication | fixed: status and validated optional on_behalf_of_user_id; own-module scoped status write; wall/role checks stay independent | `TestCoreUpdate2FPublicationAuthorAndStatus` |
| document.generated is rejected by the generic contract namespace rule | documents contract generation | fixed: exact named event allowed for documents.generate.v1 in the existing single targeted envelope; sender binding unchanged | `TestCoreUpdate2FDocumentGeneratedContractEnvelope` |
| Modules cannot run real Doc Tender jobs without host services | documents MOD-DOC-22 | fixed: deterministic coretest adapters run real queue/pipeline, preserve category locks and persist failures | `TestCoreUpdate2FDocTenderCategoryHint`, `TestCoreUpdate2FDocTenderFailureIsRealQueueFailure` |
| Intake card has no binding pre-matter payment approval/recovery contract | intake MOD-ITK-05 | fixed core side: processor.payment.v1 durable bounded authorization/recovery; processor module remains external | `TestCoreUpdate2FProcessorDeclineBeforeMatterAndApprovalRecovery`, `TestCoreUpdate2FProcessorConcurrentAndInvalidRequests` |

### Core-update-2f fix pass 1

| Review gap | Result | Regression evidence |
| --- | --- | --- |
| Core source labels collide with module status ownership | First-version upload owner is authoritative; only core-exclusive IDs are reserved. Approved IDs such as email, zoom and importer remain valid (corrected in fix pass 2) | `TestCoreUpdate2FFixStatusOwnership`, `TestCoreUpdate2FFixReservedSources`, `TestCoreUpdate2FFixPublishReservedSources` |
| Named author leaks wall membership before caller access | Caller placement checked first; inaccessible targets have identical `not_found` responses for all authors | `TestCoreUpdate2FFixPublicationCallerFirst` |
| Author ignores contact/attorney-only placement | Active firm author independently checks actual placement; all author failures share one validation response | `TestCoreUpdate2FFixPublicationAuthorPlacement` |
| File events disclose hidden file IDs | Ordinary file events use module service read visibility at enqueue; targeted grants/books behavior retained | `TestCoreUpdate2FFixFileEventVisibility`, existing books pump regression |
| Status ownership lookup distinguishes absent IDs | Missing and other-owner files share `scope_denied` | `TestCoreUpdate2FFixStatusExistenceOracle` |
| Additional conflict names unbounded | Live Practice settings default to 20 names / 200 Unicode characters; over-limit input rejected before scoring/storage | `TestCoreUpdate2FFixConflictNameLimits` |
| Processor audit IDs cannot be traced to intent | Stable consumer plus intent entity ID; no new sensitive payload | `TestCoreUpdate2FFixProcessorAuditIdentity` |


### Core-update-2f fix pass 2

| Review gap | Result | Regression evidence |
| --- | --- | --- |
| Reservation blocks approved module IDs | Approved batch 1/batch 2 IDs, matter-clock, zoom and importer validate, install and authenticate; email contract links and marketplace publishing work. Core-only labels remain refused; source alone still grants no ownership | `TestCoreUpdate2FFix2ApprovedModuleIDs`, `TestCoreUpdate2FFix2PublishApprovedSources`, retained reserved-source/ownership tests |
| Purged files lose every pending event | Enqueue checks the retained row’s placement, ignoring only purge. Visible file.purged and older pending events deliver once; walled/attorney-only files stay hidden; normal file reads stay not_found | `TestCoreUpdate2FFix2PurgedFileEvents`, `TestCoreUpdate2FFix2FileEventPolicyFailsClosed` |
| Client contact writes after the matter window | Existing target-base behavior: own shared contact files remain readable, but clients already cannot add contact-only files, directly or through a module. No policy change | `TestCoreUpdate2FFix2ClientOwnContactAfterAccessWindow`, also run against target base 4c215a3 |

### Core-update-2f contract and subscriber documentation

The reserved module IDs are exactly `core`, `upload`, `client_upload`, `editor`,
`billing`, `drop_folder`, `ui`, `api_token`, `module`, `service`, `system`, `api`,
`manual`, `timer`, `import`. Approved IDs `email`, `zoom` and `importer` remain
valid. The regenerated `ModuleManifest.id` description matches the runtime list;
`TestModuleManifestReservedIDsContract` enumerates `ReservedModuleID` switch cases
and compares them with the published list, including underscore labels.

The file visibility guard applies to every non-targeted `file.*` event identifying
a file, including `file.updated`, `file.downloaded`, `file.trashed` and
`file.purged`. It requires approved `events:read`, a matching subscription,
approved `files:read` and service-principal read access to the current placement
at enqueue. Matter walls, attorney-only restrictions and non-matter placement
checks apply; purge ignores only the retained row's purge marker. Targeted grants
and books-reader filters retain their existing behavior.

**Behavior change for existing subscribers:** without approved `files:read`, or
for walled, attorney-only or non-matter files their service principal cannot read,
subscribers stop receiving `file.updated` and `file.downloaded`. Delegated viewer
access does not grant background event access. The [batch 1 inventory](MODULE-KIT.md#batch-1-file-event-subscriptions)
lists all five module specs' subscriptions and read scopes; real-package
compatibility still needs its separate acceptance run. Existing regression
evidence remains `TestCoreUpdate2FFixFileEventVisibility` and
`TestCoreUpdate2FFix2PurgedFileEvents`.


## Batch 2 remaining gaps closed by core-update-2g

| Module gap / failing acceptance | Status | Core evidence |
| --- | --- | --- |
| Binding event envelope returns 400 (`TestDocumentsGenerateV1Contract`, `TestContractGeneratedEnvelopeCoretest`) | fixed: binding contract/event/event_id/occurred_at/data fanout, original envelope and provider sender, durable recipient dedupe; targeted 2f shape retained | `TestCoreUpdate2GBindingEnvelope`, existing 2d/2f contract and 2e sender tests |
| Client frame has no session CSRF for its rendered POST (`TestReviewClientPageAutosave`) | fixed: module-bound session HMAC header on authenticated GET, hidden `csrf`, no-store; strict POST check retained | `TestCoreUpdate2GFrameSessionCSRF`, existing 2f body-restoration test |
| Public processor approval cannot record its charge without a viewer | fixed: once-bound receipt `record` continuation under current approved consumer scopes/link, no provider call; contact or matter target, atomic payment/audit/events and replay | `TestCoreUpdate2GProcessorRecording`, `TestCoreUpdate2GProcessorContactAndRefusals` |

These close the remaining behaviors beyond 2f's original rows; external module
suites still need to run against this core revision. No external module pass is
claimed by the core regression tests.


## CORE-30 intake gap closed by core-update-2h

| Module gap / failing acceptance | Status | Core evidence |
| --- | --- | --- |
| CORE-30 firm occupancy for service callers (`TestReviewBusyAcrossWalls`) | closed: approved `calendar:read` occupancy also returns start/end only for walled events with no assigned attendee user; assigned walled events stay out and all other reads retain walls | `TestCoreUpdate2HUnassignedOccupancy`, `TestCoreUpdate2HOccupancyHeaders`, updated `TestCoreUpdate2GFirmOccupancy` and `TestCoreUpdate2GOccupancyMemoryWindow` |
