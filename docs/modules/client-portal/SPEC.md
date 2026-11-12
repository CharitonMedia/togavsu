# Module spec: client-portal (client portal and messages)

Module id `client-portal`. PRD 9.8 item 6. Status: approved for build. Every decision is made here; nothing waits on the firm owner.

## 1. Purpose and scope

The client portal is the only screen a client user ever sees. A client signs in at the core's `/login`, lands in the portal, and works with their own matters: documents the firm shared, uploads, invoices, trust balance, next appointment, messages with the firm, contact details and, when the lawpay-docuseal module is installed, payments and signatures.

The same module gives staff the other side: a firm-wide Messages inbox, a Messages tab on each matter, and a Portal access panel that grants, resends, resets and turns off a client's portal sign-in.

The module owns messages and a few small pieces of portal state. Everything else it shows (users, contacts, matters, files, invoices, trust, appointments) is a core record read through the core API as the viewer, so the core's access rules decide what a client sees.

Decisions this spec fixes:

- **Who the portal user is.** A core user with the `client` role whose `linked_contact_id` is the client's core contact (PRD 4, 5). The module keeps no user, password or session table. A client sees a matter when their linked contact is a `client` party on it; the core's `access.Reader.clientMatter` already decides that. Creating, granting and resetting client users is core identity work the core lacks today: core change C6.
- **How the portal is served.** Client pages are the module's pages, framed by the core at `/portal/client-portal/{tab}` (C1) and served by the module under `/m/client-portal/client/...`. Each frame carries the core's signed context token for the client. The module calls the core API with its module token plus that context token, so every read runs as the client, inside the client's access.
- **Payments.** Invoices come from the core. A Pay action shows only when lawpay-docuseal is installed and its published view says the invoice is payable (section 7). The pay page is lawpay-docuseal's own client tab. The portal only opens that tab; where the money lands (trust by default, or operating) is lawpay-docuseal's decision, made by its `invoice_pay_destination` setting.
- **Signing.** Documents waiting for the client's signature come from lawpay-docuseal's published view, keyed to the core file record, and open lawpay-docuseal's sign page.

## 2. What Unified does today, and how it carries over

| Behavior | Unified source | Togavsu |
| --- | --- | --- |
| Portal session is a Better Auth user linked to a contact by `contacts.auth_user_id` | `src/lib/portal/session.ts` | Core client user with `linked_contact_id`; the module reads it from `GET /api/v1/me` as the viewer |
| Staff grant: create or find the account, set a one-time temporary password, flag `must_change_password`, email it; never a magic link; report the send result to staff | `src/lib/portal-account.ts`, `src/app/api/matters/[id]/resend-portal-link/route.ts` | Same approach, done by the core (C6). The panel's Send portal access button shows the result |
| Staff reset: temporary password shown once to staff to relay by phone; forced change at next sign-in | `src/app/api/matters/[id]/reset-portal-password/route.ts`, `src/app/portal/layout.tsx` | Same, through C6; the forced change is the core page `/portal/security` (C1) |
| Client sees matters where they're a `client` party, newest opened first; a closed matter stays 14 days, then "access ended" with the firm's phone and email | `src/lib/portal/matter.ts`, `src/app/portal/access-ended/page.tsx` | The core enforces the window (C3, `client_access.days_after_close`, default 14); the module renders the list and the access-ended page |
| Active matter is remembered; write paths never guess between several matters and show a picker first | `src/lib/portal/matter.ts` `resolveActiveMatter`, `src/components/portal/MatterSelectGate.tsx`, `qc-stage-3-portal-routing.md` | Kept. The choice lives in `portal_preferences` because a sandboxed frame has no cookies. Uploads, new messages and Pay links need one resolved matter |
| Dashboard cards: new documents dot, unread messages, invoices, trust balance, replenishment banner, next appointment, fee agreement status | `src/app/portal/dashboard/page.tsx`, `src/lib/portal/dashboard-data.ts`, `src/components/portal/ReplenishmentBanner.tsx` | Kept. Trust and appointments use core client reads (C4, C11). Fee agreement status becomes the signature list from lawpay-docuseal |
| Documents: shared, live documents only; "new" until the client views one; a download doesn't clear "new" | `src/lib/portal/documents-data.ts`, `src/lib/portal/document-access.ts`, `src/lib/portal/record-document-view.ts` | The core returns only shared, non-attorney-only files to a client. Inline views go to `file_views`; the core logs downloads |
| Download all as a zip of the visible set | `src/app/api/portal/matters/[matterId]/documents/download-zip/route.ts` | The module relays `POST /api/v1/file-archives` as the client and streams the zip through without keeping it |
| Client upload: streamed; 250 MB per file; 2 GB and 100 files per matter per 24 hours; 90 per 5 minutes per client; content-sniffed type policy with the xlsx ZIP exception; empty file refused; 404 on a foreign matter; failures stay on screen | `PRD-portal-client-upload.md`, `src/app/api/portal/matters/[matterId]/documents/stream/route.ts`, `src/lib/portal/client-upload-type.ts`, `src/components/portal/UploadButton.tsx` | Kept rule for rule. Bytes go to the core's chunked upload API and become a core file record with source `client_upload` (C10). Limits are module settings |
| HEIC and Office uploads get a viewable copy | `src/lib/portal/convert-heic.ts`, `src/lib/portal/convert-to-pdf.ts` | Not in the module: the core makes previews and Doc Tender processes every arriving file (PRD 8.19, 8.21) |
| Invoices: only sent, viewed and paid ones on the client's matters; a client view marks it viewed | `src/lib/portal/invoice-access.ts`, `src/app/portal/invoices/[id]/page.tsx` | The core already does both (`billing.sharedWithClient`, the viewed transition, the `ClientInvoice` allowlist) |
| Pay and deposit: amount and destination fixed server side; a money write never targets a guessed matter | `src/app/api/portal/pay/init/route.ts`, `src/lib/portal/pay-session.ts` | Moves to lawpay-docuseal; the portal links with explicit ids only (section 7) |
| Contact info edit with history kept, attributed to the client | `src/lib/portal/contact-edit.ts`, `src/components/portal/ContactInfoSection.tsx` | Core self-edit endpoint (C5) using the core's effective-from and effective-to rows |
| Change password lives outside the portal layout | `src/app/api/portal/change-password/route.ts` | Core page `/portal/security` (C1); passwords never pass through the module |
| Threads are matter-scoped and person-scoped (one client contact per thread); either side can start one | `src/db/schema/messages.ts`, `src/lib/portal/messages-data.ts` | Kept: `message_threads.contact_id` |
| Staff start threads only on open matters, with a contact who is a client party | `src/app/api/messages/threads/route.ts` | Kept |
| Thread status active, archived or closed; a closed thread takes no new messages | `src/db/schema/messages.ts`, `src/app/api/messages/threads/[threadId]/messages/route.ts` | Kept |
| Staff attachments link matter documents, never copy them; attaching makes the file client-visible and logs "sent to client" once per file, every time | `src/lib/comms/attach-documents.ts` | Kept: `message_attachments.file_id`, `PATCH /api/v1/files/{id}` with `shared_with_client`, one client-visible timeline row per file |
| Client attachments: only files already visible to that client on that matter; one bad id aborts the send | `src/lib/portal/client-messages.ts` | Kept |
| Read receipts: one `read_at` per message; the client opening a thread stamps staff messages and writes a "client viewed" timeline row; staff opening stamps client messages | `src/lib/portal/messages-data.ts`, `src/lib/comms/messages-data.ts`, `build-log-comms-components.md` | Kept |
| Author labels use real names both ways, never a generic "Firm" | `build-log-comms-components.md` | Kept |
| A staff message emails the thread's client a portal link with no message body | `src/lib/comms/notify-client.ts` | Kept, through core system mail to the client user (C7) |
| A client message emails the firm, with no body | `src/lib/portal/client-messages.ts` | Core notification to the matter team (responsible attorney and assigned staff), email on by default (C8) |
| Staff inbox: firm-wide, by matter or by contact, with an unread count in the sidebar | `src/app/api/messages/threads/route.ts`, `src/lib/comms/messages-data.ts` | Home tab, matter tab and sidebar badge (C9) |
| Discovery pages, questionnaire, Zoom | `src/app/portal/discovery`, `src/app/portal/questionnaire`, `src/lib/comms/adapters/zoom.ts` | Other modules (section 11); they add client tabs through C1 and show in the portal nav |

