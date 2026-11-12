package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// DefaultInvoiceNumberFormat is used while billing.invoice_number_format is empty.
// YYYY is the issue year; the last run of two or more N is the zero-padded sequence.
const DefaultInvoiceNumberFormat = "INV-YYYY-NNNN"

// InvoiceFilter narrows an invoice list.
type InvoiceFilter struct {
	MatterID domain.ID
	Status   string
	FromDate string
	ToDate   string
	// Heads leaves Lines empty, for lists that show only the invoice header.
	Heads bool
	// Max stops the list after that many readable invoices; zero lists every one.
	Max int
}

// DraftRequest selects what to draft: one matter, several, or a date range across matters.
type DraftRequest struct {
	MatterID  domain.ID
	MatterIDs []domain.ID
	FromDate  string
	ToDate    string
}

// LineInput is one line on a draft edit. A nil amount on a time or expense line uses the
// computed amount; on a fee-item line it uses the catalog default.
type LineInput struct {
	Kind        string
	SourceID    domain.ID
	Description string
	AmountCents *domain.Cents
	FeeItemID   domain.ID
}

// DraftEdit replaces the lines on a draft.
type DraftEdit struct {
	Lines            []LineInput
	AdjustmentCents  *domain.Cents
	AdjustmentReason *string
	ClientMessage    *string
	IssuedOn         *string
}

// InvoicePatch changes fields that may change after the draft.
type InvoicePatch struct {
	ClientMessage *string
	InternalNotes *string
	DisputeStatus *string
	DisputeNotes  *string
	IssuedOn      *string
}

func (s *Service) invoice(ctx context.Context, id domain.ID) (Invoice, error) {
	var row Invoice
	if err := s.d.Store.Get(ctx, kindInvoice, string(id), &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Invoice{}, domain.NotFound("invoice")
		}
		return Invoice{}, err
	}
	return row, nil
}

// sharedWithClient is true once the invoice has gone to the client.
func sharedWithClient(inv Invoice) bool {
	return inv.Status == StatusSent || inv.Status == StatusViewed || inv.Status == StatusPaid
}

// ClientOnly reports a principal whose only firm-facing role is client.
func ClientOnly(p domain.Principal) bool {
	return p.HasRole(domain.RoleClient) && !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) &&
		!p.HasRole(domain.RoleStaff) && !p.HasRole(domain.RoleAccountant)
}

func (s *Service) readInvoice(ctx context.Context, p domain.Principal, inv Invoice) error {
	return s.gate.Allow(ctx, p, domain.ResourceRef{
		Resource: domain.ResInvoice, Action: domain.ActionRead, ID: inv.ID, MatterID: inv.MatterID,
		SharedWithClient: sharedWithClient(inv),
	})
}

// forReader returns inv as p may see it: a client-only principal gets only the InvoiceClient
// allowlist (ClientInvoice), so every surface built on the service (API, pages, MCP, modules,
// portal) is redacted here rather than in its handler.
func forReader(p domain.Principal, inv Invoice) Invoice {
	if ClientOnly(p) {
		return ClientInvoice(inv)
	}
	return inv
}

// Invoice returns one invoice. A client reading a sent invoice marks it viewed, and receives
// only the client allowlist.
func (s *Service) Invoice(ctx context.Context, p domain.Principal, id domain.ID) (Invoice, error) {
	inv, err := s.invoice(ctx, id)
	if err != nil {
		return Invoice{}, err
	}
	if err := s.readInvoice(ctx, p, inv); err != nil {
		return Invoice{}, err
	}
	if ClientOnly(p) && inv.Status == StatusSent {
		if inv, err = s.markViewed(ctx, p, id); err != nil {
			return Invoice{}, err
		}
	}
	return forReader(p, inv), nil
}

func (s *Service) markViewed(ctx context.Context, p domain.Principal, id domain.ID) (Invoice, error) {
	var out Invoice
	err := s.write(ctx, p, func(t *txn) error {
		inv, err := s.invoice(t.ctx, id)
		if err != nil {
			return err
		}
		out = inv
		if inv.Status != StatusSent {
			return nil
		}
		now := s.now()
		inv.Status = StatusViewed
		inv.ViewedAt = &now
		if err := t.putInvoice(inv); err != nil {
			return err
		}
		out = inv
		t.audit("invoice", id, "status", StatusSent, StatusViewed, "client viewed")
		return t.event("invoice.viewed", "invoice", id, inv.MatterID, "Client viewed invoice "+inv.Number, true)
	})
	return out, err
}

// Invoices lists invoices the caller can see. A client sees only shared invoices on its matters,
// each narrowed to the client allowlist.
func (s *Service) Invoices(ctx context.Context, p domain.Principal, f InvoiceFilter) ([]Invoice, error) {
	rows, err := invoiceHeads(ctx, s.d.Store, Where{MatterID: f.MatterID, FromDate: f.FromDate, ToDate: f.ToDate})
	if err != nil {
		return nil, err
	}
	if err := s.gate.Allow(ctx, p, domain.ResourceRef{Resource: domain.ResInvoice, Action: domain.ActionRead}); err != nil && !ClientOnly(p) {
		return nil, err
	}
	var out []Invoice
	for _, r := range rows {
		if !f.MatterID.IsZero() && r.MatterID != f.MatterID || f.Status != "" && r.Status != f.Status ||
			f.FromDate != "" && r.IssuedOn < f.FromDate || f.ToDate != "" && r.IssuedOn > f.ToDate {
			continue
		}
		err := s.readInvoice(ctx, p, r)
		if err == nil {
			if !f.Heads {
				// The header decode skipped the lines; read the whole invoice back.
				if r, err = s.invoice(ctx, r.ID); err != nil {
					return nil, err
				}
			}
			out = append(out, forReader(p, r))
			if f.Max > 0 && len(out) >= f.Max {
				break
			}
			continue
		}
		var api *domain.APIError
		if !errors.As(err, &api) {
			return nil, err
		}
	}
	return out, nil
}

