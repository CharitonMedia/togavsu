package practice

import (
	"context"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// MeContact is the client's linked contact primary email, phone and mailing address (CORE-13).
type MeContact struct {
	ContactID domain.ID
	Email     string
	Phone     string
	Address   *MeMailingAddress
}

// MeMailingAddress is the postal fields of the current primary mailing address.
type MeMailingAddress struct {
	Line1      string
	Line2      string
	City       string
	Region     string
	PostalCode string
	Country    string
}

// MeContactPatch is the optional fields on PATCH /api/v1/me/contact.
type MeContactPatch struct {
	Email   *string
	Phone   *string
	Address *MeMailingAddress
}

// GetMeContact returns the linked contact's current primary email, phone and mailing address.
func (s *Service) GetMeContact(ctx context.Context, p domain.Principal) (MeContact, error) {
	id, err := s.linkedClientContact(ctx, p)
	if err != nil {
		return MeContact{}, err
	}
	return s.readMeContact(id), nil
}

// UpdateMeContact versions primary email, phone and mailing address for the linked contact.
// Each change sets effective_to on the old primary row and inserts a new primary row (PRD 5),
// audits with the client as actor, and emits contact.updated with a client-visible timeline row.
func (s *Service) UpdateMeContact(ctx context.Context, p domain.Principal, in MeContactPatch) (_ MeContact, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	id, err := s.linkedClientContact(ctx, p)
	if err != nil {
		return MeContact{}, err
	}
	today := s.clock().Format("2006-01-02")
	var audits []change
	changed := false

	if in.Email != nil {
		next := strings.TrimSpace(*in.Email)
		if !validEmail(next) {
			return MeContact{}, domain.Invalid("email", "enter an email address")
		}
		cur := currentPrimaryEmailRow(s.db.Emails(id), today)
		if cur == nil || cur.Email != next {
			rows, err := s.versionPrimaryEmail(ctx, p, id, cur, next, today)
			if err != nil {
				return MeContact{}, err
			}
			audits = append(audits, rows...)
			changed = true
		}
	}
	if in.Phone != nil {
		e164, err := domain.ParseE164(strings.TrimSpace(*in.Phone))
		if err != nil {
			return MeContact{}, domain.Invalid("phone", "phone must be E.164")
		}
		cur := currentPrimaryPhoneRow(s.db.Phones(id), today)
		if cur == nil || cur.PhoneE164 != e164 {
			rows, err := s.versionPrimaryPhone(ctx, p, id, cur, e164, today)
			if err != nil {
				return MeContact{}, err
			}
			audits = append(audits, rows...)
			changed = true
		}
	}
	if in.Address != nil {
		addr := *in.Address
		addr.Line1 = strings.TrimSpace(addr.Line1)
		addr.Line2 = strings.TrimSpace(addr.Line2)
		addr.City = strings.TrimSpace(addr.City)
		addr.Region = strings.TrimSpace(addr.Region)
		addr.PostalCode = strings.TrimSpace(addr.PostalCode)
		addr.Country = strings.TrimSpace(addr.Country)
		if addr.Line1 == "" {
			return MeContact{}, domain.Invalid("address.line1", "line1 is required")
		}
		cur := currentPrimaryAddressRow(s.db.Addresses(id), today)
		if cur == nil || !sameMailingAddress(*cur, addr) {
			rows, err := s.versionPrimaryAddress(ctx, p, id, cur, addr, today)
			if err != nil {
				return MeContact{}, err
			}
			audits = append(audits, rows...)
			changed = true
		}
	}
	if !changed {
		return s.readMeContact(id), nil
	}
	matter := s.firstClientMatter(ctx, id)
	audits = append(audits, change{
		MatterID: matter, ContactID: id, Entity: "contact", EntityID: id,
		Field: "primary_details", Old: "", New: "updated", Kind: "update",
		Action: "contact.updated", Summary: "Updated contact details", ClientVisible: true,
	})
	for i := 0; i < len(audits)-1; i++ {
		audits[i].SkipEvent = true
	}
	if err := s.record(ctx, p, audits); err != nil {
		return MeContact{}, err
	}
	return s.readMeContact(id), nil
}

func (s *Service) linkedClientContact(ctx context.Context, p domain.Principal) (domain.ID, error) {
	if actorKind(p) != "client" {
		return "", domain.Forbidden("client role is required")
	}
	user, err := s.db.User(ctx, p.UserID)
	if err != nil || user.ContactID.IsZero() {
		return "", domain.NotFound("contact")
	}
	if _, err := s.db.Contact(ctx, user.ContactID); err != nil {
		return "", missing(err, "contact")
	}
	return user.ContactID, nil
}

func (s *Service) readMeContact(id domain.ID) MeContact {
	today := s.clock().Format("2006-01-02")
	out := MeContact{ContactID: id}
	if e := currentPrimaryEmailRow(s.db.Emails(id), today); e != nil {
		out.Email = e.Email
	}
	if p := currentPrimaryPhoneRow(s.db.Phones(id), today); p != nil {
		out.Phone = p.PhoneE164
	}
	if a := currentPrimaryAddressRow(s.db.Addresses(id), today); a != nil {
		out.Address = &MeMailingAddress{
			Line1: a.Line1, Line2: a.Line2, City: a.City,
			Region: a.Region, PostalCode: a.PostalCode, Country: a.Country,
		}
	}
	return out
}

func (s *Service) firstClientMatter(_ context.Context, contact domain.ID) domain.ID {
	for _, party := range s.db.Parties(domain.ID(""), contact) {
		if party.Role == "client" || party.Role == "authorized user" {
			return party.MatterID
		}
	}
	return ""
}

func (s *Service) versionPrimaryEmail(ctx context.Context, p domain.Principal, contact domain.ID, prev *domain.ContactEmail, email, from string) ([]change, error) {
	var out []change
	if prev != nil {
		oldTo := prev.EffectiveTo
		wasPrimary := prev.Primary
		prev.EffectiveTo = from
		prev.Primary = false
		prev.UpdatedAt = s.clock()
		if err := s.db.UpdateEmail(ctx, *prev); err != nil {
			return nil, err
		}
		if oldTo != from {
			out = append(out, change{
				ContactID: contact, Entity: "contact_email", EntityID: prev.ID, Field: "effective_to",
				Old: oldTo, New: from, Kind: "update",
			})
		}
		if wasPrimary {
			out = append(out, change{
				ContactID: contact, Entity: "contact_email", EntityID: prev.ID, Field: "primary",
				Old: "true", New: "false", Kind: "update",
			})
		}
	}
	meta, err := s.meta(p)
	if err != nil {
		return nil, err
	}
	label := "primary"
	if prev != nil && strings.TrimSpace(prev.Label) != "" {
		label = prev.Label
	}
	row := domain.ContactEmail{Meta: meta, ContactID: contact, Label: label, Primary: true, Email: email, EffectiveFrom: from}
	if err := s.db.InsertEmail(ctx, row); err != nil {
		return nil, err
	}
	out = append(out, change{
		ContactID: contact, Entity: "contact_email", EntityID: row.ID, Field: "email",
		New: email, Kind: "create",
	})
	return out, nil
}

func (s *Service) versionPrimaryPhone(ctx context.Context, p domain.Principal, contact domain.ID, prev *domain.ContactPhone, e164, from string) ([]change, error) {
	var out []change
	if prev != nil {
		oldTo := prev.EffectiveTo
		wasPrimary := prev.Primary
		prev.EffectiveTo = from
		prev.Primary = false
		prev.UpdatedAt = s.clock()
		if err := s.db.UpdatePhone(ctx, *prev); err != nil {
			return nil, err
		}
		if oldTo != from {
			out = append(out, change{
				ContactID: contact, Entity: "contact_phone", EntityID: prev.ID, Field: "effective_to",
				Old: oldTo, New: from, Kind: "update",
			})
		}
		if wasPrimary {
			out = append(out, change{
				ContactID: contact, Entity: "contact_phone", EntityID: prev.ID, Field: "primary",
				Old: "true", New: "false", Kind: "update",
			})
		}
	}
	meta, err := s.meta(p)
	if err != nil {
		return nil, err
	}
	label := "primary"
	if prev != nil && strings.TrimSpace(prev.Label) != "" {
		label = prev.Label
	}
	row := domain.ContactPhone{Meta: meta, ContactID: contact, Label: label, Primary: true, PhoneE164: e164, EffectiveFrom: from}
	if err := s.db.InsertPhone(ctx, row); err != nil {
		return nil, err
	}
	out = append(out, change{
		ContactID: contact, Entity: "contact_phone", EntityID: row.ID, Field: "phone_e164",
		New: e164, Kind: "create",
	})
	return out, nil
}

func (s *Service) versionPrimaryAddress(ctx context.Context, p domain.Principal, contact domain.ID, prev *domain.ContactAddress, addr MeMailingAddress, from string) ([]change, error) {
	var out []change
	if prev != nil {
		oldTo := prev.EffectiveTo
		wasPrimary := prev.Primary
		prev.EffectiveTo = from
		prev.Primary = false
		prev.UpdatedAt = s.clock()
		if err := s.db.UpdateAddress(ctx, *prev); err != nil {
			return nil, err
		}
		if oldTo != from {
			out = append(out, change{
				ContactID: contact, Entity: "contact_address", EntityID: prev.ID, Field: "effective_to",
				Old: oldTo, New: from, Kind: "update",
			})
		}
		if wasPrimary {
			out = append(out, change{
				ContactID: contact, Entity: "contact_address", EntityID: prev.ID, Field: "primary",
				Old: "true", New: "false", Kind: "update",
			})
		}
	}
	meta, err := s.meta(p)
	if err != nil {
		return nil, err
	}
	label := "mailing"
	if prev != nil && strings.TrimSpace(prev.Label) != "" {
		label = prev.Label
	}
	row := domain.ContactAddress{
		Meta: meta, ContactID: contact, Label: label, Primary: true,
		Line1: addr.Line1, Line2: addr.Line2, City: addr.City, Region: addr.Region,
		PostalCode: addr.PostalCode, Country: addr.Country, EffectiveFrom: from,
	}
	if err := s.db.InsertAddress(ctx, row); err != nil {
		return nil, err
	}
	out = append(out, change{
		ContactID: contact, Entity: "contact_address", EntityID: row.ID, Field: "line1",
		New: addr.Line1, Kind: "create",
	})
	return out, nil
}

func currentPrimaryEmailRow(rows []domain.ContactEmail, today string) *domain.ContactEmail {
	for i := range rows {
		e := &rows[i]
		if !e.Primary || e.Email == "" {
			continue
		}
		if !effectiveOn(e.EffectiveFrom, e.EffectiveTo, today) {
			continue
		}
		return e
	}
	return nil
}

func currentPrimaryPhoneRow(rows []domain.ContactPhone, today string) *domain.ContactPhone {
	for i := range rows {
		p := &rows[i]
		if !p.Primary || p.PhoneE164 == "" {
			continue
		}
		if !effectiveOn(p.EffectiveFrom, p.EffectiveTo, today) {
			continue
		}
		return p
	}
	return nil
}

func currentPrimaryAddressRow(rows []domain.ContactAddress, today string) *domain.ContactAddress {
	for i := range rows {
		a := &rows[i]
		if !a.Primary || a.Line1 == "" {
			continue
		}
		if !effectiveOn(a.EffectiveFrom, a.EffectiveTo, today) {
			continue
		}
		return a
	}
	return nil
}

func effectiveOn(from, to, today string) bool {
	if to != "" && to <= today {
		return false
	}
	if from != "" && from > today {
		return false
	}
	return true
}

func sameMailingAddress(prev domain.ContactAddress, next MeMailingAddress) bool {
	return prev.Line1 == next.Line1 && prev.Line2 == next.Line2 && prev.City == next.City &&
		prev.Region == next.Region && prev.PostalCode == next.PostalCode && prev.Country == next.Country
}
