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
