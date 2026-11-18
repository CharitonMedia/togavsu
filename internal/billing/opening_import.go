package billing

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// BulkInvoiceResult is one bulkInvoice outcome: a written invoice, or a skipped
// already-imported external_ref (CORE-37).
type BulkInvoiceResult struct {
	Invoice      *Invoice
	Skipped      bool
	ExternalRef  string
	SkipReason   string
	Index        int
	PreviewOK    bool
	PreviewError string
}

// BulkInvoicesPreview validates opening and legacy import items without writing.
func (s *Service) BulkInvoicesPreview(ctx context.Context, p domain.Principal, items []InvoiceImport) ([]BulkInvoiceResult, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("items", "at least one item is required")
	}
	if s.overBulkMax(ctx, len(items)) {
		return nil, domain.Invalid("items", "batch is larger than api.bulk_batch_max")
	}
	out := make([]BulkInvoiceResult, 0, len(items))
	seenRefs := map[string]bool{}
	numbers := map[string]bool{}
	heads, err := invoiceHeads(ctx, s.d.Store, Where{})
	if err != nil {
		return nil, err
	}
	for _, h := range heads {
		numbers[h.Number] = true
	}
	existingRefs, err := s.existingExternalRefs(ctx)
	if err != nil {
		return nil, err
	}
	for i, in := range items {
		r := BulkInvoiceResult{Index: i, PreviewOK: true}
		if err := s.previewOne(ctx, p, in, i, numbers, seenRefs, existingRefs); err != nil {
			r.PreviewOK = false
			r.PreviewError = err.Error()
			var api *domain.APIError
			if asOpeningAPI(err, &api) && api.Message != "" {
				r.PreviewError = api.Message
				if api.Field != "" {
					r.PreviewError = api.Field + ": " + api.Message
				}
			}
		} else if in.OpeningImport() && strings.TrimSpace(in.ExternalRef) != "" {
			ref := strings.TrimSpace(in.ExternalRef)
			if existingRefs[ref] || seenRefs[ref] {
				r.Skipped = true
				r.ExternalRef = ref
				r.SkipReason = "already imported by external_ref"
			} else {
				seenRefs[ref] = true
			}
			numbers[strings.TrimSpace(in.Number)] = true
		} else {
			numbers[strings.TrimSpace(in.Number)] = true
		}
		out = append(out, r)
	}
	return out, nil
}

func asOpeningAPI(err error, dest **domain.APIError) bool {
	if err == nil {
		return false
	}
	e, ok := err.(*domain.APIError)
	if !ok {
		return false
	}
	*dest = e
	return true
}

func (s *Service) previewOne(ctx context.Context, p domain.Principal, in InvoiceImport, i int, numbers, seenRefs, existingRefs map[string]bool) error {
	if in.MatterID.IsZero() {
		return domain.Invalid(fmt.Sprintf("items/%d/matter_id", i), "is required")
	}
	if err := s.authorizeImport(ctx, p, in); err != nil {
		return err
	}
	if _, err := s.matter(ctx, in.MatterID); err != nil {
		return err
	}
	if in.OpeningImport() {
		if err := s.checkOpeningImport(ctx, in, i, numbers, existingRefs, seenRefs, true); err != nil {
			return err
		}
	}
	number := strings.TrimSpace(in.Number)
	if number == "" {
		return domain.Invalid(fmt.Sprintf("items/%d/number", i), "is required")
	}
	if numbers[number] && !(in.OpeningImport() && existingRefs[strings.TrimSpace(in.ExternalRef)]) {
		return domain.Invalid(fmt.Sprintf("items/%d/number", i), "invoice number "+number+" is already used")
	}
	return nil
}

func (s *Service) existingExternalRefs(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	rows, _, err := findOf[Invoice](ctx, s.d.Store, kindInvoice, Where{})
	if err != nil {
		return nil, err
	}
	for _, inv := range rows {
		if ref := strings.TrimSpace(inv.ExternalRef); ref != "" {
			out[ref] = true
		}
	}
	return out, nil
}

