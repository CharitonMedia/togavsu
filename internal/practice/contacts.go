package practice

import (
	"context"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// CreateContact stores a person or organization and writes the audit log.
func (s *Service) CreateContact(ctx context.Context, p domain.Principal, in ContactInput) (domain.Contact, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.Contact{}, err
	}
	var replay domain.Contact
	hit, hold, err := s.idemBegin(ctx, p, in.IdempotencyKey, "create_contact", "", in, &replay)
	if err != nil {
		return domain.Contact{}, err
	}
	defer hold.abort()
	if hit {
		return replay, nil
	}
	if err := s.requireCustom(ctx, p, RecordContact, domain.ID(""), domain.ID(""), in.Custom); err != nil {
		return domain.Contact{}, err
	}
	row, err := s.buildContact(ctx, p, domain.Contact{}, in, true)
	if err != nil {
		return domain.Contact{}, err
	}
	if err := s.db.InsertContact(ctx, row); err != nil {
		return domain.Contact{}, conflict(err)
	}
	if err := s.saveCustom(ctx, p, RecordContact, row.ID, domain.ID(""), in.Custom); err != nil {
		return domain.Contact{}, err
	}
	if err := s.record(ctx, p, contactChanges(domain.Contact{}, row, "contact.created", "Created contact "+row.FullName)); err != nil {
		return domain.Contact{}, err
	}
	if err := hold.finish(ctx, row); err != nil {
		return domain.Contact{}, err
	}
	return row, nil
}

// UpdateContact edits a contact. A phone number change is a new historical row, not an overwrite.
func (s *Service) UpdateContact(ctx context.Context, p domain.Principal, id domain.ID, in ContactInput) (domain.Contact, error) {
	if err := s.readableContact(ctx, p, id); err != nil {
		return domain.Contact{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.Contact{}, err
	}
	prev, err := s.db.Contact(ctx, id)
	if err != nil {
		return domain.Contact{}, missing(err, "contact")
	}
	if err := s.requireCustom(ctx, p, RecordContact, domain.ID(""), prev.ID, in.Custom); err != nil {
		return domain.Contact{}, err
	}
	row, err := s.buildContact(ctx, p, prev, in, false)
	if err != nil {
		return domain.Contact{}, err
	}
	row.ID = prev.ID
	row.CreatedAt = prev.CreatedAt
	row.CreatedBy = prev.CreatedBy
	row.UpdatedAt = s.clock()
	if err := s.db.UpdateContact(ctx, row); err != nil {
		return domain.Contact{}, missing(err, "contact")
	}
	if err := s.saveCustom(ctx, p, RecordContact, row.ID, domain.ID(""), in.Custom); err != nil {
		return domain.Contact{}, err
	}
	if err := s.record(ctx, p, contactChanges(prev, row, "contact.updated", "Updated contact "+row.FullName)); err != nil {
		return domain.Contact{}, err
	}
	return row, nil
}

// ArchiveContact marks a contact archived and keeps the row.
func (s *Service) ArchiveContact(ctx context.Context, p domain.Principal, id domain.ID) (domain.Contact, error) {
	prev, err := s.db.Contact(ctx, id)
	if err != nil {
		return domain.Contact{}, missing(err, "contact")
	}
	return s.UpdateContact(ctx, p, id, ContactInput{
		Kind: prev.Kind, GivenName: prev.GivenName, MiddleName: prev.MiddleName, FamilyName: prev.FamilyName,
		Prefix: prev.Prefix, Suffix: prev.Suffix, FullName: prev.FullName, PreferredName: prev.PreferredName,
		AlsoKnownAs: prev.AlsoKnownAs, DateOfBirth: prev.DateOfBirth, DateOfDeath: prev.DateOfDeath,
		EntityType: prev.EntityType, FormationState: prev.FormationState, FormationDate: prev.FormationDate,
		Archived: true, Source: prev.Source,
	})
}

// GetContact returns a contact the caller can see. SSN is redacted unless the caller is an attorney or admin.
func (s *Service) GetContact(ctx context.Context, p domain.Principal, id domain.ID) (domain.Contact, error) {
	if err := s.readableContact(ctx, p, id); err != nil {
		return domain.Contact{}, err
	}
	row, err := s.db.Contact(ctx, id)
	if err != nil {
		return domain.Contact{}, missing(err, "contact")
	}
	return s.redactContact(ctx, p, row), nil
}

// ListContacts returns contacts the caller can see, active before archived.
func (s *Service) ListContacts(ctx context.Context, p domain.Principal) ([]domain.Contact, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionRead}); err != nil {
		return nil, err
	}
	var out []domain.Contact
	for _, row := range s.db.Contacts() {
		if s.readableContact(ctx, p, row.ID) != nil {
			continue
		}
		out = append(out, s.redactContact(ctx, p, row))
	}
	sortContacts(out)
	return out, nil
}

