package billing

import (
	"context"
	"sort"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// FlatRow is one flat-fee matter on the Billing / Generate worklist.
type FlatRow struct {
	MatterID     domain.ID
	MatterNumber string
	MatterName   string
	ClientID     domain.ID
	ClientName   string
	AccountID    domain.ID
	FlatFeeCents domain.Cents
	TrustCents   domain.Cents
	Status       string
	InvoiceID    domain.ID
}

// HourlyRow is one hourly or mixed matter with unbilled work.
type HourlyRow struct {
	MatterID      domain.ID
	MatterNumber  string
	MatterName    string
	ClientName    string
	UnbilledCents domain.Cents
}

// Worklist is the Billing / Generate page (PRD 8.15).
type Worklist struct {
	Flat   []FlatRow
	Hourly []HourlyRow
}

// StatusLabel is the words the worklist shows for a flat-fee status.
func StatusLabel(status string) string {
	switch status {
	case FlatNeedsFee:
		return "Needs fee amount"
	case FlatAwaiting:
		return "Awaiting trust funds"
	case FlatReady:
		return "Ready to apply"
	case FlatCollected:
		return "Collected"
	}
	return status
}

func (s *Service) clientName(ctx context.Context, id domain.ID) string {
	if id.IsZero() {
		return ""
	}
	if c, err := s.d.Practice.Contact(ctx, id); err == nil {
		return c.FullName
	}
	return ""
}

// balances returns the matter's trust balances, or none when no trust service is wired.
func (s *Service) balances(ctx context.Context, matter domain.ID) ([]TrustBalance, domain.Cents, error) {
	if s.d.Trust == nil {
		return nil, 0, nil
	}
	rows, err := s.d.Trust.Balances(ctx, matter)
	if err != nil {
		return nil, 0, err
	}
	var total domain.Cents
	for _, r := range rows {
		if total, err = total.Add(r.BalanceCents); err != nil {
			return nil, 0, err
		}
	}
	return rows, total, nil
}

func flatBilled(m domain.Matter) bool {
	return m.BillingType == billingFlat || m.BillingType == billingMixed && positive(int64(m.FlatFeeCents))
}

func (s *Service) flatRow(ctx context.Context, m domain.Matter) (FlatRow, error) {
	row := FlatRow{MatterID: m.ID, MatterNumber: m.Number, MatterName: m.Name, FlatFeeCents: m.FlatFeeCents}
	bals, total, err := s.balances(ctx, m.ID)
	if err != nil {
		return FlatRow{}, err
	}
	row.TrustCents = total
	clients := s.clientParties(m.ID)
	if len(clients) > 0 {
		row.ClientID = clients[0]
	}
	var best domain.Cents
	for _, b := range bals {
		if b.BalanceCents > best && s.isClient(m.ID, b.ClientID) {
			best, row.ClientID, row.AccountID = b.BalanceCents, b.ClientID, b.AccountID
		}
	}
	row.ClientName = s.clientName(ctx, row.ClientID)
	inv, billed, err := s.flatFeeInvoice(ctx, m.ID)
	if err != nil {
		return FlatRow{}, err
	}
	if billed {
		row.InvoiceID = inv.ID
	}
	switch {
	case billed && inv.Status == StatusPaid:
		row.Status = FlatCollected
	case !positive(int64(m.FlatFeeCents)):
		row.Status = FlatNeedsFee
	case total < m.FlatFeeCents:
		row.Status = FlatAwaiting
	default:
		row.Status = FlatReady
	}
	return row, nil
}

// Worklist lists flat-fee matters with their status and hourly or mixed matters with unbilled work.
func (s *Service) Worklist(ctx context.Context, p domain.Principal) (Worklist, error) {
	if err := s.readBilling(ctx, p, ""); err != nil {
		return Worklist{}, err
	}
	out := Worklist{Flat: []FlatRow{}, Hourly: []HourlyRow{}}
	for _, m := range s.mattersByNumber() {
		if m.Status == matterClosed {
			continue
		}
		ok, err := s.visible(ctx, p, m.ID)
		if err != nil {
			return Worklist{}, err
		}
		if !ok {
			continue
		}
		if flatBilled(m) {
			row, err := s.flatRow(ctx, m)
			if err != nil {
				return Worklist{}, err
			}
			out.Flat = append(out.Flat, row)
		}
		if m.BillingType != billingFlat {
			lines, _, _, err := s.pending(ctx, m, "", "")
			if err != nil {
				return Worklist{}, err
			}
			var total domain.Cents
			for _, l := range lines {
				if l.MatterFee {
					continue
				}
				if total, err = total.Add(l.AmountCents); err != nil {
					return Worklist{}, err
				}
			}
			if positive(int64(total)) && (m.BillingType == billingHourly || m.BillingType == billingMixed) {
				var client string
				if c := s.clientParties(m.ID); len(c) > 0 {
					client = s.clientName(ctx, c[0])
				}
				out.Hourly = append(out.Hourly, HourlyRow{MatterID: m.ID, MatterNumber: m.Number, MatterName: m.Name, ClientName: client, UnbilledCents: total})
			}
		}
	}
	return out, nil
}

// mattersByNumber returns practice matters in matter-number order.
func (s *Service) mattersByNumber() []domain.Matter {
	rows := s.d.Practice.Matters()
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Number < rows[j].Number })
	return rows
}