// checkOpeningImport applies CORE-37 refusals for a sourced import item.
func (s *Service) checkOpeningImport(ctx context.Context, in InvoiceImport, i int, numbers, existingRefs, seenRefs map[string]bool, preview bool) error {
	field := func(name string) string { return fmt.Sprintf("items/%d/%s", i, name) }
	if in.MatterID.IsZero() {
		return domain.Invalid(field("matter_id"), "a matter is required")
	}
	if _, err := s.matter(ctx, in.MatterID); err != nil {
		return domain.Invalid(field("matter_id"), "matter is missing or unknown")
	}
	number := strings.TrimSpace(in.Number)
	if number == "" {
		return domain.Invalid(field("number"), "is required")
	}
	ref := strings.TrimSpace(in.ExternalRef)
	if ref != "" && (existingRefs[ref] || seenRefs[ref]) {
		return nil // skip path; caller marks skipped
	}
	if numbers[number] {
		return domain.Invalid(field("number"), "invoice number "+number+" is already used")
	}
	if start := s.settingInt(ctx, "billing.invoice_number_start"); positive(int64(start)) {
		if n, ok := parseImportNumber(number); ok && n >= start {
			return domain.Invalid(field("number"), fmt.Sprintf("invoice number %s is at or above invoice_number_start (%d)", number, start))
		}
	}
	if !positive(int64(in.TotalCents)) || !positive(int64(in.SubtotalCents)) {
		return domain.Invalid(field("total_cents"), "amount due must be greater than zero")
	}
	if in.Status != StatusSent && in.Status != StatusPaid && in.Status != StatusViewed {
		return domain.Invalid(field("status"), "an opening import is sent (or paid/viewed)")
	}
	if !onlyOne(len(in.Lines)) || in.Lines[0].Kind != LineAdjustment {
		return domain.Invalid(field("lines"), `an opening import needs one "Balance forward as of" adjustment line`)
	}
	desc := strings.TrimSpace(in.Lines[0].Description)
	if !strings.HasPrefix(desc, "Balance forward as of") {
		return domain.Invalid(field("lines/0/description"), `must be "Balance forward as of [date]"`)
	}
	_ = preview
	return nil
}

// parseImportNumber extracts an integer from a historical invoice number: the whole
// string when it is digits, otherwise the last run of digits.
func parseImportNumber(number string) (int, bool) {
	number = strings.TrimSpace(number)
	if number == "" {
		return int(0), false
	}
	if n, err := strconv.Atoi(number); err == nil {
		return n, true
	}
	end := -1
	start := -1
	for i := len(number) - 1; i >= int(0); i-- {
		r := number[i]
		if r < '0' || r > '9' {
			if end >= int(0) {
				break
			}
			continue
		}
		if end < int(0) {
			end = i
		}
		start = i
	}
	if start < int(0) {
		return int(0), false
	}
	n, err := strconv.Atoi(number[start : end+1])
	return n, err == nil
}

// OpeningInvoiceItem builds a reconciled InvoiceImport for the admin form / CSV.
func OpeningInvoiceItem(matter domain.ID, number, issuedOn, dueOn string, amountDue, amountPaid domain.Cents, message, source, externalRef string, dor domain.ID) InvoiceImport {
	balance := amountDue - amountPaid
	status := StatusSent
	if !positive(int64(balance)) && positive(int64(amountPaid)) {
		status = StatusPaid
		balance = noCents
	}
	desc := "Balance forward as of " + issuedOn
	in := InvoiceImport{
		MatterID: matter, Number: number, IssuedOn: issuedOn, DueOn: dueOn, Status: status,
		Lines:         []LineInput{{Kind: LineAdjustment, Description: desc, AmountCents: &amountDue}},
		SubtotalCents: amountDue, AdjustmentCents: noCents, InterestCents: noCents, TotalCents: amountDue,
		AmountPaidCents: amountPaid, BalanceDueCents: balance, WriteOffCents: noCents,
		ClientMessage: message, Source: source, ExternalRef: externalRef, DocumentOfRecordFileID: dor,
	}
	return in
}
