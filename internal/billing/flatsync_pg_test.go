package billing

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/trust"
)

// Checkpoint 7 (AC-026, PRD 8.15): a flat matter's fee leaves trust once and is billed once,
// whichever of billing's ways of settling the fee invoice and trust's own Apply flat fee runs
// first. All cases run against the merged trust package on Postgres.

func (r *realTrust) trustApply(matter, client domain.ID, cents int64) (trust.PostResult, error) {
	return r.tr.ApplyFlatFee(r.ctx, r.attorney, trust.ApplyFlatFeeInput{MatterID: matter, ClientID: client, TrustAccountID: r.account, AmountCents: &cents}, "")
}

func (r *realTrust) inv(id domain.ID) Invoice {
	r.t.Helper()
	inv, err := r.svc.Invoice(r.ctx, r.attorney, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return inv
}

func (r *realTrust) balance(matter, client domain.ID) int64 {
	r.t.Helper()
	b, err := r.tr.MatterBalance(r.ctx, r.attorney, matter, client)
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

func (r *realTrust) agingHas(inv domain.ID) bool {
	r.t.Helper()
	rep, err := r.svc.ARAgingReport(r.ctx, r.accountant, ReportFilter{})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, row := range rep.Rows {
		if row["invoice_id"] == inv || row["invoice_id"] == string(inv) {
			return true
		}
	}
	return false
}

func (r *realTrust) unpaidWarning(matter domain.ID) bool {
	r.t.Helper()
	_, ws, err := r.ps.PreviewClose(r.ctx, r.attorney, matter)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, w := range ws {
		if w.Code == practice.WarnUnpaidInvoice {
			return true
		}
	}
	return false
}

// Order 1a: Apply trust from the fee invoice's Collections panel is trust's flat-fee
// application, so trust's page refuses a second apply and both pages show Collected.
func TestRealTrustApplyTrustOnFeeInvoiceBlocksTrustPageApply(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Apply-trust will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	inv := r.sent(m.ID)
	if inv.BalanceDueCents != 500_00 {
		t.Fatalf("fee invoice: %+v", inv)
	}
	pay, _, err := r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 500_00)
	if err != nil {
		t.Fatal(err)
	}
	if !pay.MatterFee || pay.TrustEntryID.IsZero() {
		t.Fatalf("the fee was not applied as trust's flat-fee application: %+v", pay)
	}
	if st := r.trustFee(m.ID); st.Status != trust.FeeCollected || st.CollectedCents != 500_00 {
		t.Fatalf("trust page after Apply trust: %+v", st)
	}
	if got := r.flatStatus(m.ID); got.Status != FlatCollected {
		t.Fatalf("billing worklist: %+v", got)
	}
	rows := r.ledgerRows()
	if _, err := r.trustApply(m.ID, c, 500_00); err == nil {
		t.Fatal("trust page took the fee a second time after Apply trust")
	}
	if r.ledgerRows() != rows || r.balance(m.ID, c) != 500_00 {
		t.Fatalf("fee left trust twice: rows %d->%d balance %d", rows, r.ledgerRows(), r.balance(m.ID, c))
	}
	if r.inv(inv.ID).Status != StatusPaid {
		t.Fatal("fee invoice not paid")
	}
}

// Order 1b: a part paid by Apply trust is part of trust's record; trust's page applies only
// the rest, and that rest is credited to the invoice, so the invoice ends paid and the fee
// left trust exactly once in total.
func TestRealTrustPartialApplyTrustThenTrustPageRest(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Split will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	inv := r.sent(m.ID)
	if _, _, err := r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 200_00); err != nil {
		t.Fatal(err)
	}
	if st := r.trustFee(m.ID); st.CollectedCents != 200_00 || st.Status == trust.FeeCollected {
		t.Fatalf("trust after part: %+v", st)
	}
	if _, err := r.trustApply(m.ID, c, 300_00); err != nil {
		t.Fatal(err)
	}
	got := r.inv(inv.ID)
	if got.Status != StatusPaid || got.BalanceDueCents != 0 {
		t.Fatalf("invoice after trust-page rest: %s %s", got.Status, money(got.BalanceDueCents))
	}
	if r.balance(m.ID, c) != 500_00 {
		t.Fatalf("trust: %d", r.balance(m.ID, c))
	}
	wantCode(t, mustErr2(r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 100_00)), domain.CodeConflict)
	if _, err := r.trustApply(m.ID, c, 100_00); err == nil {
		t.Fatal("trust applied past the fee")
	}
	if r.balance(m.ID, c) != 500_00 {
		t.Fatalf("trust after refused applies: %d", r.balance(m.ID, c))
	}
}

