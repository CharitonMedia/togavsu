// Package billing implements PRD sections 5 and 8.3 with 8.15: time entries, timers, expenses,
// invoices (draft, approve, send by system mail or download, payments, write-off, late
// interest, void back to unbilled), payments, the trust transfers billing asks the trust
// service to post, the Billing / Generate worklist, the collections panel, the fee items
// catalog, invoice PDFs and the billing reports with CSV export.
//
// Money is integer cents throughout; every product of a rate or percent rounds once, half away
// from zero, with overflow-checked integer arithmetic (money.go). Access is checked in Service
// on every read and write through access.Gate, so the HTTP API, the screens, MCP and modules
// get the same answers; a matter wall answers not_found. Edit history is staff-only, and a
// client sees only the InvoiceClient fields of its own sent invoices.
//
// Integration: internal/core applies Migrations() (version 300, billing_records) with the
// core files, builds New over the core pool with practice.Durable as Practice and Mirror,
// TrustLink over the trust service, and the core SMTP mailer; binds TrustEvents as the
// trust service's publisher; calls Resync at start; and mounts NewHandler's routes for the
// /api/v1 billing paths and the /billing screens. Trust and billing share the pool: a trust
// write billing makes joins billing's transaction, and billing calls Trust.Committed after
// its commit (balance sink, trust events).
package billing

// Pending lists what this package does not yet deliver. Reports and the PR body repeat it.
var Pending = []string{
	"system mail: sent through the core SMTP mailer after the commit, with no outbox; a failed send is undone and recorded",
	"invoice PDFs: FileLink stores each sent invoice's PDF as an invoice_pdf file record on the matter and is tested against the merged files service on Postgres; the core does not wire the files service yet (integrate-documents), so Deps.InvoiceFiles is nil and PDFs are rendered on demand only. The logo is drawn only when it is a JPEG",
	"files wiring: Deps.Receipts and Deps.InvoiceFiles (NewFileLink over the files service) wait for the files service in core (integrate-documents)",
	"billing events are stored in billing_records; merging them into the practice timeline is not done",
	"late interest is applied from the invoice screen (attorney or admin, only when enabled); there is no API route or nightly job",
	"expense receipts: FileLink checks receipt_file_id against the files service; until core wires Deps.Receipts, receipt_file_id is refused",
	"expense receipts over the API alone: the files contract lets no caller set financial_classification, so a file uploaded through /api/v1/files cannot become a receipt; proposed shared change for the files owner: classify a general file as receipt when an expense names it, or accept a receipt classification on upload",
	"custom fields on time entries and expenses are set through PUT .../custom-fields and custom-field-values:bulk; custom_fields in the create and patch bodies is still refused, and the screens show no custom-field card",
}
