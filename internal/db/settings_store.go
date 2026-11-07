package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
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

// Transact commits the setting change and its audit row together. The advisory lock serializes the read.
func (s SettingsStore) Transact(ctx context.Context, fn func(settings.Store) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stx := &settingsTx{tx: tx, dialect: s.Dialect, locked: map[string]bool{}}
	if err := fn(stx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

type settingsTx struct {
	tx      *sql.Tx
	dialect string
	locked  map[string]bool
}

func (t *settingsTx) lock(ctx context.Context, key string) error {
	if t.locked[key] {
		return nil
	}
	if t.dialect == "postgres" {
		if _, err := t.tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, key); err != nil {
			return err
		}
	}
	t.locked[key] = true
	return nil
}

func (t *settingsTx) Get(ctx context.Context, key string) (string, bool, error) {
	if err := t.lock(ctx, key); err != nil {
		return "", false, err
	}
	q := `SELECT value FROM settings WHERE key = ?`
	if t.dialect == "postgres" {
		q += ` FOR UPDATE`
	}
	var value string
	err := t.tx.QueryRowContext(ctx, Rebind(q, t.dialect), key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (t *settingsTx) Put(ctx context.Context, key, value string, at time.Time, actor domain.ID) error {
	if err := t.lock(ctx, key); err != nil {
		return err
	}
	micros := domain.UnixMicroUTC(at)
	res, err := t.tx.ExecContext(ctx, Rebind(`UPDATE settings SET value = ?, updated_at = ? WHERE key = ?`, t.dialect), value, micros, key)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(ctx, Rebind(`INSERT INTO settings (id, created_at, updated_at, created_by, key, value) VALUES (?, ?, ?, ?, ?, ?)`, t.dialect), string(id), micros, micros, string(actor), key, value)
	return err
}

func (t *settingsTx) Delete(ctx context.Context, key string) error {
	if err := t.lock(ctx, key); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(ctx, Rebind(`DELETE FROM settings WHERE key = ?`, t.dialect), key)
	return err
}

func (t *settingsTx) Transact(ctx context.Context, fn func(settings.Store) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(t)
}

func (t *settingsTx) LogChange(ctx context.Context, change settings.Change) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	micros := domain.UnixMicroUTC(change.At)
	_, err = t.tx.ExecContext(ctx, Rebind(`INSERT INTO audit_log (
		id, created_at, updated_at, created_by, actor_id, role, source, entity, entity_id, field, old_value, new_value, reason
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.dialect),
		string(id), micros, micros, string(change.Actor), string(change.Actor), "", "system", "setting", change.Key, "value", change.Old, change.New, "settings update")
	return err
}
