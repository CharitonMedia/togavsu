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
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Version         string             `json:"version"`
	Author          string             `json:"author"`
	Homepage        string             `json:"homepage"`
	License         string             `json:"license"`
	Price           ManifestPrice      `json:"price"`
	Container       Container          `json:"container"`
	Permissions     []Permission       `json:"permissions"`
	Widgets         []Widget           `json:"widgets"`
	Help            HelpGuide          `json:"help"`
	Tabs            []Tab              `json:"tabs"`
	MatterItems     []MatterItem       `json:"matter_items"`
	Sidebar         []Link             `json:"sidebar"`
	QuickCreate     []Link             `json:"quick_create"`
	Search          []Link             `json:"search_scopes"`
	Notifications   []NotificationKind `json:"notifications"`
	InboundWebhooks []InboundWebhook   `json:"inbound_webhooks"`
	// ClientHome names one of this module's client tabs as the portal landing tab (CORE-10).
	ClientHome string `json:"client_home"`
	// DeviceConnect is the module path the device-connect page redirects to with #grant= (CORE-28).
	DeviceConnect string `json:"device_connect"`
	// PaymentProcessor registers this module as a payment processor (CORE-23).
	PaymentProcessor *PaymentProcessor `json:"payment_processor"`
	Events           []string          `json:"events"`
	Emits            []string          `json:"emits"`
	Data             Data              `json:"data"`
	Replaces         string            `json:"replaces"`
	Settings         []SettingSpec     `json:"settings"`
	AITasks          []AITask          `json:"ai_tasks"`
	// Provides lists contracts this module offers (CORE-31).
	Provides []ContractProvide `json:"provides"`
	// Consumes lists contracts this module needs a provider for (CORE-31).
	Consumes []ContractConsume `json:"consumes"`
	// PublicPages are paths served at /p/{id}/... with no sign-in (CORE-32).
	PublicPages []PublicPage `json:"public_pages"`
	// PublicOperations limits service calls from a public-page step token (CORE-32).
	PublicOperations []string `json:"public_operations"`
	// StaffBanner reserves a slot above every staff page (CORE-34).
	StaffBanner *StaffBanner `json:"staff_banner"`
}

// ContractProvide is one named, versioned contract this module provides (CORE-31).
type ContractProvide struct {
	Contract   string              `json:"contract"`
	Many       bool                `json:"many"`
	Operations []ContractOperation `json:"operations"`
}

// ContractOperation is one operation on a provided contract (CORE-31).
type ContractOperation struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ContractConsume is one contract this module consumes (CORE-31).
type ContractConsume struct {
	Contract string `json:"contract"`
}

// PublicPage is one path served under /p/{module_id}/ (CORE-32).
type PublicPage struct {
	Path string `json:"path"`
}

// StaffBanner is the module's staff banner page (CORE-34).
type StaffBanner struct {
	Path   string `json:"path"`
	Height int    `json:"height"`
}

// PaymentProcessor is manifest payment_processor: destinations the module may charge (CORE-23).
type PaymentProcessor struct {
	Destinations []string `json:"destinations"`
}

// NotificationKind is one module notification kind (CORE-03). The core names it
// {module_id}.{kind} in preferences and createNotification.
type NotificationKind struct {
	Kind            string   `json:"kind"`
	Label           string   `json:"label"`
	DefaultChannels []string `json:"default_channels"`
}

// InboundWebhook is one provider webhook at POST /hooks/{module_id}/{id} (CORE-07).
type InboundWebhook struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Container is the image the supervisor runs, pinned by digest.
type Container struct {
	Image  string `json:"image"`
	Digest string `json:"digest"`
	Port   int    `json:"port"`
	// Events is the path on the module that receives signed event webhooks.
	Events string `json:"events"`
	// SystemMail is the module path that receives core system mail (CORE-05).
	SystemMail string `json:"system_mail"`
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

// Tab is a home, matter or client tab the module serves (CORE-10 adds client).
type Tab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Placement is "home", "matter" or "client".
	Placement string `json:"placement"`
	Path      string `json:"path"`
	// Params lists query parameters the core passes through to the module page.
	Params []string `json:"params"`
	// Nav keeps a client tab out of portal navigation when false (default true).
	Nav *bool `json:"nav"`
}

