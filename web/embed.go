// Package web embeds the server-rendered templates and static files.
package web

import "embed"

// Practice holds templates/practice: the practice screens, CSS and JS.
//
//go:embed templates/practice
var Practice embed.FS

// Billing holds templates/billing: the billing screens and CSS.
//
//go:embed templates/billing
var Billing embed.FS

// Trust holds templates/trust: the trust screens and CSS.
//
//go:embed templates/trust
var Trust embed.FS

// Calendar holds templates/calendar: the calendar, deadlines, feeds and available dates.
//
//go:embed templates/calendar
var Calendar embed.FS

// Files holds templates/files: the matter Files tab and the Files and storage tab.
//
//go:embed templates/files
var Files embed.FS

// DocTender holds templates/doctender: the review queue, the Doc Tender tab and its guide.
//
//go:embed templates/doctender
var DocTender embed.FS
