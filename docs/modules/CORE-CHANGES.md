# Core changes for the first five modules

This is the build list for the module-core lane. It merges every core change that the documents, email, client-portal, lawpay-docuseal and quickbooks specs ask for, so each capability is built once. Each module spec keeps its own list of core changes and tags every item with the CORE number below; this file is the definition the lane builds. Where a spec's wording and this file differ, this file wins.

Every item carries its own core tests, OpenAPI and MCP parity (api/contract), and the scope text in `api/contract/scopes.py` and `internal/identity/scopes.go`. Module lanes build none of these items.

## Terms used in this file

- **Viewer call.** A module token plus `X-Togavsu-Context-Token`. The core runs the call as that user, with that user's roles, walls and attorney-only rules (`module_delegation: delegated_viewer`, as today).
- **Service call.** A module token with no context token and no task authorization. The core runs it as the module's **service principal**: no user, no roles, audited under the module id with source `service`. A service call reaches only operations whose `module_delegation` names it (CORE-01), and each such operation states what the service principal may see. The **books reader** is the service principal of a module that holds `books:read` (CORE-02). It sits outside every matter wall, so it never sees a walled matter.
- **Task authorization.** A short-lived grant the core issues for one module, one record set and one workflow, sent back in `X-Togavsu-Task-Authorization`. Processor grants (CORE-23), refund grants (CORE-24), mail match tokens and filing grants (CORE-26, CORE-27) and the existing background file publication grants are all task authorizations, checked by the existing `noninteractive_workflow` rules.

## Manifest fields at a glance

The manifest decoder refuses unknown keys, so every field below lands in `internal/modules/manifest.go` with its validation. Manifests stay in the block-style YAML subset; flow mappings are refused.

| Field | Item | Used by |
| --- | --- | --- |
| `client_home` | CORE-10 | client-portal |
| `tabs[].placement: client`, `tabs[].params`, `tabs[].nav` | CORE-10 | client-portal, lawpay-docuseal |
| `sidebar[].section`, `subgroup`, `icon`, `order`, `badge {path, tone}` | CORE-08 | client-portal, email, documents, lawpay-docuseal, quickbooks |
| `notifications[] {kind, label, default_channels}` | CORE-03 | client-portal, email, lawpay-docuseal, quickbooks |
| `inbound_webhooks[] {id, path}` | CORE-07 | lawpay-docuseal |
| `payment_processor {destinations}` | CORE-23 | lawpay-docuseal |
| `container.system_mail` | CORE-05 | email |
| `device_connect` | CORE-28 | email |

## New scopes

`books:read` (CORE-02), `mail:send` (CORE-04), `mail:system` (CORE-05), `module_secrets:write` (CORE-06), `client_access:read` and `client_access:write` (CORE-14), `firm:read` (CORE-18), `users:directory` (CORE-19).

## The list

### CORE-01 Service calls

- Add the `module_delegation` value `delegated_viewer_or_service_principal`: the operation runs as the viewer when a context token is present and as the module's service principal when it is absent. The existing `service_principal` value stays for operations with no viewer form.
- A service call is refused with `scope_denied` on every operation not marked with one of those two values, and on every write unless the operation's own item below allows it.
- Operations that take service calls, each limited as its item says: the money reads of CORE-02 (books reader only), `createNotification` (CORE-03), `sendModuleMail` with `to_user_ids` (CORE-04), `matchMail` and `issueMailFilingGrant` (CORE-26, CORE-27), and `updateFeeAgreement` and `markFileStatus` for the signature module (CORE-25).
- Every service call writes an audit row naming the module, the operation and source `service`.
- Needed by: quickbooks 8.2, 8.7; email CC-3, CC-4, CC-8; lawpay-docuseal C8, C11; client-portal C7, C8.
- Criteria: MOD-QBO-06 to 18, 20, 25, 28, 33; MOD-EML-04, 11 to 20, 33; MOD-PAY-06, 14, 32, 44, 48; MOD-POR-25.

### CORE-02 Books reads

