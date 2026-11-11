# Administrative

The admin page lives at /admin (System > Administrative, admins only). It is organized in tabs. Every setting shows its current value, its default and a **Reset to default** button. A change applies on the next request with no restart and is written to the audit log with the old and new values.

## Firm profile {#admin-firm_profile}
<!-- tab: firm_profile | path: /admin?tab=firm_profile | where: Administrative > Firm profile | keywords: firm name, short name, logo, time zone, matter number format -->
Firm name, short name, address, phone, email, website, logo, time zone and the matter number format and prefix (default YYYY-NNNN).

## Users and roles {#admin-users_roles}
<!-- tab: users_roles | path: /admin?tab=users_roles | where: Administrative > Users and roles | keywords: add user, deactivate, roles, walls, hourly rate -->
The firm's users with their roles and active flag, and the seat count. Users are added and deactivated through the users API; matter walls are set on each matter.

## Security {#admin-security}
<!-- tab: security | path: /admin?tab=security | where: Administrative > Security | keywords: two-factor, idle, lockout, rate limit -->
Two-factor by role (always required for admins), the longest idle time before sign-out (default 12 hours), failed sign-ins before lockout (default 5), lockout length (default 15 minutes) and the API rate limit (default 600 a minute per token).

## Practice {#admin-practice}
<!-- tab: practice | path: /admin?tab=practice | where: Administrative > Practice | keywords: practice areas, stages, case number patterns, conflict score -->
Case number patterns (default Missouri docket numbers), the lowest conflict score shown (default 60) and the practice lists.

## Time and billing {#admin-time_billing}
<!-- tab: time_billing | path: /admin?tab=time_billing | where: Administrative > Time and billing | keywords: time increment, rates, payment terms, grace period, late interest, markup -->
Time increment (default 6 minutes; 1, 6, 10 or 15), default rates, expense markup (default 0), invoice numbering, payment terms (default 30 days), grace period (default 15 days), late interest (default off) and the default client message.

## Trust {#admin-trust}
<!-- tab: trust | path: /admin?tab=trust | where: Administrative > Trust | keywords: trust accounts, flag recipients, evergreen, reconciliation day -->
Extra recipients for trust flag emails (the admins and the responsible attorney always get them), the default evergreen minimum and the reconciliation reminder day (default the 5th).

## Calendar {#admin-calendar}
<!-- tab: calendar | path: /admin?tab=calendar | where: Administrative > Calendar | keywords: reminder offsets, ical, available dates -->
Default reminder offsets (deadlines 7 and 1 days before; events 1 day before), iCal feeds on or off, and the available dates defaults.

## Files and storage {#admin-files_storage}
<!-- tab: files_storage | path: /admin?tab=files_storage | where: Administrative > Files and storage | keywords: storage backend, encryption, largest upload, default folders, drop folder -->
The default backend (default local disk), encryption, largest upload (default 2 GB), default folders per practice area and the drop folder. Storage backends and Move files open from here.

## Document editor {#admin-document_editor}
<!-- tab: document_editor | path: /admin?tab=document_editor | where: Administrative > Document editor | keywords: lawcraft commit, edit lock timeout, runs, pdf previews -->
The LawCraft commit (read only), edit lock timeout (default 5 minutes), editor runs at once (default 2), each run's time and memory limits (60 seconds and 1 GB) and PDF previews (default on).

## Doc Tender {#admin-doc_tender}
<!-- tab: doc_tender | path: /admin?tab=doc_tender | where: Administrative > Doc Tender | keywords: naming pattern, categories, confidence, quiet time, daily summary -->
On or off (default on), the naming pattern, the review confidence (default 0.90), quiet time before photos group (default 10 minutes), retries, review recipients and the daily summary (default 7:00 a.m.).

## Home screen {#admin-home_screen}
<!-- tab: home_screen | path: /admin?tab=home_screen | where: Administrative > Home screen | keywords: role default layout, saved layouts per user -->
Each role's default layout and widgets, and how many layouts each user may save (default 10). Pick a role, arrange its widgets and save; users start from it and can always reset to it.

## Lists and search {#admin-lists_search}
<!-- tab: lists_search | path: /admin?tab=lists_search | where: Administrative > Lists and search | keywords: search start, delay, hits per scope, feed window, page size -->
Search start (default 2 characters) and delay (default 180 ms), hits per scope (5 in All, 8 in one scope), Activities Feed window (30 days) and page size (100), notes page size (25).