## 3. Manifest

`modules/client-portal/package/togavsu-module.yaml` (block style; the manifest parser takes no flow mappings). Fields marked with a C comment need that core change.

```yaml
id: client-portal
name: Client Portal
version: "1.0.0"
author: Chariton Media
homepage: https://github.com/CharitonMedia/togavsu/tree/main/modules/client-portal
license: Same as Togavsu
price: Free
container:
  image: ghcr.io/charitonmedia/togavsu-client-portal
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
  port: 8080
# C1
client_home: home
permissions:
  - scope: matters:read
    reason: List the client's matters and the matter behind each thread.
  - scope: contacts:read
    reason: Show staff the client parties on a matter and each client's name.
  # C12
  - scope: users:directory
    reason: Label each message with the staff member who wrote it.
  - scope: files:read
    reason: List shared documents and check attachments.
  - scope: files:write
    reason: Save client uploads and share attached documents with the client.
  - scope: invoices:read
    reason: Show the client's sent invoices.
  - scope: trust:read
    reason: Show the client their own trust balance and replenishment requests.
  - scope: calendar:read
    reason: Show the client their next appointment.
  - scope: profile:read
    reason: Read the signed-in client's account and contact details.
  - scope: profile:write
    reason: Save the client's own contact detail changes.
  # C12
  - scope: firm:read
    reason: Firm name, phone, email and client portal colors.
  - scope: events:write
    reason: Put message and document activity on the case timeline.
  - scope: notifications:write
    reason: Tell staff when a client writes or uploads.
  - scope: mail:send
    reason: Email a client that the firm sent a message.
  - scope: client_access:read
    reason: Show each client's portal status on the matter.
  - scope: client_access:write
    reason: Grant, resend, reset and turn off portal access.
  - scope: views:lawpay-docuseal.invoice_payability:read
    required: false
    reason: Show Pay on payable invoices.
  - scope: views:lawpay-docuseal.matter_payability:read
    required: false
    reason: Show Make a deposit.
  - scope: views:lawpay-docuseal.signature_requests:read
    required: false
    reason: List documents waiting for the client's signature.
widgets:
  - id: client-messages
    title: Client messages
    path: /staff/widget
    roles: [admin, attorney, staff]
    size:
      w: 4
      h: 2
    sizes:
      - w: 4
        h: 2
      - w: 6
        h: 3
    target:
      tab: inbox
      params: [matter_id]
tabs:
  - id: inbox
    title: Messages
    placement: home
    path: /staff/inbox
  - id: messages
    title: Messages
    placement: matter
    path: /staff/matter-messages
  # C1: client placement and params
  - id: home
    title: Dashboard
    placement: client
    path: /client/home
  - id: documents
    title: Documents
    placement: client
    path: /client/documents
    params: [matter_id]
  - id: invoices
    title: Invoices
    placement: client
    path: /client/invoices
    params: [matter_id, invoice_id]
  - id: client-messages
    title: Messages
    placement: client
    path: /client/messages
    params: [matter_id, thread_id]
  - id: account
    title: Account
    placement: client
    path: /client/account
matter_items:
  - id: access
    kind: panel
    title: Portal access
    path: /staff/access
sidebar:
  - id: messages
    title: Messages
    path: /staff/inbox
    # C9
    section: communications
    icon: message
    order: 10
    badge:
      path: /staff/badge
      tone: normal
quick_create:
  - id: new-message
    title: New message
    path: /staff/new
search_scopes:
  - id: messages
    title: Messages
    path: /staff/search
# C8
notifications:
  - kind: client_message
    label: A client sent a portal message
    default_channels: [in_app, email]
  - kind: client_upload
    label: A client uploaded a document
    default_channels: [in_app]
data:
  extends:
    - name: preferences
      record: user
      table: portal_preferences
      key: user_id
      columns:
        - name: active_matter_id
    - name: client_upload
      record: file
      table: client_uploads
      key: file_id
      columns:
        - name: uploaded_at
  views:
    - name: thread_summary
      record: matter
      key: matter_id
      columns:
        - name: active_threads
        - name: unread_from_client
        - name: last_message_at
settings:
  - key: upload_max_file_mb
    label: Largest client upload in MB
    type: int
    default: "250"
  - key: upload_daily_mb
    label: Client upload MB per matter per 24 hours
    type: int
    default: "2048"
  - key: upload_daily_files
    label: Client uploads per matter per 24 hours
    type: int
    default: "100"
  - key: upload_rate_per_5min
    label: Client uploads per client per 5 minutes
    type: int
    default: "90"
  - key: message_max_chars
    label: Longest message in characters
    type: int
    default: "10000"
  - key: email_client_on_staff_message
    label: Email the client when staff send a message
    type: bool
    default: "true"
  - key: welcome_text
    label: Dashboard welcome text for clients
    type: string
    default: ""
help:
  heading: Client Portal
  pages:
    - id: overview
      title: Client portal overview
      keywords: [portal, client, sign in]
      covers: [/modules/client-portal/tabs/inbox]
      body: |
        (build writes: what the portal is, where staff find it, what clients see)
    - id: messages
      title: Client messages
      keywords: [message, inbox, unread, thread, attach]
      covers: [client-portal.client-messages, /modules/client-portal/tabs/messages]
      body: |
        (build writes: inbox, matter tab, attachments, read receipts, closing threads)
    - id: access
      title: Giving a client portal access
      keywords: [portal access, temporary password, resend, reset]
      covers: [/modules/client-portal/matter-items/access]
      body: |
        (build writes: the panel's four actions and what the client receives)
```

