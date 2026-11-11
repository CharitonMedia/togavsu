package editor

import (
	"context"
	"database/sql"
	"embed"

	"github.com/CharitonMedia/togavsu/internal/db"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns the editor migrations (range 700-799). They run after the core and
// files migrations, in schema core.
func Migrations() ([]db.Migration, error) {
	return db.LoadFS(migrationFS)
}

// Migrate applies the editor migrations to plain, a connection without a search_path,
// under the core owner.
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
