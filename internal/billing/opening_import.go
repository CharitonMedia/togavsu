package billing

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/audit"
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
		if in.OpeningImport() {
			ref := strings.TrimSpace(in.ExternalRef)
			// Match commit: skip already-seen external_ref before other checks so preview
			// and commit agree on within-file duplicates.
			if ref != "" && (existingRefs[ref] || seenRefs[ref]) {
				r.Skipped = true
				r.ExternalRef = ref
				r.SkipReason = "already imported by external_ref"
				out = append(out, r)
				continue
			}
			got, err := s.resolveOpeningFiles(ctx, p, in, i)
			if err != nil {
				r.PreviewOK = false
				r.PreviewError = previewErr(err)
				out = append(out, r)
				continue
			}
			in = got
		}
		if err := s.previewOne(ctx, p, in, i, numbers, seenRefs, existingRefs); err != nil {
			r.PreviewOK = false
			r.PreviewError = previewErr(err)
		} else if in.OpeningImport() && strings.TrimSpace(in.ExternalRef) != "" {
			ref := strings.TrimSpace(in.ExternalRef)
			seenRefs[ref] = true
			numbers[strings.TrimSpace(in.Number)] = true
		} else {
			numbers[strings.TrimSpace(in.Number)] = true
		}
		out = append(out, r)
	}
	return out, nil
}