- Scope `books:read`: "Read the firm's invoices, payments, expenses, trust accounts and trust ledger, and the billing details of billed contacts, firm-wide except walled matters and without a viewer, for a module's background money work." Grantable only to a module token; an ordinary API token request for it is refused.
- Mark `listInvoice`, `getInvoice`, `listPayment`, `getPayment`, `listExpense`, `getExpense`, `listTrustAccount`, `getTrustAccount`, `listTrustLedger` (GET /api/v1/trust-ledger), `getTrustEntry` (GET /api/v1/trust-ledger/{trust_entry_id}) and `getTrustBalances` (GET /api/v1/trust/balances) `delegated_viewer_or_service_principal`. A service call to them needs `books:read` plus the operation's own read scope and runs as the books reader: the accountant role's field set, firm-wide, with matter walls applied exactly as for a user outside every wall.
- Walls: a walled matter's invoices, payments, expenses and trust entries are left out of every list and `not_found` on every get for the books reader, and `getTrustBalances` leaves out their amounts, so the books reader gets the answer a user outside the wall gets and no text, count or total from a walled matter. Nothing is redacted, because nothing walled is returned. When the wall is lifted, the records come back on the next read with no other change.
- `GET /api/v1/books/profile` (`getBooksProfile`, books:read, `service_principal`): `{firm_name, time_zone, currency}`, currency always `USD`.
- `GET /api/v1/books/matters/{matter_id}` (`getBooksMatter`, books:read, `service_principal`): `{id, number, name, status, client_contact_id, responsible_attorney_user_id}`; a walled matter is `not_found`; `client_contact_id` is the primary client party.
- `GET /api/v1/books/contacts/{contact_id}` (`getBooksContact`, books:read, `service_principal`): `{id, kind, display_name, given_name, family_name, organization_name, email, phone, billing_address {line1, line2, city, region, postal_code, country}, archived}` from the contact's primary current email, phone and address. Only a contact that is the client of a matter without a wall, or the contact on a payment or trust entry the books reader can read, is readable; any other is `not_found`, including a contact whose every matter is walled. No date of birth, identifier, tags or custom fields.
- Event delivery: `invoice.*`, `payment.*`, `expense.*` and `trust.*` events on a walled matter are dropped for a module holding `books:read`, as for any principal outside the wall. Lifting a wall sends no catch-up events; the module finds the records on its next full read. The derived read scope rule is unchanged.
- The books reader can call no write operation (`scope_denied`).
- Needed by: quickbooks 8.1 to 8.6; lawpay-docuseal C11.
- Criteria: MOD-QBO-01, 06 to 18, 23, 25, 27, 28, 32, 33; MOD-PAY-06, 14, 32, 48.

### CORE-03 Module notification kinds and createNotification

- Manifest `notifications`: a list of `{kind, label, default_channels}`. `kind` matches `^[a-z][a-z0-9_]{0,47}$` and is unique in the manifest; `label` is required; `default_channels` is a non-empty subset of `in_app` and `email`. The core names each kind `{module_id}.{kind}` (for example `client-portal.client_message`). Kinds join the notification preferences grid in a group under the module's name with those defaults; disabling the module hides the group, uninstall removes it.
- Implement `POST /api/v1/notifications` (`createNotification`; it is in the contract with no Go route today). `NotificationWrite` becomes: `kind` (required; a module token may send only its own declared kinds), `title` (1 to 200 characters), `link` (required; for a module token it must start with `/modules/{module_id}/` or `/portal/{module_id}/` of the calling module), optional `matter_id`, and exactly one recipient selector:
  - `user_id`: one active user. With `matter_id`, the user must be able to read the matter.
  - `audience` with `matter_id`: `responsible_attorney`, `matter_team` (the responsible attorney and every user assigned to the matter), `admins`, or `admins_and_responsible_attorney`. `matter_id` may be omitted only for `admins`.
  - `recipient_roles`: a non-empty subset of `admin` and `accountant`, accepted only on a service call.
- Allowed as a viewer call or a service call (`delegated_viewer_or_service_principal`). On a viewer call with `matter_id`, the viewer must be able to read the matter.
- The core resolves active recipients, drops any recipient the matter's wall excludes (admins included), creates one notification per recipient, and emails each recipient whose preference for that kind has email on, through the system mail router (CORE-05). A notification carries no body. Answer: `{created, notification_ids}`. `notification.created` fires per row.
- Needed by: client-portal C8; lawpay-docuseal C9; quickbooks 8.7; email CC-8.
- Criteria: MOD-POR-25; MOD-PAY-03, 14, 26, 31, 46, 53; MOD-QBO-05, 20, 22; MOD-EML-04.

### CORE-04 Module mail

- Scope `mail:send`: "Send mail from the firm to Togavsu users and to parties on a matter, through system mail." Module tokens only.
- `POST /api/v1/mail/messages` (`sendModuleMail`), `delegated_viewer_or_service_principal`, Idempotency-Key. Body: `subject` (1 to 200 characters), `text` (plain text, 1 to 20000 characters), optional `link_path`, optional `matter_id`, and exactly one of:
  - `to_user_ids`: 1 to 20 user ids. Inactive users are `skipped`. Allowed on a viewer call or a service call.
  - `to_contact_ids` with `matter_id`: 1 to 20 contact ids, each a party on that matter. Each goes to that contact's current primary email; a contact with no current email, or not a party on the matter, is `skipped`. Viewer call only, by a viewer with the admin, attorney or staff role who can read the matter.
