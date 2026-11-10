// Package calendar implements PRD 8.5 and 8.16: calendar events, deadlines,
// reminders, read-only iCal feeds, and the available-dates tool.
//
// Every read and write goes through domain.Policy with the event's or deadline's
// matter, so a matter wall hides the row from lists, widgets, the API, feeds and
// reminders exactly as practice hides the matter itself. Pages and JSON handlers
// call the same service methods.
//
// ponytail: ceiling: rows live in the process-memory Store (Memory); a restart
// loses calendar data, and lists scan every row. The section 5 tables
// calendar_events, calendar_event_attendees and deadlines exist in migration 0001,
// but feeds and sent-reminder markers need a calendar migration (range 500–599)
// that the integration lead allocates. Upgrade path: a Postgres Store over those
// tables plus migration 0500 (ical_feeds, calendar_reminders_sent). Trigger: core
// mounts this package on the shared pool (integrate-calendar).
package calendar
