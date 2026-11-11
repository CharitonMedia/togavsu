package modules

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ManifestFile is the signed manifest's name inside a module package (PRD 9.2).
const ManifestFile = "togavsu-module.yaml"

// Manifest is togavsu-module.yaml (PRD 9.2).
type Manifest struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Author      string        `json:"author"`
	Homepage    string        `json:"homepage"`
	License     string        `json:"license"`
	Price       string        `json:"price"`
	Container   Container     `json:"container"`
	Permissions []Permission  `json:"permissions"`
	Widgets     []Widget      `json:"widgets"`
	Help        HelpGuide     `json:"help"`
	Tabs        []Tab         `json:"tabs"`
	MatterItems []MatterItem  `json:"matter_items"`
	Sidebar     []Link        `json:"sidebar"`
	QuickCreate []Link        `json:"quick_create"`
	Search      []Link        `json:"search_scopes"`
	Events      []string      `json:"events"`
	Emits       []string      `json:"emits"`
	Data        Data          `json:"data"`
	Replaces    string        `json:"replaces"`
	Settings    []SettingSpec `json:"settings"`
	AITasks     []AITask      `json:"ai_tasks"`
}

// Container is the image the supervisor runs, pinned by digest.
type Container struct {
	Image  string `json:"image"`
	Digest string `json:"digest"`
	Port   int    `json:"port"`
	// Events is the path on the module that receives signed event webhooks.
	Events string `json:"events"`
}

// Permission is one requested API scope. Required defaults to true.
type Permission struct {
	Scope    string `json:"scope"`
	Required *bool  `json:"required"`
	Reason   string `json:"reason"`
}

// IsRequired reports whether declining the scope cancels the install.
func (p Permission) IsRequired() bool { return p.Required == nil || *p.Required }

// Size is a widget size in grid cells.
type Size struct {
	W int `json:"w"`
	H int `json:"h"`
}

// Target is a widget's click-through: one of the module's own tabs or a core page.
type Target struct {
	Tab    string   `json:"tab"`
	Page   string   `json:"page"`
	Params []string `json:"params"`
}

// Widget is a home-screen widget the module serves (PRD 8.6, 9.2).
type Widget struct {
	ID     string        `json:"id"`
	Title  string        `json:"title"`
	Path   string        `json:"path"`
	Size   Size          `json:"size"`
	Sizes  []Size        `json:"sizes"`
	Target *Target       `json:"target"`
	Roles  []domain.Role `json:"roles"`
}

// HelpGuide is the module's guide in the help center (PRD 8.11).
type HelpGuide struct {
	Heading string     `json:"heading"`
	Pages   []HelpPage `json:"pages"`
}

// HelpPage is one guide page.
type HelpPage struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Keywords []string `json:"keywords"`
	Covers   []string `json:"covers"`
}

// Tab is a home-screen or matter-screen tab the module serves.
type Tab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Placement is "home" or "matter".
	Placement string `json:"placement"`
	Path      string `json:"path"`
}

// MatterItem is a matter-screen panel or action.
type MatterItem struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

// Link is a sidebar entry, quick-create item or search scope.
type Link struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

// Data is the module's database extension (PRD 9.9).
type Data struct {
	// Extends lists the core records the module extends.
	Extends      []Extension     `json:"extends"`
	Views        []PublishedView `json:"views"`
	CustomFields []CustomField   `json:"custom_fields"`
}

// Extension is an extension table keyed by a core record's id, readable through include=.
type Extension struct {
	Name    string   `json:"name"`
	Record  string   `json:"record"`
	Table   string   `json:"table"`
	Key     string   `json:"key"`
	Columns []Column `json:"columns"`
}

// PublishedView is a read view keyed to a core record (PRD 9.9).
type PublishedView struct {
	Name    string   `json:"name"`
	Record  string   `json:"record"`
	Key     string   `json:"key"`
	Columns []Column `json:"columns"`
}

// Column is one readable column. AttorneyOnly values are redacted for other roles.
type Column struct {
	Name         string `json:"name"`
	AttorneyOnly bool   `json:"attorney_only"`
}

// CustomField is a field the module adds to a core record's Custom Fields card.
type CustomField struct {
	Record string `json:"record"`
	Key    string `json:"key"`
	Label  string `json:"label"`
	Type   string `json:"type"`
}