func previewErr(err error) string {
	var api *domain.APIError
	if asOpeningAPI(err, &api) && api.Message != "" {
		if api.Field != "" {
			return api.Field + ": " + api.Message
		}
		return api.Message
	}
	return err.Error()
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
	} else if err := s.validateImportCommitSide(ctx, in, i); err != nil {
		// Non-opening bulk rows still share commit-side date/recompute checks in preview.
		return err
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

// validateImportCommitSide runs every check importOne and postImportedPayment apply
// before write that BulkInvoicesPreview must also apply, so a preview that says OK never
// fails at commit (IMP-03 / CORE-CHANGES-2 §6.3): parseDate on issued_on and due_on, the
// recompute and reconciliation of subtotal/total/balance, status/balance rules, and the
// "matter has no client party" refusal when amount_paid is above zero.
func (s *Service) validateImportCommitSide(ctx context.Context, in InvoiceImport, i int) error {
	field := func(name string) string { return fmt.Sprintf("items/%d/%s", i, name) }
	if _, err := parseDate(field("issued_on"), in.IssuedOn); err != nil {
		return err
	}
	if due := strings.TrimSpace(in.DueOn); due != "" {
		if _, err := parseDate(field("due_on"), due); err != nil {
			return err
		}
	}
	switch in.Status {
	case StatusDraft, StatusSent, StatusViewed, StatusPaid, StatusVoid:
	default:
		return domain.Invalid(field("status"), "must be draft, sent, viewed, paid or void")
	}
	for name, v := range map[string]domain.Cents{"interest_cents": in.InterestCents, "amount_paid_cents": in.AmountPaidCents, "write_off_cents": in.WriteOffCents} {
		if negative(int64(v)) {
			return domain.Invalid(field(name), "cannot be negative")
		}
	}
	if len(in.Lines) == 0 {
		return domain.Invalid(field("lines"), "an invoice needs at least one line")
	}
	inv := Invoice{}
	for j, li := range in.Lines {
		lineField := fmt.Sprintf("items/%d/lines/%d", i, j)
		if li.AmountCents == nil {
			return domain.Invalid(lineField+"/amount_cents", "is required")
		}
		line := Line{Kind: li.Kind, SourceID: li.SourceID, Description: strings.TrimSpace(li.Description), FeeItemID: li.FeeItemID, AmountCents: *li.AmountCents}
		if line.Description == "" {
			return domain.Invalid(lineField+"/description", "is required")
		}
		if line.Kind != LineAdjustment && negative(int64(line.AmountCents)) {
			return domain.Invalid(lineField+"/amount_cents", "only an adjustment line may be negative")
		}
		inv.Lines = append(inv.Lines, line)
	}
	inv.AdjustmentCents, inv.InterestCents = in.AdjustmentCents, in.InterestCents
	inv.AmountPaidCents, inv.WriteOffCents = in.AmountPaidCents, in.WriteOffCents
	if err := recompute(&inv); err != nil {
		return err
	}
	if in.Status == StatusVoid {
		if positive(int64(in.AmountPaidCents)) {
			return domain.Invalid(field("amount_paid_cents"), "a void invoice carries no payment")
		}
		inv.BalanceDueCents = noCents
		if strings.TrimSpace(in.VoidReason) == "" {
			return domain.Invalid(field("void_reason"), "is required on a void invoice")
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
			return domain.Invalid(field(c.name), "does not reconcile: the lines and amounts give "+money(c.want))
		}
	}
	balance := int64(inv.BalanceDueCents)
	switch in.Status {
	case StatusDraft:
		if positive(int64(in.AmountPaidCents)) || positive(int64(in.WriteOffCents)) {
			return domain.Invalid(field("status"), "a draft has no payment or write-off")
		}
	case StatusSent, StatusViewed:
		if !positive(balance) {
			return domain.Invalid(field("status"), "a sent or viewed invoice has a balance due; with none it is paid")
		}
	case StatusPaid:
		if !isZero(balance) {
			return domain.Invalid(field("status"), "a paid invoice has no balance due")
		}
	}
	if in.Status != StatusDraft && negative(int64(inv.TotalCents)) {
		return domain.Invalid(field("total_cents"), "an invoice total cannot be negative")
	}
	if positive(int64(in.WriteOffCents)) && strings.TrimSpace(in.WriteOffReason) == "" {
		return domain.Invalid(field("write_off_reason"), "is required with a write-off")
	}
	if in.OpeningImport() && positive(int64(in.AmountPaidCents)) {
		if len(s.clientParties(in.MatterID)) == 0 {
			return domain.Invalid(field("matter_id"), "matter has no client party for the imported payment")
		}
	}
	_ = ctx
	return nil
}

func (s *Service) existingExternalRefs(ctx context.Context) (map[string]bool, error) {
	if refs, ok, err := s.externalRefsFromCore(ctx); ok {
		return refs, err
	}
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

// externalRefsFromCore reads the indexed invoices.external_ref column when dual-write is on.
func (s *Service) externalRefsFromCore(ctx context.Context) (map[string]bool, bool, error) {
	p, ok := s.d.Store.(*Postgres)
	if !ok || !p.publish {
		return nil, false, nil
	}
	rows, err := audit.Conn(ctx, p.pool).QueryContext(ctx, `SELECT external_ref FROM invoices WHERE external_ref <> ''`)
	if err != nil {
		// Table may be absent in older test DBs; fall back to document scan.
		if errorsIsUndefinedTable(err) {
			return nil, false, nil
		}
		return nil, true, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, true, err
		}
		if ref = strings.TrimSpace(ref); ref != "" {
			out[ref] = true
		}
	}
	return out, true, rows.Err()
}

func errorsIsUndefinedTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "undefined_table")
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
	// Floor before duplicate so IMP-02 names the floor even when a normal invoice already
	// occupies that number. billing.invoice_number_start=0 does not disable the floor:
	// it falls back to the lowest non-imported invoice number, or refuses until the admin
	// sets "Invoice numbering starts at".
	floor, floorLabel, err := s.openingImportFloor(ctx)
	if err != nil {
		return domain.Invalid(field("number"), err.Error())
	}
	if n, ok := parseImportNumber(number); ok && n >= floor {
		return domain.Invalid(field("number"), fmt.Sprintf("invoice number %s is at or above %s (%d)", number, floorLabel, floor))
	}
	if numbers[number] {
		return domain.Invalid(field("number"), "invoice number "+number+" is already used")
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
	if err := s.checkContactEmail(ctx, in, field("contact_email")); err != nil {
		return err
	}
	// Preview and commit share the same commit-side checks (IMP-03): dates, recompute /
	// reconciliation and the imported-payment client-party refusal.
	if err := s.validateImportCommitSide(ctx, in, i); err != nil {
		return err
	}
	_ = preview
	return nil
}