// ApplyFlatFee moves the confirmed flat fee from the client's trust funds to operating at the
// earning milestone. Only an attorney or admin can post it, as with any trust withdrawal. The
// fee is billed on a flat-fee invoice that the transfer pays, so the row shows Collected.
func (s *Service) ApplyFlatFee(ctx context.Context, p domain.Principal, matter, client, account domain.ID, amount domain.Cents) (TrustPosted, Invoice, error) {
	if err := amountField("amount_cents", amount); err != nil {
		return TrustPosted{}, Invoice{}, err
	}
	if account.IsZero() {
		return TrustPosted{}, Invoice{}, domain.Invalid("trust_account_id", "is required")
	}
	if err := s.allow(ctx, p, domain.ResTrust, domain.ActionTrustWithdraw, matter); err != nil {
		return TrustPosted{}, Invoice{}, err
	}
	if s.d.Trust == nil {
		return TrustPosted{}, Invoice{}, ErrTrustUnavailable
	}
	var posted TrustPosted
	var out Invoice
	err := s.write(ctx, p, func(t *txn) error {
		m, err := s.matter(t.ctx, matter)
		if err != nil {
			return err
		}
		if !flatBilled(m) {
			return domain.Conflict("matter does not bill a flat fee")
		}
		row, err := s.flatRow(t.ctx, m)
		if err != nil {
			return err
		}
		if row.Status != FlatReady {
			return domain.Conflict("flat fee is not ready to apply: " + StatusLabel(row.Status))
		}
		if amount != m.FlatFeeCents {
			return domain.Invalid("amount_cents", "confirm the matter's flat fee "+money(m.FlatFeeCents))
		}
		if !s.isClient(m.ID, client) {
			return domain.Invalid("client_id", "must be a client on the matter")
		}
		inv, billed, err := s.flatFeeInvoice(t.ctx, m.ID)
		if err != nil {
			return err
		}
		if billed && !inv.Open() {
			return domain.Conflict("the flat fee is on draft invoice " + inv.Number + "; approve and send it, then apply trust")
		}
		if billed && inv.BalanceDueCents < amount {
			return domain.Conflict("flat-fee invoice " + inv.Number + " owes less than the fee; apply trust from the invoice")
		}
		if !billed {
			if inv, err = s.flatInvoice(t, m); err != nil {
				return err
			}
		}
		if _, posted, err = s.transfer(t, inv, client, account, amount, "Flat fee earned: "+m.Name); err != nil {
			return err
		}
		out, err = s.invoice(t.ctx, inv.ID)
		return err
	})
	return posted, out, err
}

// flatInvoice writes an approved, sent invoice for the matter's flat fee.
func (s *Service) flatInvoice(t *txn, m domain.Matter) (Invoice, error) {
	meta, err := s.meta(t.p)
	if err != nil {
		return Invoice{}, err
	}
	issued := s.today(t.ctx)
	number, err := s.nextNumber(t.ctx, issued)
	if err != nil {
		return Invoice{}, err
	}
	now := s.now()
	inv := Invoice{
		Lines: []Line{matterFeeLine(m)},
		DueOn: issued, ApprovedBy: actor(t.p), ApprovedAt: &now, MatterName: m.Name, MatterNum: m.Number,
	}
	inv.Invoice = domain.Invoice{
		Meta: meta, MatterID: m.ID, Number: number, IssuedOn: issued, Status: StatusSent, SentAt: now.UnixMicro(),
		ClientMessage: s.setting(t.ctx, "billing.default_client_message"),
	}
	if err := recompute(&inv); err != nil {
		return Invoice{}, err
	}
	if err := t.putInvoice(inv); err != nil {
		return Invoice{}, err
	}
	if err := t.created("invoice", inv.ID, inv, "flat fee applied from trust"); err != nil {
		return Invoice{}, err
	}
	return inv, t.event("invoice.drafted", "invoice", inv.ID, m.ID, "Flat-fee invoice "+number+" issued", false)
}

