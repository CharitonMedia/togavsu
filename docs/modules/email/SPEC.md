# Email module specification

Module id `email`. PRD sections 9.6 and 9.8 item 5. This spec is complete: every decision is made here, and the build runs from it without stopping.

## 1. Purpose and scope

One module runs the firm's whole mail system (PRD 9.6). It connects to the mail the firm already uses (Gmail through the Gmail API, Microsoft 365 and Outlook.com through Microsoft Graph, and any standard IMAP/SMTP server), reads it, sends it, and files it to matters. It uses the same interface as every other module:

- a matter-screen Mail tab and a Recent mail panel;
- a home-screen Inbox widget and an Unfiled email widget;
- the unfiled email queue, with a sidebar count badge;
- automatic filing of client mail and court notices to matters, written as core files and a core note (`matters:read` and `notes:write`, plus `files:write`);
- quick email from the header's Quick create and from the matter screen;
- the Outlook add-in, which suggests the matter for an open message, files it, and files the sent copy of a reply;
- an outbound send endpoint the core routes system mail through when the admin turns that on (PRD 8.8).

The module never reads or writes core tables. It reaches core records only through the API, keeps its own rows in schema `mod_email`, and stores every filed message and attachment as a core file record (PRD 8.19), so Doc Tender processes each one like any other arriving file (PRD 8.21, Sources: mail filed by the email module).

## 2. What Unified does today, and how it carries over

Unified's mail code is a working system. The module keeps each working idea and writes it clean in Go.

- **Outbound send helper** (`src/lib/email.ts`, `sendEmail`). One SMTP transport; every attempt logged to `email_log` with status, provider message id and caller context; a dev guard `EMAIL_SENDING_DISABLED` records the attempt and sends nothing. Carries over as the `outbound` table (one row per attempt, with kind and status) and the `sending_disabled` setting.
- **Sends that callers must branch on** (`src/lib/fee-agreement-email.ts`). Kept apart from `sendEmail` because the general helper swallowed errors. Carries over as a rule: every send in the module returns a definite sent or failed result with the reason, and the system mail endpoint answers with a status code the core acts on (section 7, CC-1).
- **Quick email** (`src/app/api/email/send/route.ts`, `src/components/QuickEmailPanel.tsx`). To, subject, body, optional matter; an empty or invalid To or empty body is refused and nothing is sent; the matter record is written only after a confirmed send; a send that went out but failed to record returns `recorded: false`. Carries over unchanged in behavior. The record becomes a core note plus the sent copy as a core file, instead of Unified's `message_threads`/`messages` rows, because portal messages belong to the client portal module.
- **Mailbox worker** (`scripts/email-filer-worker.ts`, `src/lib/email/mailbox.ts`). Polls on an interval, INBOX first, then every subfolder except Archive, Sent, Trash, Drafts, Junk, Spam, Filed and Review-Later; skips `\Noselect` containers and empty folders; one run at a time. Carries over as the sync loop for every account (section 8.2). Unified's two fixed mailboxes become accounts with a role: Unified's clients@ is a `filing` account, efile@ is a `court` account, and an attorney's own mailbox is a `personal` account.
- **Claim before work** (`src/lib/email/process.ts`, `claimLogRow`). The message is claimed in the ledger with an insert that ignores conflicts on (mailbox, message id) before any write, so concurrent runs and restarts never file twice. Carries over as the unique key (account, provider message id) plus a second unique key on (matter, RFC Message-ID) in `filings`.
- **Dry run and no-retention modes** (`process.ts`, `RunOptions`). Carry over as the account's `paused` status (read and match, file nothing, touch nothing at the provider) and the after-filing action `leave`.
- **Plus-address token** (`process.ts`, `plusToken`, `resolveToken`). The token in Delivered-To or Envelope-To (`filing+2026-0042@`) names a matter by matter number or case number (exactly one matter, any status) or by a client last name (exactly one open or on-hold matter). Carries over unchanged for `filing` accounts.
- **Address matcher** (`src/lib/addin/email-matcher.ts`). Firm addresses are never candidates; exactly one non-archived contact must hold one of the message's addresses, and that contact must be a party on exactly one open or on-hold matter; any ambiguity files nothing and records the reason (`no_external_address`, `no_contact`, `multiple_contacts`, `no_open_matter`, `multiple_open_matters`). Direction: firm From means outbound, else inbound. Carries over unchanged; the address and party lookup moves into one core call (CC-3) because the module can't read core tables.
- **Court notice matcher** (`src/lib/email/court-matcher.ts`, `resolveEfile` in `process.ts`). The full case number is used exactly, never with its count suffix stripped; the trial pattern first, the appellate pattern as a fallback; a matter matches on its case number field or on the number written in its name, boundary-aware so a base number never matches a suffixed one; matters of any status count; the party text must corroborate the matter's client last name; failing a number match, a client first and last name in the caption (including "Filed on behalf of:") must point to exactly one matter. Carries over unchanged.
- **Model pick for court notices** (`src/lib/email/sol-matcher.ts`). When the deterministic steps leave candidates whose client shares a surname with the caption, a strong model picks one or says none; it is asked once per message, and a message it declined (`sol_none`) is retried on the deterministic steps only. Carries over as the `court_notice_pick` AI task run through the core (`ai:use`), on the AI tab's labeling model.
- **Hourly re-match** (`src/lib/email/rematch.ts`). Unfiled court mail is re-matched every hour under one advisory lock, because a notice often arrives before the matter carries its case number. Carries over for every unfiled message, and also runs at once when a `matter.*` or `contact.*` event arrives.
- **Render with links kept** (`src/lib/email/render.ts`). A header block (From, To, Cc, Date, Subject) over the HTML body, or the text body in `<pre>`; every remote resource blocked; anchors left live. Carries over as the filed HTML copy of the message (section 8.4). The module files that HTML instead of printing it to PDF with Chrome: Doc Tender reads HTML as it is, the core makes the PDF preview of every HTML version it saves, and the links stay live in the HTML.
- **Filing** (`src/lib/email/filing.ts`). The message copy plus each real attachment (inline cid images skipped) filed to the matter, attachments as children of the message, one timeline record, no time entry. Unified's filename-collision data loss (two same-day emails overwriting one file) cannot recur: core files are keyed by version id and Doc Tender names them. Carries over as core files linked by `attachment` file links plus one core note.
- **Retention** (`process.ts`, `applyPlan`; `mailbox.ts`). clients@ mail is deleted after a clean file; court mail is never deleted, only marked read and moved to Filed; unmatched mail is left where it is; an error leaves the message and retries next run. Carries over as the account's after-filing action, with delete allowed only on `filing` accounts and refused on `court` accounts by a database check.
- **Archive filing** (`fileNoticeToArchive`, the `mycase_archive` link table). Unified-only: Togavsu has no separate archive database; old cases come in through the record importer module as closed core matters, which the court matcher already covers because it matches any status. Not carried over.
- **Unfiled queue** (`src/app/unfiled-email`, `src/app/api/email-filing/*`). Lists unmatched and error items with mailbox, from, subject, received, attachment count and match reason; Match opens a two-pane modal with a sanitized preview beside a matter picker showing client name, matter number and status; Dismiss records the actor and reason; Defer moves the message to a Review-Later folder and the worker never re-queues it; Defer all clears the queue after a confirmation; a live count badge in the sidebar. Carries over unchanged, minus the archive-case picker.
- **Preview** (`src/app/api/email-filing/[id]/preview/route.ts`). A self-contained HTML document with escaped headers and a DOMPurify-sanitized body, shown in an iframe. Carries over with a Go allowlist sanitizer and a sandboxed, script-free Content Security Policy.
- **Per-run summary** (`src/lib/email/summary.ts`). One email per run that filed something, grouped by matter, plus the unfiled count; nothing on a quiet run; a `summary_sent_at` stamp so nothing is reported twice; a failed summary never breaks the run. Carries over, bounded to at most one summary per `summary_interval_minutes`.
- **Outlook add-in endpoints** (`src/app/api/addin/{ping,suggest-matter,matters/search,contacts/search}`). Identity probe, deterministic matter suggestion from the message's addresses, matter typeahead with client name, contact search; session-authed after the Entra path was dropped. The taskpane itself was on a deleted branch. Carries over as the module's add-in API, with a newly written taskpane and manifest, signed in through a core device grant (CC-7) because a module never sees the core session cookie.
- **Portal message notices** (`src/lib/comms/notify-client.ts`) and the rest of `src/lib/comms`. These are client portal messaging and Zoom; they belong to the client portal and Zoom modules. The portal module's notices reach clients as core system mail, which this module delivers when routing is on.

