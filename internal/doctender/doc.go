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
// itself, in the API, the review queue and the Doc Tender pages. A matter-matching
// suggestion names another matter, so it is checked against that matter's wall for
// each reader (Deps.Walls) and removed, with what matched it, for anyone walled from
// it; mail and the daily summary, which go to lists, say nothing about a walled matter.
// A grouped photo PDF is as restricted as its most restricted photo, and photos are
// grouped only within one matter or, with no matter, one contact.
//
// Settled stays settled: fields a person locked (files.locked_fields) are never written
// (files.ApplyTender skips them), a person's photo grouping is never redone, and a
// unit that passed is never run again.
//
// Deliberate shortcuts, each with its ceiling, upgrade path and trigger:
//
// ponytail: Word-family documents go through WordcraftCLI, which needs the editor
// task's wordcraft-cli in the core image; until then each such file is an
// extraction_failed review item and stays available. The independent check reads the
// document's own counts (DOCX app.xml, ODT meta.xml, DOC SummaryInformation, RTF \info
// or an in-package read of the RTF text); a document with no count of its own fails
// its check and goes to review. Ceiling: a DOC saved without SummaryInformation counts
// is never published as checked. Upgrade: read the DOC's WordDocument text (piece
// table) in-package. Trigger: the core image ships wordcraft-cli at the pinned LawCraft
// commit (AC-043-P3), or a firm reports such DOCs in the review queue.
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
//
// ponytail: call cost reads ai.model_prices, a shared setting this lane proposes and
// does not own (internal/settings, the API contract). Until the integration lead
// registers it every call, step and job costs 0 cents. Upgrade: register the setting
// (KindJSON, default "{}", AI tab) with parseModelPrices as its check. Trigger:
// integrate-documents.
//
// ponytail: the decode caps (256 MiB of decoded stream or zip entry, 100 million pixels
// per image and per PDF page's contents, 4 million cells per spreadsheet sheet, 262,144
// ToUnicode entries per PDF page) are fixed facts in wire.json, not registry settings.
// A file over any is a damaged item, not retried. Upgrade: doctender.max_decoded_bytes,
// doctender.max_image_pixels, doctender.max_sheet_cells and doctender.max_cmap_entries
// on the Doc Tender tab. Trigger: a firm's real file trips one.
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
