package practice

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

type matterWrite struct {
	Name                  *string     `json:"name"`
	PracticeAreaID        *domain.ID  `json:"practice_area_id"`
	Status                *string     `json:"status"`
	Stage                 *string     `json:"stage"`
	CaseNumber            *string     `json:"case_number"`
	Court                 *string     `json:"court"`
	CountyID              *domain.ID  `json:"county_id"`
	ClientPosition        *string     `json:"client_position"`
	OpenedOn              *string     `json:"opened_on"`
	StatuteOfLimitations  *string     `json:"statute_of_limitations_on"`
	BillingType           *string     `json:"billing_type"`
	FlatFeeCents          *int64      `json:"flat_fee_cents"`
	MatterRateCents       *int64      `json:"matter_rate_cents"`
	ResponsibleAttorneyID *domain.ID  `json:"responsible_attorney_user_id"`
	AssignedStaffUserIDs  []domain.ID `json:"assigned_staff_user_ids"`
	ReferralSourceID      *domain.ID  `json:"referral_source_id"`
}

func (w matterWrite) input() MatterInput {
	in := MatterInput{}
	if w.Name != nil {
		in.Name = *w.Name
	}
	if w.PracticeAreaID != nil {
		in.PracticeAreaID = *w.PracticeAreaID
	}
	if w.Status != nil {
		in.Status = *w.Status
	}
	if w.Stage != nil {
		in.Stage = *w.Stage
	}
	if w.CaseNumber != nil {
		in.CaseNumber = *w.CaseNumber
	}
	if w.Court != nil {
		in.Court = *w.Court
	}
	if w.CountyID != nil {
		in.CountyID = *w.CountyID
	}
	if w.ClientPosition != nil {
		in.ClientPosition = *w.ClientPosition
	}
	if w.OpenedOn != nil {
		in.OpenedOn = *w.OpenedOn
	}
	if w.StatuteOfLimitations != nil {
		in.StatuteOfLimitations = *w.StatuteOfLimitations
	}
	if w.BillingType != nil {
		in.BillingType = *w.BillingType
	}
	if w.FlatFeeCents != nil {
		in.FlatFeeCents = domain.Cents(*w.FlatFeeCents)
	}
	if w.MatterRateCents != nil {
		in.MatterRateCents = domain.Cents(*w.MatterRateCents)
	}
	if w.ResponsibleAttorneyID != nil {
		in.ResponsibleAttorneyID = *w.ResponsibleAttorneyID
	}
	if w.AssignedStaffUserIDs != nil {
		if len(w.AssignedStaffUserIDs) > 0 {
			in.AssignedStaffID = w.AssignedStaffUserIDs[0]
		}
	}
	if w.ReferralSourceID != nil {
		in.ReferralSourceID = *w.ReferralSourceID
	}
	return in
}

func (w matterWrite) patch() MatterPatch {
	patch := MatterPatch{
		Name: w.Name, PracticeAreaID: w.PracticeAreaID, Status: w.Status, Stage: w.Stage,
		CaseNumber: w.CaseNumber, Court: w.Court, CountyID: w.CountyID, ClientPosition: w.ClientPosition,
		OpenedOn: w.OpenedOn, StatuteOfLimitations: w.StatuteOfLimitations, BillingType: w.BillingType,
		ResponsibleAttorneyID: w.ResponsibleAttorneyID, ReferralSourceID: w.ReferralSourceID,
	}
	if w.FlatFeeCents != nil {
		cents := domain.Cents(*w.FlatFeeCents)
		patch.FlatFeeCents = &cents
	}
	if w.MatterRateCents != nil {
		cents := domain.Cents(*w.MatterRateCents)
		patch.MatterRateCents = &cents
	}
	if w.AssignedStaffUserIDs != nil {
		id := domain.ID("")
		if len(w.AssignedStaffUserIDs) > 0 {
			id = w.AssignedStaffUserIDs[0]
		}
		patch.AssignedStaffID = &id
	}
	return patch
}