## 3. Manifest

`modules/email/package/togavsu-module.yaml`, using the fields of `internal/modules/manifest.go` plus the fields CC-1, CC-6, CC-7 and CC-8 add (`container.system_mail`, sidebar `section`/`icon`/`order`/`badge`, `device_connect` and `notifications`).

- **Identity:** id `email`; name Email; version "1.0.0"; author Togavsu build; homepage `https://github.com/CharitonMedia/togavsu/tree/main/modules/email`; license Same as Togavsu; price Included.
- **Container:** image `ghcr.io/charitonmedia/togavsu-email`, digest pinned at release; port 8080; events `/events`; system_mail `/system-mail`.
- **device_connect:** `/outlook-addin/connected`.

**Permissions** (required unless marked optional):

- `matters:read`: match mail to matters, list matters in the pickers, show a matter's mail.
- `contacts:read`: find the contact behind an address, offer recipients from a matter's parties.
- `notes:write`: record each filed or sent message as a note on its matter.
- `files:read`: attach matter files to outgoing mail and open filed copies.
- `files:write`: store filed messages and their attachments as the matter's files, and link attachments to their message.
- `events:read`: re-match unfiled mail when matters and contacts change.
- `notifications:write`: tell the right people about unfiled mail and failing accounts.
- `module_secrets:write`: keep mailbox passwords and sign-in tokens sealed by the core (CC-2).
- `ai:use` (optional): pick the matter for a court notice the rules can't place. Declined, court notices that need a pick go to the queue.
- `modules:read` (optional): sign the Outlook add-in in as the person using it (CC-7). Declined, the add-in is unavailable.
- `mail:system` (optional): deliver the core's system mail through the firm's mail (CC-1). Declined, the core keeps sending system mail by SMTP.

**AI tasks:** `court_notice_pick`: choose the matter a court notice belongs to from the candidates given, or none.

**Widgets** (roles admin, attorney, staff):

- `inbox`, title Inbox, path `/widget/inbox`, size 4x3, sizes 4x3 and 6x4. Shows the viewer's unread count and newest 10 messages from their personal account, and one line with the unfiled count they can act on. Target tab `inbox`, params `folder`. A row opens that message in the `inbox` tab.
- `unfiled`, title Unfiled email, path `/widget/unfiled`, size 4x2, sizes 4x2 and 6x3. Oldest 8 unfiled items with age and best suggestion. Target tab `unfiled`, params `account_id`. A row opens that item's file dialog.

**Tabs:**

- `inbox` (home), Mail, `/inbox`: the viewer's personal account folders (Inbox, Sent), message view, reply, reply all, forward, new, file to matter, mark read.
- `unfiled` (home), Unfiled email, `/unfiled`: the queue (section 8.5).
- `accounts` (home), Mail accounts, `/accounts`: connect and manage accounts; the viewer's own preferences; the Outlook add-in download.
- `matter` (matter), Mail, `/matter`: every message filed to the matter, grouped by thread, newest first; open, reply, forward, new message prefilled with the client.

**Matter items:**

- panel `recent-mail`, Recent mail, `/matter/recent`: the 5 newest filed messages.
- action `email-client`, Email the client, `/compose`: opens compose with the matter set and the primary client's current address in To.

**Sidebar** (CC-6): `mail`, Mail, `/inbox`, section `communications`, icon mail, order 300. `unfiled`, Unfiled email, `/unfiled`, section `communications`, icon inbox, order 310, badge path `/badge/unfiled`, tone warning.

**Notifications** (CC-8): `account_problem`, Mail account needs attention, default channels in_app and email. `unfiled_mail`, New unfiled email, default channel in_app. The core names them `email.account_problem` and `email.unfiled_mail` in the notification preferences grid.

**Quick create:** `new-email`, New email, `/compose`.

**Search scopes:** `email`, Email, `/search` (section 8.9).

**Help** (heading Email): pages `overview` (what the module does, covers the widgets), `accounts` (connecting Gmail, Microsoft and IMAP accounts; account roles; after-filing actions), `unfiled` (the queue and its actions, covers `email.unfiled` and the unfiled tab), `filing-rules` (the matching order of section 8.3 in plain words), `quick-email` (covers `new-email` and `email-client`), `outlook` (installing and signing in to the add-in), `system-mail` (routing system mail through the module).

**Events:** `matter.*`, `contact.*`.

**Data:** extends `mail` (record matter, table `matter_mail`, key `matter_id`, columns `filed_count`, `last_mail_at`, `last_inbound_at`), read as `include=email.mail`. Published view `filed_messages` (record matter, key `matter_id`, columns `message_id`, `subject`, `direction`, `sent_at`, `html_file_id`, `note_id`), read by other modules under `views:email.filed_messages:read`. No custom fields.

