package modules

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
)

// Nav is the shell sidebar's module entries (internal/dashboard.Service). An enabled
// module's home tabs and its manifest sidebar links show there while it runs; disabling
// or removing it takes them away, with no restart (PRD 8.9, 9.4).
type Nav interface {
	AddModuleNav(module string, es []dashboard.ModuleNavEntry, count func(ctx context.Context, p domain.Principal, entryID string) (int, bool, error)) error
	RemoveModuleNav(module string)
}

// NavEntries is the sidebar for m: each home tab opens its framed page under the
// Activity section, and a sidebar link opens the home tab whose page it names. A link
// to a page that is not a home tab has no framed page in the core and is not shown.
func NavEntries(m Manifest) []dashboard.ModuleNavEntry {
	var out []dashboard.ModuleNavEntry
	seen := map[string]bool{}
	add := func(id, title, tab string) {
		if seen[tab] {
			return
		}
		seen[tab] = true
		out = append(out, dashboard.ModuleNavEntry{ID: id, Section: dashboard.SectionActivity, Label: title, Icon: "puzzle",
			Path: "/modules/" + m.ID + "/tabs/" + tab, Order: 900 + len(out)})
	}
	for _, l := range m.Sidebar {
		for _, t := range m.Tabs {
			if t.Placement == "home" && t.Path == l.Path {
				add(l.ID, l.Title, t.ID)
			}
		}
	}
	for _, t := range m.Tabs {
		if t.Placement == "home" {
			add(t.ID, t.Title, t.ID)
		}
	}
	return out
}

// InstalledModules lists every module the firm has installed, enabled or not, for the
// marketplace's flag and pull check (PRD 10.5: a disabled module is still the firm's).
func (s *Service) InstalledModules(ctx context.Context) ([]marketplace.InstalledModule, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, version FROM core.modules WHERE status IN ($1, $2, $3) ORDER BY id`,
		StatusInstalled, StatusDisabled, StatusAwaiting)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []marketplace.InstalledModule
	for rows.Next() {
		var m marketplace.InstalledModule
		if err := rows.Scan(&m.ID, &m.Version); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