// Order 1c: an outside payment cannot settle a flat fee that trust's page would then take
// again; it may pay the invoice's other lines.
func TestRealTrustOutsidePaymentCannotSettleFlatFee(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Outside-paid will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	r.expense(m.ID, 40_00, 0)
	inv := r.sent(m.ID)
	if inv.BalanceDueCents != 540_00 {
		t.Fatalf("invoice: %s", money(inv.BalanceDueCents))
	}
	outside := func(cents domain.Cents) (Payment, error) {
		return r.svc.CreatePayment(r.ctx, r.staff, PaymentInput{ContactID: c, MatterID: m.ID, AmountCents: cents,
			Destination: DestOperating, Method: "check", ReferenceNumber: "2002", InvoiceID: inv.ID})
	}
	before := r.snapshot()
	_, err := outside(540_00)
	wantCode(t, err, domain.CodeValidation)
	_, err = outside(41_00)
	wantCode(t, err, domain.CodeValidation)
	if r.snapshot() != before {
		t.Fatal("a refused outside payment wrote rows")
	}
	// A pending outside payment cannot complete over the fee either.
	pend, err := r.svc.CreatePayment(r.ctx, r.staff, PaymentInput{ContactID: c, MatterID: m.ID, AmountCents: 500_00,
		Destination: DestOperating, Method: "ach", ReferenceNumber: "ach-1", InvoiceID: inv.ID, Status: PayPending})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, mustErr(r.svc.UpdatePaymentStatus(r.ctx, r.staff, pend.ID, PayCompleted)), domain.CodeValidation)
	if _, err := outside(40_00); err != nil {
		t.Fatalf("the expense may be paid outside trust: %v", err)
	}
	if got := r.flatStatus(m.ID); got.Status != FlatReady {
		t.Fatalf("worklist after the expense: %+v", got)
	}
	// The fee comes out of trust once, from trust's page, and the invoice is then paid.
	if _, err := r.trustApply(m.ID, c, 500_00); err != nil {
		t.Fatal(err)
	}
	if got := r.inv(inv.ID); got.Status != StatusPaid {
		t.Fatalf("invoice after trust apply: %s %s", got.Status, money(got.BalanceDueCents))
	}
	if r.balance(m.ID, c) != 500_00 {
		t.Fatalf("trust: %d", r.balance(m.ID, c))
	}
}

// Order 2: the fee invoice was sent, then the fee was applied on the trust page. The
// invoice is credited, leaves Collections, AR aging and the close warning, and cannot be paid
// from trust again. A trust-page reversal reopens it.
func TestRealTrustPageApplyCreditsSentFeeInvoice(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Sent-first will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	inv := r.sent(m.ID)
	if !r.agingHas(inv.ID) || !r.unpaidWarning(m.ID) {
		t.Fatal("the sent fee invoice should be a receivable before the fee is applied")
	}
	res, err := r.trustApply(m.ID, c, 500_00)
	if err != nil {
		t.Fatal(err)
	}
	got := r.inv(inv.ID)
	if got.Status != StatusPaid || got.BalanceDueCents != 0 {
		t.Fatalf("fee invoice after trust-page apply: %s %s", got.Status, money(got.BalanceDueCents))
	}
	col, err := r.svc.CollectionsFor(r.ctx, r.attorney, got)
	if err != nil || col.Show {
		t.Fatalf("collections still offered: %+v %v", col, err)
	}
	if r.agingHas(inv.ID) {
		t.Fatal("fee invoice still in AR aging")
	}
	if r.unpaidWarning(m.ID) {
		t.Fatal("close warning still reports the fee invoice as unpaid")
	}
	rows := r.ledgerRows()
	wantCode(t, mustErr2(r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 500_00)), domain.CodeConflict)
	if r.ledgerRows() != rows || r.balance(m.ID, c) != 500_00 {
		t.Fatal("billing took the fee from trust a second time")
	}
	pays, err := r.svc.Payments(r.ctx, r.accountant, PaymentFilter{InvoiceID: inv.ID})
	if err != nil || len(pays) != 1 || !pays[0].TrustSynced || pays[0].AmountCents != 500_00 {
		t.Fatalf("credit: %+v %v", pays, err)
	}
	// The credit is trust's; it is reversed only on the trust page.
	wantCode(t, mustErr(r.svc.ReversePayment(r.ctx, r.attorney, pays[0].ID, "mistake")), domain.CodeConflict)
	if _, err := r.tr.Reverse(r.ctx, r.attorney, res.Entry.ID, trust.ReverseInput{Reason: "applied in error"}, ""); err != nil {
		t.Fatal(err)
	}
	got = r.inv(inv.ID)
	if got.Status == StatusPaid || got.BalanceDueCents != 500_00 {
		t.Fatalf("fee invoice after trust-page reversal: %s %s", got.Status, money(got.BalanceDueCents))
	}
	if p, _ := r.svc.Payment(r.ctx, r.accountant, pays[0].ID); p.Status != PayReversed {
		t.Fatalf("credit after reversal: %+v", p)
	}
	if !r.unpaidWarning(m.ID) {
		t.Fatal("reopened fee invoice missing from the close warning")
	}
	// Now billing applies it once.
	if _, _, err := r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 500_00); err != nil {
		t.Fatal(err)
	}
	if r.balance(m.ID, c) != 500_00 || r.trustFee(m.ID).CollectedCents != 500_00 || r.inv(inv.ID).Status != StatusPaid {
		t.Fatalf("after billing apply: trust %d %+v", r.balance(m.ID, c), r.trustFee(m.ID))
	}
}

