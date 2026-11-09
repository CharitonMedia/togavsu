package audit

import (
	"context"
	"database/sql"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

const writerRole = "togavsu_audit_writer"

// Postgres stores audit rows and runs each statement as the non-owner writer role.
// InstallPrivileges must have run on the pool.
type Postgres struct {
	db      *sql.DB
	dialect string
}

// NewPostgres returns a store. InstallPrivileges must have run on db.
func NewPostgres(sqlDB *sql.DB) *Postgres {
	return &Postgres{db: sqlDB, dialect: "postgres"}
}

// InstallPrivileges creates the non-owner writer role with only SELECT and INSERT.
// The table owner and a superuser can still UPDATE and DELETE. PRD section 6 forbids
// a trigger for that remaining ceiling. CREATE ROLE cannot run inside a goose
// transaction, so this is not a migration file. It is safe to run again.
func InstallPrivileges(ctx context.Context, sqlDB *sql.DB) error {
	stmts := []string{
		`DO $body$ BEGIN
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + writerRole + `') THEN
		    CREATE ROLE ` + writerRole + ` NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE;
		  END IF;
		END $body$`,
		`GRANT ` + writerRole + ` TO CURRENT_USER`,
		`GRANT USAGE ON SCHEMA core TO ` + writerRole,
		`GRANT SELECT, INSERT ON TABLE core.audit_log TO ` + writerRole,
		`REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE core.audit_log FROM ` + writerRole,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// Atomic runs fn in one transaction on this store's pool. Every Append made inside
// fn, and every store write that joins WithTx on the same pool, commits or rolls
// back together.
func (p *Postgres) Atomic(ctx context.Context, fn func(ctx context.Context) error) error {
	return WithTx(ctx, p.db, fn)
}

// Append inserts one row as the writer role.
func (p *Postgres) Append(ctx context.Context, row domain.AuditEntry) error {
	return p.asWriter(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (
			id, created_at, updated_at, created_by, actor_id, role, source, entity, entity_id, field, old_value, new_value, reason
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			string(row.ID), domain.UnixMicroUTC(row.CreatedAt), domain.UnixMicroUTC(row.UpdatedAt), string(row.CreatedBy),
			string(row.ActorID), row.Role, row.Source, row.Entity, string(row.EntityID), row.Field, row.OldValue, row.NewValue, row.Reason)
		return err
	})
}

// Get reads one row as the writer role.
func (p *Postgres) Get(ctx context.Context, id domain.ID) (domain.AuditEntry, error) {
	var row domain.AuditEntry
	err := p.asWriter(ctx, func(tx *sql.Tx) error {
		var created, updated int64
		var idText, createdBy, actorID, entityID string
		err := tx.QueryRowContext(ctx, `SELECT id, created_at, updated_at, created_by, actor_id, role, source, entity, entity_id, field, old_value, new_value, reason
			FROM audit_log WHERE id = $1`, string(id)).Scan(
			&idText, &created, &updated, &createdBy,
			&actorID, &row.Role, &row.Source, &row.Entity, &entityID,
			&row.Field, &row.OldValue, &row.NewValue, &row.Reason)
		if errors.Is(err, sql.ErrNoRows) {
			return repository.ErrNotFound
		}
		if err != nil {
			return err
		}
		row.ID = domain.ID(idText)
		row.CreatedBy = domain.ID(createdBy)
		row.ActorID = domain.ID(actorID)
		row.EntityID = domain.ID(entityID)
		row.CreatedAt = domain.FromUnixMicro(created)
		row.UpdatedAt = domain.FromUnixMicro(updated)
		return nil
	})
	return row, err
}

// List reads rows as the writer role.
func (p *Postgres) List(ctx context.Context, _ repository.Filter) (repository.Page[domain.AuditEntry], error) {
	var items []domain.AuditEntry
	err := p.asWriter(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, created_at, updated_at, created_by, actor_id, role, source, entity, entity_id, field, old_value, new_value, reason
			FROM audit_log ORDER BY created_at, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row domain.AuditEntry
			var created, updated int64
			var idText, createdBy, actorID, entityID string
			if err := rows.Scan(
				&idText, &created, &updated, &createdBy,
				&actorID, &row.Role, &row.Source, &row.Entity, &entityID,
				&row.Field, &row.OldValue, &row.NewValue, &row.Reason); err != nil {
				return err
			}
			row.ID = domain.ID(idText)
			row.CreatedBy = domain.ID(createdBy)
			row.ActorID = domain.ID(actorID)
			row.EntityID = domain.ID(entityID)
			row.CreatedAt = domain.FromUnixMicro(created)
			row.UpdatedAt = domain.FromUnixMicro(updated)
			items = append(items, row)
		}
		return rows.Err()
	})
	return repository.Page[domain.AuditEntry]{Items: items}, err
}

// Rows reads only the requested audit rows.
// ponytail: ceiling: audit_log_entity_idx is (entity, entity_id) only, so a filter on field and created_at can still scan those rows. Upgrade path: an identity migration adds field and created_at to that index. trigger: a sign-in query plan is a sequential scan once the log passes 100000 rows.
func (p *Postgres) Rows(ctx context.Context, query RowQuery) ([]domain.AuditEntry, error) {
	var since int64
	if !query.Since.IsZero() {
		since = domain.UnixMicroUTC(query.Since)
	}
	var items []domain.AuditEntry
	err := p.asWriter(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, created_at, updated_at, created_by, actor_id, role, source, entity, entity_id, field, old_value, new_value, reason
			FROM audit_log
			WHERE entity = $1 AND entity_id = $2
			AND ($3 = '' OR field = $3)
			AND (CAST($4 AS bigint) = 0 OR created_at >= CAST($4 AS bigint))
			ORDER BY created_at, id`,
			query.Entity, string(query.EntityID), query.Field, since)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			row, err := scanAudit(rows)
			if err != nil {
				return err
			}
			items = append(items, row)
		}
		return rows.Err()
	})
	return items, err
}

func scanAudit(sc interface{ Scan(...any) error }) (domain.AuditEntry, error) {
	var row domain.AuditEntry
	var created, updated int64
	var idText, createdBy, actorID, entityID string
	err := sc.Scan(
		&idText, &created, &updated, &createdBy,
		&actorID, &row.Role, &row.Source, &row.Entity, &entityID,
		&row.Field, &row.OldValue, &row.NewValue, &row.Reason)
	if err != nil {
		return domain.AuditEntry{}, err
	}
	row.ID = domain.ID(idText)
	row.CreatedBy = domain.ID(createdBy)
	row.ActorID = domain.ID(actorID)
	row.EntityID = domain.ID(entityID)
	row.CreatedAt = domain.FromUnixMicro(created)
	row.UpdatedAt = domain.FromUnixMicro(updated)
	return row, nil
}

// asWriter runs fn as the writer role. Inside WithTx on the same pool it uses that
// transaction and switches back to the session role afterwards, so a failed append
// aborts the whole transaction.
func (p *Postgres) asWriter(ctx context.Context, fn func(*sql.Tx) error) error {
	if tx := Tx(ctx, p.db); tx != nil {
		if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+writerRole); err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "SET LOCAL ROLE NONE")
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+writerRole); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// RawAsWriter runs one statement as the non-owner role. Tests use it for hostile SQL
// that does not go through Update or Delete.
func RawAsWriter(ctx context.Context, sqlDB *sql.DB, query string, args ...any) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+writerRole); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, db.Rebind(query, "postgres"), args...); err != nil {
		return err
	}
	return tx.Commit()
}
