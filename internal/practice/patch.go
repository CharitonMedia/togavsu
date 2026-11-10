package practice

import (
	"context"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// MatterPatch overlays only the fields a PATCH sent.
type MatterPatch struct {
	Name                  *string
	PracticeAreaID        *domain.ID
	Status                *string
	Stage                 *string
	CaseNumber            *string
	Court                 *string
	CountyID              *domain.ID
	ClientPosition        *string
	OpenedOn              *string
	StatuteOfLimitations  *string
	BillingType           *string
	FlatFeeCents          *domain.Cents
	MatterRateCents       *domain.Cents
	ResponsibleAttorneyID *domain.ID
	AssignedStaffID       *domain.ID
	ReferralSourceID      *domain.ID
	Custom                []CustomInput
}

// PatchMatter changes the fields that were sent and leaves the rest.
func (s *Service) PatchMatter(ctx context.Context, p domain.Principal, id domain.ID, patch MatterPatch) (_ domain.Matter, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	prev, err := s.db.Matter(ctx, id)
	if err != nil {
		return domain.Matter{}, missing(err, "matter")
	}
	in := MatterInput{
		Name: prev.Name, PracticeAreaID: prev.PracticeAreaID, Status: prev.Status, Stage: prev.Stage,
		CaseNumber: prev.CaseNumber, Court: prev.Court, CountyID: prev.CountyID, ClientPosition: prev.ClientPosition,
		OpenedOn: prev.OpenedOn, StatuteOfLimitations: prev.StatuteOfLimitations, BillingType: prev.BillingType,
		FlatFeeCents: prev.FlatFeeCents, MatterRateCents: prev.MatterRateCents,
		ResponsibleAttorneyID: prev.ResponsibleAttorneyID, AssignedStaffID: prev.AssignedStaffID,
		ReferralSourceID: prev.ReferralSourceID,
	}
	if patch.Name != nil {
		in.Name = *patch.Name
	}
	if patch.PracticeAreaID != nil {
		in.PracticeAreaID = *patch.PracticeAreaID
	}
	if patch.Status != nil {
		in.Status = *patch.Status
	}
	if patch.Stage != nil {
		in.Stage = *patch.Stage
	}
	if patch.CaseNumber != nil {
		in.CaseNumber = *patch.CaseNumber
	}
	if patch.Court != nil {
		in.Court = *patch.Court
	}
	if patch.CountyID != nil {
		in.CountyID = *patch.CountyID
	}
	if patch.ClientPosition != nil {
		in.ClientPosition = *patch.ClientPosition
	}
	if patch.OpenedOn != nil {
		in.OpenedOn = *patch.OpenedOn
	}
	if patch.StatuteOfLimitations != nil {
		in.StatuteOfLimitations = *patch.StatuteOfLimitations
	}
	if patch.BillingType != nil {
		in.BillingType = *patch.BillingType
	}
	if patch.FlatFeeCents != nil {
		in.FlatFeeCents = *patch.FlatFeeCents
	}
	if patch.MatterRateCents != nil {
		in.MatterRateCents = *patch.MatterRateCents
	}
	if patch.ResponsibleAttorneyID != nil {
		in.ResponsibleAttorneyID = *patch.ResponsibleAttorneyID
	}
	if patch.AssignedStaffID != nil {
		in.AssignedStaffID = *patch.AssignedStaffID
	}
	if patch.ReferralSourceID != nil {
		in.ReferralSourceID = *patch.ReferralSourceID
	}
	in.Custom = patch.Custom
	return s.UpdateMatter(ctx, p, id, in)
}

// ContactPatch overlays only the fields a PATCH sent.
type ContactPatch struct {
	Kind           *string
	GivenName      *string
	MiddleName     *string
	FamilyName     *string
	Suffix         *string
	FullName       *string
	PreferredName  *string
	AlsoKnownAs    *string
	DateOfBirth    *string
	DateOfDeath    *string
	SSNEIN         *string
	EntityType     *string
	FormationState *string
	FormationDate  *string
	Archived       *bool
	Source         *string
	Custom         []CustomInput
}

// PatchContact changes the fields that were sent and leaves the rest.
func (s *Service) PatchContact(ctx context.Context, p domain.Principal, id domain.ID, patch ContactPatch) (_ domain.Contact, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	prev, err := s.db.Contact(ctx, id)
	if err != nil {
		return domain.Contact{}, missing(err, "contact")
	}
	in := ContactInput{
		Kind: prev.Kind, GivenName: prev.GivenName, MiddleName: prev.MiddleName, FamilyName: prev.FamilyName,
		Prefix: prev.Prefix, Suffix: prev.Suffix, PreferredName: prev.PreferredName,
		AlsoKnownAs: prev.AlsoKnownAs, DateOfBirth: prev.DateOfBirth, DateOfDeath: prev.DateOfDeath,
		EntityType: prev.EntityType, FormationState: prev.FormationState, FormationDate: prev.FormationDate,
		Archived: prev.Archived, Source: prev.Source,
	}
	if prev.Kind == KindOrganization {
		in.FullName = prev.FullName
	}
	if patch.Kind != nil {
		in.Kind = *patch.Kind
	}
	if patch.GivenName != nil {
		in.GivenName = *patch.GivenName
	}
	if patch.MiddleName != nil {
		in.MiddleName = *patch.MiddleName
	}
	if patch.FamilyName != nil {
		in.FamilyName = *patch.FamilyName
	}
	if patch.Suffix != nil {
		in.Suffix = *patch.Suffix
	}
	if patch.FullName != nil {
		in.FullName = *patch.FullName
	}
	if patch.PreferredName != nil {
		in.PreferredName = *patch.PreferredName
	}
	if patch.AlsoKnownAs != nil {
		in.AlsoKnownAs = *patch.AlsoKnownAs
	}
	if patch.DateOfBirth != nil {
		in.DateOfBirth = *patch.DateOfBirth
	}
	if patch.DateOfDeath != nil {
		in.DateOfDeath = *patch.DateOfDeath
	}
	if patch.SSNEIN != nil {
		in.SSNEIN = *patch.SSNEIN
	}
	if patch.EntityType != nil {
		in.EntityType = *patch.EntityType
	}
	if patch.FormationState != nil {
		in.FormationState = *patch.FormationState
	}
	if patch.FormationDate != nil {
		in.FormationDate = *patch.FormationDate
	}
	if patch.Archived != nil {
		in.Archived = *patch.Archived
	}
	if patch.Source != nil {
		in.Source = *patch.Source
	}
	in.Custom = patch.Custom
	return s.UpdateContact(ctx, p, id, in)
}

// NotePatch overlays only the fields a PATCH sent.
type NotePatch struct {
	Title   *string
	Body    *string
	Pinned  *bool
	Status  *string
	Tags    []string
	HasTags bool
}

// PatchNote changes the fields that were sent and leaves the rest.
func (s *Service) PatchNote(ctx context.Context, p domain.Principal, id domain.ID, patch NotePatch) (_ domain.Note, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	prev, err := s.db.Note(ctx, id)
	if err != nil {
		return domain.Note{}, missing(err, "note")
	}
	in := NoteInput{Title: prev.Title, Body: prev.Body, Pinned: prev.Pinned, Status: prev.Status}
	if patch.Title != nil {
		in.Title = *patch.Title
	}
	if patch.Body != nil {
		in.Body = *patch.Body
	}
	if patch.Pinned != nil {
		in.Pinned = *patch.Pinned
	}
	if patch.Status != nil {
		in.Status = *patch.Status
	}
	if patch.HasTags {
		in.Tags = patch.Tags
	}
	return s.UpdateNote(ctx, p, id, in)
}

// PartyPatch overlays only the fields a PATCH sent.
type PartyPatch struct {
	Role           *string
	Primary        *bool
	RoleAttributes *string
	ServiceAddress *string
	Notes          *string
}

// PatchParty changes the fields that were sent and leaves the rest.
func (s *Service) PatchParty(ctx context.Context, p domain.Principal, id domain.ID, patch PartyPatch) (_ domain.MatterParty, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	prev, err := s.db.Party(ctx, id)
	if err != nil {
		return domain.MatterParty{}, missing(err, "party")
	}
	in := PartyInput{
		ContactID: prev.ContactID, Role: prev.Role, Primary: prev.Primary,
		RoleAttributes: prev.RoleAttributes, ServiceAddress: prev.ServiceAddress, Notes: prev.Notes,
	}
	if patch.Role != nil {
		in.Role = *patch.Role
	}
	if patch.Primary != nil {
		in.Primary = *patch.Primary
	}
	if patch.RoleAttributes != nil {
		in.RoleAttributes = *patch.RoleAttributes
	}
	if patch.ServiceAddress != nil {
		in.ServiceAddress = *patch.ServiceAddress
	}
	if patch.Notes != nil {
		in.Notes = *patch.Notes
	}
	return s.UpdateParty(ctx, p, id, in)
}

func joinAKA(parts []string) string {
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, ", ")
}