// openingImportFloor is the lowest historical number an opening import may use (exclusive
// upper bound: imports must be strictly below it). When billing.invoice_number_start is
// set above zero, that value is the floor. When it is 0 or unset, the floor is the lowest
// non-imported invoice number already in the practice; with no normal invoices yet the
// import is refused so the admin sets the setting first (CORE-37 / IMP-02).
func (s *Service) openingImportFloor(ctx context.Context) (floor int, label string, err error) {
	if start := s.settingInt(ctx, "billing.invoice_number_start"); positive(int64(start)) {
		return start, "invoice_number_start", nil
	}
	rows, err := invoiceHeads(ctx, s.d.Store, Where{})
	if err != nil {
		return int(0), "", err
	}
	found := false
	lowest := int(0)
	for _, r := range rows {
		if r.Imported {
			continue
		}
		n, ok := parseImportNumber(r.Number)
		if !ok {
			continue
		}
		if !found || n < lowest {
			lowest, found = n, true
		}
	}
	if !found {
		return int(0), "", fmt.Errorf(`set "Invoice numbering starts at" on the admin page first`)
	}
	return lowest, "the lowest normal invoice number", nil
}

func (s *Service) checkContactEmail(_ context.Context, in InvoiceImport, field string) error {
	email := strings.TrimSpace(in.ContactEmail)
	if email == "" {
		return nil
	}
	clients := s.clientParties(in.MatterID)
	for _, c := range clients {
		for _, row := range s.d.Practice.Emails(c) {
			if strings.EqualFold(strings.TrimSpace(row.Email), email) {
				return nil
			}
		}
	}
	return domain.Invalid(field, "contact_email does not match a client on the matter")
}

func (s *Service) dorFiles() DocumentOfRecordFiles {
	if d, ok := s.d.InvoiceFiles.(DocumentOfRecordFiles); ok {
		return d
	}
	return nil
}

// resolveOpeningFiles turns a CSV document_file_name into a file id and checks a typed id.
func (s *Service) resolveOpeningFiles(ctx context.Context, p domain.Principal, in InvoiceImport, i int) (InvoiceImport, error) {
	field := func(name string) string { return fmt.Sprintf("items/%d/%s", i, name) }
	name := strings.TrimSpace(in.DocumentFileName)
	if name != "" && in.DocumentOfRecordFileID.IsZero() {
		dor := s.dorFiles()
		if dor == nil {
			return in, domain.Invalid(field("document_file_name"), "document of record needs the file store")
		}
		id, err := dor.ResolveMatterFileName(ctx, p, in.MatterID, name)
		if err != nil {
			return in, rewriteField(err, field("document_file_name"))
		}
		in.DocumentOfRecordFileID = id
	}
	if !in.DocumentOfRecordFileID.IsZero() {
		if err := s.attachDocumentOfRecord(ctx, p, in.MatterID, in.DocumentOfRecordFileID, field("document_of_record_file_id"), false); err != nil {
			return in, err
		}
	}
	if err := s.checkContactEmail(ctx, in, field("contact_email")); err != nil {
		return in, err
	}
	return in, nil
}

func rewriteField(err error, field string) error {
	var api *domain.APIError
	if asOpeningAPI(err, &api) {
		return domain.Invalid(field, api.Message)
	}
	return err
}

// attachDocumentOfRecord validates the file; when share is set it also marks it client-shared.
func (s *Service) attachDocumentOfRecord(ctx context.Context, p domain.Principal, matter, fileID domain.ID, field string, share bool) error {
	dor := s.dorFiles()
	if dor == nil {
		return domain.Invalid(field, "document of record needs the file store")
	}
	if err := dor.CheckDocumentOfRecord(ctx, p, matter, fileID); err != nil {
		return rewriteField(err, field)
	}
	if share {
		if err := dor.ShareDocumentOfRecord(ctx, p, fileID); err != nil {
			return rewriteField(err, field)
		}
	}
	return nil
}

// DocumentOfRecordURL is the download path for an imported invoice's original PDF.
func (s *Service) DocumentOfRecordURL(ctx context.Context, p domain.Principal, inv Invoice) (string, error) {
	if !inv.Imported || inv.DocumentOfRecordFileID.IsZero() {
		return "", domain.NotFound("document of record")
	}
	if err := s.readInvoice(ctx, p, inv); err != nil {
		return "", err
	}
	dor := s.dorFiles()
	if dor == nil {
		return "", domain.NotFound("document of record")
	}
	return dor.DocumentDownloadPath(ctx, p, inv.DocumentOfRecordFileID)
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
