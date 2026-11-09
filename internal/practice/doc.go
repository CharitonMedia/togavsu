// Package practice implements contacts, matters, parties, notes, conflicts,
// custom fields, edit history, and the case timeline.
//
// Access checks go through domain.Policy. A matter wall hides the row from
// list, search, widgets, reports, and the API, including for an admin who is
// not named on the wall. Attorney-only values are redacted for staff.
//
// ponytail: ceiling: rows live in process memory and are scanned linearly; the core router, session principal, and Postgres are not wired. Upgrade path: a repository adapter and migration in the practice range 200–299, mounted by identity. trigger: identity passes a Postgres pool and a session principal into practice.New.
package practice