**Settings** (admin-entered at runtime on the module's settings form):

- `google_client_id` (string), `google_client_secret` (string, secret): the firm's Google OAuth client for Gmail accounts.
- `microsoft_client_id` (string), `microsoft_client_secret` (string, secret), `microsoft_tenant` (string, default `organizations`): the firm's Microsoft app registration for Graph accounts.
- `firm_domains` (string, default empty): comma-separated firm mail domains. Empty means the domains of the connected accounts' addresses.
- `poll_minutes` (int, default 5): minutes between syncs of each account.
- `backfill_days` (int, default 30): how far back a newly connected account is read.
- `case_number_patterns` (string, default `\b(\d{2}[A-Z]{2,4}-[A-Z]{2,3}\d{4,8}(?:-\d{1,3})?)\b;\b((?:SC|ED|WD|SD)\d{4,6})\b`): semicolon-separated court case number patterns, tried in order. The default is Unified's Missouri trial pattern then its appellate pattern.
- `court_ai_pick` (bool, default true): let the model pick among court notice candidates.
- `summary_recipients` (string, default empty): comma-separated addresses for the filing summary. Empty sends no summary.
- `summary_interval_minutes` (int, default 60): least gap between summaries.
- `sending_disabled` (bool, default false): record every send and deliver none (for staging copies).

**Where mail credentials live.** Accounts are records the admin (firm and court accounts) or the mailbox owner (personal accounts) enters at runtime on the Mail accounts tab, not manifest settings, because a firm has any number of them. The firm-wide OAuth client ids and secrets are manifest secret settings. Each account's IMAP/SMTP password, or its OAuth refresh and access tokens, go into the core's module secret store (CC-2) under the name `account/<account id>/<kind>`, sealed with the core's key exactly as secret settings are; `mod_email` keeps only the secret's name. The module never writes a credential to its schema, its volume or a log.

## 4. Data

Schema `mod_email`. Ids are UUIDv7 text, times are Unix milliseconds in BIGINT as matter-clock does, and every foreign key to a core record is `REFERENCES core.<table> (id) ON DELETE RESTRICT`. Column names avoid the install check's identifying-copy pattern: a message is a mail record, never a contact, and its link to a contact is a foreign key. Table names avoid every section 5 name and synonym (no `files`, `notes`, `events`, `documents`).

Migrations in `modules/email/package/migrations/`:

1. `0001_accounts.sql`
   - `accounts`: id; provider (`gmail`, `microsoft`, `imap`); role (`personal`, `filing`, `court`); address; label; owner_user_id (FK core.users, required for personal, null otherwise); created_by (FK core.users); imap_host, imap_port, imap_security (`tls`, `starttls`), smtp_host, smtp_port, smtp_security, login (IMAP accounts only); secret_ref; can_send; default_send (at most one true); system_mail (at most one true); plus_addressing; after_file (`leave`, `label`, `move`, `delete`); status (`connected`, `paused`, `needs_reauth`, `error`, `removed`); last_sync_at; last_error; sync_from; created_at; updated_at. Checks: `after_file <> 'delete' OR role = 'filing'`; `role <> 'court' OR after_file = 'move'`; `role <> 'personal' OR owner_user_id IS NOT NULL`. Unique on lower(address); partial unique indexes on default_send and on system_mail where true.
   - `folders`: id; account_id (FK accounts); provider_ref; name; kind (`inbox`, `sent`, `other`, `skip`, `filed`, `review_later`); uid_validity; last_uid; cursor (Gmail history id or Graph delta link); updated_at. Unique (account_id, provider_ref).
   - `oauth_states`: state (pk); account_id; user_id (FK core.users); code_verifier; expires_at. Rows older than 10 minutes are deleted on every sync tick.
   - `user_prefs`: user_id (pk, FK core.users); notify_unfiled (bool, default false); signature_text.
2. `0002_messages.sql`
   - `messages`: id; account_id; folder_id; provider_ref; rfc_message_id; thread_key; in_reply_to; reference_ids (text array); direction (`inbound`, `outbound`, `court`); from_header; from_addr; to_addrs, cc_addrs (text arrays); delivered_to; subject; sent_at; received_at; snippet (first 200 characters); body_text; body_html (sanitized); raw_sha256; raw_size; unread; status (`inbox`, `pending`, `filed`, `unfiled`, `deferred`, `dismissed`, `duplicate`, `error`); match_reason; ai_declined (bool); attempts; next_attempt_at; error; created_at; updated_at. Unique (account_id, provider_ref); indexes on (status, account_id), rfc_message_id and thread_key. Bodies are mail records shown in the inbox and queue; raw MIME bytes are never stored here.
   - `message_parts`: id; message_id (FK messages); part_index; filename; media_type; size_bytes; sha256; inline; content_id; file_id (FK core.files, null until filed). Unique (message_id, part_index).
   - `message_contacts`: message_id; contact_id (FK core.contacts); address. Primary key (message_id, contact_id).
   - `message_matches`: message_id; matter_id (FK core.matters); via (`token`, `thread`, `address`, `case_number`, `case_number_in_name`, `client_name`, `ai`); rank. Primary key (message_id, matter_id).
3. `0003_filings.sql`
   - `filings`: message_id (pk, FK messages); matter_id (FK core.matters); note_id (FK core.notes); html_file_id (FK core.files); filed_by (FK core.users, null when automatic); how (`auto_token`, `auto_thread`, `auto_address`, `auto_court`, `auto_court_name`, `auto_court_ai`, `queue`, `matter_tab`, `addin`, `sent`); attorney_only; filed_at; summary_sent_at. Unique (matter_id, rfc_message_id) through a stored rfc_message_id column.
   - `queue_actions`: id; message_id; action (`file`, `defer`, `restore`, `dismiss`, `retry`); reason; user_id (FK core.users); at. Append-only.
   - `matter_mail`: matter_id (pk, FK core.matters); filed_count; last_mail_at; last_inbound_at. The `mail` extension; one row per matter that has mail.
4. `0004_outbound.sql`
   - `outbound`: id; account_id; kind (`quick`, `reply`, `forward`, `addin`, `system`); idempotency_key (unique); to_addrs, cc_addrs, bcc_addrs; subject; body_text; body_html; body_sha256; in_reply_to; matter_id (FK core.matters, null allowed); created_by (FK core.users, null for system mail); system_kind; status (`queued`, `sent`, `failed`, `recorded`, `record_failed`); provider_ref; error; attempts; sent_at; created_at. A system mail row keeps its body only until it is sent, then the body columns are cleared and `body_sha256` kept, because system mail carries password reset links.
   - `outbound_files`: outbound_id; file_id (FK core.files); version_id. Primary key (outbound_id, file_id).
   - `summaries`: id; sent_at; recipients; filing_count; unfiled_count.
   - `received_events`: event_id (pk); action; record_id; received_at.
5. `0005_views.sql`: `CREATE VIEW filed_messages AS SELECT f.matter_id, f.message_id, m.subject, m.direction, m.sent_at, f.html_file_id, f.note_id FROM filings f JOIN messages m ON m.id = f.message_id`.

**Core records used, not copied:** contacts are matched by id (message_contacts); the filed message is a core file (the HTML copy) with each attachment a core file linked to it by an `attachment` file link; the timeline record is one core note on the matter. The note is the filing record people read: title `Email: <subject>` (cut to 200 characters); body the From, To, Cc and Date lines, a blank line, the text body cut to 20,000 characters, then `Attachments: <names>` and `Full message: <file name>`; tags `email` and one of `email-in`, `email-out`, `court-notice`. An attorney-only filing makes the files attorney-only and writes the note with title `Email (attorney only)` and body `Filed as attorney-only; open the file.`, because notes carry no attorney-only flag.

## 5. Provider adapters

One Go interface in `modules/email/provider`, used by sync, filing and send alike:

```go
type Provider interface {
	Check(ctx context.Context) error                                  // sign-in and reachability
	Folders(ctx context.Context) ([]Folder, error)                    // inbox, sent, subfolders, labels
	Changes(ctx context.Context, f Folder, cur Cursor, since time.Time, limit int) ([]Ref, Cursor, error)
	Fetch(ctx context.Context, r Ref) ([]byte, error)                 // the raw RFC 5322 message
	AfterFile(ctx context.Context, r Ref, a AfterFileAction) error    // leave, label, move, delete; marks read for court
	Move(ctx context.Context, r Ref, folder string) error             // Review-Later, created when missing
	MarkRead(ctx context.Context, r Ref, read bool) error
	Send(ctx context.Context, raw []byte, env Envelope) (string, error) // returns the provider's message id
}
```

Every adapter fetches the full raw message, so one parser (`modules/email/mailparse`, built on `github.com/emersion/go-message`) handles every provider's mail the same way, and every send is a raw MIME message built by one builder.

- **Gmail** (`provider/gmail`): REST over `net/http`. OAuth 2.0 authorization code with PKCE, scope `https://www.googleapis.com/auth/gmail.modify`; refresh handled in the adapter. Folders are the INBOX and SENT labels plus user labels. Changes: `users.messages.list` with `after:` for the first read, then `users.history.list` from the stored history id; a stale history id (404) falls back to a list from `since`. Fetch: `users.messages.get?format=raw`. Label action adds label `Togavsu/Filed`; move adds that label and removes INBOX; delete moves to trash (Gmail's own retention applies). Send: `users.messages.send` with the raw message.
- **Microsoft Graph** (`provider/graph`): REST over `net/http`. OAuth 2.0 authorization code with PKCE on `login.microsoftonline.com/<tenant>`, scopes `Mail.ReadWrite Mail.Send offline_access`. Folders from `mailFolders` (Inbox, Sent Items, subfolders). Changes: `mailFolders/{id}/messages/delta`, storing the delta link. Fetch: `messages/{id}/$value`. Move: `messages/{id}/move` to a `Filed` or `Review-Later` folder, created when missing; delete moves to Deleted Items. Send: `sendMail` with the MIME body; a message whose attachments total over 3 MB goes as a draft with an attachment upload session, then `send`.
- **IMAP/SMTP** (`provider/imapsmtp`): IMAP with `github.com/emersion/go-imap/v2` over TLS or STARTTLS; LOGIN with the stored password, or XOAUTH2 when the server is Gmail or Microsoft and the account was connected by OAuth. Changes: UID SEARCH for UIDs above `last_uid`; a changed UIDVALIDITY resets the folder and reads again from `sync_from`, with duplicates dropped by (rfc_message_id, raw_sha256). Fetch: `UID FETCH BODY.PEEK[]` so reading never marks mail read. Move uses MOVE, or COPY, STORE `\Deleted` and UID EXPUNGE when MOVE is missing. Delete is STORE `\Deleted` plus UID EXPUNGE (Unified's expunge). The folder skip list is Unified's. Send uses `internal/smtpsend` (context-bound; STARTTLS required unless the port is 465 with implicit TLS), then APPENDs the sent copy to the Sent folder, because SMTP keeps no sent copy.
- **Fake** (`provider/fake`): an in-memory mailbox set with folders, UIDs, UIDVALIDITY, labels, read flags and a sent list; it can be told to fail sign-in, time out, return a stale cursor, change UIDVALIDITY, or drop a message between list and fetch. Every test uses it or an `httptest` stand-in of the Gmail and Graph wire formats or the go-imap v2 in-memory test server; no test talks to a real mail server, and the test helpers refuse any configured host other than 127.0.0.1.

## 6. Core API calls it uses

Interactive calls are viewer calls: they carry the frame's or the add-in's context token. Background calls are service calls (the module token with no context token, run as the module's service principal, CORE-01) or carry a delivered task authorization.

- `GET /api/v1/modules/email/settings`: its own settings, secrets included (own token).
- `PUT`, `GET`, `DELETE /api/v1/modules/email/secrets/{name}`: `module_secrets:write` (CC-2).
- `POST /api/v1/mail/matches`: `matters:read` and `contacts:read` (CC-3).
- `POST /api/v1/mail/filing-grants`: `notes:write` and `files:write` (CC-4).
- `GET /api/v1/matters`, `GET /api/v1/matters/{matter_id}`: `matters:read` (pickers, access checks on filed rows).
- `GET /api/v1/matter-parties?matter_id=`: `matters:read` (client for compose and the add-in).
- `GET /api/v1/contacts?q=`, `GET /api/v1/contacts/{contact_id}`, `GET /api/v1/contact-emails?contact_id=`: `contacts:read`.
- `POST /api/v1/file-uploads`, `PUT /api/v1/file-uploads/{upload_id}`, `POST /api/v1/file-uploads/{upload_id}/complete`, `POST /api/v1/files`: `files:write` (filing).
- `POST /api/v1/files/{file_id}/links`: `files:write` (attachment links).
- `GET /api/v1/files?matter_id=`, `GET /api/v1/files/{file_id}`, `GET /api/v1/files/{file_id}/versions`, `GET /api/v1/files/{file_id}/versions/{version_id}/download`: `files:read` (attachments on outgoing mail, opening filed copies).
- `POST /api/v1/notes`: `notes:write`.
- `POST /api/v1/notifications`: `notifications:write` (CC-8), one `user_id` per recipient, kind `account_problem` or `unfiled_mail`, as a service call from the sync loop.
- `POST /api/v1/ai/calls`: `ai:use`, task `court_notice_pick`.
- `POST /api/v1/modules/email/device-context-tokens`: `modules:read` (CC-7).
- Inbound: signed event webhooks at `/events`; signed system mail at `/system-mail` (CC-1).

Every write carries an `Idempotency-Key` built from the message id, the step and the part index (for example `eml-<message id>-part-2`), so a filing stopped halfway resumes without a duplicate file, link or note.

## 7. Core changes

The module-core lane builds these from docs/modules/CORE-CHANGES.md. The core lacks each of them today; the email lane builds none of them. Each item names its merged definition there, which is the one built.

- **CC-1 System mail routing (CORE-05).**
  - Manifest: new `container.system_mail` (a module path). Install refuses it without the `mail:system` permission, and refuses a second installed module that declares it.
  - Scope: new `mail:system` in `api/contract/scopes.py`: "Receive the core's system mail and deliver it through the firm's mail."
  - Router: one `systemMailer` in `internal/core/mail.go` used by every system mail sender: billing's `smtpMailer` (`internal/core/finance.go`, `Mail:`), `adminMail` (`internal/core/documents.go`), the `core.mail_outbox` drain in `App.DeliverMail` (`internal/core/finance.go`, today `trust.SMTP`, which also carries marketplace module alerts from `internal/marketplace/alerts.go`), the UI's `trust.SMTP` mailer (`internal/core/ui.go`), the password reset mailer (`logMailer` in `internal/core/app.go`, which today only logs), and the supervisor's notice file (`internal/supervisor/mail.go`, `Outbox`, whose ponytail names this trigger).
  - Behavior: when the existing setting `mail.route_through_email_module` is true and the declaring module is installed and enabled, the core POSTs JSON `{id, kind, to, subject, text, attachments[{name, content_type, data_base64}]}` to the module path, signed with `X-Togavsu-Signature` (`SignWebhook`) and carrying `Idempotency-Key: <id>`. 202 means accepted. 422 is a permanent failure, recorded on the row. 5xx or a timeout is retried with the same key. 503 with problem code `not_configured`, or the module not running, sends by SMTP when the SMTP settings are complete, and otherwise leaves the row queued with the error. A `module_alert` or `update_failure` mail about the routing module itself always goes by SMTP when SMTP is configured.
  - `testMail` (`POST /api/v1/mail/test`) goes through the router, as its contract description already states.
- **CC-2 Module secret store (CORE-06).** `PUT /api/v1/modules/{module_id}/secrets/{name}` (`putModuleSecret`), `GET` (`getModuleSecret`), `DELETE` (`deleteModuleSecret`). New scope `module_secrets:write`: "Store and read the calling module's own sealed secrets." Only that module's own bearer may call, with the path id equal to the token's module. Values are sealed with the core key like secret settings; the audit log records the name only; backups carry them with module settings; uninstall with delete removes them. Limits: 512 secrets per module, 16 KB each, names matching `^[a-z0-9/_-]{1,128}$`.
- **CC-3 Mail matching (CORE-26, CORE-01).** `POST /api/v1/mail/matches` (`matchMail`), scopes `matters:read` and `contacts:read`, `module_delegation` `delegated_viewer_or_service_principal`: a viewer call with a context token, a service call without one. Body: `addresses`, `matter_numbers`, `case_numbers`, `name_tokens`, `known_matter_ids`, each at most 50 entries. Answer: `contacts_by_address` (non-archived contacts whose current email equals the address, case-insensitive), and `matters` (at most 50), each with id, number, name, status, case number, practice area, its client contacts' first and last names, and `via` (`address`, `matter_number`, `case_number`, `case_number_in_name`, `client_name`, `known`). Case numbers match the case number field exactly, or the matter name with a boundary that excludes a following hyphen. A service call omits every walled matter and also returns `match_token`, a task authorization naming the module and the returned matter ids, valid 15 minutes. A viewer call applies that viewer's walls.
- **CC-4 Mail filing grant (CORE-27, CORE-01).** `POST /api/v1/mail/filing-grants` (`issueMailFilingGrant`), scopes `notes:write` and `files:write`, `module_delegation` `service_principal`, as a service call. Body `{match_token, matter_id, message_key}`; the matter must be in the match token and unwalled. It returns a task authorization for that module, matter and message key, valid 15 minutes, and emits new event `mail.filing_authorized`. New `noninteractive_workflow` value `mail_filing`, accepted with that grant on `createNote` (its `module_delegation` changes from `delegated_viewer` to `delegated_viewer_or_noninteractive`), `createFileUpload`, `getFileUpload`, `putFileChunk`, `completeFileUpload`, `publishFile`, `bulkFile` and `createFileLink`, each limited to the grant's matter. New workflow value `mail_matching`, accepted on `createAiCall` with a match token, for the module's declared AI tasks, without `matter_id`.
- **CC-5 Origin on module publication (CORE-16).** `FileBulkItem` (used by `publishFile` and `bulkFile`) gains optional `origin` (`firm` or `received`), accepted only from a module principal. Omitted, the current rule stays (`receivedSource` makes source `email` received). Sent mail is filed with `origin: firm`.
- **CC-6 Sidebar entry fields and badges (CORE-08).** Manifest `sidebar` entries gain `section` (this module uses `communications`), `subgroup`, `icon` (this module uses `mail` and `inbox`), `order` and `badge {path, tone}` (tone `normal` or `warning`), as PRD 8.9 lists. `internal/modules/nav.go` uses them in place of the fixed Activity section, puzzle icon and order. `getNavContext` calls each badge path with the viewer's context token, waits at most 500 ms, caches the count 30 seconds per viewer, and leaves the badge off when the call fails.
- **CC-7 Device grants for the Outlook add-in (CORE-28).**
  - Manifest: new `device_connect` (a module path).
  - Core page `GET /modules/{module_id}/device-connect`: asks the signed-in staff user to connect a device to that module. On approval it creates the grant and redirects to `/m/{module_id}<device_connect>#grant=<secret>`.
  - `POST /api/v1/me/module-devices` (`createModuleDeviceGrant`): session only, used by that page.
  - `GET /api/v1/me/module-devices` (`listModuleDeviceGrants`): scope `profile:read`.
  - `DELETE /api/v1/me/module-devices/{device_id}` (`revokeModuleDeviceGrant`): scope `profile:write`; an admin may revoke anyone's.
  - `POST /api/v1/modules/{module_id}/device-context-tokens` (`exchangeModuleDeviceGrant`): the module's own bearer, scope `modules:read`, plus the device secret. It returns a context token for that user, like `createModuleContextToken`, valid 10 minutes, with an optional `matter_id` checked against the wall.
  - A grant lapses after 90 days unused and when the user is deactivated. New events `module_device.created` and `module_device.revoked`.
- **CC-8 Module notification kinds (CORE-03).** Manifest `notifications` entries `{kind, label, default_channels}`, named `email.<kind>` by the core and shown in each user's notification preferences, and the Go route for `POST /api/v1/notifications` (`createNotification`), which is in the contract today with no route. This module sends `account_problem` and `unfiled_mail` to one `user_id` at a time as a service call, with a link under `/modules/email/`; the core emails each recipient whose preference for the kind has email on.
- **CC-9 Frame sandbox (CORE-09).** Staff frames gain `allow-popups` and `allow-popups-to-escape-sandbox`, so the Mail accounts tab opens the provider's consent page in its own top-level window, and `allow-downloads`, so the accounts tab's add-in `manifest.xml` and filed attachments download from the frame. Frames never get `allow-same-origin`.
- **Harness:** none. The acceptance suite runs the module in process through the existing `fakeRuntime.handlers` map (CORE-29).
- **Shared files:** the module's three libraries, `github.com/emersion/go-imap/v2` v2.0.0-beta.5, `github.com/emersion/go-message` v0.18.1 and `golang.org/x/net` v0.33.0 (HTML parsing for the sanitizer), with go-imap's own SASL dependency `github.com/emersion/go-sasl`, are already pinned in `go.mod` and `go.sum` by the control change that adds these lanes, before any lane starts. The email lane never edits `go.mod` or `go.sum`, and neither does any other lane, module-core included. A lane that finds it needs another dependency proposes it in its PR for the integrate-modules lead.

No other core change is needed: event subscriptions, include reads, published views, context tokens, help registration, file links and notes exist today.

## 8. Behaviors and acceptance criteria

All criteria run with synthetic data: the firm domain `firm.example.test`, client Pat Doe at `pat@client.example.test`, matter 2026-0042, case number 26AB-CV00123, and the fake provider.

### 8.1 Accounts

- The admin connects `filing`, `court` and firm `personal` accounts; any admin, attorney or staff user connects their own `personal` account, which they then own. An accountant or client sees no Mail accounts tab.
- OAuth connects open the provider's consent page in a top-level window from the tab, with PKCE and a state row bound to the user; the callback at `/oauth/callback` stores the tokens through CC-2 and runs `Check`. IMAP accounts take host, ports, security and login on the form; the password goes straight to CC-2.
- Defaults by role: `personal` after_file `label`, can_send on; `filing` after_file `delete` (Unified clients@), plus_addressing on; `court` after_file `move`, can_send off.
- The admin sees every account's status and can pause or remove any account, but sees personal account mail only on their own account.

Criteria:

- MOD-EML-01: Connecting a fake IMAP account stores its password only through `putModuleSecret`; no `mod_email` column, log line or HTTP response holds the password.
- MOD-EML-02: A `court` account with after_file `delete` is refused by the database check; a `personal` account without an owner is refused.
- MOD-EML-03: An OAuth callback with an unknown or expired state stores nothing and answers 400.
- MOD-EML-04: A sign-in failure sets the account to `needs_reauth`, sends one `email.account_problem` notification to its owner (personal) or to the admin who connected it (firm and court), with email when that recipient's preference keeps it on (the default), and sends no second one until the account recovers.
- MOD-EML-05: A staff user's request to the accounts API for another user's personal account returns not found.

### 8.2 Sync

- Each account syncs every `poll_minutes`, one run per account at a time under a Postgres advisory lock. The first sync reads from `now - backfill_days`. Personal accounts read Inbox and Sent; filing and court accounts read Inbox and subfolders minus Unified's skip list, and never Sent.
- Each new message is parsed, stored with its sanitized body and its parts list (no raw bytes), and claimed with status `pending` by its unique key before any filing write.
- A message that can't be fetched or parsed is retried after 5 minutes, 30 minutes, 2 hours and 12 hours, then shows in the queue as `error` with Retry.

Criteria:

- MOD-EML-06: Two concurrent sync runs on one account with the same 3 fake messages produce 3 message rows and no duplicate filing.
- MOD-EML-07: A changed UIDVALIDITY re-reads the folder and adds no duplicate rows.
- MOD-EML-08: Reading a message never marks it read at the provider (the fake records no read-flag change).
- MOD-EML-09: A message removed between list and fetch is skipped without error, and the next run doesn't retry it.
- MOD-EML-10: Messages in Trash, Junk, Drafts, Filed and Review-Later folders are never read on any account.

### 8.3 Matching

The order, first decisive step wins:

1. **Duplicate:** a message whose RFC Message-ID is already filed to a matter is marked `duplicate` and linked to that filing.
2. **Plus token** (filing accounts with plus addressing): matter number or case number, exactly one matter of any status; else client last name, exactly one open or on-hold matter.
3. **Thread** (all accounts): In-Reply-To or References naming a filed message files to that message's matter when CC-3 returns it as `known` and it is open or on hold.
4. **Court** (court accounts): Unified's case number, client name and model pick steps (section 2), with `case_number_patterns` and `court_ai_pick`. The model pick runs once per message; a "none" sets `ai_declined`.
5. **Address** (filing and personal accounts): Unified's address matcher. Direction is outbound when every From address is a firm address.

No match: filing and court mail goes to the queue as `unfiled` with the reason and any candidates saved to `message_matches`. Personal mail with no contact at all stays `inbox` and never enters the queue; personal mail with a contact but an ambiguous matter goes to the owner's queue. A walled matter never comes back from a background match, so mail for it waits in the queue for someone on the wall.

Criteria:

- MOD-EML-11: Inbound mail from `pat@client.example.test`, whose contact is a party on exactly one open matter, files to 2026-0042 with how `auto_address`.
- MOD-EML-12: The same address held by two contacts goes to the queue with reason `multiple_contacts`; one contact on two open matters gives `multiple_open_matters`.
- MOD-EML-13: Mail to `filing+2026-0042@firm.example.test` files by token even though the sender is unknown; a last-name token matching two open matters goes to the queue.
- MOD-EML-14: A court notice for 26AB-CV00123-01 never files to a matter whose number is 26AB-CV00123, by field or by name.
- MOD-EML-15: A court notice whose number matches but whose party text doesn't contain the matter's client last name goes to the queue with reason `case_number_matched_but_party_name_mismatch`.
- MOD-EML-16: A court notice that matches a closed matter by exact case number is filed (court mail matches any status); client mail never auto-files to a closed matter.
- MOD-EML-17: With `court_ai_pick` on, a fake AI answer naming a candidate files with how `auto_court_ai`; an answer naming a matter not among the candidates files nothing; a "none" is not asked again on re-match.
- MOD-EML-18: A reply in a thread already filed to 2026-0042 files there even when the reply comes from an address with no contact.
- MOD-EML-19: Mail whose only candidate is a walled matter is queued, and the queue row shows no matter name to a user off the wall.
- MOD-EML-20: The same message arriving in a personal account and a filing account files once; the second copy is `duplicate`.

### 8.4 Filing

For a decided match the module: asks for a match token and filing grant (background) or uses the viewer's context token (queue, matter tab, add-in); fetches the raw message into a temporary working copy, checks its SHA-256 against `raw_sha256`, and deletes the copy when done; uploads the HTML copy (`email-<YYYY-MM-DD>-<subject words>.html`, header block plus sanitized body, remote resources stripped, links live); uploads each real attachment under its original name, skipping inline cid images; links the HTML file to each attachment (`attachment`); writes the note; records `filings`, updates `matter_mail`, and runs the account's after-filing action. Origin is `received` for inbound and court mail and `firm` for outbound (CC-5). Doc Tender then names, labels and folders each file.

Criteria:

- MOD-EML-21: A filed message with two attachments and one inline image creates three core files (HTML plus two), two `attachment` links from the HTML file, and one note tagged `email` and `email-in`.
- MOD-EML-22: The HTML copy contains no `<script>`, no event-handler attribute, no remote `src`, and keeps every `<a href>` from the source.
- MOD-EML-23: A filing interrupted after the second upload, then resumed, ends with exactly three files and one note (idempotency keys replay).
- MOD-EML-24: A filing account's message is deleted at the provider only after the note is written; a court account's message is marked read and moved to Filed and never deleted; a personal account's message gets the Filed label and stays in its folder.
- MOD-EML-25: When the raw message is gone from the mailbox at filing time, the row stays in the queue with error `message_not_in_mailbox` and offers File without attachments, which files the stored body and lists the missing attachment names in the note.
- MOD-EML-26: An outbound message filed from a personal Sent folder creates files with origin `firm` and a note tagged `email-out`.
- MOD-EML-27: `GET /api/v1/matters/{id}?include=email.mail` returns the filed count and last mail time for a viewer on the matter, and not found for a viewer off its wall.

### 8.5 Unfiled queue

The `unfiled` tab lists `unfiled` and `error` rows the viewer may see (firm and court account rows to admins, attorneys and staff; personal rows to their owner), oldest first, with account, from, subject, received, attachment count, reason and the best suggestion. Actions:

- **File:** a two-pane dialog with the sanitized preview beside a matter picker that lists the saved candidates first and searches matters by name, number or client name; an attorney or admin may tick Attorney only.
- **Defer:** moves the message to Review-Later at the provider and sets `deferred`; deferred rows show under a Deferred filter with Restore, and sync never re-queues them.
- **Defer all:** defers every row in view after a confirmation that names the count.
- **Dismiss:** needs a reason; on a filing account it runs the delete action; on a court account it marks the message read; on a personal account it leaves the message.
- **Retry** on error rows.

Each action is written to `queue_actions`. New unfiled rows send an `email.unfiled_mail` notification, at most once every 15 minutes per person, the owner of a personal account, or the admin who connected the firm or court account and every user with `notify_unfiled` on. The sidebar badge counts the rows the viewer may see.

Criteria:

- MOD-EML-28: Filing from the queue as staff creates the files and note under that user (actor staff, not module) and removes the row from the queue and the badge count.
- MOD-EML-29: Dismiss without a reason is refused; dismissing a court row never deletes it at the provider.
- MOD-EML-30: A deferred message stays deferred across 3 sync runs and returns to the queue on Restore.
- MOD-EML-31: Staff can't see, preview or file another user's personal unfiled row (not found).
- MOD-EML-32: The preview response carries `Content-Security-Policy: default-src 'none'; img-src data:; style-src 'unsafe-inline'; sandbox` and no script.
- MOD-EML-33: Changing a matter's case number to 26AB-CV00123 (a `matter.updated` event) files the waiting court notice within one re-match pass; the hourly sweep does the same with no event.

### 8.6 Mail tab, inbox and quick email

- The matter Mail tab lists the matter's filings by thread; opening one shows the stored body, or the HTML file when the stored row is gone. Reply, reply all and forward keep In-Reply-To and References and send from the account the message came in on when the user may send as it, else from their own personal account, else from the account marked default_send.
- Compose (quick create, matter action, inbox, replies) takes To, Cc, Bcc, subject, body, an optional matter, and attachments chosen from the matter's files the viewer can read (total at most 20 MB). It refuses an empty or invalid To and an empty body and sends nothing. After a confirmed send with a matter, it files the sent copy (how `sent`, origin firm, tag `email-out`). If the send succeeded and the filing failed, the answer is `{status: "sent", recorded: false}` and the message goes to the queue for filing.
- With `sending_disabled` on, every send is recorded as `sent` with provider id `disabled` and nothing leaves.

Criteria:

- MOD-EML-34: Quick email to an invalid address returns 400 and the fake's sent list stays empty.
- MOD-EML-35: Quick email with matter 2026-0042 and one matter file attached sends once, files one HTML copy and one note, and the attachment in the sent MIME matches the file version's SHA-256.
- MOD-EML-36: Quick email naming a matter the viewer is walled off from returns not found and sends nothing.
- MOD-EML-37: A failed provider send records `failed` with the reason, writes no note, and answers 502.
- MOD-EML-38: A reply sets In-Reply-To to the original's Message-ID and its sent copy files to the same matter by thread.

### 8.7 Outlook add-in

Files under `modules/email/outlook-addin/`, served at `/m/email/outlook-addin/`. The admin downloads `manifest.xml` from the Mail accounts tab, with the firm's public address filled in from the request host, and deploys it to the firm's Microsoft 365 tenant or sideloads it. The add-in needs Mailbox requirement set 1.14.

- **Sign-in:** the taskpane opens an Office dialog at the core page `/modules/email/device-connect` (CC-7); after approval the module page at `/outlook-addin/connected` passes the grant to the taskpane with `messageParent`; the taskpane keeps it in roaming settings and exchanges it for a 10-minute context token on each use.
- **Read mode:** shows the suggested matter from the message's addresses (Unified's suggest-matter, now CC-3 as the viewer), a matter search with client name, number and status, and a contact search. File gets the item with `getAsFileAsync` and posts it to the module, which files it as section 8.4 with how `addin`.
- **Compose mode:** File sent copy to matter adds a Bcc to the filing account's plus address for the chosen matter (Unified's clients+TOKEN path). Shown only when a filing account with plus addressing exists.

Criteria:

- MOD-EML-39: An add-in request with a revoked device grant gets 401 and files nothing.
- MOD-EML-40: Filing the same item twice from the add-in creates one filing.
- MOD-EML-41: The add-in suggestion returns a walled matter only to a user on that wall.

### 8.8 System mail

`POST /system-mail` verifies the signature (5-minute skew, as matter-clock checks events) and the idempotency key, then sends through the account marked `system_mail`. It answers 202 with the provider message id; 503 `not_configured` when no account is marked or that account is not connected; 422 for an invalid recipient; 502 for a provider failure. A repeated key returns the first answer and sends nothing.

Criteria:

- MOD-EML-42: A signed system mail sends once through the system mail account; replaying it returns the same id and the fake's sent list grows by one only.
- MOD-EML-43: A bad signature returns 401 and sends nothing.
- MOD-EML-44: With no system mail account, the answer is 503 `not_configured`, and with CC-1 in place the core then sends that mail by SMTP to a test SMTP listener.
- MOD-EML-45: After a system mail is sent, its `outbound` row holds no body text.

### 8.9 Search, widgets and summary

- The Email search scope matches subject, from, to and body text. It returns filed messages only when the viewer can read their matter (checked through the API, at most 25 results), the viewer's own personal mail, and firm queue rows for staff roles.
- The summary goes to `summary_recipients` through the default send account, at most once per `summary_interval_minutes`, listing filings by matter and the unfiled count; nothing goes on a quiet interval; `summary_sent_at` stops double reports; a failed summary is retried next interval and never stops sync.

Criteria:

- MOD-EML-46: An email search as staff returns no row filed to a matter walled off from them, and no other user's personal mail.
- MOD-EML-47: Two sync runs inside one summary interval that file 3 messages produce one summary listing 3; a quiet interval sends none.
- MOD-EML-48: The Inbox widget's title opens the `inbox` tab with the widget's folder; the Unfiled email widget opens the `unfiled` tab filtered to its account.

### 8.10 Lifecycle

- MOD-EML-49: Install with `mail:system`, `ai:use` and `modules:read` declined succeeds, and the module reports system mail, court AI pick and the add-in as unavailable; declining `notes:write` cancels the install.
- MOD-EML-50: Hard-deleting a core matter that has a filing is refused with the module named; merging two matters moves `filings`, `message_matches` and `matter_mail` rows in the same transaction.
- MOD-EML-51: Uninstall with keep leaves `mod_email` locked and a reinstall picks up accounts and filings; uninstall with delete drops the schema and the module's sealed secrets after the backup.

## 9. Tests

All tests are Go and run in the existing "Core tests" job (`scripts/ci/test-core.sh`, `go test ./...` against real Postgres through `TOGAVSU_TEST_DATABASE_URL`). No test reaches a real mail server, a real Google or Microsoft endpoint, or the internet.

- `modules/email/mailparse/parse_test.go`: multipart/alternative, mixed with attachments, inline cid images, quoted-printable and base64 bodies, non-UTF-8 charsets, encoded-word subjects, a missing Message-ID (the module derives `<sha256@togavsu.invalid>`), a 30 MB message (refused above 50 MB with `message_too_large`), malformed MIME (error, no panic).
- `modules/email/render/sanitize_test.go`: script, style with `expression()`, `javascript:` links, `on*` attributes, remote images, SVG and iframes are removed; anchors and tables survive (MOD-EML-22, 32).
- `modules/email/match/rules_test.go`: table tests of the section 8.3 order over injected CC-3 answers: every Unified reason code, the suffix boundary (MOD-EML-14), party corroboration (15), status rules (16), plus token (13), thread (18), duplicate (20), the AI pick paths with a fake model answer (17), and the default `case_number_patterns` against synthetic trial and appellate numbers.
- `modules/email/provider/gmail/gmail_test.go`, `provider/graph/graph_test.go`: `httptest` servers speaking each API's wire format: token refresh, stale history id or delta link, raw fetch, label and move, send, the Graph upload-session path over 3 MB, 401 to `needs_reauth`, 429 with Retry-After honored.
- `modules/email/provider/imapsmtp/imapsmtp_test.go`: the go-imap v2 in-memory server on 127.0.0.1: folder skip list, UID resume, UIDVALIDITY change (MOD-EML-07), PEEK never sets `\Seen` (08), MOVE and the COPY/EXPUNGE fallback, delete; a local SMTP listener for STARTTLS required and refused, and the Sent APPEND.
- `modules/email/store/store_pg_test.go`: applies the five migrations to a scratch schema; the checks of MOD-EML-02, the unique keys behind 06 and 20, and the system mail body clearing of 45.
- `internal/modules/module_email_pg_test.go` (package `modules_test`, so it uses the existing harness in `internal/modules/harness_pg_test.go`: `newEnv`, `approveAll`, `contextFor`, `moduleDB`): before install the suite sets `e.rt.handlers["email"]` to a handler that, on its first request after each Deploy, reads `e.rt.last("email")` for the deployment's secrets and builds the email handler with its own schema login from `e.moduleDB`, the fake provider and a fake AI provider. The harness itself doesn't change (CORE-29). The suite builds and signs the email package with a generated Ed25519 key through `email.Build`, adds the key as a trusted local key, installs it on the composed core against Postgres, approves its permissions, then drives MOD-EML-01, 03 to 06, 09 to 13, 19, 21, 23 to 31, 33 to 44, 46 to 51 through the core API and the module's HTTP endpoints with users of every role, a walled matter, and context and device tokens. Failure cases included: tampered package refused, declined required scope cancels install, revoked device grant, bad webhook and system mail signatures, provider send failure, sign-in failure, a filing grant for a matter outside the match token (forbidden), a filing grant for a walled matter (not found).

The core-change tests for CC-1 to CC-9 belong to the module-core lane (docs/modules/CORE-CHANGES.md), in `internal/core` and `internal/modules`, and use the email package only as the installed module.

## 10. Code layout

```
modules/email/
  README.md                  what the module does; build, sign, install (like matter-clock's)
  Dockerfile                 golang:1.22 build stage, distroless static nonroot, ENTRYPOINT ["/email", "serve"]
  cmd/email/main.go          serve | package -key signing.key -out email.signed.json
  email.go                   Config, New, Handler, Build, Archive, SignFile (as clock.go)
  server/                    HTTP: tabs, widgets, panels, compose, queue, search, badge, events,
                             system-mail, oauth callback, add-in API, preview
  sync/                      per-account loop, advisory locks, retries, rematch
  match/                     section 8.3 rules
  filing/                    section 8.4 steps with idempotency keys
  send/                      MIME builder, quick email, replies, system mail
  mailparse/  render/        parser and sanitizer
  provider/                  interface; gmail/, graph/, imapsmtp/, fake/
  coreapi/                   typed client for the section 6 calls
  store/                     mod_email queries
  web/                       HTMX templates and static assets for the frames
  outlook-addin/             manifest.xml (template), taskpane.html, taskpane.js, connected.html,
                             commands.html, icon-16.png, icon-32.png, icon-80.png
  package/togavsu-module.yaml
  package/migrations/        0001_accounts.sql through 0005_views.sql
```

- `cmd/email/main.go` follows `examples/matter-clock/cmd/matter-clock/main.go`: `serve` reads the Docker secrets `togavsu_api_token`, `togavsu_webhook_secret`, `togavsu_db_role`, `togavsu_db_password` and `togavsu_db_schema`; `package` reads a base64 Ed25519 seed and writes the signed package with `marketplace.Sign` and `marketplace.KeyID`, the marketplace signing path matter-clock uses, printing the PEM public key.
- The package and the add-in files are embedded with `embed`, so the binary serves the add-in and `Build` packs the manifest and migrations.
- The frames are server-rendered HTMX pages, as the core's are.
- The email lane writes only `modules/email/` and the one suite file `internal/modules/module_email_pg_test.go`; its three dependencies are already pinned (section 7). It does not edit `go.mod`, `go.sum`, `.github/`, protected control files or any other file in a core package. CC-1 to CC-9 go to the module-core lane through docs/modules/CORE-CHANGES.md.

## 11. Out of scope

- Running the firm's own mail server (a later module, PRD 9.7).
- Client portal messages and their notices (client portal module), calls and recordings (Zoom module), e-signature (DocuSeal module).
- Mailbox administration beyond what filing needs: deleting or archiving mail from Togavsu's inbox view, rules, folder management, signatures beyond one plain-text signature per user, calendar and contacts sync, shared-mailbox delegation, read receipts.
- Push delivery (Gmail Pub/Sub, Graph change notifications); the module polls.
- Automated court document download from e-filing links; filed notices keep their links live.
- Unified's archive database filing; old cases arrive through the record importer as closed matters.
- Filing native .eml files, which Doc Tender doesn't read; the HTML copy plus attachments is the filed record, and the raw message's SHA-256 and Message-ID are kept on the message row.
- Bulk mail and mailing lists.
