package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// SettingsStore is the settings table behind settings.Registry.
type SettingsStore struct {
	DB      *sql.DB
	Dialect string
}

// Get returns one override.
func (s SettingsStore) Get(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.DB.QueryRowContext(ctx, Rebind(`SELECT value FROM settings WHERE key = ?`, s.Dialect), key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// Put inserts or updates one setting.
func (s SettingsStore) Put(ctx context.Context, key, value string, at time.Time, actor domain.ID) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	micros := domain.UnixMicroUTC(at)
	res, err := tx.ExecContext(ctx, Rebind(`UPDATE settings SET value = ?, updated_at = ? WHERE key = ?`, s.Dialect), value, micros, key)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, Rebind(`INSERT INTO settings (id, created_at, updated_at, created_by, key, value) VALUES (?, ?, ?, ?, ?, ?)`, s.Dialect), string(id), micros, micros, string(actor), key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Delete removes an override so the registry default applies again.
func (s SettingsStore) Delete(ctx context.Context, key string) error {
	_, err := s.DB.ExecContext(ctx, Rebind(`DELETE FROM settings WHERE key = ?`, s.Dialect), key)
	return err
}
