package dashboard

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

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

// CoreWidgets is the PRD 8.6 core widget list (catalog.json), each with the page its
// title opens. The roles are the contract's roles for that widget's API route.
func CoreWidgets() []WidgetDef {
	return append([]WidgetDef(nil), catalog.Widgets...)
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
		if w.W < fixed.GridOrigin || w.W > GridColumns || w.H < fixed.GridOrigin {
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

// Catalog holds the core widgets and the widgets of approved, enabled modules. Modules
// install and uninstall while the core serves requests (PRD 9, AC-012), so every read
// takes mu's read lock and AddModule/RemoveModule take the write lock. A source is
// copied out under the lock and run after it is released.
type Catalog struct {
	mu      sync.RWMutex
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
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range ws {
		id := ModuleWidgetID(module, w.ID)
		roles := w.Roles
		if len(roles) == 0 {
			roles = staffRoles
		}
		width, height := w.W, w.H
		if width < fixed.MinWidgetSize {
			width = fixed.DefaultWidgetW
		}
		if height < fixed.MinWidgetSize {
			height = fixed.DefaultWidgetH
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
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, d := range c.defs {
		if d.Module == module {
			delete(c.defs, id)
			delete(c.sources, id)
		}
	}
}

// Def returns one widget definition.
func (c *Catalog) Def(id string) (WidgetDef, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	d, ok := c.defs[id]
	return d, ok
}

// Visible reports whether p may have widget id on a layout: the widget exists and one of
// p's roles is among the roles its API route serves.
func (c *Catalog) Visible(p domain.Principal, id string) bool {
	c.mu.RLock()
	d, ok := c.defs[id]
	c.mu.RUnlock()
	return ok && visible(p, d)
}

func visible(p domain.Principal, d WidgetDef) bool {
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
	c.mu.RLock()
	for _, d := range c.defs {
		if visible(p, d) {
			out = append(out, d)
		}
	}
	c.mu.RUnlock()
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
	c.mu.RLock()
	d, ok := c.defs[id]
	src := c.sources[id]
	c.mu.RUnlock()
	if !ok {
		return Widget{}, domain.NotFound("widget")
	}
	if !visible(p, d) {
		return Widget{}, domain.Forbidden(domain.ReasonRole)
	}
	if src == nil {
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
		w.Target.Path = d.Page
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