type contactWrite struct {
	Kind             *string  `json:"kind"`
	GivenName        *string  `json:"given_name"`
	MiddleName       *string  `json:"middle_name"`
	FamilyName       *string  `json:"family_name"`
	Suffix           *string  `json:"suffix"`
	OrganizationName *string  `json:"organization_name"`
	PreferredName    *string  `json:"preferred_name"`
	AlsoKnownAs      []string `json:"also_known_as"`
	DateOfBirth      *string  `json:"date_of_birth"`
	DateOfDeath      *string  `json:"date_of_death"`
	Identifier       *string  `json:"identifier"`
	EntityType       *string  `json:"entity_type"`
	FormationState   *string  `json:"formation_state"`
	FormationDate    *string  `json:"formation_date"`
	Source           *string  `json:"source"`
}

func (w contactWrite) input() ContactInput {
	in := ContactInput{}
	if w.Kind != nil {
		in.Kind = *w.Kind
	}
	if w.GivenName != nil {
		in.GivenName = *w.GivenName
	}
	if w.MiddleName != nil {
		in.MiddleName = *w.MiddleName
	}
	if w.FamilyName != nil {
		in.FamilyName = *w.FamilyName
	}
	if w.Suffix != nil {
		in.Suffix = *w.Suffix
	}
	if w.OrganizationName != nil {
		in.FullName = *w.OrganizationName
	}
	if w.PreferredName != nil {
		in.PreferredName = *w.PreferredName
	}
	if w.AlsoKnownAs != nil {
		in.AlsoKnownAs = joinAKA(w.AlsoKnownAs)
	}
	if w.DateOfBirth != nil {
		in.DateOfBirth = *w.DateOfBirth
	}
	if w.DateOfDeath != nil {
		in.DateOfDeath = *w.DateOfDeath
	}
	if w.Identifier != nil {
		in.SSNEIN = *w.Identifier
	}
	if w.EntityType != nil {
		in.EntityType = *w.EntityType
	}
	if w.FormationState != nil {
		in.FormationState = *w.FormationState
	}
	if w.FormationDate != nil {
		in.FormationDate = *w.FormationDate
	}
	if w.Source != nil {
		in.Source = *w.Source
	}
	return in
}

func (w contactWrite) patch() ContactPatch {
	patch := ContactPatch{
		Kind: w.Kind, GivenName: w.GivenName, MiddleName: w.MiddleName, FamilyName: w.FamilyName,
		Suffix: w.Suffix, FullName: w.OrganizationName, PreferredName: w.PreferredName,
		DateOfBirth: w.DateOfBirth, DateOfDeath: w.DateOfDeath, SSNEIN: w.Identifier,
		EntityType: w.EntityType, FormationState: w.FormationState, FormationDate: w.FormationDate, Source: w.Source,
	}
	if w.AlsoKnownAs != nil {
		aka := joinAKA(w.AlsoKnownAs)
		patch.AlsoKnownAs = &aka
	}
	return patch
}

type partyWrite struct {
	MatterID       domain.ID       `json:"matter_id"`
	ContactID      domain.ID       `json:"contact_id"`
	Role           *string         `json:"role"`
	Primary        *bool           `json:"primary"`
	RoleAttributes json.RawMessage `json:"role_attributes"`
	ServiceAddress *string         `json:"service_address_id"`
	Notes          *string         `json:"notes"`
}

func (w partyWrite) input() PartyInput {
	in := PartyInput{ContactID: w.ContactID}
	if w.Role != nil {
		in.Role = *w.Role
	}
	if w.Primary != nil {
		in.Primary = *w.Primary
	}
	if len(w.RoleAttributes) > 0 {
		in.RoleAttributes = string(w.RoleAttributes)
	}
	if w.ServiceAddress != nil {
		in.ServiceAddress = *w.ServiceAddress
	}
	if w.Notes != nil {
		in.Notes = *w.Notes
	}
	return in
}