- `link_path` must start with `/portal/` or `/modules/`; the core appends `https://{firm domain}{link_path}` as the last line of the text.
- The core resolves every address itself and never returns one. Each recipient gets their own message through the system mail router (CORE-05). Answer: `{results: [{user_id or contact_id, status, error}]}` with status `sent`, `failed` or `skipped`.
- With `matter_id`, one `mail.sent` event goes on the matter timeline: summary `Email sent: {subject}`, never the text, `client_visible` false.
- Needed by: client-portal C7; lawpay-docuseal C10.
- Criteria: MOD-POR-24; MOD-PAY-37, 40, 41.

### CORE-05 System mail routing

- Manifest `container.system_mail` (a module path). Install refuses it without the `mail:system` permission, and refuses a second installed module that declares it.
- Scope `mail:system`: "Receive the core's system mail and deliver it through the firm's mail."
- One `systemMailer` in `internal/core/mail.go` used by every system mail sender: billing's `smtpMailer` (`internal/core/finance.go`, `Mail:`), `adminMail` (`internal/core/documents.go`), the `core.mail_outbox` drain in `App.DeliverMail` (`internal/core/finance.go`, today `trust.SMTP`, which also carries marketplace module alerts from `internal/marketplace/alerts.go`), the UI's `trust.SMTP` mailer (`internal/core/ui.go`), the password reset mailer (`logMailer` in `internal/core/app.go`), the supervisor's notice file (`internal/supervisor/mail.go`, `Outbox`), notification email (CORE-03), module mail (CORE-04) and the client access mail (CORE-14).
- When the existing setting `mail.route_through_email_module` is true and the declaring module is installed and enabled, the core POSTs JSON `{id, kind, to, subject, text, attachments[{name, content_type, data_base64}]}` to the module path, signed with `X-Togavsu-Signature` (`SignWebhook`) and carrying `Idempotency-Key: <id>`. 202 means accepted. 422 is a permanent failure, recorded on the row. 5xx or a timeout is retried with the same key. 503 with problem code `not_configured`, or the module not running, sends by SMTP when the SMTP settings are complete, and otherwise leaves the row queued with the error. A `module_alert` or `update_failure` mail about the routing module itself always goes by SMTP when SMTP is configured.
- `testMail` (`POST /api/v1/mail/test`) goes through the router.
- Needed by: email CC-1; client-portal C6, C7; lawpay-docuseal C10.
- Criteria: MOD-EML-42 to 45, 49; MOD-POR-09, 24; MOD-PAY-37.

### CORE-06 Module secret store

- `PUT /api/v1/modules/{module_id}/secrets/{name}` (`putModuleSecret`), `GET` (`getModuleSecret`), `DELETE` (`deleteModuleSecret`). Scope `module_secrets:write`: "Store and read the calling module's own sealed secrets." Only that module's own bearer may call, with the path id equal to the token's module; a context token is ignored.
- Values are sealed with the core key like secret settings; the audit log records the name only; backups carry them with module settings; uninstall with keep-data keeps them; uninstall with delete removes them.
- Limits: 512 secrets per module, 16 KB each, names matching `^[a-z0-9/_-]{1,128}$`. `GET` of a missing name is `not_found`.
- Needed by: email CC-2 (`account/<account id>/<kind>`); quickbooks 8.8 (`qbo/client_secret`, `qbo/access_token`, `qbo/refresh_token`).
- Criteria: MOD-EML-01, 51; MOD-QBO-04, 05.

### CORE-07 Inbound provider webhooks

- Manifest `inbound_webhooks: [{id, path}]` (id a slug, path module-local).
- Public route `POST /hooks/{module_id}/{hook_id}`, `security: []` in OpenAPI, served by the core; caddy already sends every path to the core, so the Caddyfile doesn't change. The core forwards the raw body, query string and headers to the module's declared path, minus `Cookie`, `Authorization`, `X-Togavsu-Context-Token` and `X-CSRF-Token`, and adds `X-Togavsu-Module` and `X-Togavsu-Inbound-Hook`. POST only. A module not installed or disabled, or a hook it doesn't declare, answers 404 and forwards nothing.
- Registry settings `modules.inbound_webhook_max_bytes` (start 1048576; over is 413) and `modules.inbound_webhook_rate_per_minute` (start 120 per hook; over is 429 with Retry-After). The core returns the module's status and body and writes delivery counts, not bodies, to the audit log.
- This is the only public inbound route for modules. No other spec needs one today: quickbooks takes no Intuit webhooks and email polls instead of taking push notifications. A later module that takes provider pushes uses this route. Browser OAuth callbacks (email, quickbooks) are GET redirects to the module through the existing `/m/{module_id}/` proxy, which strips the core's cookie and credentials; they are not inbound webhooks and need no core change.
- Needed by: lawpay-docuseal C1.
- Criteria: MOD-PAY-15 to 22, 42 to 47, 53.

