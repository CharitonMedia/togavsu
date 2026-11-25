# Core changes for the second batch of modules

The core and marketplace parts of the approved batch 2 modules PRD (approved by Patrick on 2026-11-16), copied unchanged: its fixed decisions that bind the core, section 4 (module API update 2, CORE-30 to CORE-37), section 5 (shared module rules the coretest harness serves), section 6 (migration import: opening invoices, core PRD section 8.22), the license bullet of section 15.5 (the key the provider issues and the paid module checks), section 17 (marketplace listings, with no selling or billing in Togavsu) and sections 18.3 and 18.4 (tests and review gate). The nine modules build in their own repositories, CharitonMedia/togavsu-<id>, from the public kit only; keep every name in this file exactly as given, because those repositories are written against it. This file outranks docs/modules/CORE-CHANGES.md where they differ for CORE-30 onward.

Source: togavsu-modules-batch2-prd.md as approved, SHA-256 d36dd2781883b9e8b90aba431483aa97e843c400082d72a2a35b286a72e3fb41, with Patrick's 2026-11-17 ruling that selling, billing and licensing belong to each module's provider, his 2026-11-21 ruling on walled-matter occupancy, and his 2026-11-24 clarification on deactivated attendees (below).

## Fixed decisions that bind the core

- **Order.** Batch 1's module API update (CORE-01 to CORE-29) lands first. Then module API update 2 (CORE-30 onward) and the opening invoice import ship as one signed core update. The marketplace listing work builds beside it. Then the nine modules build in parallel. Section 18 has the order.
- **Public kit only.** Every module is built only from the public kit (pkg/modulekit, cmd/togavsu-module, pkg/modulekit/coretest), the public API and the docs, the same way an outside author would build one. This batch tests the module system for future contributors as much as it ships features. A gap a module hits goes into docs/modules/CONTRIBUTOR-GAPS.md and gets fixed in the core, never worked around inside the module.
- **Self-contained.** No module reads core tables. Each has its own schema, mod_{id} with hyphens as underscores, links to core records by id with ON DELETE RESTRICT, and copies no core record. Every file is a core file record. Modules reach each other only through core-brokered service contracts (CORE-31), never by calling another module's address.
- **Access comes from the core.** Walls, roles and attorney-only rules are decided in the core's service layer for every module call. A service call runs as the module's service principal, which sits outside every wall: a walled matter's records are left out of its lists and not_found on its gets. One exception, by Patrick's 2026-11-21 ruling: the service occupancy read (listCalendarEvents with view=occupancy) also returns the events of walled matters that have no assigned attendee user, as bare start and end busy intervals with nothing else, so a booking module cannot book over them. A caller can tell that a busy time exists that it cannot otherwise see; it learns nothing else about that event, its matter or the wall. Walled events with an active assigned attendee stay out of occupancy. **Patrick’s 2026-11-24 ruling:** walled events whose attendee users are all deactivated count as unassigned on the service occupancy read only. Per-user `view=busy` retains its requested-user attendee filter; it does not include unassigned walled events.
- **No restarts.** Installing, configuring, updating, disabling and removing a module never restarts the core.
- **Endpoints for agents.** Every action on a module screen is also a JSON endpoint under /m/{id}/api/, authenticated by a context token from POST /api/v1/modules/{id}/context-tokens. Each module serves /m/{id}/api/openapi.json and lists its endpoints in its help guide.
- **Every tunable is a setting.** Any value a firm might change is a module setting, checked by settings.CheckModule. Limits an outside service fixes stay constants.
- **Credentials at runtime only.** Zoom, Google, NotebookLM connector and AI keys are entered by the firm admin, or for the marketplace by its operator, at runtime. Secrets sit in the core's sealed secret store or as secret settings, never in a module table, log or response. The build creates no service credential and never uses client data.
- **Outside services are faked in every test.** No test, CI job or default setting reaches Zoom, Google, NotebookLM, a payment service or an AI provider.
- **AI goes through the core.** Every model call is a createAiCall with a task the manifest declares in ai_tasks. The module never holds a provider key, and the core's router, wall check and call log apply.
- **Time.** Every stored time is timestamptz. Provider timestamps pass through as ISO strings. Date-only values stay dates. "Today" is computed in the firm's time zone. Unified's naive-timestamp bug is the reason.
- **Release stays private.** Images and packages stay private, and no listing goes public, until Patrick says so. The build tests against a local marketplace.

