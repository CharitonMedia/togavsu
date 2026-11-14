package billing

import (
	"context"
	"fmt"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// InvoiceImport is one invoice an importer writes through bulkInvoice. Every amount is
// integer cents and must reconcile: subtotal is the sum of the lines, total is subtotal
// plus adjustment and interest, and balance due is total less amount paid and write-off.
type InvoiceImport struct {
	MatterID         domain.ID
	Number           string
	IssuedOn         string
	Status           string
	Lines            []LineInput
	SubtotalCents    domain.Cents
	AdjustmentCents  domain.Cents
	AdjustmentReason string
	InterestCents    domain.Cents
	TotalCents       domain.Cents
	AmountPaidCents  domain.Cents
	BalanceDueCents  domain.Cents
	WriteOffCents    domain.Cents
	WriteOffReason   string
	VoidReason       string
	DisputeStatus    string
	DisputeNotes     string
	ClientMessage    string
	InternalNotes    string
}

// ImportScopes are the token scopes item needs beyond the route's invoices:write: a sent,
// viewed or paid invoice is sendInvoice's invoices:send, and an amount paid is createPayment's
// payments:write. The handler checks them on a bearer token before the batch runs.
func ImportScopes(item InvoiceImport) []string {
	var need []string
	switch item.Status {
	case StatusSent, StatusViewed, StatusPaid:
		need = append(need, "invoices:send")
	}
	if positive(int64(item.AmountPaidCents)) {
		need = append(need, "payments:write")
	}
	return need
}

// authorizeImport checks the item privileges bulkInvoice names, each on the item's matter: a
// draft is createInvoiceDrafts, sent, viewed and paid are sendInvoice, void is voidInvoice, a
// write-off is writeOffInvoice, and an amount paid is createPayment. A wall is not_found.
func (s *Service) authorizeImport(ctx context.Context, p domain.Principal, in InvoiceImport) error {
	if err := s.allow(ctx, p, domain.ResInvoice, domain.ActionWrite, in.MatterID); err != nil {
		return err
	}
	if in.Status != StatusDraft || positive(int64(in.WriteOffCents)) {
		if err := s.allow(ctx, p, domain.ResInvoice, domain.ActionApproveInvoice, in.MatterID); err != nil {
			return err
		}
	}
	if positive(int64(in.AmountPaidCents)) {
		return s.allow(ctx, p, domain.ResPayment, domain.ActionWrite, in.MatterID)
	}
	return nil
}

// BulkInvoices writes imported invoices in one transaction. Every item is authorized first,
// then validated and reconciled; one failure writes nothing. A committed batch emits
// invoice.created once per item. Time and expense lines that name their source bill that row
// (a void invoice leaves it unbilled).
func (s *Service) BulkInvoices(ctx context.Context, p domain.Principal, items []InvoiceImport) ([]Invoice, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("items", "at least one item is required")
	}
	if s.overBulkMax(ctx, len(items)) {
		return nil, domain.Invalid("items", "batch is larger than api.bulk_batch_max")
	}
	for _, in := range items {
		if in.MatterID.IsZero() {
			return nil, domain.Invalid("items/matter_id", "is required")
		}
		if err := s.authorizeImport(ctx, p, in); err != nil {
			return nil, err
		}
		if _, err := s.matter(ctx, in.MatterID); err != nil {
			return nil, err
		}
	}
	var out []Invoice
	err := s.write(ctx, p, func(t *txn) error {
		heads, err := invoiceHeads(t.ctx, s.d.Store, Where{})
		if err != nil {
			return err
		}
		numbers := map[string]bool{}
		for _, h := range heads {
			numbers[h.Number] = true
		}
		sources := map[domain.ID]bool{}
		for i, in := range items {
			inv, err := s.importOne(t, in, i, numbers, sources)
			if err != nil {
				return err
			}
			out = append(out, inv)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) importOne(t *txn, in InvoiceImport, i int, numbers map[string]bool, sources map[domain.ID]bool) (Invoice, error) {
	field := func(name string) string { return fmt.Sprintf("items/%d/%s", i, name) }
	m, err := s.matter(t.ctx, in.MatterID)
	if err != nil {
		return Invoice{}, err
	}
	number := strings.TrimSpace(in.Number)
	if number == "" {
		return Invoice{}, domain.Invalid(field("number"), "is required")
	}
	if numbers[number] {
		return Invoice{}, domain.Invalid(field("number"), "invoice number "+number+" is already used")
	}
	numbers[number] = true
	if _, err := parseDate(field("issued_on"), in.IssuedOn); err != nil {
		return Invoice{}, err
	}
	switch in.Status {
	case StatusDraft, StatusSent, StatusViewed, StatusPaid, StatusVoid:
	default:
		return Invoice{}, domain.Invalid(field("status"), "must be draft, sent, viewed, paid or void")
	}
	for name, v := range map[string]domain.Cents{"interest_cents": in.InterestCents, "amount_paid_cents": in.AmountPaidCents, "write_off_cents": in.WriteOffCents} {
		if negative(int64(v)) {
			return Invoice{}, domain.Invalid(field(name), "cannot be negative")
		}
	}
	if len(in.Lines) == 0 {
		return Invoice{}, domain.Invalid(field("lines"), "an invoice needs at least one line")
	}
	inv := Invoice{MatterName: m.Name, MatterNum: m.Number}
	for j, li := range in.Lines {
		line, err := s.importLine(t, m.ID, li, fmt.Sprintf("items/%d/lines/%d", i, j), sources)
		if err != nil {
			return Invoice{}, err
		}
		inv.Lines = append(inv.Lines, line)
	}
	inv.AdjustmentCents, inv.InterestCents = in.AdjustmentCents, in.InterestCents
	inv.AmountPaidCents, inv.WriteOffCents = in.AmountPaidCents, in.WriteOffCents
	if err := recompute(&inv); err != nil {
		return Invoice{}, err
	}
	if in.Status == StatusVoid {
		// Voiding releases the balance, as voidInvoice does; a void invoice carries no payment.
		if positive(int64(in.AmountPaidCents)) {
			return Invoice{}, domain.Invalid(field("amount_paid_cents"), "a void invoice carries no payment")
		}
		inv.BalanceDueCents = noCents
		if strings.TrimSpace(in.VoidReason) == "" {
			return Invoice{}, domain.Invalid(field("void_reason"), "is required on a void invoice")
		}
	}
	for _, c := range []struct {
		name        string
		given, want domain.Cents
	}{
		{"subtotal_cents", in.SubtotalCents, inv.SubtotalCents}, {"total_cents", in.TotalCents, inv.TotalCents},
		{"balance_due_cents", in.BalanceDueCents, inv.BalanceDueCents},
	} {
		if c.given != c.want {
			return Invoice{}, domain.Invalid(field(c.name), "does not reconcile: the lines and amounts give "+money(c.want))
		}
	}
	balance := int64(inv.BalanceDueCents)
	switch in.Status {
	case StatusDraft:
		if positive(int64(in.AmountPaidCents)) || positive(int64(in.WriteOffCents)) {
			return Invoice{}, domain.Invalid(field("status"), "a draft has no payment or write-off")
		}
	case StatusSent, StatusViewed:
		if !positive(balance) {
			return Invoice{}, domain.Invalid(field("status"), "a sent or viewed invoice has a balance due; with none it is paid")
		}
	case StatusPaid:
		if !isZero(balance) {
			return Invoice{}, domain.Invalid(field("status"), "a paid invoice has no balance due")
		}
	}
	if in.Status != StatusDraft && negative(int64(inv.TotalCents)) {
		return Invoice{}, domain.Invalid(field("total_cents"), "an invoice total cannot be negative")
	}
	if positive(int64(in.WriteOffCents)) && strings.TrimSpace(in.WriteOffReason) == "" {
		return Invoice{}, domain.Invalid(field("write_off_reason"), "is required with a write-off")
	}
	meta, err := s.meta(t.p)
	if err != nil {
		return Invoice{}, err
	}
	inv.Invoice.Meta = meta
	inv.MatterID, inv.Number, inv.IssuedOn, inv.Status = m.ID, number, in.IssuedOn, in.Status
	inv.AdjustmentReason, inv.WriteOffReason, inv.VoidReason = strings.TrimSpace(in.AdjustmentReason), strings.TrimSpace(in.WriteOffReason), strings.TrimSpace(in.VoidReason)
	inv.DisputeStatus, inv.DisputeNotes = in.DisputeStatus, in.DisputeNotes
	inv.ClientMessage, inv.InternalNotes = in.ClientMessage, in.InternalNotes
	inv.DueOn = addDays(in.IssuedOn, s.settingInt(t.ctx, "billing.payment_terms_days"))
	now := s.now()
	if in.Status != StatusDraft {
		inv.ApprovedAt, inv.ApprovedBy = &now, actor(t.p)
	}
	switch in.Status {
	case StatusViewed:
		inv.ViewedAt = &now
		inv.SentAt = now.UnixMicro()
	case StatusSent:
		inv.SentAt = now.UnixMicro()
	case StatusPaid:
		inv.SentAt, inv.PaidAt = now.UnixMicro(), now.UnixMicro()
	case StatusVoid:
		inv.VoidedAt = &now
	}
	if positive(int64(in.WriteOffCents)) {
		inv.WriteOffs = []WriteOff{{On: in.IssuedOn, AmountCents: in.WriteOffCents, Reason: inv.WriteOffReason, By: actor(t.p)}}
	}
	if in.Status != StatusVoid {
		for _, l := range inv.Lines {
			if !l.SourceID.IsZero() {
				if err := s.setBilled(t, l.Kind, l.SourceID, inv.ID, number); err != nil {
					return Invoice{}, err
				}
			}
		}
	}
	if err := t.putInvoice(inv); err != nil {
		return Invoice{}, err
	}
	if err := t.created("invoice", inv.ID, inv, "imported"); err != nil {
		return Invoice{}, err
	}
	return inv, t.event("invoice.created", "invoice", inv.ID, m.ID, "Invoice "+number+" imported", false)
}

// importLine checks one imported line. Its amount is the importer's, in exact cents. A time
// or expense line may name its source row, which must be an unbilled row on the matter and
// may appear once in the batch; a flat-fee line may name a catalog fee item.
func (s *Service) importLine(t *txn, matter domain.ID, li LineInput, field string, sources map[domain.ID]bool) (Line, error) {
	line := Line{Kind: li.Kind, SourceID: li.SourceID, Description: strings.TrimSpace(li.Description), FeeItemID: li.FeeItemID}
	if li.AmountCents == nil {
		return Line{}, domain.Invalid(field+"/amount_cents", "is required")
	}
	line.AmountCents = *li.AmountCents
	switch li.Kind {
	case LineTime, LineExpense:
		if !li.SourceID.IsZero() {
			var on, billed domain.ID
			if li.Kind == LineTime {
				e, err := s.timeEntry(t.ctx, li.SourceID)
				if err != nil {
					return Line{}, domain.Invalid(field+"/source_id", "unknown time entry")
				}
				on, billed = e.MatterID, e.InvoiceID
			} else {
				e, err := s.expense(t.ctx, li.SourceID)
				if err != nil {
					return Line{}, domain.Invalid(field+"/source_id", "unknown expense")
				}
				on, billed = e.MatterID, e.InvoiceID
			}
			if on != matter {
				return Line{}, domain.Invalid(field+"/source_id", "source is on another matter")
			}
			if !billed.IsZero() || sources[li.SourceID] {
				return Line{}, domain.Invalid(field+"/source_id", "source is already billed")
			}
			sources[li.SourceID] = true
		}
	case LineFlatFee:
		if !li.FeeItemID.IsZero() {
			if _, err := s.feeItem(t.ctx, li.FeeItemID); err != nil {
				return Line{}, domain.Invalid(field+"/fee_item_id", "unknown fee item")
			}
		}
	case LineAdjustment:
	default:
		return Line{}, domain.Invalid(field+"/kind", "must be time, expense, flat_fee or adjustment")
	}
	if li.Kind != LineTime && li.Kind != LineExpense && !li.SourceID.IsZero() {
		return Line{}, domain.Invalid(field+"/source_id", "only a time or expense line names a source")
	}
	if line.Description == "" {
		return Line{}, domain.Invalid(field+"/description", "is required")
	}
	if line.Kind != LineAdjustment && negative(int64(line.AmountCents)) {
		return Line{}, domain.Invalid(field+"/amount_cents", "only an adjustment line may be negative")
	}
	return line, nil
}