### CORE-08 Sidebar placement and badges

- Manifest `sidebar[]` entries gain `section` (one of `top`, `communications`, `documents`, `litigation`, `money`, `intake`, `activity`, `system`, the PRD 8.9 sections in `internal/dashboard/nav.go`; default `activity`), `subgroup`, `icon`, `order` (int, default 1000) and `badge {path, tone}` (tone `normal` or `warning`).
- `icon` names a glyph in the core icon set: the icons `catalog.json` uses, plus `puzzle` (the default) and three the core adds: `mail`, `message` and `file`. Any other value is refused at install.
- `internal/modules/nav.go` `NavEntries` uses these fields in place of the fixed Activity section, puzzle icon and order, and accepts a sidebar path that matches one of the module's home tabs.
- `getNavContext` (`GET /api/v1/nav/context`) fills each badge by calling the module's badge path with a context token for the viewer, waits at most 500 ms, caches the count 30 seconds per viewer and entry, and leaves the badge off when the call fails, times out or answers anything but `{count}` with a non-negative integer.
- Needed by: client-portal C9; email CC-6; documents CC-7; lawpay-docuseal C14; quickbooks 8.11.
- Criteria: MOD-POR-08, 26; MOD-EML-28.

### CORE-09 Frame bridge and sandbox

- The core page that frames a module (staff tabs, matter items, widgets, and client tabs from CORE-10) listens for `postMessage` from its own child frame; the source window must be that frame's window, and anything else is ignored. The frame CSP allows that one inline script by nonce.
- Messages from the frame:
  - `{type: "togavsu:editor.open", file_id}`: staff frames only. Checks write access as the viewer, then opens `/files/{file_id}/edit` (PRD 8.20 Open).
  - `{type: "togavsu:navigate", page, params}`: the same allowlist and typed filters as widget targets (a tab of the same module, or a core page), checked as the viewer.
  - `{type: "togavsu:context.refresh", request_id}`: answered while the core session lives with `{type: "togavsu:context.token", request_id, token, expires_at}`, a fresh context token for the same user, module and matter, posted to the frame's window; after the session ends, `{type: "togavsu:context.error", request_id, code: "session_expired"}`.
- One sandbox list per frame kind. Today both are `allow-scripts allow-forms`.
  - Staff frames (`writeFrame` in `internal/modules/http.go` and the matter-screen frame in `web/templates/practice/matter.html`): `allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox`.
  - Client frames (CORE-10): `allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox allow-top-navigation-by-user-activation`.
  - Neither ever gets `allow-same-origin`. The existing assertion in `internal/modules/acceptance_pg_test.go` that the widget frame's sandbox is exactly `allow-scripts allow-forms` changes to the staff list.
- Needed by: documents CC-6; client-portal C2; email CC-9 (OAuth consent windows, attachment downloads); quickbooks 8.12 (the Connect window); lawpay-docuseal C13 (the Sign window and the Back link).
- Criteria: MOD-DOC-05, 27, 28; MOD-POR-07, 21, 31, 32; MOD-EML-03; MOD-QBO-04; MOD-PAY-49.

### CORE-10 Client pages and client tabs

- Manifest: `tabs[].placement` accepts `client`; `tabs[].params` lists the query parameters the core passes through to the module page (any placement); `tabs[].nav` (boolean, default true) keeps a client tab out of the portal navigation when false. Top-level `client_home` names one of the module's client tabs, and only one enabled module may declare it (a second install that declares it is refused).
- Routes: `GET /portal` sends a client-only user to the `client_home` tab. `GET /portal/{module_id}/{tab_id}` frames a module's client tab full-window, with no staff shell, for a client-only user, with a context token for that user; a `matter_id` param sets the matter claim only when the client may read that matter, else 404. Only the tab's declared `params` pass through. Staff, attorney, accountant and admin users get 404 on `/portal/*`.
- After sign-in and after a password reset, a client-only user goes to `/portal`, not `/practice/matters`.
- Core client pages: `/portal/security` (change password, optional TOTP, the forced change of CORE-14) and `POST /portal/sign-out`. With no module declaring `client_home`, `/portal` shows the firm name and "The client portal isn't set up yet."
- `GET /api/v1/nav/client-context` (client role, profile:read, viewer call) lists `{module_id, tab_id, title}` for every enabled module's client tabs with `nav` true.
- Needed by: client-portal C1; lawpay-docuseal C12 (`pay` and `sign` with `nav: false`).
- Criteria: MOD-POR-05, 06, 10, 13, 30, 31, 32; MOD-PAY-11, 13, 48, 49.

