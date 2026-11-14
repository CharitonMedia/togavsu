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
