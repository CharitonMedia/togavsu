package core

import (
	"embed"

	"github.com/CharitonMedia/togavsu/internal/db"
)

//go:embed migrations/*.sql
var coreMigrationFS embed.FS

// coreMigrations are the release migrations (range 1200-1299) that change tables of
// more than one package's tests' base schema, so they run only on the composed core,
// after every package's own migrations.
func coreMigrations() ([]db.Migration, error) { return db.LoadFS(coreMigrationFS) }