### CORE-11 Client matter reads and the access window

- `GET /api/v1/matters` and `GET /api/v1/matters/{matter_id}` admit the client role (client mode `own`): only matters where the user's linked contact is a `client` party, newest `opened_on` first, with a client field allowlist (`id`, `number`, `name`, `status`, `practice_area_name`, `opened_on`, `closed_on`, `responsible_attorney_name`).
- Setting `client_access.days_after_close` (int, default 14, Security tab): a client loses read access to a closed matter, and to its files and invoices, that many days after `closed_on`; reopening restores it. `access.Reader.clientMatter` applies it, so the API, MCP, portal, published view reads and downloads agree.
- `GET /api/v1/me` adds `access_ended_matters` (a count) for client users.
- Needed by: client-portal C3.
- Criteria: MOD-POR-01, 05.

### CORE-12 Client trust reads

- `GET /api/v1/me/trust` (client role, trust:read, viewer call): for each readable matter, the client's own trust balance in cents and the active replenishment requests naming the client.
- `GET /api/v1/me/trust-ledger?matter_id=` (client role, trust:read): the ledger entries naming the client (date, type, amount, description). No other client's figures and no account numbers.
- The client role gains read-only `listTrustReplenishment` and `getTrustReplenishment` for the client's readable matters, returning only requests naming the client's linked contact.
- Every client read of a replenishment request uses one field allowlist: `id`, `matter_id`, `amount_cents`, `message`, `status`, `created_at`.
- Needed by: client-portal C4; lawpay-docuseal C5 (the read half).
- Criteria: MOD-POR-15; MOD-PAY-11, 13.

### CORE-13 Client contact self-edit

- `GET /api/v1/me/contact` and `PATCH /api/v1/me/contact` (client role, profile:read and profile:write) read and change the linked contact's primary email, primary phone and mailing address. A change sets `effective_to` on the old row and adds a new primary row (PRD 5), writes the audit log with the client as actor, and emits `contact.updated` with a client-visible timeline row. The sign-in email stays separate (`/api/v1/me/email-change`).
- Needed by: client-portal C5.
- Criteria: MOD-POR-16.

### CORE-14 Client access management