// TabNav reports whether the tab appears in client portal navigation (default true).
func (t Tab) TabNav() bool { return t.Nav == nil || *t.Nav }

// MatterItem is a matter-screen panel or action.
type MatterItem struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

// Link is a sidebar entry, quick-create item or search scope.
// Section, Subgroup, Icon, Order and Badge are CORE-08 sidebar placement fields;
// they apply only to sidebar entries (validated in Validate).
type Link struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Section  string `json:"section,omitempty"`
	Subgroup string `json:"subgroup,omitempty"`
	Icon     string `json:"icon,omitempty"`
	Order    *int   `json:"order,omitempty"`
	Badge    *Badge `json:"badge,omitempty"`
}

// Badge is a sidebar count the core fetches from the module (CORE-08).
type Badge struct {
	Path string `json:"path"`
	Tone string `json:"tone"`
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
	// notifyKind is CORE-03: short kind in the manifest before the module id is prefixed.
	notifyKind = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
)

// forbiddenScopes are scopes a module can't hold: they would let it mint tokens, approve
// its own permissions or read provider keys (PRD 8.7: modules never hold keys).
var forbiddenScopes = map[string]bool{"tokens:write": true, "modules:write": true, "ai:admin": true}

// reservedContractOperations collide with fixed CORE-31 routes under
// /api/v1/modules/contracts/{contract}/… (events push, providers list).
var reservedContractOperations = map[string]bool{"events": true, "providers": true}

// coreRecords maps an extendable core record to its table in schema core.
var coreRecords = map[string]string{
	"matter": "matters", "contact": "contacts", "user": "users", "invoice": "invoices",
	"payment": "payments", "time_entry": "time_entries", "expense": "expenses", "file": "files",
	"note": "notes", "calendar_event": "calendar_events", "deadline": "deadlines",
	"trust_entry": "trust_ledger", "practice_area": "practice_areas",
	// CORE-22: modules may link to and extend these. payment_refunds is created by the
	// module-core migration that lands with this record (CORE-24 fills the refund API).
	"fee_agreement": "fee_agreements", "payment_refund": "payment_refunds", "trust_account": "trust_accounts",
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
	var first error
	m.validate(func(err error) bool {
		first = err
		return false
	})
	return first
}

// ValidateAll runs Validate's checks and returns every refusal, in Validate's order; the
// first is Validate's error. Each check after a refusal still runs; the widget checks
// report their first refusal only.
func (m Manifest) ValidateAll() []error {
	var out []error
	m.validate(func(err error) bool {
		out = append(out, err)
		return true
	})
	return out
}

