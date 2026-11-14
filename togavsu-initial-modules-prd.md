# Togavsu initial modules PRD

Nov 12, 2026 · @Patrick

Status: approved scope, ready to build once the core release is tagged. Every decision below is final for this build. Nothing in this document waits on Patrick.

## 1. What we're building

Five signed marketplace modules for Togavsu, built after the core release and listed in the Togavsu marketplace like any other author's modules:

1. **Documents and templates** (`documents`): the firm's template library, token-filled drafts and whole document packages on a matter, matter zips, and version notes, compare and copy.
2. **Email** (`email`): reads, sends and files mail from the firm's existing Gmail, Microsoft 365 or Outlook.com, and standard IMAP/SMTP accounts; the unfiled queue; quick email; the Outlook add-in; system mail delivery. Running the firm's own mail server stays a later module.
3. **Client portal and messages** (`client-portal`): the only screens a client ever sees, plus the staff Messages inbox and the Portal access panel.
4. **LawPay and DocuSeal** (`lawpay-docuseal`): LawPay card and ACH payments into operating and trust, full and partial refunds, reconciliation, and DocuSeal e-signature for fee agreements.
5. **QuickBooks Online sync** (`quickbooks`): a one-way push of the firm's money records into QuickBooks Online for the read-only accountant role.

On Nov 12, 2026 Patrick decided: "close out the Togavsu build and then run a separate PRD for the 5 modules, and then we'll just submit them and enter them into the marketplace." So the core releases first, with no module. The modules aren't core build lanes; they're separate signed packages that any firm installs from the marketplace or from a local file. This PRD replaces the draft section 9.10 on branch control/prd-initial-modules, which held the core release until the modules shipped.

Each module is built from Nolan PMS Unified's working approach, written clean on Togavsu's records. Built fresh means writing it clean; it doesn't mean throwing out a working idea. QuickBooks has no Unified code; it follows the core PRD and the QuickBooks Online API.

The modules need core additions the released core doesn't have. Those ship first, as one signed core update (section 4), and become part of the core's public module API for every author.

## 2. Fixed decisions

- **Core first.** The core release (core PRD section 11, phase 7) finishes and tags with no module. Then the module API update (section 4) ships as a signed core update. The five modules build in parallel from Nov 14, 2026, beside the core release, against the module API as section 4 and CORE-CHANGES.md name it; each module's acceptance against the real core runs once the module API update is released (Patrick's decision, Nov 14, 2026).
- **Marketplace modules, not core.** Each module is its own signed package and container image, under every rule in core PRD sections 9 and 9.9. The core still installs and runs with none of them.
- **A repository per module.** Each module's source lives in its own repository, CharitonMedia/togavsu-<id> (togavsu-documents, togavsu-email, togavsu-client-portal, togavsu-lawpay-docuseal, togavsu-quickbooks), so each module builds independently and is accepted into the marketplace from its repository (Patrick's decision, Nov 14, 2026; it replaces the earlier single-repository plan). Images publish to ghcr.io/charitonmedia/togavsu-<id>.
- **Listing:** author Chariton Media, price free, for all five (section 11.3). Every manifest says `author: Chariton Media` and `price: Free`; the specs' other values ("Togavsu", "Togavsu build", "Included") are replaced.
- **License:** "Same as Togavsu", the core's source-available license (core PRD section 3).
- **Version:** each module starts at 1.0.0.
- **No module reads core tables.** Each module has its own schema (mod_<id> with hyphens as underscores), links to core records by id with ON DELETE RESTRICT foreign keys, and copies no core record. Every file is a core file record; no module keeps file bytes.
- **Access comes from the core.** Walls, roles and attorney-only rules are decided in the core's service layer for every module call. A module shows a row only for a core record the API returns to that viewer. No service principal crosses a wall.
- **Money stays in the core.** Payments, refunds and trust entries are core records written through the core API. The trust ledger stays append-only; a correction is a reversal. A below-zero trust entry posts, flags and emails, through a module exactly as through the core.
- **No restarts.** Installing, configuring, updating, disabling and removing a module never restarts the core. The core update applies itself overnight in the firm's update window.
- **Endpoints for agents.** Every action on a module screen is also a JSON endpoint under /m/<id>/api/, authenticated by a context token an agent gets with its bearer token from POST /api/v1/modules/<id>/context-tokens. Each module serves its own OpenAPI document at /m/<id>/api/openapi.json and lists its endpoints in its help guide.
- **Every tunable is a setting.** A value a firm might change is a module setting on the module's settings form (admin page, Modules and marketplace tab), never a literal in code, so the core's settings.CheckModule passes. Limits an outside service fixes (Intuit's request ceiling, Microsoft Graph's 3 MB upload threshold) stay constants.
- **Credentials at runtime only.** LawPay, DocuSeal, Intuit, Google and Microsoft keys and mailbox sign-ins are entered by the firm admin (or, for a personal mailbox, its owner) at runtime. Module secrets sit in the core's sealed secret store or as secret settings, never in a module table, log or response. The build creates no service credential and never uses client data.
- **Outside services are faked in every test.** No test, CI job or default setting reaches LawPay, DocuSeal, Intuit, Google, Microsoft or a real mail server.

## 3. Sources and precedence

- **Specs:** docs/modules/documents/SPEC.md, email/SPEC.md, client-portal/SPEC.md, lawpay-docuseal/SPEC.md and quickbooks/SPEC.md, and docs/modules/CORE-CHANGES.md (CORE-01 to CORE-29), written Nov 12, 2026 from Unified's working code. They are on main under docs/modules/, landed by the first part of the control change that starts this build (section 12.1).
- **Precedence:** this PRD, then CORE-CHANGES.md, then each SPEC.md. Where a spec and this PRD differ (author, price, build order, tunables), this PRD wins. Everything this PRD doesn't restate is built exactly as its spec says.
- **Criterion IDs** keep the specs' numbering (MOD-DOC, MOD-EML, MOD-POR, MOD-PAY, MOD-QBO), so CORE-CHANGES.md's cross-references stay valid.

## 4. Core update: the module API

### 4.1 Rules

The module API update is one core update, built by one module-core lane, released as a signed core image and applied by the supervisor in each firm's overnight update window. It follows every core rule:

- **Walls, roles and attorney-only redaction** are applied in the service layer, so the UI, the API, /mcp, widgets, include= reads, published views and module extensions get identical answers. A service call (no viewer) runs as the calling module's service principal, which sits outside every matter wall: a walled matter's records are left out of its lists, `not_found` on its gets, and missing from its totals and events.
- **Trust:** the ledger stays append-only. A payment refund on a trust payment posts a ledger row of type refund under the below-zero rule; nothing edits or deletes a ledger row.
- **Audit:** every write, and every service call, goes to the audit log with the module id and source (`service` for a service call). Secrets and temporary passwords never reach logs, the audit log or events.
- **API and MCP parity:** every new operation is in the OpenAPI spec, on /mcp, with its scope text in api/contract/scopes.py and internal/identity/scopes.go.
- **Settings:** every new tunable is in the settings registry with its default, on its admin page tab.
- **Migrations** are expand-only, so the running core and the new core share the schema during the swap. No restart.
- **Public module API:** every addition below is part of the core's public module API, documented in docs/api/modules.md and the OpenAPI spec, and open to any signed module that requests the scope and gets the admin's approval. Nothing in the core checks a module's id or author to grant it anything.
- **Old modules keep working.** A manifest without the new fields installs and runs as before; matter-clock passes unchanged.

### 4.2 Additions

Each item is built as CORE-CHANGES.md defines it.

