package practice

import (
	"context"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func (s *Service) canReadCatalog(ctx context.Context, p domain.Principal) error {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionRead}); err == nil {
		return nil
	}
	_, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead})
	return err
}

func (s *Service) ListAreas(ctx context.Context, p domain.Principal) ([]domain.PracticeArea, error) {
	if err := s.canReadCatalog(ctx, p); err != nil {
		return nil, err
	}
	return s.db.Areas(), nil
}

func (s *Service) GetArea(ctx context.Context, p domain.Principal, id domain.ID) (domain.PracticeArea, error) {
	if err := s.canReadCatalog(ctx, p); err != nil {
		return domain.PracticeArea{}, err
	}
	row, err := s.db.Area(ctx, id)
	if err != nil {
		return domain.PracticeArea{}, missing(err, "practice area")
	}
	return row, nil
}

func (s *Service) UpdateArea(ctx context.Context, p domain.Principal, id domain.ID, name string, stages []string, active *bool) (_ domain.PracticeArea, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return domain.PracticeArea{}, err
	}
	row, err := s.db.Area(ctx, id)
	if err != nil {
		return domain.PracticeArea{}, missing(err, "practice area")
	}
	oldName, oldStages, oldActive := row.Name, row.Stages, row.Active
	if strings.TrimSpace(name) != "" {
		row.Name = strings.TrimSpace(name)
	}
	if stages != nil {
		raw, err := marshalStrings(stages)
		if err != nil {
			return domain.PracticeArea{}, err
		}
		row.Stages = raw
	}
	if active != nil {
		row.Active = *active
	}
	row.UpdatedAt = s.clock()
	if err := s.db.UpdateArea(ctx, row); err != nil {
		return domain.PracticeArea{}, err
	}
	if err := s.record(ctx, p, changedFields("practice_area", row.ID, domain.ID(""), "practice_area.updated", "Updated practice area "+row.Name, [][3]string{
		{"name", oldName, row.Name},
		{"stages", oldStages, row.Stages},
		{"active", boolText(oldActive), boolText(row.Active)},
	})); err != nil {
		return domain.PracticeArea{}, err
	}
	return row, nil
}

func (s *Service) ListCounties(ctx context.Context, p domain.Principal) ([]domain.County, error) {
	if err := s.canReadCatalog(ctx, p); err != nil {
		return nil, err
	}
	return s.db.Counties(), nil
}

func (s *Service) GetCounty(ctx context.Context, p domain.Principal, id domain.ID) (domain.County, error) {
	if err := s.canReadCatalog(ctx, p); err != nil {
		return domain.County{}, err
	}
	row, err := s.db.County(ctx, id)
	if err != nil {
		return domain.County{}, missing(err, "county")
	}
	return row, nil
}

func (s *Service) UpdateCounty(ctx context.Context, p domain.Principal, id domain.ID, name, state string) (_ domain.County, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return domain.County{}, err
	}
	row, err := s.db.County(ctx, id)
	if err != nil {
		return domain.County{}, missing(err, "county")
	}
	oldName, oldState := row.Name, row.State
	if strings.TrimSpace(name) != "" {
		row.Name = strings.TrimSpace(name)
	}
	if strings.TrimSpace(state) != "" {
		row.State = strings.TrimSpace(state)
	}
	row.UpdatedAt = s.clock()
	if err := s.db.UpdateCounty(ctx, row); err != nil {
		return domain.County{}, err
	}
	if err := s.record(ctx, p, changedFields("county", row.ID, domain.ID(""), "county.updated", "Updated county "+row.Name, [][3]string{
		{"name", oldName, row.Name},
		{"state", oldState, row.State},
	})); err != nil {
		return domain.County{}, err
	}
	return row, nil
}

func (s *Service) ListReferrals(ctx context.Context, p domain.Principal) ([]domain.ReferralSource, error) {
	if err := s.canReadCatalog(ctx, p); err != nil {
		return nil, err
	}
	return s.db.Referrals(), nil
}

func (s *Service) GetReferral(ctx context.Context, p domain.Principal, id domain.ID) (domain.ReferralSource, error) {
	if err := s.canReadCatalog(ctx, p); err != nil {
		return domain.ReferralSource{}, err
	}
	row, err := s.db.Referral(ctx, id)
	if err != nil {
		return domain.ReferralSource{}, missing(err, "referral source")
	}
	return row, nil
}

