package billing

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPaymentRefunds covers CORE-24 create/list/status on the in-memory store: operating
// refunds lower invoice paid amount; over-remaining is refund_exceeds_payment; same-status
// replays; staff cannot create.
func TestPaymentRefunds(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Refund Estate", billingHourly, 0, 250_00)
	f.logTime(f.attorney, m.ID, "2026-06-01", 20, nil) // 20*6min * $250 = $500
	inv := f.sent(m.ID)
	pay, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{
		ContactID: client, MatterID: m.ID, AmountCents: inv.TotalCents, Destination: DestOperating,
		Method: "check", ReferenceNumber: "r-1", InvoiceID: inv.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, mustErr(f.svc.CreatePaymentRefund(f.ctx, f.staff, pay.ID, RefundInput{
		AmountCents: 50_00, Reason: "partial",
	})), domain.CodeForbidden)

	ref, err := f.svc.CreatePaymentRefund(f.ctx, f.attorney, pay.ID, RefundInput{
		AmountCents: 50_00, Reason: "client dispute", Status: RefundCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != RefundCompleted || ref.AmountCents != 50_00 {
		t.Fatalf("refund: %+v", ref)
	}
	pay, err = f.svc.Payment(f.ctx, f.attorney, pay.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pay.RefundedCents != 50_00 || pay.RefundState != RefundPartial || pay.Status != PayCompleted {
		t.Fatalf("payment after refund: refunded=%d state=%s status=%s", pay.RefundedCents, pay.RefundState, pay.Status)
	}
	after, err := f.svc.Invoice(f.ctx, f.attorney, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.AmountPaidCents != inv.TotalCents-50_00 || after.Status != StatusSent {
		t.Fatalf("invoice after refund: paid=%d status=%s want paid=%d sent", after.AmountPaidCents, after.Status, inv.TotalCents-50_00)
	}

	wantCode(t, mustErr(f.svc.CreatePaymentRefund(f.ctx, f.attorney, pay.ID, RefundInput{
		AmountCents: inv.TotalCents, Reason: "too much",
	})), domain.CodeRefundExceeds)

	pending, err := f.svc.CreatePaymentRefund(f.ctx, f.attorney, pay.ID, RefundInput{
		AmountCents: 25_00, Reason: "hold", Status: RefundPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.svc.UpdatePaymentRefundStatus(f.ctx, f.attorney, pay.ID, pending.ID, RefundPending, "")
	if err != nil || again.Status != RefundPending {
		t.Fatalf("same-status replay: %+v %v", again, err)
	}
	done, err := f.svc.UpdatePaymentRefundStatus(f.ctx, f.attorney, pay.ID, pending.ID, RefundCompleted, "")
	if err != nil || done.Status != RefundCompleted {
		t.Fatalf("complete pending: %+v %v", done, err)
	}
	pay, _ = f.svc.Payment(f.ctx, f.attorney, pay.ID)
	if pay.RefundedCents != 75_00 || pay.RefundState != RefundPartial {
		t.Fatalf("after second refund: %+v", pay)
	}
	rows, err := f.svc.ListPaymentRefunds(f.ctx, f.attorney, pay.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list: %d %v", len(rows), err)
	}
}

// TestTrustPaymentRefund posts a trust ledger refund row (CORE-24).
func TestTrustPaymentRefund(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Trust Refund", billingHourly, 0, 0)
	pay := f.deposit(m.ID, client, 100_00)
	ref, err := f.svc.CreatePaymentRefund(f.ctx, f.attorney, pay.ID, RefundInput{
		AmountCents: 40_00, Reason: "overpayment", Status: RefundCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.TrustEntryID.IsZero() {
		t.Fatal("trust refund needs a ledger entry id")
	}
	pay, _ = f.svc.Payment(f.ctx, f.attorney, pay.ID)
	if pay.RefundedCents != 40_00 || pay.RefundState != RefundPartial {
		t.Fatalf("trust payment: %+v", pay)
	}
}
