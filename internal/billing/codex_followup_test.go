package billing

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// A flat fee is Ready only when one client trust account covers it, and Apply refuses an
// account that holds less than the fee, so a split balance never overdraws one account.
func TestFlatFeeReadyNeedsOneAccountThatCovers(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Split funds", "flat", 1500_00, 0)
	other := f.id()
	f.deposit(m.ID, client, 1000_00)
	if _, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: 600_00,
		Destination: DestTrust, TrustAccountID: other, Method: "check", ReferenceNumber: "1002"}); err != nil {
		t.Fatal(err)
	}
	if row := f.flatStatus(m.ID); row.Status != FlatAwaiting || row.TrustCents != 1600_00 {
		t.Fatalf("$1,000 and $600 in two accounts should await funds for a $1,500 fee: %+v", row)
	}
	before := f.snapshot()
	wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1500_00)), domain.CodeConflict)
	if f.snapshot() != before {
		t.Fatal("a refused apply wrote rows")
	}

	f.deposit(m.ID, client, 500_00)
	row := f.flatStatus(m.ID)
	if row.Status != FlatReady || row.AccountID != f.account {
		t.Fatalf("one account now covers the fee: %+v", row)
	}
	before = f.snapshot()
	wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, other, 1500_00)), domain.CodeConflict)
	if f.snapshot() != before {
		t.Fatal("an apply from the short account wrote rows")
	}
	if _, _, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1500_00); err != nil {
		t.Fatal(err)
	}
	if b := f.trust.balance(f.ctx, m.ID, client); b != 600_00 {
		t.Fatalf("after the fee the client holds %s, want $600.00 left in the other account", b)
	}
}

// An email address whose effective-from date is after today never receives an invoice.
func TestInvoiceMailSkipsAddressNotYetEffective(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Future email", "hourly", 0, 0)
	c, err := f.ps.CreateContact(f.ctx, f.staff, practice.ContactInput{Kind: "person", GivenName: "Dana", FamilyName: "Client"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.ps.AddParty(f.ctx, f.staff, m.ID, practice.PartyInput{ContactID: c.ID, Role: "client"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []domain.ContactEmail{
		{Meta: domain.Meta{ID: f.id()}, ContactID: c.ID, Primary: true, Email: "later@example.test", EffectiveFrom: "2026-07-01"},
		{Meta: domain.Meta{ID: f.id()}, ContactID: c.ID, Email: "now@example.test", EffectiveFrom: "2026-06-15"},
	} {
		if err := f.pm.InsertEmail(f.ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	f.logTime(f.attorney, m.ID, "2026-06-01", 3, nil)
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, invs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SendInvoice(f.ctx, f.attorney, invs[0].ID, "email"); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.sent) != 1 {
		t.Fatalf("mails: %d", len(f.mail.sent))
	}
	to := map[string]bool{}
	for _, a := range f.mail.sent[0].To {
		to[a] = true
	}
	if to["later@example.test"] || !to["now@example.test"] || !to["cora@example.test"] {
		t.Fatalf("recipients: %v", f.mail.sent[0].To)
	}
}