func recompute(inv *Invoice) error {
	var lines []domain.Cents
	for _, l := range inv.Lines {
		lines = append(lines, l.AmountCents)
	}
	sub, err := sum(lines...)
	if err != nil {
		return domain.Invalid("lines", "amounts do not fit")
	}
	total, err := sum(sub, inv.AdjustmentCents, inv.InterestCents)
	if err != nil {
		return domain.Invalid("lines", "amounts do not fit")
	}
	owed, err := sum(inv.AmountPaidCents, inv.WriteOffCents)
	if err != nil {
		return domain.Invalid("lines", "amounts do not fit")
	}
	balance, err := total.Sub(owed)
	if err != nil {
		return domain.Invalid("lines", "amounts do not fit")
	}
	inv.SubtotalCents, inv.TotalCents, inv.BalanceDueCents = sub, total, balance
	return nil
}

// settle sets paid, sent or viewed from the balance on an invoice that has left draft.
func (s *Service) settle(inv *Invoice) {
	if inv.Status == StatusDraft || inv.Status == StatusVoid {
		return
	}
	if !positive(int64(inv.BalanceDueCents)) {
		if inv.Status != StatusPaid {
			inv.Status = StatusPaid
			inv.PaidAt = s.now().UnixMicro()
		}
		return
	}
	inv.PaidAt = int64(0)
	if inv.ViewedAt != nil {
		inv.Status = StatusViewed
	} else {
		inv.Status = StatusSent
	}
}

var nRun = regexp.MustCompile(`N{2,}`)

// nextNumber renders the firm invoice number format for the issue year with the next sequence.
func (s *Service) nextNumber(ctx context.Context, issuedOn string) (string, error) {
	format := strings.TrimSpace(s.setting(ctx, "billing.invoice_number_format"))
	if format == "" {
		format = DefaultInvoiceNumberFormat
	}
	all := nRun.FindAllStringIndex(format, -1)
	if all == nil {
		format += "-NNNN"
		all = nRun.FindAllStringIndex(format, -1)
	}
	loc := all[len(all)-1]
	lo, hi := loc[0], loc[len(loc)-1]
	year := issuedOn
	if len(year) >= len("YYYY") {
		year = year[:len("YYYY")]
	}
	prefix := strings.ReplaceAll(format[:lo], "YYYY", year)
	suffix := strings.ReplaceAll(format[hi:], "YYYY", year)
	width := hi - lo
	rows, err := invoiceHeads(ctx, s.d.Store, Where{})
	if err != nil {
		return "", err
	}
	var high int
	for _, r := range rows {
		if !strings.HasPrefix(r.Number, prefix) || !strings.HasSuffix(r.Number, suffix) || len(r.Number) < len(prefix)+len(suffix) {
			continue
		}
		if n, err := strconv.Atoi(r.Number[len(prefix) : len(r.Number)-len(suffix)]); err == nil && n > high {
			high = n
		}
	}
	return fmt.Sprintf("%s%0*d%s", prefix, width, high+1, suffix), nil
}

func hours(minutes int64) string {
	return fmt.Sprintf("%d.%02d", minutes/60, (minutes%60)*100/60)
}

func (s *Service) userName(ctx context.Context, id domain.ID) string {
	if u, err := s.d.Practice.User(ctx, id); err == nil && u.Name != "" {
		return u.Name
	}
	return "Unknown user"
}

func (s *Service) timeLine(ctx context.Context, e TimeEntry) (Line, error) {
	amount, err := timeAmount(e.RateCents, e.Minutes())
	if err != nil {
		return Line{}, err
	}
	desc := fmt.Sprintf("%s %s: %s (%s h at %s/h)", e.WorkedOn, s.userName(ctx, e.UserID), e.Description, hours(e.Minutes()), money(e.RateCents))
	return Line{Kind: LineTime, SourceID: e.ID, Description: desc, AmountCents: amount}, nil
}

func expenseLine(e Expense) (Line, error) {
	amount, err := expenseAmount(e.AmountCents, e.MarkupPercent)
	if err != nil {
		return Line{}, err
	}
	desc := e.IncurredOn + " " + e.Description
	if !isZero(int64(e.MarkupPercent)) {
		desc += fmt.Sprintf(" (%s plus %d%%)", money(e.AmountCents), e.MarkupPercent)
	}
	return Line{Kind: LineExpense, SourceID: e.ID, Description: desc, AmountCents: amount}, nil
}

// flatFeeBilled reports a non-void invoice on the matter that already bills the flat fee.
func (s *Service) flatFeeInvoice(ctx context.Context, matter domain.ID) (Invoice, bool, error) {
	rows, _, err := findOf[Invoice](ctx, s.d.Store, kindInvoice, Where{MatterID: matter})
	if err != nil {
		return Invoice{}, false, err
	}
	for _, r := range rows {
		if r.MatterID == matter && r.Status != StatusVoid && r.HasFlatFee() {
			return r, true, nil
		}
	}
	return Invoice{}, false, nil
}

// matterFeeLine bills the matter's own flat fee. Only this line marks the fee as billed.
func matterFeeLine(m domain.Matter) Line {
	return Line{Kind: LineFlatFee, Description: "Flat fee: " + m.Name, AmountCents: m.FlatFeeCents, MatterFee: true}
}

