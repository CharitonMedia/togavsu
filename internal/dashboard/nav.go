package dashboard

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Sidebar sections in PRD 8.9 order. The top band has no heading.
const (
	SectionTop            = "top"
	SectionCommunications = "communications"
	SectionDocuments      = "documents"
	SectionLitigation     = "litigation"
	SectionMoney          = "money"
	SectionIntake         = "intake"
	SectionActivity       = "activity"
	SectionSystem         = "system"
)

// Sections is the section order with its heading.
var Sections = []struct{ ID, Label string }{
	{SectionTop, ""}, {SectionCommunications, "Communications"}, {SectionDocuments, "Documents"},
	{SectionLitigation, "Litigation"}, {SectionMoney, "Money"}, {SectionIntake, "Intake"},
	{SectionActivity, "Activity"}, {SectionSystem, "System"},
}

// NavEntry is one sidebar link. Resource names what the linked screen reads, so the
// entry shows only to roles the service layer would serve; the route still checks.
type NavEntry struct {
	ID       string            `json:"id"`
	Section  string            `json:"section"`
	Subgroup string            `json:"subgroup,omitempty"`
	Label    string            `json:"label"`
	Icon     string            `json:"icon"`
	Path     string            `json:"path"`
	Order    int               `json:"order"`
	Badge    string            `json:"badge,omitempty"`
	Tone     string            `json:"tone,omitempty"`
	Module   string            `json:"module,omitempty"`
	Count    *int              `json:"count,omitempty"`
	Resource domain.Resource   `json:"-"`
	Roles    []domain.Role     `json:"-"`
	Extra    map[string]string `json:"-"`
}

// CoreNav is the PRD 8.9 core sidebar (catalog.json).
func CoreNav() []NavEntry {
	out := make([]NavEntry, len(catalog.Nav))
	for i, n := range catalog.Nav {
		e := n.NavEntry
		e.Resource = domain.Resource(n.Resource)
		for _, r := range n.Roles {
			e.Roles = append(e.Roles, domain.Role(r))
		}
		out[i] = e
	}
	return out
}

// ModuleNavEntry is a sidebar entry from a module manifest (PRD 8.9). Badge is the
// module's count; Tone is normal or warning.
type ModuleNavEntry struct {
	ID       string        `json:"id"`
	Section  string        `json:"section"`
	Subgroup string        `json:"subgroup,omitempty"`
	Label    string        `json:"label"`
	Icon     string        `json:"icon"`
	Path     string        `json:"path"`
	Order    int           `json:"order"`
	Tone     string        `json:"tone,omitempty"`
	Roles    []domain.Role `json:"roles,omitempty"`
}

type moduleNav struct {
	entries []NavEntry
	count   func(ctx context.Context, p domain.Principal, entryID string) (int, bool, error)
}

type navRegistry struct {
	mu      sync.RWMutex
	modules map[string]moduleNav
}

func newNavRegistry() *navRegistry { return &navRegistry{modules: map[string]moduleNav{}} }

func validSection(s string) bool {
	for _, sec := range Sections {
		if sec.ID == s {
			return true
		}
	}
	return false
}

// AddModuleNav registers an enabled module's sidebar entries. count answers each entry's
// badge for the viewer; nil means the entries carry no count.
func (s *Service) AddModuleNav(module string, es []ModuleNavEntry, count func(ctx context.Context, p domain.Principal, entryID string) (int, bool, error)) error {
	if !moduleSlug.MatchString(module) {
		return domain.Invalid("module_id", "module id must be a slug")
	}
	var out []NavEntry
	for _, e := range es {
		if e.ID == "" || strings.TrimSpace(e.Label) == "" || !strings.HasPrefix(e.Path, "/") || strings.HasPrefix(e.Path, "//") {
			return domain.Invalid("nav", "a module sidebar entry needs an id, a label and a path on this site")
		}
		if !validSection(e.Section) || e.Section == SectionTop {
			return domain.Invalid("nav.section", "unknown sidebar section "+e.Section)
		}
		if e.Tone != "" && e.Tone != "normal" && e.Tone != "warning" {
			return domain.Invalid("nav.tone", "tone is normal or warning")
		}
		sub := ""
		if e.Subgroup != "" {
			sub = module + "." + e.Subgroup
		}
		out = append(out, NavEntry{ID: module + "." + e.ID, Section: e.Section, Subgroup: sub, Label: e.Label, Icon: e.Icon,
			Path: e.Path, Order: e.Order, Tone: e.Tone, Module: module, Roles: e.Roles, Badge: "module:" + e.ID})
	}
	s.navs.mu.Lock()
	s.navs.modules[module] = moduleNav{entries: out, count: count}
	s.navs.mu.Unlock()
	return nil
}

