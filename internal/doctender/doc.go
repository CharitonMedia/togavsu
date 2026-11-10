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
//
// Deliberate shortcuts, each with its ceiling, upgrade path and trigger:
//
// ponytail: cost: ai_call_log.cost_cents is written as 0 because the registry has no
// model price setting; spend reports tokens and calls. Upgrade: an ai.model_prices
// setting on the AI tab and cents computed per call. Trigger: the AI tab gains prices
// or a provider returns cost in its response.
//
// ponytail: Word-family documents go through WordcraftCLI, which needs the editor
// task's wordcraft-cli in the core image; until then each such file is an
// extraction_failed review item and stays available. Trigger: the core image ships
// wordcraft-cli at the pinned LawCraft commit (AC-043-P3).
//
// ponytail: the core image (platform-owned Dockerfile) does not yet ship tesseract or
// ffmpeg. Without Tesseract the scanned-page check falls back to the check model's
// read; without ffmpeg only uncompressed AVI video and WAV audio are read natively and
// other video is an unsupported_format item. Trigger: the image adds tesseract-ocr and
// ffmpeg (proposed in the PR).
//
// ponytail: a text-layer page's OCR check sends a one-page PDF to the OCR model,
// because the core has no rasterizer. Ceiling: the OCR model must accept PDF input.
// Upgrade: rasterize the page (pdftoppm or mutool in the image). Trigger: a provider
// refuses PDF input.
//
// ponytail: the PDF reader does not decrypt: any PDF naming a security handler is a
// password_protected item, including owner-restricted files with an empty user
// password. It also leaves CCITT, JBIG2 and JPX page images to the whole-page PDF
// path. Upgrade: RC4/AES Standard handler with the empty password, image codecs.
// Trigger: a firm reports an owner-restricted PDF flagged as password-protected.
//
// ponytail: matter matching, the review queue and the job list read every candidate
// row and filter access in Go through files.Get (one check per file). Ceiling: linear
// in matters and queue rows. Upgrade: SQL filters on case numbers and visible matters.
// Trigger: 10k matters, or a queue page slower than 200 ms.
//
// ponytail: publish writes the Markdown bytes to storage before its transaction (as
// files.AddRendition does); a rolled-back publish leaves an unreferenced object.
// Upgrade: an orphan-object sweep in files. Trigger: a storage audit finds one.
//
// ponytail: a person's photo grouping is locked by doc_tender_group_members (each
// photo in one group), not files.locked_fields "grouping", which files has no API to
// set. Upgrade: a files.LockGrouping call. Trigger: files exposes it.
//
// ponytail: the organization-name cut (20 characters, PRD 8.21) and the PDF word-space
// kern are fixed facts in wire.json, not registry settings. Trigger: an admin asks to
// change the cut (add doctender.client_name_chars).
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