func (s *Service) UpdateReferral(ctx context.Context, p domain.Principal, id domain.ID, name, kind string, active *bool) (_ domain.ReferralSource, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return domain.ReferralSource{}, err
	}
	row, err := s.db.Referral(ctx, id)
	if err != nil {
		return domain.ReferralSource{}, missing(err, "referral source")
	}
	oldName, oldKind, oldActive := row.Name, row.Kind, row.Active
	if strings.TrimSpace(name) != "" {
		row.Name = strings.TrimSpace(name)
	}
	if strings.TrimSpace(kind) != "" {
		row.Kind = strings.TrimSpace(kind)
	}
	if active != nil {
		row.Active = *active
	}
	row.UpdatedAt = s.clock()
	if err := s.db.UpdateReferral(ctx, row); err != nil {
		return domain.ReferralSource{}, err
	}
	if err := s.record(ctx, p, changedFields("referral_source", row.ID, domain.ID(""), "referral_source.updated", "Updated referral source "+row.Name, [][3]string{
		{"name", oldName, row.Name},
		{"kind", oldKind, row.Kind},
		{"active", boolText(oldActive), boolText(row.Active)},
	})); err != nil {
		return domain.ReferralSource{}, err
	}
	return row, nil
}

func (s *Service) ListParties(ctx context.Context, p domain.Principal, matter domain.ID) ([]domain.MatterParty, error) {
	if !matter.IsZero() {
		if err := s.gateMatter(ctx, p, matter, domain.ActionRead); err != nil {
			return nil, err
		}
		return s.db.Parties(matter, domain.ID("")), nil
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return nil, err
	}
	var out []domain.MatterParty
	for _, row := range s.db.Parties(domain.ID(""), domain.ID("")) {
		if s.visibleMatter(ctx, p, row.MatterID) && s.readableContact(ctx, p, row.ContactID) == nil {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *Service) GetParty(ctx context.Context, p domain.Principal, id domain.ID) (domain.MatterParty, error) {
	row, err := s.db.Party(ctx, id)
	if err != nil {
		return domain.MatterParty{}, missing(err, "party")
	}
	if err := s.gateMatter(ctx, p, row.MatterID, domain.ActionRead); err != nil {
		return domain.MatterParty{}, err
	}
	if err := s.readableContact(ctx, p, row.ContactID); err != nil {
		return domain.MatterParty{}, err
	}
	return row, nil
}

func (s *Service) ListConflicts(ctx context.Context, p domain.Principal, query string) ([]domain.ConflictRecord, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead, SharedWithClient: false}); err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var out []domain.ConflictRecord
	for _, row := range s.db.ConflictRecords() {
		if !row.MatterID.IsZero() && !s.visibleMatter(ctx, p, row.MatterID) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(row.Name), query) {
			continue
		}
		out = append(out, s.redactConflict(ctx, p, row))
	}
	return out, nil
}

func (s *Service) GetConflict(ctx context.Context, p domain.Principal, id domain.ID) (domain.ConflictRecord, error) {
	row, err := s.db.Conflict(ctx, id)
	if err != nil {
		return domain.ConflictRecord{}, missing(err, "conflict record")
	}
	if !row.MatterID.IsZero() && !s.visibleMatter(ctx, p, row.MatterID) {
		return domain.ConflictRecord{}, domain.NotFound("conflict record")
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead, MatterID: row.MatterID, SharedWithClient: false}); err != nil {
		return domain.ConflictRecord{}, err
	}
	return s.redactConflict(ctx, p, row), nil
}

func (s *Service) UpdateConflict(ctx context.Context, p domain.Principal, id domain.ID, in ConflictInput) (_ domain.ConflictRecord, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	prev, err := s.db.Conflict(ctx, id)
	if err != nil {
		return domain.ConflictRecord{}, missing(err, "conflict record")
	}
	if !prev.MatterID.IsZero() && !s.visibleMatter(ctx, p, prev.MatterID) {
		return domain.ConflictRecord{}, domain.NotFound("conflict record")
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionWrite, MatterID: prev.MatterID, SharedWithClient: false}); err != nil {
		return domain.ConflictRecord{}, err
	}
	next := prev
	if strings.TrimSpace(in.Name) != "" {
		next.Name = strings.TrimSpace(in.Name)
	}
	if in.Relationship != "" {
		next.Relationship = in.Relationship
	}
	if in.Scope != "" {
		next.Scope = in.Scope
	}
	if in.Reason != "" {
		if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResConflictReason, Action: domain.ActionWrite, MatterID: prev.MatterID}); err != nil {
			return domain.ConflictRecord{}, err
		}
		next.Reason = strings.TrimSpace(in.Reason)
	}
	next.UpdatedAt = s.clock()
	if err := s.db.UpdateConflict(ctx, next); err != nil {
		return domain.ConflictRecord{}, err
	}
	var changes []change
	if prev.Name != next.Name {
		changes = append(changes, change{MatterID: next.MatterID, Entity: "conflict_record", EntityID: next.ID, Field: "name", Old: prev.Name, New: next.Name, Kind: "update"})
	}
	if prev.Reason != next.Reason {
		changes = append(changes, change{
			MatterID: next.MatterID, Entity: "conflict_record", EntityID: next.ID, Field: "reason",
			Old: prev.Reason, New: next.Reason, Kind: "update", AttorneyOnly: true, SkipEvent: true,
		})
	}
	if len(changes) > 0 {
		changes[0].Action = "conflict_record.updated"
		changes[0].Summary = "Updated conflict record " + next.Name
		if err := s.record(ctx, p, changes); err != nil {
			return domain.ConflictRecord{}, err
		}
	}
	return s.redactConflict(ctx, p, next), nil
}

