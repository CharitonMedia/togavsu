package practice

import (
	"context"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// CreateField defines a custom field. Admin only. The schema does not change.
func (s *Service) CreateField(ctx context.Context, p domain.Principal, def FieldDef) (FieldDef, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return FieldDef{}, err
	}
	norm, err := normalizeField(def)
	if err != nil {
		return FieldDef{}, err
	}
	for _, existing := range s.db.Fields() {
		if existing.RecordType == norm.RecordType && existing.Key == norm.Key && !existing.Retired {
			return FieldDef{}, domain.Conflict("custom field key already exists")
		}
	}
	meta, err := s.meta(p)
	if err != nil {
		return FieldDef{}, err
	}
	norm.ID = meta.ID
	norm.CreatedAt = meta.CreatedAt
	norm.UpdatedAt = meta.UpdatedAt
	norm.CreatedBy = meta.CreatedBy
	if err := s.db.PutField(ctx, norm); err != nil {
		return FieldDef{}, err
	}
	if err := s.record(ctx, p, []change{{
		Entity: "custom_field_definition", EntityID: norm.ID, Field: "label", New: norm.Label, Kind: "create",
		Action: "custom_field.created", Summary: "Defined custom field " + norm.Label,
	}}); err != nil {
		return FieldDef{}, err
	}
	return norm, nil
}

// RetireField hides a field and keeps its values.
func (s *Service) RetireField(ctx context.Context, p domain.Principal, id domain.ID) (FieldDef, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return FieldDef{}, err
	}
	row, err := s.db.Field(ctx, id)
	if err != nil {
		return FieldDef{}, missing(err, "custom field")
	}
	row.Retired = true
	row.UpdatedAt = s.clock()
	if err := s.db.PutField(ctx, row); err != nil {
		return FieldDef{}, err
	}
	if err := s.record(ctx, p, []change{{
		Entity: "custom_field_definition", EntityID: row.ID, Field: "retired", Old: "false", New: "true", Kind: "update",
		Action: "custom_field.retired", Summary: "Retired custom field " + row.Label,
	}}); err != nil {
		return FieldDef{}, err
	}
	return row, nil
}

// SetCustomFields saves values for a matter or contact. A field limited to other practice areas is rejected.
func (s *Service) SetCustomFields(ctx context.Context, p domain.Principal, recordType string, recordID, area domain.ID, values []CustomInput) error {
	if err := s.gateRecord(ctx, p, recordType, recordID, domain.ActionWrite); err != nil {
		return err
	}
	if recordType == RecordMatter {
		row, err := s.db.Matter(ctx, recordID)
		if err != nil {
			return missing(err, "matter")
		}
		area = row.PracticeAreaID
	}
	return s.saveCustom(ctx, p, recordType, recordID, area, values)
}

// CustomFields returns the fields that apply to this record. Retired and out-of-area fields are omitted.
func (s *Service) CustomFields(ctx context.Context, p domain.Principal, recordType string, recordID, area domain.ID) ([]ShownField, error) {
	if err := s.gateRecord(ctx, p, recordType, recordID, domain.ActionRead); err != nil {
		return nil, err
	}
	return s.shownFields(ctx, p, recordType, recordID, area)
}

// ExportMattersCSV exports visible matters. Applicable custom fields are columns.
func (s *Service) ExportMattersCSV(ctx context.Context, p domain.Principal) (string, error) {
	views, err := s.ListMatters(ctx, p, "", domain.ID(""))
	if err != nil {
		return "", err
	}
	return s.mattersCSV(views), nil
}

func (s *Service) mattersCSV(views []MatterView) string {
	columns := []string{"number", "name", "status", "practice_area_id"}
	ids := []domain.ID{"", "", "", ""}
	seen := map[domain.ID]bool{}
	for _, view := range views {
		for _, field := range view.CustomFields {
			if seen[field.DefinitionID] {
				continue
			}
			seen[field.DefinitionID] = true
			columns = append(columns, field.Label+" ["+string(field.DefinitionID)+"]")
			ids = append(ids, field.DefinitionID)
		}
	}
	var b strings.Builder
	b.WriteString(csvJoin(columns))
	b.WriteByte('\n')
	for _, view := range views {
		row := []string{view.Matter.Number, view.Matter.Name, view.Matter.Status, string(view.Matter.PracticeAreaID)}
		byID := map[domain.ID]string{}
		for _, field := range view.CustomFields {
			byID[field.DefinitionID] = field.Value
		}
		for _, id := range ids[4:] {
			row = append(row, byID[id])
		}
		b.WriteString(csvJoin(row))
		b.WriteByte('\n')
	}
	return b.String()
}

