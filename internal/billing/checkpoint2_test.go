package billing

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Regression tests for the second Codex checkpoint on PR #27 (review of 17e5bbd..481550f).

func (f *fx) flatStatus(matter domain.ID) FlatRow {
	f.t.Helper()
	wl, err := f.svc.Worklist(f.ctx, f.attorney)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, r := range wl.Flat {
		if r.MatterID == matter {
			return r
		}
	}
	f.t.Fatalf("matter %s not on the flat-fee worklist", matter)
	return FlatRow{}
}

func (f *fx) transfers(matter domain.ID) int {
	n := 0
	for _, e := range f.trust.entries(f.ctx) {
		if e.Matter == matter && e.Type == "transfer_to_operating" {
			n++
		}
	}
	return n
}

// P1 (AC-026): the matter fee shares a sent invoice with expenses (flat matter) or with time and
// expenses (mixed matter). Apply flat fee pays the fee on that invoice; the row then shows
// Collected although the other lines are unpaid, and a second apply is refused with nothing
// written, so the fee leaves trust exactly once.
func TestFlatFeeOnCombinedInvoiceAppliesOnce(t *testing.T) {
	t.Run("flat fee with larger expenses", func(t *testing.T) {
		f := newFx(t)
		m, client := f.matter("Combined flat", "flat", 1000_00, 0)
		f.expense(m.ID, 1500_00, 0)
		inv := f.sent(m.ID)
		if !inv.HasFlatFee() || inv.TotalCents != 2500_00 {
			t.Fatalf("combined invoice: %+v", inv.Lines)
		}
		f.deposit(m.ID, client, 2000_00)
		if s := f.flatStatus(m.ID).Status; s != FlatReady {
			t.Fatalf("before apply: %s", s)
		}
		_, applied, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1000_00)
		if err != nil {
			t.Fatal(err)
		}
		if applied.ID != inv.ID || applied.BalanceDueCents != 1500_00 || applied.Status != StatusSent {
			t.Fatalf("applied to the combined invoice: %+v", applied.Invoice)
		}
		row := f.flatStatus(m.ID)
		if row.Status != FlatCollected || row.InvoiceID != inv.ID || row.TrustCents != 1000_00 {
			t.Fatalf("the fee is collected though the expenses are unpaid: %+v", row)
		}
		before := f.snapshot()
		wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1000_00)), domain.CodeConflict)
		if f.snapshot() != before || f.transfers(m.ID) != 1 {
			t.Fatalf("a second apply moved the fee again: transfers %d", f.transfers(m.ID))
		}
		// Reversing the fee transfer makes the fee uncollected again, and it can be reapplied once.
		pays, _ := f.svc.Payments(f.ctx, f.attorney, PaymentFilter{InvoiceID: inv.ID})
		if len(pays) != 1 || !pays[0].MatterFee {
			t.Fatalf("fee payment: %+v", pays)
		}
		if _, err := f.svc.ReversePayment(f.ctx, f.attorney, pays[0].ID, "posted to the wrong client"); err != nil {
			t.Fatal(err)
		}
		if s := f.flatStatus(m.ID).Status; s != FlatReady {
			t.Fatalf("after reversal: %s", s)
		}
		if _, _, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1000_00); err != nil {
			t.Fatal(err)
		}
		if s := f.flatStatus(m.ID).Status; s != FlatCollected {
			t.Fatalf("reapplied: %s", s)
		}
		wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1000_00)), domain.CodeConflict)
	})
	t.Run("flat fee with smaller expenses", func(t *testing.T) {
		f := newFx(t)
		m, client := f.matter("Small costs", "flat", 1000_00, 0)
		f.expense(m.ID, 200_00, 0)
		inv := f.sent(m.ID)
		f.deposit(m.ID, client, 1500_00)
		if _, _, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1000_00); err != nil {
			t.Fatal(err)
		}
		if row := f.flatStatus(m.ID); row.Status != FlatCollected || row.InvoiceID != inv.ID {
			t.Fatalf("the row must reach Collected: %+v", row)
		}
		wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1000_00)), domain.CodeConflict)
		if f.transfers(m.ID) != 1 {
			t.Fatal("the fee left trust twice")
		}
	})
	t.Run("mixed matter", func(t *testing.T) {
		f := newFx(t)
		m, client := f.matter("Mixed", "mixed", 500_00, 200_00)
		f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil)
		f.expense(m.ID, 40_00, 0)
		inv := f.sent(m.ID)
		if !inv.HasFlatFee() || len(inv.Lines) != 3 {
			t.Fatalf("mixed invoice: %+v", inv.Lines)
		}
		f.deposit(m.ID, client, 2000_00)
		if s := f.flatStatus(m.ID).Status; s != FlatReady {
			t.Fatalf("before apply: %s", s)
		}
		_, applied, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 500_00)
		if err != nil || applied.ID != inv.ID || !positive(int64(applied.BalanceDueCents)) {
			t.Fatalf("apply: %v %+v", err, applied.Invoice)
		}
		if s := f.flatStatus(m.ID).Status; s != FlatCollected {
			t.Fatalf("mixed fee collected: %s", s)
		}
		before := f.snapshot()
		wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 500_00)), domain.CodeConflict)
		if f.snapshot() != before || f.transfers(m.ID) != 1 {
			t.Fatal("a second mixed apply wrote rows")
		}
		// Apply trust for the time and expenses is an ordinary transfer, not a second fee.
		if _, _, err := f.svc.ApplyTrust(f.ctx, f.attorney, inv.ID, client, f.account, applied.BalanceDueCents); err != nil {
			t.Fatal(err)
		}
		if s := f.flatStatus(m.ID).Status; s != FlatCollected {
			t.Fatalf("paid: %s", s)
		}
	})
}