// A draft billed before the trust-page apply is credited when it is sent, so the client is
// not asked for the fee.
func TestRealTrustPageApplyBeforeSendCreditsOnSend(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Draft-first will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	invs, err := r.svc.DraftInvoices(r.ctx, r.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.trustApply(m.ID, c, 500_00); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.ApproveInvoice(r.ctx, r.attorney, invs[0].ID); err != nil {
		t.Fatal(err)
	}
	sent, err := r.svc.SendInvoice(r.ctx, r.attorney, invs[0].ID, "file")
	if err != nil {
		t.Fatal(err)
	}
	if sent.Status != StatusPaid || sent.BalanceDueCents != 0 {
		t.Fatalf("sent invoice: %s %s", sent.Status, money(sent.BalanceDueCents))
	}
	wantCode(t, mustErr2(r.svc.ApplyTrust(r.ctx, r.attorney, sent.ID, c, r.account, 500_00)), domain.CodeConflict)
	if r.balance(m.ID, c) != 500_00 {
		t.Fatalf("trust: %d", r.balance(m.ID, c))
	}
}

// Without the commit-event hook (an event lost to a crash), the next billing write on the
// invoice and Resync still reconcile with trust's record before anything else moves money.
func TestRealTrustLostEventReconciledBeforeApplyTrustAndAtResync(t *testing.T) {
	r := realTrustFx(t)
	r.hook.Bind(nil)
	m, c := r.matter("Lost-event will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	inv := r.sent(m.ID)
	if _, err := r.trustApply(m.ID, c, 500_00); err != nil {
		t.Fatal(err)
	}
	if r.inv(inv.ID).Status == StatusPaid {
		t.Fatal("test setup: the hook is unbound, nothing should have synced yet")
	}
	rows := r.ledgerRows()
	wantCode(t, mustErr2(r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 500_00)), domain.CodeConflict)
	if r.ledgerRows() != rows {
		t.Fatal("Apply trust took the fee again before reconciling")
	}

	// With an expense on the invoice too, Apply trust first credits the trust-page fee and
	// then pays only the expense: it does not try to take the fee again.
	m3, c3 := r.matter("Lost-event will with costs", "flat", 500_00, 0)
	r.deposit(m3.ID, c3, 1000_00)
	r.expense(m3.ID, 40_00, 0)
	inv3 := r.sent(m3.ID)
	if _, err := r.trustApply(m3.ID, c3, 500_00); err != nil {
		t.Fatal(err)
	}
	pay, _, err := r.svc.ApplyTrust(r.ctx, r.attorney, inv3.ID, c3, r.account, 40_00)
	if err != nil {
		t.Fatalf("Apply trust on the expense after a trust-page fee apply: %v", err)
	}
	if pay.MatterFee || pay.AmountCents != 40_00 {
		t.Fatalf("expense payment: %+v", pay)
	}
	if got := r.inv(inv3.ID); got.Status != StatusPaid || r.balance(m3.ID, c3) != 460_00 {
		t.Fatalf("after expense: %s trust %d", got.Status, r.balance(m3.ID, c3))
	}

	// The same for an outside payment of the expense: the fee is credited first.
	m4, c4 := r.matter("Lost-event will paid by check", "flat", 500_00, 0)
	r.deposit(m4.ID, c4, 1000_00)
	r.expense(m4.ID, 40_00, 0)
	inv4 := r.sent(m4.ID)
	if _, err := r.trustApply(m4.ID, c4, 500_00); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.CreatePayment(r.ctx, r.staff, PaymentInput{ContactID: c4, MatterID: m4.ID, AmountCents: 40_00,
		Destination: DestOperating, Method: "check", ReferenceNumber: "3003", InvoiceID: inv4.ID}); err != nil {
		t.Fatal(err)
	}
	if got := r.inv(inv4.ID); got.Status != StatusPaid {
		t.Fatalf("outside expense payment left the trust-page fee unreconciled: %s", money(got.BalanceDueCents))
	}

	m2, c2 := r.matter("Resync will", "flat", 300_00, 0)
	r.deposit(m2.ID, c2, 300_00)
	inv2 := r.sent(m2.ID)
	if _, err := r.trustApply(m2.ID, c2, 300_00); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Resync(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.inv(inv2.ID); got.Status != StatusPaid {
		t.Fatalf("Resync left the fee invoice open: %s", money(got.BalanceDueCents))
	}
	if r.unpaidWarning(m2.ID) {
		t.Fatal("close warning stale after Resync")
	}
	// A second Resync writes nothing.
	before := r.snapshot()
	if err := r.svc.Resync(r.ctx); err != nil {
		t.Fatal(err)
	}
	if r.snapshot() != before {
		t.Fatal("an in-sync Resync wrote rows")
	}
}

// Reversing billing's own fee application on the trust page reopens billing's invoice and
// marks billing's payment reversed with trust's reversal entry.
func TestRealTrustPageReversalOfBillingApplyReopensInvoice(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Reversed will", "flat", 400_00, 0)
	r.deposit(m.ID, c, 900_00)
	posted, inv, err := r.svc.ApplyFlatFee(r.ctx, r.attorney, m.ID, c, r.account, 400_00)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := r.tr.Reverse(r.ctx, r.attorney, posted.EntryID, trust.ReverseInput{Reason: "wrong matter"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := r.inv(inv.ID)
	if got.Status == StatusPaid || got.BalanceDueCents != 400_00 {
		t.Fatalf("invoice after trust-page reversal: %s %s", got.Status, money(got.BalanceDueCents))
	}
	pays, err := r.svc.Payments(r.ctx, r.accountant, PaymentFilter{InvoiceID: inv.ID})
	if err != nil || len(pays) != 1 || pays[0].Status != PayReversed || pays[0].ReversalEntry != rev.Entry.ID {
		t.Fatalf("billing payment: %+v %v", pays, err)
	}
	if got := r.flatStatus(m.ID); got.Status != FlatReady {
		t.Fatalf("worklist: %+v", got)
	}
	if r.balance(m.ID, c) != 900_00 {
		t.Fatalf("trust: %d", r.balance(m.ID, c))
	}
}

// A flat-fee invoice closed by a write-off is not a collected fee: the worklist agrees with
// trust's page, which still shows the fee in trust.
func TestRealTrustWrittenOffFeeIsNotCollected(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Written-off will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	inv := r.sent(m.ID)
	if _, err := r.svc.WriteOff(r.ctx, r.attorney, inv.ID, 500_00, "courtesy"); err != nil {
		t.Fatal(err)
	}
	if r.inv(inv.ID).Status != StatusPaid {
		t.Fatal("test setup: a full write-off closes the invoice")
	}
	got, st := r.flatStatus(m.ID), r.trustFee(m.ID)
	if got.Status == FlatCollected || st.Status == trust.FeeCollected {
		t.Fatalf("pages disagree or claim a written-off fee was collected: billing %s trust %s", got.Status, st.Status)
	}
}
