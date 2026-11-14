package core

import (
	"embed"

	"github.com/CharitonMedia/togavsu/internal/db"
)

// coreMigrationFS holds the release-range migrations (1240-1259) that span packages.
// They live with the core, which applies every package's migrations as one ordered
// set; a package test that applies only its own lower migrations never sees them.
//
//go:embed migrations/*.sql
var coreMigrationFS embed.FS

func coreMigrations() ([]db.Migration, error) { return db.LoadFS(coreMigrationFS) }
