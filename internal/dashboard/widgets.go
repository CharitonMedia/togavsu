package dashboard

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Target is where a widget's title opens, with the widget's own filters.
type Target struct {
	Path  string            `json:"path"`
	Query map[string]string `json:"query"`
}

// URL is the target as a link. Query keys are sorted so the link is stable.
func (t Target) URL() string {
	if len(t.Query) == 0 {
		return t.Path
	}
	v := url.Values{}
	for k, val := range t.Query {
		v.Set(k, val)
	}
	return t.Path + "?" + v.Encode()
}

// WidgetRow is one record in a widget; TargetPath opens that record.
type WidgetRow struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle,omitempty"`
	TargetPath string `json:"target_path"`
}

// Widget is the contract Widget payload.
type Widget struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Target   Target      `json:"target"`
	PagePath string      `json:"page_path"`
	Rows     []WidgetRow `json:"rows"`
}

// Source loads one widget for the viewer from the domain service that owns its data.
// It must apply the same access the API applies; the dashboard adds none of its own.
type Source func(ctx context.Context, p domain.Principal, filters map[string]string) (Widget, error)

// WidgetDef is one entry in the widget picker.
type WidgetDef struct {
	ID    string        `json:"id"`
	Title string        `json:"title"`
	Page  string        `json:"page_path"`
	Roles []domain.Role `json:"roles"`
	// Module is the module id for a module widget; empty for core widgets.
	Module string `json:"module,omitempty"`
	W      int    `json:"w"`
	H      int    `json:"h"`
	Help   string `json:"help_topic,omitempty"`
}

var staffRoles = []domain.Role{domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff}
var allStaff = []domain.Role{domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff, domain.RoleAccountant}

// CoreWidgets is the PRD 8.6 core widget list, each with the page its title opens. The
// roles are the contract's roles for that widget's API route.
func CoreWidgets() []WidgetDef {
	return []WidgetDef{
		{ID: "my-time", Title: "My time today and this week", Page: "/billing/time", Roles: staffRoles, W: 4, H: 3},
		{ID: "running-timer", Title: "Running timer", Page: "/billing/time", Roles: staffRoles, W: 4, H: 2},
		{ID: "unbilled", Title: "Unbilled time", Page: "/billing/reports/unbilled-time", Roles: allStaff, W: 4, H: 3},
		{ID: "my-open-matters", Title: "My open matters", Page: "/practice/matters", Roles: staffRoles, W: 6, H: 4},
		{ID: "upcoming-deadlines", Title: "Upcoming deadlines", Page: "/calendar/deadlines", Roles: staffRoles, W: 6, H: 4},
		{ID: "todays-calendar", Title: "Today's calendar", Page: "/calendar", Roles: staffRoles, W: 6, H: 3},
		{ID: "trust-alerts", Title: "Trust alerts", Page: "/trust/flags", Roles: allStaff, W: 6, H: 3},
		{ID: "replenishment-requests", Title: "Replenishment requests", Page: "/trust/replenishment", Roles: allStaff, W: 6, H: 3},
		{ID: "ar-aging", Title: "AR aging", Page: "/billing/reports/ar-aging", Roles: allStaff, W: 6, H: 3},
		{ID: "recent-activity", Title: "Recent activity", Page: "/practice/activity", Roles: staffRoles, W: 6, H: 4},
		{ID: "pending-conflict-checks", Title: "Pending conflict checks", Page: "/practice/matters", Roles: staffRoles, W: 6, H: 3},
		{ID: "module-alerts", Title: "Module alerts", Page: "/admin?tab=modules", Roles: []domain.Role{domain.RoleAdmin}, W: 6, H: 3},
	}
}

// ManifestWidget is a widget a module manifest declares (PRD 9.2, 9.4). Target is the
// click-through the core opens, normally one of the module's own tabs.
type ManifestWidget struct {
	ID     string        `json:"id"`
	Title  string        `json:"title"`
	Target string        `json:"target"`
	Roles  []domain.Role `json:"roles"`
	W      int           `json:"w"`
	H      int           `json:"h"`
}

var moduleSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateManifestWidgets is the install-time check: every widget names a click-through
// target inside the core's site, and ids are unique. The module runtime calls it before
// it approves an install; a refusal installs nothing.
func ValidateManifestWidgets(module string, ws []ManifestWidget) error {
	if !moduleSlug.MatchString(module) {
		return domain.Invalid("module_id", "module id must be a slug")
	}
	seen := map[string]bool{}
	for i, w := range ws {
		field := fmt.Sprintf("widgets[%d]", i)
		if strings.TrimSpace(w.ID) == "" || strings.TrimSpace(w.Title) == "" {
			return domain.Invalid(field, "a widget needs an id and a title")
		}
		if seen[w.ID] {
			return domain.Invalid(field, "duplicate widget id "+w.ID)
		}
		seen[w.ID] = true
		t := strings.TrimSpace(w.Target)
		if t == "" {
			return domain.Invalid(field+".target", "widget "+w.ID+" has no click-through target")
		}
		u, err := url.Parse(t)
		if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(t, "//") {
			return domain.Invalid(field+".target", "widget "+w.ID+" target must be a path on this site")
		}
		if w.W < 0 || w.W > GridColumns || w.H < 0 {
			return domain.Invalid(field, "widget size is outside the grid")
		}
		for _, r := range w.Roles {
			if _, err := domain.ParseRole(string(r)); err != nil {
				return domain.Invalid(field+".roles", "unknown role")
			}
		}
	}
	return nil
}

