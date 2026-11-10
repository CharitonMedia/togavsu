package billing

import (
	"context"
	"log"
	"sort"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// A flat matter's fee is settled in one place: the trust service's flat-fee record
// (trust_flat_fee_applications), which trust's own Apply flat fee and billing's trust
// settlements both write. Billing's fee invoice follows that record:
//   - billing settles the fee portion of a fee invoice only through Trust.ApplyFlatFee
//     (Apply flat fee and the fee portion of Apply trust), never through a generic trust
//     transfer or an outside payment, so trust's page refuses a second apply;
//   - an application made on the trust page is credited to the open fee invoice as a
//     trust-synced payment, and a trust-page reversal takes that credit (or billing's own
//     fee payment) back, so the invoice does not stay a receivable for a fee trust already
//     took, and cannot be paid from trust again.
// syncFlatFee runs in every billing write that settles a fee invoice, after a fee invoice
// is sent, on trust's commit events (TrustEvents) and at Resync.

// feePayments returns the completed fee payments on matter: billing's own, which carry a
// trust entry, and trust-synced credits.
func (s *Service) feePayments(ctx context.Context, matter domain.ID) (own, synced []Payment, err error) {
	pays, err := listOf[Payment](ctx, s.d.Store, kindPayment)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range pays {
		if p.MatterID != matter || !p.MatterFee || p.Status != PayCompleted {
			continue
		}
		if p.TrustSynced {
			synced = append(synced, p)
		} else {
			own = append(own, p)
		}
	}
	return own, synced, nil
}

func sumPaid(rows []Payment) (domain.Cents, error) {
	var sum domain.Cents
	var err error
	for _, p := range rows {
		if sum, err = sum.Add(p.AmountCents); err != nil {
			return 0, err
		}
	}
	return sum, nil
}

// feeOwed is the part of inv's matter-fee line no completed fee payment covers yet.
func (s *Service) feeOwed(ctx context.Context, inv Invoice) (domain.Cents, error) {
	if !inv.HasFlatFee() {
		return 0, nil
	}
	own, synced, err := s.feePayments(ctx, inv.MatterID)
	if err != nil {
		return 0, err
	}
	paid := domain.Cents(0)
	for _, p := range append(own, synced...) {
		if p.InvoiceID == inv.ID {
			if paid, err = paid.Add(p.AmountCents); err != nil {
				return 0, err
			}
		}
	}
	owed := inv.matterFeeCents() - paid
	if owed < 0 {
		owed = 0
	}
	return owed, nil
}

// trustSettled reports whether inv bills the fee of a flat matter whose fee trust's record
// settles.
func (s *Service) trustSettled(ctx context.Context, inv Invoice) (bool, error) {
	if !inv.HasFlatFee() || s.d.Trust == nil {
		return false, nil
	}
	m, err := s.matter(ctx, inv.MatterID)
	if err != nil {
		return false, err
	}
	return m.BillingType == billingFlat, nil
}

// syncFlatFee brings billing's fee payments on a flat matter in line with trust's record.
// It writes nothing when they already agree.
func (s *Service) syncFlatFee(t *txn, m domain.Matter) error {
	if m.BillingType != billingFlat || s.d.Trust == nil {
		return nil
	}
	applied, err := s.d.Trust.FlatFeeCollected(t.ctx, m.ID)
	if err != nil {
		return err
	}
	own, synced, err := s.feePayments(t.ctx, m.ID)
	if err != nil {
		return err
	}
	ownCents, err := sumPaid(own)
	if err != nil {
		return err
	}
	if applied < ownCents {
		// An application billing made was reversed on the trust page.
		live := own[:0:0]
		for _, p := range own {
			rev, err := s.d.Trust.Reversal(t.ctx, m.ID, p.TrustEntryID)
			if err != nil {
				return err
			}
			if rev.IsZero() {
				live = append(live, p)
				continue
			}
			if err := s.unsettle(t, p, rev, "fee application reversed on the trust page"); err != nil {
				return err
			}
		}
		if ownCents, err = sumPaid(live); err != nil {
			return err
		}
	}
	target := applied - ownCents
	if target < 0 {
		target = 0
	}
	have, err := sumPaid(synced)
	if err != nil {
		return err
	}
	if have == target {
		return nil
	}
	if have > target {
		// Newest first; a credit larger than the excess is taken back whole and the rest
		// credited again below.
		sort.SliceStable(synced, func(i, j int) bool { return synced[i].ID > synced[j].ID })
		for _, p := range synced {
			if have <= target {
				break
			}
			if err := s.unsettle(t, p, "", "fee application reversed on the trust page"); err != nil {
				return err
			}
			have -= p.AmountCents
		}
	}
	if have >= target {
		return nil
	}
	return s.creditFee(t, m, target-have)
}

// unsettle marks a fee payment reversed and returns its amount to the invoice balance.
// The trust entry is already reversed (or the credit never had one of its own).
func (s *Service) unsettle(t *txn, row Payment, reversal domain.ID, reason string) error {
	before := row
	if !row.InvoiceID.IsZero() {
		if err := s.applyToInvoice(t, row.InvoiceID, -row.AmountCents); err != nil {
			return err
		}
	}
	now := s.now()
	row.Status, row.ReversedAt, row.ReverseReason, row.ReversalEntry, row.UpdatedAt = PayReversed, &now, reason, reversal, now
	if err := t.put(kindPayment, row.ID, row); err != nil {
		return err
	}
	if err := t.diff("payment", row.ID, before, row, reason); err != nil {
		return err
	}
	return t.event("payment.reversed", "payment", row.ID, row.MatterID, "Payment "+money(row.AmountCents)+" reversed: "+reason, false)
}

// creditFee credits up to amount of a trust-page fee application to the open fee invoice.
// With no open fee invoice (none yet, a draft, or one already paid) nothing is credited;
// the next sync after the invoice is sent credits it.
func (s *Service) creditFee(t *txn, m domain.Matter, amount domain.Cents) error {
	inv, billed, err := s.flatFeeInvoice(t.ctx, m.ID)
	if err != nil || !billed || !inv.Open() {
		return err
	}
	owed, err := s.feeOwed(t.ctx, inv)
	if err != nil {
		return err
	}
	credit := min(amount, owed, inv.BalanceDueCents)
	if !positive(int64(credit)) {
		return nil
	}
	meta, err := s.meta(t.p)
	if err != nil {
		return err
	}
	client := domain.ID("")
	if c := s.clientParties(m.ID); len(c) > 0 {
		client = c[0]
	}
	desc := "Flat fee applied on the trust page, credited to invoice " + inv.Number
	row := Payment{ReceivedOn: s.today(t.ctx), MatterFee: true, TrustSynced: true}
	row.Payment = domain.Payment{
		Meta: meta, ContactID: client, MatterID: m.ID, AmountCents: credit, Destination: DestOperating,
		Method: MethodInternal, ReferenceNumber: "trust flat fee", Status: PayCompleted, InvoiceID: inv.ID,
		Source: sourceOf(t.p, ""),
	}
	if err := s.applyToInvoice(t, inv.ID, credit); err != nil {
		return err
	}
	if err := t.put(kindPayment, row.ID, row); err != nil {
		return err
	}
	if err := t.created("payment", row.ID, row, desc); err != nil {
		return err
	}
	return t.event("payment.completed", "payment", row.ID, m.ID, desc, false)
}

// syncInvoiceFee syncs the matter of inv when it bills a flat matter's fee, and returns
// the invoice as it stands after.
func (s *Service) syncInvoiceFee(t *txn, inv Invoice) (Invoice, error) {
	ok, err := s.trustSettled(t.ctx, inv)
	if err != nil || !ok {
		return inv, err
	}
	m, err := s.matter(t.ctx, inv.MatterID)
	if err != nil {
		return Invoice{}, err
	}
	if err := s.syncFlatFee(t, m); err != nil {
		return Invoice{}, err
	}
	return s.invoice(t.ctx, inv.ID)
}

// SyncFlatFee brings a flat matter's fee invoice in line with trust's flat-fee record. Other
// matters are left alone. It runs as the system: it records what trust already did.
func (s *Service) SyncFlatFee(ctx context.Context, matter domain.ID) error {
	if s.d.Trust == nil {
		return nil
	}
	m, err := s.matter(ctx, matter)
	if err != nil || m.BillingType != billingFlat {
		return err
	}
	return s.write(ctx, domain.SystemPrincipal(), func(t *txn) error {
		m, err := s.matter(t.ctx, matter)
		if err != nil {
			return err
		}
		return s.syncFlatFee(t, m)
	})
}

// syncAfter runs SyncFlatFee after a write that may have opened a fee invoice. The write
// already committed; a failed sync is logged and retried by the next sync.
func (s *Service) syncAfter(ctx context.Context, matter domain.ID) {
	if err := s.SyncFlatFee(context.WithoutCancel(ctx), matter); err != nil {
		log.Printf("billing: flat fee sync for matter %s: %v", matter, err)
	}
}