// Overdue lists open invoices past their due date, oldest first. Zero-fee matters are omitted.
func (s *Service) Overdue(ctx context.Context, p domain.Principal) ([]Invoice, error) {
	rows, err := s.Invoices(ctx, p, InvoiceFilter{})
	if err != nil {
		return nil, err
	}
	today := s.today(ctx)
	var out []Invoice
	for _, r := range rows {
		if r.Open() && r.DueOn < today && !s.zeroFee(r.MatterID) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DueOn < out[j].DueOn })
	return out, nil
}

// Collections is the panel on a sent invoice with a balance: funded clients for Apply trust,
// with the default client when only one holds funds.
type Collections struct {
	Show          bool
	Funded        []TrustBalance
	DefaultClient domain.ID
	DefaultAcct   domain.ID
	Balance       domain.Cents
	// Suggested prefills Apply trust: the balance due, or the default client's funds if less.
	Suggested domain.Cents
}

// CollectionsFor builds the panel. Zero-fee matters drop out of collections.
func (s *Service) CollectionsFor(ctx context.Context, p domain.Principal, inv Invoice) (Collections, error) {
	out := Collections{Balance: inv.BalanceDueCents, Suggested: inv.BalanceDueCents}
	if !inv.Open() || s.zeroFee(inv.MatterID) {
		return out, nil
	}
	if err := s.readInvoice(ctx, p, inv); err != nil {
		return Collections{}, err
	}
	out.Show = true
	bals, _, err := s.balances(ctx, inv.MatterID)
	if err != nil {
		return Collections{}, err
	}
	for _, b := range bals {
		if positive(int64(b.BalanceCents)) && s.isClient(inv.MatterID, b.ClientID) {
			out.Funded = append(out.Funded, b)
		}
	}
	if onlyOne(len(out.Funded)) {
		out.DefaultClient, out.DefaultAcct = out.Funded[0].ClientID, out.Funded[0].AccountID
		out.Suggested = min(out.Balance, out.Funded[0].BalanceCents)
	}
	return out, nil
}

// Summary is a matter's billing numbers for At a Glance and the close screen.
type Summary struct {
	TimeMinutes   int64
	TimeEntries   int
	UnbilledCents domain.Cents
	Invoices      int
	UnpaidCents   domain.Cents
}

// MatterSummary returns the matter's billing summary.
func (s *Service) MatterSummary(ctx context.Context, p domain.Principal, matter domain.ID) (Summary, error) {
	if err := s.readBilling(ctx, p, matter); err != nil {
		return Summary{}, err
	}
	m, err := s.matter(ctx, matter)
	if err != nil {
		return Summary{}, err
	}
	var out Summary
	times, err := listOf[TimeEntry](ctx, s.d.Store, kindTime)
	if err != nil {
		return Summary{}, err
	}
	for _, e := range times {
		if e.MatterID == matter {
			out.TimeEntries++
			out.TimeMinutes += e.Minutes()
		}
	}
	lines, _, _, err := s.pending(ctx, m, "", "")
	if err != nil {
		return Summary{}, err
	}
	for _, l := range lines {
		if out.UnbilledCents, err = out.UnbilledCents.Add(l.AmountCents); err != nil {
			return Summary{}, err
		}
	}
	invs, err := listOf[Invoice](ctx, s.d.Store, kindInvoice)
	if err != nil {
		return Summary{}, err
	}
	for _, inv := range invs {
		if inv.MatterID != matter || inv.Status == StatusVoid {
			continue
		}
		out.Invoices++
		if inv.Open() {
			if out.UnpaidCents, err = out.UnpaidCents.Add(inv.BalanceDueCents); err != nil {
				return Summary{}, err
			}
		}
	}
	return out, nil
}