## 4. Core update: module API update 2

### 4.1 Rules

Module API update 2 is one core update, built by one module-core lane on top of batch 1's update, released as a signed core image and applied overnight by the supervisor. Every rule in batch 1 PRD section 4.1 holds: walls in the service layer, the append-only trust ledger, audit of every write and service call, API and MCP parity, settings in the registry, expand-only migrations, and old modules that keep working unchanged. Every addition is part of the public module API, documented in docs/api/modules.md and the OpenAPI spec, and open to any signed module that requests the scope and gets the admin's approval. Nothing in the core checks a module's id or author.

### 4.2 Additions

| Item | What the core adds | Needed by |
|---|---|---|
| CORE-30 Wider service calls | Marks these operations delegated_viewer_or_service_principal, each with its own per-minute limit setting: createContact, contact emails and phones, getContact, listContacts by email or phone (exact match only), createMatter, createMatterParty, getMatter, listMatters, runConflictCheck, getConflictCheck, createCalendarEvent, updateCalendarEvent, deleteCalendarEvent, listCalendarEvents, previewAvailableDates, createDeadline, updateDeadline, createNote, listMatterTimeline, listFileRenditions and downloadFileRendition for the markdown kind. A service-principal write records the module as its author and source service. | intake, phone, workflows, matter-wiki, discovery |
| CORE-31 Module service contracts | Manifest fields provides and consumes, each a list of named, versioned contracts such as phone.provider.v1. The admin approves each link between a consumer and a provider on the Modules tab. The core proxies the call: POST /api/v1/modules/contracts/{contract}/{operation} from the consumer reaches the provider's declared path, signed by the core with the same HMAC scheme as events, carrying the caller's context token when there is one. Providers can also push contract events to a consumer, delivered like core events. One provider per contract per firm unless the contract says many. No module learns another module's address. | phone, phone-zoom, phone-google, workflows, mo-estate-planning, documents 1.1, lawpay-docuseal 1.1 |
| CORE-32 Public module pages | Manifest public_pages: paths served at /p/{id}/... with no sign-in, under a per-IP rate limit setting, a body size limit setting and the core's CSRF and HMAC step-token helpers. A public page's server calls run as the module's service principal and only on operations the manifest lists under public_operations. The admin sees the public pages and their operations at install. | intake |
| CORE-33 Forms kit | A public kit package, pkg/modulekit/forms: the form definition model (tracks, screens, fields), the 16 field types (text, email, tel, textarea, number, date, select, multi-select, boolean, radio, address, masked, read-only, hidden, file, repeating), the conditions (eq, neq, in, exists, notExists, gt, lt, gte, lte, and count conditions on repeating groups), validation, an HTMX renderer for staff, client and public pages, autosave endpoints with offline queue, "I don't know" answers, and masked-field encryption through the module secret store. Answers stay in the calling module's own schema. | intake, mo-estate-planning |
| CORE-34 Banner slot | Manifest staff_banner: the core reserves a slot above every staff page and loads the module's banner page there in a sandboxed same-origin frame of fixed height. The frame bridge reports show or hide. One banner per module; the admin orders them. | google-notebooklm |
| CORE-35 Manifest price | The manifest's price field takes either the old string or a structured value {amount_cents, currency, interval: month or year, note}. The core shows it on the install screen and stores it; the core never charges, checks or enforces a price. | mo-estate-planning, marketplace |
| CORE-36 Contributor gap fixes | Fixes every open row in CONTRIBUTOR-GAPS.md that this batch hits: the /m/ proxy forwards X-Togavsu-Context-Token; /api/v1/me accepts a context token; a missing scope answers scope_denied, not a bare 403; the webhook signature format matches the docs; POST /api/v1/notes writes core.notes; invoices, payments and expenses are stored as rows in core.invoices, core.payments and core.expenses so module foreign keys work; conflict decisions emit conflict_check.decided as the contract says; getConflictCheck and createEvent are open to service calls; Accepts() reports core_items so a module can see which core items are built; coretest gains stand-ins for mail and Doc Tender renditions. Each fix closes its row in CONTRIBUTOR-GAPS.md. | all nine |
| CORE-37 Opening invoice import | Section 6. | core |

