package practice

// Pending is the honest list of criteria this package does not claim. The core
// (internal/core) mounts these routes with the identity session principal, serves /mcp,
// and stores practice rows in Postgres through Durable.
var Pending = []string{
	"AC-003-P5 and AC-003-F3 module include rows (modules)",
	"AC-020 file-text search (files)",
	"section 5 relational tables from migration 0001 (contacts, matters, matter_parties, notes) are not the practice source of truth: practice rows persist as documents in practice_records (migration 200), so rows seeded into the relational tables do not appear on practice screens",
	"bulk write POST /api/v1/custom-field-values:bulk (the other practice :bulk routes are mounted, one transaction per batch, in bulk.go)",
	"merge POST /api/v1/contacts/{id}/merge and POST /api/v1/matters/{id}/merge",
	"archive matter POST /api/v1/matters/{id}/archive is blocked on a contracts change: domain.Matter and migration 0001 have no archived column",
	"MCP and bearer tokens reach only contract operations: GET /api/v1/reports/matters and GET /api/v1/matters/{matter_id}/conflict-checks are served but are not in api/openapi/v1, so they need a contract change",
	"practice area, county and referral admin screens; a fresh install has no practice area until one is created through POST /api/v1/practice-areas",
}