`events` is omitted: the module subscribes to nothing. It reads everything live when a page loads, and the core moves its links itself when it merges contacts or matters (PRD 9.9), so it requests no `events:read`. The core names the two notification kinds `client-portal.client_message` and `client-portal.client_upload` (C8). Clients get no help pages (PRD 8.11). The build replaces each help `body` placeholder with real Markdown giving the menu path and how to use the screen.

**Pages and agent endpoints.** Every page is server-rendered HTML with HTMX. Every action is also a JSON endpoint under `/m/client-portal/api/`, authenticated by `X-Togavsu-Context-Token`. An agent gets a context token with its bearer token from `POST /api/v1/modules/client-portal/context-tokens` and calls:

| Method and path | Who | Does |
| --- | --- | --- |
| GET `/api/threads?matter_id=&contact_id=&status=` | staff, client | Threads the viewer may see, newest message first |
| POST `/api/threads` `{matter_id, contact_id, subject, body, file_ids}` | staff, client | Starts a thread; a client's `contact_id` is always their own |
| GET `/api/threads/{id}` | staff, client | Thread with messages; stamps the other side's messages read |
| POST `/api/threads/{id}/messages` `{body, file_ids}` | staff, client | Replies |
| POST `/api/threads/{id}/status` `{status}` | staff | active, archived or closed |
| POST `/api/messages/{id}/retract` | staff author | Retracts the author's own message |
| GET `/api/unread` | staff | `{count}` of unread client messages on matters the viewer sees |
| GET `/api/search?q=` | staff | Thread hits for the core's search |
| POST `/api/active-matter` `{matter_id}` | client | Saves the active matter |
| POST `/api/uploads`, PUT `/api/uploads/{id}`, POST `/api/uploads/{id}/complete` | client | Client upload in 8 MB chunks, relayed to the core |
| POST `/api/archives` `{matter_id}` | client | Zip of every visible document on the matter |
| GET `/api/access?matter_id=` | staff | Portal status for each client party |
| POST `/api/access/{contact_id}/grant`, `/temporary-password`, `/deactivate`, `/reactivate` | staff | Relays to C6 |

Staff pages: `/staff/inbox`, `/staff/matter-messages`, `/staff/access`, `/staff/new`, `/staff/widget`, `/staff/badge`, `/staff/search`. Client pages: `/client/home`, `/client/documents`, `/client/invoices`, `/client/messages`, `/client/account`, `/client/access-ended`. A client token on a staff page or endpoint gets 404, and so does a staff token on a client page.

## 4. Data

Schema `mod_client_portal`. Ids are UUIDv7 as `TEXT`, matching core ids; times are `BIGINT` unix seconds UTC, as matter-clock does. Every foreign key to a core record is `ON DELETE RESTRICT`. No table copies a contact, user, matter, file or invoice; uploads and attachments are core file records referenced by id.

`0001_messages.sql`

