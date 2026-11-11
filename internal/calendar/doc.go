// Package calendar implements PRD 8.5 and 8.16: calendar events, deadlines,
// reminders, read-only iCal feeds, and the available-dates tool.
//
// Every read and write goes through domain.Policy with the event's or deadline's
// matter, so a matter wall hides the row from lists, widgets, the API, feeds and
// reminders exactly as practice hides the matter itself. Pages and JSON handlers
// call the same service methods.
//
// The running core uses the Postgres Store (postgres.go): events, attendees and deadlines
// in the section 5 tables, and feeds, reminder claims, in-app notices and idempotency in
// migration 0500. Memory is the in-process Store the package tests use.
package calendar