// P1: the service, not the handler, narrows invoices to the InvoiceClient allowlist for a
// client-only principal, on Invoice, Invoices and InvoicePDF.
func TestServiceGivesClientsOnlyTheAllowlist(t *testing.T) {
	f := newFx(t)
	m, contact := f.matter("Redacted", "hourly", 0, 0)
	te := f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil)
	drafts, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	id := drafts[0].ID
	adj, adjReason := domain.Cents(-10_00), "SECRET-ADJUSTMENT courtesy for slow response"
	if _, err := f.svc.EditDraft(f.ctx, f.staff, id, DraftEdit{Lines: []LineInput{{Kind: LineTime, SourceID: te.ID}},
		AdjustmentCents: &adj, AdjustmentReason: &adjReason}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SendInvoice(f.ctx, f.attorney, id, "file"); err != nil {
		t.Fatal(err)
	}
	notes, dispute, dnotes := "SECRET-NOTES client is slow to pay", "disputed", "SECRET-DISPUTE says call was shorter"
	if _, err := f.svc.UpdateInvoice(f.ctx, f.attorney, id, InvoicePatch{InternalNotes: &notes, DisputeStatus: &dispute, DisputeNotes: &dnotes}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.WriteOff(f.ctx, f.attorney, id, 5_00, "SECRET-WRITEOFF goodwill"); err != nil {
		t.Fatal(err)
	}
	staffRow, err := f.svc.Invoice(f.ctx, f.attorney, id)
	if err != nil || staffRow.InternalNotes != notes || len(staffRow.WriteOffs) != 1 || staffRow.ApprovedBy.IsZero() || staffRow.Lines[0].SourceID != te.ID {
		t.Fatalf("staff keep the full record: %v %+v", err, staffRow)
	}
	client := f.clientUser(contact)
	check := func(where string, row Invoice) {
		t.Helper()
		b, _ := json.Marshal(row)
		for _, leak := range []string{"SECRET-", string(staffRow.ApprovedBy), string(te.ID), dispute} {
			if strings.Contains(string(b), leak) {
				t.Fatalf("%s: client row carries staff field %q: %s", where, leak, b)
			}
		}
		if row.InternalNotes != "" || row.DisputeStatus != "" || row.DisputeNotes != "" || row.AdjustmentReason != "" ||
			row.WriteOffReason != "" || row.WriteOffs != nil || row.VoidReason != "" || row.ApprovedAt != nil || !row.ApprovedBy.IsZero() {
			t.Fatalf("%s: staff fields reached the client: %+v", where, row)
		}
		for _, l := range row.Lines {
			if !l.SourceID.IsZero() || !l.FeeItemID.IsZero() || l.MatterFee {
				t.Fatalf("%s: line source ids reached the client: %+v", where, l)
			}
		}
		if row.ID != id || row.Number != staffRow.Number || row.AdjustmentCents != adj || row.TotalCents != staffRow.TotalCents ||
			row.BalanceDueCents != staffRow.BalanceDueCents || row.DueOn != staffRow.DueOn || len(row.Lines) != len(staffRow.Lines) {
			t.Fatalf("%s: client allowlist fields are missing: %+v", where, row)
		}
	}
	one, err := f.svc.Invoice(f.ctx, client, id)
	if err != nil {
		t.Fatal(err)
	}
	check("Invoice", one)
	if one.Status != StatusViewed {
		t.Fatalf("a client read still marks the invoice viewed: %s", one.Status)
	}
	list, err := f.svc.Invoices(f.ctx, client, InvoiceFilter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("client list: %v %d", err, len(list))
	}
	check("Invoices", list[0])
	pdf, row, err := f.svc.InvoicePDF(f.ctx, client, id)
	if err != nil || len(pdf) == 0 {
		t.Fatal(err)
	}
	check("InvoicePDF", row)
	if over, err := f.svc.Overdue(f.ctx, client); err == nil {
		for _, r := range over {
			check("Overdue", r)
		}
	}
	// The staff record itself is unchanged by the projection.
	if stored, _ := f.svc.invoice(f.ctx, id); stored.InternalNotes != notes || stored.ViewedAt == nil {
		t.Fatalf("stored invoice changed: %+v", stored)
	}
}

// P2: a staff draft edit cannot bill an unreviewed timer draft or a non-billable row, so the
// review gate on drafting and the worklist cannot be bypassed.
func TestDraftEditRefusesUnreviewedAndNonBillableSources(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Edit gate", "hourly", 0, 0)
	billed := f.logTime(f.attorney, m.ID, "2026-06-01", 4, nil)
	drafts, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	id := drafts[0].ID
	tm, _, err := f.svc.StartTimer(f.ctx, f.staff, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(30 * time.Minute)
	_, timerDraft, err := f.svc.StopTimer(f.ctx, f.staff, tm.ID)
	if err != nil || !timerDraft.NeedsReview {
		t.Fatalf("timer draft: %v %+v", err, timerDraft)
	}
	no := false
	date, desc, n := "2026-06-03", "Internal file review", 2
	nonBillable, err := f.svc.CreateTime(f.ctx, f.attorney, TimeInput{MatterID: &m.ID, WorkedOn: &date, Increments: &n,
		Description: &desc, ActivityTypeID: &f.activity, Billable: &no})
	if err != nil {
		t.Fatal(err)
	}
	amt := domain.Cents(12_00)
	freeCost, err := f.svc.CreateExpense(f.ctx, f.staff, ExpenseInput{MatterID: &m.ID, IncurredOn: &date, Description: &desc,
		AmountCents: &amt, ExpenseTypeID: &f.expType, Billable: &no})
	if err != nil {
		t.Fatal(err)
	}
	keep := LineInput{Kind: LineTime, SourceID: billed.ID}
	price := domain.Cents(99_00)
	before := f.snapshot()
	cases := map[string]struct {
		line LineInput
		code string
	}{
		"unreviewed timer draft":            {LineInput{Kind: LineTime, SourceID: timerDraft.ID, Description: "Typed in by staff"}, domain.CodeConflict},
		"unreviewed timer draft with price": {LineInput{Kind: LineTime, SourceID: timerDraft.ID, Description: "x", AmountCents: &price}, domain.CodeConflict},
		"non-billable time":                 {LineInput{Kind: LineTime, SourceID: nonBillable.ID}, domain.CodeValidation},
		"non-billable expense":              {LineInput{Kind: LineExpense, SourceID: freeCost.ID}, domain.CodeValidation},
	}
	for name, c := range cases {
		err := mustErr(f.svc.EditDraft(f.ctx, f.staff, id, DraftEdit{Lines: []LineInput{keep, c.line}}))
		if code(err) != c.code {
			t.Fatalf("%s: got %v, want %s", name, err, c.code)
		}
	}
	if f.snapshot() != before {
		t.Fatal("a refused draft edit wrote rows")
	}
	if e, _ := f.svc.timeEntry(f.ctx, timerDraft.ID); !e.InvoiceID.IsZero() {
		t.Fatal("the timer draft was billed")
	}
	// A valid edit still works, and the timer draft can be billed once reviewed.
	reviewed := "Call with opposing counsel"
	if _, err := f.svc.UpdateTime(f.ctx, f.staff, timerDraft.ID, TimeInput{Description: &reviewed}); err != nil {
		t.Fatal(err)
	}
	edited, err := f.svc.EditDraft(f.ctx, f.staff, id, DraftEdit{Lines: []LineInput{keep, {Kind: LineTime, SourceID: timerDraft.ID}}})
	if err != nil || len(edited.Lines) != 2 {
		t.Fatalf("reviewed timer entry: %v %+v", err, edited.Lines)
	}
}

// P2 (AC-004): a date-range draft by a role without draft rights is refused, not an empty 201.
func TestRangeDraftRefusedWithoutDraftRight(t *testing.T) {
	f := newFx(t)
	m, contact := f.matter("Range", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-06-01", 3, nil)
	before := f.snapshot()
	rng := DraftRequest{FromDate: "2026-01-01", ToDate: "2026-12-31"}
	wantCode(t, mustErr(f.svc.DraftInvoices(f.ctx, f.accountant, rng)), domain.CodeForbidden)
	if c := code(mustErr(f.svc.DraftInvoices(f.ctx, f.clientUser(contact), rng))); c != domain.CodeForbidden && c != domain.CodeNotFound {
		t.Fatalf("client range draft: %s", c)
	}
	if f.snapshot() != before {
		t.Fatal("a refused range draft wrote rows")
	}
	a := newAPI(t, f)
	status, body, _ := a.do(f.accountant, "POST", "/api/v1/invoice-drafts", map[string]any{"from_date": "2026-01-01", "to_date": "2026-12-31"})
	if status != 403 {
		t.Fatalf("accountant range draft over the API: %d %s", status, body)
	}
	if f.snapshot() != before {
		t.Fatal("a refused API range draft wrote rows")
	}
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, rng)
	if err != nil || len(invs) != 1 {
		t.Fatalf("staff range draft still drafts: %v %d", err, len(invs))
	}
}