// AddPhone stores an E.164 phone. The previous primary stays in history when a new primary is set.
func (s *Service) AddPhone(ctx context.Context, p domain.Principal, contact domain.ID, label, phone, from string, primary bool) (domain.ContactPhone, error) {
	if err := s.readableContact(ctx, p, contact); err != nil {
		return domain.ContactPhone{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.ContactPhone{}, err
	}
	e164, err := domain.ParseE164(phone)
	if err != nil {
		return domain.ContactPhone{}, domain.Invalid("phone", "phone must be E.164")
	}
	if !validDate(from) {
		return domain.ContactPhone{}, domain.Invalid("effective_from", "use YYYY-MM-DD")
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.ContactPhone{}, err
	}
	if primary {
		if err := s.clearPrimaryPhone(ctx, p, contact); err != nil {
			return domain.ContactPhone{}, err
		}
	}
	row := domain.ContactPhone{Meta: meta, ContactID: contact, Label: label, Primary: primary, PhoneE164: e164, EffectiveFrom: from}
	if err := s.db.InsertPhone(ctx, row); err != nil {
		return domain.ContactPhone{}, err
	}
	if err := s.record(ctx, p, []change{{
		ContactID: contact, Entity: "contact_phone", EntityID: row.ID, Field: "phone_e164", New: e164, Kind: "create",
		Action: "contact.phone_added", Summary: "Added phone " + e164,
	}}); err != nil {
		return domain.ContactPhone{}, err
	}
	return row, nil
}

// ChangePhone closes the current row and inserts the new number so history is kept.
func (s *Service) ChangePhone(ctx context.Context, p domain.Principal, id domain.ID, phone, from, label string) (domain.ContactPhone, error) {
	prev, err := s.db.Phone(ctx, id)
	if err != nil {
		return domain.ContactPhone{}, missing(err, "phone")
	}
	if err := s.readableContact(ctx, p, prev.ContactID); err != nil {
		return domain.ContactPhone{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.ContactPhone{}, err
	}
	e164, err := domain.ParseE164(phone)
	if err != nil {
		return domain.ContactPhone{}, domain.Invalid("phone", "phone must be E.164")
	}
	if e164 == prev.PhoneE164 {
		return prev, nil
	}
	if from == "" {
		from = s.clock().Format("2006-01-02")
	}
	if !validDate(from) {
		return domain.ContactPhone{}, domain.Invalid("effective_from", "use YYYY-MM-DD")
	}
	oldTo := prev.EffectiveTo
	wasPrimary := prev.Primary
	prev.EffectiveTo = from
	prev.Primary = false
	prev.UpdatedAt = s.clock()
	if err := s.db.UpdatePhone(ctx, prev); err != nil {
		return domain.ContactPhone{}, err
	}
	var closed []change
	if oldTo != from {
		closed = append(closed, change{
			ContactID: prev.ContactID, Entity: "contact_phone", EntityID: prev.ID, Field: "effective_to",
			Old: oldTo, New: from, Kind: "update",
		})
	}
	if wasPrimary {
		closed = append(closed, change{
			ContactID: prev.ContactID, Entity: "contact_phone", EntityID: prev.ID, Field: "primary",
			Old: "true", New: "false", Kind: "update",
		})
	}
	if len(closed) > 0 {
		closed[0].Action = "contact.phone_changed"
		closed[0].Summary = "Closed phone " + prev.PhoneE164
		for i := 1; i < len(closed); i++ {
			closed[i].SkipEvent = true
		}
		if err := s.record(ctx, p, closed); err != nil {
			return domain.ContactPhone{}, err
		}
	}
	nextLabel := prev.Label
	if strings.TrimSpace(label) != "" {
		nextLabel = strings.TrimSpace(label)
	}
	row, err := s.AddPhone(ctx, p, prev.ContactID, nextLabel, e164, from, wasPrimary)
	if err != nil {
		return domain.ContactPhone{}, err
	}
	if nextLabel != prev.Label {
		if err := s.record(ctx, p, []change{{
			ContactID: row.ContactID, Entity: "contact_phone", EntityID: row.ID, Field: "label",
			Old: prev.Label, New: nextLabel, Kind: "create", SkipEvent: true,
		}}); err != nil {
			return domain.ContactPhone{}, err
		}
	}
	return row, nil
}

// AddEmail stores an email without overwriting an earlier one.
func (s *Service) AddEmail(ctx context.Context, p domain.Principal, contact domain.ID, label, email, from string, primary bool) (domain.ContactEmail, error) {
	if err := s.readableContact(ctx, p, contact); err != nil {
		return domain.ContactEmail{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.ContactEmail{}, err
	}
	email = strings.TrimSpace(email)
	if !validEmail(email) {
		return domain.ContactEmail{}, domain.Invalid("email", "enter an email address")
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.ContactEmail{}, err
	}
	row := domain.ContactEmail{Meta: meta, ContactID: contact, Label: label, Primary: primary, Email: email, EffectiveFrom: from}
	if err := s.db.InsertEmail(ctx, row); err != nil {
		return domain.ContactEmail{}, err
	}
	if err := s.record(ctx, p, []change{{
		ContactID: contact, Entity: "contact_email", EntityID: row.ID, Field: "email", New: email, Kind: "create",
		Action: "contact.email_added", Summary: "Added email " + email,
	}}); err != nil {
		return domain.ContactEmail{}, err
	}
	return row, nil
}

// AddAddress stores an address without overwriting an earlier one.
func (s *Service) AddAddress(ctx context.Context, p domain.Principal, row domain.ContactAddress) (domain.ContactAddress, error) {
	if err := s.readableContact(ctx, p, row.ContactID); err != nil {
		return domain.ContactAddress{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.ContactAddress{}, err
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.ContactAddress{}, err
	}
	row.Meta = meta
	if err := s.db.InsertAddress(ctx, row); err != nil {
		return domain.ContactAddress{}, err
	}
	if err := s.record(ctx, p, []change{{
		ContactID: row.ContactID, Entity: "contact_address", EntityID: row.ID, Field: "line1", New: row.Line1, Kind: "create",
		Action: "contact.address_added", Summary: "Added an address",
	}}); err != nil {
		return domain.ContactAddress{}, err
	}
	return row, nil
}

// AddContactTag adds a free tag.
func (s *Service) AddContactTag(ctx context.Context, p domain.Principal, contact domain.ID, tag string) (domain.ContactTag, error) {
	if err := s.readableContact(ctx, p, contact); err != nil {
		return domain.ContactTag{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.ContactTag{}, err
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return domain.ContactTag{}, domain.Invalid("tag", "tag is required")
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.ContactTag{}, err
	}
	row := domain.ContactTag{Meta: meta, ContactID: contact, Tag: tag}
	if err := s.db.InsertTag(ctx, row); err != nil {
		return domain.ContactTag{}, err
	}
	if err := s.record(ctx, p, []change{{
		ContactID: contact, Entity: "contact_tag", EntityID: row.ID, Field: "tag", New: tag, Kind: "create",
		Action: "contact.tag_added", Summary: "Tagged the contact " + tag,
	}}); err != nil {
		return domain.ContactTag{}, err
	}
	return row, nil
}

func (s *Service) buildContact(ctx context.Context, p domain.Principal, prev domain.Contact, in ContactInput, create bool) (domain.Contact, error) {
	kind := in.Kind
	if kind == "" {
		kind = prev.Kind
	}
	if kind != KindPerson && kind != KindOrganization {
		return domain.Contact{}, domain.Invalid("kind", "kind must be person or organization")
	}
	if !validDate(in.DateOfBirth) || !validDate(in.DateOfDeath) || !validDate(in.FormationDate) {
		return domain.Contact{}, domain.Invalid("date", "use YYYY-MM-DD")
	}
	full := strings.TrimSpace(in.FullName)
	if kind == KindPerson && full == "" {
		full = strings.TrimSpace(strings.Join([]string{in.Prefix, in.GivenName, in.MiddleName, in.FamilyName, in.Suffix}, " "))
		full = strings.Join(strings.Fields(full), " ")
	}
	if full == "" {
		return domain.Contact{}, domain.Invalid("name", "name is required")
	}
	ssn := prev.SSNEINCiphertext
	if in.SSNEIN != "" {
		if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSensitiveContact, Action: domain.ActionWrite}); err != nil {
			return domain.Contact{}, err
		}
		sealed, err := s.sealSSN(in.SSNEIN)
		if err != nil {
			return domain.Contact{}, err
		}
		ssn = sealed
	}
	meta := prev.Meta
	if create {
		var err error
		meta, err = s.meta(p)
		if err != nil {
			return domain.Contact{}, err
		}
	}
	return domain.Contact{
		Meta: meta, Kind: kind, GivenName: in.GivenName, MiddleName: in.MiddleName, FamilyName: in.FamilyName,
		Prefix: in.Prefix, Suffix: in.Suffix, FullName: full, PreferredName: in.PreferredName, AlsoKnownAs: in.AlsoKnownAs,
		DateOfBirth: in.DateOfBirth, DateOfDeath: in.DateOfDeath, SSNEINCiphertext: ssn, EntityType: in.EntityType,
		FormationState: in.FormationState, FormationDate: in.FormationDate, Archived: in.Archived, Source: in.Source,
	}, nil
}

func (s *Service) sealSSN(plain string) (string, error) {
	if len(s.key) != 32 {
		return "", domain.Invalid("ssn_ein", "identifier must already be encrypted")
	}
	sealed, err := sealIdentifier(s.key, plain)
	if err != nil {
		return "", domain.Invalid("ssn_ein", "identifier must already be encrypted")
	}
	if !strings.HasPrefix(plain, sealPrefix) && strings.Contains(sealed, plain) {
		return "", domain.Invalid("ssn_ein", "identifier must already be encrypted")
	}
	return sealed, nil
}

func (s *Service) redactContact(ctx context.Context, p domain.Principal, row domain.Contact) domain.Contact {
	d, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSensitiveContact, Action: domain.ActionRead})
	if err != nil || d.Redact || !d.Allow {
		row.SSNEINCiphertext = ""
		return row
	}
	if row.SSNEINCiphertext == "" {
		return row
	}
	plain, err := openIdentifier(s.key, row.SSNEINCiphertext)
	if err != nil {
		row.SSNEINCiphertext = ""
		return row
	}
	row.SSNEINCiphertext = plain
	return row
}

func (s *Service) readableContact(ctx context.Context, p domain.Principal, id domain.ID) error {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionRead}); err != nil {
		return err
	}
	if _, err := s.db.Contact(ctx, id); err != nil {
		return missing(err, "contact")
	}
	parties := s.db.Parties(domain.ID(""), id)
	if len(parties) == 0 {
		return nil
	}
	for _, party := range parties {
		if s.visibleMatter(ctx, p, party.MatterID) {
			return nil
		}
	}
	return domain.NotFound("contact")
}