```sql
CREATE TABLE message_threads (
  id TEXT PRIMARY KEY,
  matter_id TEXT NOT NULL REFERENCES core.matters (id) ON DELETE RESTRICT,
  contact_id TEXT NOT NULL REFERENCES core.contacts (id) ON DELETE RESTRICT,
  subject TEXT NOT NULL DEFAULT '',
  created_by_user_id TEXT NOT NULL REFERENCES core.users (id) ON DELETE RESTRICT,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived', 'closed')),
  last_message_at BIGINT NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE INDEX message_threads_matter_last ON message_threads (matter_id, last_message_at);
CREATE INDEX message_threads_contact_last ON message_threads (contact_id, last_message_at);

CREATE TABLE messages (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL REFERENCES message_threads (id) ON DELETE RESTRICT,
  author_user_id TEXT NOT NULL REFERENCES core.users (id) ON DELETE RESTRICT,
  author_kind TEXT NOT NULL CHECK (author_kind IN ('staff', 'client')),
  body TEXT NOT NULL,
  read_at BIGINT,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retracted')),
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE INDEX messages_thread_created ON messages (thread_id, created_at);
CREATE INDEX messages_unread ON messages (thread_id, author_kind, read_at);

CREATE TABLE message_attachments (
  message_id TEXT NOT NULL REFERENCES messages (id) ON DELETE RESTRICT,
  file_id TEXT NOT NULL REFERENCES core.files (id) ON DELETE RESTRICT,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (message_id, file_id)
);

CREATE TABLE message_notifications (
  message_id TEXT PRIMARY KEY REFERENCES messages (id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK (status IN ('sent', 'failed', 'skipped')),
  detail TEXT NOT NULL DEFAULT '',
  attempted_at BIGINT NOT NULL
);

CREATE VIEW thread_summary AS
  SELECT t.matter_id,
         SUM(CASE WHEN t.status = 'active' THEN 1 ELSE 0 END) AS active_threads,
         SUM(COALESCE(u.unread, 0)) AS unread_from_client,
         MAX(t.last_message_at) AS last_message_at
    FROM message_threads t
    LEFT JOIN (SELECT thread_id, COUNT(*) AS unread FROM messages
                WHERE author_kind = 'client' AND read_at IS NULL AND status = 'active'
                GROUP BY thread_id) u ON u.thread_id = t.id
   GROUP BY t.matter_id;
```

One `author_user_id` replaces Unified's dual staff and contact author columns, because staff and clients are both core users. Messages are never deleted; a retracted one keeps its body in the table and shows as "Message removed".

`0002_portal_state.sql`

```sql
CREATE TABLE portal_preferences (
  user_id TEXT PRIMARY KEY REFERENCES core.users (id) ON DELETE RESTRICT,
  active_matter_id TEXT REFERENCES core.matters (id) ON DELETE RESTRICT,
  updated_at BIGINT NOT NULL
);

CREATE TABLE file_views (
  file_id TEXT NOT NULL REFERENCES core.files (id) ON DELETE RESTRICT,
  user_id TEXT NOT NULL REFERENCES core.users (id) ON DELETE RESTRICT,
  first_viewed_at BIGINT NOT NULL,
  last_viewed_at BIGINT NOT NULL,
  PRIMARY KEY (file_id, user_id)
);

CREATE TABLE client_uploads (
  file_id TEXT PRIMARY KEY REFERENCES core.files (id) ON DELETE RESTRICT,
  user_id TEXT NOT NULL REFERENCES core.users (id) ON DELETE RESTRICT,
  matter_id TEXT NOT NULL REFERENCES core.matters (id) ON DELETE RESTRICT,
  size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
  uploaded_at BIGINT NOT NULL
);
CREATE INDEX client_uploads_matter_time ON client_uploads (matter_id, uploaded_at);

CREATE TABLE upload_windows (
  user_id TEXT PRIMARY KEY REFERENCES core.users (id) ON DELETE RESTRICT,
  window_start BIGINT NOT NULL,
  uploads INTEGER NOT NULL CHECK (uploads >= 0)
);
```

`client_uploads` is the per-matter 24-hour ledger for the volume caps, because the core's file list has no source or date filter. `upload_windows` is Unified's fixed 5-minute window, keyed on the client user. `file_views` drives the "new" dot and records inline views only; downloads are the core's audit record.

**Published view:** `thread_summary`, keyed to matter, read by another module under `views:client-portal.thread_summary:read` or by any screen with `include=client-portal.thread_summary`. The core applies the viewer's matter access through `matter_id`.

**Hard deletes.** An admin purge of a trashed file that an attachment or upload row points to is refused by the RESTRICT key, and the core names this module (PRD 9.9). That's intended: a document sent to a client stays on record.

## 5. Core API calls

All calls carry the module token. V marks calls that also carry the viewer's context token, so the core answers as that staff member or client.

| Method and path | Scope | V | Used for |
| --- | --- | --- | --- |
| POST `/api/v1/modules/client-portal/context-tokens/verify` | module token | | Viewer user, roles, matter in view |
| GET `/api/v1/me` | profile:read | V | `linked_contact_id`; for clients, `access_ended_matters` (C3) |
| GET `/api/v1/matters`, GET `/api/v1/matters/{matter_id}` | matters:read | V | Client's matters (C3); staff matter checks |
| GET `/api/v1/matters?include=client-portal.thread_summary` | matters:read | V | Inbox matter filter with counts |
| GET `/api/v1/matter-parties?matter_id=` | matters:read | V | Client parties on a matter (staff) |
| GET `/api/v1/contacts/{contact_id}` | contacts:read | V | Client names for staff |
| GET `/api/v1/user-directory?ids=` | users:directory | V | Staff author names, only for threads the viewer already passed (C12) |
| GET `/api/v1/files?matter_id=`, GET `/api/v1/files/{file_id}` | files:read | V | Shared documents, attachment picker and checks |
| PATCH `/api/v1/files/{file_id}` `{shared_with_client: true}` | files:write | V | A staff attachment shares the file |
| POST `/api/v1/file-uploads`, PUT `/api/v1/file-uploads/{upload_id}`, POST `/api/v1/file-uploads/{upload_id}/complete`, POST `/api/v1/files` | files:write | V | Client and staff uploads in 8 MB chunks |
| POST `/api/v1/file-archives` `{file_ids}` | files:read | V | Download all: the ids of every file the client can see on the matter, from the same list the documents page shows |
| GET `/api/v1/files/{file_id}/versions/{version_id}/download?disposition=inline` or `attachment` | files:read | client session | Linked from the frame; the browser opens it with the client's own core session |
| GET `/api/v1/invoices?matter_id=`, GET `/api/v1/invoices/{invoice_id}` | invoices:read | V | Client invoices; the core marks one viewed |
| GET `/api/v1/invoices/{invoice_id}/pdf` | invoices:read | client session | Linked from the frame |
| GET `/api/v1/me/trust`, GET `/api/v1/me/trust-ledger?matter_id=` | trust:read | V | C4 |
| GET `/api/v1/me/appointments` | calendar:read | V | C11 |
| GET `/api/v1/me/contact`, PATCH `/api/v1/me/contact` | profile:read, profile:write | V | C5 |
| GET `/api/v1/firm-profile` | firm:read | V | Firm name, phone, email, portal colors and label (C12) |
| GET `/api/v1/nav/client-context` | profile:read | V | Other modules' client tabs for the portal nav (C1) |
| POST `/api/v1/events` | events:write | V | Timeline rows |
| POST `/api/v1/notifications` `{kind, title, link, audience: matter_team, matter_id}` | notifications:write | | Staff notices, sent as a service call (C8) |
| POST `/api/v1/mail/messages` `{to_user_ids, matter_id, subject, text, link_path}` | mail:send | V | Email to the thread's client user after a staff message (C7) |
| GET `/api/v1/client-access?contact_id=`, POST `/api/v1/client-access`, POST `/api/v1/client-access/{user_id}/temporary-password`, `/deactivate`, `/reactivate` | client_access:read, client_access:write | V | C6 |
| GET `/api/v1/modules/lawpay-docuseal/views/{view}?record_id=` | views:lawpay-docuseal.{view}:read | V | Section 7 |
| PUT `/api/v1/help/modules/client-portal/pages` | help:write, granted at install | | Help pages from a running version |

