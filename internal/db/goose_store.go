package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	goosedb "github.com/pressly/goose/v3/database"
)

// ledgerStore is the goose version store. It records owner, version, checksum, and applied time.
type ledgerStore struct {
	owner     string
	dialect   string
	checksums map[int64]string
}

func (s *ledgerStore) Tablename() string { return "schema_migrations" }

func (s *ledgerStore) ensureBaseline(ctx context.Context, db goosedb.DBTxConn) error {
	return s.Insert(ctx, db, goosedb.InsertRequest{Version: 0})
}

func (s *ledgerStore) CreateVersionTable(ctx context.Context, db goosedb.DBTxConn) error {
	_, err := db.ExecContext(ctx, schemaMigrationsSQL)
	if err != nil {
		return fmt.Errorf("create version table: %w", err)
	}
	return nil
}

func (s *ledgerStore) Insert(ctx context.Context, db goosedb.DBTxConn, req goosedb.InsertRequest) error {
	sum := "baseline"
	if req.Version != 0 {
		var ok bool
		sum, ok = s.checksums[req.Version]
		if !ok {
			return fmt.Errorf("%w: %d", ErrVersion, req.Version)
		}
	}
	_, err := db.ExecContext(ctx, Rebind(`INSERT INTO schema_migrations (owner, version, checksum, applied_at) VALUES (?, ?, ?, ?)`, s.dialect), s.owner, req.Version, sum, time.Now().UTC().UnixMicro())
	return err
}

func (s *ledgerStore) Delete(ctx context.Context, db goosedb.DBTxConn, version int64) error {
	_, err := db.ExecContext(ctx, Rebind(`DELETE FROM schema_migrations WHERE owner = ? AND version = ?`, s.dialect), s.owner, version)
	return err
}

func (s *ledgerStore) GetMigration(ctx context.Context, db goosedb.DBTxConn, version int64) (*goosedb.GetMigrationResult, error) {
	var micros int64
	var sum string
	err := db.QueryRowContext(ctx, Rebind(`SELECT applied_at, checksum FROM schema_migrations WHERE owner = ? AND version = ?`, s.dialect), s.owner, version).Scan(&micros, &sum)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %d", goosedb.ErrVersionNotFound, version)
	}
	if err != nil {
		return nil, err
	}
	if err := s.check(version, sum); err != nil {
		return nil, err
	}
	return &goosedb.GetMigrationResult{Timestamp: time.UnixMicro(micros).UTC(), IsApplied: true}, nil
}

func (s *ledgerStore) GetLatestVersion(ctx context.Context, db goosedb.DBTxConn) (int64, error) {
	var version sql.NullInt64
	err := db.QueryRowContext(ctx, Rebind(`SELECT MAX(version) FROM schema_migrations WHERE owner = ?`, s.dialect), s.owner).Scan(&version)
	if err != nil {
		return -1, err
	}
	if !version.Valid {
		return -1, goosedb.ErrVersionNotFound
	}
	return version.Int64, nil
}

func (s *ledgerStore) ListMigrations(ctx context.Context, db goosedb.DBTxConn) ([]*goosedb.ListMigrationsResult, error) {
	rows, err := db.QueryContext(ctx, Rebind(`SELECT version, checksum FROM schema_migrations WHERE owner = ? ORDER BY version DESC`, s.dialect), s.owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*goosedb.ListMigrationsResult
	for rows.Next() {
		var version int64
		var sum string
		if err := rows.Scan(&version, &sum); err != nil {
			return nil, err
		}
		if err := s.check(version, sum); err != nil {
			return nil, err
		}
		out = append(out, &goosedb.ListMigrationsResult{Version: version, IsApplied: true})
	}
	return out, rows.Err()
}

func (s *ledgerStore) TableExists(ctx context.Context, db goosedb.DBTxConn) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n)
	if err == nil {
		var baseline int
		if err := db.QueryRowContext(ctx, Rebind(`SELECT COUNT(*) FROM schema_migrations WHERE owner = ? AND version = 0`, s.dialect), s.owner).Scan(&baseline); err != nil {
			return false, err
		}
		if baseline == 0 {
			if err := s.Insert(ctx, db, goosedb.InsertRequest{Version: 0}); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "does not exist") || strings.Contains(msg, "no such table") {
		return false, nil
	}
	return false, err
}

func (s *ledgerStore) check(version int64, stored string) error {
	if version == 0 {
		return nil
	}
	want, ok := s.checksums[version]
	if ok && want != stored {
		return fmt.Errorf("%w: %s version %d", ErrChecksum, s.owner, version)
	}
	return nil
}
