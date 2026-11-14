# Getting started

Togavsu is practice management for a small law firm: contacts, matters, time and invoicing, trust, the calendar, files, the document editor and Doc Tender, with modules for everything else. Every staff screen uses the same shell: the sidebar on the left, the header across the top and the page beside them. The **?** icon in a page's title bar opens that page's topic in this guide.

## Getting started as an admin {#tutorial-admin}
<!-- tutorial: admin | where: Help & Training > Getting started > Admin | path: /help/topics/tutorial-admin | keywords: first run, setup, two-factor, admin tabs -->
Your first day as the firm's admin:

1. **First-run setup.** On a new install the browser opens /setup. Enter the firm name and the first admin's name, email and password, then either scan the two-factor QR code with an authenticator app and type the six-digit code, or skip two-factor and sign in. Two-factor is optional for every role until an admin turns it on under Administrative > Security.
2. **Sign in.** Go to /login and enter your email and password. If two-factor is on for you, enter the current code too.
3. **Home screen.** You land on Home (sidebar > Home). It shows the admin role's default widgets. Click a widget's title to open the full page behind it, or a row to open that record.
4. **Firm profile.** Open Administrative > Firm profile and set the firm name, short name (shown at the top of the sidebar), address, phone and time zone.
5. **Users and roles.** Open Administrative > Users and roles to add each attorney, staff member and accountant. Each new user signs in with the password you set; they set up two-factor only if you require it for their role.
6. **Work through every admin tab.** Security, Practice, Time and billing, Trust, Calendar, Files and storage, Document editor, Doc Tender, Home screen, Lists and search, Notifications and mail, Appearance, AI and inference, Help, Modules and marketplace, Updates, Backups and logs, Agents and API and System each hold the settings for that area. Each setting shows its current value, its default and a Reset to default button; a change applies on the next page load.
7. **Role home screens.** On Administrative > Home screen, set the default layout each role starts with.
8. **Mail.** On Administrative > Notifications and mail, enter the SMTP settings so trust flags, reminders, invoices and problem reports can be emailed.

## Getting started as an attorney {#tutorial-attorney}
<!-- tutorial: attorney | where: Help & Training > Getting started > Attorney | path: /help/topics/tutorial-attorney | keywords: first day, approve invoices, trust withdrawals, conflicts -->
1. **Sign in and two-factor.** The admin creates your account. Sign in at /login. Two-factor is optional unless the admin requires it for attorneys; you can turn it on yourself under My profile.
2. **Home screen.** Home shows your time today and this week, the running timer, your open matters, upcoming deadlines, today's calendar, trust alerts and pending conflict checks. Move and resize widgets with Edit layout, then Save as to keep the layout by name.
3. **Open a matter.** Use the search box in the header (Ctrl-K) or sidebar > Matters. The matter screen has tabs for parties, time, expenses, invoices, trust, calendar and deadlines, notes and files.
4. **Log time.** Header > Timers starts and stops timers; Quick create > Log time records time directly.
5. **Decide conflicts.** On a new matter, run the conflict check and record clear, escalate or no go on each hit. Reasons are attorney-only.
6. **Approve and send invoices.** Money > Billing / Generate drafts invoices from unbilled time; open a draft under Money > Invoices to approve and send it.
7. **Trust.** Money > Trust holds the ledger, balances and replenishment requests. Attorneys post withdrawals and transfers to operating and resolve trust flags with a note.

## Getting started as staff {#tutorial-staff}
<!-- tutorial: staff | where: Help & Training > Getting started > Staff | path: /help/topics/tutorial-staff | keywords: first day, contacts, deadlines, time entry -->
1. **Sign in and two-factor.** Sign in at /login with the account the admin made. Two-factor is optional unless the admin requires it for staff.
2. **Home screen.** Your widgets show your time, timers, open matters, deadlines, today's calendar and pending conflict checks.
3. **Contacts and matters.** Quick create > New person and New matter open the create forms. Search finds people by name, email or phone and matters by number or case number.
4. **Calendar and deadlines.** Sidebar > Calendar shows day, week and month views; deadlines carry reminders by email and on the home screen.
5. **Time and draft invoices.** Record time and expenses on the matter or from Money > Time Entries. Staff can draft invoices, record payments and trust deposits; withdrawals and conflict decisions belong to attorneys.

## Getting started as an accountant {#tutorial-accountant}
<!-- tutorial: accountant | where: Help & Training > Getting started > Accountant | path: /help/topics/tutorial-accountant | keywords: read only, reports, reconciliation -->
1. **Sign in and two-factor.** Sign in at /login. Two-factor is optional unless the admin requires it for accountants.
2. **Home screen.** You see unbilled time, AR aging, trust alerts and replenishment requests.
3. **Read-only money.** Money > Invoices, Payments and the Trust group open read-only. Reports (unbilled time, time by user, AR aging, payments received, write-offs and the trust reports) export to CSV.
4. **Reconciliation.** Trust > Reconciliation shows each month's statement balance against the ledger and client balances.