### 4.3 What the update adds to the base

- **Migrations:** the contract link table, the public page registry, the banner order setting row, the structured price columns on the module record, invoice import columns (section 6.4), and the move of billing records from JSON to rows with a backfill that reconciles every cent.
- **Settings:** modules.public_page_rate_per_minute (60), modules.public_page_max_bytes (10 MB), a per-operation service call limit for each CORE-30 operation (default 120 a minute), modules.contract_call_max_bytes (25 MB).
- **Scopes:** contracts:call, contracts:provide, public_pages:serve, banner:show.
- **Events:** module.contract_linked, module.contract_unlinked, invoice.imported.

### 4.4 Build order and acceptance

Order: CORE-36 first, because modules and later items depend on the fixes; then CORE-30; then CORE-31, CORE-32, CORE-33 and CORE-34 in parallel; then CORE-35 and CORE-37.

CU2-01 Every CORE item's tests pass against Postgres, and each new operation is in openapi.json, on /mcp, and refused without its scope.

CU2-02 A service call on every CORE-30 operation leaves out a walled matter's records and answers not_found on a walled get; lifting the wall returns them on the next read. The one exception is the occupancy read, which also returns the events of walled matters with no assigned attendee user as start and end intervals only (Patrick's 2026-11-21 ruling, section 1).

Note to CU2-02: Patrick clarified on 2026-11-24 that walled events whose attendee users are all deactivated count as unassigned on the service occupancy read only; per-user busy reads retain their requested-user attendee filter (section 1).

CU2-03 A contract call reaches only a provider the admin linked; unlinking stops it at once with contract_unlinked; neither module ever receives the other's address.

CU2-04 A public page serves with no session, refuses an operation not in public_operations, and answers 429 past the rate setting.

CU2-05 Every row CORE-36 names is closed in CONTRIBUTOR-GAPS.md, each with a test that failed before the fix.

CU2-06 After the billing move, every invoice, payment and expense reads the same through the API as before, to the cent, and a module foreign key to core.invoices installs and holds.

CU2-07 matter-clock and the five batch 1 modules install, run and uninstall on the updated core with no change to their packages.

CU2-08 The update applies overnight with no failed requests during the core's update load test, and a fresh install reaches first-run setup with no module installed.

## 5. Shared module rules

Every rule in batch 1 PRD section 5 holds: manifest, data, access, events, help and agent endpoints. These change or add:

- **Code.** Go standard library and the kit's dependencies, server-rendered HTMX pages, a Dockerfile on a distroless runtime, and serve and package subcommands. mo-estate-planning is the one exception, in section 16.
- **Repos.** Each lane writes only its own repo. A lane that needs a core change files it in CONTRIBUTOR-GAPS.md through the module-core lane; it never edits the core repo.
- **CI.** Each repo runs gofmt, go vet, go test -race against a Postgres 17 service, togavsu-module check -installable -strict package, and a docker build, as the batch 1 repos do. Each also gets a release workflow (section 17.5).
- **Contracts.** A consumer works when no provider is linked and says so on screen, in plain words naming what to install.
- **Client screens.** A module's client pages come through client-portal's client page slots (batch 1). A module with client screens works without client-portal installed, and shows staff "Client portal not installed" where a share would go.
- **Synthetic data.** Every acceptance test uses batch 1's fixture firm: Example Law LLC, attorney Jordan Testwell (bar MO 00001), matter 2026-0001 Sample v. Placeholder, client Avery Sample, opposing party Riley Placeholder, county Testcounty. Discovery fixtures come from the fictitious set in Unified's docs/model-comparison/fixtures. No real client document is ever a fixture.

