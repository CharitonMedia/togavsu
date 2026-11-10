package billing

import (
	"context"
	"errors"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// PaymentInput records a payment. Status defaults to completed.
type PaymentInput struct {
	ContactID       domain.ID
	MatterID        domain.ID
	AmountCents     domain.Cents
	Destination     string
	TrustAccountID  domain.ID
	Method          string
	ReferenceNumber string
	Status          string
	InvoiceID       domain.ID
	Source          string
	ReceivedOn      string
}

// PaymentFilter narrows a payment list.
type PaymentFilter struct {
	MatterID  domain.ID
	InvoiceID domain.ID
	FromDate  string
	ToDate    string
	Status    string
}

var methods = map[string]bool{"cash": true, "check": true, "card": true, "ach": true, MethodInternal: true, "other": true}

// ErrTrustUnavailable is returned when a trust operation runs with no trust service wired.
var ErrTrustUnavailable = domain.Conflict("the trust service is not available")

func (s *Service) payment(ctx context.Context, id domain.ID) (Payment, error) {
	var row Payment
	if err := s.d.Store.Get(ctx, kindPayment, string(id), &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Payment{}, domain.NotFound("payment")
		}
		return Payment{}, err
	}
	return row, nil
}

// Payment returns one payment the caller may read.
func (s *Service) Payment(ctx context.Context, p domain.Principal, id domain.ID) (Payment, error) {
	row, err := s.payment(ctx, id)
	if err != nil {
		return Payment{}, err
	}
	if err := s.allow(ctx, p, domain.ResPayment, domain.ActionRead, row.MatterID); err != nil {
		return Payment{}, err
	}
	return row, nil
}

