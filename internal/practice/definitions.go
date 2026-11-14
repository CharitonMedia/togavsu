package practice

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Marker definitions, module markers and party roles (PRD 5, 8.7 Practice tab, 9.4).

// DefaultMarkerColor is shown for a firm marker created before markers had a color.
const DefaultMarkerColor = "#6B7280"

var (
	markerLabelRule = regexp.MustCompile(`^[^<>\r\n\t]{1,40}$`)
	markerColor     = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	targetLength    = regexp.MustCompile(`^.{1,128}$`)
	markerTarget    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}(/[a-z0-9][a-z0-9-]{0,63})*$`)
	moduleIDRule    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

// SetModuleCheck gives the service the module runtime's installed-and-enabled check for
// module markers. Until it is set every module principal is refused.
func (s *Service) SetModuleCheck(fn func(ctx context.Context, module string) (bool, error)) {
	s.moduleOn = fn
}

// MarkerWrite patches or creates a marker definition. Nil leaves a field as it is.
type MarkerWrite struct {
	Label  *string
	Color  *string
	Active *bool
	Target *string
}

func markerLabel(v string) (string, error) {
	if !markerLabelRule.MatchString(v) {
		return "", domain.Invalid("label", "label is 1 to 40 characters of plain text")
	}
	return v, nil
}

func checkColor(v string) error {
	if !markerColor.MatchString(v) {
		return domain.Invalid("color", "color is a six-digit hex value such as #1F6FEB")
	}
	return nil
}

// idTime is the creation instant carried in a UUIDv7, for rows stored before they kept one.
func idTime(id domain.ID) time.Time {
	at, err := id.Time()
	if err != nil {
		return time.Time{}
	}
	return at.UTC()
}

func (row MarkerDef) shown() MarkerDef {
	if row.Color == "" {
		row.Color = DefaultMarkerColor
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = idTime(row.ID)
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	if row.CreatedBy.IsZero() {
		row.CreatedBy = domain.SystemUserID
	}
	return row
}

// coreMarker reports a label the core gives meaning to (zero fee, pro bono, also asked).
func coreMarker(label string) bool {
	for _, l := range domain.DefaultMarkers() {
		if strings.EqualFold(l, strings.TrimSpace(label)) {
			return true
		}
	}
	return false
}

// labelTaken reports another active marker with label in the same namespace.
func (s *Service) labelTaken(module, label string, except domain.ID) bool {
	for _, row := range s.db.Markers() {
		if row.ID != except && !row.Inactive && row.ModuleID == module && strings.EqualFold(row.Label, label) {
			return true
		}
	}
	return false
}

// ListMarkerDefinitions returns firm and module marker definitions; active filters.
func (s *Service) ListMarkerDefinitions(ctx context.Context, p domain.Principal, active *bool) ([]MarkerDef, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionRead}); err != nil {
		return nil, err
	}
	if err := s.ensureMarkers(ctx); err != nil {
		return nil, err
	}
	var out []MarkerDef
	for _, row := range s.db.Markers() {
		if active == nil || *active == !row.Inactive {
			out = append(out, row.shown())
		}
	}
	return out, nil
}

// GetMarkerDefinition returns one marker definition.
func (s *Service) GetMarkerDefinition(ctx context.Context, p domain.Principal, id domain.ID) (MarkerDef, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionRead}); err != nil {
		return MarkerDef{}, err
	}
	if err := s.ensureMarkers(ctx); err != nil {
		return MarkerDef{}, err
	}
	row, ok := s.db.Marker(id)
	if !ok {
		return MarkerDef{}, domain.NotFound("marker")
	}
	return row.shown(), nil
}

// CreateMarkerDefinition adds a firm marker. Admin with settings:write; a module principal
// is refused here and registers its own markers through CreateModuleMarker.
func (s *Service) CreateMarkerDefinition(ctx context.Context, p domain.Principal, in MarkerWrite) (_ MarkerDef, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if p.Source == domain.SourceModule {
		return MarkerDef{}, domain.Forbidden("a module registers markers in its own namespace")
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return MarkerDef{}, err
	}
	if in.Label == nil {
		return MarkerDef{}, domain.Invalid("label", "label is required")
	}
	label, err := markerLabel(*in.Label)
	if err != nil {
		return MarkerDef{}, err
	}
	color := DefaultMarkerColor
	if in.Color != nil {
		if err := checkColor(*in.Color); err != nil {
			return MarkerDef{}, err
		}
		color = *in.Color
	}
	if err := s.ensureMarkers(ctx); err != nil {
		return MarkerDef{}, err
	}
	inactive := in.Active != nil && !*in.Active
	if !inactive && s.labelTaken("", label, "") {
		return MarkerDef{}, domain.Conflict("an active firm marker already has this label")
	}
	meta, err := s.meta(p)
	if err != nil {
		return MarkerDef{}, err
	}
	row := MarkerDef{ID: meta.ID, Label: label, Color: color, Inactive: inactive, CreatedAt: meta.CreatedAt, CreatedBy: meta.CreatedBy, UpdatedAt: meta.UpdatedAt}
	if err := s.db.PutMarker(ctx, row); err != nil {
		return MarkerDef{}, err
	}
	if err := s.record(ctx, p, []change{{
		Entity: "marker", EntityID: row.ID, Field: "label", New: label, Kind: "create",
		Action: "marker.created", Summary: "Added marker " + label,
	}}); err != nil {
		return MarkerDef{}, err
	}
	return row.shown(), nil
}

// UpdateMarkerDefinition patches a firm marker. A module's marker is changed only by that
// module. The core markers keep their labels, which the core gives meaning to.
func (s *Service) UpdateMarkerDefinition(ctx context.Context, p domain.Principal, id domain.ID, in MarkerWrite) (_ MarkerDef, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if p.Source == domain.SourceModule {
		return MarkerDef{}, domain.Forbidden("a module changes its markers in its own namespace")
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return MarkerDef{}, err
	}
	if err := s.ensureMarkers(ctx); err != nil {
		return MarkerDef{}, err
	}
	prev, ok := s.db.Marker(id)
	if !ok {
		return MarkerDef{}, domain.NotFound("marker")
	}
	if prev.ModuleID != "" {
		return MarkerDef{}, domain.Forbidden("this marker belongs to module " + prev.ModuleID)
	}
	prev = prev.shown()
	next := prev
	if in.Label != nil {
		if next.Label, err = markerLabel(*in.Label); err != nil {
			return MarkerDef{}, err
		}
		if coreMarker(prev.Label) && next.Label != prev.Label {
			return MarkerDef{}, domain.Invalid("label", "a core marker keeps its label")
		}
	}
	if in.Color != nil {
		if err := checkColor(*in.Color); err != nil {
			return MarkerDef{}, err
		}
		next.Color = *in.Color
	}
	if in.Active != nil {
		next.Inactive = !*in.Active
	}
	if !next.Inactive && s.labelTaken("", next.Label, id) {
		return MarkerDef{}, domain.Conflict("an active firm marker already has this label")
	}
	next.UpdatedAt = s.clock()
	if err := s.db.PutMarker(ctx, next); err != nil {
		return MarkerDef{}, err
	}
	if err := s.record(ctx, p, markerChanges(prev, next, "marker.updated", "Updated marker "+next.Label)); err != nil {
		return MarkerDef{}, err
	}
	return next, nil
}

func markerChanges(prev, next MarkerDef, action, summary string) []change {
	return changedFields("marker", next.ID, domain.ID(""), action, summary, [][3]string{
		{"label", prev.Label, next.Label},
		{"color", prev.Color, next.Color},
		{"active", boolText(!prev.Inactive), boolText(!next.Inactive)},
		{"target", prev.Target, next.Target},
	})
}

// moduleGate admits only an installed, enabled module's own token acting for itself:
// no viewer, no user, the path module id equal to the signed module id on the token.
// The core route has already required markers:write on that token.
func (s *Service) moduleGate(ctx context.Context, p domain.Principal, module string) error {
	if p.Source != domain.SourceModule || p.ModuleID == "" || !p.UserID.IsZero() {
		return domain.Forbidden("only a module's own token registers module markers")
	}
	if !moduleIDRule.MatchString(module) || module != p.ModuleID {
		return domain.Forbidden("a module writes markers only in its own namespace")
	}
	on := false
	if s.moduleOn != nil {
		var err error
		if on, err = s.moduleOn(ctx, module); err != nil {
			return err
		}
	}
	if !on {
		return domain.Forbidden("the module is not installed and enabled")
	}
	return nil
}

func moduleTarget(v string) (string, error) {
	if !targetLength.MatchString(v) || !markerTarget.MatchString(v) {
		return "", domain.Invalid("target", "target is a module-local page such as cases/open")
	}
	return v, nil
}

// CreateModuleMarker registers a marker in the calling module's namespace.
func (s *Service) CreateModuleMarker(ctx context.Context, p domain.Principal, module string, in MarkerWrite) (_ MarkerDef, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.moduleGate(ctx, p, module); err != nil {
		return MarkerDef{}, err
	}
	if in.Label == nil {
		return MarkerDef{}, domain.Invalid("label", "label is required")
	}
	label, err := markerLabel(*in.Label)
	if err != nil {
		return MarkerDef{}, err
	}
	if in.Color == nil {
		return MarkerDef{}, domain.Invalid("color", "color is required")
	}
	if err := checkColor(*in.Color); err != nil {
		return MarkerDef{}, err
	}
	target := ""
	if in.Target != nil {
		if target, err = moduleTarget(*in.Target); err != nil {
			return MarkerDef{}, err
		}
	}
	if coreMarker(label) {
		return MarkerDef{}, domain.Conflict("that label is a core marker")
	}
	if s.labelTaken(module, label, "") {
		return MarkerDef{}, domain.Conflict("an active marker in this namespace already has this label")
	}
	meta, err := s.meta(p)
	if err != nil {
		return MarkerDef{}, err
	}
	row := MarkerDef{ID: meta.ID, Label: label, Color: *in.Color, ModuleID: module, Target: target,
		CreatedAt: meta.CreatedAt, CreatedBy: meta.CreatedBy, UpdatedAt: meta.UpdatedAt}
	if err := s.db.PutMarker(ctx, row); err != nil {
		return MarkerDef{}, err
	}
	if err := s.record(ctx, p, []change{{
		Entity: "marker", EntityID: row.ID, Field: "label", New: label, Kind: "create",
		Action: "marker.created", Summary: "Module " + module + " added marker " + label,
	}}); err != nil {
		return MarkerDef{}, err
	}
	return row.shown(), nil
}

// ownModuleMarker loads a marker the calling module owns. A firm marker or another
// module's is forbidden; a missing one is not found.
func (s *Service) ownModuleMarker(module string, id domain.ID) (MarkerDef, error) {
	row, ok := s.db.Marker(id)
	if !ok {
		return MarkerDef{}, domain.NotFound("marker")
	}
	if row.ModuleID != module {
		return MarkerDef{}, domain.Forbidden("this marker is not in the module's namespace")
	}
	return row.shown(), nil
}

// UpdateModuleMarker edits the calling module's own active marker.
func (s *Service) UpdateModuleMarker(ctx context.Context, p domain.Principal, module string, id domain.ID, in MarkerWrite) (_ MarkerDef, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.moduleGate(ctx, p, module); err != nil {
		return MarkerDef{}, err
	}
	prev, err := s.ownModuleMarker(module, id)
	if err != nil {
		return MarkerDef{}, err
	}
	if prev.Inactive {
		return MarkerDef{}, domain.Conflict("a revoked marker is not edited; register a new one")
	}
	next := prev
	if in.Label != nil {
		if next.Label, err = markerLabel(*in.Label); err != nil {
			return MarkerDef{}, err
		}
		if coreMarker(next.Label) {
			return MarkerDef{}, domain.Conflict("that label is a core marker")
		}
	}
	if in.Color != nil {
		if err := checkColor(*in.Color); err != nil {
			return MarkerDef{}, err
		}
		next.Color = *in.Color
	}
	if in.Target != nil {
		if next.Target, err = moduleTarget(*in.Target); err != nil {
			return MarkerDef{}, err
		}
	}
	if s.labelTaken(module, next.Label, id) {
		return MarkerDef{}, domain.Conflict("an active marker in this namespace already has this label")
	}
	next.UpdatedAt = s.clock()
	if err := s.db.PutMarker(ctx, next); err != nil {
		return MarkerDef{}, err
	}
	if err := s.record(ctx, p, markerChanges(prev, next, "marker.updated", "Module "+module+" updated marker "+next.Label)); err != nil {
		return MarkerDef{}, err
	}
	return next, nil
}

// RevokeModuleMarker sets the calling module's marker inactive. Pins stay. Revoking an
// inactive marker returns it and records nothing.
func (s *Service) RevokeModuleMarker(ctx context.Context, p domain.Principal, module string, id domain.ID) (_ MarkerDef, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.moduleGate(ctx, p, module); err != nil {
		return MarkerDef{}, err
	}
	prev, err := s.ownModuleMarker(module, id)
	if err != nil || prev.Inactive {
		return prev, err
	}
	next := prev
	next.Inactive = true
	next.UpdatedAt = s.clock()
	if err := s.db.PutMarker(ctx, next); err != nil {
		return MarkerDef{}, err
	}
	if err := s.record(ctx, p, []change{{
		Entity: "marker", EntityID: id, Field: "active", Old: "true", New: "false",
		Action: "marker.revoked", Summary: "Module " + module + " revoked marker " + next.Label,
	}}); err != nil {
		return MarkerDef{}, err
	}
	return next, nil
}

// MatterMarkerPatch moves a pin to another matter, marker or practice area. Nil keeps it.
type MatterMarkerPatch struct {
	MatterID       *domain.ID
	MarkerID       *domain.ID
	PracticeAreaID *domain.ID
}

// UpdateMatterMarker patches a pinned marker. The caller must be able to write the
// matter it is on and the matter it moves to; walls answer not_found.
func (s *Service) UpdateMatterMarker(ctx context.Context, p domain.Principal, id domain.ID, in MatterMarkerPatch) (_ MatterMarker, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	prev, ok := s.findMarker(id)
	if !ok {
		return MatterMarker{}, domain.NotFound("marker")
	}
	if err := s.gateMatter(ctx, p, prev.MatterID, domain.ActionWrite); err != nil {
		return MatterMarker{}, err
	}
	next := prev
	if in.MatterID != nil && *in.MatterID != prev.MatterID {
		if err := s.gateMatter(ctx, p, *in.MatterID, domain.ActionWrite); err != nil {
			return MatterMarker{}, err
		}
		next.MatterID = *in.MatterID
	}
	if err := s.ensureMarkers(ctx); err != nil {
		return MatterMarker{}, err
	}
	def, ok := s.db.Marker(next.MarkerID)
	if in.MarkerID != nil && *in.MarkerID != prev.MarkerID {
		if def, ok = s.db.Marker(*in.MarkerID); !ok || def.Inactive {
			return MatterMarker{}, domain.Invalid("marker_id", "unknown or inactive marker")
		}
		next.MarkerID, next.Label = def.ID, def.Label
	}
	if in.PracticeAreaID != nil {
		next.PracticeAreaID = *in.PracticeAreaID
	}
	if ok && def.ModuleID == "" && def.Label == MarkerAlsoAsked {
		if next.PracticeAreaID.IsZero() {
			return MatterMarker{}, domain.Invalid("practice_area_id", "name the practice area")
		}
		if _, err := s.db.Area(ctx, next.PracticeAreaID); err != nil {
			return MatterMarker{}, domain.Invalid("practice_area_id", "unknown practice area")
		}
	} else if !next.PracticeAreaID.IsZero() {
		return MatterMarker{}, domain.Invalid("practice_area_id", "only Also asked about names a practice area")
	}
	if next == prev {
		return prev, nil
	}
	next.UpdatedAt = s.clock()
	if err := s.db.UpdateMatterMarker(ctx, next); err != nil {
		return MatterMarker{}, conflict(missing(err, "marker"))
	}
	changes := []change{{
		MatterID: next.MatterID, Entity: "matter", EntityID: next.MatterID, Field: "marker",
		Old: prev.Label, New: next.Label, Kind: "update", Action: "matter.updated", Summary: "Updated marker " + next.Label,
	}}
	if next.Label == prev.Label {
		changes[0].Old, changes[0].New = string(prev.PracticeAreaID), string(next.PracticeAreaID)
		changes[0].Field = "marker_practice_area"
	}
	if next.MatterID != prev.MatterID {
		changes[0].Old, changes[0].New, changes[0].Field = "", next.Label, "marker"
		changes = append(changes, change{
			MatterID: prev.MatterID, Entity: "matter", EntityID: prev.MatterID, Field: "marker",
			Old: prev.Label, Kind: "update", Action: "matter.updated", Summary: "Moved marker " + prev.Label + " to another matter",
		})
	}
	if err := s.record(ctx, p, changes); err != nil {
		return MatterMarker{}, err
	}
	return next, nil
}

// markerChoices are the active firm marker labels the matter screen offers to pin.
func (s *Service) markerChoices(ctx context.Context) []string {
	if err := s.ensureMarkers(ctx); err != nil {
		return s.listSetting(ctx, "practice.markers", domain.DefaultMarkers())
	}
	var out []string
	for _, row := range s.db.Markers() {
		if row.ModuleID == "" && !row.Inactive && !containsString(out, row.Label) {
			out = append(out, row.Label)
		}
	}
	return out
}

// partyRoleKeys is the active role list parties are checked against.
func (s *Service) partyRoleKeys(ctx context.Context) []string {
	return s.listSetting(ctx, "practice.party_roles", domain.DefaultPartyRoles())
}

// ensurePartyRoles gives every key on practice.party_roles its record, so a role the
// admin added on the settings page is listed too.
func (s *Service) ensurePartyRoles(ctx context.Context, keys []string) error {
	have := map[string]bool{}
	for _, row := range s.db.PartyRoles() {
		have[row.Key] = true
	}
	now := s.clock()
	for _, key := range keys {
		if have[key] {
			continue
		}
		id, err := s.id()
		if err != nil {
			return err
		}
		if err := s.db.PutPartyRole(ctx, PartyRoleDef{ID: id, Key: key, Label: key, CreatedAt: now, CreatedBy: domain.SystemUserID, UpdatedAt: now}); err != nil {
			return conflict(err)
		}
		have[key] = true
	}
	return nil
}

// PartyRole is a role record with its active state, which is whether its key is on
// practice.party_roles.
type PartyRole struct {
	PartyRoleDef
	Active bool
}

func roleView(row PartyRoleDef, keys []string) PartyRole {
	return PartyRole{PartyRoleDef: row, Active: containsString(keys, row.Key)}
}

// ListPartyRoles returns every party role; active filters.
func (s *Service) ListPartyRoles(ctx context.Context, p domain.Principal, active *bool) ([]PartyRole, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionRead}); err != nil {
		return nil, err
	}
	keys := s.partyRoleKeys(ctx)
	if err := s.ensurePartyRoles(ctx, keys); err != nil {
		return nil, err
	}
	var out []PartyRole
	for _, row := range s.db.PartyRoles() {
		v := roleView(row, keys)
		if active == nil || *active == v.Active {
			out = append(out, v)
		}
	}
	return out, nil
}

// GetPartyRole returns one party role.
func (s *Service) GetPartyRole(ctx context.Context, p domain.Principal, id domain.ID) (PartyRole, error) {
	rows, err := s.ListPartyRoles(ctx, p, nil)
	if err != nil {
		return PartyRole{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return PartyRole{}, domain.NotFound("party role")
}

// PartyRoleWrite creates or patches a party role. Nil leaves a field as it is.
type PartyRoleWrite struct {
	Key    *string
	Label  *string
	Active *bool
}

// setRoleKeys stores the active key list through the settings registry, which audits the
// change (setting.updated). It runs last in the unit, so a refused setting rolls the role
// record back with it.
// ponytail: ceiling: the setting commits in its own transaction just before the practice
// unit does; if that commit then fails the role list is ahead of the records, and the next
// read recreates the missing record from the setting. Upgrade path: settings writes join
// the practice transaction. Trigger: settings share the practice unit's transaction.
func (s *Service) setRoleKeys(ctx context.Context, p domain.Principal, keys []string) error {
	if s.settings == nil {
		return domain.Invalid("practice.party_roles", "settings are not available")
	}
	if len(keys) == 0 {
		return domain.Invalid("active", "at least one party role stays active")
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if _, err := s.settings.Set(ctx, "practice.party_roles", string(raw), p); err != nil {
		return domain.Forbidden("the party role list could not be saved: " + err.Error())
	}
	return nil
}

func roleKey(key *string, label string) string {
	if key != nil && strings.TrimSpace(*key) != "" {
		return strings.TrimSpace(*key)
	}
	return strings.ToLower(label)
}

// CreatePartyRole adds a party role. Admin with settings:write.
func (s *Service) CreatePartyRole(ctx context.Context, p domain.Principal, in PartyRoleWrite) (_ PartyRole, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return PartyRole{}, err
	}
	if in.Label == nil || strings.TrimSpace(*in.Label) == "" {
		return PartyRole{}, domain.Invalid("label", "label is required")
	}
	label := strings.TrimSpace(*in.Label)
	key := roleKey(in.Key, label)
	keys := s.partyRoleKeys(ctx)
	if err := s.ensurePartyRoles(ctx, keys); err != nil {
		return PartyRole{}, err
	}
	meta, err := s.meta(p)
	if err != nil {
		return PartyRole{}, err
	}
	row := PartyRoleDef{ID: meta.ID, Key: key, Label: label, CreatedAt: meta.CreatedAt, CreatedBy: meta.CreatedBy, UpdatedAt: meta.UpdatedAt}
	if err := s.db.PutPartyRole(ctx, row); err != nil {
		return PartyRole{}, conflict(err)
	}
	if err := s.record(ctx, p, []change{{
		Entity: "party_role", EntityID: row.ID, Field: "key", New: key, Kind: "create",
		Action: "setting.updated", Summary: "Added party role " + label,
	}}); err != nil {
		return PartyRole{}, err
	}
	active := in.Active == nil || *in.Active
	if active {
		keys = append(append([]string(nil), keys...), key)
		if err := s.setRoleKeys(ctx, p, keys); err != nil {
			return PartyRole{}, err
		}
	}
	return PartyRole{PartyRoleDef: row, Active: active}, nil
}

// UpdatePartyRole patches a party role. A key that parties already use does not change.
func (s *Service) UpdatePartyRole(ctx context.Context, p domain.Principal, id domain.ID, in PartyRoleWrite) (_ PartyRole, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return PartyRole{}, err
	}
	keys := s.partyRoleKeys(ctx)
	if err := s.ensurePartyRoles(ctx, keys); err != nil {
		return PartyRole{}, err
	}
	var prev PartyRoleDef
	found := false
	for _, row := range s.db.PartyRoles() {
		if row.ID == id {
			prev, found = row, true
		}
	}
	if !found {
		return PartyRole{}, domain.NotFound("party role")
	}
	wasActive := containsString(keys, prev.Key)
	next := prev
	if in.Label != nil {
		if strings.TrimSpace(*in.Label) == "" {
			return PartyRole{}, domain.Invalid("label", "label is required")
		}
		next.Label = strings.TrimSpace(*in.Label)
	}
	if in.Key != nil && strings.TrimSpace(*in.Key) != "" && strings.TrimSpace(*in.Key) != prev.Key {
		for _, party := range s.db.Parties("", "") {
			if party.Role == prev.Key {
				return PartyRole{}, domain.Conflict("parties already use this role key")
			}
		}
		next.Key = strings.TrimSpace(*in.Key)
	}
	active := wasActive
	if in.Active != nil {
		active = *in.Active
	}
	next.UpdatedAt = s.clock()
	if err := s.db.PutPartyRole(ctx, next); err != nil {
		return PartyRole{}, conflict(err)
	}
	if err := s.record(ctx, p, changedFields("party_role", id, domain.ID(""), "setting.updated", "Updated party role "+next.Label, [][3]string{
		{"key", prev.Key, next.Key},
		{"label", prev.Label, next.Label},
		{"active", boolText(wasActive), boolText(active)},
	})); err != nil {
		return PartyRole{}, err
	}
	var nextKeys []string
	for _, k := range keys {
		if k != prev.Key {
			nextKeys = append(nextKeys, k)
		}
	}
	if active {
		nextKeys = append(nextKeys, next.Key)
	}
	if wasActive != active || (active && next.Key != prev.Key) {
		if err := s.setRoleKeys(ctx, p, nextKeys); err != nil {
			return PartyRole{}, err
		}
	}
	return PartyRole{PartyRoleDef: next, Active: active}, nil
}