func (w partyWrite) patch() PartyPatch {
	patch := PartyPatch{Role: w.Role, Primary: w.Primary, ServiceAddress: w.ServiceAddress, Notes: w.Notes}
	if len(w.RoleAttributes) > 0 {
		raw := string(w.RoleAttributes)
		patch.RoleAttributes = &raw
	}
	return patch
}

type noteWrite struct {
	MatterID  domain.ID `json:"matter_id"`
	ContactID domain.ID `json:"contact_id"`
	Title     *string   `json:"title"`
	Body      *string   `json:"body"`
	Pinned    *bool     `json:"pinned"`
	Tags      []string  `json:"tags"`
	Status    *string   `json:"status"`
}

func (w noteWrite) input() NoteInput {
	in := NoteInput{MatterID: w.MatterID, ContactID: w.ContactID, Tags: w.Tags}
	if w.Title != nil {
		in.Title = *w.Title
	}
	if w.Body != nil {
		in.Body = *w.Body
	}
	if w.Pinned != nil {
		in.Pinned = *w.Pinned
	}
	if w.Status != nil {
		in.Status = *w.Status
	}
	return in
}

func (w noteWrite) patch() NotePatch {
	return NotePatch{Title: w.Title, Body: w.Body, Pinned: w.Pinned, Status: w.Status, Tags: w.Tags, HasTags: w.Tags != nil}
}

type conflictWrite struct {
	Name         *string    `json:"name"`
	Aliases      []string   `json:"aliases"`
	DateOfBirth  *string    `json:"date_of_birth"`
	Relationship *string    `json:"relationship"`
	Scope        *string    `json:"scope"`
	Reason       *string    `json:"reason"`
	MatterID     *domain.ID `json:"matter_id"`
	FromDate     *string    `json:"from_date"`
	ToDate       *string    `json:"to_date"`
	Source       *string    `json:"source"`
}

func (w conflictWrite) input() ConflictInput {
	in := ConflictInput{}
	if w.Name != nil {
		in.Name = *w.Name
	}
	if w.Aliases != nil {
		in.Aliases = joinAKA(w.Aliases)
	}
	if w.DateOfBirth != nil {
		in.DateOfBirth = *w.DateOfBirth
	}
	if w.Relationship != nil {
		in.Relationship = *w.Relationship
	}
	if w.Scope != nil {
		in.Scope = *w.Scope
	}
	if w.Reason != nil {
		in.Reason = *w.Reason
	}
	if w.MatterID != nil {
		in.MatterID = *w.MatterID
	}
	if w.FromDate != nil {
		in.StartsOn = *w.FromDate
	}
	if w.ToDate != nil {
		in.EndsOn = *w.ToDate
	}
	if w.Source != nil {
		in.Source = *w.Source
	}
	return in
}

func when(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func idOrNil(id domain.ID) any {
	if id.IsZero() {
		return nil
	}
	return id
}

func splitAKA(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func contactDoc(row domain.Contact) map[string]any {
	var identifier any
	if row.SSNEINCiphertext != "" {
		identifier = row.SSNEINCiphertext
	}
	org := ""
	if row.Kind == KindOrganization {
		org = row.FullName
	}
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"kind": row.Kind, "given_name": row.GivenName, "middle_name": row.MiddleName, "family_name": row.FamilyName,
		"suffix": row.Suffix, "organization_name": org, "full_name": row.FullName, "preferred_name": row.PreferredName,
		"also_known_as": splitAKA(row.AlsoKnownAs), "date_of_birth": row.DateOfBirth, "date_of_death": row.DateOfDeath,
		"identifier": identifier, "entity_type": row.EntityType, "formation_state": row.FormationState,
		"formation_date": row.FormationDate, "archived": row.Archived, "source": row.Source,
	}
}

func partyDoc(row domain.MatterParty) map[string]any {
	var attrs any
	if row.RoleAttributes != "" {
		var parsed any
		if json.Unmarshal([]byte(row.RoleAttributes), &parsed) == nil {
			attrs = parsed
		} else {
			attrs = row.RoleAttributes
		}
	}
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"matter_id": row.MatterID, "contact_id": row.ContactID, "role": row.Role, "primary": row.Primary,
		"role_attributes": attrs, "service_address_id": idOrNil(domain.ID(row.ServiceAddress)), "notes": row.Notes,
	}
}