Timeline actions written through `POST /api/v1/events`: `client_portal.message_sent` (client-visible), `client_portal.message_viewed` (client opened staff messages; client-visible), `client_portal.file_sent_to_client` (one per attached file; client-visible), `client_portal.thread_status_changed`, `client_portal.message_retracted`. Summaries never carry message bodies.

## 6. Core changes

The module-core lane builds these from docs/modules/CORE-CHANGES.md. Each item names its merged definition there; that definition is the one built, and this list says only what this module needs from it.

The core lacks each of these today. C1 to C10 and C12 are required for the module to ship; C11 feeds the dashboard's appointment card.

- **C1. Client pages and the client frame (CORE-10).** Manifest: `tabs[].placement` accepts `client`; `tabs[].params` lists the query parameters the core passes through; `tabs[].nav: false` keeps a client tab out of the portal nav; top-level `client_home` names one client tab, and only one enabled module may declare it (a second install that declares it is refused). Core routes: `GET /portal` sends a client-only user to that tab; `GET /portal/{module_id}/{tab_id}` frames a module's client tab full-window, with no staff shell, for a client-only user, with a context token for that user (a `matter_id` param sets the matter claim only when the client may read that matter, else 404). Staff, attorney, accountant and admin users get 404 on `/portal/*`. After sign-in and after a password reset, a client-only user goes to `/portal`, not `/practice/matters`. Core client pages: `/portal/security` (change password, optional TOTP, the forced change of C6) and `POST /portal/sign-out`. With no module declaring `client_home`, `/portal` shows the firm name and "The client portal isn't set up yet." New endpoint `GET /api/v1/nav/client-context` (client role, profile:read) lists `{module_id, tab_id, title}` for every enabled module's client tabs with `nav` true.
- **C2. Frame bridge (CORE-09).** The page asks its parent for a fresh context token with `{type: "togavsu:context.refresh", request_id}` and gets `togavsu:context.token` back. Client frames use the client sandbox list, so download links open with the client's own session and Pay and Sign links reach another module's client tab with `target="_top"`; staff frames use the staff list for downloads.
- **C3. Client matter reads and the access window (CORE-11).** `GET /api/v1/matters` and `GET /api/v1/matters/{matter_id}` admit the client role (client mode `own`): only matters where the user's linked contact is a `client` party, newest `opened_on` first, with a client field allowlist (`id`, `number`, `name`, `status`, `practice_area_name`, `opened_on`, `closed_on`, `responsible_attorney_name`). New setting `client_access.days_after_close` (int, default 14, Security tab): a client loses read access to a closed matter, and to its files and invoices, that many days after `closed_on`; reopening restores it. `access.Reader.clientMatter` applies it, so the API, MCP, portal and downloads agree. `GET /api/v1/me` adds `access_ended_matters` (a count) for client users.
- **C4. Client trust read (CORE-12).** `GET /api/v1/me/trust` (client role, trust:read) returns, for each readable matter, the client's own trust balance in cents and the active replenishment requests naming the client (`id`, `matter_id`, `amount_cents`, `message`, `status`, `created_at`). `GET /api/v1/me/trust-ledger?matter_id=` returns the ledger entries naming the client (date, type, amount, description). No other client's figures and no account numbers.
- **C5. Client contact self-edit (CORE-13).** `GET /api/v1/me/contact` and `PATCH /api/v1/me/contact` (client role, profile:read and profile:write) read and change the linked contact's primary email, primary phone and mailing address. A change sets `effective_to` on the old row and adds a new primary row (PRD 5), writes the audit log with the client as actor, and emits `contact.updated` with a client-visible timeline row. The sign-in email stays separate (`/api/v1/me/email-change`).
- **C6. Client access management (CORE-14).** Scopes `client_access:read` and `client_access:write` (admin, attorney and staff roles; modules may request them). Core migration adds `core.users.must_change_password` (boolean, default false); a client with it set is sent to `/portal/security` before any other page, and every API call except `/api/v1/me/password` and `/api/v1/auth/logout` answers 403 `password_change_required`. Endpoints: `GET /api/v1/client-access?contact_id=` returns `{user_id, email, active, must_change_password, last_sign_in_at, granted_at}` or 404; `POST /api/v1/client-access {contact_id, email}` creates or finds the client user linked to that contact (email defaults to the contact's primary email; 400 when there is none), sets a new temporary password with `must_change_password`, mails it by system mail (CORE-05) with the sign-in address, and returns `{user_id, created, email, mail_status}` with `mail_status` one of `sent`, `failed` or `skipped`, never the password; `POST /api/v1/client-access/{user_id}/temporary-password` sets a new temporary password, revokes the user's sessions and returns `{temporary_password}` once; `POST /api/v1/client-access/{user_id}/deactivate` and `/reactivate`. Each endpoint refuses a user holding any role besides client (409) and a contact who is a client party on no matter the caller can read (404). Events `client_access.granted`, `client_access.password_reset`, `client_access.deactivated`, `client_access.reactivated`. Temporary passwords never reach logs, the audit log or events.
- **C7. Module mail to users (CORE-04).** `POST /api/v1/mail/messages` with `to_user_ids` (the thread's client user), `matter_id`, `subject`, `text` and a `link_path` under `/portal/client-portal/`. It goes through the system mail router (CORE-05), so it reaches the email module when the admin routed system mail there. The module never sees an address.
- **C8. Module notification kinds (CORE-03).** Manifest `notifications`, and `POST /api/v1/notifications` as a service call with `audience: matter_team` and `matter_id`, which reaches the responsible attorney and the assigned staff and honors each user's channels, email included.
- **C9. Sidebar placement and badges (CORE-08).** `section: communications`, `icon: message`, `order` and `badge`; the badge is fetched with the viewer's context token (500 ms timeout, 30-second cache, left off on failure).
- **C10. Client uploads through a module (CORE-16).** `files.sourceFor` returns `client_upload` for a client-only principal even when a module delegated it, so the file's origin is `received`, its uploader is the client user, Doc Tender treats it as a client upload and the timeline highlights it. The audit row still names the module as the channel.
- **C11. Client appointments (CORE-15).** `GET /api/v1/me/appointments` (client role, calendar:read): upcoming `client_meeting` and `court_date` events on the client's readable matters, with `title`, `kind`, `start`, `end`, `all_day`, `location` and `matter_id` only.
- **C12. Firm profile and staff names (CORE-18, CORE-19).** `GET /api/v1/firm-profile` (`firm:read`, client role included) for the firm's name, phone, email and portal colors, and `GET /api/v1/user-directory?ids=` (`users:directory`; a client caller gets names of staff users only) for author labels. Both are viewer calls; the core's admin-only settings and user reads stay closed to the portal.
- **Service calls (CORE-01).** The staff notices of C8 are sent with no viewer. Every other call in section 5 marked V carries the viewer's context token.

## 7. Cross-module contract with lawpay-docuseal

client-portal requests three optional view scopes. When lawpay-docuseal isn't installed or isn't enabled, a view read answers 404; when the admin declined the scope, 403. Either way the portal hides Pay, Make a deposit and Sign, and every other page works.

**Views lawpay-docuseal must publish** in its manifest `data.views`, read through `GET /api/v1/modules/lawpay-docuseal/views/{view}?record_id=` with the client's context token. The core applies the client's access to the keyed record.

| View and scope | Keyed to | Columns | Meaning |
| --- | --- | --- | --- |
| `invoice_payability`, `views:lawpay-docuseal.invoice_payability:read` | invoice, `invoice_id` | `invoice_id` TEXT, `payable` BOOLEAN, `unpayable_reason` TEXT (`''`, `not_configured`, `zero_balance`, `in_progress`), `in_progress_cents` BIGINT | One row per invoice the module can see; no row means not payable |
| `matter_payability`, `views:lawpay-docuseal.matter_payability:read` | matter, `matter_id` | `matter_id` TEXT, `trust_deposits_enabled` BOOLEAN, `minimum_deposit_cents` BIGINT | Whether a client may make a trust deposit on the matter |
| `signature_requests`, `views:lawpay-docuseal.signature_requests:read` | file, `file_id` | `file_id` TEXT, `signing_request_id` TEXT, `matter_id` TEXT, `signer_contact_id` TEXT, `status` TEXT (`awaiting_client`, `client_signed`, `completed`, `declined`, `expired`, `void`), `sent_at` BIGINT, `expires_at` BIGINT or NULL | One row per signer per document |

**Client tabs lawpay-docuseal must declare** (C1, CORE-10):

- `pay`: placement `client`, `params: [purpose, invoice_id, matter_id, replenishment_request_id, return_to]`. `purpose=invoice_pay` needs `invoice_id`, and the amount is the invoice's balance due read from the core, never a client figure. `purpose=trust_deposit` needs `matter_id`; with `replenishment_request_id` the amount is that request's amount, otherwise the client enters an amount of at least `minimum_deposit_cents`. An id the client can't read answers 404.
- `sign`: placement `client`, `params: [signing_request_id, return_to]`. The signer must be the viewer's linked contact, else 404.
- `return_to` is honored only when it starts with `/portal/client-portal/`; anything else returns to `/portal`. The portal URL-encodes it, because it can carry its own query string.
- Both tabs declare `nav: false`, so they never appear in the portal nav; they open only from the links below.
- The document to sign must be `shared_with_client`. The view is keyed to the file, so rows for an unshared file never reach a client.

**How the portal uses them:**

- Invoices page: reads `invoice_payability` for the listed invoice ids; shows Pay where `payable` is true and "Payment processing" where `unpayable_reason` is `in_progress`. Pay opens `/portal/lawpay-docuseal/pay?purpose=invoice_pay&invoice_id={id}&return_to={encoded /portal/client-portal/invoices?invoice_id={id}}` with `target="_top"`.
- Dashboard and account: reads `matter_payability` for the active matter; with `trust_deposits_enabled`, shows Make a deposit opening `/portal/lawpay-docuseal/pay?purpose=trust_deposit&matter_id={id}&return_to={encoded /portal/client-portal/home}` with `target="_top"`; an active replenishment request on that matter (C4) adds `replenishment_request_id={request id}`, and lawpay-docuseal then charges that request's amount.
- Documents page and dashboard: reads `signature_requests` for the listed file ids; rows whose `signer_contact_id` is the viewer's contact show their status, and `awaiting_client` rows show Sign opening `/portal/lawpay-docuseal/sign?signing_request_id={id}&return_to=/portal/client-portal/documents`.
- The portal only opens the pay tab. lawpay-docuseal decides where the money lands: by default a portal invoice payment is a trust deposit that leaves the invoice balance unchanged until staff apply trust to it, and with its `invoice_pay_destination` setting at operating the payment reduces the invoice. Either way the payment is a core record, and the invoice balance the portal shows is always the core's. While a payment is pending, or a trust deposit for the invoice waits to be applied, `invoice_payability` reads `in_progress` and the portal shows "Payment processing" instead of Pay. Nothing flows from lawpay-docuseal into client-portal tables, and lawpay-docuseal needs nothing client-portal publishes.

## 8. Behaviors and acceptance criteria

Synthetic fixtures: Ann Admin (admin), Avery Attorney (attorney, responsible on every matter), Sam Staff (staff, assigned to M1), Cal Accountant (accountant); contacts Casey Client, Drew Other and Jordan Coclient, each with a client user; matter M1 (open) with Casey as client party, M2 (open) with Drew, M3 (open) with Casey and Jordan; M4 walled to Avery, with Casey as client party.

**Access and isolation**

- MOD-POR-01: Casey's portal lists M1, M3 and M4 and never M2. Every module page and endpoint called with Casey's token that names M2, or M2's files, invoices, threads or messages, answers 404, and so does a guessed id.
- MOD-POR-02: On M3, Casey can't read, post to or attach to a thread whose `contact_id` is Jordan, and Jordan can't read Casey's.
- MOD-POR-03: An M1 file that isn't shared, or is attorney-only, never appears in Casey's documents, attachment picker or zip, and Casey can't attach it.
- MOD-POR-04: Draft and void invoices never appear to Casey; a sent one does and turns viewed when Casey opens it.
- MOD-POR-05: Fifteen days after M1 closes (window 14), M1 leaves Casey's portal and Casey's core API calls on it answer 404. When Casey has no readable matter left, `/client/home` shows the access-ended page with the firm's name, phone and email. Reopening M1 restores it.
- MOD-POR-06: Staff, attorney, admin and accountant users get 404 at `/portal/client-portal/home`; a client user gets 404 on every `/staff/*` page and staff endpoint.
- MOD-POR-07: A context token issued for matter-clock is refused by client-portal; an expired token answers 401, and the page gets a fresh one from its parent frame (C2) and retries once.
- MOD-POR-08: M4's threads appear in no inbox, badge, widget or search result for Sam, who is outside its wall.

**Portal access**

- MOD-POR-09: Send portal access on M1 for a contact with no client user creates one linked to that contact and emails a temporary password; the panel shows "Sent to {email}". When mail fails it shows "Not sent" and keeps the button.
- MOD-POR-10: Temporary password shows a password once to Sam. Casey's next sign-in lands on `/portal/security`, and nothing else opens until Casey sets a new password.
- MOD-POR-11: Turn off access stops Casey from signing in; turn on restores it. Neither changes Casey's contact or matters.
- MOD-POR-12: The panel refuses portal access for a contact who isn't a client party on the matter, and for a user who also holds the staff role. Cal sees no panel.

**Matters and dashboard**

- MOD-POR-13: With one readable matter the portal opens on it. With several and no saved choice, upload, New message and every Pay or deposit link first show the matter picker; after a choice the pages use it and remember it at the next sign-in.
- MOD-POR-14: The dashboard shows a new-documents dot until Casey views each new shared file inline; a download alone doesn't clear it.
- MOD-POR-15: The dashboard shows unread staff messages, the balance due on sent invoices, Casey's trust balance, an active replenishment request and the next client meeting, each read from the core.
- MOD-POR-16: Casey changes their phone on the account page; the core keeps the old number with an end date, and the timeline shows the change with Casey as actor.

**Documents and uploads**

- MOD-POR-17: Casey uploads a PDF, a JPEG, a HEIC, a docx and an xlsx to M1. Each becomes one core file on M1 with source `client_upload`, origin received and shared with the client, and appears on the staff Files tab. The xlsx stays `.xlsx`.
- MOD-POR-18: An `.exe` renamed `.pdf` answers 400 "That file has to be a PDF, Word, Excel, text, or image file." and leaves no file record. A zero-byte file answers 400 "That file was empty."
- MOD-POR-19: A file over `upload_max_file_mb` answers 413. The upload that would pass 100 files or 2 GB on M1 within 24 hours answers 413 "That's as much as this portal accepts in a day. Call the office and we'll take the rest another way." The 91st upload within 5 minutes answers 429 with `Retry-After`. None leaves a file record.
- MOD-POR-20: In a batch of five with one bad file, four land and the failure stays listed on screen.
- MOD-POR-21: Download all on M1 returns a zip of exactly the files Casey can see there.

**Messages**

- MOD-POR-22: Sam starts a thread on M1 with Casey. Starting one on a closed or prospect matter, or with Drew as the contact on M1, answers 400.
- MOD-POR-23: Sam attaches an unshared M1 file: it becomes shared and one "sent to client" timeline row is written; attaching it again later writes another. Attaching an M2 file or an attorney-only file answers 400 and writes nothing.
- MOD-POR-24: A staff message emails Casey a portal link with no message body. With `email_client_on_staff_message` false, no mail goes and the notification row reads `skipped`.
- MOD-POR-25: Casey's message reaches Avery and Sam as a `client-portal.client_message` notification, email on by default, with no body.
- MOD-POR-26: Casey opening the thread stamps Sam's messages read and writes one client-visible "viewed" timeline row. Sam opening it stamps Casey's messages read and lowers the badge.
- MOD-POR-27: A closed thread refuses new messages from either side with 409. An archived thread leaves the staff inbox and stays readable.
- MOD-POR-28: Sam retracts Sam's own message and Casey sees "Message removed"; Sam can't retract Casey's or Avery's.
- MOD-POR-29: Searching a word from an M1 thread body finds the thread for Sam; searching a word from an M4 thread finds nothing for Sam.

**Payments and signing**

- MOD-POR-30: With lawpay-docuseal absent, or its scopes declined, no Pay, deposit or Sign control renders and every page loads.
- MOD-POR-31: With a provider publishing `invoice_payability` true for invoice I1, Pay on I1 opens `/portal/lawpay-docuseal/pay?purpose=invoice_pay&invoice_id=I1&return_to=...`; with `payable` false, there's no Pay.
- MOD-POR-32: A `signature_requests` row with Casey as signer and `awaiting_client` shows Sign; a row with Jordan as signer on the same shared file shows nothing to Casey.

**Install and data**

- MOD-POR-33: The signed package installs, its migrations run in `mod_client_portal`, its database login can't read `core` tables, and an applied migration whose checksum changed stops the update.
- MOD-POR-34: Hard-deleting a contact, matter, user or file that a module row points to is refused and names `client-portal`.
- MOD-POR-35: `GET /api/v1/matters/{M1}?include=client-portal.thread_summary` returns the counts to Sam and is refused to a viewer who can't read M1.

## 9. Tests

All tests are Go and run in the existing "Core tests" CI job (`go test ./...` against real Postgres through `TOGAVSU_TEST_DATABASE_URL`). Like the matter-clock suites, database tests skip when the variable is unset outside CI and fail when it's unset in CI.

- `modules/client-portal/package_test.go` (no database): the manifest parses with `modules.ParseManifest`; `Build` produces a package whose Ed25519 signature verifies; every migration passes the core's migration check; a flipped byte fails verification.
- `modules/client-portal/upload_test.go` (no database): the type-sniffing table (pdf, jpg, png, heic and docx by content; xlsx by extension inside a ZIP; doc, txt, xls and csv by extension; an exe renamed pdf; empty) and the fixed-window limiter.
- `internal/modules/module_client-portal_pg_test.go` (package `modules_test`, so it uses the existing harness in `internal/modules/harness_pg_test.go`: `newEnv`, `approveAll`, `contextFor`, `moduleDB`): before install the suite sets `e.rt.handlers["client-portal"]` to a handler that, on its first request after each Deploy, reads `e.rt.last("client-portal")` for the deployment's secrets and builds this module's `Server` with its own schema login from `e.moduleDB`. Then the section 8 fixtures, `clientportal.Build` with a generated key, a trusted-key add, `POST /api/v1/modules` and approval of every required scope. The harness itself doesn't change (CORE-29). One subtest per criterion, MOD-POR-01 to 35, failure paths included: wrong-client reads, cross-matter and attorney-only attachments, expired and foreign context tokens, type, size, volume and rate refusals, closed-thread posts, retracting another user's message, staff pages with a client token, declined optional scopes.
- In the same file, a small signed fixture module with id `lawpay-docuseal`, registered in `e.rt.handlers["lawpay-docuseal"]`, publishes the three section 7 views and declares `pay` and `sign` client tabs with `nav: false`; it installs beside client-portal and drives MOD-POR-30 to 32. The fixture is test scaffolding and never ships.
- Install failures in the acceptance suite: a package signed by an untrusted key is refused; declining a required scope cancels the install; a migration edited after install is refused on update.

## 10. Code layout

```
modules/client-portal/
  README.md                  what it does, build, sign and install steps, as matter-clock's
  Dockerfile                 distroless image, ENTRYPOINT ["/client-portal", "serve"]
  cmd/client-portal/main.go  "serve" and "package -key -out" subcommands, as matter-clock's
  package.go                 //go:embed package; Build(priv, edit), as matterclock.Build
  portal.go                  Server, routes, context-token checks, core API client
  threads.go                 threads, messages, attachments, read receipts, notices
  documents.go               document list, views, zip relay, upload relay and limits
  access.go                  portal access panel relays (C6)
  payments.go                lawpay-docuseal view reads and links (section 7)
  pages/                     Go templates for staff and client pages (HTMX)
  package/togavsu-module.yaml
  package/migrations/0001_messages.sql
  package/migrations/0002_portal_state.sql
  *_test.go                  section 9 unit tests
internal/modules/module_client-portal_pg_test.go   section 9 acceptance suite
```

The package is signed through the marketplace signing path matter-clock uses: the signed package file holding the `.tar.gz`, its digest and an Ed25519 signature, with `marketplace.KeyID` and `marketplace.PublicKeyPEM` for the printed key. The module imports only the standard library, existing `go.mod` dependencies and, in tests, the exported core packages the harness uses. The module lane writes `modules/client-portal/` and the one suite file `internal/modules/module_client-portal_pg_test.go`; it doesn't edit `.github/`, `AGENTS.md`, `docs/build/` control files, `go.mod` or any other file in a core package. C1 to C12 go to the module-core lane through docs/modules/CORE-CHANGES.md.

Client pages follow Unified's layout, phone width first: a header with the firm short name, the client portal label and the matter picker; nav for Dashboard, Documents, Invoices, Messages and Account, then other modules' client tabs from C1; a sign-out link to `/portal/sign-out`; and on the account page a "Change password" link to `/portal/security`. Colors come from the client portal colors in Settings > Appearance.

## 11. Out of scope

- Discovery sets and RFP responses (discovery module), questionnaires and intake (intake module), Zoom calls and meetings (Zoom module). Each adds its own client tabs through C1.
- Charging cards, trust deposits, refunds and e-signature (lawpay-docuseal).
- Converting uploads to other formats (core previews and Doc Tender).
- Group threads with several clients, message editing, and staff removal of client messages.
- Text messages to clients; client email goes through core system mail only.
- Self sign-up from the portal: every client user is granted by staff (C6), or by another module through the same core endpoints.