| Item | What the core adds | Needed by |
| --- | --- | --- |
| CORE-01 Service calls | `module_delegation` value `delegated_viewer_or_service_principal`: a module call with no context token runs as the module's service principal, only on operations marked for it, each with its own limits; every other operation and every unmarked write answers `scope_denied`. | quickbooks, lawpay-docuseal, email, client-portal |
| CORE-02 Books reads | Scope `books:read` (module tokens only) and the books reader: invoice, payment, expense, trust account, trust ledger and trust balance reads with no viewer, with the accountant's field set, firm-wide except walled matters. A walled matter's records never reach the module: left out of lists, `not_found` on gets, absent from totals, their events dropped; they come back on the next read after the wall lifts, with no catch-up events. Plus GET /api/v1/books/profile, /books/matters/{id} and /books/contacts/{id} (billing details only). No writes. | quickbooks, lawpay-docuseal |
| CORE-03 Notification kinds | Manifest `notifications` (kinds named `{module_id}.{kind}` in each user's preferences grid) and the POST /api/v1/notifications route, with recipients by `user_id`, by `audience` on a matter, or by `recipient_roles` (admin, accountant) on a service call; walls drop excluded recipients; email per each user's preference. | client-portal, email, lawpay-docuseal, quickbooks |
| CORE-04 Module mail | Scope `mail:send` and POST /api/v1/mail/messages to users, or to parties on a matter, through system mail; the core resolves addresses and never returns one; a `mail.sent` timeline row with the subject, never the text. | client-portal, lawpay-docuseal |
| CORE-05 System mail routing | Manifest `container.system_mail`, scope `mail:system`, one systemMailer behind every core mail sender; when the admin routes system mail through the email module, the core posts each message to it signed and idempotent, and falls back to SMTP when the module answers `not_configured` or isn't running. | email, client-portal, lawpay-docuseal |
| CORE-06 Module secret store | Scope `module_secrets:write` and PUT, GET, DELETE /api/v1/modules/{id}/secrets/{name}, callable only by that module's own token; sealed with the core key; names only in the audit log; kept or removed with the module's data at uninstall. | email, quickbooks |
| CORE-07 Inbound provider webhooks | Manifest `inbound_webhooks` and the public route POST /hooks/{module_id}/{hook_id}, which forwards the raw body to the module minus cookies and credentials, with size and rate settings (`modules.inbound_webhook_max_bytes` 1048576, `modules.inbound_webhook_rate_per_minute` 120). The only public inbound route for modules. | lawpay-docuseal |
| CORE-08 Sidebar placement and badges | Manifest sidebar `section`, `subgroup`, `icon`, `order` and `badge {path, tone}`; the core fetches each badge as the viewer (500 ms limit, 30-second cache, left off on failure); icons `mail`, `message` and `file` join the core set. | all five |
| CORE-09 Frame bridge and sandbox | postMessage bridge from a module frame (`editor.open`, `navigate`, `context.refresh`) and one sandbox list per frame kind: staff frames add downloads and popups; client frames also allow top navigation on a click; neither ever gets `allow-same-origin`. | all five |
| CORE-10 Client pages and client tabs | Manifest `tabs[].placement: client`, `tabs[].params`, `tabs[].nav` and `client_home`; /portal and /portal/{module_id}/{tab_id} framed full-window for client users only; /portal/security and /portal/sign-out; GET /api/v1/nav/client-context. | client-portal, lawpay-docuseal |
| CORE-11 Client matter reads | The client role reads its own matters with a field allowlist; setting `client_access.days_after_close` (default 14, Security tab) ends a client's access to a closed matter, its files and invoices; reopening restores it. | client-portal |
| CORE-12 Client trust reads | GET /api/v1/me/trust and /me/trust-ledger (the client's own figures only, no account numbers) and client reads of replenishment requests naming the client. | client-portal, lawpay-docuseal |
| CORE-13 Client contact self-edit | GET and PATCH /api/v1/me/contact for primary email, phone and mailing address, keeping history rows, audited with the client as actor. | client-portal |
| CORE-14 Client access management | Scopes `client_access:read` and `client_access:write`; `core.users.must_change_password`; grant, temporary password, deactivate and reactivate for client users only, with mail by system mail and the password shown once and never logged. | client-portal, lawpay-docuseal |
| CORE-15 Client appointments | GET /api/v1/me/appointments: upcoming client meetings and court dates on the client's readable matters, limited fields. | client-portal |
| CORE-16 File origin on module publication | Optional `origin` on module file publication; a file a module publishes for a client viewer is a client upload (origin received, uploader the client), while the audit row names the module as the channel. | email, client-portal |
| CORE-17 Library files | `core.files.library`: firm-only files with no matter or contact, kept out of Doc Tender (no job, no exception, no tokens spent). | documents |
| CORE-18 Firm profile read | Scope `firm:read` and GET /api/v1/firm-profile (firm details, time zone, client portal colors and label), every role. | documents, client-portal |
| CORE-19 User directory | Scope `users:directory` and GET /api/v1/user-directory (id, name, roles, active; a client caller gets staff names only). | documents, client-portal |
| CORE-20 Delegation on settings reads | County, practice area, party role and custom field definition reads take a module viewer call. | documents |
| CORE-21 Archive entries | An `entries` selector on POST /api/v1/file-archives (version or rendition plus a checked relative path), each entry checked as the viewer before the first byte; one streamed response finishes after the token expires. | documents, client-portal |
| CORE-22 Linkable core records | `fee_agreement`, `payment_refund` and `trust_account` become records a module can link to and extend; `fee-agreements` works with include=. | lawpay-docuseal, quickbooks |
| CORE-23 Payment processors | Manifest `payment_processor`; processor initialization, delivery of the processor grant, and its use on createPayment and updatePaymentStatus; client-initiated payments limited to the client's own matter and invoice; pending grants valid for `payments.processor_pending_grant_days` (default 30); a payment that names a replenishment request satisfies it. | lawpay-docuseal |
| CORE-24 Payment refunds | Core record `payment_refunds`, POST, status and list endpoints (admin and attorney; trust refunds also need trust:withdraw), `refunded_cents` and `refund_state` on Payment, a refund grant for the processor module, and completion in one transaction: a trust refund ledger row under the below-zero rule, or a lower amount paid on an operating invoice. | lawpay-docuseal |
| CORE-25 Fee agreement signature callback | `fee_agreements.signature_module_id`; the module that sent an agreement may move it to signed and mark that one file executed, and nothing else. | lawpay-docuseal |
| CORE-26 Mail matching | POST /api/v1/mail/matches: contacts by address and candidate matters by number, case number, name or known id; a service call omits walled matters and returns a 15-minute match token. | email |
| CORE-27 Mail filing grant | POST /api/v1/mail/filing-grants: a 15-minute task authorization for one module, one unwalled matter and one message, accepted on note and file writes for that matter, plus `mail_matching` AI calls. | email |
| CORE-28 Device grants | Manifest `device_connect`, the /modules/{id}/device-connect page, device grant list and revoke, and exchange for a 10-minute context token; grants lapse after 90 days unused or when the user is deactivated. | email |
| CORE-29 Acceptance harness | The module test harness serves every core route the module suites call, so each module's Postgres acceptance suite runs its module in process with no other harness change. | all five |

### 4.3 What the update adds to the base

- **Migrations:** `core.users.must_change_password`, `core.files.library`, `core.payment_refunds` with the payment refund columns, `core.fee_agreements.signature_module_id`, the module secret table and the module device grant table.
- **Settings:** `client_access.days_after_close`, `modules.inbound_webhook_max_bytes`, `modules.inbound_webhook_rate_per_minute`, `payments.processor_pending_grant_days`. `mail.route_through_email_module` exists already.
- **Scopes:** `books:read`, `mail:send`, `mail:system`, `module_secrets:write`, `client_access:read`, `client_access:write`, `firm:read`, `users:directory`.
- **Events:** `mail.sent`, `client_access.granted`, `client_access.password_reset`, `client_access.deactivated`, `client_access.reactivated`, `payment.refund_created`, `payment.refund_authorized`, `payment.refunded`, `payment.refund_failed`, `mail.filing_authorized`, `module_device.created`, `module_device.revoked`.
- **Dependencies:** the email module's libraries are pinned in go.mod and go.sum by the control change, before any lane starts: github.com/emersion/go-imap/v2 v2.0.0-beta.5, github.com/emersion/go-message v0.18.1, golang.org/x/net v0.33.0 and github.com/emersion/go-sasl. No lane edits go.mod or go.sum.

### 4.4 Build order and acceptance

Order, because later items use earlier ones: CORE-29 and CORE-01; then CORE-22, CORE-08, CORE-09, CORE-03, CORE-05, CORE-04 and CORE-06; then CORE-10 to CORE-16; then CORE-17 to CORE-21; then CORE-02 and CORE-23 to CORE-28.

Each item carries its own core tests beside the code it changes. The update is done when these pass in automated tests:

- **CU-01** Every CORE item's tests pass against Postgres, and each new operation is in openapi.json, on /mcp, and refused without its scope.
- **CU-02** No service call, books read, mail match, notification or event delivery returns any text, count or total from a walled matter; lifting the wall returns its records on the next read.
- **CU-03** A trust payment refund posts one ledger row of type refund; one that takes a client below zero posts, raises a flag and emails the admins and the responsible attorney; no path edits or deletes a ledger row.
- **CU-04** matter-clock installs, runs and uninstalls on the updated core with no change to its package.
- **CU-05** The update applies overnight through the supervisor with no failed requests during the core's update load test, and a fresh install of the updated core reaches first-run setup with no module installed.

## 5. Shared module rules

- **Manifest:** block-style togavsu-module.yaml with only fields the updated core accepts; image digest pinned to the built image before signing.
- **Data:** numbered migrations that touch only the module's schema; no functions, triggers, extensions, roles or grants; no table named for or duplicating a core record; extension tables keyed to the core record's id; published views declared in the manifest.
- **Access:** interactive calls carry the viewer's context token; background calls are service calls or carry a core task authorization. Every module page re-checks the viewer through the API; a role a page doesn't serve gets 404.
- **Events:** each module verifies the core's webhook signature (HMAC of the raw body, timestamp within five minutes) and handles each event id once.
- **Help:** each module registers its guide pages under its own heading, in real Markdown that gives the menu path and how to use each screen; client users get no help pages.
- **Agent endpoints:** section 2. Each module's acceptance suite calls at least one endpoint per screen action with an agent's context token.
- **Code:** Go standard library and the dependencies already pinned in the core's go.mod only; server-rendered HTMX pages; a Dockerfile on a distroless runtime; `serve` and `package` subcommands as matter-clock has. Each module's build writes only its own repository, CharitonMedia/togavsu-<id>; the spec stays at docs/modules/<id>/ in the core repository.

## 6. Documents and templates (`documents`)

### 6.1 Purpose

Turns the firm's own DOCX templates into finished drafts on a matter, generates whole document packages in one step, downloads a matter's files as one zip, and adds version notes, compare and copy on top of core file versions. It owns nothing the core owns: template bodies, components and outputs are core files; editing is LawCraft through the core editor; Doc Tender names, categorizes and files every generated draft.

### 6.2 Kept from Unified

- Template library with name, subject area (now core practice areas), document class, topics, description and active or archived; search by name, area and topic.
- Reusable components (caption, signature block, certificate of service, letterhead) stitched in by slot: a paragraph holding only `{{slot:name}}`.
- Body edits add an immutable version; metadata edits don't.
- Unified's field vocabulary and token resolution: client from the client party, address from the current history row, attorney from the responsible attorney, long dates in the firm's time zone. `matter_internal_number` becomes `matter_number`.
- DocForge generation rules: a package is all or nothing, no unresolved token ever ships, every run makes new draft files, a failed run reports each template and token by name.
- Finalize and revert to draft, as the core's status tag, never in the name.
- One timeline line per run.
- Bulk download of a selection or folder with no cap, streamed, deduped entry names, named by matter and date, a frozen manifest per download and one timeline line.

### 6.3 Screens

- Home tabs: Templates (templates, packages, components; search and filters; create, edit metadata, archive) and Generated drafts (drafts, with a Final filter, Open in editor, Mark final, Mark draft).
- Matter tab Templates and downloads; matter actions New from template, Download files as zip and Version tools; panel Recently generated.
- Widget Generated drafts (4x3 or 6x4; top eight drafts of the last `drafts_window_days`), opening the drafts tab filtered to the clicked row's matter.
- Sidebar Templates in the Documents section (icon file, order 200); quick create Document from template; search scope Templates.
- Admin only: Attorney profiles (bar number, bar state, courtesy title typed by the admin, signature line) and Release (trashed files the module still links).
- Accountant and client viewers get 404 on every page and endpoint.

### 6.4 Behavior

- **New template:** upload a DOCX (a zip holding word/document.xml; anything else refused), save the current version of a readable file as a template, or blank (a minimal DOCX built with the standard library holding `{{document_title}}`). The body is a library file (CORE-17).
- **Components** copy paragraphs and tables into the slot, so a component holding a picture, hyperlink, footnote, endnote, comment, numbering reference or any other relationship part is refused at save and at generation, naming the part.
- **Tokens:** `{{name}}` in the body, tables, headers and footers; a token split across runs still resolves in the first run's formatting; values are XML-escaped and line breaks become DOCX breaks. Vocabulary: matter, client, opposing party, served party, firm, attorney, document and dates, and `matter_cf_<key>` and `client_cf_<key>` custom fields. A blank value counts as unresolved; any other token is a manual fill on the form. No token reads an SSN or EIN.
- **Generate:** the assemble page shows each token, its value and source, and inputs for the rest; with two client parties it asks which. Generate is refused while anything is unfilled. Every output renders in memory and publishes in one bulkFile call: arrival name `<template-slug>-<YYYY-MM-DD>.docx`, status draft, attorney-only from the template, not shared with the client, the template's category hint when set. With output DOCX and PDF and the editor permission, a PDF is converted beside each draft; a failed conversion leaves the DOCX and offers Retry PDF. Typed values are never stored in module tables, logs or events.
- **Packages:** an ordered list of up to `max_package_templates` templates; one form gathers the union of tokens.
- **Zip:** whole matter, folder or selection; status and category filters; current or all versions; optional Markdown copies; folders or flat. The module writes the manifest, then streams the core's archive (CORE-21). Quarantined versions need a confirmation; an empty scope says "Nothing to download".
- **Version tools:** one note and label per version, compare by line diff of two versions' Markdown copies, copy a version as a new file, save as template.
- **Release:** module rows hold core files with RESTRICT keys, so the core refuses to purge a tracked trashed file; Release drops the module's links after the admin confirms, keeping manifest paths and SHA-256s.

### 6.5 Data

Schema `mod_documents`: templates, template_practice_areas, components, template_slots, packages, package_practice_areas, package_templates; generation_runs (outcome, error report of names only, the names of hand-filled tokens, never values); generated_files (extension of file); bulk_downloads and bulk_download_items (the frozen manifest); version_notes, version_copies; attorney_profiles (extension of user); received_events. Published view `generated_documents` (file, template, run, template version) under `views:documents.generated_documents:read`.

### 6.6 Permissions, events and settings

- **Required scopes:** matters:read, contacts:read, files:read, files:write, settings:read, firm:read, users:directory, events:read, events:write. **Optional:** editor:run (PDF beside a draft), doc_tender:read (category hint and status line).
- **Events:** subscribes to file.updated and file.trashed; writes `documents.generated` and `documents.bulk_downloaded` timeline events.
- **Settings:** default output (docx; or docx_and_pdf), most templates in one package (25), zip layout (folders), days of drafts the widget shows (30).
- **Outside services:** none.
- **Core update items:** CORE-08, CORE-09, CORE-17 to CORE-21.

### 6.7 Acceptance

All on synthetic data: firm Example Law LLC, attorney Jordan Testwell (bar MO 00001), matter 2026-0001 Sample v. Placeholder, client Avery Sample, opposing party Riley Placeholder, county Testcounty.

- **MOD-DOC-01** The signed package installs on the updated core, creates mod_documents with every table and view, and shows all eleven scopes with reasons; declining a required scope cancels the install.
- **MOD-DOC-02** Declining editor:run installs, and the DOCX and PDF output shows "Needs the editor permission" and can't be picked; declining doc_tender:read hides the category hint and status line.
- **MOD-DOC-03** Uploading a DOCX makes a library file (no matter, no contact) and a template row; its Doc Tender status is not_processed_library and no exception opens.
- **MOD-DOC-04** Uploading a PDF, or a zip without word/document.xml, returns 422 naming the reason; nothing is published or written.
- **MOD-DOC-05** A blank template's file is a zip whose word/document.xml holds `{{document_title}}`, and the core editor opens it.
- **MOD-DOC-06** A new version of the body file shows as newest on the template page, and the next generation records that version number.
- **MOD-DOC-07** Editing name, topics or practice areas adds no file version.
- **MOD-DOC-08** Generating from an archived template or package returns 409 and writes nothing.
- **MOD-DOC-09** Check template on a body with `{{client_full_name}}`, `{{school_name}}` and `{{slot:caption}}` reports one known token, one manual-only token and one slot; saving with caption unmapped returns 422.
- **MOD-DOC-10** A component holding a hyperlink returns 422 naming "hyperlink"; one holding a numbered list returns 422 naming "numbering".
- **MOD-DOC-11** Every vocabulary token resolves to its expected value on the synthetic matter, including matter_county "Testcounty", attorney_bar_number "00001" and today_long in the firm's time zone at a fixed test clock.
- **MOD-DOC-12** `{{client_full_name}}` split across three runs resolves to "Avery Sample" in the first run's formatting; `R&D <Co>` is written escaped; a two-line value writes a w:br.
- **MOD-DOC-13** Tokens in headers and footers resolve.
- **MOD-DOC-14** A slot paragraph is replaced by the mapped component's paragraphs and tables in order, with its tokens resolved.
- **MOD-DOC-15** With `{{school_name}}` unfilled, Generate returns 422 naming the template and token; no file, no event, and the run is failed.
- **MOD-DOC-16** A blank manual value is refused; after a run with "Testvalue-7731" typed, no module table, module log line or core event contains that string.
- **MOD-DOC-17** With two client parties the page requires a choice, and the chosen client fills the client tokens.
- **MOD-DOC-18** A single-template run publishes one DOCX on the matter: draft, origin firm, not shared, attorney-only from the template, source documents, with a generated_files row returned through include=documents.generated.
- **MOD-DOC-19** Generating the same template twice makes two files; the first file's versions are unchanged.
- **MOD-DOC-20** One successful run writes exactly one documents.generated event on the matter timeline.
- **MOD-DOC-21** With DOCX and PDF output, a run makes a PDF linked to its DOCX; with the converter failing, the run is pdf_failed, the DOCX stays, and Retry PDF makes the PDF.
- **MOD-DOC-22** A template's category hint becomes its output's category, and Doc Tender keeps it.
- **MOD-DOC-23** A package of three whose third template maps a slot to a component holding a picture returns 422 naming that template and "picture" and publishes nothing; fixed, it publishes three drafts in one bulkFile call.
- **MOD-DOC-24** A package over the template limit can't be saved.
- **MOD-DOC-25** Mark final sets the status and drops the file from the default list; Mark draft returns it.
- **MOD-DOC-26** A staff user off a walled matter's wall sees none of its drafts in the tab or widget, and its module pages return 404.
- **MOD-DOC-27** The widget's click-through opens the drafts tab with the matter's id, showing only that matter's drafts.
- **MOD-DOC-28** A whole-matter zip by staff of six files (four in two folders, one unfiled, one trashed) plus one attorney-only file holds exactly the five visible current versions under Pleadings/, Correspondence/ and Unfiled/; manifest rows match the zip's paths, versions and SHA-256s; the core logged five downloads; one documents.bulk_downloaded event exists.
- **MOD-DOC-29** Folder scope, a selection and a status filter of final each produce exactly their files.
- **MOD-DOC-30** All versions with Markdown on a two-version file writes name-v1.docx, name-v2.docx, name-v1.md and name-v2.md where those copies exist.
- **MOD-DOC-31** An empty scope says "Nothing to download" with no manifest and no event; a quarantined version left out is absent from the zip and the manifest.
- **MOD-DOC-32** A stream cut mid-zip marks the manifest interrupted with no summary event.
- **MOD-DOC-33** A note and label on version 2 show on the version list, and a viewer without write access gets 404 on the write; compare marks one added and one removed line for a one-line change, and a version with no Markdown copy shows "Markdown copy not ready"; Copy as new file makes a second file whose version 1 SHA-256 equals the source's, plus a version_copies row.
- **MOD-DOC-34** A staff viewer gets 404 on Attorney profiles; after the admin saves bar number 00001, the token resolves to it and include=documents.attorney returns it to an admin.
- **MOD-DOC-35** A delivered file.updated refreshes a tracked file's status once; a second delivery of the same event id changes nothing; a bad signature returns 401.
- **MOD-DOC-36** Purging a trashed generated file is refused naming documents; after Release the purge succeeds and the manifest row keeps its path and SHA-256.
- **MOD-DOC-37** Template search finds by name, topics or description for admin, attorney and staff; quick create opens a matter picker and then the assemble page; the six help pages appear under Documents and templates.
- **MOD-DOC-38** Accountant and client viewers get 404 on every module page and endpoint and aren't offered the widget.
- **MOD-DOC-39** Uninstall keeping data leaves mod_documents locked and a reinstall reads it; deleting data drops it after a backup and typed confirmation; library and generated files stay in the core.

### 6.8 Not in this module

Questionnaire tokens and the DocForge engine (intake module and DocForge), Quick Parts (LawCraft), page split and merge (Doc Tender), client bulk download (client portal), signing and sending (lawpay-docuseal and email), importing Unified's HTML templates (record importers), clause logic. The library starts empty.

## 7. Email (`email`)

### 7.1 Purpose

One module runs the firm's whole mail system (core PRD 9.6). It connects Gmail (Gmail API), Microsoft 365 and Outlook.com (Microsoft Graph) and any IMAP/SMTP server; reads, sends and files mail to matters as core files plus one core note; keeps the unfiled queue; sends quick email; runs the Outlook add-in; and delivers the core's system mail when the admin routes it there. Doc Tender processes every filed message and attachment like any arriving file.

### 7.2 Kept from Unified

- One send path that logs every attempt, a sending-disabled switch for staging copies, and a definite sent or failed answer for every send.
- Quick email: an empty or invalid To or empty body sends nothing; the matter record is written only after a confirmed send; a send that went out but didn't record answers `recorded: false`.
- The mailbox worker: INBOX first, then subfolders minus Archive, Sent, Trash, Drafts, Junk, Spam, Filed and Review-Later; one run at a time. Unified's clients@ becomes a filing account, efile@ a court account, an attorney's mailbox a personal account.
- Claim before work, so concurrent runs and restarts never file twice.
- Plus-address tokens (`filing+2026-0042@`) naming a matter by number, case number or client last name.
- The address matcher (firm addresses never count; exactly one contact on exactly one open or on-hold matter; every ambiguity recorded by reason) and the court notice matcher (full case number, never stripped of its suffix; trial then appellate pattern; party text must corroborate the client's last name; caption name fallback; any matter status), with a strong-model pick asked once per message.
- Hourly re-match of unfiled mail, plus at once on matter and contact events.
- The filed HTML copy with remote resources blocked and links live; attachments filed beside it, inline images skipped; no time entry.
- Retention by account role: delete after a clean file only on filing accounts; court mail marked read and moved to Filed, never deleted; unmatched mail left in place.
- The unfiled queue with File, Defer, Defer all, Dismiss with a reason, Retry, a sanitized preview and a sidebar count; the per-run filing summary.
- The Outlook add-in's identity probe, matter suggestion, matter and contact search, now with a newly written taskpane signed in by a core device grant.

### 7.3 Screens

- Home tabs: Mail (the viewer's personal Inbox and Sent; read, reply, reply all, forward, new, file to matter, mark read), Unfiled email (the queue) and Mail accounts (connect and manage accounts, the viewer's preferences, the add-in download).
- Matter tab Mail (filed messages by thread); panel Recent mail (newest five); action Email the client.
- Widgets Inbox (unread count and newest ten personal messages) and Unfiled email (oldest eight with age and best suggestion).
- Sidebar Mail and Unfiled email (with a warning-tone count) in Communications; quick create New email; search scope Email.
- Outlook add-in served at /m/email/outlook-addin/, with its manifest.xml downloaded from the Mail accounts tab.

### 7.4 Behavior

- **Accounts:** the admin connects filing, court and firm personal accounts; any admin, attorney or staff user connects their own personal account. OAuth opens the provider's consent page in its own window with PKCE and a state bound to the user; IMAP takes host, ports, security and login. Defaults: personal labels after filing and can send; filing deletes after filing with plus addressing on; court moves to Filed and can't send. Database checks refuse delete on anything but a filing account and an ownerless personal account. The admin sees every account's status but personal mail only on their own account.
- **Sync** every `poll_minutes` per account under an advisory lock; the first sync reads `backfill_days` back. Personal accounts read Inbox and Sent; filing and court accounts read Inbox and subfolders minus the skip list, never Sent. Fetching never marks mail read. Failures retry after 5 minutes, 30 minutes, 2 hours and 12 hours, then show as errors with Retry.
- **Matching order,** first decisive step wins: duplicate Message-ID; plus token; thread; court rules (court accounts); address (filing and personal accounts). A background match never returns a walled matter, so that mail waits in the queue for someone on the wall.
- **Filing:** a match token and filing grant in the background, or the viewer's context token by hand; the raw message is a temporary working copy checked against its SHA-256 and deleted when done; the HTML copy and each attachment become core files linked by attachment links, plus one note (title `Email: <subject>`, the header lines and text body, tags email and email-in, email-out or court-notice). Origin received for inbound and court mail, firm for outbound. An attorney-only filing makes the files attorney-only and writes a note that says only that. Every write carries an idempotency key, so an interrupted filing resumes without duplicates.
- **Compose:** To, Cc, Bcc, subject, body, optional matter, attachments from the matter's readable files up to 20 MB; replies keep In-Reply-To and References and send from the account the message came in on when the user may send as it.
- **System mail:** POST /system-mail checks the signature and idempotency key and sends through the account marked for system mail; 503 `not_configured` with none; a system mail row keeps no body once sent, because system mail carries password reset links.
- **Summary** to `summary_recipients` at most once per `summary_interval_minutes`, only when something filed.

### 7.5 Data

Schema `mod_email`: accounts (provider, role, address, owner, connection fields, a secret's name only, after-filing action, status), folders (cursors and UID state), oauth_states, user_prefs; messages (sanitized body, parts list, status, match reason; never raw MIME bytes), message_parts, message_contacts, message_matches; filings (unique per matter and Message-ID), queue_actions (append-only), matter_mail (extension of matter: filed count, last mail); outbound, outbound_files, summaries, received_events. Published view `filed_messages` under `views:email.filed_messages:read`. Passwords and OAuth tokens live only in the core secret store as `account/<account id>/<kind>`.

### 7.6 Permissions, events and settings

- **Required scopes:** matters:read, contacts:read, notes:write, files:read, files:write, events:read, notifications:write, module_secrets:write. **Optional:** ai:use (court notice pick), modules:read (the Outlook add-in), mail:system (system mail delivery). Declining an optional scope marks that feature unavailable.
- **AI task:** `court_notice_pick`, run through the core on the AI tab's labeling model.
- **Events:** subscribes to matter.* and contact.*; notification kinds `email.account_problem` (in-app and email) and `email.unfiled_mail` (in-app).
- **Settings:** Google client id and secret; Microsoft client id, secret and tenant (default organizations); firm domains (default the connected accounts' domains); poll minutes (5); backfill days (30); case number patterns (default Unified's Missouri trial then appellate patterns); court AI pick (on); summary recipients (none); summary interval (60 minutes); sending disabled (off).
- **Outside service adapters:** one Provider interface (check, folders, changes, fetch raw, after-file action, move, mark read, send) with Gmail, Graph and IMAP/SMTP adapters and an in-memory fake that can fail sign-in, time out, return a stale cursor, change UIDVALIDITY and drop a message between list and fetch. Tests use the fake, httptest stand-ins of the Gmail and Graph wire formats, and the go-imap v2 in-memory server on 127.0.0.1; test helpers refuse any other host.
- **Core update items:** CORE-01, CORE-03, CORE-05, CORE-06, CORE-08, CORE-09, CORE-16, CORE-26 to CORE-28.

### 7.7 Acceptance

All on synthetic data: firm domain firm.example.test, client Pat Doe at pat@client.example.test, matter 2026-0042, case number 26AB-CV00123, the fake provider.

- **MOD-EML-01** Connecting a fake IMAP account stores its password only through the core secret store; no mod_email column, log line or response holds it.
- **MOD-EML-02** A court account set to delete after filing, and a personal account with no owner, are refused by the database.
- **MOD-EML-03** An OAuth callback with an unknown or expired state stores nothing and answers 400.
- **MOD-EML-04** A sign-in failure sets needs_reauth and sends one email.account_problem notification to the owner, or to the admin who connected a firm or court account, emailed by default, and no second one until the account recovers.
- **MOD-EML-05** Staff asking for another user's personal account get not found.
- **MOD-EML-06** Two concurrent syncs over the same three messages make three rows and no duplicate filing.
- **MOD-EML-07** A changed UIDVALIDITY re-reads the folder with no duplicate rows.
- **MOD-EML-08** Reading a message never marks it read at the provider.
- **MOD-EML-09** A message removed between list and fetch is skipped without error and not retried.
- **MOD-EML-10** Trash, Junk, Drafts, Filed and Review-Later folders are never read.
- **MOD-EML-11** Mail from pat@client.example.test, whose contact is a party on exactly one open matter, files to 2026-0042 by address.
- **MOD-EML-12** An address held by two contacts queues with multiple_contacts; one contact on two open matters queues with multiple_open_matters.
- **MOD-EML-13** Mail to filing+2026-0042@firm.example.test files by token from an unknown sender; a last-name token matching two open matters queues.
- **MOD-EML-14** A notice for 26AB-CV00123-01 never files to a matter numbered 26AB-CV00123, by field or by name.
- **MOD-EML-15** A notice whose number matches but whose party text lacks the client's last name queues with case_number_matched_but_party_name_mismatch.
- **MOD-EML-16** A court notice matching a closed matter by exact case number files; client mail never auto-files to a closed matter.
- **MOD-EML-17** With the AI pick on, a fake answer naming a candidate files by AI; one naming a non-candidate files nothing; a "none" isn't asked again on re-match.
- **MOD-EML-18** A reply in a thread filed to 2026-0042 files there even from an address with no contact.
- **MOD-EML-19** Mail whose only candidate is a walled matter queues, and its row shows no matter name to a user off the wall.
- **MOD-EML-20** One message in a personal and a filing account files once; the second copy is a duplicate.
- **MOD-EML-21** A message with two attachments and one inline image makes three core files, two attachment links from the HTML copy, and one note tagged email and email-in.
- **MOD-EML-22** The HTML copy has no script, no event-handler attribute and no remote src, and keeps every link.
- **MOD-EML-23** A filing interrupted after the second upload and resumed ends with exactly three files and one note.
- **MOD-EML-24** A filing account's message is deleted at the provider only after the note is written; a court account's is marked read and moved to Filed, never deleted; a personal account's gets the Filed label and stays.
- **MOD-EML-25** When the raw message is gone at filing time, the row stays queued with message_not_in_mailbox and offers File without attachments, which files the stored body and names the missing attachments in the note.
- **MOD-EML-26** An outbound message filed from a personal Sent folder makes files with origin firm and a note tagged email-out.
- **MOD-EML-27** include=email.mail on a matter returns its filed count and last mail time to a viewer on the matter and not found to a viewer off its wall.
- **MOD-EML-28** Filing from the queue as staff writes the files and note under that user and drops the row from the queue and badge.
- **MOD-EML-29** Dismiss without a reason is refused; dismissing a court row never deletes it at the provider.
- **MOD-EML-30** A deferred message stays deferred across three syncs and returns on Restore.
- **MOD-EML-31** Staff can't see, preview or file another user's personal unfiled row.
- **MOD-EML-32** The preview carries the script-free sandboxed Content Security Policy in the spec and no script.
- **MOD-EML-33** A matter.updated event giving a matter case number 26AB-CV00123 files the waiting notice within one re-match pass; the hourly sweep does the same with no event.
- **MOD-EML-34** Quick email to an invalid address returns 400 and sends nothing.
- **MOD-EML-35** Quick email on 2026-0042 with one matter file attached sends once, files one HTML copy and one note, and the sent attachment matches the file version's SHA-256.
- **MOD-EML-36** Quick email naming a matter the viewer is walled off from returns not found and sends nothing.
- **MOD-EML-37** A failed provider send records failed with the reason, writes no note and answers 502.
- **MOD-EML-38** A reply carries the original's Message-ID in In-Reply-To, and its sent copy files to the same matter by thread.
- **MOD-EML-39** An add-in request with a revoked device grant gets 401 and files nothing.
- **MOD-EML-40** Filing one item twice from the add-in makes one filing.
- **MOD-EML-41** The add-in suggests a walled matter only to a user on its wall.
- **MOD-EML-42** A signed system mail sends once; a replay returns the same id and sends nothing more.
- **MOD-EML-43** A system mail with a bad signature returns 401 and sends nothing.
- **MOD-EML-44** With no system mail account the module answers 503 not_configured, and the core then sends that mail by SMTP to a test listener.
- **MOD-EML-45** A sent system mail's row holds no body text.
- **MOD-EML-46** Email search as staff returns nothing filed to a matter walled off from them and no other user's personal mail.
- **MOD-EML-47** Two syncs inside one summary interval that file three messages send one summary listing three; a quiet interval sends none.
- **MOD-EML-48** The Inbox widget opens the Mail tab on its folder; the Unfiled email widget opens the queue filtered to its account.
- **MOD-EML-49** Install with mail:system, ai:use and modules:read declined succeeds and reports those three features unavailable; declining notes:write cancels the install.
- **MOD-EML-50** Hard-deleting a matter with a filing is refused naming the module; merging two matters moves filings, matches and matter_mail rows in the same transaction.
- **MOD-EML-51** Uninstall keeping data leaves mod_email locked and a reinstall picks up accounts and filings; deleting data drops the schema and the module's sealed secrets after the backup.

### 7.8 Not in this module

The firm's own mail server (a later module), portal messages (client portal), calls (Zoom), e-signature (lawpay-docuseal), mailbox administration beyond filing, push delivery (the module polls), court document download from e-filing links, Unified's archive database, native .eml filing, bulk mail.

## 8. Client portal and messages (`client-portal`)

### 8.1 Purpose

The only screens a client sees. A client signs in at the core's /login, lands in the portal, and works with their own matters: shared documents, uploads, invoices, trust balance, next appointment, messages with the firm, contact details, and payments and signatures when lawpay-docuseal is installed. Staff get a firm-wide Messages inbox, a Messages tab on each matter and a Portal access panel. A portal user is a core user with the client role linked to the client's contact; the module keeps no user, password or session table, and every client read runs through the core as that client.

### 8.2 Kept from Unified

- Staff grant: find or create the account, a one-time temporary password, a forced change at next sign-in, the send result shown to staff; never a magic link. Staff reset shows a temporary password once to relay by phone.
- Clients see matters where they're a client party, newest first; a closed matter stays 14 days, then "access ended" with the firm's phone and email.
- The remembered active matter; write paths never guess between several matters and show a picker first.
- Dashboard cards: new documents dot, unread messages, invoices, trust balance, replenishment banner, next appointment, documents awaiting signature.
- Shared documents only; "new" until viewed inline, a download doesn't clear it; Download all as a zip.
- Client upload rule for rule: streamed, 250 MB a file, 2 GB and 100 files per matter per 24 hours, 90 per 5 minutes per client, a content-sniffed type check with the xlsx exception, empty files refused, failures kept on screen.
- Only sent, viewed and paid invoices, marked viewed when the client opens one.
- Contact edits with history kept, attributed to the client.
- Threads scoped to a matter and one client contact, started by either side; staff start them only on open matters with a client party; active, archived or closed; staff attachments link matter files and share them, logging "sent to client" each time; client attachments only from files already visible to that client; read receipts both ways; real names on both sides; a staff message emails the client a portal link with no body; a client message notifies the matter team with no body.

### 8.3 Screens

- Client tabs (framed at /portal/client-portal/{tab}): Dashboard (client home), Documents, Invoices, Messages, Account, and the access-ended page. Nav also lists other modules' client tabs. Colors and labels come from the client portal appearance settings; phone width first.
- Staff: home tab Messages (firm-wide inbox by matter or contact), matter tab Messages, panel Portal access (Send portal access, Temporary password, Turn off, Turn on), widget Client messages, sidebar Messages in Communications with an unread count, quick create New message, search scope Messages.
- A client token on a staff page gets 404, and a staff token on a client page gets 404.

### 8.4 Payments and signing contract with lawpay-docuseal

The portal reads three optional published views of lawpay-docuseal (`invoice_payability`, `matter_payability`, `signature_requests`) with the client's context token and opens its `pay` and `sign` client tabs with `target="_top"`. When that module is missing or its scopes are declined, Pay, Make a deposit and Sign don't render and every page still works. The portal never decides an amount or a destination: Pay on an invoice passes the invoice id, a deposit passes the matter and any active replenishment request, and `return_to` is honored only under /portal/client-portal/. The invoice balance shown is always the core's; while a payment is pending or a trust deposit waits to be applied, the invoice reads "Payment processing". The view columns and rules are client-portal SPEC section 7, which lawpay-docuseal implements exactly.

### 8.5 Data

Schema `mod_client_portal`: message_threads (matter, client contact, subject, status), messages (one author user, author kind, body, read time, active or retracted; never deleted), message_attachments (core file ids), message_notifications (sent, failed or skipped); portal_preferences (extension of user: active matter), file_views (inline views for the "new" dot), client_uploads (extension of file; the 24-hour volume ledger), upload_windows (the 5-minute limiter). Published view `thread_summary` keyed to matter (active threads, unread from client, last message).

### 8.6 Permissions, events and settings

- **Required scopes:** matters:read, contacts:read, users:directory, files:read, files:write, invoices:read, trust:read, calendar:read, profile:read, profile:write, firm:read, events:write, notifications:write, mail:send, client_access:read, client_access:write. **Optional:** the three lawpay-docuseal view scopes.
- **Events:** subscribes to nothing; reads live on page load. Writes client_portal.message_sent, message_viewed and file_sent_to_client (client-visible), thread_status_changed and message_retracted; summaries never carry message bodies. Notification kinds `client-portal.client_message` (in-app and email) and `client-portal.client_upload` (in-app).
- **Settings:** largest upload (250 MB), MB per matter per 24 hours (2048), uploads per matter per 24 hours (100), uploads per client per 5 minutes (90), longest message (10,000 characters), email the client on a staff message (on), dashboard welcome text (empty).
- **Outside services:** none. Client mail goes through core system mail, so it reaches the email module when the admin routes system mail there.
- **Core update items:** CORE-01, CORE-03, CORE-04, CORE-05, CORE-08 to CORE-16, CORE-18, CORE-19, CORE-21.

### 8.7 Acceptance

Synthetic fixtures: Ann Admin, Avery Attorney (responsible on every matter), Sam Staff (assigned to M1), Cal Accountant; contacts Casey Client, Drew Other and Jordan Coclient, each with a client user; M1 open with Casey, M2 open with Drew, M3 open with Casey and Jordan, M4 walled to Avery with Casey.

- **MOD-POR-01** Casey's portal lists M1, M3 and M4, never M2; every page and endpoint naming M2 or anything on it, or a guessed id, answers 404 to Casey.
- **MOD-POR-02** On M3, Casey can't read, post to or attach to Jordan's thread, and Jordan can't reach Casey's.
- **MOD-POR-03** An unshared or attorney-only M1 file never appears in Casey's documents, attachment picker or zip, and can't be attached.
- **MOD-POR-04** Draft and void invoices never appear to Casey; a sent one does and turns viewed when opened.
- **MOD-POR-05** Fifteen days after M1 closes, M1 leaves Casey's portal and the core answers 404 on it; with no readable matter left, the dashboard shows the access-ended page with the firm's name, phone and email; reopening restores M1.
- **MOD-POR-06** Staff, attorney, admin and accountant users get 404 on client pages; a client gets 404 on every staff page and endpoint.
- **MOD-POR-07** A context token issued for another module is refused; an expired one answers 401, and the page gets a fresh one from its parent frame and retries once.
- **MOD-POR-08** M4's threads appear in no inbox, badge, widget or search result for Sam.
- **MOD-POR-09** Send portal access for a contact with no client user creates one, emails a temporary password and shows "Sent to {email}"; when mail fails it shows "Not sent" and keeps the button.
- **MOD-POR-10** Temporary password shows once to Sam; Casey's next sign-in lands on /portal/security and nothing else opens until a new password is set.
- **MOD-POR-11** Turn off stops Casey signing in and Turn on restores it, changing neither the contact nor the matters.
- **MOD-POR-12** The panel refuses access for a contact who isn't a client party on the matter and for a user who also holds the staff role; Cal sees no panel.
- **MOD-POR-13** With one readable matter the portal opens on it; with several and no saved choice, upload, New message and every Pay or deposit link show the matter picker first, and the choice is remembered at the next sign-in.
- **MOD-POR-14** The new-documents dot stays until Casey views each new shared file inline; a download alone doesn't clear it.
- **MOD-POR-15** The dashboard shows unread staff messages, the balance due on sent invoices, Casey's trust balance, an active replenishment request and the next client meeting, each read from the core.
- **MOD-POR-16** Casey changes their phone; the core keeps the old number with an end date and the timeline shows the change with Casey as actor.
- **MOD-POR-17** A PDF, JPEG, HEIC, DOCX and XLSX uploaded to M1 each become one core file with source client_upload, origin received, shared with the client and visible on the staff Files tab; the XLSX stays .xlsx.
- **MOD-POR-18** An .exe renamed .pdf answers 400 "That file has to be a PDF, Word, Excel, text, or image file." and a zero-byte file 400 "That file was empty."; neither leaves a file record.
- **MOD-POR-19** A file over the size limit answers 413; the upload that would pass 100 files or 2 GB on M1 in 24 hours answers 413 with the call-the-office message; the 91st upload in 5 minutes answers 429 with Retry-After; none leaves a file record.
- **MOD-POR-20** In a batch of five with one bad file, four land and the failure stays listed.
- **MOD-POR-21** Download all on M1 returns a zip of exactly the files Casey can see there.
- **MOD-POR-22** Sam starts a thread on M1 with Casey; one on a closed or prospect matter, or with Drew on M1, answers 400.
- **MOD-POR-23** Attaching an unshared M1 file shares it and writes one "sent to client" timeline row, and attaching it again writes another; an M2 or attorney-only file answers 400 and writes nothing.
- **MOD-POR-24** A staff message emails Casey a portal link with no body; with that setting off, no mail goes and the notification row reads skipped.
- **MOD-POR-25** Casey's message reaches Avery and Sam as a client_message notification, emailed by default, with no body.
- **MOD-POR-26** Casey opening a thread marks Sam's messages read and writes one client-visible viewed row; Sam opening it marks Casey's read and lowers the badge.
- **MOD-POR-27** A closed thread refuses new messages from either side with 409; an archived one leaves the staff inbox and stays readable.
- **MOD-POR-28** Sam retracts Sam's own message and Casey sees "Message removed"; Sam can't retract Casey's or Avery's.
- **MOD-POR-29** A word from an M1 thread finds it for Sam; a word from an M4 thread finds nothing for Sam.
- **MOD-POR-30** With lawpay-docuseal absent or its scopes declined, no Pay, deposit or Sign control renders and every page loads.
- **MOD-POR-31** With invoice_payability true for invoice I1, Pay opens the pay tab for I1 with its return path; with it false there's no Pay.
- **MOD-POR-32** A signature_requests row with Casey as signer awaiting signature shows Sign; Jordan's row on the same shared file shows nothing to Casey.
- **MOD-POR-33** The signed package installs, its migrations run in mod_client_portal, its database login can't read core tables, and a changed applied migration stops the update.
- **MOD-POR-34** Hard-deleting a contact, matter, user or file a module row points to is refused naming client-portal.
- **MOD-POR-35** include=client-portal.thread_summary on M1 returns the counts to Sam and is refused to a viewer who can't read M1.

### 8.8 Not in this module

Discovery, questionnaires and intake, Zoom (each adds its own client tabs), charging and signing (lawpay-docuseal), upload conversion (core previews and Doc Tender), group threads, message editing, staff removal of client messages, text messages, self sign-up.

## 9. LawPay and DocuSeal (`lawpay-docuseal`)

### 9.1 Purpose

Takes LawPay card and ACH payments into the firm's operating and trust accounts, issues full and partial refunds and reconciles LawPay against the core; sends fee agreements for e-signature through DocuSeal and files the signed copy on the matter. Money never moves outside the core: a charge becomes a core payment (destination trust posts the ledger deposit in the core), a refund becomes a core payment refund. Money is integer cents end to end.

### 9.2 Kept from Unified

- The LawPay gateway wrapper (charge, full or partial refund, read a transaction; errors with LawPay's message, code and status) and separate operating and trust accounts with a public key for hosted card fields.
- Client trust deposits with a server-decided amount and a 30-minute pay session (now a single-use pay intent), $1.00 to $50,000.00, prefilled from an active replenishment request, satisfying it on completion.
- Portal invoice payment lands in trust by default and is applied to the invoice by a deliberate staff act; setting `invoice_pay_destination` lets a firm choose operating.
- Charged but not recorded: the client sees the LawPay transaction id and is told to contact the firm; PAY- codes kept; now retried automatically, then an exception.
- Staff Take payment, now card or ACH, operating or trust, optional invoice.
- The LawPay webhook: always 200, raw payload kept before parsing, the transaction id and status found in several candidate fields, pending payments advanced, unknown transactions logged; now behind the core's inbound hook route with a URL token and a live transaction read before any write.
- The refund reconciliation plan in full: refunds detected before the completed or failed branch, so a refund payload carrying a charge id never completes a pending charge; every refund verified; fail closed; idempotent. Partial refunds now post, because the core refund record holds them. A refund made in the LawPay dashboard waits as an exception for an attorney to post.
- Conflict no-go: a proposed full refund of the consult fee on the matter's latest completed LawPay card payment, one click for the attorney.
- Fee agreement drafts with Unified's limits, billing type filled from the fee type only when empty, send in Unified's order with a Client submitter (prefilled read-only identity, DocuSeal email off) and a Firm submitter (DocuSeal email on), rollback on failure, the invite from the firm and a Resend warning on a failed invite.
- The DocuSeal webhook with a constant-time token check, client signed, countersigned and executed states, void and resend, and an orphan log; now with the executed PDF stored as a core file.
- Method labels "Credit card" and "Bank transfer (ACH)"; 3 card attempts per client per 10 minutes.

### 9.3 Screens

- Home tabs: Fee agreements and LawPay reconciliation (transactions by state and kind; every open exception has Resolve with a required note).
- Matter tab Payments and signing; panel Fee agreement; actions Send fee agreement, Take payment and Refund (attorney and admin only).
- Widgets: Fee agreements awaiting signature, and Payment and signing exceptions (admin and attorney).
- Sidebar LawPay in Money (icon payment, order 400).
- Client tabs `pay` and `sign`, kept out of the portal nav and opened only from the client portal's links.
- The settings page shows the two webhook URLs to copy into LawPay and DocuSeal and a Test connection button for each service.

### 9.4 Behavior

- **Charges:** each charge starts with core processor initialization, charges LawPay's operating or trust account, and records the core payment with the delivered grant. ACH that LawPay reports as authorized records a pending payment; a verified completion completes it once. The `pay` tab decides the amount on the server: an invoice's balance due read from the core, or a replenishment request's amount, or a client-entered deposit within the limits. A client can't reach another client's or another matter's invoice, deposit or request.
- **Refunds:** core refund pending, LawPay refund, core refund completed. A refund over what remains is refused before any call. A trust refund that takes a client below zero shows the resulting balance, still posts, and raises the core's trust flag; the module never blocks it.
- **Reconciliation:** a sweep every `reconcile_interval_minutes` retries unrecorded charges and refunds with the same idempotency keys, advances pending ACH younger than `pending_check_days`, and opens an exception for anything that needs a person (charged_unrecorded, external_refund, unknown_transaction, account_mismatch, amount_mismatch, refund_unposted, manual_refund_needed, signed_pdf_failed, docuseal_orphan, signing_error), notifying the admins and the responsible attorney.
- **Fee agreements:** Send needs complete client contact details and active portal access, files the unsigned agreement as a core file shared with the client, sets the core agreement sent, and mails the invite through core mail with a link to the portal's sign tab. On `submission.completed`, confirmed by a submission read, the signed PDF becomes a core file marked executed and the core agreement moves to signed (CORE-25). Optional expiry by `signing_expire_days`.
- **Views** for the client portal (`invoice_payability`, `matter_payability`, `signature_requests`) are built from module rows plus a one-row module state, so they answer not_configured without reading settings, and carry no amount the core owns and no signing URL.

### 9.5 Data

Schema `mod_lawpay_docuseal`: received_events, processor_grants, lawpay_charges (state machine from intent to recorded or failed), lawpay_refunds, lawpay_webhook_receipts (deduped by body hash), exceptions (one open row per kind and record); agreement_details (extension of fee agreement: case number, new case), signings (one live signing per agreement), docuseal_webhook_receipts; module_state, payable_invoices, matter_deposits, and the three published views. No names, emails, phones or addresses are stored; contact details are read from the core at send time.

### 9.6 Permissions, events and settings

- **Required scopes:** matters:read, matters:write, contacts:read, invoices:read, payments:read, payments:write, trust:read, trust:write, trust:withdraw (the core still requires an attorney or admin), files:read, files:write, events:read, events:write, notifications:write, conflicts:read, mail:send, books:read, client_access:read. Manifest also declares `payment_processor` (operating and trust) and inbound webhooks `lawpay` and `docuseal`.
- **Events:** invoice.sent, invoice.updated, invoice.voided, payment.completed, payment.reversed, payment.processor_authorized, payment.refund_authorized, fee_agreement.updated, conflict_check.decided, trust.replenishment_requested, file.background_authorized. Notification kinds `lawpay-docuseal.exception` and `lawpay-docuseal.refund_proposed` (in-app and email).
- **Settings:** LawPay secret key (secret), public key, operating account id, trust account id, the Togavsu trust account for LawPay deposits, API address (default https://api.8am.com/v1), webhook URL token (secret); smallest charge (100 cents), largest charge (5,000,000 cents); invoice payment destination (trust); pay page minutes (30); card attempts per window (3) and window minutes (10); sweep minutes (60); retries before an exception (3); pending ACH check days (14); DocuSeal address, API key (secret), webhook token (secret), fee agreement template id, firm signer name and email; days before an unsigned agreement expires (0, never).
- **Outside service adapters:** LawPay (charge, refund, read transaction) and DocuSeal (create submission, read submission, current PDF, completed PDF, archive) interfaces with HTTP adapters built only from settings at runtime, a table-driven status map, and in-memory fakes with scripted outcomes (decline, network error, 404, delayed settlement, dashboard refund, client declines, expired submission) that generate webhook bodies. Card data never touches the module; hosted fields return a token, and fakes use placeholder tokens.
- **Core update items:** CORE-01 to CORE-04, CORE-07 to CORE-10, CORE-12, CORE-14, CORE-22 to CORE-25.

### 9.7 Acceptance

Each criterion runs against Postgres and the fake adapters with synthetic records.

- **MOD-PAY-01** With a required setting empty, pay pages, Take payment, Refund and Send say the service isn't set up, link to settings and make no adapter call.
- **MOD-PAY-02** An admin reading module settings sees every secret redacted; no secret appears in a page, log line, receipt or exception.
- **MOD-PAY-03** A Togavsu trust account id the core doesn't return as active blocks trust charges and opens account_mismatch with a notification; operating charges keep working.
- **MOD-PAY-04** Staff Take payment of 25,000 cents to operating makes one initialization, one LawPay charge on the operating account, one completed core card payment with the LawPay charge id as reference and one recorded charge row.
- **MOD-PAY-05** The same to trust charges the trust account and makes a trust-destination core payment on the configured trust account with exactly one core trust deposit.
- **MOD-PAY-06** An ACH charge reported authorized records a pending payment and no trust row; a verified completion completes it and posts one trust row; a second completion posts nothing.
- **MOD-PAY-07** An amount outside the charge limits is refused before any core or adapter call.
- **MOD-PAY-08** A declined card leaves no core payment, a declined charge row with a PAY-CHRG code, and shows "Card declined".
- **MOD-PAY-09** Submitting one pay intent twice charges once and returns the first result; an expired intent charges nothing.
- **MOD-PAY-10** A client's fourth card attempt inside the window answers 429 with no adapter call.
- **MOD-PAY-11** A trust deposit on the pay tab with an active 50,000-cent replenishment request charges exactly 50,000 with no amount field and satisfies the request; without one the client enters an amount, and one below the minimum is refused before any call.
- **MOD-PAY-12** With invoice payments going to trust, a client invoice payment is a trust deposit with no invoice on the core payment, the invoice balance is unchanged and the charge row names the invoice; set to operating, the core payment names the invoice and its balance drops.
- **MOD-PAY-13** A client can't pay another matter's deposit or another client's invoice, or use another client's or matter's replenishment request; each answers 404 with no core or adapter call.
- **MOD-PAY-14** When the core payment write fails after a successful charge, the payer sees the LawPay transaction id and the contact-the-firm message; the sweep retries with the same key; after the retry limit one charged_unrecorded exception opens and one notification goes to the admins and the responsible attorney.
- **MOD-PAY-15** A LawPay webhook with a missing or wrong token gets 401 and writes nothing, not even a receipt.
- **MOD-PAY-16** Malformed JSON with a good token is stored as malformed and answers 200.
- **MOD-PAY-17** A duplicate delivery raises the duplicate count, answers 200 and changes nothing else.
- **MOD-PAY-18** A webhook naming a transaction LawPay doesn't know changes nothing, is unverified and answers 200.
- **MOD-PAY-19** A REFUND COMPLETED body carrying a pending charge's id leaves the charge and its core payment pending.
- **MOD-PAY-20** A verified charge on an account matching neither setting, or disagreeing with the row's destination, posts nothing and opens account_mismatch.
- **MOD-PAY-21** A verified charge with no charge row opens unknown_transaction; an attorney picks matter and contact and records it with the verified amount and method and the destination from the account id; the exception resolves.
- **MOD-PAY-22** The module answers health checks inside the stack, and the public hook route takes POST only.
- **MOD-PAY-23** An attorney's partial refund of 4,000 on a 10,000 operating payment runs core pending, LawPay refund, core completed, leaving refunded_cents 4,000, refund_state partial and the invoice's amount paid 4,000 lower.
- **MOD-PAY-24** A second refund of 6,000 makes it full; one more cent is refused by the module and by the core with refund_exceeds_payment and no adapter call.
- **MOD-PAY-25** A 15,000 refund on a 10,000 payment is refused before any core write or adapter call.
- **MOD-PAY-26** A 30,000 trust refund when the client's matter balance is 10,000 warns "This leaves the client at -$200.00 in trust. It will post and raise a trust flag." and still submits; the core posts the refund row, raises one flag and emails the admins and the responsible attorney.
- **MOD-PAY-27** Staff and accountant users don't see Refund, and a direct call is refused by the core.
- **MOD-PAY-28** A LawPay refund failure marks the core refund and module row failed and shows LawPay's message; nothing posts.
- **MOD-PAY-29** When LawPay refunds and the core completion fails, the sweep completes it with the refund grant, and the later webhook matches the stored refund id and does nothing.
- **MOD-PAY-30** A verified dashboard refund opens external_refund and notifies; an attorney's Post refund creates a completed core refund with LawPay's refund id, partial when partial.
- **MOD-PAY-31** A no-go conflict decision on a matter whose latest completed LawPay payment is a 17,500 card charge proposes one 17,500 refund with one notification; a replayed event adds nothing; Issue refund runs the MOD-PAY-23 sequence; a non-LawPay latest payment opens manual_refund_needed.
- **MOD-PAY-32** The sweep advances every pending ACH charge younger than the check window; an older one opens an exception.
- **MOD-PAY-33** The reconciliation tab lists transactions with matter, amount, destination, state and core payment link, filtered by state and kind; Resolve needs a note.
- **MOD-PAY-34** Saving a draft creates the core fee agreement and its details row; an amount at or below zero or above 99,999,999 cents, or an empty or over-4,000-character services description, is refused; a closed matter's panel is read-only.
- **MOD-PAY-35** A flat 150,000 draft on a matter with no billing type sets flat and the flat fee; an hourly matter stays hourly; an existing flat fee is kept.
- **MOD-PAY-36** Send with missing or malformed client email, phone, street, city, two-letter state or ZIP answers 422 listing the fields; Send for a client without active portal access answers 422 "Give the client portal access first; they sign in the portal."; neither calls anything.
- **MOD-PAY-37** Send makes one DocuSeal submission with the Client and Firm submitters and fields the spec names, stores the ids, files the agreement as sent as one core file shared with the client, sets the core agreement sent, mails one invite through core mail linking the portal's sign tab, and writes a client-visible "Fee agreement sent for signature".
- **MOD-PAY-38** Two Send clicks at once make one submission; the second gets 409.
- **MOD-PAY-39** When storing ids or filing the unsigned copy fails, the submission is archived and the agreement returns to draft; when archiving fails too, the signing is error with the orphan id and an exception opens.
- **MOD-PAY-40** A failed invite leaves the agreement sent with "Use Resend signing link"; Resend sends the same link with no new submission.
- **MOD-PAY-41** Void from sent or client signed archives the submission, sets the core status void with the reason and sends the void notice; a client completion during voiding is stamped and shown.
- **MOD-PAY-42** A DocuSeal webhook with a missing or wrong token gets 401 and writes nothing.
- **MOD-PAY-43** The client's completion moves sent to client signed; the firm's stamps countersigned; a duplicate changes nothing.
- **MOD-PAY-44** A confirmed submission completion stores the PDF once as a core file on the matter, shared and executed, sets the core agreement signed with the completion date in the firm's time zone, and writes a client-visible "Fee agreement executed"; two concurrent deliveries make one file.
- **MOD-PAY-45** A completion arriving while still sent proceeds once DocuSeal shows both submitters completed.
- **MOD-PAY-46** A PDF download failure keeps the signing executed, retries on the sweep, and after the retry limit opens signed_pdf_failed with a notification.
- **MOD-PAY-47** Unknown submissions are orphans and events on finished signings are terminal, both 200; an internal error answers 500 so DocuSeal retries.
- **MOD-PAY-48** invoice_payability reads payable after invoice.sent; in_progress with the amount during a pending ACH charge; in_progress (funded) after a full trust charge until staff reopen online payment or the core balance reaches zero (zero_balance); not_configured with a LawPay setting empty; no row after invoice.voided; nothing for another client's invoice.
- **MOD-PAY-49** signature_requests reads awaiting_client, client_signed, then completed, and void while voiding or voided; an unshared file gives a client no row; the sign tab opens DocuSeal in a new window for the signer and 404 for another client.
- **MOD-PAY-50** Reading a view without its approved view scope is refused.
- **MOD-PAY-51** The awaiting-signature widget counts sent and client-signed agreements on matters the viewer can see, leaves out walled matters, and opens the agreements tab filtered by state.
- **MOD-PAY-52** matter_payability enables deposits after a matter's first replenishment request with the minimum equal to the smallest charge; it's off after staff turn deposits off and while the trust account check fails; a matter with no row gets no Make a deposit.
- **MOD-PAY-53** A client decline moves the signing to declined, opens signing_error and notifies the responsible attorney; with a 30-day expiry the submission carries it and a verified expiry moves it to expired; with 0 there's no expiry.

### 9.8 Not in this module

Pre-matter consult fees and the no-go client notice (intake), chargeback disputes inside LawPay (they land as external refunds), check refunds (core trust refund), saved cards, recurring charges, surcharges and payment plans, building the DocuSeal template, signing anything but the fee agreement, ledger sync (quickbooks).

## 10. QuickBooks Online sync (`quickbooks`)

### 10.1 Purpose

One-way push from Togavsu, the system of record, to QuickBooks Online for the firm's accountant: billed contacts as Customers, sent invoices as Invoices, write-offs as CreditMemos, completed operating payments as Payments, reversals as voids, every trust ledger entry as a JournalEntry between a mapped trust bank account and the client trust liability account, and expenses as Purchases when the admin picks that. Trust is never income. Pushes run on core events within seconds, plus a nightly reconcile. The module never writes a Togavsu money record; its only core writes are notifications and its own sealed secrets.

### 10.2 Walls

The books reader honors matter walls (CORE-02). A walled matter's invoices, payments, expenses and trust entries, the matter itself and its client linkage never reach the module and are never pushed to QuickBooks; a contact whose every matter is walled gets no Customer. A record synced before its matter was walled stays as it is in QuickBooks, moves to hidden at the next read, leaves every count, and gets no call while the wall stands. When the wall lifts, the records sync on the next nightly reconcile or Sync now; an unchanged record returns to synced with no QuickBooks call. No screen, widget, count or alert shows anything about a walled matter to any viewer.

### 10.3 Screens

- Home tab QuickBooks (admin and accountant): status line, exceptions with the QuickBooks fault text, attempts, Retry, and Re-push or Acknowledge for drift, and a read-only books view by month with Togavsu and QuickBooks totals.
- Home tab QuickBooks setup (admin acts, accountant reads): redirect URI to register, Connect (its own top-level window) and Disconnect, the client secret field, every mapping with the account or item it resolved to and whether its type fits, the QuickBooks accounts and items with their Ids, the trust bank map per Togavsu trust account, Create missing accounts and items (never a bank account), and Reset sync (typed RESET).
- Matter tab QuickBooks (admin, attorney, staff): the matter's invoices, payments and trust entries with their QuickBooks state.
- Widget QuickBooks sync (admin and accountant): connection state, items synced today, waiting and exceptions, each linking to the books tab filtered by status. Sidebar QuickBooks in Money (icon ledger, order 410). A Sandbox company banner on every page in sandbox.
- Retry, Re-push, Acknowledge and Sync now need admin or accountant and change only module rows; Connect, Disconnect, Create missing and Reset are admin only.

### 10.4 Behavior

- **Mapping:** invoices post with DocNumber, issue date, one line per invoice line by item (legal services, flat fee, reimbursed costs, adjustment), invoice adjustment and interest lines, and lines that must equal the total to the cent; EmailStatus NotSet and no online payment, so QuickBooks never contacts a client. Operating payments link to the synced invoice and deposit to Undeposited Funds or the operating bank; trust-destination payments are skipped because their ledger row posts. Trust deposits, withdrawals, refunds and adjustments post two-line JournalEntries; a transfer to operating that pays an invoice posts the trust side plus the linked operating Payment; one that pays no invoice posts four lines with the earned-from-trust income account; a reversal posts the mirror. Write-offs post a CreditMemo and a zero Payment applying it. Every entity carries a Togavsu marker note. No sales tax.
- **Push:** a verified event marks the record's row pending; one worker pushes due rows in dependency order (customers, invoices, write-offs, payments, trust entries, expenses, voids), pushes only when the payload hash changed, reuses a stored request id on retry so a timeout never makes a second entity, and handles stale SyncTokens with one read. Retryable faults retry on the retry schedule setting, then fail; non-retryable faults fail at once.
- **Reconcile** nightly at `reconcile_hour_utc` and on Sync now: a full read of every record through the books reader (not a change cursor), a drift check of QuickBooks changes since the last run, and a trust totals check per account.
- **Customers:** an existing QuickBooks customer with the exact name and no other Togavsu marker is adopted; a name taken by a vendor or employee gets an id suffix.
- **Connection:** Intuit OAuth with a one-use state bound to the admin; tokens and the client secret only in the core secret store; refresh under 5 minutes left; invalid_grant pauses pushes and alerts admins at once; a reconnect reminder 14 days before the refresh token expires; a different company is refused until Reset sync.
- **Alerts:** failed and drift rows are exceptions; new exceptions send one `quickbooks.sync_problem` notification to admins and accountants per alert interval, emailed by default; connection loss alerts at once.
- **Tunables:** the retry schedule (default 1 minute, 5 minutes, 30 minutes, 2 hours, 6 hours) and the alert interval (default 15 minutes) are module settings. Intuit's request ceiling (the module holds each company to 400 requests a minute and 4 at once), the worker tick (5 seconds) and the OAuth state lifetime (10 minutes) stay fixed constants.

### 10.5 Data

Schema `mod_quickbooks`: connection (one row; company and token expiry times, never a token), oauth_states, received_events, trust_bank_map (keyed to core trust accounts); customer_sync, invoice_sync (extension of invoice), payment_sync (extension of payment), trust_entry_sync, expense_sync, each with the same state columns (pending, synced, skipped, failed, drift, acknowledged, hidden; QuickBooks id and SyncToken; payload hash; request id; attempts; errors); sync_log (append-only, one core record per row), runs, alerts_sent. Published view `invoice_qbo` (state and synced time) under `views:quickbooks.invoice_qbo:read`.

### 10.6 Permissions, events and settings

- **Required scopes:** invoices:read, payments:read, expenses:read, trust:read, contacts:read, matters:read, events:read, books:read, notifications:write, module_secrets:write. Declining books:read cancels the install.
- **Events:** invoice.*, payment.*, expense.*, trust.entry_posted, contact.updated. Notification kind `quickbooks.sync_problem` (in-app and email).
- **Settings:** Intuit client id; environment (sandbox); sync start date (empty means the first connection day); operating bank; default trust bank; trust liability (Client Trust Liability); earned from trust (Legal Fee Income); payment deposit (Undeposited Funds); expense push (invoice lines only); client costs (Client Costs Advanced) and paid-from account for purchases mode; items Legal Services, Flat Fee, Reimbursed Costs, Invoice Adjustment, Late Interest and Write-Off; reconcile hour (8 UTC); retry schedule; alert interval (15 minutes). The client secret is entered on the setup tab and stored in the core secret store.
- **Outside service adapter:** Auth and Books interfaces with an HTTP adapter for Intuit (minorversion 75, request ids, change data capture, client-side limiter, base URLs as config) and an in-memory fake that enforces unique names, SyncToken concurrency, balanced JournalEntries, void rules and request id replay, and can fail, throttle, time out after applying a write, revoke the refresh token and edit or delete as the accountant. No test calls Intuit.
- **Core update items:** CORE-01 to CORE-03, CORE-06, CORE-08, CORE-09, CORE-22.

### 10.7 Acceptance

All against the fake and synthetic records: firm Sample Law Co, client Jane Sample, matter 2026-0042.

- **MOD-QBO-01** The signed package installs, shows the ten scopes with reasons, and records migrations 0001 to 0003 in mod_quickbooks; declining books:read cancels the install.
- **MOD-QBO-02** The install check accepts every migration; every core link is a RESTRICT foreign key, and hard-deleting a synced contact is refused naming the module.
- **MOD-QBO-03** The widget targets the books tab and its counts link to synced, pending and exceptions.
- **MOD-QBO-04** OAuth against the fake stores tokens only in the core secret store; no module column, log line or response holds a token or the client secret; Connect opens its own window; a reused or expired state is refused; a non-admin can't start a connection.
- **MOD-QBO-05** An expired access token refreshes before the next push and the rotated refresh token is stored; invalid_grant sets reconnect needed, pauses pushes and alerts admins at once.
- **MOD-QBO-06** A sent invoice with time, expense, flat fee and adjustment lines, an invoice adjustment and interest posts one QuickBooks Invoice matching to the cent, with the invoice number and EmailStatus NotSet.
- **MOD-QBO-07** A draft invoice never posts; an invoice dated before the start date is skipped with a reason.
- **MOD-QBO-08** A 150,000-cent trust deposit posts a JournalEntry debiting the trust bank and crediting the trust liability with Jane Sample as entity; no trust JournalEntry line touches an income account except the four-line shape's earned line, and income balances don't move.
- **MOD-QBO-09** A trust-destination payment is skipped and only its ledger deposit posts.
- **MOD-QBO-10** Apply trust of 50,000 to a sent invoice posts a two-line trust JournalEntry and a 50,000 Payment to the operating bank applied to the invoice, lowering its QuickBooks balance by 50,000.
- **MOD-QBO-11** A flat fee apply posts the same pair against its flat-fee invoice.
- **MOD-QBO-12** A transfer to operating with no linked payment posts the four-line JournalEntry.
- **MOD-QBO-13** Withdrawal, refund, positive and negative adjustment and reversal each post their shape; a reversal mirrors the original exactly.
- **MOD-QBO-14** A below-zero withdrawal posts normally.
- **MOD-QBO-15** A completed operating payment posts to the configured deposit account linked to its invoice; one whose invoice isn't synced waits and posts after it.
- **MOD-QBO-16** A reversed payment voids its QuickBooks Payment; one never synced is skipped.
- **MOD-QBO-17** A voided invoice is voided in QuickBooks only after its payments are voided, with any write-off credit memo and link deleted first; a draft voided before sync makes no call.
- **MOD-QBO-18** A 12,500 write-off posts a CreditMemo and a zero Payment applying it; raising it to 20,000 updates both; the QuickBooks balance equals Togavsu's.
- **MOD-QBO-19** One event delivered twice is recorded once and makes one create; two events for one record make one push.
- **MOD-QBO-20** A 503 retries on schedule and succeeds; five 503s fail the row and send one sync_problem notification to each admin and accountant, emailed by default; the accountant's Retry posts it with the accountant logged as actor.
- **MOD-QBO-21** A timeout after QuickBooks applied a write retries with the same request id and leaves one invoice.
- **MOD-QBO-22** Trust liability mapped to an income account fails every trust entry with mapping_invalid and posts nothing; the rows retry once the setting is fixed.
- **MOD-QBO-23** The module holds no write scope but notifications and its own secrets; with its token or an accountant's context token, payment, trust and invoice writes are refused and core rows don't change; Retry, Re-push and Acknowledge change only module rows.
- **MOD-QBO-24** Staff and attorney context tokens can't Retry, Acknowledge, Sync now or Reset; only an admin can Connect, Disconnect, Create missing and Reset.
- **MOD-QBO-25** The nightly reconcile pushes a record whose event never arrived; a second run with no changes writes nothing to QuickBooks.
- **MOD-QBO-26** An invoice edited in QuickBooks by the accountant becomes drift with an exception; Re-push restores Togavsu's version; Acknowledge silences it until the Togavsu invoice changes.
- **MOD-QBO-27** The first push creates the customer; a same-named customer with no other marker is adopted; a name taken by a vendor gets the id suffix; contact.updated updates name and address.
- **MOD-QBO-28** Matter 2026-0043, walled to one attorney with a client on no other matter: its sent invoice, operating payment and trust deposit never reach the module (left out of lists, not_found on gets and on the books matter and contact reads, absent from trust balances, no event delivered, no module row) and after a reconcile the fake holds no entity and recorded no call for them; no screen, widget or alert shows them to an admin, the accountant or the attorney on the wall. An invoice on 2026-0042 synced before an admin walls that matter moves to hidden at the next reconcile, leaves every count, and gets no call when it changes. Lifting both walls and pressing Sync now pushes the 2026-0043 customer, invoice, payment and deposit whole and updates the 2026-0042 invoice.
- **MOD-QBO-29** Purchases mode posts each expense once as a Purchase to the client costs account; invoice-lines mode posts none.
- **MOD-QBO-30** In sandbox the adapter uses the sandbox address and every page shows the banner.
- **MOD-QBO-31** A different company is refused until Reset sync with RESET, which clears the sync tables and keeps the log.
- **MOD-QBO-32** include=quickbooks.invoice_sync returns the sync state to an accountant and not_found on a walled matter the viewer is off.
- **MOD-QBO-33** A JournalEntry altered in the fake raises a totals exception naming the trust account and the difference.
- **MOD-QBO-34** The retry schedule and alert interval are module settings on the admin page with the defaults above; changing the retry schedule changes the next retry's time with no restart.

### 10.8 Not in this module

Any write from QuickBooks back into Togavsu or any Intuit webhook (a later version would use CORE-07), opening balances before the start date, bank feeds and deposits grouping, overhead expenses, payroll, vendors and bills, sales tax, multicurrency, classes, locations and sub-customers, emailing invoices or taking payments through QuickBooks, QuickBooks Desktop, time entries on their own.

## 11. Marketplace submission and listing

### 11.1 Signing

- Each module image is built by the release workflow, pushed to ghcr.io/charitonmedia/togavsu-<id> and signed with Cosign using the same release signing path as the core images; its digest goes into the manifest before the package is signed.
- Each package (manifest, migrations and help, with the image digest) is signed through the marketplace signing path matter-clock uses (marketplace.Sign, SignedPackage, KeyID) by `<id> package -key <key file> -out <id>.signed.json`, with the Chariton Media author key.
- The Chariton Media author key is an Ed25519 key kept as a GitHub repository secret beside the Togavsu release key. It is added beside the release key before the sign, publish and list stage (12.1 step 5), and its public key is registered on the Chariton Media author account on the marketplace. No module lane sees the private key; only the release workflow signs.
- A tampered or unsigned package is refused by the core at install.

### 11.2 Submission

- The release workflow uploads each signed package to the Togavsu marketplace service (core PRD 10.6) under the Chariton Media author account; the marketplace checks the signature against the registered key on upload.
- Each signed package file is also attached to the GitHub release for its module tag (`<id>-v1.0.0`), so a firm can install it from a local file.

### 11.3 Listing

Each listing shows the author, the author's verified signing key, every permission the module requests with its reason, the version history, the price, the core version it needs (the core with the module API update), and the core PRD 10.2 notice: "Modules are published by their authors. The Togavsu marketplace does not review, test or warrant any module. Install at your own risk."

- **Author:** Chariton Media.
- **Price:** free.

### 11.4 Install

- A firm admin installs from the marketplace or from the local signed file; both are allowed for every module, including after a flag or pull (core PRD 10.3 to 10.5).
- The core verifies the signature, shows every requested permission with its reason, and the admin approves or declines; declining a required scope cancels the install, and declining an optional one installs with that feature marked unavailable.
- The admin then enters the module's keys and sign-ins on its settings form or setup tab at runtime. Nothing is preset.
- A core without the module API update refuses the package, because its manifest decoder refuses the new fields; the listing names the core version needed.
- Install, update, disable and remove never restart the core.

## 12. Build plan

### 12.1 Order

1. **Control change** (integration lead), in two parts. The first puts the five specs and CORE-CHANGES.md on main under docs/modules/, this PRD at the repo root beside the core PRD, and the core PRD 9.10 pointer. The second, once the first has merged, pins the email libraries in go.mod, gives docs/build/tasks.json and the controller plan the assignments below, and has the controller and review gate read this PRD for module tasks. The Chariton Media author key is added before step 5 (11.1).
2. **Module API update** (one module-core lane): CORE-01 to CORE-29 in the order of 4.4, merged to main, released as a signed core image, and applied through the overnight updater. Done when CU-01 to CU-05 pass.
3. **Five module builds in parallel**, one per module repository, started Nov 14, 2026 beside the core release lanes: documents, email, client-portal, lawpay-docuseal and quickbooks. Each builds and tests against a fake core that serves the contract plus the CORE items it uses, then runs its acceptance against the released core once step 2 lands. None depends on another; client-portal tests the lawpay-docuseal views against a signed test fixture module that never ships.
4. **Integrate modules** (integration lead): all five signed packages, built from their repositories, on one released core, the cross-module checks in section 13, the guide pages, and the fresh-install checks.
5. **Sign, publish and list** (each module repository's release workflow): images, packages, module tags, marketplace upload (section 11).

The build controller in this repository runs steps 1 and 2. Steps 3 to 5 run outside it, in the module repositories and by the integration lead, under the same rules: Codex review on every PR, three fix passes then a report to Patrick, and an email to Patrick whenever a build stops.

### 12.2 How it runs

- The build runs the way the core build runs: the build controller (scripts/build_controller.py), lane agents on GitHub-hosted runners or Cursor-hosted VMs, GitHub as the canonical source, docs/build/WORKFLOW.md as the procedure. Lane agents use sub-agents wherever their work splits, so each lane's context stays clear. No phase waits for approval once this PRD is approved.
- Nothing builds, tests or installs on Patrick's machines. Render-1 holds only the control checkout and launches cloud jobs.
- Each lane writes only its owned paths (section 5). The module-core lane never edits a module suite file, and module lanes never edit core packages, .github/, go.mod or protected control files. A lane that needs another dependency proposes it in its PR for the integration lead.

### 12.3 Tests

- Every test is Go and runs in the module repository's CI against real Postgres, with generated synthetic data only. No client data is used anywhere.
- Each module has unit tests and one Postgres acceptance suite in its repository. Until the module API update is released the suite runs against a fake core serving the contract plus the CORE items the module uses; after it, against the released core image. The suite builds and signs the package with a generated test key, trusts it, installs it on the composed core, approves its scopes and drives every MOD criterion with users of every role, a walled matter, and context, device and agent tokens. Failure paths are part of each criterion.
- Outside services: fake adapters and local test servers only. LawPay and DocuSeal use in-memory fakes; QuickBooks uses its fake and an httptest server; Gmail and Graph use httptest stand-ins of their wire formats; IMAP and SMTP use the go-imap v2 in-memory server and a local SMTP listener on 127.0.0.1. Test helpers refuse any other host, live adapters are built only from settings at runtime, and no test or CI job reaches a real service.
- Keys and sign-ins are entered by the firm admin at runtime. The build creates no service credential; tests use generated keys and placeholder tokens that work only against the fakes.

### 12.4 Review gate

- Every PR gets the Codex checkpoint at its exact base and head SHAs. A missing, failed, stale or incomplete review blocks the merge, and every new commit needs another review. The reviewer doesn't author the code it approves.
- The integration lead merges only after every required check passes and every finding is resolved, under strict branch protection.
- Each stage ends at an automated checkpoint: executable tests pass, the integrated revision is reviewed, the simplification review runs (core PRD section 11, with PONYTAIL-DEBT.md updated), and the reporter files its report.
- **Fix passes:** limit passes to fix the same issue to 3, then raise it with Patrick. The build controller emails Patrick at the stall-alert address, naming the issue, the three attempts and what's blocking, and the reporter posts the same on the tracking issue. Work that doesn't depend on that issue keeps going.

### 12.5 Reporter

After each stage the reporter posts on the GitHub tracking issue and the build controller emails Patrick at the stall-alert address: what merged, the criteria that passed, anything that failed and how it was fixed, the simplification result and the debt count. A final report closes the build with every module's listing link and signed package.

## 13. Acceptance

The build is done when all of this is true and working in production:

- The module API update is merged, released as a signed core image, and passes CU-01 to CU-05.
- Each module passes every one of its MOD criteria in automated tests against Postgres: MOD-DOC-01 to 39, MOD-EML-01 to 51, MOD-POR-01 to 35, MOD-PAY-01 to 53, MOD-QBO-01 to 34.
- Each module is signed (Cosign image, signed package), published, tagged and listed in the Togavsu marketplace with author Chariton Media, price free, its permissions and the no-warranty notice.
- Each module installs from the marketplace, and from its local signed file, onto a fresh install of the released core with no other module installed, and uninstalls, with no core restart.

Shared criteria, run once all five are merged:

- **MOD-ALL-01** All five install together on one core; disabling or removing any one leaves the other four and the core working.
- **MOD-ALL-02** Every action on every module screen has a JSON endpoint under /m/<id>/api/ that an agent reaches with a context token from its bearer token, and each module serves its OpenAPI document.
- **MOD-ALL-03** Every module's guide pages appear under its own heading in /help and in search after install, open from the module's ? icons, and disappear after uninstall.
- **MOD-ALL-04** No module table, log line, page, event or response holds a password, OAuth token, API key or client secret in any test run.
- **MOD-ALL-05** A staff user outside a wall gets nothing from a walled matter from any module: no row, count, badge, search hit, widget line or notification.

Cross-module criteria, with the real modules in place of test fixtures:

- **MOD-INT-01** With client-portal and lawpay-docuseal installed, a client pays an invoice from the portal: the pay tab charges the fake LawPay, the core records the payment, and the portal shows "Payment processing" until the invoice is settled; a fee agreement sent from the matter shows Sign in the portal and, once completed, the executed PDF on the matter.
- **MOD-INT-02** With email installed and system mail routed through it, a client portal staff-message notice, a client access grant and a fee agreement invite each go out through the email module's system mail account; with the email module disabled they go out by SMTP.
- **MOD-INT-03** With quickbooks and lawpay-docuseal installed, a LawPay trust deposit posts to QuickBooks as one trust JournalEntry and its partial refund as a second; an operating LawPay payment posts as a QuickBooks Payment linked to its invoice.
- **MOD-INT-04** A message filed by the email module and a draft generated by the documents module each go through Doc Tender within 10 minutes and come out named, categorized and filed.