// SettingSpec is one field of the module's settings form.
type SettingSpec struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Default  string   `json:"default"`
	Required bool     `json:"required"`
	Secret   bool     `json:"secret"`
	Options  []string `json:"options"`
}

// AITask is a task the module runs through the core's AI settings (PRD 8.7).
type AITask struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// Replaceable core domains (PRD 9.5).
const (
	DomainTime     = "time"
	DomainTrust    = "trust"
	DomainCalendar = "calendar"
)

var (
	moduleID  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}[a-z0-9]$`)
	slug      = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	ident     = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	scopeRe   = regexp.MustCompile(`^[a-z_]+:[a-z_]+$`)
	viewRe    = regexp.MustCompile(`^views:([a-z][a-z0-9-]*[a-z0-9])\.([a-z_][a-z0-9_]*):read$`)
	eventRe   = regexp.MustCompile(`^[a-z_]+\.([a-z_]+|\*)$`)
	digestRe  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	versionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
)

// forbiddenScopes are scopes a module can't hold: they would let it mint tokens, approve
// its own permissions or read provider keys (PRD 8.7: modules never hold keys).
var forbiddenScopes = map[string]bool{"tokens:write": true, "modules:write": true, "ai:admin": true}

// coreRecords maps an extendable core record to its table in schema core.
var coreRecords = map[string]string{
	"matter": "matters", "contact": "contacts", "user": "users", "invoice": "invoices",
	"payment": "payments", "time_entry": "time_entries", "expense": "expenses", "file": "files",
	"note": "notes", "calendar_event": "calendar_events", "deadline": "deadlines",
	"trust_entry": "trust_ledger", "practice_area": "practice_areas",
}

// CoreTable returns the schema-core table for a record name.
func CoreTable(record string) (string, bool) {
	t, ok := coreRecords[record]
	return t, ok
}

// ParseManifest reads and validates togavsu-module.yaml.
func ParseManifest(src []byte) (Manifest, error) {
	var m Manifest
	if err := decodeYAML(src, &m); err != nil {
		return Manifest{}, invalid("manifest", err.Error())
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func invalid(field, msg string) error { return domain.Invalid(field, msg) }

// Schema is the module's own Postgres schema: mod_ plus the id (PRD 9.9).
func (m Manifest) Schema() string { return SchemaFor(m.ID) }

// SchemaFor returns the schema name for a module id.
func SchemaFor(id string) string { return "mod_" + strings.ReplaceAll(id, "-", "_") }

// Validate applies every install-time manifest check except the migration checks.
func (m Manifest) Validate() error {
	if !moduleID.MatchString(m.ID) {
		return invalid("id", "module id must be a lower-case slug")
	}
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Author) == "" {
		return invalid("name", "name and author are required")
	}
	if !versionRe.MatchString(m.Version) {
		return invalid("version", "version is required; quote it in the manifest")
	}
	if m.Container.Image == "" || !digestRe.MatchString(m.Container.Digest) {
		return invalid("container", "container image and sha256 digest are required")
	}
	if m.Container.Port <= zero || m.Container.Port > limits().MaxPort {
		return invalid("container.port", "container port is required")
	}
	seen := map[string]bool{}
	for i, p := range m.Permissions {
		f := fmt.Sprintf("permissions[%d]", i)
		if !scopeRe.MatchString(p.Scope) && !viewRe.MatchString(p.Scope) {
			return invalid(f, "unknown scope format "+p.Scope)
		}
		if forbiddenScopes[p.Scope] {
			return invalid(f, "a module cannot request "+p.Scope)
		}
		if seen[p.Scope] {
			return invalid(f, "duplicate scope "+p.Scope)
		}
		seen[p.Scope] = true
	}
	if err := m.validatePages(); err != nil {
		return err
	}
	if _, err := m.DashboardWidgets(); err != nil {
		return err
	}
	if err := m.validateHelp(); err != nil {
		return err
	}
	for i, e := range m.Events {
		if !eventRe.MatchString(e) {
			return invalid(fmt.Sprintf("events[%d]", i), "event subscriptions look like matter.created or matter.*")
		}
	}
	if len(m.Events) > 0 && !seen["events:read"] {
		return invalid("events", "event subscriptions need the events:read permission")
	}
	switch m.Replaces {
	case "", DomainTime, DomainCalendar:
	case DomainTrust:
		// PRD 9.5: the below-zero flag is part of the trust contract.
		if !contains(m.Emits, "trust.flag_raised") {
			return invalid("emits", "a trust replacement must raise trust.flag_raised")
		}
	default:
		return invalid("replaces", "replaces is time, trust or calendar")
	}
	if err := m.validateData(); err != nil {
		return err
	}
	keys := map[string]bool{}
	for i, s := range m.Settings {
		if !ident.MatchString(s.Key) || keys[s.Key] {
			return invalid(fmt.Sprintf("settings[%d]", i), "settings keys are unique lower-case names")
		}
		keys[s.Key] = true
		switch s.Type {
		case "string", "int", "bool", "enum":
		default:
			return invalid(fmt.Sprintf("settings[%d].type", i), "type is string, int, bool or enum")
		}
		if s.Type == "enum" && len(s.Options) == 0 {
			return invalid(fmt.Sprintf("settings[%d].options", i), "an enum needs options")
		}
	}
	tasks := map[string]bool{}
	for i, t := range m.AITasks {
		if !slug.MatchString(t.ID) || tasks[t.ID] {
			return invalid(fmt.Sprintf("ai_tasks[%d]", i), "AI task ids are unique slugs")
		}
		tasks[t.ID] = true
	}
	if len(m.AITasks) > 0 && !seen["ai:use"] {
		return invalid("ai_tasks", "AI tasks need the ai:use permission")
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// modulePath checks a path the module serves under /m/{id}/.
func modulePath(field, p string) error {
	u, err := url.Parse(p)
	if err != nil || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || u.IsAbs() || u.Host != "" || strings.Contains(p, "..") {
		return invalid(field, "a module page is a path such as /tab")
	}
	return nil
}

func (m Manifest) validatePages() error {
	ids := map[string]bool{}
	for i, t := range m.Tabs {
		f := fmt.Sprintf("tabs[%d]", i)
		if !slug.MatchString(t.ID) || ids[t.ID] || strings.TrimSpace(t.Title) == "" {
			return invalid(f, "tabs need a unique id and a title")
		}
		ids[t.ID] = true
		if t.Placement != "home" && t.Placement != "matter" {
			return invalid(f+".placement", "placement is home or matter")
		}
		if err := modulePath(f+".path", t.Path); err != nil {
			return err
		}
	}
	for i, it := range m.MatterItems {
		f := fmt.Sprintf("matter_items[%d]", i)
		if !slug.MatchString(it.ID) || (it.Kind != "panel" && it.Kind != "action") || strings.TrimSpace(it.Title) == "" {
			return invalid(f, "matter items need an id, a kind (panel or action) and a title")
		}
		if err := modulePath(f+".path", it.Path); err != nil {
			return err
		}
	}
	for name, list := range map[string][]Link{"sidebar": m.Sidebar, "quick_create": m.QuickCreate, "search_scopes": m.Search} {
		for i, l := range list {
			f := fmt.Sprintf("%s[%d]", name, i)
			if !slug.MatchString(l.ID) || strings.TrimSpace(l.Title) == "" {
				return invalid(f, "entries need an id and a title")
			}
			if err := modulePath(f+".path", l.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

// Tab returns the tab with id.
func (m Manifest) Tab(id string) (Tab, bool) {
	for _, t := range m.Tabs {
		if t.ID == id {
			return t, true
		}
	}
	return Tab{}, false
}

// TabPage is the core page that frames a module tab.
func TabPage(module, tab string) string { return "/modules/" + module + "/tabs/" + tab }

// DashboardWidgets converts the manifest's widgets for the dashboard catalog. A widget
// with no click-through target, or a target tab the manifest doesn't declare, refuses the
// install (PRD 8.6, AC-011).
func (m Manifest) DashboardWidgets() ([]dashboard.ManifestWidget, error) {
	out := make([]dashboard.ManifestWidget, 0, len(m.Widgets))
	for i, w := range m.Widgets {
		f := fmt.Sprintf("widgets[%d]", i)
		if err := modulePath(f+".path", w.Path); err != nil {
			return nil, err
		}
		target := ""
		if w.Target != nil {
			switch {
			case w.Target.Tab != "" && w.Target.Page != "":
				return nil, invalid(f+".target", "a target is one tab or one page")
			case w.Target.Tab != "":
				if _, ok := m.Tab(w.Target.Tab); !ok {
					return nil, invalid(f+".target", "widget "+w.ID+" targets tab "+w.Target.Tab+", which the manifest doesn't declare")
				}
				target = TabPage(m.ID, w.Target.Tab)
			default:
				target = w.Target.Page
			}
			for _, p := range w.Target.Params {
				if !ident.MatchString(p) {
					return nil, invalid(f+".target.params", "filter parameters are lower-case names")
				}
			}
		}
		for _, s := range w.Sizes {
			if s.W <= zero || s.H <= zero || s.W > dashboard.GridColumns {
				return nil, invalid(f+".sizes", "a size is inside the grid")
			}
		}
		out = append(out, dashboard.ManifestWidget{ID: w.ID, Title: w.Title, Target: target, Roles: w.Roles, W: w.Size.W, H: w.Size.H})
	}
	if err := dashboard.ValidateManifestWidgets(m.ID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Widget returns the widget with id.
func (m Manifest) Widget(id string) (Widget, bool) {
	for _, w := range m.Widgets {
		if w.ID == id {
			return w, true
		}
	}
	return Widget{}, false
}

func (m Manifest) validateHelp() error {
	ids := map[string]bool{}
	for i, p := range m.Help.Pages {
		f := fmt.Sprintf("help.pages[%d]", i)
		if !slug.MatchString(p.ID) || ids[p.ID] || strings.TrimSpace(p.Title) == "" {
			return invalid(f, "help pages need a unique id and a title")
		}
		ids[p.ID] = true
	}
	return nil
}

// HelpHeading is the heading the module's guide files under; default its name.
func (m Manifest) HelpHeading() string {
	if strings.TrimSpace(m.Help.Heading) != "" {
		return m.Help.Heading
	}
	return m.Name
}

func (m Manifest) validateData() error {
	names := map[string]bool{}
	check := func(f, name, record, key string, cols []Column) error {
		if !ident.MatchString(name) || names[name] {
			return invalid(f+".name", "include and view names are unique lower-case names")
		}
		names[name] = true
		if _, ok := coreRecords[record]; !ok {
			return invalid(f+".record", "unknown core record "+record)
		}
		if !ident.MatchString(key) {
			return invalid(f+".key", "the key column is required")
		}
		if len(cols) == 0 {
			return invalid(f+".columns", "list the readable columns")
		}
		seen := map[string]bool{}
		for _, c := range cols {
			if !ident.MatchString(c.Name) || seen[c.Name] {
				return invalid(f+".columns", "columns are unique lower-case names")
			}
			seen[c.Name] = true
		}
		return nil
	}
	for i, e := range m.Data.Extends {
		f := fmt.Sprintf("data.extends[%d]", i)
		if !ident.MatchString(e.Table) {
			return invalid(f+".table", "the extension table is required")
		}
		if err := check(f, e.Name, e.Record, e.Key, e.Columns); err != nil {
			return err
		}
	}
	for i, v := range m.Data.Views {
		if err := check(fmt.Sprintf("data.views[%d]", i), v.Name, v.Record, v.Key, v.Columns); err != nil {
			return err
		}
	}
	for i, c := range m.Data.CustomFields {
		f := fmt.Sprintf("data.custom_fields[%d]", i)
		if _, ok := coreRecords[c.Record]; !ok || !ident.MatchString(c.Key) || strings.TrimSpace(c.Label) == "" {
			return invalid(f, "custom fields need a core record, a key and a label")
		}
	}
	return nil
}

// Scopes returns the requested scopes sorted.
func (m Manifest) Scopes() []string {
	out := make([]string, 0, len(m.Permissions))
	for _, p := range m.Permissions {
		out = append(out, p.Scope)
	}
	sort.Strings(out)
	return out
}

// Readable is an include or published view: what include= and the views API read.
type Readable struct {
	Module  string
	Name    string
	Record  string
	Object  string // table or view name in the module schema
	Key     string
	Columns []Column
	View    bool
}

// Readables lists the module's extension tables and published views.
func (m Manifest) Readables() []Readable {
	var out []Readable
	for _, e := range m.Data.Extends {
		out = append(out, Readable{Module: m.ID, Name: e.Name, Record: e.Record, Object: e.Table, Key: e.Key, Columns: e.Columns})
	}
	for _, v := range m.Data.Views {
		out = append(out, Readable{Module: m.ID, Name: v.Name, Record: v.Record, Object: v.Name, Key: v.Key, Columns: v.Columns, View: true})
	}
	return out
}

// ViewScope is the scope another module needs to read a published view.
func ViewScope(module, view string) string { return "views:" + module + "." + view + ":read" }