func (s *Service) saveCustom(ctx context.Context, p domain.Principal, recordType string, recordID, area domain.ID, values []CustomInput) error {
	if len(values) == 0 {
		return s.requireCustom(ctx, p, recordType, area, recordID, nil)
	}
	if err := s.requireCustom(ctx, p, recordType, area, recordID, values); err != nil {
		return err
	}
	for _, value := range values {
		def, err := s.lookupField(recordType, value)
		if err != nil {
			return err
		}
		if def.Retired {
			return domain.Invalid("custom_fields", "field is retired")
		}
		if !fieldApplies(def, area) {
			return domain.Invalid("custom_fields", "field does not apply to this practice area")
		}
		canonical, err := canonicalValue(def, value.Value)
		if err != nil {
			return err
		}
		if def.FieldType == FieldContact && canonical != "" {
			if err := s.readableContact(ctx, p, domain.ID(canonical)); err != nil {
				return err
			}
		}
		prev := ""
		for _, existing := range s.db.Values(recordID) {
			if existing.DefinitionID == def.ID {
				prev = existing.Value
			}
		}
		if err := s.db.PutValue(ctx, FieldValue{
			DefinitionID: def.ID, Key: def.Key, RecordType: recordType, RecordID: recordID, Value: canonical,
		}); err != nil {
			return err
		}
		matterID := domain.ID("")
		contactID := domain.ID("")
		if recordType == RecordMatter {
			matterID = recordID
		}
		if recordType == RecordContact {
			contactID = recordID
		}
		if err := s.record(ctx, p, []change{{
			MatterID: matterID, ContactID: contactID, Entity: recordType, EntityID: recordID, Field: "custom:" + def.Key,
			Old: prev, New: canonical, Kind: kindOf(prev, canonical), Action: "custom_field.updated",
			Summary: "Saved " + def.Label, SkipEvent: prev == canonical,
		}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) requireCustom(ctx context.Context, p domain.Principal, recordType string, area, recordID domain.ID, incoming []CustomInput) error {
	provided := map[domain.ID]string{}
	for _, value := range incoming {
		def, err := s.lookupField(recordType, value)
		if err != nil {
			return err
		}
		if def.Retired {
			return domain.Invalid("custom_fields", "field is retired")
		}
		if !fieldApplies(def, area) {
			return domain.Invalid("custom_fields", "field does not apply to this practice area")
		}
		canonical, err := canonicalValue(def, value.Value)
		if err != nil {
			return err
		}
		if def.FieldType == FieldContact && canonical != "" {
			if err := s.readableContact(ctx, p, domain.ID(canonical)); err != nil {
				return err
			}
		}
		provided[def.ID] = canonical
	}
	stored := map[domain.ID]string{}
	if !recordID.IsZero() {
		for _, value := range s.db.Values(recordID) {
			stored[value.DefinitionID] = value.Value
		}
	}
	for _, def := range s.db.Fields() {
		if def.RecordType != recordType || def.Retired || !def.Required || !fieldApplies(def, area) {
			continue
		}
		val, ok := provided[def.ID]
		if !ok {
			val = stored[def.ID]
		}
		if strings.TrimSpace(val) == "" {
			return domain.Invalid("custom_fields", def.Label+" is required")
		}
	}
	_ = ctx
	return nil
}

func (s *Service) shownFields(ctx context.Context, p domain.Principal, recordType string, recordID, area domain.ID) ([]ShownField, error) {
	stored := map[domain.ID]string{}
	for _, value := range s.db.Values(recordID) {
		stored[value.DefinitionID] = value.Value
	}
	var out []ShownField
	for _, def := range s.db.Fields() {
		if def.RecordType != recordType || def.Retired || !fieldApplies(def, area) {
			continue
		}
		out = append(out, ShownField{
			DefinitionID: def.ID, Key: def.Key, Label: def.Label, FieldType: def.FieldType,
			Required: def.Required, Options: append([]string(nil), def.Options...),
			Value: stored[def.ID], SortOrder: def.SortOrder,
		})
	}
	for i := 1; i < len(out); i++ {
		row := out[i]
		j := i
		for j > 0 && (row.SortOrder < out[j-1].SortOrder || (row.SortOrder == out[j-1].SortOrder && row.Label < out[j-1].Label)) {
			out[j] = out[j-1]
			j--
		}
		out[j] = row
	}
	_ = ctx
	_ = p
	return out, nil
}

func (s *Service) lookupField(recordType string, in CustomInput) (FieldDef, error) {
	if !in.DefinitionID.IsZero() {
		for _, def := range s.db.Fields() {
			if def.RecordType == recordType && def.ID == in.DefinitionID {
				return def, nil
			}
		}
		return FieldDef{}, domain.Invalid("custom_fields", "unknown custom field")
	}
	var retired FieldDef
	foundRetired := false
	for _, def := range s.db.Fields() {
		if def.RecordType != recordType || in.Key == "" || def.Key != in.Key {
			continue
		}
		if !def.Retired {
			return def, nil
		}
		retired = def
		foundRetired = true
	}
	if foundRetired {
		return retired, nil
	}
	return FieldDef{}, domain.Invalid("custom_fields", "unknown custom field")
}

func (s *Service) gateRecord(ctx context.Context, p domain.Principal, recordType string, id domain.ID, action domain.Action) error {
	switch recordType {
	case RecordMatter:
		return s.gateMatter(ctx, p, id, action)
	case RecordContact:
		if action == domain.ActionRead {
			return s.readableContact(ctx, p, id)
		}
		if err := s.readableContact(ctx, p, id); err != nil {
			return err
		}
		_, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: action})
		return err
	default:
		_, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: action})
		return err
	}
}