// pending gathers the matter's unbilled lines. Flat-fee matters bill the fee and expenses;
// hourly, contingency and pro bono matters bill time and expenses; mixed matters bill all three.
func (s *Service) pending(ctx context.Context, m domain.Matter, from, to string) ([]Line, []TimeEntry, []Expense, error) {
	var lines []Line
	var times []TimeEntry
	var costs []Expense
	if m.BillingType == billingFlat || m.BillingType == billingMixed {
		_, billed, err := s.flatFeeInvoice(ctx, m.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		applied := false
		if !billed && m.BillingType == billingFlat && s.d.Trust != nil {
			// A fee applied from the trust page has no billing invoice; billing it again
			// would charge the client for money already taken from trust.
			cents, err := s.d.Trust.FlatFeeCollected(ctx, m.ID)
			if err != nil {
				return nil, nil, nil, err
			}
			applied = positive(int64(cents))
		}
		if !billed && !applied && positive(int64(m.FlatFeeCents)) {
			lines = append(lines, matterFeeLine(m))
		}
	}
	if m.BillingType != billingFlat {
		f := TimeFilter{MatterID: m.ID, FromDate: from, ToDate: to, Unbilled: true}
		rows, _, err := findOf[TimeEntry](ctx, s.d.Store, kindTime, f.where())
		if err != nil {
			return nil, nil, nil, err
		}
		for _, e := range rows {
			if !f.match(e) || !e.Billable || !e.Reviewed() {
				continue
			}
			line, err := s.timeLine(ctx, e)
			if err != nil {
				return nil, nil, nil, err
			}
			lines = append(lines, line)
			times = append(times, e)
		}
	}
	f := ExpenseFilter{MatterID: m.ID, FromDate: from, ToDate: to, Unbilled: true}
	rows, _, err := findOf[Expense](ctx, s.d.Store, kindExpense, Where{MatterID: m.ID, FromDate: from, ToDate: to, Unbilled: true})
	if err != nil {
		return nil, nil, nil, err
	}
	for _, e := range rows {
		if !f.match(e) || !e.Billable {
			continue
		}
		line, err := expenseLine(e)
		if err != nil {
			return nil, nil, nil, err
		}
		lines = append(lines, line)
		costs = append(costs, e)
	}
	return lines, times, costs, nil
}

// DraftInvoices drafts invoices from unbilled time and expenses. Naming a matter the caller
// cannot bill fails and drafts nothing; a date range is refused for a role that cannot draft and
// otherwise drafts only matters the caller may bill.
func (s *Service) DraftInvoices(ctx context.Context, p domain.Principal, req DraftRequest) ([]Invoice, error) {
	byRange := req.FromDate != "" || req.ToDate != ""
	if byRange {
		if _, err := parseDate("from_date", req.FromDate); err != nil {
			return nil, err
		}
		if _, err := parseDate("to_date", req.ToDate); err != nil {
			return nil, err
		}
		if req.FromDate > req.ToDate {
			return nil, domain.Invalid("from_date", "must be on or before to_date")
		}
	}
	if !exactlyOne(!req.MatterID.IsZero(), len(req.MatterIDs) > 0, byRange) {
		return nil, domain.Invalid("selector", "set exactly one of matter_id, matter_ids, or from_date with to_date")
	}
	if byRange {
		// A date range drafts across matters; a role with no draft right is refused outright
		// instead of drafting an empty list.
		if err := s.allow(ctx, p, domain.ResInvoice, domain.ActionWrite, ""); err != nil {
			return nil, err
		}
	}
	var ids []domain.ID
	named := true
	switch {
	case !req.MatterID.IsZero():
		ids = []domain.ID{req.MatterID}
	case len(req.MatterIDs) > 0:
		seen := map[domain.ID]bool{}
		for _, id := range req.MatterIDs {
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	default:
		named = false
		for _, m := range s.mattersByNumber() {
			if m.Status == matterClosed {
				continue
			}
			if err := s.allow(ctx, p, domain.ResInvoice, domain.ActionWrite, m.ID); err == nil {
				ids = append(ids, m.ID)
			}
		}
	}
	if named {
		for _, id := range ids {
			if err := s.allow(ctx, p, domain.ResInvoice, domain.ActionWrite, id); err != nil {
				return nil, err
			}
			if _, err := s.matter(ctx, id); err != nil {
				return nil, err
			}
		}
	}
	var out []Invoice
	err := s.write(ctx, p, func(t *txn) error {
		for _, id := range ids {
			m, err := s.matter(t.ctx, id)
			if err != nil {
				return err
			}
			inv, ok, err := s.draftOne(t, m, req.FromDate, req.ToDate)
			if err != nil {
				return err
			}
			if !ok {
				if named {
					return domain.Conflict("matter " + m.Number + " has nothing unbilled")
				}
				continue
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

func (s *Service) draftOne(t *txn, m domain.Matter, from, to string) (Invoice, bool, error) {
	lines, times, costs, err := s.pending(t.ctx, m, from, to)
	if err != nil || len(lines) == 0 {
		return Invoice{}, false, err
	}
	meta, err := s.meta(t.p)
	if err != nil {
		return Invoice{}, false, err
	}
	issued := s.today(t.ctx)
	number, err := s.nextNumber(t.ctx, issued)
	if err != nil {
		return Invoice{}, false, err
	}
	inv := Invoice{Lines: lines, FromDate: from, ToDate: to, MatterName: m.Name, MatterNum: m.Number}
	inv.Invoice = domain.Invoice{
		Meta: meta, MatterID: m.ID, Number: number, IssuedOn: issued, Status: StatusDraft,
		ClientMessage: s.setting(t.ctx, "billing.default_client_message"),
	}
	inv.DueOn = addDays(issued, s.settingInt(t.ctx, "billing.payment_terms_days"))
	if err := recompute(&inv); err != nil {
		return Invoice{}, false, err
	}
	for _, e := range times {
		e.InvoiceID = inv.ID
		e.UpdatedAt = s.now()
		if err := t.put(kindTime, e.ID, e); err != nil {
			return Invoice{}, false, err
		}
		t.audit("time_entry", e.ID, "invoice_id", "", string(inv.ID), "drafted on "+number)
	}
	for _, e := range costs {
		e.InvoiceID = inv.ID
		e.UpdatedAt = s.now()
		if err := t.put(kindExpense, e.ID, e); err != nil {
			return Invoice{}, false, err
		}
		t.audit("expense", e.ID, "invoice_id", "", string(inv.ID), "drafted on "+number)
	}
	if err := t.putInvoice(inv); err != nil {
		return Invoice{}, false, err
	}
	if err := t.created("invoice", inv.ID, inv, ""); err != nil {
		return Invoice{}, false, err
	}
	return inv, true, t.event("invoice.drafted", "invoice", inv.ID, m.ID, "Invoice "+number+" drafted", false)
}

// writable loads an invoice for a write the caller must be allowed on its matter.
func (s *Service) writable(t *txn, id domain.ID, act domain.Action) (Invoice, error) {
	inv, err := s.invoice(t.ctx, id)
	if err != nil {
		return Invoice{}, err
	}
	if err := s.allow(t.ctx, t.p, domain.ResInvoice, act, inv.MatterID); err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

// EditDraft replaces the lines of a draft. Removed time and expense rows return to unbilled;
// added rows must be unbilled on the same matter. Any edit clears the approval.
func (s *Service) EditDraft(ctx context.Context, p domain.Principal, id domain.ID, in DraftEdit) (Invoice, error) {
	var out Invoice
	err := s.write(ctx, p, func(t *txn) error {
		before, err := s.writable(t, id, domain.ActionWrite)
		if err != nil {
			return err
		}
		if before.Status != StatusDraft {
			return domain.Conflict("only a draft invoice can be edited")
		}
		after := before
		after.Lines = nil
		held := map[domain.ID]bool{}
		for _, l := range before.Lines {
			if !l.SourceID.IsZero() {
				held[l.SourceID] = true
			}
		}
		used := map[domain.ID]bool{}
		// The matter-fee mark stays on the first flat-fee line that is not a catalog item, so
		// editing its text or amount keeps the fee billed. Removing every such line releases it.
		keepFee := before.HasFlatFee()
		for i, li := range in.Lines {
			line, err := s.draftLine(t, before, li, held, i)
			if err != nil {
				return err
			}
			if keepFee && line.Kind == LineFlatFee && line.FeeItemID.IsZero() {
				line.MatterFee, keepFee = true, false
			}
			if !line.SourceID.IsZero() {
				if used[line.SourceID] {
					return domain.Invalid(fmt.Sprintf("lines/%d/source_id", i), "a source is billed once")
				}
				used[line.SourceID] = true
			}
			after.Lines = append(after.Lines, line)
		}
		if in.AdjustmentCents != nil {
			after.AdjustmentCents = *in.AdjustmentCents
		}
		if in.AdjustmentReason != nil {
			after.AdjustmentReason = strings.TrimSpace(*in.AdjustmentReason)
		}
		if !isZero(int64(after.AdjustmentCents)) && after.AdjustmentReason == "" {
			return domain.Invalid("adjustment_reason", "an adjustment needs a reason")
		}
		if in.ClientMessage != nil {
			after.ClientMessage = *in.ClientMessage
		}
		if in.IssuedOn != nil {
			if _, err := parseDate("issued_on", *in.IssuedOn); err != nil {
				return err
			}
			after.IssuedOn = *in.IssuedOn
			after.DueOn = addDays(after.IssuedOn, s.settingInt(t.ctx, "billing.payment_terms_days"))
		}
		after.ApprovedAt, after.ApprovedBy = nil, ""
		if err := recompute(&after); err != nil {
			return err
		}
		if err := s.relink(t, before, used); err != nil {
			return err
		}
		if err := t.putInvoice(after); err != nil {
			return err
		}
		if err := t.diff("invoice", id, before, after, ""); err != nil {
			return err
		}
		out = after
		return t.event("invoice.updated", "invoice", id, after.MatterID, "Draft invoice "+after.Number+" edited", false)
	})
	return out, err
}

func (s *Service) draftLine(t *txn, inv Invoice, li LineInput, held map[domain.ID]bool, i int) (Line, error) {
	field := fmt.Sprintf("lines/%d", i)
	line := Line{Kind: li.Kind, SourceID: li.SourceID, Description: strings.TrimSpace(li.Description), FeeItemID: li.FeeItemID}
	switch li.Kind {
	case LineTime, LineExpense:
		if li.SourceID.IsZero() {
			return Line{}, domain.Invalid(field+"/source_id", "a time or expense line names its source")
		}
		var computed Line
		var matter, billedOn domain.ID
		if li.Kind == LineTime {
			e, err := s.timeEntry(t.ctx, li.SourceID)
			if err != nil {
				return Line{}, domain.Invalid(field+"/source_id", "unknown time entry")
			}
			if !e.Reviewed() {
				return Line{}, domain.Conflict("time entry is a timer draft; review it with a description before billing it")
			}
			if !e.Billable {
				return Line{}, domain.Invalid(field+"/source_id", "time entry is not billable; mark it billable first")
			}
			if computed, err = s.timeLine(t.ctx, e); err != nil {
				return Line{}, err
			}
			matter, billedOn = e.MatterID, e.InvoiceID
		} else {
			e, err := s.expense(t.ctx, li.SourceID)
			if err != nil {
				return Line{}, domain.Invalid(field+"/source_id", "unknown expense")
			}
			if !e.Billable {
				return Line{}, domain.Invalid(field+"/source_id", "expense is not billable; mark it billable first")
			}
			if computed, err = expenseLine(e); err != nil {
				return Line{}, err
			}
			matter, billedOn = e.MatterID, e.InvoiceID
		}
		if matter != inv.MatterID {
			return Line{}, domain.Invalid(field+"/source_id", "source is on another matter")
		}
		if !billedOn.IsZero() && (billedOn != inv.ID || !held[li.SourceID]) {
			return Line{}, domain.Conflict("source is already billed on another invoice")
		}
		if line.Description == "" {
			line.Description = computed.Description
		}
		line.AmountCents = computed.AmountCents
	case LineFlatFee:
		if !li.FeeItemID.IsZero() {
			item, err := s.feeItem(t.ctx, li.FeeItemID)
			if err != nil {
				return Line{}, domain.Invalid(field+"/fee_item_id", "unknown fee item")
			}
			onInvoice := false
			for _, l := range inv.Lines {
				onInvoice = onInvoice || l.FeeItemID == item.ID
			}
			if !item.Active && !onInvoice {
				return Line{}, domain.Invalid(field+"/fee_item_id", "fee item is not active")
			}
			if line.Description == "" {
				line.Description = item.Name
			}
			line.AmountCents = item.DefaultAmountCents
		}
	case LineAdjustment:
	default:
		return Line{}, domain.Invalid(field+"/kind", "must be time, expense, flat_fee or adjustment")
	}
	if li.AmountCents != nil {
		line.AmountCents = *li.AmountCents
	}
	if line.Description == "" {
		return Line{}, domain.Invalid(field+"/description", "is required")
	}
	if line.Kind != LineAdjustment && negative(int64(line.AmountCents)) {
		return Line{}, domain.Invalid(field+"/amount_cents", "only an adjustment line may be negative")
	}
	return line, nil
}

// relink points billed rows at the invoice and releases rows that left it.
func (s *Service) relink(t *txn, inv Invoice, keep map[domain.ID]bool) error {
	for _, l := range inv.Lines {
		if l.SourceID.IsZero() || keep[l.SourceID] {
			continue
		}
		if err := s.setBilled(t, l.Kind, l.SourceID, "", inv.Number); err != nil {
			return err
		}
	}
	held := map[domain.ID]bool{}
	for _, l := range inv.Lines {
		held[l.SourceID] = true
	}
	for id := range keep {
		if held[id] {
			continue
		}
		kind := LineTime
		if _, err := s.timeEntry(t.ctx, id); err != nil {
			kind = LineExpense
		}
		if err := s.setBilled(t, kind, id, inv.ID, inv.Number); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) setBilled(t *txn, kind string, id, invoice domain.ID, number string) error {
	reason := "released from " + number
	if !invoice.IsZero() {
		reason = "billed on " + number
	}
	switch kind {
	case LineTime:
		e, err := s.timeEntry(t.ctx, id)
		if err != nil {
			return err
		}
		old := e.InvoiceID
		e.InvoiceID, e.UpdatedAt = invoice, s.now()
		if err := t.put(kindTime, id, e); err != nil {
			return err
		}
		t.audit("time_entry", id, "invoice_id", string(old), string(invoice), reason)
	case LineExpense:
		e, err := s.expense(t.ctx, id)
		if err != nil {
			return err
		}
		old := e.InvoiceID
		e.InvoiceID, e.UpdatedAt = invoice, s.now()
		if err := t.put(kindExpense, id, e); err != nil {
			return err
		}
		t.audit("expense", id, "invoice_id", string(old), string(invoice), reason)
	}
	return nil
}

// ApproveInvoice records an attorney or admin approval of a draft.
func (s *Service) ApproveInvoice(ctx context.Context, p domain.Principal, id domain.ID) (Invoice, error) {
	var out Invoice
	err := s.write(ctx, p, func(t *txn) error {
		inv, err := s.writable(t, id, domain.ActionApproveInvoice)
		if err != nil {
			return err
		}
		if inv.Status != StatusDraft {
			return domain.Conflict("only a draft invoice can be approved")
		}
		if len(inv.Lines) == 0 {
			return domain.Conflict("an invoice needs at least one line")
		}
		if negative(int64(inv.TotalCents)) {
			return domain.Conflict("an invoice total cannot be negative")
		}
		now := s.now()
		inv.ApprovedAt, inv.ApprovedBy = &now, actor(p)
		if err := t.putInvoice(inv); err != nil {
			return err
		}
		out = inv
		t.audit("invoice", id, "approved_by", "", string(actor(p)), "")
		return t.event("invoice.approved", "invoice", id, inv.MatterID, "Invoice "+inv.Number+" approved", false)
	})
	return out, err
}

// clientEmails returns the primary current email of each client party, or any current email.
// Current means in effect on the firm-local date: started (EffectiveFrom) and not ended
// (EffectiveTo), so an address scheduled for later never receives an invoice early.
func (s *Service) clientEmails(ctx context.Context, matter domain.ID) []string {
	today := s.today(ctx)
	var out []string
	for _, contact := range s.clientParties(matter) {
		var pick string
		for _, e := range s.d.Practice.Emails(contact) {
			if e.Email == "" || e.EffectiveTo != "" && e.EffectiveTo < today || e.EffectiveFrom != "" && e.EffectiveFrom > today {
				continue
			}
			if pick == "" || e.Primary {
				pick = e.Email
			}
			if e.Primary {
				break
			}
		}
		if pick != "" {
			out = append(out, pick)
		}
	}
	return out
}

// SendInvoice sends an approved draft. deliver email mails the PDF by system mail to the
// matter's clients; deliver file marks it sent and the user downloads the PDF. A sent invoice
// can be emailed again; its status does not change.
//
// The send commits first and the mail goes out only after the commit, so a failed commit never
// emails the client. If system mail then fails, a second write undoes the send (when nothing
// else changed the invoice in between) and appends an invoice.send_undone event after the
// invoice.sent event, which stays in the append-only stream; both writes are audited.
// ponytail: ceiling: a crash between the commit and the mail leaves the invoice sent with no
// email; the user sees no error-free confirmation and can send by email again. Upgrade path: a
// system-mail outbox row written in the same transaction and delivered by a retrying worker.
// Trigger: core gains a system-mail outbox, or a firm reports a sent invoice that never arrived.
func (s *Service) SendInvoice(ctx context.Context, p domain.Principal, id domain.ID, deliver string) (Invoice, error) {
	if deliver != "email" && deliver != "file" {
		return Invoice{}, domain.Invalid("deliver", "must be email or file")
	}
	var out, before Invoice
	var msg *Message
	err := s.write(ctx, p, func(t *txn) error {
		inv, err := s.writable(t, id, domain.ActionApproveInvoice)
		if err != nil {
			return err
		}
		switch inv.Status {
		case StatusDraft:
			if !inv.Approved() {
				return domain.Conflict("an attorney or admin must approve the draft before it is sent")
			}
		case StatusSent, StatusViewed:
			if deliver != "email" {
				return domain.Conflict("invoice is already sent")
			}
		default:
			return domain.Conflict("a " + inv.Status + " invoice cannot be sent")
		}
		before = inv
		if deliver == "email" {
			if s.d.Mail == nil {
				return domain.Conflict("system mail is not configured")
			}
			to := s.clientEmails(t.ctx, inv.MatterID)
			if len(to) == 0 {
				return domain.Invalid("deliver", "the matter's client has no email address")
			}
			sending := inv
			if sending.Status == StatusDraft {
				sending.Status = StatusSent
			}
			pdf, err := s.renderPDF(t.ctx, sending)
			if err != nil {
				return err
			}
			firm := s.setting(t.ctx, "firm.name")
			msg = &Message{
				To: to, Subject: strings.TrimSpace(firm + " invoice " + inv.Number),
				Text:        invoiceMailText(inv, firm),
				Attachments: []Attachment{{Name: "invoice-" + inv.Number + ".pdf", ContentType: "application/pdf", Data: pdf}},
			}
			inv.SentTo = to
		}
		if inv.Status == StatusDraft {
			inv.Status = StatusSent
			inv.SentAt = s.now().UnixMicro()
			s.settle(&inv)
		}
		if err := t.putInvoice(inv); err != nil {
			return err
		}
		if err := t.diff("invoice", id, before, inv, "deliver "+deliver); err != nil {
			return err
		}
		if err := t.event("invoice.sent", "invoice", id, inv.MatterID, "Invoice "+inv.Number+" sent by "+deliver, true); err != nil {
			return err
		}
		out, err = s.invoice(t.ctx, id)
		return err
	})
	if err != nil {
		return Invoice{}, err
	}
	if msg != nil {
		if mailErr := s.d.Mail.Send(ctx, *msg); mailErr != nil {
			mailErr = fmt.Errorf("billing: system mail: %w", mailErr)
			if err := s.undoSend(ctx, p, before, out, mailErr); err != nil {
				return Invoice{}, errors.Join(mailErr, err)
			}
			return Invoice{}, mailErr
		}
	}
	// The sent PDF goes to the file store: the attachment's own bytes for an email send.
	var sentPDF []byte
	if msg != nil {
		sentPDF = msg.Attachments[0].Data
	}
	out = s.keepPDF(ctx, p, out, sentPDF)
	// A flat fee applied on the trust page before the invoice went out is credited now, so
	// the invoice does not ask the client for it or stay open for Apply trust.
	if out.HasFlatFee() {
		s.syncAfter(ctx, out.MatterID)
		if cur, err := s.invoice(ctx, out.ID); err == nil {
			out = cur
		}
	}
	return out, nil
}

// undoSend restores the delivery fields a send changed after its mail failed and appends a
// compensating invoice.send_undone event; the invoice.sent event is never removed. When another
// write changed the invoice in the meantime it only records the failure.
func (s *Service) undoSend(ctx context.Context, p domain.Principal, before, sent Invoice, cause error) error {
	return s.write(context.WithoutCancel(ctx), p, func(t *txn) error {
		cur, err := s.invoice(t.ctx, sent.ID)
		if err != nil {
			return err
		}
		reason := "system mail failed: " + cause.Error()
		if !cur.UpdatedAt.Equal(sent.UpdatedAt) {
			t.audit("invoice", cur.ID, "deliver", "email", "failed", reason+"; the invoice changed since, so the send stands")
			return nil
		}
		after := cur
		after.Status, after.SentAt, after.SentTo, after.PaidAt = before.Status, before.SentAt, before.SentTo, before.PaidAt
		if err := t.putInvoice(after); err != nil {
			return err
		}
		if err := t.diff("invoice", cur.ID, cur, after, reason+"; send undone"); err != nil {
			return err
		}
		return t.event("invoice.send_undone", "invoice", cur.ID, cur.MatterID, "Invoice "+cur.Number+" send undone: system mail failed", true)
	})
}

func invoiceMailText(inv Invoice, firm string) string {
	var b strings.Builder
	if inv.ClientMessage != "" {
		b.WriteString(inv.ClientMessage + "\n\n")
	}
	fmt.Fprintf(&b, "Invoice %s dated %s is attached.\nAmount due: %s, due by %s.\n", inv.Number, inv.IssuedOn, money(inv.BalanceDueCents), inv.DueOn)
	if firm != "" {
		b.WriteString("\n" + firm + "\n")
	}
	return b.String()
}

// UpdateInvoice patches the message, notes, dispute and issue date.
func (s *Service) UpdateInvoice(ctx context.Context, p domain.Principal, id domain.ID, in InvoicePatch) (Invoice, error) {
	var out Invoice
	err := s.write(ctx, p, func(t *txn) error {
		before, err := s.writable(t, id, domain.ActionWrite)
		if err != nil {
			return err
		}
		if before.Status == StatusVoid {
			return domain.Conflict("a void invoice cannot change")
		}
		after := before
		if in.ClientMessage != nil {
			after.ClientMessage = *in.ClientMessage
		}
		if in.InternalNotes != nil {
			after.InternalNotes = *in.InternalNotes
		}
		if in.DisputeStatus != nil {
			switch *in.DisputeStatus {
			case "", "none", "disputed", "resolved":
			default:
				return domain.Invalid("dispute_status", "must be none, disputed or resolved")
			}
			after.DisputeStatus = *in.DisputeStatus
		}
		if in.DisputeNotes != nil {
			after.DisputeNotes = *in.DisputeNotes
		}
		if in.IssuedOn != nil {
			if _, err := parseDate("issued_on", *in.IssuedOn); err != nil {
				return err
			}
			after.IssuedOn = *in.IssuedOn
			after.DueOn = addDays(after.IssuedOn, s.settingInt(t.ctx, "billing.payment_terms_days"))
		}
		if err := t.putInvoice(after); err != nil {
			return err
		}
		out = after
		if err := t.diff("invoice", id, before, after, ""); err != nil {
			return err
		}
		return t.event("invoice.updated", "invoice", id, after.MatterID, "Invoice "+after.Number+" updated", false)
	})
	return out, err
}

// VoidInvoice voids an invoice and returns its time and expenses to unbilled. An invoice with
// a completed or pending payment must have that payment reversed first.
func (s *Service) VoidInvoice(ctx context.Context, p domain.Principal, id domain.ID, reason string) (Invoice, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Invoice{}, domain.Invalid("reason", "is required")
	}
	var out Invoice
	err := s.write(ctx, p, func(t *txn) error {
		inv, err := s.writable(t, id, domain.ActionApproveInvoice)
		if err != nil {
			return err
		}
		if inv.Status == StatusVoid {
			return domain.Conflict("invoice is already void")
		}
		pays, err := listOf[Payment](t.ctx, s.d.Store, kindPayment)
		if err != nil {
			return err
		}
		for _, pay := range pays {
			if pay.InvoiceID == id && (pay.Status == PayCompleted || pay.Status == PayPending) {
				return domain.Conflict("reverse the payments on this invoice before voiding it")
			}
		}
		before := inv
		for _, l := range inv.Lines {
			if !l.SourceID.IsZero() {
				if err := s.setBilled(t, l.Kind, l.SourceID, "", inv.Number); err != nil {
					return err
				}
			}
		}
		now := s.now()
		inv.Status, inv.VoidReason, inv.VoidedAt = StatusVoid, reason, &now
		inv.BalanceDueCents = noCents
		if err := t.putInvoice(inv); err != nil {
			return err
		}
		out = inv
		if err := t.diff("invoice", id, before, inv, reason); err != nil {
			return err
		}
		return t.event("invoice.voided", "invoice", id, inv.MatterID, "Invoice "+inv.Number+" voided", false)
	})
	return out, err
}

// WriteOff writes off part or all of an open invoice's balance with a reason.
func (s *Service) WriteOff(ctx context.Context, p domain.Principal, id domain.ID, amount domain.Cents, reason string) (Invoice, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Invoice{}, domain.Invalid("reason", "is required")
	}
	if err := amountField("amount_cents", amount); err != nil {
		return Invoice{}, err
	}
	var out Invoice
	err := s.write(ctx, p, func(t *txn) error {
		inv, err := s.writable(t, id, domain.ActionApproveInvoice)
		if err != nil {
			return err
		}
		if !inv.Open() {
			return domain.Conflict("only a sent invoice with a balance can be written off")
		}
		if amount > inv.BalanceDueCents {
			return domain.Invalid("amount_cents", "is more than the balance due "+money(inv.BalanceDueCents))
		}
		before := inv
		if inv.WriteOffCents, err = sum(inv.WriteOffCents, amount); err != nil {
			return domain.Invalid("amount_cents", "does not fit")
		}
		inv.WriteOffs = append(inv.WriteOffs, WriteOff{On: s.today(t.ctx), AmountCents: amount, Reason: reason, By: actor(t.p)})
		if inv.WriteOffReason != "" {
			reason = inv.WriteOffReason + "; " + reason
		}
		inv.WriteOffReason = reason
		if err := recompute(&inv); err != nil {
			return err
		}
		s.settle(&inv)
		if err := t.putInvoice(inv); err != nil {
			return err
		}
		out = inv
		if err := t.diff("invoice", id, before, inv, ""); err != nil {
			return err
		}
		return t.event("invoice.updated", "invoice", id, inv.MatterID, "Invoice "+inv.Number+" written off "+money(amount), false)
	})
	return out, err
}

// ApplyLateInterest sets simple late interest on an open invoice past its due date and grace
// period, when billing.late_interest_enabled is on. It replaces the previous interest, so a
// rerun on the same day changes nothing.
func (s *Service) ApplyLateInterest(ctx context.Context, p domain.Principal, id domain.ID) (Invoice, error) {
	if s.setting(ctx, "billing.late_interest_enabled") != "true" {
		return Invoice{}, domain.Conflict("late interest is off")
	}
	pct := s.settingInt(ctx, "billing.late_interest_annual_percent")
	var out Invoice
	err := s.write(ctx, p, func(t *txn) error {
		inv, err := s.writable(t, id, domain.ActionApproveInvoice)
		if err != nil {
			return err
		}
		if !inv.Open() {
			return domain.Conflict("only a sent invoice with a balance accrues interest")
		}
		late := daysBetween(addDays(inv.DueOn, s.settingInt(t.ctx, "billing.grace_period_days")), s.today(t.ctx))
		if !positive(int64(late)) {
			return domain.Conflict("invoice is not past its grace period")
		}
		base, err := sum(inv.SubtotalCents, inv.AdjustmentCents, -inv.AmountPaidCents, -inv.WriteOffCents)
		if err != nil {
			return err
		}
		var interest domain.Cents
		if positive(int64(base)) {
			if interest, err = lateInterest(base, pct, late); err != nil {
				return err
			}
		}
		before := inv
		inv.InterestCents = interest
		if err := recompute(&inv); err != nil {
			return err
		}
		s.settle(&inv)
		if err := t.putInvoice(inv); err != nil {
			return err
		}
		out = inv
		if err := t.diff("invoice", id, before, inv, fmt.Sprintf("%d days late at %d%%", late, pct)); err != nil {
			return err
		}
		return t.event("invoice.updated", "invoice", id, inv.MatterID, "Late interest on "+inv.Number, false)
	})
	return out, err
}

// keepPDF stores a sent invoice's PDF in the file store as the next version of its invoice_pdf
// file record, when the bytes differ from the newest stored version, and links the file to the
// invoice (PDFFileID). A draft is not stored. pdf nil renders the invoice as it is now. The
// send or download that triggered it has already succeeded, so a file store failure is
// audited on the invoice and retried by the next download, not returned.
// ponytail: ceiling: the file and the invoice link commit in two transactions, so a crash
// between them leaves an unlinked invoice_pdf record and the next download adds another; one
// core process is assumed (pdfMu, like Service.mu). Upgrade trigger: a second core replica, or
// files.Create gains a way to join billing's transaction with after-commit events.
func (s *Service) keepPDF(ctx context.Context, p domain.Principal, inv Invoice, pdf []byte) Invoice {
	if s.d.InvoiceFiles == nil || inv.Status == StatusDraft {
		return inv
	}
	ctx = context.WithoutCancel(ctx)
	s.pdfMu.Lock()
	defer s.pdfMu.Unlock()
	cur, err := s.invoice(ctx, inv.ID)
	if err != nil || cur.Status == StatusDraft {
		return inv
	}
	if pdf == nil {
		if pdf, err = s.renderPDF(ctx, cur); err != nil {
			s.pdfFailed(ctx, p, cur, err)
			return inv
		}
	}
	sum := sha256.Sum256(pdf)
	hash := hex.EncodeToString(sum[:])
	if !cur.PDFFileID.IsZero() && cur.PDFSHA256 == hash {
		return inv
	}
	by := p.UserID
	if by.IsZero() {
		by = domain.SystemUserID
	}
	fileID, err := s.d.InvoiceFiles.SaveInvoicePDF(ctx, by, cur, cur.PDFFileID, pdf)
	if err != nil {
		s.pdfFailed(ctx, p, cur, err)
		return inv
	}
	err = s.write(ctx, p, func(t *txn) error {
		row, err := s.invoice(t.ctx, cur.ID)
		if err != nil {
			return err
		}
		before := row
		row.PDFFileID, row.PDFSHA256 = fileID, hash
		if err := t.putInvoice(row); err != nil {
			return err
		}
		return t.diff("invoice", row.ID, before, row, "invoice PDF stored in the file store")
	})
	if err != nil {
		s.pdfFailed(ctx, p, cur, err)
		return inv
	}
	inv.PDFFileID, inv.PDFSHA256 = fileID, hash
	return inv
}

// pdfFailed audits a PDF the file store did not take.
func (s *Service) pdfFailed(ctx context.Context, p domain.Principal, inv Invoice, cause error) {
	_ = s.write(ctx, p, func(t *txn) error {
		t.audit("invoice", inv.ID, "pdf_file", string(inv.PDFFileID), "failed", "file store: "+cause.Error())
		return nil
	})
}

// InvoicePDF renders the invoice PDF and records the download in the audit log and timeline.
// A sent invoice's PDF is also kept in the file store (keepPDF).
// The returned row is narrowed to the client allowlist for a client-only principal.
func (s *Service) InvoicePDF(ctx context.Context, p domain.Principal, id domain.ID) ([]byte, Invoice, error) {
	inv, err := s.invoice(ctx, id)
	if err != nil {
		return nil, Invoice{}, err
	}
	if err := s.readInvoice(ctx, p, inv); err != nil {
		return nil, Invoice{}, err
	}
	pdf, err := s.renderPDF(ctx, inv)
	if err != nil {
		return nil, Invoice{}, err
	}
	client := ClientOnly(p)
	err = s.write(ctx, p, func(t *txn) error {
		t.audit("invoice", id, "download", "", "invoice-"+inv.Number+".pdf", "")
		return t.event("file.downloaded", "invoice", id, inv.MatterID, "Invoice "+inv.Number+" PDF downloaded", client)
	})
	if err != nil {
		return nil, Invoice{}, err
	}
	inv = s.keepPDF(ctx, p, inv, pdf)
	return pdf, forReader(p, inv), nil
}
