package calendar

import (
	"context"
	"sort"
	"sync"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// Store is the calendar persistence seam. Memory satisfies it; a Postgres store
// over calendar_events, calendar_event_attendees and deadlines is the upgrade path.
type Store interface {
	PutEvent(ctx context.Context, row Event) error
	DeleteEvent(ctx context.Context, id domain.ID) error
	Event(ctx context.Context, id domain.ID) (Event, error)
	Events(ctx context.Context) ([]Event, error)

	PutDeadline(ctx context.Context, row Deadline) error
	// DeleteDeadline removes a deadline. It only undoes a create whose audit failed.
	DeleteDeadline(ctx context.Context, id domain.ID) error
	Deadline(ctx context.Context, id domain.ID) (Deadline, error)
	Deadlines(ctx context.Context) ([]Deadline, error)

	PutFeed(ctx context.Context, row Feed) error
	// DeleteFeed removes a feed. It only undoes a create whose audit failed.
	DeleteFeed(ctx context.Context, id domain.ID) error
	Feed(ctx context.Context, id domain.ID) (Feed, error)
	FeedBySecret(ctx context.Context, hash string) (Feed, error)
	Feeds(ctx context.Context) ([]Feed, error)

	// ClaimReminder records that key was sent. It reports false when it was already sent.
	ClaimReminder(ctx context.Context, key string) (bool, error)
	// ReleaseReminder drops a claim whose delivery failed, so the next run retries it.
	ReleaseReminder(ctx context.Context, key string) error
	PutNotice(ctx context.Context, n Notice) error
	Notices(ctx context.Context, user domain.ID) ([]Notice, error)
	AppendEvent(ctx context.Context, row domain.Event) error

	IdemGet(key string) (IdemRecord, bool)
	IdemPut(key string, rec IdemRecord)
}

// IdemRecord is a stored response for an Idempotency-Key.
type IdemRecord struct {
	Hash   string
	Status int
	Body   []byte
}

// Memory is the in-process Store.
type Memory struct {
	mu        sync.RWMutex
	events    map[domain.ID]Event
	deadlines map[domain.ID]Deadline
	feeds     map[domain.ID]Feed
	sent      map[string]bool
	notices   []Notice
	stream    []domain.Event
	idem      map[string]IdemRecord
}

var _ Store = (*Memory)(nil)

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{
		events: map[domain.ID]Event{}, deadlines: map[domain.ID]Deadline{}, feeds: map[domain.ID]Feed{},
		sent: map[string]bool{}, idem: map[string]IdemRecord{},
	}
}

func (m *Memory) PutEvent(_ context.Context, row Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row.Attendees = append([]domain.ID(nil), row.Attendees...)
	m.events[row.ID] = row
	return nil
}

func (m *Memory) DeleteEvent(_ context.Context, id domain.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.events[id]; !ok {
		return repository.ErrNotFound
	}
	delete(m.events, id)
	return nil
}

func (m *Memory) Event(_ context.Context, id domain.ID) (Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.events[id]
	if !ok {
		return Event{}, repository.ErrNotFound
	}
	row.Attendees = append([]domain.ID(nil), row.Attendees...)
	return row, nil
}

func (m *Memory) Events(context.Context) ([]Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Event, 0, len(m.events))
	for _, row := range m.events {
		row.Attendees = append([]domain.ID(nil), row.Attendees...)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartsAt != out[j].StartsAt {
			return out[i].StartsAt < out[j].StartsAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *Memory) PutDeadline(_ context.Context, row Deadline) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row.Offsets = append([]int(nil), row.Offsets...)
	m.deadlines[row.ID] = row
	return nil
}

func (m *Memory) DeleteDeadline(_ context.Context, id domain.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deadlines[id]; !ok {
		return repository.ErrNotFound
	}
	delete(m.deadlines, id)
	return nil
}

func (m *Memory) Deadline(_ context.Context, id domain.ID) (Deadline, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.deadlines[id]
	if !ok {
		return Deadline{}, repository.ErrNotFound
	}
	row.Offsets = append([]int(nil), row.Offsets...)
	return row, nil
}

func (m *Memory) Deadlines(context.Context) ([]Deadline, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Deadline, 0, len(m.deadlines))
	for _, row := range m.deadlines {
		row.Offsets = append([]int(nil), row.Offsets...)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DueOn != out[j].DueOn {
			return out[i].DueOn < out[j].DueOn
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *Memory) PutFeed(_ context.Context, row Feed) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.feeds[row.ID] = row
	return nil
}

func (m *Memory) DeleteFeed(_ context.Context, id domain.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.feeds[id]; !ok {
		return repository.ErrNotFound
	}
	delete(m.feeds, id)
	return nil
}

func (m *Memory) Feed(_ context.Context, id domain.ID) (Feed, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.feeds[id]
	if !ok {
		return Feed{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) FeedBySecret(_ context.Context, hash string) (Feed, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, row := range m.feeds {
		if row.SecretHash == hash {
			return row, nil
		}
	}
	return Feed{}, repository.ErrNotFound
}

func (m *Memory) Feeds(context.Context) ([]Feed, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Feed, 0, len(m.feeds))
	for _, row := range m.feeds {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) ClaimReminder(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sent[key] {
		return false, nil
	}
	m.sent[key] = true
	return true, nil
}

func (m *Memory) ReleaseReminder(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sent, key)
	return nil
}

func (m *Memory) PutNotice(_ context.Context, n Notice) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notices = append(m.notices, n)
	return nil
}

func (m *Memory) Notices(_ context.Context, user domain.ID) ([]Notice, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Notice
	for _, n := range m.notices {
		if n.UserID == user {
			out = append(out, n)
		}
	}
	return out, nil
}

func (m *Memory) AppendEvent(_ context.Context, row domain.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stream = append(m.stream, row)
	return nil
}

// Stream returns the activity rows written so far.
func (m *Memory) Stream() []domain.Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]domain.Event(nil), m.stream...)
}

func (m *Memory) IdemGet(key string) (IdemRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.idem[key]
	return rec, ok
}

func (m *Memory) IdemPut(key string, rec IdemRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idem[key] = rec
}
