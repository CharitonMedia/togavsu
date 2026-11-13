# The app shell and home screen

## Home screen {#home}
<!-- template: dashboard/home.html | path: /, /home | where: Sidebar > Home | keywords: widgets, layout, edit layout, save as, reset, default layout -->
**What it does.** Home is a 12-column grid of widgets. Each widget shows only what you can read through the API; a walled matter never appears.

**How to use it.** Click a widget's title to open the full page with the same filters and date range, for example Upcoming deadlines opens the deadlines page on the same dates. Click one row to open that record. Choose **Edit layout** to drag, resize, add and remove widgets; links don't open while you edit. **Save** keeps the layout you work in. **Save as** stores it by name (up to 10 saved layouts by default); you can rename, overwrite, delete or mark one as your default. **Reset** offers your default saved layout first, then your other saved layouts and your role's default; picking one restores it exactly.

**Where.** Sidebar > Home, or the firm short name at the top of the sidebar.

## Sidebar {#sidebar}
<!-- path: /api/v1/nav/context | where: Every staff screen > left edge | keywords: collapse, sections, badges, report a problem -->
The sidebar holds Home, Matters (with the count of active matters), Contacts and Calendar at the top, then the sections Communications, Documents, Litigation, Money, Intake, Activity and System. You see only the entries your role and the installed modules allow. Click a subgroup heading such as Trust to collapse or expand it; the sidebar button in the header collapses the whole sidebar to icons. Both choices are saved with your user, so they follow you to another computer. **Report a problem** at the bottom opens a short form; the report is saved and emailed to the firm's admins.

## Search {#search}
<!-- template: dashboard/search.html | path: /search | where: Header > Search box (Ctrl-K) | keywords: find, matter number, phone, invoice number, scope -->
Type at least two characters in the header's search box; results appear after you stop typing. Pick a scope beside the box: All, Matters, People, Invoices or Files. All shows up to five hits per scope, one scope up to eight. Arrow keys move through the hits, Enter opens one and Escape closes the list. Search honors matter walls and roles in every scope. Press Ctrl-K (Cmd-K on a Mac) from any screen to jump to the box.

## Notifications {#notifications}
<!-- template: dashboard/notifications.html | path: /notifications | where: Header > Bell | keywords: unread, mark all read, trust flag -->
The bell in the header shows your unread count. Open it to see recent notifications; clicking one marks it read and opens its item. **Mark all read** clears the count. You see only your own notifications: trust flags, deadline reminders, replenishment requests, matter assignment, and update failures and module alerts for admins.

## Timers {#timers}
<!-- template: dashboard/timers.html | path: /timers | where: Header > Timers | keywords: start, pause, stop, running timer -->
Header > Timers lists your running and paused timers. Start a timer on a matter, pause and resume it, and stop it. A stopped timer opens for review and saves as a draft time entry.

## My settings {#my-settings}
<!-- template: dashboard/me_settings.html | path: /me/settings | where: Header > User chip > My settings | keywords: dark mode, text size, reminders, time zone, idle, notification preferences -->
Choose your own light or dark mode and text size (the firm's colors stay), a personal display time zone, your default reminder lead times for events and deadlines, and automatic sign-out after idle time (never longer than the firm allows). The notification grid sets which kinds reach you in-app and by email; the trust below-zero email can't be turned off.

## My profile {#profile}
<!-- template: dashboard/profile.html | path: /me/profile | where: Header > User chip > My profile | keywords: name, initials, phone, picture, password, two-factor, recovery codes, api tokens -->
Change your name, initials and phone, and upload a profile picture (PNG, JPEG or WebP, up to 256 KB) that the user chip shows instead of your initials. Your email and roles are set by an admin. Change password asks for your current password and signs out your other sessions. Set up two-factor asks for your password, shows a QR code and a key for your authenticator app, and finishes when you enter a code from it; it turns two-factor on, or moves it to a new app, and any older authenticator works until then. New recovery codes replaces your unused codes and shows the new ones once. My API tokens lists the tokens you own; revoke any of them here. Admins can also issue a token for themselves.

## Quick create {#quick-create}
<!-- template: dashboard/quick_create.html | path: /quick-create | where: Header > Quick create | keywords: new matter, new person, log time -->
Quick create opens New matter, New person and Log time from any screen. Modules add their own items here.
