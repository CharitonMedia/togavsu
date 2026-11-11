package modules

import (
	"embed"

	"github.com/CharitonMedia/togavsu/internal/db"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns the module runtime's core migrations (range 1000-1099). They add
// the runtime's tables to schema core and run after the core migrations in internal/db.
func Migrations() ([]db.Migration, error) {
	return db.LoadFS(migrationFS)
}