// RemoveModuleNav drops a disabled or removed module's entries.
func (s *Service) RemoveModuleNav(module string) {
	s.navs.mu.Lock()
	delete(s.navs.modules, module)
	s.navs.mu.Unlock()
}

// NavGroup is one sidebar section or subgroup as rendered.
type NavGroup struct {
	ID        string     `json:"id"`
	Label     string     `json:"label"`
	Entries   []NavEntry `json:"entries"`
	Subgroups []NavGroup `json:"subgroups,omitempty"`
	Collapsed bool       `json:"collapsed"`
}

// NavContext is GET /api/v1/nav/context: everything the sidebar needs.
type NavContext struct {
	IsAdmin          bool             `json:"is_admin"`
	FirmShortName    string           `json:"firm_short_name"`
	StaffPortalLabel string           `json:"staff_portal_label"`
	SidebarCollapsed bool             `json:"sidebar_collapsed"`
	Subgroups        []SubgroupState  `json:"subgroups"`
	Badges           map[string]int   `json:"badges"`
	Entries          []NavEntry       `json:"entries"`
	Modules          []map[string]any `json:"modules"`
	// Groups is the same entries arranged for the shell; not part of the JSON contract.
	Groups []NavGroup `json:"-"`
}

func roleReads(p domain.Principal, res domain.Resource) bool {
	if res == "" {
		return true
	}
	return domain.Decide(p, domain.AccessInput{Resource: res, Action: domain.ActionRead}).Allow
}

