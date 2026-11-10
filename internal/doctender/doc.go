// Package doctender is Togavsu's file processor (PRD 8.21). Every new file or version
// queues its own job when files reports file.version_created; a worker leases jobs from
// Postgres, checks the matter (a closed matter spends nothing and is listed as Not
// processed), extracts each page, photo, sheet or media window as its own unit with an
// independent check, labels and categorizes on the AI tab's models, matches the
// matter, names the file by the firm's pattern and files it into its category's
// folder, and publishes the Markdown copy, the record, search and a timeline event in
// one transaction. Whatever fails its check goes to the review queue with Doc Tender's
// best answer; the file stays listed, viewable and downloadable throughout.
//
// Access: every job, exception and result is read through the files service as the
// caller, so walls, roles and attorney-only files apply exactly as they do to the file
// itself, in the API, the review queue and the Doc Tender pages.
//
// Settled stays settled: fields a person locked (files.locked_fields) are never written
// (files.ApplyTender skips them), a person's photo grouping is never redone, and a
// unit that passed is never run again.
package doctender

import "time"

// Fixed counting facts used by the format readers (byte widths, hex pairs, octal
// escapes, RGB samples). They are properties of the formats, not firm settings.
const (
	zero  = int(0)
	one   = zero + 1
	two   = zero + 2
	three = zero + 3
	four  = zero + 4
	eight = zero + 8
)

// Time units for converting timestamps and leases. They are units, not tunables;
// every tunable delay is read from the settings registry.
const (
	secondUnit = time.Duration(1e9)
	minuteUnit = 60 * secondUnit
	hourUnit   = 60 * minuteUnit
	microUnit  = time.Duration(1e3)
)
