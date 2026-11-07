package settings

import (
	"context"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

type memoryRow struct {
	value string
	at    time.Time
	actor domain.ID
}

// Memory is a process-local settings store for tests and for wiring before SQL is opened.
type Memory struct {
	mu sync.Mutex
	m  map[string]memoryRow
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{m: map[string]memoryRow{}}
}

// Get returns a stored override.
func (m *Memory) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.m[key]
	if !ok {
		return "", false, nil
	}
	return row.value, true, nil
}

// Put stores an override.
func (m *Memory) Put(_ context.Context, key, value string, at time.Time, actor domain.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[key] = memoryRow{value: value, at: at, actor: actor}
	return nil
}

// Delete removes an override.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, key)
	return nil
}

// Transact holds the store lock until fn returns. Mutations publish only when fn and ctx succeed.
func (m *Memory) Transact(ctx context.Context, fn func(Store) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	tx := &memTx{m: cloneRows(m.m)}
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.m = tx.m
	return nil
}

type memTx struct {
	m map[string]memoryRow
}

func (t *memTx) Get(_ context.Context, key string) (string, bool, error) {
	row, ok := t.m[key]
	if !ok {
		return "", false, nil
	}
	return row.value, true, nil
}

func (t *memTx) Put(_ context.Context, key, value string, at time.Time, actor domain.ID) error {
	t.m[key] = memoryRow{value: value, at: at, actor: actor}
	return nil
}

func (t *memTx) Delete(_ context.Context, key string) error {
	delete(t.m, key)
	return nil
}

func (t *memTx) Transact(ctx context.Context, fn func(Store) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(t)
}

func cloneRows(in map[string]memoryRow) map[string]memoryRow {
	out := make(map[string]memoryRow, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