## 6. Core: migration import, opening invoices

### 6.1 Purpose

A firm moving onto Togavsu brings each client's open balance. This puts those balances in as real invoices without faking their history, so collections, the client portal, statements and trust replenishment work from day one. It lives in the core as core PRD section 8.22, Migration import, because every firm that switches needs it.

### 6.2 Kept from Unified

From Unified's opening AR import PRD:

- A staff form with matter, historical invoice number, invoice date, amount due, due date and an optional message.
- One line on the invoice: "Balance forward as of [date]".
- It lands as sent and never touches the invoice number sequence.
- It refuses a number at or above invoice_number_start, a duplicate number, an amount of zero or less, or a missing matter.
- The original invoice PDF attaches as the document of record and the client sees it in the portal.
- An invoice_imported timeline event.
- Void works on a lineless invoice.
- An optional amount paid to date.

### 6.3 Screens

- **Admin page, Migration import tab, Opening invoices.** The single form above, and a CSV upload with the same columns plus contact email and the original PDF's file name. The upload shows a preview with every row's result before anything is written. The whole file is written in one transaction or not at all.
- **Matter billing tab.** An imported invoice shows "Imported" beside its number and opens its document of record.

### 6.4 Behavior

- The form and the CSV both write through the existing bulkInvoice endpoint, which already takes historic invoices with reconciled cents. The core adds three fields to its InvoiceImport body: source (form, csv or api), external_ref, and document_of_record_file_id.
- An imported invoice has one adjustment line, "Balance forward as of [date]", status sent, its historical number, and imported true.
- Amount paid to date, when given, posts as one core payment of method other, marked imported, so the balance due is right and no money moves.
- external_ref is unique per firm, so a second run of the same CSV skips rows already in and reports them.
- The client portal and statements show the document of record in place of a rendered invoice.
- Importer modules (MyCase, Clio, later) use the same endpoint with source api.
- Opening trust balances go in through ordinary trust deposit entries, which the core already has.

### 6.5 Acceptance

IMP-01 The form makes a sent invoice with one "Balance forward as of" line, its historical number and imported true; the next normal invoice takes the next number in the sequence, unchanged.

IMP-02 A number at or above invoice_number_start, a duplicate number, an amount of zero or less, or a missing matter is refused with a message naming the reason, and nothing is written.

IMP-03 A CSV of 500 rows with one bad row writes nothing and the preview names the bad row; with the row fixed, all 500 land in one transaction.

IMP-04 Re-running the same CSV writes nothing new and reports every row as already imported by external_ref.

IMP-05 Amount paid to date posts one imported payment and the balance due equals amount due less amount paid.

IMP-06 The client sees the document of record in the portal for an imported invoice, and staff open it from the billing tab.

IMP-07 Voiding an imported invoice works and logs the void; the sequence is untouched.

IMP-08 The collections panel, aging and statements include imported invoices exactly like others.

## 15. Missouri estate planning (mo-estate-planning), the paid module

### 15.5 Behavior

- **The license.** Chariton Media sells and bills the module itself, outside Togavsu; the marketplace and the core take no part. The module reads its license key from its secret settings, where the firm admin enters it. The key is an Ed25519-signed statement from Chariton Media naming the firm's install id, the module, and the paid-through date, and the module checks it offline against Chariton Media's public key built into the image. Chariton Media issues keys with the repo's cmd/issue-license tool and its own private key, kept outside Togavsu and outside the repo; a renewal is a new key the admin enters. After the paid-through date plus a grace period setting (14 days), Generate package stops with a plain message; every questionnaire, answer and document already made stays readable and downloadable. The core enforces nothing.