- Scopes `client_access:read` and `client_access:write` (admin, attorney and staff roles; modules may request them).
- Migration: `core.users.must_change_password` (boolean, default false). A client with it set is sent to `/portal/security` before any other page, and every API call except `/api/v1/me/password` and `/api/v1/auth/logout` answers 403 `password_change_required`.
- `GET /api/v1/client-access?contact_id=` returns `{user_id, email, active, must_change_password, last_sign_in_at, granted_at}` or 404.
- `POST /api/v1/client-access {contact_id, email}` creates or finds the client user linked to that contact (email defaults to the contact's primary email; 400 when there is none), sets a new temporary password with `must_change_password`, mails it by system mail (CORE-05) with the sign-in address, and returns `{user_id, created, email, mail_status}` with `mail_status` one of `sent`, `failed` or `skipped`, never the password.
- `POST /api/v1/client-access/{user_id}/temporary-password` sets a new temporary password, revokes the user's sessions and returns `{temporary_password}` once. `POST /api/v1/client-access/{user_id}/deactivate` and `/reactivate`.
- Each endpoint refuses a user holding any role besides client (409) and a contact who is a client party on no matter the caller can read (404). All are viewer calls.
- Events `client_access.granted`, `client_access.password_reset`, `client_access.deactivated`, `client_access.reactivated`. Temporary passwords never reach logs, the audit log or events.
- Needed by: client-portal C6; lawpay-docuseal C15 (read only, to check that a signer can sign in).
- Criteria: MOD-POR-09 to 12; MOD-PAY-36.

### CORE-15 Client appointments

- `GET /api/v1/me/appointments` (client role, calendar:read): upcoming `client_meeting` and `court_date` events on the client's readable matters, with `title`, `kind`, `start`, `end`, `all_day`, `location` and `matter_id` only.
- Needed by: client-portal C11.
- Criteria: MOD-POR-15.

### CORE-16 File origin and source on module publication

- `FileBulkItem` (used by `publishFile` and `bulkFile`) gains optional `origin` (`firm` or `received`), accepted only from a module token. Omitted, the current rule stays (`receivedSource` makes source `email` received).
- `files.sourceFor` returns `client_upload` when the delegated viewer is a client-only user, even when a module made the call, so the file's origin is `received`, its uploader is the client user, Doc Tender treats it as a client upload and the timeline highlights it. The audit row still names the module as the channel. A module may not pass `origin: firm` for a client-only viewer (`validation_failed`).
- Needed by: email CC-5; client-portal C10.
- Criteria: MOD-EML-26; MOD-POR-17.

### CORE-17 Library files

- Migration: `library BOOLEAN NOT NULL DEFAULT false` on `core.files`.
- `createFileUpload`, `publishFile` and `FileBulkItem` accept `library`; `true` requires `matter_id` and `contact_id` null, else `validation_failed`. `File` returns `library`. `listFile` takes a `library` boolean filter. `updateFileMetadata` refuses setting a matter or contact on a library file with `validation_failed`.
- Access is the existing firm-only rule (admin, attorney, staff; accountant and client get `not_found`).
- Doc Tender queues no job for a library file's versions, reports `getDocTenderStatus` as `not_processed_library`, opens no exception, spends no tokens, and its nightly sweep skips library files. `file.version_created` still fires.
- Needed by: documents CC-1.
- Criteria: MOD-DOC-03, 05, 06, 39.

### CORE-18 Firm profile read

- `GET /api/v1/firm-profile` (`getFirmProfile`), scope `firm:read`, every role including client, viewer call (`delegated_viewer`). Returns `name`, `short_name`, `address`, `phone`, `email`, `website`, `logo_file_id` and `time_zone` from the `firm.*` settings, plus `client_portal`, the client portal colors and label from Settings > Appearance as `getAppearance` returns them. Read only; writes stay on the admin settings.
- Needed by: documents CC-2; client-portal C12.
- Criteria: MOD-DOC-11; MOD-POR-05.

### CORE-19 User directory

- `GET /api/v1/user-directory` (`listUserDirectory`), scope `users:directory`, roles admin, attorney, staff and client, viewer call, cursor and limit. Filters: `ids` (up to 100), `role`, `active`. Each row: `id`, `name`, `roles`, `active`. No email, auth or rate fields.
- A client caller gets only `id` and `name`, and only for active users who hold a staff, attorney or admin role.
- Needed by: documents CC-3; client-portal C12.
- Criteria: MOD-DOC-11, 34; MOD-POR-26.

### CORE-20 Delegation on settings reads

- Set `module_delegation: delegated_viewer` on `listCounty`, `getCounty`, `listPracticeArea`, `getPracticeArea`, `listPartyRole`, `getPartyRole`, `listCustomFieldDefinition` and `getCustomFieldDefinition`. Scope (`settings:read`) and roles stay as they are.
- Needed by: documents CC-4.
- Criteria: MOD-DOC-11, 37.

### CORE-21 Archive entries

- `createFileArchive` accepts a third selector, `entries`: an array of `{file_id, version_id, path}` or `{file_id, rendition_id, path}`, exclusive with `file_ids` and `folder_id`. `path` is relative, `/`-separated, at most 255 bytes, with no leading `/`, no backslash, no `.` or `..` segment and no control character, and unique without regard to case; otherwise `validation_failed` and no bytes.
- Each entry is checked as the viewer before the first byte (a hidden one is `not_found` for the whole archive), the quarantine rule is unchanged, each entry writes one `file.downloaded`, and the zip is streamed.
- Credentials, the context token included, are checked once when the request starts, and that one response streams to completion after the token's expiry. This holds for every selector, so client-portal's `file_ids` archive relay streams the same way.
- Needed by: documents CC-5.
- Criteria: MOD-DOC-28 to 32; MOD-POR-21.

### CORE-22 Linkable core records

- Add to `coreRecords` in `internal/modules/manifest.go`: `fee_agreement` (`fee_agreements`), `payment_refund` (`payment_refunds`, created by CORE-24) and `trust_account` (`trust_accounts`), so module foreign keys and extensions on them pass the install check.
- Add `fee-agreements` to `pluralRecords` in `internal/modules/http.go` for `include=`.
- Needed by: lawpay-docuseal C7 (`fee_agreement`, `payment_refund`); quickbooks 8.9 (`trust_account`, for `trust_bank_map`).
- Criteria: MOD-PAY-23, 34; MOD-QBO-02.

### CORE-23 Payment processors

- Manifest `payment_processor: {destinations: [operating, trust]}` registers a processor.
- Implement `initiateProcessorPayment`, delivery of `processor_authorization` on `payment.processor_authorized`, and acceptance of that task authorization on `createPayment` and `updatePaymentStatus`, as `docs/api/conventions.md` describes; `internal/billing/doc.go` lists them as not implemented.
- Client-initiated payments: `initiateProcessorPayment` is allowed on a client viewer call, limited to the client's own readable matter, with `contact_id` the client user's linked contact, and destination trust, or destination operating only with the `invoice_id` of that client's own sent invoice with a balance due. Anything else stays `not_found` or `forbidden`.
- Pending grant lifetime: a processor grant whose payment was created pending stays valid for `updatePaymentStatus` on that payment until it leaves pending or `payments.processor_pending_grant_days` passes (registry setting, start 30).
- Replenishment link: `ProcessorInitializationWrite` and `PaymentWrite` take optional `replenishment_request_id` (an active request on the same matter and client). Completing that trust payment marks the request satisfied and emits `trust.replenishment_satisfied`.
- Needed by: lawpay-docuseal C2, C3, C4, C5 (the write half).
- Criteria: MOD-PAY-04 to 06, 09, 11 to 14, 32.

### CORE-24 Payment refunds

- Core record `payment_refunds`: payment, amount_cents, reason, reference_number, status (pending, completed, failed), trust_entry_id, source, created_by. `Payment` gains read-only `refunded_cents` and `refund_state` (none, partial, full); its status stays completed.
- `POST /api/v1/payments/{payment_id}/refunds` (`createPaymentRefund`): body `amount_cents`, `reason`, optional `reference_number`, `status` pending or completed. Roles admin and attorney; scope payments:write, plus trust:withdraw when the payment's destination is trust; viewer call; Idempotency-Key. Refused with `validation_failed`, error code `refund_exceeds_payment`, when the amount is zero or more than the payment amount minus all pending and completed refunds; refused when the payment isn't completed.
- `POST /api/v1/payments/{payment_id}/refunds/{refund_id}/status` (`updatePaymentRefundStatus`): pending to completed or failed; the same status replays. Viewer call, or a task authorization: the `refund_authorization` grant the core delivers on `payment.refund_authorized` to the module named in the payment's `source` when a refund is created pending.
- `GET /api/v1/payments/{payment_id}/refunds` (`listPaymentRefunds`): payments:read.
- Completion, in one transaction: for a trust payment, one trust ledger row of entry type refund on the payment's trust account, client and matter, under the 8.4 below-zero rule (it posts, flags and emails); for an operating payment applied to an invoice, a lower amount paid on that invoice, and status back from paid to sent when a balance reappears.
- Events `payment.refund_created`, `payment.refund_authorized`, `payment.refunded` and `payment.refund_failed` (read scope payments:read), plus `trust.entry_posted` and `trust.flag_raised` as they occur.
- Needed by: lawpay-docuseal C6.
- Criteria: MOD-PAY-23 to 31.

### CORE-25 Fee agreement signature callback

- `fee_agreements` gains `signature_module_id`, set when a module token moves an agreement to sent.
- That module may then, by service call (CORE-01), call `updateFeeAgreement` to move sent to signed with `signed_on` and `signed_file_id`, and `markFileStatus` to set that one file executed. No other field or transition opens to it.
- Needed by: lawpay-docuseal C8.
- Criteria: MOD-PAY-44, 45.

### CORE-26 Mail matching

- `POST /api/v1/mail/matches` (`matchMail`), scopes `matters:read` and `contacts:read`, `delegated_viewer_or_service_principal`.
- Body: `addresses`, `matter_numbers`, `case_numbers`, `name_tokens`, `known_matter_ids`, each at most 50 entries.
- Answer: `contacts_by_address` (non-archived contacts whose current email equals the address, case-insensitive), and `matters` (at most 50), each with id, number, name, status, case number, practice area, its client contacts' first and last names, and `via` (`address`, `matter_number`, `case_number`, `case_number_in_name`, `client_name`, `known`). Case numbers match the case number field exactly, or the matter name with a boundary that excludes a following hyphen.
- A service call omits every walled matter and also returns `match_token`, a task authorization naming the module and the returned matter ids, valid 15 minutes. A viewer call applies that viewer's walls.
- Needed by: email CC-3.
- Criteria: MOD-EML-11 to 20, 33, 41.

### CORE-27 Mail filing grant

- `POST /api/v1/mail/filing-grants` (`issueMailFilingGrant`), scopes `notes:write` and `files:write`, `service_principal`. Body `{match_token, matter_id, message_key}`; the matter must be in the match token and unwalled. Returns a task authorization for that module, matter and message key, valid 15 minutes, and emits `mail.filing_authorized`.
- New `noninteractive_workflow` value `mail_filing`, accepted with that grant on `createNote` (its `module_delegation` changes from `delegated_viewer` to `delegated_viewer_or_noninteractive`), `createFileUpload`, `getFileUpload`, `putFileChunk`, `completeFileUpload`, `publishFile`, `bulkFile` and `createFileLink`, each limited to the grant's matter.
- New workflow value `mail_matching`, accepted on `createAiCall` with a match token, for the module's declared AI tasks, without `matter_id`.
- Needed by: email CC-4.
- Criteria: MOD-EML-11, 17, 21, 23, 24.

### CORE-28 Device grants

- Manifest `device_connect` (a module path).
- Core page `GET /modules/{module_id}/device-connect`: asks the signed-in staff user to connect a device to that module. On approval it creates the grant and redirects to `/m/{module_id}<device_connect>#grant=<secret>`.
- `POST /api/v1/me/module-devices` (`createModuleDeviceGrant`): session only, used by that page. `GET /api/v1/me/module-devices` (`listModuleDeviceGrants`): profile:read. `DELETE /api/v1/me/module-devices/{device_id}` (`revokeModuleDeviceGrant`): profile:write; an admin may revoke anyone's.
- `POST /api/v1/modules/{module_id}/device-context-tokens` (`exchangeModuleDeviceGrant`): the module's own bearer, scope `modules:read`, plus the device secret. Returns a context token for that user, like `createModuleContextToken`, valid 10 minutes, with an optional `matter_id` checked against the wall.
- A grant lapses after 90 days unused and when the user is deactivated. Events `module_device.created` and `module_device.revoked`.
- Needed by: email CC-7.
- Criteria: MOD-EML-39 to 41, 49.

### CORE-29 Acceptance harness core surface

- The module acceptance suites run against `env.compose()` in `internal/modules/harness_pg_test.go`, which today mounts only the module, marketplace, calendar, billing and practice routes. Extend `compose` so every core route a module spec's core API table lists is served by `e.srv`: build each further core domain handler (files and folders, contacts, trust and notifications, notes, mail, settings and firm profile, user directory, identity and `/api/v1/me`, client access, the `/portal` pages and `/hooks`) the way compose builds billing, calendar and practice today, with `e.principal`, and append it to `domains`. People authenticate with `X-Test-User` as today; modules with their bearer and context tokens.
- No other harness change. `fakeRuntime.handlers` (a map of module id to `http.Handler`) already lets each suite run its module in process: the suite sets `e.rt.handlers["<module id>"]` before install to a handler that, on its first request after each Deploy, reads `e.rt.last("<module id>")` for the deployment's secrets, opens the module's own login with `e.moduleDB`, and builds the module's handler with the module's fakes injected. No Deploy hook, no new harness package and no change to `fakeRuntime`.
- Each module's Postgres acceptance suite is `internal/modules/module_<id>_pg_test.go` in package `modules_test` (the harness's package), with each hyphen in the module id written as an underscore (`module_client_portal_pg_test.go`, `module_lawpay_docuseal_pg_test.go`); its module code lives in `modules/<id>/` with the id as it is. Module lanes write their one suite file there and touch nothing else in `internal/modules`. The module-core lane never edits any `internal/modules/module_*_pg_test.go` file; those suite files belong to the module lanes, and the core lane's own tests go beside the code it changes.
- Needed by: every module's acceptance suite.
- Criteria: every MOD-DOC, MOD-EML, MOD-POR, MOD-PAY and MOD-QBO criterion run against Postgres.