func (s *Service) GetConflictCheck(ctx context.Context, p domain.Principal, id domain.ID) (ConflictRun, error) {
	for _, run := range s.db.Runs(domain.ID("")) {
		if run.ID != id {
			continue
		}
		if err := s.gateMatterShared(ctx, p, run.MatterID, domain.ActionRead, false); err != nil {
			return ConflictRun{}, err
		}
		return s.redactRun(ctx, p, run), nil
	}
	return ConflictRun{}, domain.NotFound("conflict check")
}

func (s *Service) ListConflictChecks(ctx context.Context, p domain.Principal, matter domain.ID) ([]ConflictRun, error) {
	if !matter.IsZero() {
		return s.ConflictChecks(ctx, p, matter)
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead, SharedWithClient: false}); err != nil {
		return nil, err
	}
	var out []ConflictRun
	for _, run := range s.db.Runs(domain.ID("")) {
		if s.gateMatterShared(ctx, p, run.MatterID, domain.ActionRead, false) != nil {
			continue
		}
		out = append(out, s.redactRun(ctx, p, run))
	}
	return out, nil
}

func (s *Service) MatterWall(ctx context.Context, p domain.Principal, id domain.ID) ([]domain.MatterWall, error) {
	if err := s.gateMatter(ctx, p, id, domain.ActionRead); err != nil {
		return nil, err
	}
	return s.db.Walls(id), nil
}