func fieldApplies(def FieldDef, area domain.ID) bool {
	if len(def.PracticeAreaIDs) == 0 {
		return true
	}
	if area.IsZero() {
		return false
	}
	return containsID(def.PracticeAreaIDs, area)
}

func normalizeField(def FieldDef) (FieldDef, error) {
	switch def.RecordType {
	case RecordMatter, RecordContact, RecordTimeEntry, RecordExpense:
	default:
		return FieldDef{}, domain.Invalid("record_type", "unknown record type")
	}
	switch def.FieldType {
	case FieldText, FieldLongText, FieldNumber, FieldMoney, FieldDate, FieldBoolean, FieldPickList, FieldContact:
	default:
		return FieldDef{}, domain.Invalid("field_type", "unknown field type")
	}
	def.Label = strings.TrimSpace(def.Label)
	if def.Label == "" {
		return FieldDef{}, domain.Invalid("label", "label is required")
	}
	def.Key = strings.TrimSpace(def.Key)
	if def.Key == "" {
		def.Key = slug(def.Label)
	}
	if def.FieldType == FieldPickList && len(def.Options) == 0 {
		return FieldDef{}, domain.Invalid("options", "a pick list needs options")
	}
	return def, nil
}

func canonicalValue(def FieldDef, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if def.Required {
			return "", domain.Invalid("custom_fields", def.Label+" is required")
		}
		return "", nil
	}
	switch def.FieldType {
	case FieldText, FieldLongText:
		return raw, nil
	case FieldNumber:
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return "", domain.Invalid("custom_fields", def.Label+" must be a number")
		}
		return raw, nil
	case FieldMoney:
		cents, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || cents < 0 {
			return "", domain.Invalid("custom_fields", def.Label+" must be cents")
		}
		return strconv.FormatInt(cents, 10), nil
	case FieldDate:
		if !validDate(raw) || raw == "" {
			return "", domain.Invalid("custom_fields", def.Label+" must be a date")
		}
		return raw, nil
	case FieldBoolean:
		if raw != "true" && raw != "false" {
			return "", domain.Invalid("custom_fields", def.Label+" must be true or false")
		}
		return raw, nil
	case FieldPickList:
		if !containsString(def.Options, raw) {
			return "", domain.Invalid("custom_fields", def.Label+" is not an option")
		}
		return raw, nil
	case FieldContact:
		if _, err := domain.ParseID(raw); err != nil {
			return "", domain.Invalid("custom_fields", def.Label+" must be a contact id")
		}
		return raw, nil
	default:
		return "", domain.Invalid("field_type", "unknown field type")
	}
}

func slug(label string) string {
	var b strings.Builder
	prev := false
	for _, r := range strings.ToLower(label) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			prev = false
			continue
		}
		if !prev && b.Len() > 0 {
			b.WriteByte('_')
			prev = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func csvJoin(cols []string) string {
	out := make([]string, len(cols))
	for i, col := range cols {
		out[i] = csvCell(col)
	}
	return strings.Join(out, ",")
}

func csvCell(col string) string {
	if col != "" {
		switch col[0] {
		case '=', '+', '-', '@', '\t', '\r':
			col = "'" + col
		}
	}
	if strings.ContainsAny(col, ",\"\n\r") {
		return `"` + strings.ReplaceAll(col, `"`, `""`) + `"`
	}
	return col
}