// Payments lists payments on matters the caller can see.
func (s *Service) Payments(ctx context.Context, p domain.Principal, f PaymentFilter) ([]Payment, error) {
	if err := s.allow(ctx, p, domain.ResPayment, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	rows, err := listOf[Payment](ctx, s.d.Store, kindPayment)
	if err != nil {
		return nil, err
	}
	var out []Payment
	for _, r := range rows {
		if !f.MatterID.IsZero() && r.MatterID != f.MatterID || !f.InvoiceID.IsZero() && r.InvoiceID != f.InvoiceID ||
			f.FromDate != "" && r.ReceivedOn < f.FromDate || f.ToDate != "" && r.ReceivedOn > f.ToDate ||
			f.Status != "" && r.Status != f.Status {
			continue
		}
		if s.allow(ctx, p, domain.ResPayment, domain.ActionRead, r.MatterID) == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Service) buildPayment(ctx context.Context, p domain.Principal, in PaymentInput) (Payment, error) {
	if err := amountField("amount_cents", in.AmountCents); err != nil {
		return Payment{}, err
	}
	if !methods[in.Method] {
		return Payment{}, domain.Invalid("method", "must be cash, check, card, ach, internal_transfer or other")
	}
	if in.Method == MethodInternal {
		return Payment{}, domain.Invalid("method", "internal_transfer is reserved for applying trust")
	}
	if in.Status == "" {
		in.Status = PayCompleted
	}
	switch in.Status {
	case PayPending, PayCompleted, PayFailed:
	default:
		return Payment{}, domain.Invalid("status", "a new payment is pending, completed or failed")
	}
	if err := s.allow(ctx, p, domain.ResPayment, domain.ActionWrite, in.MatterID); err != nil {
		return Payment{}, err
	}
	if _, err := s.matter(ctx, in.MatterID); err != nil {
		return Payment{}, err
	}
	if in.ContactID.IsZero() || !s.isParty(in.MatterID, in.ContactID) {
		return Payment{}, domain.Invalid("contact_id", "must be a party on the matter")
	}
	switch in.Destination {
	case DestOperating:
		if !in.TrustAccountID.IsZero() {
			return Payment{}, domain.Invalid("trust_account_id", "an operating payment has no trust account")
		}
	case DestTrust:
		if in.TrustAccountID.IsZero() {
			return Payment{}, domain.Invalid("trust_account_id", "a trust payment names its trust account")
		}
		if !in.InvoiceID.IsZero() {
			return Payment{}, domain.Invalid("invoice_id", "a trust deposit is not applied to an invoice; apply trust from the invoice")
		}
		if err := s.allow(ctx, p, domain.ResTrust, domain.ActionTrustDeposit, in.MatterID); err != nil {
			return Payment{}, err
		}
		if s.d.Trust == nil {
			return Payment{}, ErrTrustUnavailable
		}
	default:
		return Payment{}, domain.Invalid("destination", "must be operating or trust")
	}
	if in.ReceivedOn == "" {
		in.ReceivedOn = s.today(ctx)
	} else if _, err := parseDate("received_on", in.ReceivedOn); err != nil {
		return Payment{}, err
	}
	meta, err := s.meta(p)
	if err != nil {
		return Payment{}, err
	}
	row := Payment{TrustAccountID: in.TrustAccountID, ReceivedOn: in.ReceivedOn}
	row.Payment = domain.Payment{
		Meta: meta, ContactID: in.ContactID, MatterID: in.MatterID, AmountCents: in.AmountCents,
		Destination: in.Destination, Method: in.Method, ReferenceNumber: strings.TrimSpace(in.ReferenceNumber),
		Status: in.Status, InvoiceID: in.InvoiceID, Source: sourceOf(p, in.Source),
	}
	return row, nil
}

// CreatePayment records one payment (an outside payment, a deposit to trust, or a module
// processor payment). A completed operating payment on an invoice reduces its balance; a
// completed trust payment posts one deposit through the trust service.
func (s *Service) CreatePayment(ctx context.Context, p domain.Principal, in PaymentInput) (Payment, error) {
	rows, err := s.BulkPayments(ctx, p, []PaymentInput{in})
	if err != nil {
		return Payment{}, err
	}
	return rows[0], nil
}

// BulkPayments validates every payment, then writes them in one transaction.
func (s *Service) BulkPayments(ctx context.Context, p domain.Principal, items []PaymentInput) ([]Payment, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("items", "at least one item is required")
	}
	if s.overBulkMax(ctx, len(items)) {
		return nil, domain.Invalid("items", "batch is larger than api.bulk_batch_max")
	}
	rows := make([]Payment, 0, len(items))
	for _, in := range items {
		row, err := s.buildPayment(ctx, p, in)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	err := s.write(ctx, p, func(t *txn) error {
		for i := range rows {
			if err := s.insertPayment(t, &rows[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) insertPayment(t *txn, row *Payment) error {
	if !row.InvoiceID.IsZero() {
		inv, err := s.invoice(t.ctx, row.InvoiceID)
		if err != nil || inv.MatterID != row.MatterID {
			return domain.Invalid("invoice_id", "must be an invoice on this matter")
		}
		if !inv.Open() {
			return domain.Conflict("payments apply only to a sent invoice with a balance")
		}
	}
	if row.Status == PayCompleted {
		if err := s.complete(t, row); err != nil {
			return err
		}
	}
	if err := t.put(kindPayment, row.ID, *row); err != nil {
		return err
	}
	if err := t.created("payment", row.ID, *row, ""); err != nil {
		return err
	}
	if err := t.event("payment.created", "payment", row.ID, row.MatterID, "Payment "+money(row.AmountCents)+" recorded", false); err != nil {
		return err
	}
	if row.Status == PayCompleted {
		return t.event("payment.completed", "payment", row.ID, row.MatterID, "Payment "+money(row.AmountCents)+" completed", false)
	}
	return nil
}

// complete applies a payment that just became completed: it reduces the invoice balance or
// posts the trust deposit. An operating payment may not exceed the balance due.
func (s *Service) complete(t *txn, row *Payment) error {
	if row.Destination == DestTrust {
		posted, err := s.d.Trust.Post(t.ctx, t.p, TrustPosting{
			EntryType: "deposit", MatterID: row.MatterID, ClientID: row.ContactID, AccountID: row.TrustAccountID,
			AmountCents: row.AmountCents, Method: row.Method, Description: "Payment " + row.ReferenceNumber, PaymentID: row.ID,
		})
		if err != nil {
			return err
		}
		row.TrustEntryID = t.trusted(posted).EntryID
		return nil
	}
	if row.InvoiceID.IsZero() {
		return nil
	}
	return s.applyToInvoice(t, row.InvoiceID, row.AmountCents)
}

// applyToInvoice adds amount (negative for a reversal) to the invoice's amount paid.
func (s *Service) applyToInvoice(t *txn, id domain.ID, amount domain.Cents) error {
	inv, err := s.invoice(t.ctx, id)
	if err != nil {
		return err
	}
	if positive(int64(amount)) && amount > inv.BalanceDueCents {
		return domain.Invalid("amount_cents", "is more than the balance due "+money(inv.BalanceDueCents))
	}
	before := inv
	if inv.AmountPaidCents, err = sum(inv.AmountPaidCents, amount); err != nil {
		return domain.Invalid("amount_cents", "does not fit")
	}
	if err := recompute(&inv); err != nil {
		return err
	}
	s.settle(&inv)
	if err := t.putInvoice(inv); err != nil {
		return err
	}
	if err := t.diff("invoice", id, before, inv, ""); err != nil {
		return err
	}
	if inv.Status == StatusPaid && before.Status != StatusPaid {
		return t.event("invoice.paid", "invoice", id, inv.MatterID, "Invoice "+inv.Number+" paid", true)
	}
	return nil
}

// UpdatePaymentStatus moves a pending payment to completed or failed. The same status writes
// nothing. Completed, failed and reversed payments do not move here.
func (s *Service) UpdatePaymentStatus(ctx context.Context, p domain.Principal, id domain.ID, status string) (Payment, error) {
	if status != PayPending && status != PayCompleted && status != PayFailed {
		return Payment{}, domain.Invalid("status", "must be pending, completed or failed")
	}
	var out Payment
	err := s.write(ctx, p, func(t *txn) error {
		row, err := s.payment(t.ctx, id)
		if err != nil {
			return err
		}
		if err := s.allow(t.ctx, p, domain.ResPayment, domain.ActionWrite, row.MatterID); err != nil {
			return err
		}
		out = row
		if row.Status == status {
			return nil
		}
		if row.Status != PayPending {
			return domain.Conflict("a " + row.Status + " payment cannot change status; reverse it instead")
		}
		if row.Destination == DestTrust && status == PayCompleted {
			if err := s.allow(t.ctx, p, domain.ResTrust, domain.ActionTrustDeposit, row.MatterID); err != nil {
				return err
			}
			if s.d.Trust == nil {
				return ErrTrustUnavailable
			}
		}
		before := row
		row.Status = status
		row.UpdatedAt = s.now()
		if status == PayCompleted {
			if !row.InvoiceID.IsZero() {
				inv, err := s.invoice(t.ctx, row.InvoiceID)
				if err != nil {
					return err
				}
				if !inv.Open() {
					return domain.Conflict("the invoice no longer has a balance")
				}
			}
			if err := s.complete(t, &row); err != nil {
				return err
			}
		}
		if err := t.put(kindPayment, id, row); err != nil {
			return err
		}
		out = row
		if err := t.diff("payment", id, before, row, ""); err != nil {
			return err
		}
		action := "payment.updated"
		if status == PayCompleted {
			action = "payment.completed"
		}
		return t.event(action, "payment", id, row.MatterID, "Payment "+status, false)
	})
	return out, err
}

// ReversePayment reverses a completed payment. The payment row keeps its amount; its status
// becomes reversed, the invoice balance returns, and a trust-linked payment gets exactly one
// reversal entry from the trust service.
func (s *Service) ReversePayment(ctx context.Context, p domain.Principal, id domain.ID, reason string) (Payment, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Payment{}, domain.Invalid("reason", "is required")
	}
	var out Payment
	err := s.write(ctx, p, func(t *txn) error {
		row, err := s.payment(t.ctx, id)
		if err != nil {
			return err
		}
		if err := s.allow(t.ctx, p, domain.ResPayment, domain.ActionApproveInvoice, row.MatterID); err != nil {
			return err
		}
		if !row.TrustEntryID.IsZero() {
			if err := s.allow(t.ctx, p, domain.ResTrust, domain.ActionTrustWithdraw, row.MatterID); err != nil {
				return err
			}
			if s.d.Trust == nil {
				return ErrTrustUnavailable
			}
		}
		if row.Status != PayCompleted {
			return domain.Conflict("only a completed payment can be reversed")
		}
		before := row
		if !row.TrustEntryID.IsZero() {
			posted, err := s.d.Trust.Reverse(t.ctx, p, row.TrustEntryID, reason)
			if err != nil {
				return err
			}
			row.ReversalEntry = t.trusted(posted).EntryID
		}
		if !row.InvoiceID.IsZero() {
			if err := s.applyToInvoice(t, row.InvoiceID, -row.AmountCents); err != nil {
				return err
			}
		}
		now := s.now()
		row.Status, row.ReversedAt, row.ReverseReason, row.UpdatedAt = PayReversed, &now, reason, now
		if err := t.put(kindPayment, id, row); err != nil {
			return err
		}
		out = row
		if err := t.diff("payment", id, before, row, reason); err != nil {
			return err
		}
		return t.event("payment.reversed", "payment", id, row.MatterID, "Payment "+money(row.AmountCents)+" reversed", false)
	})
	return out, err
}

// ApplyTrust moves an explicit amount from a client's trust funds to operating and applies it
// to an open invoice: one trust transfer entry and one internal-transfer payment, together.
func (s *Service) ApplyTrust(ctx context.Context, p domain.Principal, invoiceID, client, account domain.ID, amount domain.Cents) (Payment, TrustPosted, error) {
	if err := amountField("amount_cents", amount); err != nil {
		return Payment{}, TrustPosted{}, err
	}
	if account.IsZero() {
		return Payment{}, TrustPosted{}, domain.Invalid("trust_account_id", "is required")
	}
	var out Payment
	var posted TrustPosted
	err := s.write(ctx, p, func(t *txn) error {
		inv, err := s.invoice(t.ctx, invoiceID)
		if err != nil {
			return err
		}
		if err := s.allow(t.ctx, p, domain.ResTrust, domain.ActionTrustWithdraw, inv.MatterID); err != nil {
			return err
		}
		if s.d.Trust == nil {
			return ErrTrustUnavailable
		}
		if !inv.Open() {
			return domain.Conflict("trust applies only to a sent invoice with a balance")
		}
		if amount > inv.BalanceDueCents {
			return domain.Invalid("amount_cents", "is more than the balance due "+money(inv.BalanceDueCents))
		}
		if !s.isClient(inv.MatterID, client) {
			return domain.Invalid("client_id", "must be a client on the matter")
		}
		out, posted, err = s.transfer(t, inv, client, account, amount, "Applied to invoice "+inv.Number, false)
		return err
	})
	return out, posted, err
}

// transfer posts the trust transfer to operating and records the matching payment on inv.
// matterFee marks the payment as Apply flat fee's transfer for the matter's own fee.
func (s *Service) transfer(t *txn, inv Invoice, client, account domain.ID, amount domain.Cents, desc string, matterFee bool) (Payment, TrustPosted, error) {
	meta, err := s.meta(t.p)
	if err != nil {
		return Payment{}, TrustPosted{}, err
	}
	posted, err := s.d.Trust.Post(t.ctx, t.p, TrustPosting{
		EntryType: "transfer_to_operating", MatterID: inv.MatterID, ClientID: client, AccountID: account,
		AmountCents: amount, Method: MethodInternal, Description: desc, InvoiceID: inv.ID, PaymentID: meta.ID,
	})
	if err != nil {
		return Payment{}, TrustPosted{}, err
	}
	return s.transferred(t, meta, inv, client, account, amount, desc, matterFee, posted)
}

// transferred records the operating payment on inv for a trust transfer that just posted.
func (s *Service) transferred(t *txn, meta domain.Meta, inv Invoice, client, account domain.ID, amount domain.Cents, desc string, matterFee bool, posted TrustPosted) (Payment, TrustPosted, error) {
	t.trusted(posted)
	row := Payment{TrustAccountID: account, ReceivedOn: s.today(t.ctx), MatterFee: matterFee}
	row.Payment = domain.Payment{
		Meta: meta, ContactID: client, MatterID: inv.MatterID, AmountCents: amount, Destination: DestOperating,
		Method: MethodInternal, ReferenceNumber: "trust", Status: PayCompleted, InvoiceID: inv.ID,
		TrustEntryID: posted.EntryID, Source: sourceOf(t.p, ""),
	}
	if err := s.applyToInvoice(t, inv.ID, amount); err != nil {
		return Payment{}, TrustPosted{}, err
	}
	if err := t.put(kindPayment, row.ID, row); err != nil {
		return Payment{}, TrustPosted{}, err
	}
	if err := t.created("payment", row.ID, row, desc); err != nil {
		return Payment{}, TrustPosted{}, err
	}
	if err := t.event("payment.completed", "payment", row.ID, inv.MatterID, desc, false); err != nil {
		return Payment{}, TrustPosted{}, err
	}
	return row, posted, nil
}

// RequestReplenishment asks the trust service for a replenishment linked to the invoice.
// The amount is explicit; the server does not substitute the balance.
func (s *Service) RequestReplenishment(ctx context.Context, p domain.Principal, invoiceID, client domain.ID, amount domain.Cents, message string) (any, error) {
	if err := amountField("amount_requested_cents", amount); err != nil {
		return nil, err
	}
	var out any
	err := s.write(ctx, p, func(t *txn) error {
		inv, err := s.writable(t, invoiceID, domain.ActionWrite)
		if err != nil {
			return err
		}
		if s.d.Trust == nil {
			return ErrTrustUnavailable
		}
		if inv.Status == StatusVoid || inv.Status == StatusDraft {
			return domain.Conflict("replenishment links only to a sent invoice")
		}
		if !s.isClient(inv.MatterID, client) {
			return domain.Invalid("client_id", "must be a client on the matter")
		}
		posted, err := s.d.Trust.RequestReplenishment(t.ctx, p, ReplenishmentInput{
			MatterID: inv.MatterID, ClientID: client, AmountCents: amount, InvoiceID: inv.ID, Message: message,
		})
		if err != nil {
			return err
		}
		out = t.trusted(posted).Result
		t.audit("invoice", inv.ID, "replenishment_requested", "", money(amount), message)
		return nil
	})
	return out, err
}
