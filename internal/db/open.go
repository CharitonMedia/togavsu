package db

import (
	"database/sql"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// OpenPostgres opens a pgx-backed pool. searchPath is set on every new connection when non-empty.
func OpenPostgres(dsn, searchPath string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if searchPath != "" {
		if cfg.RuntimeParams == nil {
			cfg.RuntimeParams = map[string]string{}
		}
		cfg.RuntimeParams["search_path"] = searchPath
	}
	return stdlib.OpenDB(*cfg), nil
}
