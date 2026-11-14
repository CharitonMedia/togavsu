package billing

import (
	"context"
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// CustomFields is where time entry and expense custom field values live: the practice
// service's custom fields (PRD 8.14), which hold the definitions of every record type.
// Billing authorizes the record first; *practice.Service satisfies it.
type CustomFields interface {
	SetRecordCustom(ctx context.Context, p domain.Principal, t practice.CustomTarget, values []practice.CustomInput) error
	CheckCustom(ctx context.Context, p domain.Principal, t practice.CustomTarget, values []practice.CustomInput) error
	ShownCustom(ctx context.Context, recordType string, recordID, area domain.ID) []map[string]any
}

// CustomTarget authorizes a custom field write on time entry or expense id for p (the
// same role, scope and wall checks as editing the record) and returns its target.
func (s *Service) CustomTarget(ctx context.Context, p domain.Principal, recordType string, id domain.ID) (practice.CustomTarget, error) {
	if s.d.Custom == nil {
		return practice.CustomTarget{}, domain.Conflict("custom fields are not configured")
	}
	var matter domain.ID
	res, action := domain.ResTime, "time_entry.updated"
	switch recordType {
	case practice.RecordTimeEntry:
		row, err := s.timeEntry(ctx, id)
		if err != nil {
			return practice.CustomTarget{}, err
		}
		matter = row.MatterID
	case practice.RecordExpense:
		row, err := s.expense(ctx, id)
		if err != nil {
			return practice.CustomTarget{}, err
		}
		matter, res, action = row.MatterID, domain.ResExpense, "expense.updated"
	default:
		return practice.CustomTarget{}, domain.Invalid("record_type", "billing holds time_entry and expense values")
	}
	if err := s.readBilling(ctx, p, matter); err != nil {
		return practice.CustomTarget{}, err
	}
	if err := s.allow(ctx, p, res, domain.ActionWrite, matter); err != nil {
		return practice.CustomTarget{}, err
	}
	m, err := s.d.Practice.Matter(ctx, matter)
	if err != nil {
		return practice.CustomTarget{}, domain.NotFound("matter")
	}
	return practice.CustomTarget{RecordType: recordType, RecordID: id, MatterID: matter, Area: m.PracticeAreaID, Action: action}, nil
}

// SetCustomFields writes custom field values on a time entry or expense
// (putTimeEntryCustomFields, putExpenseCustomFields): one practice unit with an audit
// row and a time_entry.updated or expense.updated event per changed value.
func (s *Service) SetCustomFields(ctx context.Context, p domain.Principal, recordType string, id domain.ID, values []practice.CustomInput) error {
	t, err := s.CustomTarget(ctx, p, recordType, id)
	if err != nil {
		return err
	}
	return s.d.Custom.SetRecordCustom(ctx, p, t, values)
}

// custom is the record's CustomFieldValue list for its view; empty without CustomFields.
func (s *Service) custom(recordType string, id, matter domain.ID) []any {
	out := []any{}
	if s.d.Custom == nil {
		return out
	}
	var area domain.ID
	if m, err := s.d.Practice.Matter(context.Background(), matter); err == nil {
		area = m.PracticeAreaID
	}
	for _, v := range s.d.Custom.ShownCustom(context.Background(), recordType, id, area) {
		out = append(out, v)
	}
	return out
}

func (h *Handler) timeView(e TimeEntry) timeJSON {
	v := timeView(e)
	v.CustomFields = h.Svc.custom(practice.RecordTimeEntry, e.ID, e.MatterID)
	return v
}

func (h *Handler) expenseView(e Expense) expenseJSON {
	v := expenseView(e)
	v.CustomFields = h.Svc.custom(practice.RecordExpense, e.ID, e.MatterID)
	return v
}

// putCustom serves PUT /api/v1/time-entries/{time_entry_id}/custom-fields and its
// expense sibling. It answers with the record as its GET does.
func (h *Handler) putCustom(recordType, param string) func(http.ResponseWriter, *http.Request, domain.Principal) {
	return func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
		var body struct {
			Values []map[string]any `json:"values"`
		}
		h.write(w, r, p, &body, func() (int, any, error) {
			if body.Values == nil {
				return 0, nil, domain.Invalid("values", "values is required")
			}
			values, err := practice.CustomInputs(body.Values)
			if err != nil {
				return 0, nil, err
			}
			id := domain.ID(r.PathValue(param))
			if err := h.Svc.SetCustomFields(r.Context(), p, recordType, id, values); err != nil {
				return 0, nil, err
			}
			if recordType == practice.RecordExpense {
				row, err := h.Svc.Expense(r.Context(), p, id)
				return http.StatusOK, h.expenseView(row), err
			}
			row, err := h.Svc.TimeEntry(r.Context(), p, id)
			return http.StatusOK, h.timeView(row), err
		})
	}
}
