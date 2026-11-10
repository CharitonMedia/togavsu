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
// Integration (owned by the lead, not this package):
//   - core.Migrate applies Migrations() (version 300, billing_records) with the core files.
//   - core.Open builds New(Deps{Store: NewPostgres(pool), Practice: <practice.Durable>,
//     Walls: app.Practice.Policy.Walls, Audit: aud, Settings: reg, Mirror: <practice.Durable>,
//     Trust: NewTrustLink(<trust service>, pool), Mail: <system mail>, Receipts: link,
//     InvoiceFiles: link}) with link from NewFileLink(<files service>), calls Resync once, and mounts
//     NewHandler(svc, sub(web.Billing, "templates/billing"), app.principal).Routes() for
//     /api/v1 billing paths and /billing screens ahead of the practice catch-all.
//   - web/embed.go embeds templates/billing.
//   - Trust and billing share pool: a trust write billing makes joins billing's transaction,
//     and billing calls Trust.Committed after its commit (balance sink, trust events).
package billing

// Pending lists what this package does not yet deliver. Reports and the PR body repeat it.
var Pending = []string{
	"trust: TrustLink adapts the merged trust service and is tested against it on Postgres; until core wires Deps.Trust, trust deposits, Apply trust, Apply flat fee and trust-linked reversals return conflict",
	"flat fee from the trust page: trust's own Apply flat fee shares the application record with billing and is credited to an open fee invoice through TrustEvents; until core sets trust.Deps.Events to a bound billing.TrustEvents, the credit waits for the next billing write on the invoice or Resync, and a trust-page apply with no fee invoice creates none",
	"system mail: no SMTP Mailer exists in core yet; deliver=email returns conflict until one is wired. Mail is sent after the commit, with no outbox",
	"core wiring: migration 300, the handler mount, web embed and the /billing sign-in redirect are lead-owned",
	"custom fields on time entries and expenses (PUT .../custom-fields) are not implemented",
	"invoices:bulk and payment processor initializations are not implemented",
	"invoice PDFs: FileLink stores each sent invoice's PDF (on send and on download, one immutable version per distinct rendering) as an invoice_pdf file record on the matter, tested against the merged files service on Postgres; until core wires Deps.InvoiceFiles, PDFs are rendered on demand only. The logo is drawn only when it is a JPEG",
	"files wiring: Deps.Receipts and Deps.InvoiceFiles (NewFileLink over the merged files service) are lead-owned core wiring",
	"billing events are stored in billing_records; merging them into the practice timeline is lead-owned",
	"late interest is applied from the invoice screen (attorney or admin, only when enabled); there is no API route or nightly job",
	"expense receipts: FileLink checks receipt_file_id against the merged files service (readable by the caller, untrashed, on the expense's matter, classed receipt), and the expense screen uploads a receipt as a receipt file record; until core wires Deps.Receipts, receipt_file_id is refused",
	"expense receipts over the API alone: the files contract lets no caller set financial_classification, so a file uploaded through /api/v1/files cannot become a receipt; proposed shared change for the files owner: classify a general file as receipt when an expense names it, or accept a receipt classification on upload",
	"custom-field cards on time entries and expenses wait on custom fields",
}
