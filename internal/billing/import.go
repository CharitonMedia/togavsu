package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// InvoiceImport is one invoice an importer writes through bulkInvoice. Every amount is
// integer cents and must reconcile: subtotal is the sum of the lines, total is subtotal
// plus adjustment and interest, and balance due is total less amount paid and write-off.
// Source, ExternalRef and DocumentOfRecordFileID are CORE-37 opening-import fields.
type InvoiceImport struct {
	MatterID         domain.ID
	Number           string
	IssuedOn         string
	DueOn            string
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
	// Source is form, csv or api for opening invoice import (CORE-37). Empty keeps the
	// legacy bulk importer behaviour.
	Source string
	// ExternalRef is unique per firm; a second run skips the row as already imported.
	ExternalRef string
	// DocumentOfRecordFileID is the original invoice PDF shown instead of a render.
	DocumentOfRecordFileID domain.ID
	// ContactEmail is the CSV contact email; when set it must match a client on the matter.
	ContactEmail string
	// DocumentFileName is the CSV original PDF name; resolved to DocumentOfRecordFileID.
	DocumentFileName string
}

// ImportSourceForm, ImportSourceCSV and ImportSourceAPI are InvoiceImport.Source values.
const (
	ImportSourceForm = "form"
	ImportSourceCSV  = "csv"
	ImportSourceAPI  = "api"
)

