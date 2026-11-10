package files

import (
	"context"
	"database/sql"
	"embed"

	"github.com/CharitonMedia/togavsu/internal/db"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns the files migrations (range 600-699). They expand the section 5
// file tables in schema core and run after the core migrations in internal/db.
func Migrations() ([]db.Migration, error) {
	return db.LoadFS(migrationFS)
}

// Migrate applies the files migrations to plain, a connection without a search_path,
// under the core owner so schema_migrations records them with the other core rows.
func Migrate(ctx context.Context, plain *sql.DB) error {
	ms, err := Migrations()
	if err != nil {
		return err
	}
	if err := db.ValidateMigrationVersions(ms); err != nil {
		return err
	}
	return db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms})
}