func (s *Service) ListFields(ctx context.Context, p domain.Principal, recordType string, area domain.ID) ([]FieldDef, error) {
	if err := s.canReadCatalog(ctx, p); err != nil {
		return nil, err
	}
	var out []FieldDef
	for _, row := range s.db.Fields() {
		if recordType != "" && row.RecordType != recordType {
			continue
		}
		if !area.IsZero() && !fieldApplies(row, area) {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) GetField(ctx context.Context, p domain.Principal, id domain.ID) (FieldDef, error) {
	if err := s.canReadCatalog(ctx, p); err != nil {
		return FieldDef{}, err
	}
	row, err := s.db.Field(ctx, id)
	if err != nil {
		return FieldDef{}, missing(err, "custom field")
	}
	return row, nil
}

func (s *Service) UpdateField(ctx context.Context, p domain.Principal, id domain.ID, label string, required *bool, areas []domain.ID) (_ FieldDef, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return FieldDef{}, err
	}
	row, err := s.db.Field(ctx, id)
	if err != nil {
		return FieldDef{}, missing(err, "custom field")
	}
	oldLabel, oldRequired, oldAreas := row.Label, row.Required, idList(row.PracticeAreaIDs)
	if strings.TrimSpace(label) != "" {
		row.Label = strings.TrimSpace(label)
	}
	if required != nil {
		row.Required = *required
	}
	if areas != nil {
		row.PracticeAreaIDs = areas
	}
	row.UpdatedAt = s.clock()
	if err := s.db.PutField(ctx, row); err != nil {
		return FieldDef{}, err
	}
	if err := s.record(ctx, p, changedFields("custom_field_definition", row.ID, domain.ID(""), "custom_field.updated", "Updated custom field "+row.Label, [][3]string{
		{"label", oldLabel, row.Label},
		{"required", boolText(oldRequired), boolText(row.Required)},
		{"practice_area_ids", oldAreas, idList(row.PracticeAreaIDs)},
	})); err != nil {
		return FieldDef{}, err
	}
	return row, nil
}

func (s *Service) ArchiveNote(ctx context.Context, p domain.Principal, id domain.ID) (_ domain.Note, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	prev, err := s.db.Note(ctx, id)
	if err != nil {
		return domain.Note{}, missing(err, "note")
	}
	tags := []string{}
	for _, tag := range s.db.NoteTags(id) {
		tags = append(tags, tag.Tag)
	}
	return s.UpdateNote(ctx, p, id, NoteInput{
		Title: prev.Title, Body: prev.Body, Pinned: prev.Pinned, Status: NoteArchived, Tags: tags,
	})
}

func (s *Service) RevisePhone(ctx context.Context, p domain.Principal, id domain.ID, e164, from, label string) (_ domain.ContactPhone, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	current, err := s.db.Phone(ctx, id)
	if err != nil {
		return domain.ContactPhone{}, missing(err, "phone")
	}
	if e164 != "" && e164 != current.PhoneE164 {
		return s.ChangePhone(ctx, p, id, e164, from, label)
	}
	if err := s.readableContact(ctx, p, current.ContactID); err != nil {
		return domain.ContactPhone{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.ContactPhone{}, err
	}
	if label == "" || label == current.Label {
		return current, nil
	}
	old := current.Label
	current.Label = label
	current.UpdatedAt = s.clock()
	if err := s.db.UpdatePhone(ctx, current); err != nil {
		return domain.ContactPhone{}, err
	}
	if err := s.record(ctx, p, []change{{
		ContactID: current.ContactID, Entity: "contact_phone", EntityID: current.ID, Field: "label",
		Old: old, New: label, Kind: "update", Action: "contact.phone_updated", Summary: "Updated a phone label",
	}}); err != nil {
		return domain.ContactPhone{}, err
	}
	return current, nil
}

func (s *Service) ReviseEmail(ctx context.Context, p domain.Principal, id domain.ID, email, label string) (_ domain.ContactEmail, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	row, err := s.GetEmail(ctx, p, id)
	if err != nil {
		return domain.ContactEmail{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.ContactEmail{}, err
	}
	oldEmail, oldLabel := row.Email, row.Label
	if strings.TrimSpace(email) != "" {
		if !validEmail(email) {
			return domain.ContactEmail{}, domain.Invalid("email", "enter an email address")
		}
		row.Email = strings.TrimSpace(email)
	}
	if label != "" {
		row.Label = label
	}
	row.UpdatedAt = s.clock()
	if err := s.db.UpdateEmail(ctx, row); err != nil {
		return domain.ContactEmail{}, err
	}
	if err := s.record(ctx, p, changedFields("contact_email", row.ID, row.ContactID, "contact.email_updated", "Updated an email", [][3]string{
		{"email", oldEmail, row.Email},
		{"label", oldLabel, row.Label},
	})); err != nil {
		return domain.ContactEmail{}, err
	}
	return row, nil
}

func (s *Service) ReviseAddress(ctx context.Context, p domain.Principal, id domain.ID, line1, label string) (_ domain.ContactAddress, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	row, err := s.GetAddress(ctx, p, id)
	if err != nil {
		return domain.ContactAddress{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.ContactAddress{}, err
	}
	oldLine, oldLabel := row.Line1, row.Label
	if strings.TrimSpace(line1) != "" {
		row.Line1 = strings.TrimSpace(line1)
	}
	if label != "" {
		row.Label = label
	}
	row.UpdatedAt = s.clock()
	if err := s.db.UpdateAddress(ctx, row); err != nil {
		return domain.ContactAddress{}, err
	}
	if err := s.record(ctx, p, changedFields("contact_address", row.ID, row.ContactID, "contact.address_updated", "Updated an address", [][3]string{
		{"line1", oldLine, row.Line1},
		{"label", oldLabel, row.Label},
	})); err != nil {
		return domain.ContactAddress{}, err
	}
	return row, nil
}

func (s *Service) ReviseTag(ctx context.Context, p domain.Principal, id domain.ID, tag string) (_ domain.ContactTag, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	row, err := s.GetTag(ctx, p, id)
	if err != nil {
		return domain.ContactTag{}, err
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
		return domain.ContactTag{}, err
	}
	tag = strings.TrimSpace(tag)
	if tag == "" || tag == row.Tag {
		return row, nil
	}
	old := row.Tag
	row.Tag = tag
	row.UpdatedAt = s.clock()
	if err := s.db.UpdateTag(ctx, row); err != nil {
		return domain.ContactTag{}, err
	}
	if err := s.record(ctx, p, []change{{
		ContactID: row.ContactID, Entity: "contact_tag", EntityID: row.ID, Field: "tag",
		Old: old, New: tag, Kind: "update", Action: "contact.tag_updated", Summary: "Updated a tag",
	}}); err != nil {
		return domain.ContactTag{}, err
	}
	return row, nil
}

func (s *Service) GetPhone(ctx context.Context, p domain.Principal, id domain.ID) (domain.ContactPhone, error) {
	row, err := s.db.Phone(ctx, id)
	if err != nil {
		return domain.ContactPhone{}, missing(err, "phone")
	}
	if err := s.readableContact(ctx, p, row.ContactID); err != nil {
		return domain.ContactPhone{}, err
	}
	return row, nil
}

func (s *Service) ListPhones(ctx context.Context, p domain.Principal, contact domain.ID) ([]domain.ContactPhone, error) {
	return listChild(s, ctx, p, contact, s.db.Phones, func(row domain.ContactPhone) domain.ID { return row.ContactID })
}

func (s *Service) GetEmail(ctx context.Context, p domain.Principal, id domain.ID) (domain.ContactEmail, error) {
	for _, row := range s.db.Emails(domain.ID("")) {
		if row.ID == id {
			if err := s.readableContact(ctx, p, row.ContactID); err != nil {
				return domain.ContactEmail{}, err
			}
			return row, nil
		}
	}
	return domain.ContactEmail{}, domain.NotFound("email")
}

func (s *Service) ListEmails(ctx context.Context, p domain.Principal, contact domain.ID) ([]domain.ContactEmail, error) {
	return listChild(s, ctx, p, contact, s.db.Emails, func(row domain.ContactEmail) domain.ID { return row.ContactID })
}

func (s *Service) GetAddress(ctx context.Context, p domain.Principal, id domain.ID) (domain.ContactAddress, error) {
	for _, row := range s.db.Addresses(domain.ID("")) {
		if row.ID == id {
			if err := s.readableContact(ctx, p, row.ContactID); err != nil {
				return domain.ContactAddress{}, err
			}
			return row, nil
		}
	}
	return domain.ContactAddress{}, domain.NotFound("address")
}

func (s *Service) ListAddresses(ctx context.Context, p domain.Principal, contact domain.ID) ([]domain.ContactAddress, error) {
	return listChild(s, ctx, p, contact, s.db.Addresses, func(row domain.ContactAddress) domain.ID { return row.ContactID })
}

func (s *Service) GetTag(ctx context.Context, p domain.Principal, id domain.ID) (domain.ContactTag, error) {
	for _, row := range s.db.Tags(domain.ID("")) {
		if row.ID == id {
			if err := s.readableContact(ctx, p, row.ContactID); err != nil {
				return domain.ContactTag{}, err
			}
			return row, nil
		}
	}
	return domain.ContactTag{}, domain.NotFound("tag")
}

func (s *Service) ListTags(ctx context.Context, p domain.Principal, contact domain.ID) ([]domain.ContactTag, error) {
	return listChild(s, ctx, p, contact, s.db.Tags, func(row domain.ContactTag) domain.ID { return row.ContactID })
}

func listChild[T any](s *Service, ctx context.Context, p domain.Principal, contact domain.ID, all func(domain.ID) []T, idOf func(T) domain.ID) ([]T, error) {
	if !contact.IsZero() {
		if err := s.readableContact(ctx, p, contact); err != nil {
			return nil, err
		}
		return all(contact), nil
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionRead}); err != nil {
		return nil, err
	}
	var out []T
	for _, row := range all(domain.ID("")) {
		if s.readableContact(ctx, p, idOf(row)) == nil {
			out = append(out, row)
		}
	}
	return out, nil
}

func changedFields(entity string, id, contact domain.ID, action, summary string, pairs [][3]string) []change {
	var out []change
	for _, pair := range pairs {
		if pair[1] == pair[2] {
			continue
		}
		ch := change{
			Entity: entity, EntityID: id, ContactID: contact, Field: pair[0], Old: pair[1], New: pair[2], Kind: "update",
		}
		if len(out) == 0 {
			ch.Action = action
			ch.Summary = summary
		} else {
			ch.SkipEvent = true
		}
		out = append(out, ch)
	}
	return out
}

func idList(ids []domain.ID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return strings.Join(parts, ",")
}

func (s *Service) matchesCustom(record domain.ID, custom map[string]string) bool {
	if len(custom) == 0 {
		return true
	}
	values := s.db.Values(record)
	for key, want := range custom {
		found := false
		for _, value := range values {
			if value.Key == key && value.Value == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