func (s *Service) clearPrimaryPhone(ctx context.Context, p domain.Principal, contact domain.ID) error {
	for _, phone := range s.db.Phones(contact) {
		if !phone.Primary {
			continue
		}
		phone.Primary = false
		phone.UpdatedAt = s.clock()
		if err := s.db.UpdatePhone(ctx, phone); err != nil {
			return err
		}
		if err := s.record(ctx, p, []change{{
			ContactID: contact, Entity: "contact_phone", EntityID: phone.ID, Field: "primary",
			Old: "true", New: "false", Kind: "update", SkipEvent: true,
		}}); err != nil {
			return err
		}
	}
	return nil
}

func contactChanges(prev, next domain.Contact, action, summary string) []change {
	pairs := [][2]string{
		{"kind", prev.Kind + "\x00" + next.Kind},
		{"given_name", prev.GivenName + "\x00" + next.GivenName},
		{"middle_name", prev.MiddleName + "\x00" + next.MiddleName},
		{"family_name", prev.FamilyName + "\x00" + next.FamilyName},
		{"prefix", prev.Prefix + "\x00" + next.Prefix},
		{"suffix", prev.Suffix + "\x00" + next.Suffix},
		{"full_name", prev.FullName + "\x00" + next.FullName},
		{"preferred_name", prev.PreferredName + "\x00" + next.PreferredName},
		{"also_known_as", prev.AlsoKnownAs + "\x00" + next.AlsoKnownAs},
		{"date_of_birth", prev.DateOfBirth + "\x00" + next.DateOfBirth},
		{"date_of_death", prev.DateOfDeath + "\x00" + next.DateOfDeath},
		{"entity_type", prev.EntityType + "\x00" + next.EntityType},
		{"formation_state", prev.FormationState + "\x00" + next.FormationState},
		{"formation_date", prev.FormationDate + "\x00" + next.FormationDate},
		{"source", prev.Source + "\x00" + next.Source},
		{"archived", boolText(prev.Archived) + "\x00" + boolText(next.Archived)},
		{"ssn_ein_ciphertext", ssnAuditPair(prev.SSNEINCiphertext, next.SSNEINCiphertext)},
	}
	var out []change
	first := true
	for _, pair := range pairs {
		old, neu, _ := strings.Cut(pair[1], "\x00")
		if old == neu {
			continue
		}
		ch := change{
			ContactID: next.ID, Entity: "contact", EntityID: next.ID, Field: pair[0], Old: old, New: neu,
			Kind: kindOf(old, neu), AttorneyOnly: pair[0] == "ssn_ein_ciphertext",
		}
		if first {
			ch.Action = action
			ch.Summary = summary
			first = false
		} else {
			ch.SkipEvent = true
		}
		out = append(out, ch)
	}
	return out
}

// ssnAuditPair records that an identifier changed. The value itself stays out of the audit log.
func ssnAuditPair(prev, next string) string {
	if prev == next {
		return "\x00"
	}
	old, neu := "", ""
	if prev != "" {
		old = "previous"
	}
	if next != "" {
		neu = "changed"
	}
	return old + "\x00" + neu
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func kindOf(old, neu string) string {
	if old == "" && neu != "" {
		return "create"
	}
	if neu == "" && old != "" {
		return "delete"
	}
	return "update"
}

func sortContacts(rows []domain.Contact) {
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		j := i
		for j > 0 && contactLess(row, rows[j-1]) {
			rows[j] = rows[j-1]
			j--
		}
		rows[j] = row
	}
}

func contactLess(a, b domain.Contact) bool {
	if a.Archived != b.Archived {
		return !a.Archived
	}
	return strings.ToLower(a.FullName) < strings.ToLower(b.FullName)
}
