package core

import (
	"embed"

	"github.com/CharitonMedia/togavsu/internal/db"
)

//go:embed migrations/*.sql
var coreMigrationFS embed.FS

// coreMigrations are the composed core's own migrations (release range): tables only
// the core's routes use, such as core_idempotency. They run with every package's
// migrations in version order; no package's tests apply them on their own, so a
// package that migrates only up to its own range never sees a version above it.
func coreMigrations() ([]db.Migration, error) {
	return db.LoadFS(coreMigrationFS)
}