// validate hands each refusal to report and stops when report returns false.
func (m Manifest) validate(report func(error) bool) {
	ok := func(err error) bool { return err == nil || report(err) }
	checks := []func() bool{
		func() bool {
			return moduleID.MatchString(m.ID) || ok(invalid("id", "module id must be a lower-case slug"))
		},
		func() bool {
			return (strings.TrimSpace(m.Name) != "" && strings.TrimSpace(m.Author) != "") || ok(invalid("name", "name and author are required"))
		},
		func() bool {
			return versionRe.MatchString(m.Version) || ok(invalid("version", "version is required; quote it in the manifest"))
		},
		func() bool {
			return (m.Container.Image != "" && digestRe.MatchString(m.Container.Digest)) || ok(invalid("container", "container image and sha256 digest are required"))
		},
		func() bool {
			return (m.Container.Port > zero && m.Container.Port <= limits().MaxPort) || ok(invalid("container.port", "container port is required"))
		},
		func() bool {
			return m.Price.validate() == nil || ok(m.Price.validate())
		},
	}
	for _, c := range checks {
		if !c() {
			return
		}
	}
	seen := map[string]bool{}
	for i, p := range m.Permissions {
		f := fmt.Sprintf("permissions[%d]", i)
		switch {
		case !scopeRe.MatchString(p.Scope) && !viewRe.MatchString(p.Scope):
			if !ok(invalid(f, "unknown scope format "+p.Scope)) {
				return
			}
		case forbiddenScopes[p.Scope]:
			if !ok(invalid(f, "a module cannot request "+p.Scope)) {
				return
			}
		case seen[p.Scope]:
			if !ok(invalid(f, "duplicate scope "+p.Scope)) {
				return
			}
		default:
			seen[p.Scope] = true
		}
	}
	if !m.validatePages(ok) {
		return
	}
	if _, err := m.DashboardWidgets(); !ok(err) {
		return
	}
	if !m.validateHelp(ok) {
		return
	}
	for i, e := range m.Events {
		if !eventRe.MatchString(e) && !ok(invalid(fmt.Sprintf("events[%d]", i), "event subscriptions look like matter.created or matter.*")) {
			return
		}
	}
	if len(m.Events) > 0 && !seen["events:read"] && !ok(invalid("events", "event subscriptions need the events:read permission")) {
		return
	}
	switch m.Replaces {
	case "", DomainTime, DomainCalendar:
	case DomainTrust:
		// PRD 9.5: the below-zero flag is part of the trust contract.
		if !contains(m.Emits, "trust.flag_raised") && !ok(invalid("emits", "a trust replacement must raise trust.flag_raised")) {
			return
		}
	default:
		if !ok(invalid("replaces", "replaces is time, trust or calendar")) {
			return
		}
	}
	if !m.validateData(ok) {
		return
	}
	keys := map[string]bool{}
	for i, s := range m.Settings {
		if !ident.MatchString(s.Key) || keys[s.Key] {
			if !ok(invalid(fmt.Sprintf("settings[%d]", i), "settings keys are unique lower-case names")) {
				return
			}
			continue
		}
		keys[s.Key] = true
		switch s.Type {
		case "string", "int", "bool", "enum":
		default:
			if !ok(invalid(fmt.Sprintf("settings[%d].type", i), "type is string, int, bool or enum")) {
				return
			}
			continue
		}
		if s.Type == "enum" && len(s.Options) == 0 && !ok(invalid(fmt.Sprintf("settings[%d].options", i), "an enum needs options")) {
			return
		}
	}
	tasks := map[string]bool{}
	for i, t := range m.AITasks {
		if !slug.MatchString(t.ID) || tasks[t.ID] {
			if !ok(invalid(fmt.Sprintf("ai_tasks[%d]", i), "AI task ids are unique slugs")) {
				return
			}
			continue
		}
		tasks[t.ID] = true
	}
	if len(m.AITasks) > 0 && !seen["ai:use"] {
		ok(invalid("ai_tasks", "AI tasks need the ai:use permission"))
	}
	if path := strings.TrimSpace(m.Container.SystemMail); path != "" {
		if !ok(modulePath("container.system_mail", path)) {
			return
		}
		if !seen["mail:system"] && !ok(invalid("container.system_mail", "system mail needs the mail:system permission")) {
			return
		}
	}
	if path := strings.TrimSpace(m.DeviceConnect); path != "" {
		if !ok(modulePath("device_connect", path)) {
			return
		}
	}
	if !m.validateNotifications(ok) {
		return
	}
	if !m.validateInboundWebhooks(ok) {
		return
	}
	if !m.validatePaymentProcessor(ok) {
		return
	}
	if !m.validateContracts(ok) {
		return
	}
	if !m.validatePublicPages(ok) {
		return
	}
	if !m.validateStaffBanner(ok) {
		return
	}
}