func (s *Service) entryVisible(p domain.Principal, e NavEntry) bool {
	if len(e.Roles) > 0 {
		ok := false
		for _, r := range e.Roles {
			if p.HasRole(r) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return roleReads(p, e.Resource)
}

// Nav builds the sidebar for p: entries its roles and the enabled modules allow, live
// badge counts of records p can read, and p's saved collapse state.
func (s *Service) Nav(ctx context.Context, p domain.Principal) (NavContext, error) {
	if err := staff(p); err != nil {
		return NavContext{}, err
	}
	prefs, _, err := s.d.Store.Prefs(ctx, p.UserID)
	if err != nil {
		return NavContext{}, err
	}
	short := s.setting(ctx, "firm.short_name")
	if short == "" {
		short = s.setting(ctx, "firm.name")
	}
	if short == "" {
		short = "Togavsu"
	}
	nc := NavContext{
		IsAdmin: p.HasRole(domain.RoleAdmin), FirmShortName: short, StaffPortalLabel: s.setting(ctx, "appearance.staff_portal_label"),
		SidebarCollapsed: prefs.Sidebar.Collapsed, Subgroups: prefs.Sidebar.Subgroups, Badges: map[string]int{}, Entries: []NavEntry{}, Modules: []map[string]any{},
	}
	if nc.Subgroups == nil {
		nc.Subgroups = []SubgroupState{}
	}
	entries := CoreNav()
	s.navs.mu.RLock()
	mods := make([]string, 0, len(s.navs.modules))
	for m := range s.navs.modules {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	counts := map[string]moduleNav{}
	for _, m := range mods {
		mn := s.navs.modules[m]
		counts[m] = mn
		entries = append(entries, mn.entries...)
		nc.Modules = append(nc.Modules, map[string]any{"id": m})
	}
	s.navs.mu.RUnlock()
	for _, e := range entries {
		if !s.entryVisible(p, e) {
			continue
		}
		switch {
		case e.Module != "" && counts[e.Module].count != nil:
			n, ok, err := counts[e.Module].count(ctx, p, strings.TrimPrefix(e.ID, e.Module+"."))
			if err == nil && ok {
				nc.Badges[e.Badge] = n
				v := n
				e.Count = &v
			}
		case e.Badge != "" && s.d.Badges[e.Badge] != nil:
			n, err := s.d.Badges[e.Badge](ctx, p)
			if err == nil {
				nc.Badges[e.Badge] = n
				v := n
				e.Count = &v
			}
		}
		nc.Entries = append(nc.Entries, e)
	}
	nc.Groups = s.group(nc.Entries, prefs.Sidebar)
	return nc, nil
}

func (s *Service) group(entries []NavEntry, state SidebarState) []NavGroup {
	collapsed := map[string]bool{}
	for _, g := range state.Subgroups {
		collapsed[g.ID] = g.Collapsed
	}
	var out []NavGroup
	for _, sec := range Sections {
		g := NavGroup{ID: sec.ID, Label: sec.Label}
		subs := map[string]*NavGroup{}
		var subOrder []string
		var rows []NavEntry
		for _, e := range entries {
			if e.Section == sec.ID {
				rows = append(rows, e)
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Order < rows[j].Order })
		for _, e := range rows {
			if e.Subgroup == "" {
				g.Entries = append(g.Entries, e)
				continue
			}
			sg, ok := subs[e.Subgroup]
			if !ok {
				label := catalog.Subgroups[e.Subgroup]
				if label == "" {
					_, after, _ := strings.Cut(e.Subgroup, ".")
					label = capitalize(after)
				}
				sg = &NavGroup{ID: e.Subgroup, Label: label, Collapsed: collapsed[e.Subgroup]}
				subs[e.Subgroup] = sg
				subOrder = append(subOrder, e.Subgroup)
			}
			sg.Entries = append(sg.Entries, e)
		}
		for _, id := range subOrder {
			g.Subgroups = append(g.Subgroups, *subs[id])
		}
		if len(g.Entries) == 0 && len(g.Subgroups) == 0 {
			continue // a section with nothing visible is hidden
		}
		out = append(out, g)
	}
	return out
}

// Sidebar returns p's saved sidebar state.
func (s *Service) Sidebar(ctx context.Context, p domain.Principal) (SidebarState, error) {
	if err := staff(p); err != nil {
		return SidebarState{}, err
	}
	prefs, _, err := s.d.Store.Prefs(ctx, p.UserID)
	if prefs.Sidebar.Subgroups == nil {
		prefs.Sidebar.Subgroups = []SubgroupState{}
	}
	return prefs.Sidebar, err
}

// PutSidebar saves p's sidebar state with the user, so it follows them.
func (s *Service) PutSidebar(ctx context.Context, p domain.Principal, st SidebarState) (SidebarState, error) {
	if err := staff(p); err != nil {
		return SidebarState{}, err
	}
	seen := map[string]bool{}
	var subs []SubgroupState
	for _, g := range st.Subgroups {
		if g.ID == "" || len(g.ID) > fixed.SubgroupIDMax || seen[g.ID] {
			return SidebarState{}, domain.Invalid("subgroups", "subgroup ids must be unique and non-empty")
		}
		seen[g.ID] = true
		subs = append(subs, g)
	}
	if len(subs) > fixed.MaxSubgroups {
		return SidebarState{}, domain.Invalid("subgroups", "too many subgroups")
	}
	if subs == nil {
		subs = []SubgroupState{}
	}
	st.Subgroups = subs
	s.lmu.Lock()
	defer s.lmu.Unlock()
	prefs, _, err := s.d.Store.Prefs(ctx, p.UserID)
	if err != nil {
		return SidebarState{}, err
	}
	prefs.Sidebar = st
	if err := s.d.Store.PutPrefs(ctx, p.UserID, prefs, s.d.Now().UTC()); err != nil {
		return SidebarState{}, err
	}
	return st, nil
}

// ToggleSubgroup flips one subgroup, for the no-script form post.
func (s *Service) ToggleSubgroup(ctx context.Context, p domain.Principal, id string, collapsed bool) (SidebarState, error) {
	st, err := s.Sidebar(ctx, p)
	if err != nil {
		return SidebarState{}, err
	}
	found := false
	for i := range st.Subgroups {
		if st.Subgroups[i].ID == id {
			st.Subgroups[i].Collapsed, found = collapsed, true
		}
	}
	if !found {
		st.Subgroups = append(st.Subgroups, SubgroupState{ID: id, Collapsed: collapsed})
	}
	return s.PutSidebar(ctx, p, st)
}
