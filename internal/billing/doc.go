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
//     Trust: <trust service>, Mail: <system mail>}), calls Resync once, and mounts
//     NewHandler(svc, sub(web.Billing, "templates/billing"), app.principal).Routes() for
//     /api/v1 billing paths and /billing screens ahead of the practice catch-all.
//   - web/embed.go embeds templates/billing.
package billing

// Pending lists what this package does not yet deliver. Reports and the PR body repeat it.
var Pending = []string{
	"trust: the Trust interface is implemented by the trust package; until it is wired, trust deposits, Apply trust, Apply flat fee and trust-linked reversals return conflict",
	"system mail: no SMTP Mailer exists in core yet; deliver=email returns conflict until one is wired. Mail is sent after the commit, with no outbox",
	"core wiring: migration 300, the handler mount, web embed and the /billing sign-in redirect are lead-owned",
	"custom fields on time entries and expenses (PUT .../custom-fields) are not implemented",
	"invoices:bulk and payment processor initializations are not implemented",
	"invoice PDFs are rendered on demand, not stored as file records; the logo is drawn only when it is a JPEG",
	"billing events are stored in billing_records; merging them into the practice timeline is lead-owned",
	"late interest is applied from the invoice screen (attorney or admin, only when enabled); there is no API route or nightly job",
	"expense screens offer no receipt upload until the files lane wires Deps.Receipts; custom-field cards on time entries and expenses wait on custom fields",
	"expense receipts: no files service exists yet, so receipt_file_id is refused until the files lane wires Deps.Receipts",
}