// Catalog holds the core widgets and the widgets of approved, enabled modules.
type Catalog struct {
	defs    map[string]WidgetDef
	sources map[string]Source
}

// NewCatalog returns the core widgets with the given sources. A core widget without a
// source stays in the catalog; loading it reports that its data is not available.
func NewCatalog(sources map[string]Source) *Catalog {
	c := &Catalog{defs: map[string]WidgetDef{}, sources: map[string]Source{}}
	for _, d := range CoreWidgets() {
		c.defs[d.ID] = d
	}
	for id, s := range sources {
		c.sources[id] = s
	}
	return c
}

// ModuleWidgetID is the catalog id of a module's widget.
func ModuleWidgetID(module, id string) string { return module + "." + id }

// AddModule validates and adds a module's widgets once the firm approves them. src
// loads the module widget's rows (answered by the module and checked by the core).
func (c *Catalog) AddModule(module string, ws []ManifestWidget, src func(widgetID string) Source) error {
	if err := ValidateManifestWidgets(module, ws); err != nil {
		return err
	}
	for _, w := range ws {
		id := ModuleWidgetID(module, w.ID)
		roles := w.Roles
		if len(roles) == 0 {
			roles = staffRoles
		}
		width, height := w.W, w.H
		if width == 0 {
			width = 4
		}
		if height == 0 {
			height = 3
		}
		c.defs[id] = WidgetDef{ID: id, Title: w.Title, Page: w.Target, Roles: roles, Module: module, W: width, H: height}
		if src != nil {
			c.sources[id] = src(w.ID)
		}
	}
	return nil
}

// RemoveModule drops a module's widgets; saved layouts lose them when they next load.
func (c *Catalog) RemoveModule(module string) {
	for id, d := range c.defs {
		if d.Module == module {
			delete(c.defs, id)
			delete(c.sources, id)
		}
	}
}

// Def returns one widget definition.
func (c *Catalog) Def(id string) (WidgetDef, bool) {
	d, ok := c.defs[id]
	return d, ok
}

// Visible reports whether p may have widget id on a layout: the widget exists and one of
// p's roles is among the roles its API route serves.
func (c *Catalog) Visible(p domain.Principal, id string) bool {
	d, ok := c.defs[id]
	if !ok {
		return false
	}
	for _, r := range d.Roles {
		if p.HasRole(r) {
			return true
		}
	}
	return false
}

// For lists the widgets p may add, core widgets first in PRD order, then modules.
func (c *Catalog) For(p domain.Principal) []WidgetDef {
	order := map[string]int{}
	for i, d := range CoreWidgets() {
		order[d.ID] = i
	}
	var out []WidgetDef
	for id, d := range c.defs {
		if c.Visible(p, id) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		oi, iok := order[out[i].ID]
		oj, jok := order[out[j].ID]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Load runs one widget's source as p. A widget p cannot see is forbidden before the
// source runs; the source applies record-level access (walls, attorney-only) itself.
func (c *Catalog) Load(ctx context.Context, p domain.Principal, id string, filters map[string]string) (Widget, error) {
	if !c.Visible(p, id) {
		if _, ok := c.defs[id]; !ok {
			return Widget{}, domain.NotFound("widget")
		}
		return Widget{}, domain.Forbidden(domain.ReasonRole)
	}
	src := c.sources[id]
	if src == nil {
		d := c.defs[id]
		return Widget{ID: id, Title: d.Title, Target: Target{Path: d.Page, Query: map[string]string{}}, PagePath: d.Page, Rows: []WidgetRow{}}, errUnavailable
	}
	w, err := src(ctx, p, filters)
	if err != nil {
		return Widget{}, err
	}
	if w.Rows == nil {
		w.Rows = []WidgetRow{}
	}
	if w.Target.Query == nil {
		w.Target.Query = map[string]string{}
	}
	if w.Target.Path == "" {
		w.Target.Path = c.defs[id].Page
	}
	if w.PagePath == "" {
		w.PagePath = w.Target.Path
	}
	w.ID = id
	return w, nil
}

// errUnavailable marks a core widget whose domain is not composed into this core.
var errUnavailable = &domain.APIError{Code: "unavailable", Message: "this widget's data is not available in this core yet"}

// IsUnavailable reports errUnavailable.
func IsUnavailable(err error) bool { return err == errUnavailable }
