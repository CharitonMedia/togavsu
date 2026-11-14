package calendar

import (
	"context"
	"sort"
	"sync"
	"time"

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
	// EventsBetween lists, in start order, at least every event overlapping [from, to):
	// callers still apply the exact window test.
	EventsBetween(ctx context.Context, from, to time.Time) ([]Event, error)
	// EventsAfter is EventsBetween in start order from just after the event at (start,
	// id) (zero after: from the first), at most limit events.
	EventsAfter(ctx context.Context, from, to time.Time, after Event, limit int) ([]Event, error)

	PutDeadline(ctx context.Context, row Deadline) error
	// DeleteDeadline removes a deadline. It only undoes a create whose audit failed.
	DeleteDeadline(ctx context.Context, id domain.ID) error
	Deadline(ctx context.Context, id domain.ID) (Deadline, error)
	Deadlines(ctx context.Context) ([]Deadline, error)
	// DeadlinesFor lists, by due date, at least every deadline matchDeadline(row, f,
	// today) keeps: callers still apply it.
	DeadlinesFor(ctx context.Context, f DeadlineFilter, today string) ([]Deadline, error)
	// DeadlineCounts counts per matter the deadlines DeadlinesFor would list.
	DeadlineCounts(ctx context.Context, f DeadlineFilter, today string) (map[domain.ID]int, error)

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

	// IdemGet reads a stored Idempotency-Key response; an error is not "no record".
	IdemGet(ctx context.Context, key string) (IdemRecord, bool, error)
	// IdemPut stores a response; inside audit.Service.Atomic it commits with the write.
	IdemPut(ctx context.Context, key string, rec IdemRecord) error
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

// EventsAfter filters Events by start order; the caller's window test does the rest.
func (m *Memory) EventsAfter(ctx context.Context, _, _ time.Time, after Event, limit int) ([]Event, error) {
	all, err := m.Events(ctx)
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, e := range all {
		if after.ID != "" && (e.StartsAt < after.StartsAt || e.StartsAt == after.StartsAt && e.ID <= after.ID) {
			continue
		}
		out = append(out, e)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// EventsBetween is Events; the caller's window test does the narrowing.
func (m *Memory) EventsBetween(ctx context.Context, _, _ time.Time) ([]Event, error) {
	return m.Events(ctx)
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

// DeadlineCounts counts the deadlines matchDeadline keeps, per matter.
func (m *Memory) DeadlineCounts(ctx context.Context, f DeadlineFilter, today string) (map[domain.ID]int, error) {
	rows, err := m.Deadlines(ctx)
	out := map[domain.ID]int{}
	for _, row := range rows {
		if matchDeadline(row, f, today) {
			out[row.MatterID]++
		}
	}
	return out, err
}

// DeadlinesFor is Deadlines; the caller's filter does the narrowing.
func (m *Memory) DeadlinesFor(ctx context.Context, _ DeadlineFilter, _ string) ([]Deadline, error) {
	return m.Deadlines(ctx)
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

func (m *Memory) IdemGet(_ context.Context, key string) (IdemRecord, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.idem[key]
	return rec, ok, nil
}

func (m *Memory) IdemPut(_ context.Context, key string, rec IdemRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idem[key] = rec
	return nil
}
