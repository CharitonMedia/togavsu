# Togavsu core PRD

Nov 6, 2026 · @Patrick

Status: approved scope, ready to build. Every decision below is final for this build. Nothing in this document waits on Patrick.

Amended Nov 12, 2026: Patrick held release so the first five modules are built first (section 9.10).

## 1. What we're building

Togavsu is a ground-up practice management system for solo and small law firms. This PRD covers the core only: the minimum usable practice management, plus the module interface everything else plugs into.

The core ships as signed Docker images that a firm installs with Docker Desktop. It's free to download, self-install and modify. Togavsu is source-available, not open source.

Core scope, in one line: contacts, matters, time with invoicing, trust balances, calendar and deadlines, files and storage, the document editor, Doc Tender, the database, the starting dashboards, the admin dashboard, and the module runtime.

Out of core scope (modules, interface only): document templates and assembly, discovery/RFP, intake and questionnaires, email, Zoom, LawPay, client portal, QuickBooks sync, record importers, the matter wiki, and a self-hosted mail server later. Five of them are built as modules before release (section 9.10).

## 2. Fixed decisions

- **Stack:** Go on Postgres. Postgres is the standard database. The data layer is written so SQLite can be added later without touching domain code (section 6).
- **Install:** Docker Desktop. Images are signed with Cosign. A native installer can come later; nothing in this build blocks it.
- **No end-user restarts:** settings changes, module installs and core updates all apply while the system runs. Core updates apply themselves overnight.
- **Replaceable domains:** time/invoicing, trust and calendar can each be replaced by a module.
- **Roles:** admin, attorney, staff, accountant, client.
- **Trust below zero:** never blocked; always flagged; the flag emails the admin and the responsible attorney.
- **Marketplace:** modules must be signed. The operator does not review, inspect or vouch for modules. The operator can flag a module and can pull it from the marketplace. Firms that already run it keep it; the firm admin decides what to do.
- **Every feature has an API endpoint agents can use.** The UI calls the same endpoints.
- **Everything adjustable is on the admin page.** No tunable value lives only in code or in env files (section 8.7).
- **One file store:** every file is a core file record; its bytes sit on the storage backend the admin picks. No module keeps a file store of its own (section 8.19).
- **Document editor:** the core edits documents with LawCraft, pinned to one commit (section 8.20).
- **Doc Tender:** every file is processed when it arrives, never in a nightly batch, and its Markdown copy is kept for good (section 8.21).
- **One database:** modules extend it in schemas of their own and link to core records; they never copy them (section 9.9).

## 3. License and distribution

- **Code license:** a source-available license modeled on the Business Source License 1.1. Additional Use Grant: any firm may download, install, run and modify Togavsu for its own practice at no charge. Anyone hosting Togavsu commercially, or serving it to other firms, needs a commercial license: $500 per year plus $2 per seat per month for every seat the host serves. No change-date conversion; the source stays under these terms. The build places this text in LICENSE and a plain-English summary in LICENSE-SUMMARY.md.
- **Paid modules:** allowed. Module authors set their own license and price.
- **No license enforcement in code.** The core has no license keys and no phone-home for licensing. The admin dashboard shows the active seat count so a host can bill from it.
- **One install per firm.** The core is single-tenant. A commercial host runs one stack per firm.
- **Ownership:** the software belongs to Chariton Media. Source lives in the GitHub repo CharitonMedia/togavsu. Images publish to GitHub Container Registry under the CharitonMedia organization.
- **Marketplace listing is separate from the code license.** The license governs the code. Listing, flagging and pulling a module are marketplace terms, set by the marketplace operator, and don't change anyone's rights to code they already have.

## 4. Users and roles

A user holds one or more roles. Patrick, for example, is admin and attorney. Access decides what data a user can see; the UI never decides it.

| Role | Can do |
| --- | --- |
| Admin | Everything, including firm settings, users, roles, matter walls, modules, updates, backups, API tokens, audit log |
| Attorney | All practice work; approve and send invoices; trust withdrawals and transfers; conflict decisions; see attorney-only fields |
| Staff | Contacts, matters, notes, files and the editor, calendar, deadlines, time, expenses, draft invoices, record payments and trust deposits; no trust withdrawals, no conflict decisions, no attorney-only fields |
| Accountant | Read-only: invoices, payments, trust ledgers, reconciliations, billing reports, exports. No notes, no files beyond receipts and invoice PDFs, no conflict reasons, no sensitive contact fields |
| Client | API access to their own matters' shared items only. The client portal module supplies the client UI |

Rules:

- All staff, attorneys and admins see every matter unless a matter has a wall. An admin can wall a matter to named users; everyone else loses it from lists, search, widgets and the API.
- Attorney-only fields (conflict reasons, encrypted identifiers such as SSN and EIN) return redacted for any role without access.
- Permissions are checked in the service layer on every read and write, so the API, the UI, widgets and modules get identical answers.

## 5. Core records

The record set follows Nolan PMS Unified closely, cleaned up. All tables carry id (UUIDv7), created\_at, updated\_at and created\_by. Money is stored as integer cents. Timestamps are UTC; display uses the firm's time zone. This is the base: the records every firm needs and every module links to, so no module defines them again (section 9.9). Unified fields not listed here, such as gender, marital status or employer, are custom fields (section 8.14).

**Contacts**

- contacts: kind (person or organization), name parts, full name, preferred name, also known as, date of birth, date of death, encrypted SSN or EIN, organization fields (entity type, formation state and date), archived flag, source.
- contact\_addresses, contact\_emails, contact\_phones: many per contact, each with a label, primary flag and effective-from/effective-to dates, so history is kept instead of overwritten. Phones are stored in E.164.
- contact\_tags: free tags, many per contact.

**Matters**

- matters: matter number (format YYYY-NNNN, firm-configurable prefix), name, practice area, status, stage, case number, court, county, client position, opened on, closed on, close reason, statute of limitations date, billing type (flat, hourly, mixed, contingency, pro bono), flat fee amount, matter rate, responsible attorney, assigned staff, referral source.
- Status values: prospect, open, on\_hold, closed. Stage is a free list per practice area that the admin maintains; modules such as intake may add stages.
- A matter can always close. An unpaid invoice balance or a nonzero trust balance raises a warning on the close screen; neither blocks the close.
- matter\_parties: matter, contact, role, primary flag, role attributes, service address, notes. Roles: client, opposing party, opposing counsel, prosecutor, judge, expert, witness, co-counsel, guardian ad litem, referrer, officer, authorized user.
- matter\_walls: matter and the users allowed in.

**Lists the admin keeps**

- practice\_areas: name, matter types, stages in order, active flag. Matters, stages, custom fields and default folders hang off it.
- counties: name and state, seeded at first run with the counties of the firm's state; the admin adds others.
- referral\_sources: name, kind (person, organization, advertising, web, other), linked contact, active flag.

**Staff, auth and permissions**

- users: name, email, roles, active flag, default hourly rate, linked contact (for client users).
- sessions, password credentials (Argon2id), TOTP secrets, recovery codes, API tokens.
- TOTP two-factor is required for admin, attorney, staff and accountant; optional for client users.

**Firm settings**

Settings live in a settings table, one row per setting, and are set on the admin page (section 8.7). They include firm name, address, phone, logo, time zone, time increment, default rates, activity types, expense types, invoice numbering and terms, grace period, late interest (off by default), trust accounts, system mail (SMTP), update window and backup retention.

**Fee agreements**

fee\_agreements: matter, fee type, amount, services description, status (draft, sent, signed, void), signed date, void reason, the signed copy (a file record, section 8.19). One active agreement per matter. E-signature belongs to a module; the core stores the record and the signed file.

**Time and expenses**

- time\_entries: matter, user, date worked, increments, description, activity type, billable flag, rate, invoice (once billed), source (manual, timer, API, module id).
- Increment defaults to 6 minutes (0.1 hour). The admin can set 1, 6, 10 or 15 minutes.
- timers: one running timer per user per matter; stopping a timer creates a draft time entry.
- expenses: matter, date incurred, description, amount, expense type, billable flag, markup percent, receipt (a file record), invoice once billed.

**Invoices and payments**

- invoices: matter, number, date, status (draft, sent, viewed, paid, void), line items (time, expense, flat fee, adjustment), subtotal, adjustment and reason, interest, total, amount paid, balance due, write-off and reason, void reason, dispute status and notes, sent at, paid at, client message, internal notes.
- Billing flow: draft from unbilled time and expenses, attorney approves, the system sends the PDF by system mail or the user downloads it. Voiding an invoice releases its time and expenses back to unbilled.
- payments: contact, matter, amount, destination (operating or trust), method (cash, check, card, ACH, internal transfer, other), reference number, status (pending, completed, failed, reversed), applied invoice, related trust entry, source (manual, API, module id). Payment processor modules such as LawPay write payments through the API.

**Trust**

- trust\_accounts: the firm's trust bank accounts.
- trust\_ledger: append-only. Entry types: deposit, withdrawal, transfer to operating, refund, adjustment. Every entry names the client, the matter, the amount, the method, the description and the user. Corrections are reversal entries; no entry is ever edited or deleted.
- Balances: per client per matter, per client, and per trust account, computed from the ledger and cached for display.
- trust\_flags: raised automatically (section 8.4).
- trust\_replenishment\_requests: matter, client, amount requested, threshold that triggered it, message, status (active, satisfied, cancelled). A matter can carry an evergreen minimum; dropping below it creates a request.
- trust\_reconciliations: monthly; records the bank statement balance and date, compares it to the ledger total and the sum of client balances, and stores the result and any difference.

**Notes, conflicts, calendar, audit**

- notes: matter or contact, author, title, body, pinned, tags, status (active, archived).
- conflict\_records: name, aliases, date of birth, relationship (prior client, opposing party, other), scope (client or matter level), attorney-only reason, matter reference, date range, source.
- conflict\_reviews: matter, matched record, confidence score, decision (clear, escalate, no go), attorney-only reason, decided by and when.
- calendar\_events: title, kind (court date, client meeting, deadline, block, other), start, end, all-day, matter, location, travel minutes, notes, attendees (users).
- deadlines: matter, title, due date, responsible user, status (open, done, waived), reminder offsets, done by and when.
- audit\_log: append-only; every create, update and delete across the core, with actor, role, source (UI, API token, module id), entity, field, old value, new value, reason and timestamp.
- events: the append-only stream behind the case timeline and the activity feed: actor kind (staff, client, agent, module or System), actor, action, record, matter, summary, client-visible flag and time. Every write and every file download adds one.

