package billing

import (
	"testing"
	"time"
)

// AR aging dates a void and a payment reversal by the firm's calendar day (firm.time_zone),
// the same day its as-of date and every issue date use. 03:00 UTC on January 16 is
// 21:00 on January 15 in Chicago, so a void or reversal then is on the firm's January 15.
func TestARAgingDatesVoidsAndReversalsInTheFirmZone(t *testing.T) {
	f := newFx(t)
	defer overrideSettings(f.svc, settingOverride{"firm.time_zone": "America/Chicago"})()
	rows := func(asOf string) []map[string]any {
		t.Helper()
		rep, err := f.svc.ARAgingReport(f.ctx, f.accountant, ReportFilter{AsOf: asOf})
		if err != nil {
			t.Fatal(err)
		}
		return rep.Rows
	}
	m, _ := f.matter("Zone case", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-01-05", 40, nil) // 1000.00
	f.now = time.Date(2026, 1, 10, 18, 0, 0, 0, time.UTC)
	voided := f.sent(m.ID)
	f.now = time.Date(2026, 1, 16, 3, 0, 0, 0, time.UTC)
	if _, err := f.svc.VoidInvoice(f.ctx, f.attorney, voided.ID, "billed the wrong client"); err != nil {
		t.Fatal(err)
	}
	if got := rows("2026-01-15"); len(got) != 0 {
		t.Fatalf("voided on the firm's January 15, still on that day's aging: %+v", got)
	}
	if got := rows("2026-01-14"); len(got) != 1 || got[0]["invoice_id"] != voided.ID {
		t.Fatalf("owed the day before the void: %+v", got)
	}

	other, client2 := f.matter("Zone case two", "hourly", 0, 0)
	f.logTime(f.attorney, other.ID, "2026-02-02", 40, nil)
	f.now = time.Date(2026, 2, 3, 18, 0, 0, 0, time.UTC)
	inv := f.sent(other.ID)
	pay, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client2, MatterID: other.ID, AmountCents: inv.TotalCents,
		Destination: DestOperating, Method: "check", InvoiceID: inv.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(2026, 2, 10, 3, 0, 0, 0, time.UTC)
	if _, err := f.svc.ReversePayment(f.ctx, f.attorney, pay.ID, "bounced"); err != nil {
		t.Fatal(err)
	}
	if got := rows("2026-02-09"); len(got) != 1 || got[0]["invoice_id"] != inv.ID || got[0]["balance_cents"] != int64(inv.TotalCents) {
		t.Fatalf("reversed on the firm's February 9, so the whole invoice is owed that day: %+v", got)
	}
	if got := rows("2026-02-08"); len(got) != 0 {
		t.Fatalf("paid in full the day before the reversal: %+v", got)
	}
}
