package billing

import (
	"context"
	"encoding/json"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// RecordApprovedOperatingPayment is the module broker's trusted receipt sink.
// The broker validates the stored approval, target ownership, live scopes/link
// and holds the receipt row lock in this same transaction. This is not a route
// for arbitrary service-principal payment writes.
func (s *Service) RecordApprovedOperatingPayment(ctx context.Context, p domain.Principal, in PaymentInput) (json.RawMessage, error) {
	if p.Source != domain.SourceService || p.ModuleID == "" || !p.UserID.IsZero() || in.Destination != DestOperating || in.Status != PayCompleted || in.Method != "card" || in.ReferenceNumber == "" || in.Source == "" || in.ContactID.IsZero() || !in.TrustAccountID.IsZero() || !in.InvoiceID.IsZero() {
		return nil, domain.Forbidden("only the bound approved operating receipt can be recorded")
	}
	if err := amountField("amount_cents", in.AmountCents); err != nil {
		return nil, err
	}
	if _, err := s.d.Practice.Contact(ctx, in.ContactID); err != nil {
		return nil, domain.NotFound("contact")
	}
	if !in.MatterID.IsZero() {
		if _, err := s.matter(ctx, in.MatterID); err != nil {
			return nil, err
		}
		if !s.isParty(in.MatterID, in.ContactID) {
			return nil, domain.Invalid("contact_id", "must be a party on the bound matter")
		}
	}
	meta, err := s.meta(p)
	if err != nil {
		return nil, err
	}
	meta.CreatedBy = domain.ID(p.ModuleID)
	row := Payment{Payment: domain.Payment{Meta: meta, ContactID: in.ContactID, MatterID: in.MatterID, AmountCents: in.AmountCents, Destination: DestOperating, Method: "card", ReferenceNumber: in.ReferenceNumber, Status: PayCompleted, Source: in.Source, RefundState: RefundNone}, ReceivedOn: s.today(ctx)}
	if err := s.write(ctx, p, func(t *txn) error { return s.insertPayment(t, &row) }); err != nil {
		return nil, err
	}
	return json.Marshal(paymentView(row))
}