// OpeningImport reports whether this item uses CORE-37 opening-invoice semantics.
func (in InvoiceImport) OpeningImport() bool {
	switch in.Source {
	case ImportSourceForm, ImportSourceCSV, ImportSourceAPI:
		return true
	}
	return false
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
// invoice.created once per item (and invoice.imported for CORE-37 opening imports). Time
// and expense lines that name their source bill that row (a void invoice leaves it unbilled).
// Already-imported external_ref rows are skipped and reported; they do not fail the batch.
func (s *Service) BulkInvoices(ctx context.Context, p domain.Principal, items []InvoiceImport) ([]Invoice, error) {
	rows, skipped, err := s.BulkInvoicesDetailed(ctx, p, items)
	_ = skipped
	return rows, err
}

// BulkInvoicesDetailed is BulkInvoices plus the skipped external_ref rows (CORE-37).
func (s *Service) BulkInvoicesDetailed(ctx context.Context, p domain.Principal, items []InvoiceImport) ([]Invoice, []BulkInvoiceResult, error) {
	if len(items) == 0 {
		return nil, nil, domain.Invalid("items", "at least one item is required")
	}
	if s.overBulkMax(ctx, len(items)) {
		return nil, nil, domain.Invalid("items", "batch is larger than api.bulk_batch_max")
	}
	resolved := make([]InvoiceImport, len(items))
	for i, in := range items {
		if in.MatterID.IsZero() {
			return nil, nil, domain.Invalid("items/matter_id", "is required")
		}
		if err := s.authorizeImport(ctx, p, in); err != nil {
			return nil, nil, err
		}
		if _, err := s.matter(ctx, in.MatterID); err != nil {
			return nil, nil, err
		}
		if in.OpeningImport() {
			got, err := s.resolveOpeningFiles(ctx, p, in, i)
			if err != nil {
				return nil, nil, err
			}
			in = got
		}
		resolved[i] = in
	}
	var out []Invoice
	var skipped []BulkInvoiceResult
	err := s.write(ctx, p, func(t *txn) error {
		// Read refs inside the write transaction so a concurrent commit is visible under
		// stronger isolation; unique-violation on dual-write still maps to a named refusal.
		existingRefs, err := s.existingExternalRefs(t.ctx)
		if err != nil {
			return err
		}
		heads, err := invoiceHeads(t.ctx, s.d.Store, Where{})
		if err != nil {
			return err
		}
		numbers := map[string]bool{}
		for _, h := range heads {
			numbers[h.Number] = true
		}
		sources := map[domain.ID]bool{}
		seenRefs := map[string]bool{}
		for i, in := range resolved {
			if in.OpeningImport() {
				ref := strings.TrimSpace(in.ExternalRef)
				if ref != "" && (existingRefs[ref] || seenRefs[ref]) {
					skipped = append(skipped, BulkInvoiceResult{Index: i, Skipped: true, ExternalRef: ref, SkipReason: "already imported by external_ref"})
					continue
				}
				if err := s.checkOpeningImport(t.ctx, in, i, numbers, existingRefs, seenRefs, false); err != nil {
					return batchRejected(err)
				}
			}
			inv, err := s.importOne(t, in, i, numbers, sources)
			if err != nil {
				if ref := strings.TrimSpace(in.ExternalRef); ref != "" && isExternalRefUnique(err) {
					return batchRejected(domain.Invalid(fmt.Sprintf("items/%d/external_ref", i), "already imported by external_ref"))
				}
				return batchRejected(err)
			}
			if ref := strings.TrimSpace(in.ExternalRef); ref != "" {
				seenRefs[ref] = true
				existingRefs[ref] = true
			}
			out = append(out, inv)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return out, skipped, nil
}

func isExternalRefUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && strings.Contains(pg.ConstraintName, "external_ref")
}

// batchRejected is how one item's validation or reconciliation failure reaches the
// caller: batch_rejected (422), the contract's code for an item that rejected the
// all-or-nothing batch. Not found, forbidden and unauthenticated keep their codes, so a
// wall still answers not_found; request-shape errors are checked before the batch and
// stay validation.
func batchRejected(err error) error { return domain.BatchRejected(err) }

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
	if due := strings.TrimSpace(in.DueOn); due != "" {
		if _, err := parseDate(field("due_on"), due); err != nil {
			return Invoice{}, err
		}
		inv.DueOn = due
	} else {
		inv.DueOn = addDays(in.IssuedOn, s.settingInt(t.ctx, "billing.payment_terms_days"))
	}
	if in.OpeningImport() {
		inv.Imported = true
		inv.ImportSource = in.Source
		inv.ExternalRef = strings.TrimSpace(in.ExternalRef)
		inv.DocumentOfRecordFileID = in.DocumentOfRecordFileID
		if !inv.DocumentOfRecordFileID.IsZero() {
			if err := s.attachDocumentOfRecord(t.ctx, t.p, inv.MatterID, inv.DocumentOfRecordFileID, field("document_of_record_file_id"), true); err != nil {
				return Invoice{}, err
			}
		}
	}
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
	paid := inv.AmountPaidCents
	if in.OpeningImport() && positive(int64(paid)) {
		// Amounts stay on the invoice; the imported payment records them without re-applying.
		inv.AmountPaidCents = paid
	}
	if err := t.putInvoice(inv); err != nil {
		return Invoice{}, err
	}
	if in.OpeningImport() && positive(int64(paid)) {
		if err := s.postImportedPayment(t, inv, paid); err != nil {
			return Invoice{}, err
		}
	}
	if err := t.created("invoice", inv.ID, inv, "imported"); err != nil {
		return Invoice{}, err
	}
	if err := t.event("invoice.created", "invoice", inv.ID, m.ID, "Invoice "+number+" imported", false); err != nil {
		return Invoice{}, err
	}
	if in.OpeningImport() {
		return inv, t.event("invoice.imported", "invoice", inv.ID, m.ID, "Opening invoice "+number+" imported", true)
	}
	return inv, nil
}

// postImportedPayment records the amount-paid-to-date as one method=other payment marked
// imported, without moving money or re-applying cents already on the invoice (CORE-37).
func (s *Service) postImportedPayment(t *txn, inv Invoice, amount domain.Cents) error {
	clients := s.clientParties(inv.MatterID)
	if len(clients) == 0 {
		return domain.Invalid("matter_id", "matter has no client party for the imported payment")
	}
	meta, err := s.meta(t.p)
	if err != nil {
		return err
	}
	row := Payment{ReceivedOn: inv.IssuedOn, Imported: true}
	row.Payment = domain.Payment{
		Meta: meta, ContactID: clients[0], MatterID: inv.MatterID, AmountCents: amount,
		Destination: DestOperating, Method: "other", ReferenceNumber: "imported:" + inv.Number,
		Status: PayCompleted, InvoiceID: inv.ID, Source: "import",
	}
	if err := t.put(kindPayment, row.ID, row); err != nil {
		return err
	}
	if err := t.created("payment", row.ID, row, "imported"); err != nil {
		return err
	}
	return t.event("payment.created", "payment", row.ID, inv.MatterID, "Imported payment "+money(amount)+" on "+inv.Number, false)
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
