package dashboard

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Memory is the in-process Store for unit tests. The running core uses PG.
type Memory struct {
	mu       sync.Mutex
	working  map[domain.ID][]LayoutWidget
	layouts  map[domain.ID]Layout
	roles    map[domain.Role]RoleLayout
	prefs    map[domain.ID]Prefs
	notify   map[domain.ID][]NotificationPref
	problems []ProblemReport
	profiles map[domain.ID]Profile
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{
		working: map[domain.ID][]LayoutWidget{}, layouts: map[domain.ID]Layout{},
		roles: map[domain.Role]RoleLayout{}, prefs: map[domain.ID]Prefs{}, notify: map[domain.ID][]NotificationPref{},
		profiles: map[domain.ID]Profile{},
	}
}

func (m *Memory) Working(_ context.Context, user domain.ID) ([]LayoutWidget, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.working[user]
	return cloneWidgets(w), ok, nil
}

func (m *Memory) PutWorking(_ context.Context, user domain.ID, widgets []LayoutWidget, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.working[user] = cloneWidgets(widgets)
	return nil
}

func (m *Memory) Layouts(_ context.Context, user domain.ID) ([]Layout, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Layout
	for _, l := range m.layouts {
		if l.UserID == user {
			l.Widgets = cloneWidgets(l.Widgets)
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out, nil
}

func (m *Memory) InsertLayout(_ context.Context, l Layout) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.layouts {
		if o.UserID == l.UserID && o.Slot == l.Slot {
			return ErrSlotTaken
		}
	}
	l.Widgets = cloneWidgets(l.Widgets)
	m.layouts[l.ID] = l
	return nil
}

func (m *Memory) UpdateLayout(_ context.Context, l Layout) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.layouts[l.ID]
	if !ok || old.UserID != l.UserID {
		return ErrNotFound
	}
	l.Slot, l.Default = old.Slot, old.Default
	l.Widgets = cloneWidgets(l.Widgets)
	m.layouts[l.ID] = l
	return nil
}

func (m *Memory) DeleteLayout(_ context.Context, user, id domain.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.layouts[id]
	if !ok || old.UserID != user {
		return ErrNotFound
	}
	delete(m.layouts, id)
	return nil
}

func (m *Memory) SetDefault(_ context.Context, user, id domain.ID, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !id.IsZero() {
		if l, ok := m.layouts[id]; !ok || l.UserID != user {
			return ErrNotFound
		}
	}
	for k, l := range m.layouts {
		if l.UserID != user {
			continue
		}
		want := k == id
		if l.Default != want {
			l.Default, l.UpdatedAt = want, at
			m.layouts[k] = l
		}
	}
	return nil
}

func (m *Memory) RoleLayout(_ context.Context, role domain.Role) (RoleLayout, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.roles[role]
	l.Widgets = cloneWidgets(l.Widgets)
	return l, ok, nil
}

func (m *Memory) PutRoleLayout(_ context.Context, l RoleLayout) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l.Widgets = cloneWidgets(l.Widgets)
	m.roles[l.Role] = l
	return nil
}

func (m *Memory) Prefs(_ context.Context, user domain.ID) (Prefs, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.prefs[user]
	return p, ok, nil
}

func (m *Memory) PutPrefs(_ context.Context, user domain.ID, p Prefs, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prefs[user] = p
	return nil
}

func (m *Memory) NotificationPrefs(_ context.Context, user domain.ID) ([]NotificationPref, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]NotificationPref(nil), m.notify[user]...), nil
}

func (m *Memory) PutNotificationPrefs(_ context.Context, user domain.ID, rows []NotificationPref, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notify[user] = append([]NotificationPref(nil), rows...)
	return nil
}

func (m *Memory) InsertProblem(_ context.Context, r ProblemReport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.problems = append(m.problems, r)
	return nil
}

func (m *Memory) Problems(_ context.Context) ([]ProblemReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]ProblemReport(nil), m.problems...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func cloneWidgets(in []LayoutWidget) []LayoutWidget {
	if in == nil {
		return nil
	}
	out := make([]LayoutWidget, len(in))
	for i, w := range in {
		out[i] = w
		if w.Filters != nil {
			f := make(map[string]string, len(w.Filters))
			for k, v := range w.Filters {
				f[k] = v
			}
			out[i].Filters = f
		}
	}
	return out
}

// Profile is one user's profile row.
func (m *Memory) Profile(_ context.Context, user domain.ID) (Profile, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr, ok := m.profiles[user]
	pr.Picture = append([]byte(nil), pr.Picture...)
	return pr, ok, nil
}

// PutProfile stores one profile row.
func (m *Memory) PutProfile(_ context.Context, pr Profile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr.Picture = append([]byte(nil), pr.Picture...)
	m.profiles[pr.UserID] = pr
	return nil
}
