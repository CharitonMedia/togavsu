package practice

import (
	"context"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// AddParty links a contact to a matter. The matter shows an unchecked-conflicts badge until a check includes the party.
func (s *Service) AddParty(ctx context.Context, p domain.Principal, matter domain.ID, in PartyInput) (domain.MatterParty, error) {
	if err := s.gateMatter(ctx, p, matter, domain.ActionWrite); err != nil {
		return domain.MatterParty{}, err
	}
	if _, err := s.db.Contact(ctx, in.ContactID); err != nil {
		return domain.MatterParty{}, domain.Invalid("contact_id", "unknown contact")
	}
	roles := s.listSetting(ctx, "practice.party_roles", domain.DefaultPartyRoles())
	if !containsString(roles, in.Role) {
		return domain.MatterParty{}, domain.Invalid("role", "unknown party role")
	}
	if in.Primary {
		for _, existing := range s.db.Parties(matter, domain.ID("")) {
			if existing.Role == in.Role && existing.Primary {
				existing.Primary = false
				existing.UpdatedAt = s.clock()
				if err := s.db.UpdateParty(ctx, existing); err != nil {
					return domain.MatterParty{}, err
				}
			}
		}
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.MatterParty{}, err
	}
	row := domain.MatterParty{
		Meta: meta, MatterID: matter, ContactID: in.ContactID, Role: in.Role, Primary: in.Primary,
		RoleAttributes: in.RoleAttributes, ServiceAddress: in.ServiceAddress, Notes: in.Notes,
	}
	if err := s.db.InsertParty(ctx, row); err != nil {
		return domain.MatterParty{}, err
	}
	contact, _ := s.db.Contact(ctx, in.ContactID)
	if err := s.record(ctx, p, []change{{
		MatterID: matter, Entity: "matter_party", EntityID: row.ID, Field: "role", New: in.Role, Kind: "create",
		Action: "matter.party_added", Summary: "Added " + contact.FullName + " as " + in.Role,
	}}); err != nil {
		return domain.MatterParty{}, err
	}
	return row, nil
}

// UpdateParty edits a party.
func (s *Service) UpdateParty(ctx context.Context, p domain.Principal, id domain.ID, in PartyInput) (domain.MatterParty, error) {
	prev, err := s.db.Party(ctx, id)
	if err != nil {
		return domain.MatterParty{}, missing(err, "party")
	}
	if err := s.gateMatter(ctx, p, prev.MatterID, domain.ActionWrite); err != nil {
		return domain.MatterParty{}, err
	}
	roles := s.listSetting(ctx, "practice.party_roles", domain.DefaultPartyRoles())
	if !containsString(roles, in.Role) {
		return domain.MatterParty{}, domain.Invalid("role", "unknown party role")
	}
	next := prev
	next.Role = in.Role
	next.Primary = in.Primary
	next.RoleAttributes = in.RoleAttributes
	next.ServiceAddress = in.ServiceAddress
	next.Notes = in.Notes
	next.UpdatedAt = s.clock()
	if err := s.db.UpdateParty(ctx, next); err != nil {
		return domain.MatterParty{}, err
	}
	if err := s.record(ctx, p, partyChanges(prev, next)); err != nil {
		return domain.MatterParty{}, err
	}
	return next, nil
}

func partyChanges(prev, next domain.MatterParty) []change {
	type pair struct{ field, old, neu string }
	rows := []pair{
		{"role", prev.Role, next.Role},
		{"primary", boolText(prev.Primary), boolText(next.Primary)},
		{"role_attributes", prev.RoleAttributes, next.RoleAttributes},
		{"service_address", prev.ServiceAddress, next.ServiceAddress},
		{"notes", prev.Notes, next.Notes},
	}
	var out []change
	first := true
	for _, row := range rows {
		if row.old == row.neu {
			continue
		}
		ch := change{
			MatterID: next.MatterID, Entity: "matter_party", EntityID: next.ID,
			Field: row.field, Old: row.old, New: row.neu, Kind: "update",
		}
		if first {
			ch.Action = "matter.party_updated"
			ch.Summary = "Updated a party"
			first = false
		} else {
			ch.SkipEvent = true
		}
		out = append(out, ch)
	}
	return out
}

func (s *Service) unchecked(matter domain.ID) bool {
	for _, party := range s.db.Parties(matter, domain.ID("")) {
		checked, ok := s.db.Checked(party.ID)
		if !ok || checked.Before(party.UpdatedAt) {
			return true
		}
	}
	return false
}

// CreateArea stores a practice area. Admin only.
func (s *Service) CreateArea(ctx context.Context, p domain.Principal, name, matterTypes string, stages []string) (domain.PracticeArea, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return domain.PracticeArea{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.PracticeArea{}, domain.Invalid("name", "name is required")
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.PracticeArea{}, err
	}
	raw, err := marshalStrings(stages)
	if err != nil {
		return domain.PracticeArea{}, err
	}
	row := domain.PracticeArea{Meta: meta, Name: name, MatterTypes: matterTypes, Stages: raw, Active: true}
	if err := s.db.InsertArea(ctx, row); err != nil {
		return domain.PracticeArea{}, err
	}
	return row, nil
}

// CreateCounty stores a county.
func (s *Service) CreateCounty(ctx context.Context, p domain.Principal, name, state string) (domain.County, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return domain.County{}, err
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.County{}, err
	}
	row := domain.County{Meta: meta, Name: strings.TrimSpace(name), State: strings.TrimSpace(state)}
	if row.Name == "" || row.State == "" {
		return domain.County{}, domain.Invalid("name", "name and state are required")
	}
	if err := s.db.InsertCounty(ctx, row); err != nil {
		return domain.County{}, err
	}
	return row, nil
}

// CreateReferral stores a referral source.
func (s *Service) CreateReferral(ctx context.Context, p domain.Principal, name, kind string, contact domain.ID) (domain.ReferralSource, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return domain.ReferralSource{}, err
	}
	switch kind {
	case "person", "organization", "advertising", "web", "other":
	default:
		return domain.ReferralSource{}, domain.Invalid("kind", "unknown referral kind")
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.ReferralSource{}, err
	}
	row := domain.ReferralSource{Meta: meta, Name: strings.TrimSpace(name), Kind: kind, ContactID: contact, Active: true}
	if row.Name == "" {
		return domain.ReferralSource{}, domain.Invalid("name", "name is required")
	}
	if err := s.db.InsertReferral(ctx, row); err != nil {
		return domain.ReferralSource{}, err
	}
	return row, nil
}