func (h *Handler) noteDoc(row domain.Note) map[string]any {
	tags := []string{}
	for _, tag := range h.Svc.db.NoteTags(row.ID) {
		tags = append(tags, tag.Tag)
	}
	return noteDocument(row, tags)
}

func noteDocument(row domain.Note, tags []string) map[string]any {
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"matter_id": idOrNil(row.MatterID), "contact_id": idOrNil(row.ContactID), "author_id": row.AuthorID,
		"title": row.Title, "body": row.Body, "pinned": row.Pinned, "tags": tags, "status": row.Status,
	}
}

func conflictDoc(row domain.ConflictRecord) map[string]any {
	var reason any
	if row.Reason != "" {
		reason = row.Reason
	}
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"name": row.Name, "aliases": splitAKA(row.Aliases), "date_of_birth": row.DateOfBirth,
		"relationship": row.Relationship, "scope": row.Scope, "reason": reason,
		"matter_id": idOrNil(row.MatterID), "from_date": row.StartsOn, "to_date": row.EndsOn, "source": row.Source,
	}
}

func phoneDoc(row domain.ContactPhone) map[string]any {
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"contact_id": row.ContactID, "label": row.Label, "primary": row.Primary, "e164": row.PhoneE164,
		"effective_from": row.EffectiveFrom, "effective_to": row.EffectiveTo,
	}
}

func emailDoc(row domain.ContactEmail) map[string]any {
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"contact_id": row.ContactID, "label": row.Label, "primary": row.Primary, "email": row.Email,
		"effective_from": row.EffectiveFrom, "effective_to": row.EffectiveTo,
	}
}

func addressDoc(row domain.ContactAddress) map[string]any {
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"contact_id": row.ContactID, "label": row.Label, "primary": row.Primary, "line1": row.Line1, "line2": row.Line2,
		"city": row.City, "region": row.Region, "postal_code": row.PostalCode, "country": row.Country,
		"effective_from": row.EffectiveFrom, "effective_to": row.EffectiveTo,
	}
}

func tagDoc(row domain.ContactTag) map[string]any {
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"contact_id": row.ContactID, "tag": row.Tag,
	}
}

func markerDoc(row MatterMarker) map[string]any {
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.CreatedAt),
		"matter_id": row.MatterID, "marker_id": row.MarkerID, "practice_area_id": idOrNil(row.PracticeAreaID),
	}
}

func fieldDoc(row FieldDef) map[string]any {
	options := row.Options
	if options == nil {
		options = []string{}
	}
	areas := row.PracticeAreaIDs
	if areas == nil {
		areas = []domain.ID{}
	}
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"record_type": row.RecordType, "key": row.Key, "label": row.Label, "field_type": row.FieldType,
		"sort_order": row.SortOrder, "required": row.Required, "practice_area_ids": areas, "options": options,
		"retired": row.Retired,
	}
}

func areaDoc(row domain.PracticeArea) map[string]any {
	var stages []string
	_ = json.Unmarshal([]byte(row.Stages), &stages)
	if stages == nil {
		stages = []string{}
	}
	types := splitAKA(row.MatterTypes)
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"name": row.Name, "matter_types": types, "stages": stages, "active": row.Active,
	}
}

func countyDoc(row domain.County) map[string]any {
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"name": row.Name, "state": row.State,
	}
}

func referralDoc(row domain.ReferralSource) map[string]any {
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"name": row.Name, "kind": row.Kind, "contact_id": idOrNil(row.ContactID), "active": row.Active,
	}
}
