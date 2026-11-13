# Calendar, files and the editor

## Calendar {#calendar}
<!-- template: calendar/calendar.html | path: /calendar | where: Sidebar > Calendar | keywords: day, week, month, firm-wide, court date, event -->
Day, week and month views, per user or firm-wide, filtered by matter and kind. The New event button adds an event. Today's calendar on Home opens today in day view.

## Event {#calendar-event}
<!-- template: calendar/event.html | path: /calendar/events/{id} | where: Calendar > an event | keywords: attendees, travel, location -->
One event with its kind, times, matter, location, travel minutes, attendees and notes.

## Deadlines {#calendar-deadlines}
<!-- template: calendar/deadlines.html | path: /calendar/deadlines | where: Sidebar > Calendar > Deadlines | keywords: upcoming deadlines, overdue, done, waived, date range -->
Deadlines in a date range, overdue ones first. The Upcoming deadlines widget opens this page on the same dates. The New deadline button adds one. Mark a deadline done or waived.

## Deadline {#calendar-deadline}
<!-- template: calendar/deadline.html | path: /calendar/deadlines/{id} | where: Deadlines > a deadline | keywords: reminders, responsible -->
One deadline with its due date, responsible user, reminder offsets and status.

## Available dates {#calendar-available}
<!-- template: calendar/available.html | path: /calendar/available-dates | where: Sidebar > Calendar > Available dates | keywords: hearing, pleading, letter, half days -->
Lists the days an attorney is free for a hearing and formats them as a pleading or a letter with Copy and Download. It never changes the calendar.

## Calendar feeds {#calendar-feeds}
<!-- template: calendar/feeds.html | path: /calendar/feeds | where: Sidebar > Calendar > Feeds | keywords: ical, google, outlook, revoke -->
Read-only iCal feeds for you and the firm with revocable secret links.

## Matter files {#files-matter}
<!-- template: files/matter_files.html | path: /practice/matters/{matter_id}/files | where: Matter screen > Files | keywords: upload, folders, versions, download, zip, status -->
Upload by drag and drop (8 MB chunks that resume), move files between folders, mark draft, final, executed or filed, download one file, a version, or a zip, and restore an older version as a new one. Each file shows its Doc Tender status: Doc Tender reads, names, categorizes and files every new file as it arrives, and anything it can't settle goes to its review queue with the file still available. See [Doc Tender](/help/topics/doc-tender) for what it does with each file type and how names are made.

## Storage {#files-storage}
<!-- template: files/storage.html | path: /files/admin/storage | where: Administrative > Files and storage > Storage backends | keywords: backends, test, move files, encryption -->
Storage backends with a Test button each, the default backend and Move files between backends.

## Document editor {#editor-edit}
<!-- template: editor/edit.html | path: /files/{file_id}/edit | where: Matter screen > Files > Edit | keywords: lawcraft, edit lock, save version -->
Edits a document with LawCraft. Opening takes an edit lock; each save adds a new version.

## New pleading {#editor-pleading}
<!-- template: editor/pleading.html | path: /matters/{matter_id}/pleadings/new | where: Matter screen > Files > New pleading | keywords: court profile, caption -->
Starts a pleading from the matter's court profile and opens it in the editor.
