package billing

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/audit"

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
	rows, _, err := s.BulkInvoicesDetailed(ctx, p, items)
	return rows, err
}

// BulkInvoicesDetailed returns every row's outcome, including errors in a rejected batch.
func (s *Service) BulkInvoicesDetailed(ctx context.Context, p domain.Principal, items []InvoiceImport) ([]Invoice, []BulkInvoiceResult, error) {
	return s.bulkInvoices(ctx, p, items, false)
}

var errImportPreview = errors.New("invoice import preview rollback")

// bulkInvoices is the sole preview and commit path. Preview executes the actual writes
// inside the transaction, then rolls back before audit publication and invoice mirroring.
func (s *Service) bulkInvoices(ctx context.Context, p domain.Principal, items []InvoiceImport, preview bool) ([]Invoice, []BulkInvoiceResult, error) {
	if len(items) == 0 {
		return nil, nil, domain.Invalid("items", "at least one item is required")
	}
	if s.overBulkMax(ctx, len(items)) {
		return nil, nil, domain.Invalid("items", "batch is larger than api.bulk_batch_max")
	}
	var rejected error
	var invoices []Invoice
	results := make([]BulkInvoiceResult, 0, len(items))
	err := s.write(ctx, p, func(t *txn) error {
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
		var firstErr error
		for i, in := range items {
			r := BulkInvoiceResult{Index: i, Row: i + 1, PreviewOK: true}
			rowNumbers, rowSources := maps.Clone(numbers), maps.Clone(sources)
			rowTxn := &txn{s: s, p: p, mirror: map[domain.ID]Invoice{}}
			var inv Invoice
			rowErr := s.importRowAtomic(t.ctx, func(ctx context.Context) error {
				rowTxn.ctx = ctx
				var err error
				inv, r.Skipped, err = s.importRow(rowTxn, in, i, rowNumbers, rowSources, existingRefs, seenRefs)
				return err
			})
			if rowErr != nil {
				var api *domain.APIError
				if !errors.As(rowErr, &api) {
					return rowErr
				}
				if firstErr == nil {
					firstErr = batchRejected(rowErr)
				}
				r.PreviewOK = false
				r.PreviewError = fmt.Sprintf("row %d: %s", i+1, previewErr(rowErr))
			} else if r.Skipped {
				r.ExternalRef = strings.TrimSpace(in.ExternalRef)
				r.SkipReason = "already imported by external_ref"
			} else {
				numbers, sources = rowNumbers, rowSources
				invoices = append(invoices, inv)
				t.events = append(t.events, rowTxn.events...)
				t.changes = append(t.changes, rowTxn.changes...)
				maps.Copy(t.mirror, rowTxn.mirror)
			}
			results = append(results, r)
		}
		if firstErr != nil {
			rejected = firstErr
			return firstErr
		}
		if preview {
			return errImportPreview
		}
		return nil
	})
	if errors.Is(err, errImportPreview) {
		return nil, results, nil
	}
	// A preview describes row refusals in results; a commit also rejects the batch.
	if preview && rejected != nil && err == rejected {
		return nil, results, nil
	}
	if err != nil {
		return nil, results, err
	}
	return invoices, results, nil
}

// importRow applies authorization before external-ref handling, so skips cannot leak
// a walled or forbidden invoice. Only refs present before this run may be skipped.
func (s *Service) importRow(t *txn, in InvoiceImport, i int, numbers map[string]bool, sources map[domain.ID]bool, existingRefs, seenRefs map[string]bool) (Invoice, bool, error) {
	if in.MatterID.IsZero() {
		return Invoice{}, false, domain.Invalid(fmt.Sprintf("items/%d/matter_id", i), "is required")
	}
	if err := s.authorizeImport(t.ctx, t.p, in); err != nil {
		return Invoice{}, false, err
	}
	if _, err := s.matter(t.ctx, in.MatterID); err != nil {
		return Invoice{}, false, err
	}
	if in.OpeningImport() {
		ref := strings.TrimSpace(in.ExternalRef)
		if ref != "" {
			if seenRefs[ref] {
				return Invoice{}, false, domain.Invalid(fmt.Sprintf("items/%d/external_ref", i), "duplicate external_ref inside this file")
			}
			seenRefs[ref] = true
			if existingRefs[ref] {
				return Invoice{}, true, nil
			}
		}
		var err error
		in, err = s.resolveOpeningFiles(t.ctx, t.p, in, i)
		if err != nil {
			return Invoice{}, false, err
		}
		if err := s.checkOpeningImport(t.ctx, in, i, numbers); err != nil {
			return Invoice{}, false, err
		}
	}
	inv, err := s.importOne(t, in, i, numbers, sources)
	if isExternalRefUnique(err) {
		err = domain.Invalid(fmt.Sprintf("items/%d/external_ref", i), "already imported by external_ref")
	}
	return inv, false, err
}

// importRowAtomic isolates a refused row so later rows still get their real result,
// including after a database constraint error. The enclosing transaction remains the
// only commit boundary.
func (s *Service) importRowAtomic(ctx context.Context, fn func(context.Context) error) error {
	switch st := s.d.Store.(type) {
	case *Postgres:
		conn := audit.Conn(ctx, st.pool)
		if _, err := conn.ExecContext(ctx, "SAVEPOINT invoice_import_row"); err != nil {
			return err
		}
		rowErr := fn(ctx)
		if rowErr != nil {
			if _, err := conn.ExecContext(ctx, "ROLLBACK TO SAVEPOINT invoice_import_row"); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, "RELEASE SAVEPOINT invoice_import_row"); err != nil {
			return err
		}
		return rowErr
	case *Memory:
		return st.Atomic(context.WithValue(ctx, memTx{}, (*Memory)(nil)), fn)
	default:
		return errors.New("invoice import requires a transactional store")
	}
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
	// Validate dates and reconcile before writing any invoice rows.
	if err := s.validateImportCommitSide(t.ctx, in, i); err != nil {
		return Invoice{}, err
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
		// Voiding releases the balance, as voidInvoice does; validated above.
		inv.BalanceDueCents = noCents
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
			version, err := s.attachDocumentOfRecord(t.ctx, t.p, inv.MatterID, inv.DocumentOfRecordFileID, field("document_of_record_file_id"), true)
			if err != nil {
				return Invoice{}, err
			}
			inv.DocumentOfRecordVersionID = version
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
