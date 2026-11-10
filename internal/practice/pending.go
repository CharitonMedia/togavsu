package practice

// Pending is the honest list of criteria this package does not claim. The core
// (internal/core) mounts these routes with the identity session principal, serves /mcp,
// and stores practice rows in Postgres through Durable.
var Pending = []string{
	"AC-003-P5 and AC-003-F3 module include rows (modules)",
	"AC-020 file-text search (files)",
	"AC-024-F4 application-role SQL UPDATE and DELETE of the events table; audit_log refuses them for the non-superuser core role, but the owning role can grant them back (identity ceiling 4)",
	"section 5 relational tables from migration 0001 (contacts, matters, matter_parties, notes) are not the practice source of truth: practice rows persist as documents in practice_records (migration 200), so rows seeded into the relational tables do not appear on practice screens",
	"bulk writes POST /api/v1/contacts:bulk, /api/v1/matters:bulk, /api/v1/notes:bulk, /api/v1/conflict-records:bulk, /api/v1/matter-parties:bulk, /api/v1/contact-phones:bulk, /api/v1/contact-emails:bulk, /api/v1/contact-addresses:bulk, /api/v1/contact-tags:bulk, /api/v1/matter-markers:bulk, and /api/v1/custom-field-values:bulk stay one-transaction batches for the integration lead",
	"merge POST /api/v1/contacts/{id}/merge and POST /api/v1/matters/{id}/merge",
	"archive matter POST /api/v1/matters/{id}/archive is blocked on a contracts change: domain.Matter and migration 0001 have no archived column",
	"MCP and bearer tokens reach only contract operations: GET /api/v1/reports/matters and GET /api/v1/matters/{matter_id}/conflict-checks are served but are not in api/openapi/v1, so they need a contract change",
	"practice area, county and referral admin screens; a fresh install has no practice area until one is created through POST /api/v1/practice-areas",
}
