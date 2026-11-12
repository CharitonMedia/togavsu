package practice

import "github.com/CharitonMedia/togavsu/internal/domain"

// childIndex maps a contact or matter to the ids of its child rows, so a screen that
// asks for every contact's phones or every matter's parties does not scan the whole
// collection once per parent (AC-017: 20,000 matters and their contacts). Memory builds
// it on first use and drops it on any write to those collections; m.mu guards it.
type childIndex struct {
	addresses, emails, phones, tags map[domain.ID][]domain.ID
	partiesByMatter, partiesByCon   map[domain.ID][]domain.ID
	walls                           map[domain.ID][]domain.ID
}

// children returns the index, building it if a write dropped it. The caller holds m.mu.
func (m *Memory) children() *childIndex {
	if m.idx != nil {
		return m.idx
	}
	x := &childIndex{
		addresses: map[domain.ID][]domain.ID{}, emails: map[domain.ID][]domain.ID{}, phones: map[domain.ID][]domain.ID{},
		tags: map[domain.ID][]domain.ID{}, partiesByMatter: map[domain.ID][]domain.ID{}, partiesByCon: map[domain.ID][]domain.ID{},
		walls: map[domain.ID][]domain.ID{},
	}
	for id, r := range m.addresses {
		x.addresses[r.ContactID] = append(x.addresses[r.ContactID], id)
	}
	for id, r := range m.emails {
		x.emails[r.ContactID] = append(x.emails[r.ContactID], id)
	}
	for id, r := range m.phones {
		x.phones[r.ContactID] = append(x.phones[r.ContactID], id)
	}
	for id, r := range m.tags {
		x.tags[r.ContactID] = append(x.tags[r.ContactID], id)
	}
	for id, r := range m.parties {
		x.partiesByMatter[r.MatterID] = append(x.partiesByMatter[r.MatterID], id)
		x.partiesByCon[r.ContactID] = append(x.partiesByCon[r.ContactID], id)
	}
	for id, r := range m.walls {
		x.walls[r.MatterID] = append(x.walls[r.MatterID], id)
	}
	m.idx = x
	return x
}

// pick returns the rows of src named by ids.
func pick[T any](src map[domain.ID]T, ids []domain.ID) []T {
	var out []T
	for _, id := range ids {
		if r, ok := src[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

// indexed is the sub-map of src named by ids.
func indexed[T any](src map[domain.ID]T, ids []domain.ID) map[domain.ID]T {
	out := make(map[domain.ID]T, len(ids))
	for _, id := range ids {
		if r, ok := src[id]; ok {
			out[id] = r
		}
	}
	return out
}

// glanceIndex holds the per-matter figures a matter row shows (invoice count, trust
// balance, time), so listing 20,000 matters does not scan every invoice, balance and
// time entry once per matter. Memory drops it on any invoice, trust or time write.
type glanceIndex struct {
	invoices map[domain.ID]int
	trust    map[domain.ID][]domain.Cents
	times    map[domain.ID][2]int // entries, increments
}

// MatterGlance returns the matter's invoice count, trust balances and time totals.
func (m *Memory) MatterGlance(matter domain.ID) (invoices int, trust []domain.Cents, entries, increments int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gidx == nil {
		g := &glanceIndex{invoices: map[domain.ID]int{}, trust: map[domain.ID][]domain.Cents{}, times: map[domain.ID][2]int{}}
		for _, r := range m.invoices {
			g.invoices[r.MatterID]++
		}
		for _, r := range m.trust {
			g.trust[r.MatterID] = append(g.trust[r.MatterID], r.BalanceCents)
		}
		for _, r := range m.times {
			t := g.times[r.MatterID]
			g.times[r.MatterID] = [2]int{t[0] + 1, t[1] + r.Increments}
		}
		m.gidx = g
	}
	t := m.gidx.times[matter]
	return m.gidx.invoices[matter], m.gidx.trust[matter], t[0], t[1]
}
