# Contacts and matters

## Matters {#practice-matters}
<!-- template: practice/matters.html | path: /practice/matters | where: Sidebar > Matters | keywords: create matter, status, open matters, list -->
Lists every matter you can see; walled matters are absent unless you are inside the wall. Filter by status. The Create matter form at the top opens a new matter with its number (YYYY-NNNN by default), practice area, client and responsible attorney.

## Matter screen {#practice-matter}
<!-- template: practice/matter.html | path: /practice/matters/{matter_id} | where: Sidebar > Matters > a matter | keywords: parties, at a glance, timeline, conflict check, notes, edit history -->
The header shows the number, name, status, client, responsible attorney and markers. At a Glance shows stage, trust balance, time logged, days open, invoices and files. Tabs hold parties, time, expenses, invoices, trust, calendar and deadlines, notes and module tabs. Run the conflict check from here; attorneys decide each hit. The Case Timeline lists every event newest first, and Edit history shows each change from the audit log.

## Close a matter {#practice-close}
<!-- template: practice/close.html | path: /practice/matters/{matter_id}/close | where: Matter screen > Close | keywords: close reason, warnings, unpaid, trust balance -->
A matter can always close. An unpaid invoice balance or a nonzero trust balance shows a warning you acknowledge; neither blocks the close. An attorney or admin can reopen it later with a reason.

## Matters missing a case number {#practice-needs-case-number}
<!-- template: practice/needs_case_number.html | path: /practice/matters/needs-case-number | where: Sidebar > Matters > Missing case number | keywords: docket, worklist -->
Lists open matters with no case number that have a court or something shaped like a docket number in the name. Each row opens the matter; filling the case number removes it. The page changes no data.

## Contacts {#practice-contacts}
<!-- template: practice/contacts.html | path: /practice/contacts | where: Sidebar > Contacts | keywords: people, organizations, create contact, archive -->
Lists people and organizations. The Create contact form adds one with addresses, emails and phones. Archived contacts stay searchable.

## Contact screen {#practice-contact}
<!-- template: practice/contact.html | path: /practice/contacts/{contact_id} | where: Sidebar > Contacts > a contact | keywords: phones, emails, addresses, custom fields, ssn -->
Shows the contact's names, addresses, emails and phones with their history, matters, notes, custom fields and edit history. SSN and EIN show redacted to roles without attorney-only access.

## Notes {#practice-notes}
<!-- template: practice/notes.html | path: /practice/notes | where: Communications > Notes | keywords: search notes, pinned, tags -->
Every note you can see across matters and contacts. Search the title or body and filter by matter, author, tag and dates. Pinned notes come first, then newest, 25 a page.

## Activities Feed {#practice-activity}
<!-- template: practice/activity.html | path: /practice/activity | where: Activity > Activities Feed | keywords: recent activity, clients, staff, agents -->
The last 30 days of activity across the firm, newest first, grouped under Today, Yesterday, Earlier this week and Earlier this month. Tabs filter to clients, staff and agents; staff chips filter to one person. Walled matters stay hidden.

## Practice search page {#practice-search}
<!-- template: practice/search.html | path: /practice/search | where: Header > Search > full results | keywords: search results -->
The full-page view of a search, grouped by scope. It uses the same search service as the header box.
