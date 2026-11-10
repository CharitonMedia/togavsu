package billing

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// History returns the audit rows for an invoice, time entry, expense or payment, newest first,
// after the same read check as the record itself (PRD 8.14 edit history).
// Edit history is staff-only: its rows carry internal notes, dispute notes, reasons and whole
// staff records that the client allowlist (InvoiceClient) hides, so a client-only principal is
// refused here for every surface (API, pages, MCP).
func (s *Service) History(ctx context.Context, p domain.Principal, entity string, id domain.ID) ([]domain.AuditEntry, error) {
	if ClientOnly(p) {
		return nil, domain.Forbidden("edit history is visible to firm users only")
	}
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

// ClientInvoice keeps only the fields of the client allowlist (InvoiceClient) plus the due date
// and matter labels the client already sees on the PDF. Pages render a client's invoice from it.
func ClientInvoice(i Invoice) Invoice {
	var out Invoice
	out.ID, out.MatterID, out.Number, out.IssuedOn, out.Status = i.ID, i.MatterID, i.Number, i.IssuedOn, i.Status
	for _, l := range i.Lines {
		out.Lines = append(out.Lines, Line{Kind: l.Kind, Description: l.Description, AmountCents: l.AmountCents})
	}
	out.SubtotalCents, out.AdjustmentCents, out.InterestCents = i.SubtotalCents, i.AdjustmentCents, i.InterestCents
	out.TotalCents, out.AmountPaidCents, out.BalanceDueCents = i.TotalCents, i.AmountPaidCents, i.BalanceDueCents
	out.SentAt, out.PaidAt, out.ClientMessage = i.SentAt, i.PaidAt, i.ClientMessage
	out.DueOn, out.MatterName, out.MatterNum = i.DueOn, i.MatterName, i.MatterNum
	return out
}
