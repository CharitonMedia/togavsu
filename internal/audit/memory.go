package audit

import (
	"context"
	"sync"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// Memory is an append-only in-process log.
type Memory struct {
	mu   sync.Mutex
	rows []domain.AuditEntry
}

// NewMemory returns an empty log.
func NewMemory() *Memory {
	return &Memory{}
}

// Append adds a row. A repeated id is a conflict.
func (m *Memory) Append(_ context.Context, row domain.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, have := range m.rows {
		if have.ID == row.ID {
			return repository.ErrConflict
		}
	}
	m.rows = append(m.rows, row)
	return nil
}

// Get returns one row.
func (m *Memory) Get(_ context.Context, id domain.ID) (domain.AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return domain.AuditEntry{}, repository.ErrNotFound
}

// List returns a copy of the rows, oldest first.
func (m *Memory) List(_ context.Context, _ repository.Filter) (repository.Page[domain.AuditEntry], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.AuditEntry, len(m.rows))
	copy(out, m.rows)
	return repository.Page[domain.AuditEntry]{Items: out}, nil
}

// Rows returns rows that match the query, oldest first.
func (m *Memory) Rows(_ context.Context, query RowQuery) ([]domain.AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.AuditEntry
	for _, row := range m.rows {
		if query.Entity != "" && row.Entity != query.Entity {
			continue
		}
		if !query.EntityID.IsZero() && row.EntityID != query.EntityID {
			continue
		}
		if query.Field != "" && row.Field != query.Field {
			continue
		}
		if !query.Since.IsZero() && row.CreatedAt.Before(query.Since) {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

// Len is the row count.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}