**Files, editor and Doc Tender**

- files: name (the file's one name, section 8.21), extension, matter, contact, folder, category, status (draft, final, executed, filed, received; none shows no tag), origin (firm or received), attorney-only flag, shared-with-client flag, source (upload, client upload, editor, email, Zoom, drop folder, importer or module id), the name it arrived with (kept for page order and audit, never shown as its name), fields a person locked (name, category, matter, grouping), current version, trashed at and by. Receipts, signed fee agreements and invoice PDFs are file records too.
- file\_versions: file, number, storage backend, storage key, size, SHA-256, media type, saved by and when, how (upload, editor, editor run, conversion, grouping, module), note, quarantined flag. A version never changes.
- file\_renditions: file version, kind (Markdown copy, PDF preview, grouped PDF), its own version number, storage backend and key, size, SHA-256, made by and when. Markdown copies are kept for good.
- file\_links: typed links between files: photos to the PDF made from them, a conversion to its source, an email to its attachments.
- storage\_backends: kind (local disk, network path, S3-compatible, Dropbox), name, settings (encrypted), encryption on or off, default flag, health, backed-up flag. storage\_moves: from, to, scope (all files or one matter), status, progress, confirmed by.
- edit\_locks: file, user, since, last renewal.
- court\_profiles: the firm's own LawCraft court profiles as JSON, verified flag, verified by and when.
- doc\_categories: name, description, tier (high or standard), the folder it files to, active flag.
- doc\_tender\_jobs: file version, stage, status, attempts, next attempt, last error, lease.
- doc\_tender\_units: job, unit (page, photo, sheet or media window), method, model, text, check result.
- doc\_tender\_results: file version, name parts, summary, category and how it was set (model, model agreement or person), critical fields (names, dates, amounts, case numbers) each cited to its page or timestamp, confidence per field, matter match.
- doc\_tender\_exceptions: file, kind, opened at, detail, status, resolved by, when and how.

Model calls and their cost go to the AI call log (section 8.7).

## 6. Architecture

**Containers (one Docker Compose project):**

- **caddy:** TLS and reverse proxy; the single entry point on one domain.
- **core:** the Go application; web UI, REST API, MCP endpoint, background jobs.
- **postgres:** Postgres 17 with a named data volume.
- **supervisor:** a small Go service with Docker socket access. It runs updates, starts and stops module containers, and takes backups. The core never touches the Docker socket; it asks the supervisor over an authenticated local API.
- **modules:** one container per installed module.

**Core application:**

- Go, standard library HTTP with the chi router; pgx for Postgres; sqlc for typed queries; goose migrations embedded in the binary.
- UI: server-rendered Go templates with HTMX. The home screen grid uses gridstack.js. No SPA framework.
- Background jobs run in the core process from a Postgres-backed job queue (Doc Tender processing, file moves, reminders, flag emails, update checks, marketplace polling, cache rebuilds).
- Settings reload live; no setting requires a restart.

**Data layer rules (so SQLite can be added later):**

- Domain code talks to repository interfaces, never to SQL directly.
- Queries stay in portable SQL. No Postgres-only features in core queries: no jsonb operators, no arrays, no pg\_trgm, no stored procedures, no triggers carrying business logic.
- JSON columns are stored and read whole, parsed in Go.
- Money in integer cents; IDs as UUIDv7; timestamps UTC.
- Fuzzy name matching for conflicts runs in Go (normalized names, trigram and phonetic scoring), not in the database.
- sqlc query files sit in a per-dialect folder; only the Postgres folder ships in this build.
- Core tables live in the Postgres schema core; each module's tables live in a schema of its own (section 9.9). When SQLite is added, a schema maps to a table-name prefix.
- The base grows only by migrations. Core and module migrations are numbered SQL files with checksums, recorded in schema\_migrations (owner, version, checksum, applied at). Each runs once, in its own transaction, expand-then-contract, while the system runs. A changed checksum on a migration that already ran stops the update.

**Updates without restarts:**

- Each night in the firm's update window (default 2:00 to 4:00 a.m. firm time), the supervisor checks the registry for a newer signed core image.
- It verifies the Cosign signature against the Togavsu release key, backs up the database, runs migrations, starts the new core container, waits for its health check, switches caddy to it and stops the old one. Users see no interruption.
- Migrations follow expand-then-contract so the old and new core run against the same schema during the swap.
- A failed signature check, migration or health check aborts the update, keeps the running version, restores the backup if a migration ran, and emails the admin.
- The admin can change the window or pause updates. Updates never apply outside the window unless the admin starts one.

**Backups and logs:**

- Backups run through the backup system below.
- Audit log is kept forever.
- Application and access logs are kept 400 days by default so year-over-year comparison works. Nothing shortens retention without the admin changing the setting.

**Backup system:**

- **Automatic:** a nightly full backup (the database with every module schema, file bytes from every storage backend, copied once per version since versions never change, and module data volumes) and one before every update. Kept 30 days on the local backup volume by default; the admin can raise it.
- **Download:** the admin can make a backup on demand from the admin dashboard and download it to their computer as one archive file.
- **Other destinations:** the admin can add destinations that receive a copy of every automatic backup: a local or network path (an SMB or NFS share mounted into the stack), S3-compatible storage, or SFTP. Each destination has its own retention. A failed copy emails the admins.
- **Encryption:** every archive is encrypted with AES-256 using a backup passphrase the admin sets at first run. Restore asks for it.
- **Restore:** the admin uploads a backup file, or picks one from the list or a destination. The supervisor checks the file, takes a safety backup of the current state, restores, and brings the core back with no manual steps. Restore is the one operation that interrupts users; the dashboard warns before it starts.
- **Checks:** every backup is checksummed when written and when copied. Once a month the supervisor test-restores the newest backup into a temporary database and compares record counts. Only failures email the admins.
- **API:** /api/v1/backups (list, create, download, delete), /api/v1/backup-destinations (add, edit, test, remove) and /api/v1/restores (start, status), admin scope only, and the same operations through /mcp.

**Security:**

- The source is public, so assume attackers read it. Every endpoint authenticates and authorizes in the service layer.
- Sensitive fields (SSN, EIN, TOTP secrets, module secrets) are encrypted with AES-256-GCM using a key the installer generates and stores in a Docker secret.
- Strict Content Security Policy; CSRF tokens on form posts; secure, HTTP-only, same-site cookies; login and API rate limits; lockout after repeated failed logins.
- Secrets never reach logs or the audit log.

## 7. API for agents

- REST JSON under /api/v1. Every core feature has endpoints; the UI uses the same handlers.
- OpenAPI 3.1 spec generated from the code and served at /api/v1/openapi.json.
- An MCP endpoint at /mcp exposes the same operations, generated from the OpenAPI spec, so agents can connect directly.
- Auth: session cookie for the browser; bearer tokens for agents and modules. Tokens are issued in the admin dashboard, tied to a user or module, scoped (for example time:write, trust:read), and revocable.
- Writes accept an Idempotency-Key header. Lists use cursor pagination and filters.
- A bulk endpoint per record type validates and writes batches in one transaction and rejects the whole batch when anything fails to validate or reconcile. Importer modules use it.
- Events: every write emits an event (for example matter.created, time\_entry.updated, trust.flag\_raised). Modules subscribe in their manifest; the core delivers events by signed webhook with retries.

## 8. Feature requirements

### 8.1 Contacts and matters

- Create, edit, archive and search contacts and matters; search covers names, aliases, emails, phones, matter numbers and case numbers.
- Matter screen: header (number, name, status, client, responsible attorney), tabs for parties, time, expenses, invoices, trust, calendar and deadlines, notes, and any tabs modules add.
- Every edit lands in the audit log.

### 8.2 Conflicts

- Running a conflict check on a matter searches contacts, matter parties and conflict records by name, alias and date of birth.
- Hits come back scored. An attorney records a decision per hit: clear, escalate or no go. Reasons are attorney-only.
- New matters show an unchecked-conflicts badge until every party has a check.

### 8.3 Time, expenses and invoicing

- Quick time entry from anywhere (global shortcut and home-screen widget), timers, and bulk entry.
- Invoice drafts pull unbilled time and expenses for a matter or a date range; flat-fee matters bill the fee.
- Payments apply to invoices; trust-to-operating transfers apply to invoices and post a matching trust ledger entry.
- Invoice PDF rendered in the core with the firm's logo and settings.
- Reports: unbilled time, time by user, AR aging, payments received, write-offs. CSV export on every report.

### 8.4 Trust

- Deposits, withdrawals, transfers to operating, refunds and adjustments, each written to the ledger.
- **Below-zero rule:** any entry that takes a client's balance (per matter or overall) below zero posts normally. It's never blocked; a firm may be making an adjustment on paper offline. The core raises a trust flag, emails the firm's admins and the matter's responsible attorney, and shows the flag on the trust alerts widget until an attorney or admin resolves it with a note.
- Replenishment requests trigger at the matter's evergreen minimum and appear on the dashboard.
- Monthly reconciliation: enter the statement balance; the core shows ledger total, sum of client balances, and the difference, and stores the result.
- Reports: trust balances by client, client ledger, account ledger, reconciliation history.

### 8.5 Calendar and deadlines

- Day, week and month views; per-user and firm-wide; filter by matter and kind.
- Deadlines with reminders by email and on the dashboard; overdue deadlines show on the home screen until marked done or waived.
- A read-only iCal feed per user and per firm, with a revocable secret URL, so events show in Google or Outlook calendars. Two-way calendar sync belongs to a module.

### 8.6 Home screen

- A 12-column widget grid, like an Android home page. Users show, hide, add, remove, move and resize widgets.
- Access decides which widgets and which data a user gets. A widget never shows data its viewer can't read through the API.
- Each user's layout is saved per user. A user can also save the current layout by name, up to 10 saved layouts per user, and rename, overwrite or delete any of them. One saved layout can be marked as the user's default. Reset offers the user's default saved layout first, then their other saved layouts and the role default; picking one restores it exactly. A user with no saved layouts goes straight to the role default. The admin sets the default layout for each role. Saved layouts belong to the user and survive core updates and role changes; a widget the user can no longer see, or whose module was removed, drops out when the layout loads and the rest stay in place. Records: home\_layouts. API: /api/v1/me/layouts (list, create, rename, update, delete, set default, apply).
- Core widgets, with the page each opens: my time today and this week (time entries for that user and period), running timer (that timer's time entry), unbilled time (unbilled time and expenses), my open matters (matters list, the user's open matters), upcoming deadlines (deadlines page, same date range), today's calendar (calendar, today in day view), trust alerts (trust flags), replenishment requests (replenishment requests), AR aging (AR aging report), recent activity (Activities Feed), pending conflict checks (conflict checks), module alerts (admin; module alerts on the admin dashboard).
- **Click-through:** clicking a widget's title opens the full page that holds the information the widget shows, with the same filters, sort and date range; upcoming deadlines opens the deadlines page filtered to the same dates. Clicking one row in a widget, such as one deadline or one matter, opens that record. The page applies the same access the widget does, so a click never shows more than its viewer can read and walled matters stay hidden. Clicks don't open anything while the layout is being edited. Back returns to the home screen as it was. Every core widget names its target; a module widget opens the target its manifest declares, normally one of the module's own tabs, with the widget's filters passed along, and the core checks the viewer's access there too.
- Module widgets appear in the same picker once the firm approves them.

### 8.7 Admin page

Every value in Togavsu that a firm might want to change is set on the admin page (the admin dashboard, at /admin), never hard-coded. The page is organized in tabs. Each setting shows its current value, its default and a Reset to default button. A change applies on the next request with no restart and goes to the audit log with the old and new values.

**No hard-coded tunables:** every setting is declared once in the core's settings registry (key, type, default, allowed range, tab and help text) and stored in the settings table. Code reads settings only through the registry. Every default named anywhere in this PRD is the value its setting starts with, and the admin can change it. Env files and Docker secrets hold only what the core needs before it can read the database: the database connection, the encryption key, the domain caddy serves and the supervisor's local API secret. The Togavsu release signing key ships in the image and is not a setting, because changing it would switch off the update signature check. Fixed rules are not settings either: the trust ledger is append-only, the audit log is kept forever, a below-zero trust entry always flags, and permissions are checked in the service layer.

Tabs:

- **Firm profile:** firm name, short name, address, phone, email, website, logo, time zone, matter number format and prefix (default YYYY-NNNN).
- **Users and roles:** add, edit and deactivate users; roles; default hourly rate; bar number and state for attorneys; linked contact for client users; matter walls.
- **Security:** two-factor by role (default required for admin, attorney, staff and accountant, optional for client users; the admin role always requires it), longest idle time before sign-out (default 12 hours), failed sign-ins before lockout (default 5), lockout length (default 15 minutes), API rate limit (default 600 requests a minute per token).
- **Practice:** practice areas and their stages, matter markers, custom fields, party roles, case number patterns (default Missouri docket numbers), lowest conflict score shown (default 60 out of 100).
- **Time and billing:** time increment (default 6 minutes; 1, 6, 10 or 15), default rates, activity types, expense types, default expense markup (default 0 percent), invoice numbering, payment terms (default due in 30 days), grace period (default 15 days), late interest (default off; the rate when on), fee items catalog, default client message on invoices.
- **Trust:** trust accounts (bank, account name, IOLTA flag, last four digits), extra recipients for trust flag emails (the admins and the responsible attorney always get them and can't be removed), default evergreen minimum for new matters (default none), monthly reconciliation reminder day (default the 5th).
- **Calendar:** default reminder offsets (deadlines 7 days and 1 day before; events 1 day before), iCal feeds (on or off, revoke links), available dates defaults (range three months, purpose "one day trial", title "AVAILABLE DATES FOR HEARING").
- **Files and storage:** storage backends (local disk, network path, S3-compatible, Dropbox) with a Test button each, the default backend (default local disk), encryption per backend (always on for S3-compatible and Dropbox; default on for local disk and network paths, where the admin can turn it off), which backends backups copy (default all; excluding one shows a warning), largest upload (default 2 GB), default folders per practice area (default Correspondence, Pleadings, Discovery, Evidence, Client documents, Billing, Notes), the drop folder on or off for each network path, Move files.
- **Document editor:** the LawCraft commit in use (read only), edit lock timeout (default 5 minutes without a renewal), editor runs at once (default 2) and each run's time and memory limits (default 60 seconds and 1 GB), PDF previews (default on), the firm's court profiles.
- **Doc Tender:** on or off (default on), the naming pattern, the categories with their descriptions, tiers and folders, the confidence below which an item goes to review (default 0.90), quiet time before photos are grouped (default 10 minutes), the retry schedule, review queue recipients (default the admins), the daily summary email (default on, 7:00 a.m. firm time), and spend by day, matter and file type. Its models are on the AI tab.
- **Home screen:** each role's default layout and widgets, saved layouts allowed per user (default 10).
- **Lists and search:** search start (default 2 characters) and delay (default 180 ms), hits per scope (default 5 in All, 8 in one scope), Activities Feed window (default 30 days) and page size (default 100), notes page size (default 25).
- **Notifications and mail:** SMTP settings, routing system mail through the email module, firm default channels for each notification kind (section 8.17), problem report recipients (default the admins).
- **Appearance:** colors, presets, typography, text size, names and labels, and the firm's default mode (section 8.17).
- **AI and inference:** below.
- **Help:** the firm's help sections and items (section 8.11).
- **Modules and marketplace:** installed modules with install, configure (each module's settings form from its manifest), permissions, enable, disable and remove; which module replaces time, trust or calendar; trusted local signing keys; marketplace address (default the Togavsu marketplace); alert check frequency (default daily); marketplace alerts.
- **Updates:** update window (default 2:00 to 4:00 a.m. firm time), pause updates, update now, update history.
- **Backups and logs:** nightly backup time (default 1:00 a.m. firm time), local retention (default 30 days), destinations each with its own retention, monthly test-restore day (default the 1st), backup now, download, restore, application and access log retention (default 400 days).
- **Agents and API:** API tokens and scopes, webhook retries (default 8 over 24 hours), llms.txt (show or hide the firm name; the firm paragraph).
- **System:** seat count, audit log viewer, problem reports.

**AI and inference tab:** the one place every AI call made through Togavsu gets its provider, model and key.

- **API keys:** one per provider, encrypted like other secrets, with a Provider check button that tests each key. Providers: Anthropic, OpenAI, any OpenAI-compatible endpoint, and a local endpoint by URL.
- **Primary and secondary provider:** each with a fast model and a strong model. A call that fails, or gets no answer within the timeout (default 30 seconds), goes to the secondary provider. There's no secondary until the admin sets one.
- **Per-task overrides:** any task a module declares (for example search, drafting or summaries) can run on its own provider and model, with its own fallback.
- **Doc Tender tasks:** OCR, photo description and grouping, transcription, labeling and categorizing, each with a model and a check model. Each provider also names its speech-to-text model for transcription. Labeling and categorizing default to gpt-6.1-sol on OpenAI once an OpenAI key is set, and to the primary provider's strong model otherwise; they never run on a local endpoint, and the tab doesn't offer one for them. Each check model defaults to the secondary provider's model for that task, or to the primary provider's other tier when there's no secondary.
- Modules ask the core to run AI calls through the API under the ai:use scope and never hold keys. Every call logs the task, provider, model, tokens, whether it fell back, and the user or module that asked. Doc Tender (section 8.21) is the core's only AI feature, and it runs on these settings like any module.

API: /api/v1/settings (list every setting with its tab, value and default; update and reset for admins), also through /mcp.

### 8.8 System mail

The core sends its own system mail (trust flags, deadline reminders, invoices, password resets, update failures, module alerts) through SMTP settings the admin enters. The firm's full mail system is the email module's job; when the email module is installed, the admin can route system mail through it.

### 8.9 App shell: sidebar and header

Every staff screen uses one shell, carried over from Nolan PMS Unified: a sidebar on the left, a header across the top, and the page beside them.

**Sidebar:**

- 240 pixels wide, full height. At the top, the firm's short name with a label under it (default "Staff portal"); the admin sets both.
- Top band, no heading: Home, Matters, Contacts, Calendar, each with an icon. Matters shows a count of active matters.
- Sections below, in this order: Communications, Documents, Litigation, Money, Intake, Activity, System. A section with nothing visible in it is hidden.
- Core entries: Notes (Communications); Time Entries, Billing / Generate, Invoices with a draft count, Payments, and a Trust subgroup holding Trust Ledger, Trust Balances and Trust Replenishment (Money); Activities Feed (Activity); Administrative, admin only, and Help & Training (System).
- Modules add entries through their manifest: section, subgroup, label, icon, order, and an optional count badge in a normal or warning tone. Unified's Messages with an unread count, Unfiled Emails with a warning count, Zoom, Templates, Discovery, Intake Admin and Veterans Evaluations all become module entries this way. A module can also place one panel in a slot under the top band, where Unified shows the Zoom softphone.
- Subgroups collapse and expand. Each user's choice is saved with the user, so it follows them to another computer.
- The active page is marked with an accent bar on the left edge and a bold label.
- Bottom: a "Report a problem" link that opens a short form; the report is saved and emailed to the firm's admins. Footer: the user's name and role, "signed in".
- The sidebar only hides links. The service layer still decides access on every route and API call.
- One call, GET /api/v1/nav/context, returns everything the sidebar needs: admin flag, installed modules, badge counts, firm short name and label.

**Header:**

- A bar across the top of every staff page holding, left to right: the sidebar button, the search box (section 8.10), Timers, Notifications, Help, Quick create and the user chip.
- Sidebar button: collapses the sidebar to icons and back; saved per user.
- Timers: opens the running-timers panel to start, pause and stop timers. A stopped timer opens for review and saves as a draft time entry.
- Notifications: a bell with the user's unread count. It opens the recent notifications; clicking one marks it read and opens its item; "Mark all read" clears the count. Each user sees only their own. Core events that notify: trust flags, deadline reminders, replenishment requests, matter assignment, and update failures and module alerts for admins. Modules send notifications through the API. Records: notifications (user, kind, title, link, read at).
- Help: opens /help (section 8.11).
- Quick create: New matter, New person, Log time. Modules add items; Unified's New message and New email come from the portal and email modules.
- User chip: the user's initials or profile picture; clicking it opens My profile and Sign out.
- Phone width: the sidebar hides and a bottom tab bar shows Home, Matters (opens the search set to Matters), Messages when a messaging module is installed or Calendar when not, and Time.
- API: /api/v1/notifications (list, mark read, mark all read), /api/v1/timers and /api/v1/nav/context, all also through /mcp.

### 8.10 Search

- One search box in the header, with a scope picker beside it: All, Matters, People, Invoices, Files. Modules add scopes.
- Ctrl-K or Cmd-K puts the cursor in the box from any screen. Placeholder text: "Search matters, people, invoices, files (Ctrl-K)".
- Search starts at 2 characters, 180 ms after typing stops. Matching ignores case and finds the text anywhere in a field.
- What each scope searches:
  - Matters: matter name, matter number, case number and state case number. Open matters first, then newest opened.
  - People: full name, first name, last name, also known as, email and phone. Active contacts before archived, then alphabetical.
  - Invoices: invoice number. Newest first; each hit names its matter.
  - Files: file name and the full text of its saved Markdown copy (section 8.21), with a 120-character excerpt around the first match. Open matters first, then newest.
- All shows up to 5 hits per scope, grouped under scope headings; one scope shows up to 8.
- Each hit shows a title and a short subtitle (status, kind, city or matter) and opens its record. Arrow keys move through the hits, Enter opens one, Escape closes the list. With no hits: "No matches for {query} in {scope}."
- Search honors roles and matter walls in every scope: matters behind a wall, and their invoices and documents, never appear for users outside it, and attorney-only fields are never searched for users without access. (Unified limits only documents this way.)
- Portable SQL: lower-cased LIKE on indexed lower-case columns; no full-text or trigram features in the core.
- API: GET /api/v1/search?q=&scope=, also through /mcp. Module scopes are answered by the module and merged by the core. Staff only; client users get no search.

### 8.11 Help and guides

- Help & Training lives at /help, reached from the sidebar and the header's Help button.
- At the top, two guide cards: the Togavsu user guide in HTML, opening in a new tab, and the same guide as a PDF with its own table of contents for printing or reading offline.
- The guide has one source: Markdown files in the repo's /docs/guide. The build renders it to HTML and PDF and ships both inside the core image, so every install carries the guide for its own version. The build agents write and update the guide in each phase, with screenshots taken on seed data.
- **What the guide covers:** every core feature, every screen and every admin page tab. Each topic says what it does, how to use it and exactly where to find it: the menu path (for example Administrative > Time and billing > Time increment) and a link that opens that screen.
- **Getting started:** a tutorial for each role (admin, attorney, staff, accountant) that walks through the role's first day: signing in and two-factor, the home screen, and the tasks the role does most. The admin tutorial also covers first-run setup and every admin tab.
- **Help from any screen:** every screen and every admin tab has a ? icon in its title bar that opens its guide topic. The guide's search finds topics by name and by the words on the screen.
- Both guide files sit behind staff sign-in, never in public files, because they describe the system that holds client data. Client users get a 404.
- Under the guides, the firm's help sections, each holding items. An item is text, a video or an image, with a title and a sort order. A section with no items shows "No items in this section yet."
- The admin adds, edits, orders and removes sections and items in the admin dashboard; no database work is needed. Records: help\_sections and help\_items.
- **Module guides:** a module declares its help guide in its manifest. On install, the core registers the module's guide pages through the help registration API and merges them into the help center under the module's own heading: they appear in the guide's search, and the module's widgets, tabs and matter-screen items open their pages from their ? icons. A running module can also call the API to add or change its pages. On a module update the core replaces its pages with the new version's; on disable they hide; on uninstall they're removed. None of this needs a restart. A module can write pages only under its own heading.
- API: /api/v1/help/sections and /api/v1/help/items (read for all staff, write for admins), /api/v1/help/modules/{module-id}/pages (list, register, replace, remove; open to that module's own token, which gets the help:write scope for its own heading at install), plus /help/guide and /help/guide.pdf.

### 8.12 Matter screen additions

- **At a Glance:** a card at the top of every matter showing Stage, Trust Balance, Time Logged (hours and entry count), Days Open and Invoices. It also shows Files: the count, and any open Doc Tender exceptions.
- **Case Timeline:** every event on the matter, newest first, from the core's event stream: who did it (staff, client, agent, module or System), what happened and when. Client actions, such as a client downloading documents through the portal, show highlighted. Modules add their events to the same timeline.
- **Markers:** small labels in the matter header. Core markers: Pro bono; Zero fee (no fee is charged, by law or by choice; kept apart from Pro bono so a zero-fee matter never counts as pro bono work in any report); Also asked about (another practice area the client raised, with that area named; set by staff or by an intake module). The admin adds firm markers with a label and a color; modules add markers through the API. Zero-fee matters drop out of AR aging and collections. Records: matter\_markers.
- **Reopen a closed matter:** an attorney or admin reopens a closed matter with a reason. It returns to the status it held before closing, the reason goes to the audit log and the timeline, and nothing about the close (dates, close reason) is erased.
- **Matters missing a case number:** a worklist at /matters/needs-case-number listing open matters with no case number that either have a court entered or carry something shaped like a docket number in the matter name. The default pattern matches Missouri docket numbers (for example 23AR-CV00825); the admin can add patterns. Each row opens the matter; filling the case number removes it from the list. The page changes no data.

### 8.13 Firm-wide pages

- **Activities Feed (/activity):** the last 30 days of activity across the firm, newest first, 100 rows a page with paging. Rows group under Today, Yesterday, Earlier this week and Earlier this month, using the firm's time zone. Tabs with counts: All, Clients, Staff and Agents (API tokens and modules). Under Staff, a chip for each person filters to that person. Each row names the actor, the action and the matter, with a link and the time. It reads the same event stream as the timeline and honors matter walls.
- **Notes (/notes):** every note the user can see across all matters and contacts. Search title or body; filter by matter, author, tag and a from and to date. Pinned notes first, then newest; 25 a page. Notes written by an agent or module may be rich text and are sanitized before display; staff notes are plain text that keeps its line breaks. Notes gain tags (free text, many per note).

### 8.14 Edit history and custom fields

- **Edit history:** a collapsible Edit history card on matter, contact, invoice, fee agreement, time entry and expense screens. Each row shows when, who and their role, the field, the old value, the new value, the kind (create, update, delete) and any note, newest first, read from the audit log. Attorney-only values show redacted to users without access. API: GET /api/v1/{record type}/{id}/history.
- **Custom fields:** the admin defines extra fields for matters, contacts, time entries and expenses. Types: text, long text, number, money, date, yes or no, pick list and contact link. Each field has a label, an order, an optional required flag and an optional limit to chosen practice areas, so a matter shows only the fields for its area. Fields show in a Custom Fields card on the record, can be filtered on in lists, appear in CSV exports and are read and written through the API. Adding a field never changes the schema: definitions and values live in custom\_field\_definitions and custom\_field\_values. Retiring a field hides it and keeps its values.

### 8.15 Billing additions

- **Billing / Generate worklist (/billing):** two tables. Flat-fee matters list matter, client, flat fee, trust balance and a status: Needs fee amount, Awaiting trust funds, Ready to apply or Collected. On a Ready row, Apply flat fee moves the fee from trust to operating at the earning milestone; the amount is prefilled from the matter's flat fee and the user confirms it, and only an attorney or admin can post it, as with any trust withdrawal. Hourly and mixed matters with unbilled time or expenses list the unbilled total, with Generate draft invoice on each row and for all selected rows.
- **Collections panel:** on every sent invoice with a balance. Apply trust (pick the client holding funds; defaults to the only funded client), Record outside payment (a payment received outside the system, recorded, never issued), and Request replenishment for the balance, linked to the invoice. Amounts are prefilled; the server requires an explicit amount on every post. An overdue invoices list sorts by age.
- **Fee items catalog:** the admin keeps the firm's standard flat-fee items: name, description, default amount, practice area and an active flag. Picking one on an invoice line, or as a matter's flat fee, fills in the text and amount, which stay editable. A deactivated item stays on past invoices. Records: fee\_items.

### 8.16 Available dates

- A calendar tool at /calendar/available-dates that lists the days an attorney is free for a hearing, formatted to file or send.
- Inputs: start date (required), end date (default three months after the start), whether to include half days, and the matter, picked by search to fill the rest: court, caption, party labels (Petitioner and Respondent, or Plaintiff and Defendant), each party's name, case number, which side the firm represents, purpose (default "one day trial"), who to coordinate with, title (default "AVAILABLE DATES FOR HEARING") and a service date.
- A day is busy when the attorney has a court date, a block or an all-day event on it. With half days on, a day with only a morning or only an afternoon free is listed as that half.
- Output as a pleading (caption, title, the dates, certificate of service) or as a letter (recipient, address, Re line, salutation). The result shows in a panel with Copy and Download.
- Read only: it never creates, changes or deletes a calendar event. API: POST /api/v1/calendar/available-dates.

### 8.17 Settings additions

- **Notification preferences (each user):** a grid of notification kinds against channels, in-app and email. Defaults: every kind in-app; email also for trust flags, deadline reminders, update failures and module alerts. The trust below-zero email to the admins and the responsible attorney can't be turned off. Modules' notification kinds join the grid. Records: notification\_preferences.
- **Appearance (admin page, Appearance tab):** staff portal colors for each area (sidebar, header, page and accents) with presets (Navy and gold, Charcoal and copper, Forest and cream, Maximum contrast); client portal colors with a header preview; typography (a heading font and a body font from Lora, Merriweather, Libre Baskerville, Source Serif, Inter, IBM Plex Sans, Source Sans and Atkinson Hyperlegible) and a text size; names and labels (firm short name, staff portal label, client portal label); the firm's default mode: light, dark or follow the device (default follow the device). Each user can pick their own mode and text size in My settings; the colors stay the firm's. Changes show on the next page load with no restart. API: /api/v1/settings/appearance (read, update).
- **Profile (each user):** name, initials, email, phone, profile picture, password change, two-factor reset and new recovery codes, and the API tokens the user owns.
- **My settings (each user):** default reminder lead times for events and deadlines, a personal display time zone, light or dark mode and text size (default the firm's), and automatic sign-out after idle time. The admin sets the firm's longest idle time on the Security tab (default 12 hours); a user can choose shorter.
- **AI settings:** on the admin page's AI and inference tab (section 8.7).

### 8.18 llms.txt for agents

- GET /llms.txt serves a short Markdown guide for AI agents with no sign-in: the product and version, the firm name (the admin can hide it), where the OpenAPI spec and the /mcp endpoint are, that every call needs a bearer token the admin issues, and a link to the API docs. The admin can add one paragraph of firm text.
- It never holds client data, matter data or anything from inside the firm's records.

### 8.19 Files and storage

Every file in Togavsu is a core record in the database; its bytes live on the storage backend the admin picks. It's one file store for everything: receipts, signed fee agreements and invoice PDFs, the editor, Doc Tender and every module use it.

- **Access:** a file belongs to a matter, a contact or both. A file on a walled matter is behind the wall; a file with only a contact follows that contact's access; attorney-only files are hidden from roles without attorney-only access. The same rules hold in lists, previews, downloads, search, widgets and the API.
- **One name:** the file's name is its only name. Doc Tender sets it (section 8.21), and there's no separate title. A user with write access can rename a file; the extension stays, and the rename goes to the edit history.
- **Status:** draft, final, executed, filed or received lives on the record and shows as a tag beside the name, never in it. It comes from what happened to the file: Mark draft, Mark final, Mark executed and Mark filed set it, and files from clients, mail and other parties arrive as received. A model's opinion never sets it.
- **Folders:** each matter has folders; the admin sets the defaults per practice area. Doc Tender files a document into the folder its category maps to, and users move files between folders. Folders are records only, so moving a file never moves its bytes.
- **Versions:** saving a changed file, from the editor, an upload or a module, adds a version. No version is ever overwritten or edited. Older versions download and restore; a restore copies the old version forward as a new one.
- **Upload:** drag and drop or pick files on a matter's or contact's Files tab, or through the API. Uploads go in 8 MB chunks and resume after a dropped connection. Largest file: 2 GB by default. A file that matches one already in the matter by SHA-256 gets a warning and still uploads. The client portal module writes client uploads as file records with the client as uploader.
- **Download:** one file, any version, or a folder or a selection as a zip. Every download goes to the audit log and the case timeline; client downloads show highlighted.
- **Preview:** PDFs and images open in the browser, audio and video play in it, and every format the editor reads gets a PDF preview when a version is saved.
- **Trash:** delete moves a file to the trash, where it stays listed and restorable. Only an admin can empty a file from the trash, after typing its name, and the audit log keeps the record. Nothing in Togavsu removes a client file on its own.
- **Storage backends:** local disk (a Docker volume on the firm's computer, the default), a network path (an SMB or NFS share mounted into the stack), S3-compatible storage, and Dropbox (the admin connects the firm's Dropbox account; files go to its Apps/Togavsu folder). Each is an adapter behind one Go interface (put, get with byte ranges, stat, delete, health check), so the core can add more backends later. Modules can't add backends, so module code never touches file bytes outside the API.
- **How bytes are kept:** under opaque keys (the version's id), never under the file's name, so renames and folder moves never touch storage. Bytes are encrypted with AES-256-GCM before they leave the core: always on S3-compatible storage and Dropbox, and by default on local disk and network paths, where the admin can turn it off per backend. Every read checks the SHA-256; a mismatch fails the read, raises a storage alert and emails the admins.
- **Moving between backends:** new files go to the default backend. The admin moves all files, or one matter's, to another backend from the Files and storage tab. The move runs in the background: it copies each version and rendition, checks its SHA-256 on the new backend, then points the record at the new copy. Files stay readable throughout, a stopped move resumes where it stopped, and progress shows on the tab. Old copies are deleted only after the whole move checks out and the admin confirms.
- **Drop folder:** a network path backend can have a drop folder, with one subfolder per open matter, named by matter number. A file dropped into a matter's subfolder joins that matter; one dropped at the top joins no matter, and Doc Tender matches it. A dropped file is taken once its size and SHA-256 hold steady for 30 seconds, then removed from the drop folder.
- **Malware scanning** is a module's job: it subscribes to file.version\_created and can mark a version quarantined through the API. A quarantined version shows a warning and downloads only after the user confirms; it's never hidden.
- **Rule for modules:** a module that handles files stores every file as a core file record through this API. It keeps no file bytes in its schema or on its volume, apart from temporary working copies it deletes when done.
- API: /api/v1/files (list, upload, read, rename, move, mark status, trash, restore), /api/v1/files/{id}/versions (list, add, download, restore), /api/v1/files/{id}/renditions (Markdown copy, PDF preview), /api/v1/storage-backends and /api/v1/storage-moves (admin only), all also through /mcp. Scopes: files:read and files:write.

### 8.20 Document editor

The core's document editor is LawCraft, Patrick's legal edition of the WordCraft word processor: written in Rust, licensed MIT or Apache-2.0, in the repo CharitonMedia/LawCraft. Modules use it and never ship an editor of their own.

- **Pinned version:** commit 770a8fab12392485c0c2789fb254da7d9296df4d on LawCraft's main branch (Oct 28, 2026: WordCraft 0.12.0 merged in, with LawCraft's court profiles, New Pleading, Edit Pleading and saved courts). The build checks out exactly that commit. Moving to a newer one is an ordinary Togavsu change with its own tests and never happens on its own.
- **How it's built:** the core image's build has a Rust stage that compiles two things from that commit. First, the browser editor: a WebAssembly build of LawCraft's wordcraft-web app with the wordcraft-law and wordcraft-law-ui crates added as its add-in and LawCraft turned on, built with trunk. Togavsu keeps this small wrapper crate in its own repo at /editor, depending on LawCraft's crates at the pinned commit; LawCraft's repo isn't changed. Second, wordcraft-cli, the headless build, for server-side runs. Both ship in the core image. CI reads the LawCraft repo with a read-only deploy key. The core serves the browser editor at /editor/, with a Content Security Policy that allows WebAssembly, and runs wordcraft-cli inside the core container. If a LawCraft crate doesn't build for WebAssembly at the pinned commit, the Files agent fixes it in a LawCraft change, offers it to LawCraft's repo, and moves the pin to that commit.
- **Editing:** Edit on a file in a format the editor opens (DOCX, DOC, ODT, RTF, HTML, Markdown or text) opens /files/{id}/edit, a Togavsu page that frames /editor/?host=parent on the same origin. Through the editor's own bridge, the page sends the current version in (wordcraft:open) and takes each save back (wordcraft:save) as a new version. A file in another format saves as DOCX, and its name's extension changes to .docx. Unsaved changes show in the page header, and leaving with unsaved changes asks first.
- **LawCraft's tools come with it:** court profiles, New Pleading and Edit Pleading, table of authorities, legal symbols and legal proofing. When the editor opens, the page gives LawCraft the firm's details from firm settings, and the matter's court, county, case number and parties, through LawCraft's own commands, so a pleading's caption fills from the matter. The firm's court profiles are core records loaded into the editor on open; a profile saved in the editor is written back, and one an agent made stays unverified until a person approves it.
- **One editor at a time:** opening a file to edit takes its edit lock. Anyone else who opens it sees who is editing and since when, and can preview or download the current version. The lock renews while the editor is open and lapses after 5 minutes without a renewal. An admin can release a lock, and the release goes to the audit log.
- **Access:** editing needs write access to the file; walls and roles apply exactly as they do to the file. Client users get no editor in the core.
- **Track changes and comments** stay inside the DOCX, as LawCraft keeps them, so they open in Word.
- **Browsers:** the editor needs WebGPU or WebGL2. Where the browser has neither (wordcraft:failed), the page says so and offers the download.
- **Editor API for modules and agents:**
  - Open: a module's frame asks the core shell to open the editor on a file it can write (UI bridge action editor.open).
  - Run: POST /api/v1/files/{id}/editor-runs takes a list of LawCraft commands by id with their parameters (filling merge fields or applying a court profile, for example), runs them headless with wordcraft-cli on the current version, and saves the result as a new version or a new file. Commands that read or write the file system or the network are refused; GET /api/v1/editor/commands lists the allowed commands.
  - Convert: POST /api/v1/files/{id}/conversions makes a new file in another format (PDF, DOCX, ODT, RTF, HTML, Markdown or text), linked to its source.
  - Runs are sandboxed: no network, a temporary working folder deleted afterward, and the limits on the Document editor tab (default 2 at once, 60 seconds and 1 GB of memory each). Scope: editor:run. Every run goes to the audit log and the file's version history. All of it also through /mcp.
- **Version history:** each version records who saved it and how (editor, upload, editor run, conversion, module); the file's history card lists them beside its field edits.

### 8.21 Doc Tender

Doc Tender is the core's file processor. It receives every file that lands in a matter, from every source, and turns each into an accurately extracted, searchable, correctly named, categorized and filed record in the right client matter. It's a file management system and a lead into discovery, not the discovery system: its labels and cited fields are where the discovery module starts. It's a fresh design on the core's file records, storage backends and AI settings; nothing is ported from Unified's doc-tender.

**Rules:**

- **Each file as it arrives.** Every new file or new version queues its own job at once; nothing waits for a nightly batch. A nightly sweep only checks that every file has a finished job or an open exception, and queues any that don't.
- **Closed matters cost nothing.** Before any model call, Doc Tender checks the matter's status. A file on a closed matter is stored and listed as Not processed (matter closed), and no tokens are spent. Reopening the matter queues its files.
- **Accuracy over cost.** It has to be right every time, because an error can cost a lawyer's license. Labeling and categorizing run on a cloud provider's strong model set on the AI tab (default gpt-6.1-sol), never on a local model. Every step that reads content has an independent check, and nothing that fails its check is published as correct.
- **Client documents are never written off.** A file Doc Tender can't process stays listed, viewable, downloadable and usable everywhere, carries a flag naming what failed, and goes to the review queue. Processing state never limits access.
- **Settled stays settled.** A name, category, matter or grouping a person sets is locked, and Doc Tender never changes it. A step that passed is never redone, and each decision is made once.
- **Routine items need no person.** Only exceptions reach the review queue.
- **A category never acts.** No deadline, filing, notice or client message comes from a category.
- **Before AI is set up:** until the AI tab has a provider key, Doc Tender stores and lists every file and holds its jobs; it starts on its own once a key is set. The first-run setup offers the AI tab for this.

**Sources:** staff uploads, client uploads, the editor, mail filed by the email module, recordings from the Zoom module, importers, the drop folder (section 8.19), and files any module writes.

**Steps for each file,** each recorded with its method, model, attempts and cost:

1. **Check:** the matter's status (closed stops here), whether the file opens, and whether it's password-protected or damaged.
2. **Extract,** by type:
   - Text, Markdown and HTML: read as they are.
   - Word and other documents (DOCX, DOC, ODT, RTF): converted by LawCraft's wordcraft-cli and checked against the document's own word and page counts.
   - Spreadsheets (XLSX and CSV): one Markdown table per sheet, checked by row and column counts.
   - PDF: page by page. A page with a usable text layer is read as text and checked against an OCR read of the same page; a scanned page goes to the OCR model and is checked against a second read by Tesseract, which ships in the core image. Each page is saved as it finishes, so a failure resumes at that page, never at page 1.
   - Photos: OCR'd and described (what it shows, plus any text in it) by the vision model. Photos of one multi-page document are grouped: once the matter has had no new photos for 10 minutes, the vision model looks at the full pages and decides which photos belong together and in what order. Each group becomes one PDF, with the photos as its pages in order, and one Markdown file; the photos stay linked to it as its sources. A grouping is decided once.
   - Audio: transcribed by the speech-to-text model, with speakers and timestamps where the model gives them. The transcript is checked for coverage (timestamps run the whole recording with no unexplained gap over 30 seconds) and, where a check model is set, against a second transcription compared in 5-minute windows.
   - Video: the audio transcribed as above, plus one frame a second described by the vision model, in one Markdown report that puts the transcript and what's on screen on one timeline. Zoom recordings get the transcript only.
   - Anything else: an unsupported-format exception; the file stays available.
3. **Label:** the labeling model reads the checked Markdown and gives the name parts (what it is, its own date), a summary readable on hover, and the critical fields (names, dates, amounts, case numbers), each cited to its page or timestamp, with a confidence for each.
4. **Categorize:** one category from the admin's list. High-tier categories (default `court_filing`, `court_notice`, `discovery`, `agency_notice`, `police_criminal`, `estate_planning`, `contracts_agreements`) are categorized by both the labeling model and its check model and set only when they agree.
5. **Match the matter:** a file that arrived on a matter keeps it. A file with no matter, or whose parties and case number point to another matter, goes to the review queue with the best match; Doc Tender never moves a file between matters on its own.
6. **Name and file:** the file is renamed to the naming pattern, and that's its only name. A clash in the same matter adds -2, -3 and so on. It's filed into its category's folder.
7. **Publish,** in one transaction: the Markdown copy, the record, search and a timeline event.

**Naming pattern:** default `<clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>`, lowercase with hyphens, for example smith-2026-0042-motion-to-withdraw-2026-10-15.docx. The doc date is the document's own date (YYYY-MM-DD), left off when it has none. The client last name is the primary client's last name, or an organization's name cut to 20 characters. The admin builds the pattern on the Doc Tender tab from tokens (client last name, client first name, matter number, matter name, what it is, document date, received date, category). A status token is refused, because status never goes in a name. Doc Tender sets the name once; after that only a person renames a file. The name a file arrived with stays on the record for page order and audit and is never shown as its name.

**Categories:** the default list below, each with its description, tier and folder. The admin edits, adds and retires categories; a retired category stays on the files that have it.

| Category | Covers | Folder |
| --- | --- | --- |
| `court_filing` | Pleading, motion, petition, order, judgment, exhibit, or a party's notice of hearing | Pleadings |
| `court_notice` | Clerk or e-filing notice: filing confirmation, Case.net page, hearing notice or link | Pleadings |
| `discovery` | Interrogatories, requests for production or admission, responses, depositions | Discovery |
| `correspondence` | Letter or email to or from the client, counsel or a third party, including plea offers | Correspondence |
| `agency_notice` | Letter from a government agency, such as child support, IRS, DOR, Children's Division or MSPD | Correspondence |
| `estate_planning` | The will, trust, power of attorney, directive or beneficiary deed itself | Client documents |
| `property_title` | Deed, deed of trust, mortgage release, secured note, survey, title | Client documents |
| `financial_record` | Bank or tax record, pay stub, statement, invoice, income and expense or asset and debt form | Client documents |
| `personal_record` | ID, birth, death or dissolution certificate, driving record, completion certificate | Client documents |
| `medical_insurance` | Medical record or evaluation, health or vision insurance document | Client documents |
| `contracts_agreements` | Contract between parties, prenup, articles, bylaws or operating agreement | Client documents |
| `police_criminal` | Police report, probable cause statement, citation, bodycam report, restitution | Evidence |
| `evidence_media` | Photo, video, screenshot, text messages or call log, including phone message exports | Evidence |
| `call_meeting` | Recording, transcript or notes of a phone call, voicemail or meeting | Notes |
| `engagement_intake` | This firm's fee agreement, invoice, intake or consultation notes, MSPD forms | Billing |
| `firm_work_product` | This firm's memo, research, hearing or trial prep, case notes | Notes |
| `reference` | Article, statute, template, logo or blank form; not about a client matter | Notes |

**Markdown copy:** every processed file gets a Markdown copy, kept for good. It's stored on the same backend as the original, with the original's base name and the .md extension (smith-2026-0042-motion-to-withdraw-2026-10-15.md), linked to the same file record and matter, and covered by the same access, walls, backups and retention as the original. If the file is renamed, the copy's name follows. A grouped photo set has one Markdown copy for the group; a Markdown original is its own copy. Its header names the source file and version, the pages, and the models and check results, and any unresolved spot is marked in place (for example \[unresolved: page 4\]). Search, AI features and modules read the Markdown, through the API. Reprocessing adds a new Markdown version and never overwrites the old one. The planned matter wiki will build from these copies (section 9.8).

**Exceptions and the review queue:** low confidence (below 0.90 by default) on a name part, category or matter; check models that disagree; a matter it can't match; extraction that failed after its retries; a password-protected, damaged or unsupported file; a provider that stays down for an hour. Each item shows the file, what's wrong and Doc Tender's best answer, with Accept, Correct, Assign matter, Retry and Keep as is (the file stays available with its flag). A new exception emails the admin at once, bundled to at most one email every 15 minutes, and raises a notification. The queue is the Doc Tender entry in the sidebar's Documents section, with a live count, for admins and attorneys.

**Retries:** each unit gets up to 6 attempts over about 38 hours (2 at once, then after 15 minutes, 2 hours, 12 hours and 24 hours), then the check method, then an exception. A provider health check runs before calls; while a provider is down, no attempts are spent and jobs wait. A worker that stops loses its lease after 15 minutes, and the next one resumes at the first unit without a passing check.

**Cost:** every model call is in the AI call log with tokens and cost. The Doc Tender tab shows spend by day, matter and file type, and the daily summary email gives files processed, open exceptions with their ages, and spend.

**Guide:** the user guide has a Doc Tender topic: what it does with each file type, how names are made, what the review queue holds and how to clear it, and where its settings live. It opens from the ? icons on the Files tab, the review queue and the Doc Tender admin tab.

API: /api/v1/doc-tender/jobs (list, status, retry), /api/v1/doc-tender/exceptions (list, resolve), /api/v1/doc-tender/categories and /api/v1/doc-tender/naming (admins write), all also through /mcp. Events: doc\_tender.published and doc\_tender.exception\_opened.

## 9. Module interface

### 9.1 What a module is

Modules are self-contained. Each runs as its own container and reaches core records only through the API. Its own records live in its own schema in the firm's Postgres (section 9.9), and its files are core file records (section 8.19). A module never reads or writes core tables.

### 9.2 Manifest

Each module ships a signed togavsu-module.yaml:

- id, name, version, author, homepage, license, price note
- container image reference and digest
- permissions requested (API scopes, for example matters:read, time:write)
- widgets (id, title, default size, sizes allowed, click-through target: one of the module's own tabs or a core page, with the filter parameters it passes); install refuses a widget with no target
- help guide (the heading its pages file under, default the module's name; each page's title, Markdown body, search keywords and the widget, tab or screen it covers)
- tabs (home-screen tabs and matter-screen tabs)
- matter-screen items (panels and actions); sidebar entries, quick-create items, search scopes and help sections (sections 8.9 to 8.11)
- event subscriptions
- data: numbered schema migrations, the core records it extends (custom fields or extension tables) and the read views it publishes (section 9.9)
- replaces (optional): time, trust or calendar
- settings schema (rendered by the core as the module's settings form)

### 9.3 Install and permissions

- Install from the marketplace or from a local file.
- The core verifies the module's signature before anything runs. Accepted signers: keys published through the marketplace, plus any keys the firm admin adds as trusted local keys. A firm that patches a module signs its build with its own local key.
- The admin sees every requested permission and approves or declines them at install. A declined required permission cancels the install. A new version asking for new permissions waits for the admin's approval; it keeps running on the old version meanwhile.
- The core issues the module a scoped token. Every module call is checked against its approved permissions and written to the audit log under the module's id.
- Install, update, disable and remove happen without restarting the core.

### 9.4 UI

- A module's widgets, tabs and matter-screen items are pages the module serves. The core shows them in sandboxed iframes at /m/{module-id}/ through caddy.
- The core hands each frame a short-lived signed context token (user, roles, matter in view). The module never sees the core session cookie.
- A module's dashboard panel can show on the home screen as a widget or on its own tab.

### 9.5 Replacing a core domain

- A module that declares replaces: trust (or time, or calendar) implements the core's OpenAPI contract for that domain.
- When the admin switches the domain to the module, the core sends that domain's API calls and screens to the module and keeps its own data read-only. Switching back restores the core's version.
- The trust below-zero rule and flag emails are part of the trust contract; a replacing module must raise the same flag event.

### 9.6 Email module notes

One module runs the firm's whole mail system, sending and receiving, and files mail to matters. It connects to the mail the firm already uses (Gmail, Outlook and standard IMAP/SMTP). It uses the same interface as every other module: matter-screen mail tab, home-screen inbox widget, matters:read and notes:write scopes for filing, and an outbound send endpoint the core can route system mail through. Running its own mail server is a later module.

### 9.7 Modules named for later

Document templates and assembly, discovery/RFP, intake and questionnaires, email, Zoom, LawPay, client portal, QuickBooks sync, record importers (MyCase, Clio and others), the matter wiki, self-hosted mail server. The core ships with none of them; this build proves the interface with one sample module (section 11). Section 9.10 adds the first five as modules built before release.

### 9.8 Planned modules

These come from Nolan PMS Unified and Doc Tender and are built later as modules, each on the core's records, files and editor. This build doesn't spec or build them, except items 1, 5, 6 and 8 and QuickBooks sync, which section 9.10 adds.

1. Documents and templates: templates, document assembly and generation, bulk download and extra versioning tools. It builds on the core's file records, editor and Doc Tender labels, and keeps no file store or editor of its own.
2. Discovery: discovery sets, received RFPs and interrogatories, objections, exhibits.
3. Intake: phone intake, intake forms, questionnaires with uploads, veterans evaluations.
4. Meeting prep and design sheet.
5. Email filing: unfiled email queue, IMAP filing, Outlook add-in, quick email.
6. Client portal and messages.
7. Zoom: softphone, click-to-call, call triage, call log.
8. LawPay refunds and DocuSeal signing for fee agreements.
9. Opening invoice import.
10. Workflows.
11. Google account banner and NotebookLM panel.
12. Matter wiki: an individualized wiki for each matter, built from Doc Tender's saved Markdown copies. Named here only; this PRD doesn't spec it.

### 9.9 Module data and database extension

There's one database. Core records live in the Postgres schema core and each module's records in a schema of its own, so modules extend the base and never rebuild it.

- **Own schema:** at install the core creates the module's schema (mod\_ plus the module id) and a database login that can read and write only that schema, handed to the module as a Docker secret. The login can't see core tables or any other module's schema.
- **Versioned migrations:** a module ships its tables as numbered SQL migrations inside its signed package. The core applies them, not the module: at install, and at each update before the new version starts. Each runs once in its own transaction and is recorded in schema\_migrations with its checksum. A failed migration rolls back and the module stays on its old version. A changed migration that already ran is refused. Migrations follow expand-then-contract, so the old version keeps running during an update.
- **What a migration may do:** create, alter and drop tables, indexes and views in the module's own schema, and add foreign keys to core records' ids. Nothing else: no statements outside its schema, and no functions, triggers, extensions, roles or grants. The install check parses every migration and refuses a package that breaks this.
- **Link, don't copy:** module tables point to core records (contacts, matters, users, invoices, time entries, files and the rest) by id, through foreign keys Postgres enforces. A module never stores its own copy of a core record. The manifest declares which core records the module extends. The install check refuses a migration whose table duplicates a core record type: one named for a section 5 record or a plain synonym (contacts, people, clients, matters, cases, users, invoices, payments, files, documents, events), or one holding a core record's identifying fields (a name with an email, phone or address, for example) without a foreign key to that record.
- **Extension points:** a module adds fields to a core record without a parallel table, in one of two ways: custom fields (section 8.14) declared in its manifest, shown in the record's Custom Fields card under the module's name; or an extension table in its schema whose primary key is the core record's id, one row per record, declared in the manifest as that record's extension.
- **Deletes and merges:** foreign keys to core records are ON DELETE RESTRICT. A core record that module rows point to can't be hard-deleted, and the core names the module that holds it. When the core merges two records, it moves the module rows' links in the same transaction.
- **Reads across tables:** core read and list endpoints take include=, naming a module's extension tables and views (for example GET /api/v1/matters/{id}?include=matter-clock.targets). The core runs the join itself, in one query, inside the viewer's access: walls, roles and attorney-only fields apply to module rows through the core record they point to. So any screen, a module tab or a matter list column included, can pull from core and module tables at once. A module table not keyed to a core record is private to its module and can't be included.
- **Published views:** a module can publish read views in its manifest (name, the core record it's keyed to, columns). Another module reads them through the core API under the scope views:{module-id}.{view}:read, which the admin approves at install like any other scope. Modules never connect to each other's schemas.
- **A module's own reads:** a module reads and writes its own schema directly and reaches core records only through the API. When it shows rows to a user, it shows only rows for core records the API returns to that user; reading through include= does that by itself.
- **Disable and uninstall:** disabling keeps the schema. Uninstall asks whether to keep the module's data (the schema stays, locked, and a reinstall picks it up) or delete it. Delete runs only after a backup that includes the schema and the admin's typed confirmation; then the core drops the schema and its login.
- **Backups** include every module schema with the database.
- **Core migrations** follow the same process (section 6), so the base grows over time during overnight updates, with no restart.
- **Files** are never stored in a module schema (section 8.19).

### 9.10 Initial modules

On Nov 12, 2026 Patrick decided that Togavsu is not ready for release until the first five modules exist. They are built now, before release, as signed modules under every rule in sections 9 and 9.9:

1. Documents and templates (9.8 item 1).
2. Email filing (9.8 item 5), connecting to the firm's existing Gmail, Outlook or IMAP mail. The self-hosted mail server stays a later module.
3. Client portal and messages (9.8 item 6).
4. LawPay refunds and DocuSeal signing for fee agreements (9.8 item 8).
5. QuickBooks sync for the read-only accountant role (9.7).

- **Specs:** each module is specified in docs/modules/<id>/SPEC.md, written from Nolan PMS Unified's working code and reusing its working approach, built clean. QuickBooks sync has no Unified code; its spec follows this PRD and the QuickBooks Online API.
- **Core additions:** the core changes these modules need are listed in docs/modules/CORE-CHANGES.md (CORE-01 to CORE-29) and are core scope for this build. They follow every core rule: walls, roles and attorney-only redaction applied in services across UI, API, MCP and module extensions; the append-only trust ledger with reversals; audit entries; and API and MCP parity. A module never sees a walled matter's records unless the wall lets that user see them; no service principal crosses a wall.
- **Acceptance:** each module's acceptance is the MOD criteria in its spec. They add to section 12 and replace none of it.
- **Release:** release waits until these five modules and their integration are merged.
- **Outside services:** LawPay, DocuSeal, QuickBooks and the mail providers (Gmail, Outlook and IMAP/SMTP servers) run against fake adapters or local test servers in every test; no test reaches a real outside service. Keys and mail sign-ins are entered by the firm admin at runtime; the build creates no credentials and never uses client data.
- **Shipping:** the core still installs with no module. Firms install these five the way they install any signed module.

## 10. Marketplace

### 10.1 Operator stance

The operator does not review, inspect, test or vouch for modules. Review would imply liability for them. Signing proves who published a module, not that it works.

### 10.2 Listings

Each listing shows the author, the author's verified signing key, the permissions the module requests, the version history, the price if any, and this notice: "Modules are published by their authors. The Togavsu marketplace does not review, test or warrant any module. Install at your own risk."

### 10.3 Flagging

- The operator can flag a module, or one version, with a short note.
- A flagged listing shows a warning: "We've been alerted that this item has problems." plus the note.
- Installs stay possible; the warning shows again at install.

### 10.4 Pulling a module

- The operator can pull a module off the marketplace. A pulled module disappears from search and can't be installed from the marketplace.
- Firms can still install it from a local file. The marketplace doesn't control code a firm already has.

### 10.5 Firms already running it

- Each core checks the marketplace daily, and when an admin opens the modules screen, for flags and pulls on installed modules.
- On a new flag or pull: a module alert on the admin dashboard and an email to every admin, carrying the warning text and the operator's note.
- Nothing is switched off remotely. The firm admin decides: keep running it, disable it, update it, remove it, or patch it and run their own signed build.

### 10.6 Marketplace service

A small Go service in the same repo (/marketplace), deployed separately: author accounts with signing-key registration, module upload with signature check, listing pages, search, flag and pull controls for the operator, and a read API the core uses for browsing, installs and alert polling.

## 11. Build plan

The build runs in Cursor Cloud Agents from start to finish, with GitHub as the canonical source and Codex as the independent checkpoint reviewer. No phase waits for approval. Each phase ends at an automated checkpoint: executable tests and UI QC pass, Codex reviews the integrated revision, the reporter files its report, and ready work in the next phase starts. A failed checkpoint gets fixed inside the phase. The operational workflow is docs/build/WORKFLOW.md; assignments and ownership are in docs/build/tasks.json.

**Where it runs:** the build, its tests and its test installs run in isolated Cursor-hosted cloud VMs and GitHub-hosted CI runners, not on Patrick's machines. Each implementation agent has its own branch and workspace. Render-1 holds the control checkout and launches cloud jobs; no product build or test install runs there. No client data is used anywhere; all tests use generated seed data.

**Agents:**

- **Lead (orchestrator):** owns the repo, shared contracts, dependency and migration allocation, phase plan, integration and merges. Starts with at most four concurrent Cursor implementation agents; increases concurrency only when assignments have independent ownership and prerequisites. Writes no feature code itself when a worker can.
- **Codex checkpoint reviewer:** independently reviews each PR at its exact base/head SHAs, and each integrated phase. It does not author the implementation it approves. Missing, failed, stale or incomplete reviews block merges; every new commit requires another review.
- **Platform:** Compose stack, caddy, supervisor, updater, backups, Cosign signing, CI and release.
- **Data:** schema, migrations and the module migration checks, repository layer, settings registry, seed data generator.
- **Identity:** auth, two-factor, roles, matter walls, API tokens, audit log.
- **Practice:** contacts, matters, parties, notes, conflicts, markers, reopen, timeline, activity feed, notes page, missing case number list, custom fields, edit history.
- **Billing:** time, timers, expenses, invoices, payments, PDFs, billing reports, billing worklist, collections panel, fee items catalog.
- **Trust:** trust accounts, ledger, flags, replenishment, reconciliation, trust reports.
- **Calendar:** events, deadlines, reminders, iCal feeds, available dates.
- **Files:** file records, storage backends and their adapters, uploads, downloads, previews, moves, the drop folder, the LawCraft editor build and page, and the editor API.
- **Doc Tender:** extraction for every file type, checks, photo grouping, labeling, categories, naming, Markdown copies, the review queue and cost reporting. It builds a generated test corpus with no client data: documents, scanned-looking PDFs made from them, photographed pages, synthetic recordings and video, and a stand-in for each way the old doc-tender failed (an old .doc file, a 120-page scanned PDF that fails partway, a page set that was merged and then split, a password-protected PDF).
- **UI:** shell (sidebar, header, mobile tab bar), search, notifications, help center, user guide and role tutorials, matter screen, home-screen grid and widgets, admin page and all its tabs, appearance, profile, my settings, notification preferences.
- **Modules:** manifest, signature checks, runtime via supervisor, iframe UI bridge, domain replacement, module schemas with extension tables, include reads and published views, sample module, marketplace client and marketplace service, AI settings.
- **API:** OpenAPI generation, MCP endpoint, webhooks, bulk endpoints, API docs, llms.txt.
- **QA:** writes and runs unit, integration and end-to-end tests for every phase; runs the security checks.
- **Reporter:** after every phase, posts a report as a comment on a tracking issue in the repo that mentions Patrick, so GitHub emails him: what shipped, test results, anything that failed and how it was fixed. A final report closes the build.

Sub-agents run in parallel wherever their work doesn't depend on each other, so the lead's context stays clear.

**Parallel work and GitHub checkpoints:**

- Start four bounded assignments in parallel: Data/contracts, Platform scaffolding, API contracts and QA acceptance mapping. They have disjoint ownership; the Data agent owns the first shared contract PR. Integration resolves their contracts before any product phase passes.
- Every task identifies its PRD sections, acceptance IDs, owned files and dependencies. Workers open small PRs; they never push directly to main or merge their own changes. The lead owns shared package changes and migration numbering.
- CI runs meaningful positive and failure-path tests. Codex compares the actual diff and test evidence to the assigned requirements and emits a pass/fail report tied to both base and head. Strict branch protection requires checks again when main changes. A passing metadata check or a scaffold never establishes product acceptance.
- The lead merges only after checks pass and findings are resolved. Each phase also runs the combined acceptance cases against its integrated revision, files its report and gets a recovery tag. All seven phases and all section 12 acceptance criteria remain required.

**Simplification review (Ponytail discipline under Cursor):**

- Claude Code-specific Ponytail installation commands, hooks and slash commands are replaced by an explicit equivalent review under Cursor; the build must not pretend those commands ran.
- Every agent builds only what is needed, reuses repo code, then the Go standard library, then native platform features, then installed dependencies, and only then writes the minimum additional code. A deliberate shortcut carries a `// ponytail: <ceiling>, <upgrade path>, trigger: <condition>` comment.
- This PRD remains the explicit request. Simplification never cuts requested settings, endpoints, interfaces, guide topics, security behavior or tests, and never stops to ask Patrick about an already settled decision.
- At each phase checkpoint, independently review the phase diff for unnecessary complexity, apply only cuts preserving requirements, rerun QA and update PONYTAIL-DEBT.md. Every marker needs a concrete upgrade trigger. The reporter gives the review's net line count and ledger count.
- Before the phase 7 release tag, audit the whole repository, apply only requirement-preserving cuts, rerun all tests and finalize the debt ledger. Correctness/security go through QA and Codex independently of simplification.
- The reporter continues to post phase reports on the GitHub tracking issue mentioning Patrick. No routine checkpoint waits for approval.

**Phases:**

1. Foundation: repo, Compose stack, Go skeleton, migrations, repository layer, settings registry with its CI check, auth, roles, audit log, API skeleton with OpenAPI, MCP and llms.txt, CI with signed images.
2. Practice records: contacts, matters, parties, walls, notes, conflicts, markers, reopen, timeline, activity feed, notes page, missing case number list, custom fields, edit history.
3. Billing and trust: time, timers, expenses, invoices, payments, trust ledger, flags with email, replenishment, reconciliation, reports, billing worklist, collections panel, fee items catalog. (Billing and Trust agents in parallel.)
4. Calendar and deadlines, reminders, iCal feeds, available dates; in parallel, files and storage, the LawCraft editor and Doc Tender (Files and Doc Tender agents).
5. UI: matter screen, home-screen grid with all core widgets and role defaults, admin page and all its tabs, appearance, profile, my settings, notification preferences. (Runs in parallel with phase 4 once phase 3's APIs exist.)
6. Modules and marketplace: manifest, install flow, permissions, iframe bridge, domain replacement, module schemas and migrations, sample module, marketplace service, flag and pull alerts, AI settings.
7. Updates and release: zero-downtime updater, backups, restore, user guide rebuilt in HTML and PDF, fresh-install test on a clean Docker Desktop, release tag. The release tag waits until the five section 9.10 modules and their integration are merged and their MOD criteria pass.

**Sample module:** "Matter Clock," a small module that adds a home-screen widget, a matter-screen tab and a help guide page; the widget and tab show hours worked per matter this month. It also keeps a monthly target of hours per matter in its own schema: its migration creates matter\_targets linked to matters, its manifest declares that it extends matters and publishes a read view, and the matter screen shows the target through an include read. It exercises signing, install, permission approval, the iframe bridge, events, module data and uninstall. It's test scaffolding and doesn't ship in the release.

## 12. Acceptance criteria

The build is done when a tagged release with signed images installs on a clean Docker Desktop and every item below passes in automated tests, and every MOD criterion of the five section 9.10 modules passes in automated tests too.

- A fresh install reaches the first-run setup (firm name, first admin, two-factor) in one Compose up with no manual edits.
- Every core feature is reachable through /api/v1 and through /mcp, and the OpenAPI spec validates.
- A staff user can't see a walled matter anywhere: list, search, widget, report or API.
- An accountant can read billing and trust and can't write anything.
- A trust withdrawal that takes a client below zero posts, raises a flag, and sends email to the admins and the responsible attorney.
- Trust ledger entries can't be edited or deleted through any path; reversals work.
- Reconciliation shows a zero difference on seeded data and the right difference when the statement balance is off.
- Invoices draft from unbilled time, approve, send by system mail, accept payment, and void back to unbilled.
- A matter with an unpaid invoice or a leftover trust balance closes after a warning.
- Home-screen layouts save per user; a user saves a named layout and marks it default, and after a reset that layout is offered first and restores exactly; the role default the admin set stays available; an 11th saved layout is refused.
- Clicking the upcoming deadlines widget opens the deadlines page with the same date range, and clicking one deadline opens it; a staff user's click never reaches a walled matter; the sample module's widget opens the tab its manifest names; a manifest with a widget that has no click-through target is refused at install.
- The sample module installs from a local file, asks for its permissions, shows its widget and tab, receives events, and uninstalls, all without a core restart.
- An unsigned or tampered module is refused.
- A module switched in for calendar takes over calendar screens and API; switching back restores the core's.
- A flagged module shows the warning on its listing and alerts installed firms by dashboard and email; a pulled module can't be installed from the marketplace and can still be installed from a local file.
- An overnight update swaps the core with no failed requests during a load test; a bad image (failed signature or health check) is refused and the old version keeps running.
- p95 page and API response under 300 ms on seed data of 50 users, 20,000 matters and 1,000,000 time entries.
- Audit log records every write with actor and source. A backup downloads to the admin's computer, copies to an S3-compatible destination and a network path, and restores to a working system with matching record counts.
- The sidebar shows only the entries a user's role and installed modules allow, with live counts; a collapsed subgroup or sidebar stays collapsed for that user on another computer.
- Search finds a matter by number, a person by phone and an invoice by number in each scope and in All, and returns nothing from a walled matter to a user outside the wall.
- A new trust flag raises the responsible attorney's notification count; Mark all read clears it.
- /help shows the user guide in HTML and PDF for the running version and the firm's sections; an admin adds a help item from the dashboard; a client user gets a 404 on /help/guide.
- Reopening a closed matter requires a reason, restores its earlier status and shows in the case timeline; a zero-fee matter carries its marker and stays out of AR aging.
- The edit history card shows every change to a matter with old and new values; attorney-only values show redacted to staff.
- A custom field limited to one practice area shows only on that area's matters, saves through the UI and the API, and appears in CSV export.
- A flat-fee matter with the fee in trust shows Ready to apply; applying it posts the trust transfer and marks it Collected.
- Available dates lists only days with no court date, block or all-day event between the dates given, in pleading and letter form, and never changes the calendar.
- An Appearance change shows on the next page load with no restart; a module's AI call runs on the key in AI settings and the module never sees the key; /llms.txt loads without sign-in and holds no firm records.
- Every default named in this PRD is a setting on its admin page tab with that default; changing one there takes effect on the next request with no restart and logs the old and new values; Reset to default restores it.
- A CI check fails the build when core code reads an environment variable outside the bootstrap package or uses a tunable value that isn't in the settings registry; the release build passes it.
- With the primary AI provider unreachable, a module's AI call falls back to the secondary provider and the call log shows both.
- Every screen and every admin tab has a guide topic, opened from its ? icon, that says what it does and where it lives; a CI check fails the build when a screen or tab has none; each role's getting-started tutorial opens from /help.
- The sample module's guide appears in the help center under its own heading and in search after install, opens from its widget's ? icon, refreshes on update and disappears after uninstall, all without a restart.
- Every phase report gives the explicit simplification review result and the debt ledger count; the released repo holds PONYTAIL-DEBT.md, and every `ponytail:` marker in it names an upgrade trigger.
- Every record in section 5 exists after a fresh install, and the seed data fills each one.
- The sample module's migration creates matter\_targets in its own schema, linked to matters; the matter screen shows its target through include=; its login can't read core contacts; a staff user outside a wall gets none of its rows for a walled matter; a second module reads its published view only after the admin approves the scope.
- A module package whose migration creates its own contacts table, writes outside its schema or adds a trigger is refused at install; a changed migration that already ran is refused on update; a failing migration rolls back and the old version keeps running.
- Uninstall with Delete data drops the schema only after a backup and the typed confirmation; a core record that module rows point to can't be hard-deleted.
- A 1 GB upload over a connection that drops resumes and its SHA-256 matches; a file on a walled matter can't be listed, previewed, downloaded or searched from outside the wall; every save adds a version and no version changes.
- Moving all files from local disk to S3-compatible storage and then to Dropbox keeps every file readable during the move, checks every SHA-256 and deletes old copies only after the admin confirms; a changed byte on a backend fails the read and emails the admins; a restored backup brings back files from every backend with matching SHA-256s.
- A DOCX opens in the editor, saves as a new version and opens in Word with no repair prompt; a second user sees the edit lock, which frees 5 minutes after the editor closes without saving; New Pleading fills its caption from the matter; the Document editor tab shows commit 770a8fab12392485c0c2789fb254da7d9296df4d.
- An editor run fills a document's merge fields and saves a new version; a run that tries to read a local file is refused; a conversion to PDF makes a new file linked to its source.
- A staff upload, a client upload, a file written by the sample module and a drop-folder file are each processed within 10 minutes of arrival with no nightly run, and each comes out with the pattern name as its only name, a category, a summary, its folder and a saved Markdown copy.
- A 120-page PDF with scanned pages processes page by page, and a failure injected at page 49 resumes at page 49.
- Eight photos of a three-page letter and a five-page lease, uploaded together, come out as two PDFs and two Markdown copies with the pages in order.
- An audio file and a video come out with Markdown transcripts with timestamps, and the video's report adds what's on screen.
- A file on a closed matter makes no model call, and reopening the matter processes it.
- A password-protected PDF, a damaged file and a low-confidence label each land in the review queue, email the admin, and stay listed, viewable and downloadable with a flag.
- A name, category or matter a person set is never changed by a later reprocess; reprocessing adds a Markdown version and keeps the old one.
- The Markdown copy has the original's base name with .md, follows the original's walls, is in backups and is what Files search matches.
- A naming pattern edited on the Doc Tender tab names the next file, and a pattern with a status token is refused; a category added there is used for the next file.
- With test keys set, the AI call log shows gpt-6.1-sol for labeling, and the AI tab offers no local endpoint for labeling or categorizing.