## 17. Marketplace: listings and submission

Patrick's ruling of Nov 17, 2026 (section 1): selling, billing, licensing and price changes are the module provider's business, not Togavsu's. The marketplace lists, installs and checks package signatures. It never sells, charges, subscribes, renews, refunds or issues a license.

### 17.1 Price from the manifest

The listing takes its price from the manifest's structured price (CORE-35) at first upload and shows it as the provider's own information, beside the line "Sold and licensed by {author}. Togavsu doesn't sell or bill this module." The author can change the listed price and a link to their own sales page on the marketplace without a new package; the change shows at once and touches no install. This closes the CONTRIBUTOR-GAPS row "listing ignores the manifest's price".

### 17.2 Upload takes the signed file

POST /v1/authors/me/packages takes the kit's signed package file as its body, unchanged. The jq step in MODULE-KIT.md goes away. This closes its CONTRIBUTOR-GAPS row.

### 17.3 Selling stays with the provider

- **No billing in Togavsu.** The marketplace has no checkout, subscription, renewal, refund, payment-service connection, platform fee or price-change notice, and stores no payment detail or license key.
- **A paid module checks its own license.** A provider that charges issues its own key and its module checks it, as mo-estate-planning does (section 15.5). The firm admin enters the key in the module's secret settings. The core and the marketplace enforce nothing.
- **The operator never reviews, tests or warrants a paid module,** the same as a free one. The listing keeps the core PRD 10.2 notice.

### 17.4 Public marketplace

The public marketplace deployment, and the marketplace address that replaces the togavsu-marketplace placeholder, wait for Patrick's release word. Until then the build runs and tests a local marketplace, and every listing stays private.

### 17.5 Signing and release

Each module repo gets a release workflow, as batch 1 PRD section 11 describes: build the image, push it to ghcr.io/charitonmedia/togavsu-{id}, sign it with Cosign on the core's release signing path, write the digest into the manifest, sign the package with the Chariton Media author key kept as a secret in each repo, attach the signed package to the module's GitHub release, and upload it to the marketplace. Releases are drafts and images are private until Patrick says otherwise.

### 17.6 Acceptance

MKT-01 A package with a structured price lists that price as the provider's information, with the "Sold and licensed by" line; a package with the old string still lists.

MKT-02 Uploading the kit's signed file unchanged succeeds; a tampered one is refused.

MKT-03 The author changes the listed price and sales link without a new package; the listing shows both at once, and nothing is charged, sent or changed on any install.

MKT-04 A priced module installs from the local marketplace with no key and no payment step.

MKT-05 The marketplace has no checkout, subscription, renewal, refund, payment-service connection, platform fee or price-change notice: no route, setting, state record, email or dependency for any of them.

MKT-06 No payment detail, license key or provider secret appears in marketplace state, logs or responses.

MKT-07 No marketplace test or code path reaches a payment service or any outside host; test helpers refuse any other host.

## Tests and review gate (batch 2 PRD 18.3 and 18.4)

### 18.3 Tests

Every module has unit tests and one Postgres acceptance suite built on pkg/modulekit/coretest, which builds and signs the package with a generated test key, installs it on the composed core, approves its scopes, and drives every MOD criterion with users of every role, a walled matter, and context and agent tokens. Failure paths are part of each criterion. Outside services are fakes and local test servers only, and test helpers refuse any other host.

### 18.4 Review gate

Every PR gets the Codex checkpoint at its exact base and head SHAs; a missing, failed or stale review blocks the merge. Review-bot comments are fixed by the lane, not passed to Patrick. Fix passes: limit passes to fix the same issue to 3, then raise it with Patrick. When automation stops for any reason, the build controller emails Patrick at the stall-alert address naming the issue, the attempts and what's blocking, and work that doesn't depend on it keeps going.