// contractName is a named, versioned contract id such as phone.provider.v1.
var contractName = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$`)

func (m Manifest) validateContracts(ok func(error) bool) bool {
	seenProvide := map[string]bool{}
	hasCall, hasProvide := false, false
	for _, p := range m.Permissions {
		if p.Scope == "contracts:call" {
			hasCall = true
		}
		if p.Scope == "contracts:provide" {
			hasProvide = true
		}
	}
	for i, c := range m.Provides {
		f := fmt.Sprintf("provides[%d]", i)
		if !contractName.MatchString(c.Contract) || seenProvide[c.Contract] {
			if !ok(invalid(f+".contract", "provides contracts are unique names like phone.provider.v1")) {
				return false
			}
			continue
		}
		seenProvide[c.Contract] = true
		if len(c.Operations) == 0 {
			if !ok(invalid(f+".operations", "a provided contract needs at least one operation")) {
				return false
			}
			continue
		}
		ops := map[string]bool{}
		for j, op := range c.Operations {
			of := fmt.Sprintf("%s.operations[%d]", f, j)
			if reservedContractOperations[op.Name] {
				if !ok(invalid(of+".name", "operation names events and providers are reserved for core contract routes")) {
					return false
				}
				continue
			}
			if !slug.MatchString(op.Name) || ops[op.Name] {
				if !ok(invalid(of+".name", "operation names are unique slugs")) {
					return false
				}
				continue
			}
			ops[op.Name] = true
			if !ok(modulePath(of+".path", op.Path)) {
				return false
			}
		}
	}
	if len(m.Provides) > 0 && !hasProvide {
		if !ok(invalid("provides", "providing a contract needs the contracts:provide permission")) {
			return false
		}
	}
	seenConsume := map[string]bool{}
	for i, c := range m.Consumes {
		f := fmt.Sprintf("consumes[%d]", i)
		if !contractName.MatchString(c.Contract) || seenConsume[c.Contract] {
			if !ok(invalid(f+".contract", "consumes contracts are unique names like phone.provider.v1")) {
				return false
			}
			continue
		}
		seenConsume[c.Contract] = true
	}
	if len(m.Consumes) > 0 && !hasCall {
		if !ok(invalid("consumes", "consuming a contract needs the contracts:call permission")) {
			return false
		}
	}
	return true
}

func (m Manifest) validatePublicPages(ok func(error) bool) bool {
	hasServe := false
	for _, p := range m.Permissions {
		if p.Scope == "public_pages:serve" {
			hasServe = true
			break
		}
	}
	seen := map[string]bool{}
	for i, pg := range m.PublicPages {
		f := fmt.Sprintf("public_pages[%d]", i)
		if !ok(modulePath(f+".path", pg.Path)) {
			return false
		}
		if seen[pg.Path] {
			if !ok(invalid(f+".path", "public page paths are unique")) {
				return false
			}
			continue
		}
		seen[pg.Path] = true
	}
	if len(m.PublicPages) > 0 && !hasServe {
		if !ok(invalid("public_pages", "public pages need the public_pages:serve permission")) {
			return false
		}
	}
	opID := regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	ops := map[string]bool{}
	for i, op := range m.PublicOperations {
		f := fmt.Sprintf("public_operations[%d]", i)
		if !opID.MatchString(op) {
			if !ok(invalid(f, "public_operations names an OpenAPI operationId")) {
				return false
			}
			continue
		}
		if ops[op] {
			if !ok(invalid(f, "duplicate public operation "+op)) {
				return false
			}
			continue
		}
		ops[op] = true
	}
	if len(m.PublicOperations) > 0 && len(m.PublicPages) == 0 {
		if !ok(invalid("public_operations", "public_operations requires public_pages")) {
			return false
		}
	}
	return true
}

func (m Manifest) validateStaffBanner(ok func(error) bool) bool {
	if m.StaffBanner == nil {
		return true
	}
	hasShow := false
	for _, p := range m.Permissions {
		if p.Scope == "banner:show" {
			hasShow = true
			break
		}
	}
	if !ok(modulePath("staff_banner.path", m.StaffBanner.Path)) {
		return false
	}
	if m.StaffBanner.Height <= 0 || m.StaffBanner.Height > 400 {
		if !ok(invalid("staff_banner.height", "banner height is 1 to 400 pixels")) {
			return false
		}
	}
	if !hasShow {
		return ok(invalid("staff_banner", "a staff banner needs the banner:show permission"))
	}
	return true
}

// Provide looks up a provided contract by name.
func (m Manifest) Provide(contract string) (ContractProvide, bool) {
	for _, c := range m.Provides {
		if c.Contract == contract {
			return c, true
		}
	}
	return ContractProvide{}, false
}

// ProvideOperation looks up one operation on a provided contract.
func (m Manifest) ProvideOperation(contract, operation string) (ContractOperation, bool) {
	c, ok := m.Provide(contract)
	if !ok {
		return ContractOperation{}, false
	}
	for _, op := range c.Operations {
		if op.Name == operation {
			return op, true
		}
	}
	return ContractOperation{}, false
}

// ConsumesContract reports whether the manifest consumes contract.
func (m Manifest) ConsumesContract(contract string) bool {
	for _, c := range m.Consumes {
		if c.Contract == contract {
			return true
		}
	}
	return false
}

// PublicPagePath reports whether path is a declared public page (exact or under a declared prefix).
func (m Manifest) PublicPagePath(path string) bool {
	path = "/" + strings.TrimPrefix(path, "/")
	for _, pg := range m.PublicPages {
		base := "/" + strings.TrimPrefix(pg.Path, "/")
		if path == base || strings.HasPrefix(path, strings.TrimSuffix(base, "/")+"/") {
			return true
		}
	}
	return false
}

// AllowsPublicOperation reports whether operationId is listed under public_operations.
func (m Manifest) AllowsPublicOperation(operationID string) bool {
	for _, op := range m.PublicOperations {
		if op == operationID {
			return true
		}
	}
	return false
}

func (m Manifest) validatePaymentProcessor(ok func(error) bool) bool {
	if m.PaymentProcessor == nil {
		return true
	}
	if len(m.PaymentProcessor.Destinations) == 0 {
		return ok(invalid("payment_processor.destinations", "at least one of operating or trust is required"))
	}
	seen := map[string]bool{}
	for i, d := range m.PaymentProcessor.Destinations {
		f := fmt.Sprintf("payment_processor.destinations[%d]", i)
		if d != "operating" && d != "trust" {
			if !ok(invalid(f, "must be operating or trust")) {
				return false
			}
			continue
		}
		if seen[d] {
			if !ok(invalid(f, "duplicate destination "+d)) {
				return false
			}
			continue
		}
		seen[d] = true
	}
	if !Subscribed(m.Events, "payment.processor_authorized") {
		return ok(invalid("payment_processor", "a payment processor must subscribe to payment.processor_authorized"))
	}
	needWrite := true
	needTrust := seen["trust"]
	for _, p := range m.Permissions {
		if p.Scope == "payments:write" {
			needWrite = false
		}
		if p.Scope == "trust:write" {
			needTrust = false
		}
	}
	if needWrite {
		return ok(invalid("payment_processor", "a payment processor needs the payments:write permission"))
	}
	if needTrust {
		return ok(invalid("payment_processor", "a trust destination needs the trust:write permission"))
	}
	return true
}

func (m Manifest) validateNotifications(ok func(error) bool) bool {
	seen := map[string]bool{}
	for i, n := range m.Notifications {
		f := fmt.Sprintf("notifications[%d]", i)
		if !notifyKind.MatchString(n.Kind) || seen[n.Kind] {
			if !ok(invalid(f+".kind", "kind is a unique lower-case name up to 48 characters")) {
				return false
			}
			continue
		}
		seen[n.Kind] = true
		if strings.TrimSpace(n.Label) == "" {
			if !ok(invalid(f+".label", "label is required")) {
				return false
			}
		}
		if len(n.DefaultChannels) == 0 {
			if !ok(invalid(f+".default_channels", "default_channels is a non-empty subset of in_app and email")) {
				return false
			}
			continue
		}
		ch := map[string]bool{}
		for j, c := range n.DefaultChannels {
			if c != "in_app" && c != "email" || ch[c] {
				if !ok(invalid(fmt.Sprintf("%s.default_channels[%d]", f, j), "default_channels is a non-empty subset of in_app and email")) {
					return false
				}
				break
			}
			ch[c] = true
		}
	}
	return true
}

// FullNotificationKind is the preference and createNotification kind for a short manifest kind.
func FullNotificationKind(moduleID, kind string) string {
	return moduleID + "." + kind
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

func (m Manifest) validatePages(ok func(error) bool) bool {
	ids := map[string]bool{}
	for i, t := range m.Tabs {
		f := fmt.Sprintf("tabs[%d]", i)
		if !slug.MatchString(t.ID) || ids[t.ID] || strings.TrimSpace(t.Title) == "" {
			if !ok(invalid(f, "tabs need a unique id and a title")) {
				return false
			}
			continue
		}
		ids[t.ID] = true
		if t.Placement != "home" && t.Placement != "matter" && t.Placement != "client" {
			if !ok(invalid(f+".placement", "placement is home, matter or client")) {
				return false
			}
			continue
		}
		if !ok(modulePath(f+".path", t.Path)) {
			return false
		}
		for j, p := range t.Params {
			if !ident.MatchString(p) {
				if !ok(invalid(fmt.Sprintf("%s.params[%d]", f, j), "params are lower-case names")) {
					return false
				}
			}
		}
	}
	if home := strings.TrimSpace(m.ClientHome); home != "" {
		tab, found := m.Tab(home)
		if !found || tab.Placement != "client" {
			if !ok(invalid("client_home", "client_home names one of this module's client tabs")) {
				return false
			}
		}
	}
	for i, it := range m.MatterItems {
		f := fmt.Sprintf("matter_items[%d]", i)
		if !slug.MatchString(it.ID) || (it.Kind != "panel" && it.Kind != "action") || strings.TrimSpace(it.Title) == "" {
			if !ok(invalid(f, "matter items need an id, a kind (panel or action) and a title")) {
				return false
			}
			continue
		}
		if !ok(modulePath(f+".path", it.Path)) {
			return false
		}
	}
	homePaths := map[string]bool{}
	for _, t := range m.Tabs {
		if t.Placement == "home" {
			homePaths[t.Path] = true
		}
	}
	for _, list := range []struct {
		name  string
		links []Link
	}{{"sidebar", m.Sidebar}, {"quick_create", m.QuickCreate}, {"search_scopes", m.Search}} {
		for i, l := range list.links {
			f := fmt.Sprintf("%s[%d]", list.name, i)
			if !slug.MatchString(l.ID) || strings.TrimSpace(l.Title) == "" {
				if !ok(invalid(f, "entries need an id and a title")) {
					return false
				}
				continue
			}
			if !ok(modulePath(f+".path", l.Path)) {
				return false
			}
			if list.name != "sidebar" {
				if l.Section != "" || l.Subgroup != "" || l.Icon != "" || l.Order != nil || l.Badge != nil {
					if !ok(invalid(f, "section, subgroup, icon, order and badge are sidebar fields")) {
						return false
					}
				}
				continue
			}
			if !homePaths[l.Path] {
				if !ok(invalid(f+".path", "a sidebar path must match a home tab")) {
					return false
				}
			}
			if l.Section != "" && !sidebarSection(l.Section) {
				if !ok(invalid(f+".section", "section is a PRD 8.9 sidebar section")) {
					return false
				}
			}
			if l.Icon != "" && !sidebarIcon(l.Icon) {
				if !ok(invalid(f+".icon", "icon is not in the core icon set")) {
					return false
				}
			}
			if l.Badge != nil {
				if !ok(modulePath(f+".badge.path", l.Badge.Path)) {
					return false
				}
				if l.Badge.Tone != "normal" && l.Badge.Tone != "warning" {
					if !ok(invalid(f+".badge.tone", "tone is normal or warning")) {
						return false
					}
				}
			}
		}
	}
	return true
}

// sidebarSection is a PRD 8.9 section id (CORE-08).
func sidebarSection(s string) bool {
	switch s {
	case "top", "communications", "documents", "litigation", "money", "intake", "activity", "system":
		return true
	}
	return false
}

// sidebarIcon is a glyph in the core icon set (catalog.json plus puzzle, mail, message, file).
func sidebarIcon(name string) bool {
	return sidebarIcons[name]
}

var sidebarIcons = func() map[string]bool {
	out := map[string]bool{
		"puzzle": true, "mail": true, "message": true, "file": true,
	}
	for _, n := range dashboard.CoreNav() {
		if n.Icon != "" {
			out[n.Icon] = true
		}
	}
	return out
}()

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

// MatterItem returns the matter-screen panel or action with id.
func (m Manifest) MatterItem(id string) (MatterItem, bool) {
	for _, it := range m.MatterItems {
		if it.ID == id {
			return it, true
		}
	}
	return MatterItem{}, false
}

// MatterItemPage is the core page that frames a module's matter-screen panel or action.
func MatterItemPage(module, item string) string {
	return "/modules/" + module + "/matter-items/" + item
}

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

func (m Manifest) validateHelp(ok func(error) bool) bool {
	ids := map[string]bool{}
	for i, p := range m.Help.Pages {
		f := fmt.Sprintf("help.pages[%d]", i)
		if !slug.MatchString(p.ID) || ids[p.ID] || strings.TrimSpace(p.Title) == "" {
			if !ok(invalid(f, "help pages need a unique id and a title")) {
				return false
			}
			continue
		}
		ids[p.ID] = true
	}
	return true
}

// HelpHeading is the heading the module's guide files under; default its name.
func (m Manifest) HelpHeading() string {
	if strings.TrimSpace(m.Help.Heading) != "" {
		return m.Help.Heading
	}
	return m.Name
}

func (m Manifest) validateData(ok func(error) bool) bool {
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
			if !ok(invalid(f+".table", "the extension table is required")) {
				return false
			}
			continue
		}
		if !ok(check(f, e.Name, e.Record, e.Key, e.Columns)) {
			return false
		}
	}
	for i, v := range m.Data.Views {
		if !ok(check(fmt.Sprintf("data.views[%d]", i), v.Name, v.Record, v.Key, v.Columns)) {
			return false
		}
	}
	for i, c := range m.Data.CustomFields {
		f := fmt.Sprintf("data.custom_fields[%d]", i)
		if _, known := coreRecords[c.Record]; !known || !ident.MatchString(c.Key) || strings.TrimSpace(c.Label) == "" {
			if !ok(invalid(f, "custom fields need a core record, a key and a label")) {
				return false
			}
		}
	}
	return true
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

func (m Manifest) validateInboundWebhooks(ok func(error) bool) bool {
	seen := map[string]bool{}
	for i, h := range m.InboundWebhooks {
		f := fmt.Sprintf("inbound_webhooks[%d]", i)
		if !slug.MatchString(h.ID) || seen[h.ID] {
			if !ok(invalid(f+".id", "inbound webhook ids are unique slugs")) {
				return false
			}
			continue
		}
		seen[h.ID] = true
		if !ok(modulePath(f+".path", h.Path)) {
			return false
		}
	}
	return true
}

// InboundWebhook returns the declared inbound webhook with id.
func (m Manifest) InboundWebhook(id string) (InboundWebhook, bool) {
	for _, h := range m.InboundWebhooks {
		if h.ID == id {
			return h, true
		}
	}
	return InboundWebhook{}, false
}