## Notifications and mail {#admin-notifications_mail}
<!-- tab: notifications_mail | path: /admin?tab=notifications_mail | where: Administrative > Notifications and mail | keywords: smtp, system mail, problem report recipients, default channels -->
SMTP settings for system mail, routing through the email module, the firm default channels for each notification kind and the problem report recipients (default the admins).

## Appearance {#admin-appearance}
<!-- tab: appearance | path: /admin?tab=appearance | where: Administrative > Appearance | keywords: colors, presets, fonts, text size, dark mode, staff portal label -->
Staff portal colors for the sidebar, header, page and accents, with the presets Navy and gold, Charcoal and copper, Forest and cream and Maximum contrast; client portal header color; heading and body fonts; text size; the staff and client portal labels; and the firm's default mode (default follow the device). Changes show on the next page load.

## AI and inference {#admin-ai}
<!-- tab: ai | path: /admin?tab=ai | where: Administrative > AI and inference | keywords: api keys, provider, model, fallback, timeout -->
The one place every AI call gets its provider, model and key: keys per provider, primary and secondary providers with fast and strong models, the timeout (default 30 seconds), per-task overrides and the Doc Tender task models. Modules never see the keys.

## Help {#admin-help}
<!-- tab: help | path: /admin?tab=help | where: Administrative > Help | keywords: help sections, help items, video, image -->
The firm's help sections and items shown under the guides on Help & Training. Add a section, then add text, video or image items with a title and sort order. An image is an uploaded file, given by its file id; a video is an uploaded file or an https link, which opens in a new tab.

## Modules and marketplace {#admin-modules}
<!-- tab: modules | path: /admin?tab=modules | where: Administrative > Modules and marketplace | keywords: install, enable, disable, signing keys, marketplace -->
Installed modules with install, configure, permissions, enable, disable and remove, which module replaces time, trust or calendar, trusted signing keys, the marketplace address and alert frequency.

## Updates {#admin-updates}
<!-- tab: updates | path: /admin?tab=updates | where: Administrative > Updates | keywords: update window, pause, update now, history -->
The update window (default 2:00 to 4:00 a.m.), pause updates, update now and the update history.

## Backups and logs {#admin-backups}
<!-- tab: backups | path: /admin?tab=backups | where: Administrative > Backups and logs | keywords: nightly backup, retention, destinations, restore, log retention -->
Nightly backup time (default 1:00 a.m.), local retention (default 30 days), destinations, the monthly test-restore day, backup now, download, restore and log retention (default 400 days).

## Agents and API {#admin-agents_api}
<!-- tab: agents_api | path: /admin?tab=agents_api | where: Administrative > Agents and API | keywords: api tokens, scopes, webhooks, llms.txt -->
API tokens and scopes, webhook retries (default 8 over 24 hours), and llms.txt: show or hide the firm name and the firm paragraph.

## System {#admin-system}
<!-- tab: system | path: /admin?tab=system | where: Administrative > System | keywords: seat count, audit log, problem reports -->
The active seat count, the audit log viewer and the problem reports users sent with Report a problem.

# Help and Training

## Help & Training {#help-center}
<!-- template: dashboard/help.html | path: /help | where: System > Help & Training, or Header > Help | keywords: guide, pdf, getting started, firm help -->
The user guide in HTML (opens in a new tab) and as a PDF, the getting-started tutorial for each role, the guide's search, and the firm's own help sections and items.

## Guide topic {#help-topic}
<!-- template: dashboard/help_topic.html | path: /help/topics/{topic_id} | where: Any screen > ? icon | keywords: topic, help page -->
One topic of this guide, opened from a screen's ? icon. It says what the screen does, how to use it and where it lives, with a link that opens the screen and a link to the same topic in the full guide.

## Administrative page {#admin}
<!-- template: admin/admin.html | path: /admin | where: System > Administrative | keywords: settings, tabs, reset to default -->
The admin page itself: pick a tab across the top. Each tab's topic above says what it holds.

## Sign in {#sign-in}
<!-- path: /login, /setup, /logout | where: Before sign-in | keywords: password, two-factor, first-run setup -->
Sign in with your email and password, then the six-digit two-factor code. A new install opens first-run setup at /setup.