## Shared files

- **go.mod.** The email module's three libraries, `github.com/emersion/go-imap/v2` v2.0.0-beta.5, `github.com/emersion/go-message` v0.18.1 and `golang.org/x/net` v0.33.0 (the HTML parser behind its sanitizer), are already pinned in `go.mod` and `go.sum` by the control change that adds these lanes, before any lane starts. No lane edits `go.mod` or `go.sum`: not the email lane, not any other module lane and not the module-core lane. None of the items above needs a new dependency. A lane that finds it needs another dependency proposes it in its PR for the integrate-modules lead.
- **Migrations.** Core migrations from this list: `core.users.must_change_password` (CORE-14), `core.files.library` (CORE-17), `core.payment_refunds` and the `payments` refund columns (CORE-24), `core.fee_agreements.signature_module_id` (CORE-25), the module secret table (CORE-06) and the module device grant table (CORE-28).
- **Settings.** `client_access.days_after_close` (CORE-11), `modules.inbound_webhook_max_bytes` and `modules.inbound_webhook_rate_per_minute` (CORE-07), `payments.processor_pending_grant_days` (CORE-23). `mail.route_through_email_module` exists already (CORE-05).
- **Events.** `mail.sent` (CORE-04), `client_access.granted`, `client_access.password_reset`, `client_access.deactivated`, `client_access.reactivated` (CORE-14), `payment.refund_created`, `payment.refund_authorized`, `payment.refunded`, `payment.refund_failed` (CORE-24), `mail.filing_authorized` (CORE-27), `module_device.created`, `module_device.revoked` (CORE-28).

## Order

The lane builds in this order, because later items use earlier ones: CORE-29 and CORE-01 first; then CORE-22, CORE-08, CORE-09, CORE-03, CORE-05, CORE-04 and CORE-06; then the client items CORE-10 to CORE-16; then CORE-17 to CORE-21; then CORE-02 and CORE-23 to CORE-28.
