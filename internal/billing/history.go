package billing

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// History returns the audit rows for an invoice, time entry, expense or payment, newest first,
// after the same read check as the record itself (PRD 8.14 edit history).
func (s *Service) History(ctx context.Context, p domain.Principal, entity string, id domain.ID) ([]domain.AuditEntry, error) {
	switch entity {
	case "invoice":
		if _, err := s.Invoice(ctx, p, id); err != nil {
			return nil, err
		}
	case "time_entry":
		if _, err := s.TimeEntry(ctx, p, id); err != nil {
			return nil, err
		}
	case "expense":
		if _, err := s.Expense(ctx, p, id); err != nil {
			return nil, err
		}
	case "payment":
		if _, err := s.Payment(ctx, p, id); err != nil {
			return nil, err
		}
	default:
		return nil, domain.NotFound(entity)
	}
	rows, err := s.d.Audit.ForEntity(ctx, entity, id)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AuditEntry, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		out = append(out, rows[i])
	}
	return out, nil
}
