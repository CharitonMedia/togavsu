package modules

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

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

// NavEntries is the sidebar for m (CORE-08): sidebar[] entries use section, subgroup,
// icon, order and badge; home tabs without a sidebar entry still appear under Activity
// with the puzzle icon. A sidebar path must match a home tab (Validate).
func NavEntries(m Manifest) []dashboard.ModuleNavEntry {
	var out []dashboard.ModuleNavEntry
	seen := map[string]bool{}
	homeByPath := map[string]Tab{}
	for _, t := range m.Tabs {
		if t.Placement == "home" {
			homeByPath[t.Path] = t
		}
	}
	add := func(id, title, tab, section, subgroup, icon string, order int, tone string) {
		if seen[tab] {
			return
		}
		seen[tab] = true
		if section == "" {
			section = dashboard.SectionActivity
		}
		if icon == "" {
			icon = "puzzle"
		}
		out = append(out, dashboard.ModuleNavEntry{
			ID: id, Section: section, Subgroup: subgroup, Label: title, Icon: icon,
			Path: "/modules/" + m.ID + "/tabs/" + tab, Order: order, Tone: tone,
		})
	}
	for _, l := range m.Sidebar {
		t, ok := homeByPath[l.Path]
		if !ok {
			continue
		}
		order := domain.SidebarDefaultOrder
		if l.Order != nil {
			order = *l.Order
		}
		tone := ""
		if l.Badge != nil {
			tone = l.Badge.Tone
		}
		add(l.ID, l.Title, t.ID, l.Section, l.Subgroup, l.Icon, order, tone)
	}
	for _, t := range m.Tabs {
		if t.Placement == "home" {
			add(t.ID, t.Title, t.ID, dashboard.SectionActivity, "", "puzzle", domain.SidebarDefaultOrder+len(out), "")
		}
	}
	return out
}

// badgeCacheKey is one viewer and sidebar entry for the 30-second badge cache (CORE-08).
type badgeCacheKey struct {
	module, entry, user string
}

type badgeCacheEntry struct {
	count int
	ok    bool
	until time.Time
}

var (
	badgeMu    sync.Mutex
	badgeCache = map[badgeCacheKey]badgeCacheEntry{}
)

// badgeCounter returns the AddModuleNav count hook for module: each entry with a badge
// path is fetched as the viewer with a 500 ms limit and cached 30 seconds (CORE-08).
func (s *Service) badgeCounter(module string, m Manifest) func(ctx context.Context, p domain.Principal, entryID string) (int, bool, error) {
	paths := map[string]string{}
	for _, l := range m.Sidebar {
		if l.Badge != nil && l.Badge.Path != "" {
			paths[l.ID] = l.Badge.Path
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return func(ctx context.Context, p domain.Principal, entryID string) (int, bool, error) {
		path := paths[entryID]
		if path == "" {
			return 0, false, nil
		}
		key := badgeCacheKey{module: module, entry: entryID, user: string(p.UserID)}
		now := s.now()
		badgeMu.Lock()
		if e, ok := badgeCache[key]; ok && now.Before(e.until) {
			badgeMu.Unlock()
			return e.count, e.ok, nil
		}
		badgeMu.Unlock()
		n, ok := s.fetchBadge(ctx, p, module, path)
		badgeMu.Lock()
		badgeCache[key] = badgeCacheEntry{count: n, ok: ok, until: now.Add(30 * time.Second)}
		badgeMu.Unlock()
		return n, ok, nil
	}
}

// fetchBadge GETs the module badge path with a context token for p. Anything but
// {count: non-negative int} within 500 ms leaves the badge off.
func (s *Service) fetchBadge(ctx context.Context, p domain.Principal, module, path string) (int, bool) {
	ct, err := s.IssueContext(ctx, p, module, "")
	if err != nil {
		return 0, false
	}
	callCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	base, err := s.moduleBase(callCtx, module)
	if err != nil || base == "" {
		return 0, false
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, stringsTrimRightSlash(base)+path, nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set(ContextHeader, ct.Token)
	client := s.d.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, false
	}
	var body struct {
		Count *int `json:"count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Count == nil || *body.Count < 0 {
		return 0, false
	}
	return *body.Count, true
}

func stringsTrimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// moduleBase is the in-process or runtime URL for an installed module.
func (s *Service) moduleBase(_ context.Context, module string) (string, error) {
	if s.d.Runtime == nil {
		return "", nil
	}
	return s.d.Runtime.BaseURL(module), nil
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
