package billing

import (
	"context"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// grantCapture records refund_authorization credentials DeliverGrant receives.
type grantCapture struct {
	creds []string
}

func (g *grantCapture) Processor(ctx context.Context, module string) (map[string]bool, []string, bool, error) {
	return map[string]bool{"payments:write": true, "trust:write": true, "trust:withdraw": true},
		[]string{"operating", "trust"}, true, nil
}

func (g *grantCapture) DeliverGrant(ctx context.Context, module string, ev domain.Event, field string, grant any, credential string) error {
	g.creds = append(g.creds, credential)
	return nil
}

// TestRefundGrantBoundToRefundID: a grant for refund A must not complete or fail refund B
// (same processor or a different one). B stays pending.
func TestRefundGrantBoundToRefundID(t *testing.T) {
	f := newFx(t)
	cap := &grantCapture{}
	f.svc.SetProcessors(cap)

	m, client := f.matter("Grant Bind Estate", billingHourly, 0, 250_00)
	f.logTime(f.attorney, m.ID, "2026-06-01", 40, nil) // $1000
	inv := f.sent(m.ID)

	paySame, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{
		ContactID: client, MatterID: m.ID, AmountCents: 400_00, Destination: DestOperating,
		Method: "card", ReferenceNumber: "same-proc", InvoiceID: inv.ID, Source: "pay-proc",
	})
	if err != nil {
		t.Fatal(err)
	}
	payOther, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{
		ContactID: client, MatterID: m.ID, AmountCents: 300_00, Destination: DestOperating,
		Method: "card", ReferenceNumber: "other-proc", InvoiceID: inv.ID, Source: "pay-lite",
	})
	if err != nil {
		t.Fatal(err)
	}

	refA, err := f.svc.CreatePaymentRefund(f.ctx, f.attorney, paySame.ID, RefundInput{
		AmountCents: 50_00, Reason: "hold A", Status: RefundPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	refB, err := f.svc.CreatePaymentRefund(f.ctx, f.attorney, paySame.ID, RefundInput{
		AmountCents: 40_00, Reason: "hold B", Status: RefundPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	refC, err := f.svc.CreatePaymentRefund(f.ctx, f.attorney, payOther.ID, RefundInput{
		AmountCents: 30_00, Reason: "hold C", Status: RefundPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cap.creds) != 3 {
		t.Fatalf("expected 3 refund grants, got %d", len(cap.creds))
	}
	grantA, grantB := cap.creds[0], cap.creds[1]

	modA := domain.Principal{Source: domain.SourceModule, ModuleID: "pay-proc"}
	modB := domain.Principal{Source: domain.SourceModule, ModuleID: "pay-lite"}

	// Same processor: grant for A presented for B.
	wantCode(t, mustErr(f.svc.UpdatePaymentRefundStatus(f.ctx, modA, paySame.ID, refB.ID, RefundCompleted, grantA)), domain.CodeForbidden)
	stillB, err := f.svc.ListPaymentRefunds(f.ctx, f.attorney, paySame.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range stillB {
		if row.ID == refB.ID && row.Status != RefundPending {
			t.Fatalf("same-processor mismatch changed B: %+v", row)
		}
		if row.ID == refA.ID && row.Status != RefundPending {
			t.Fatalf("same-processor mismatch changed A: %+v", row)
		}
	}

	// Different processor: grant for A presented for C on pay-lite.
	wantCode(t, mustErr(f.svc.UpdatePaymentRefundStatus(f.ctx, modB, payOther.ID, refC.ID, RefundCompleted, grantA)), domain.CodeForbidden)
	wantCode(t, mustErr(f.svc.UpdatePaymentRefundStatus(f.ctx, modA, payOther.ID, refC.ID, RefundFailed, grantA)), domain.CodeForbidden)
	stillC, err := f.svc.ListPaymentRefunds(f.ctx, f.attorney, payOther.ID)
	if err != nil || len(stillC) != 1 || stillC[0].Status != RefundPending {
		t.Fatalf("different-processor mismatch changed C: %+v %v", stillC, err)
	}

	// Matching grant still completes B.
	done, err := f.svc.UpdatePaymentRefundStatus(f.ctx, modA, paySame.ID, refB.ID, RefundCompleted, grantB)
	if err != nil || done.Status != RefundCompleted {
		t.Fatalf("matching grant: %+v %v", done, err)
	}
}
