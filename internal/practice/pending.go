package practice

// Pending is the honest list of criteria this package does not claim.
// Identity has not passed, so session mounting, the core router, and Postgres are pending.
var Pending = []string{
	"identity session principal, CSRF on the core router, and mounting these routes in cmd/core",
	"AC-003-P1 core /api/v1 router (identity)",
	"AC-003-P5 and AC-003-F3 module include rows (modules)",
	"AC-009 integrated browser close on the signed-in app (identity, ui)",
	"AC-020 file-text search (files)",
	"AC-024-F4 application-role SQL UPDATE and DELETE of audit_log and events; no trigger is added; the database immutability debt stays open",
	"AC-025 live browser save on the integrated matter screen (identity, ui)",
	"Postgres Store over the contacts, matters, matter_parties, notes, and audit_log tables already created by migration 0001 (integration lead)",
	"bulk writes POST /api/v1/contacts:bulk, /api/v1/matters:bulk, /api/v1/notes:bulk, /api/v1/conflict-records:bulk, /api/v1/matter-parties:bulk, /api/v1/contact-phones:bulk, /api/v1/contact-emails:bulk, /api/v1/contact-addresses:bulk, /api/v1/contact-tags:bulk, /api/v1/matter-markers:bulk, and /api/v1/custom-field-values:bulk stay one-transaction batches for the integration lead",
	"merge POST /api/v1/contacts/{id}/merge and POST /api/v1/matters/{id}/merge",
	"archive matter POST /api/v1/matters/{id}/archive: domain.Matter has no archived flag, so the column waits for the contracts package",
	"MCP transport for these practice operations waits for identity to mount /mcp",
	"CSRF cookie Secure is set on HTTPS and on X-Forwarded-Proto https; plain HTTP local checks omit Secure so the browser keeps the cookie, and the core session cookie remains identity's",
}
