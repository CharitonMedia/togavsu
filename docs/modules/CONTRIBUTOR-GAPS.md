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
| No way to hold back the parts that wait on core changes: no module could build a signed, installable package against the pinned core, and each kept its own hold-back list | quickbooks (`internal/pendingcore`), all five | fixed in this PR: `pending.yaml`, `Installable`, `BuildInstallable`, `package -installable` | none |
| `Check` stopped at the first refusal and returned only error text, so modules regex-matched messages | all five | fixed in this PR: `Refusals` with kind, file, path, statement and stable codes; `CheckAll` | none |
| `Check` ignored scopes; an unknown scope surfaced only when the admin approved the install | quickbooks, email, documents, client-portal, lawpay-docuseal | fixed in this PR: `Check` refuses scopes not in `Scopes()` | none |
| `Build` signed a package without checking it | quickbooks (wrapped `Build` in its own check) | fixed in this PR: `Build` checks and refuses | none |
| No way to ask what a core accepts | all five | partly fixed in this PR: `Accepts()` and `togavsu-module accepts` give the manifest fields and their digest, scopes and linkable core records | open: the core does not record which CORE items it has built; add a built-items list the core keeps with each item |
| The update check compared against the previous release's source, re-run through today's core, so a migration it held back counted as shipped | review of this PR | fixed in this PR: `CheckUpdate` and `check -previous-file` read the shipped signed package | none |
| `check -json` printed nothing when the pending list or a hold-back failed | review of this PR | fixed in this PR: every failure is the `{ok, refusals}` result | none |
| `package -dir` shipped every file in the package directory, such as the `files.go` that embeds it | email (`package/files.go`) | fixed in this PR: `ReadDir` and `Files` read only the manifest, `pending.yaml` and `migrations/`; the core refuses any other file at install (`unknown_file`), and so do `Check` and `Build` | none |
| `Installable` returned no pending list or unused entries, so callers parsed `pending.yaml` again | documents | fixed in this PR: `Installable` and `BuildInstallable` return an `InstallablePackage` with `Pending` and `Unused` | none |
| Nothing failed CI when a pending entry held nothing back after its core item landed | documents | fixed in this PR: `-strict` on `check -installable` and `package -installable` (`pending_unused`); `held` and `unused` in `check -json` | none |
| A refusal no pending entry covered read like any other refusal | review of this PR | fixed in this PR: its text ends `(not covered by pending.yaml)`, with the reason when an entry names the place but not that kind of refusal | none |
| `go:embed` of the package directory also embeds `pending.yaml` and the `.go` file, so code listing the embedded files ships them | documents | documented in this PR: MODULE-KIT.md, What a package holds (read through `Files`, ship through `Installable`) | none |
| Pending field paths shifted as earlier items were held back, so holding `tabs[2]` to `tabs[6]` meant "each held in turn as the one before it goes" | client-portal | fixed in this PR: paths name the manifest as written, and `[key=value]` selects items (`tabs[placement=client]`); client-portal's `tabs[2]` entry now holds only the third tab and needs that selector | none |
| `CheckAll` reported only the first refused manifest value, so authors fixed one per run | review of this PR | fixed in this PR: every refused value (`ValidateAll`), and `Installable` repeats the check until nothing covered is left; the widget checks still report their first, and a value of the wrong type stops the manifest checks (documented) | none |
| A held part's own comment lines stayed in the manifest | review of this PR | fixed in this PR: comment lines directly above a held part, at its indent, go with it | none |
| No way to see what `-installable` would ship without signing it | review of this PR | fixed in this PR: `package -installable -unsigned -out DIR` | none |
| `Build` now checks before signing and refuses a package with `pending.yaml`; at core `3d9453e` it signed unchecked | review of this PR | documented in this PR: MODULE-KIT.md upgrade note | none |
| A pending entry's `core` took one item, though some parts wait on two | lawpay-docuseal (`0005_payment_refund_link.sql`, CORE-22 and CORE-24) | fixed in this PR: `core` is one item or a list | none |
| A held migration came back as soon as the relation it references was allowed by name, whether or not the core's schema had it | review of this PR | fixed in this PR: the reference check also needs the relation in the core's schema (`core_relation_missing`), from `internal/modules/core_tables.txt`, which a test keeps equal to the core's migrations | none |
| A pending entry covered every refusal in its place, so a migration entry hid any other problem in its file | review of this PR | fixed in this PR: each entry kind covers only its refusal kinds, and a held migration is checked as if the core relations it references existed | none |
| While a foreign key to a core table is held back, nothing checks the ids the module stores, so the later `ADD CONSTRAINT` can fail | review of this PR | documented in this PR: MODULE-KIT.md (validate ids through the API until the constraint lands) | none |
| `Installable` looped forever when a field entry held back a value the core requires (`tabs[1].placement`), since removing it left the same refusal | Codex review of this PR | fixed in this PR: a refusal of a part already held back stops with `pending_edit`, each pass must change the files, and the passes are bounded | none |
| No writer for `pending.yaml`, so module tests hand-write the YAML | review of this PR | open | kit follow-up: `modulekit.WritePending(entries)` and a command to add an entry |
| No way to keep a pending part in a build on purpose, to prove the live core still refuses it at approval | review of this PR | open | kit follow-up: an `Installable` option that keeps named parts, for acceptance tests that sign with `SignFile` |
| `coretest` has one client login and no stand-ins for mail, the rendition service, the converter or Doc Tender, so modules that need them build their own fakes | client-portal, documents, email, lawpay-docuseal | open | CORE-29 (acceptance harness core surface) |
| Marketplace submission by an outside author has not been exercised end to end | kit review | open | a dry run of listing submission and review from a repository outside the org |

## Core behavior modules work around

| Gap | Found by | Status | Follow-up |
| --- | --- | --- | --- |
| The `/m/` proxy drops the `X-Togavsu-Context-Token` header, so modules pass the token another way | client-portal | open | core follow-up: forward the header through the proxy |
| `GET /api/v1/me` refuses a module token with a context token | client-portal, lawpay-docuseal | open | core follow-up: answer it as the viewer (delegated_viewer) |
| A missing scope answers 403 `forbidden`, not `scope_denied` | documents, email | open | core follow-up: answer `scope_denied` naming the scope |
| The core emits `conflict.decided`; the OpenAPI document says `conflict_check.decided` | lawpay-docuseal | open | core follow-up: one event name in code and contract |
| Webhook signature format differs between what the core sends and what the contract documents | documents, email, lawpay-docuseal, quickbooks | open | core follow-up: align the signature header format with the contract |
| `POST /api/v1/notes` answers 201 but `core.notes` stays empty | email | open | core follow-up: write notes to `core.notes` |
| Billing records are stored as JSON, so `core.invoices`, `core.payments` and `core.expenses` stay empty and module foreign keys to them can't be exercised | quickbooks, lawpay-docuseal | open | core follow-up: store billing records in their tables |
| File purge is not a hard delete | documents | open | core follow-up: purge removes the file and its versions |
| CORE-01 lacks `getConflictCheck` and `createEvent` for service calls | lawpay-docuseal, documents, client-portal | open | CORE-01 |
| No timeline events written by a client (portal) principal | client-portal, lawpay-docuseal | open | core follow-up: let a client principal write a timeline event |
| A refused permission carries no reason the module can show | documents, quickbooks | open | core follow-up: permission reasons in the refusal |
