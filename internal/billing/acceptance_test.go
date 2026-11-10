package billing

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// AC-008: invoices draft from unbilled time, approve, send by system mail, accept payment, and
// void back to unbilled.
func TestAC008InvoiceLifecycle(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Estate of Reed", "hourly", 0, 0)
	// 3 increments of 6 min at the attorney's 250.00/h = 75.00; 7 at 333.33/h = 233.33 (233.331).
	t1 := f.logTime(f.attorney, m.ID, "2026-06-01", 3, nil)
	t2 := f.logTime(f.staff, m.ID, "2026-06-03", 7, cents(333_33))
	ex := f.expense(m.ID, 10_01, 15) // 11.5115 rounds to 11.51
	if t1.RateCents != 250_00 || t1.IncrementMinutes != 6 {
		t.Fatalf("default rate or increment: %+v", t1)
	}

	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	inv := invs[0]
	if inv.Status != StatusDraft || len(inv.Lines) != 3 || inv.SubtotalCents != 75_00+233_33+11_51 || inv.BalanceDueCents != inv.TotalCents {
		t.Fatalf("draft: %+v", inv)
	}
	if inv.Number != "INV-2026-0001" || inv.DueOn != "2026-07-15" {
		t.Fatalf("number or due date: %s %s", inv.Number, inv.DueOn)
	}
	for _, id := range []domain.ID{t1.ID, t2.ID} {
		row, _ := f.svc.timeEntry(f.ctx, id)
		if row.InvoiceID != inv.ID {
			t.Fatal("time entry was not linked to the draft")
		}
	}
	if _, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID}); code(err) != domain.CodeConflict {
		t.Fatalf("second draft should find nothing unbilled: %v", err)
	}

	// Staff drafts but cannot approve or send.
	wantCode(t, mustErr(f.svc.ApproveInvoice(f.ctx, f.staff, inv.ID)), domain.CodeForbidden)
	wantCode(t, mustErr(f.svc.SendInvoice(f.ctx, f.attorney, inv.ID, "email")), domain.CodeConflict)
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, inv.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, mustErr(f.svc.SendInvoice(f.ctx, f.staff, inv.ID, "email")), domain.CodeForbidden)
	sent, err := f.svc.SendInvoice(f.ctx, f.attorney, inv.ID, "email")
	if err != nil {
		t.Fatal(err)
	}
	if sent.Status != StatusSent || sent.SentAt == 0 || len(f.mail.sent) != 1 {
		t.Fatalf("send: %+v mails=%d", sent.Invoice, len(f.mail.sent))
	}
	msg := f.mail.sent[0]
	if msg.To[0] != "cora@example.test" || len(msg.Attachments) != 1 || !bytes.HasPrefix(msg.Attachments[0].Data, []byte("%PDF-")) ||
		!bytes.Contains(msg.Attachments[0].Data, []byte(inv.Number)) {
		t.Fatalf("mail: %+v", msg.To)
	}

	// Partial payment, then the rest: paid. Overpayment is refused.
	total := sent.TotalCents
	pay1, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: 100_00, Destination: DestOperating, Method: "check", InvoiceID: inv.ID})
	if err != nil {
		t.Fatal(err)
	}
	mid, _ := f.svc.invoice(f.ctx, inv.ID)
	if mid.BalanceDueCents != total-100_00 || mid.Status != StatusSent {
		t.Fatalf("partial: %+v", mid.Invoice)
	}
	_, err = f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: total, Destination: DestOperating, Method: "check", InvoiceID: inv.ID})
	wantCode(t, err, domain.CodeValidation)
	if _, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: total - 100_00, Destination: DestOperating, Method: "card", InvoiceID: inv.ID}); err != nil {
		t.Fatal(err)
	}
	paid, _ := f.svc.invoice(f.ctx, inv.ID)
	if paid.Status != StatusPaid || paid.BalanceDueCents != 0 || paid.AmountPaidCents != total || paid.PaidAt == 0 {
		t.Fatalf("paid: %+v", paid.Invoice)
	}

	// Void is refused while payments stand; reversing them reopens the balance; then void releases.
	wantCode(t, mustErr(f.svc.VoidInvoice(f.ctx, f.attorney, inv.ID, "billed the wrong client")), domain.CodeConflict)
	pays, _ := f.svc.Payments(f.ctx, f.attorney, PaymentFilter{InvoiceID: inv.ID})
	for _, p := range pays {
		wantCode(t, mustErr(f.svc.ReversePayment(f.ctx, f.staff, p.ID, "bounced")), domain.CodeForbidden)
		if _, err := f.svc.ReversePayment(f.ctx, f.attorney, p.ID, "bounced"); err != nil {
			t.Fatal(err)
		}
	}
	reopened, _ := f.svc.invoice(f.ctx, inv.ID)
	if reopened.Status != StatusSent || reopened.BalanceDueCents != total {
		t.Fatalf("reversal should reopen: %+v", reopened.Invoice)
	}
	if again, _ := f.svc.payment(f.ctx, pay1.ID); again.AmountCents != 100_00 || again.Status != PayReversed {
		t.Fatal("reversal edited the payment amount or kept it completed")
	}
	wantCode(t, mustErr(f.svc.VoidInvoice(f.ctx, f.staff, inv.ID, "wrong client")), domain.CodeForbidden)
	wantCode(t, mustErr(f.svc.VoidInvoice(f.ctx, f.attorney, inv.ID, " ")), domain.CodeValidation)
	void, err := f.svc.VoidInvoice(f.ctx, f.attorney, inv.ID, "billed the wrong client")
	if err != nil {
		t.Fatal(err)
	}
	if void.Status != StatusVoid || void.VoidReason == "" {
		t.Fatal("void")
	}
	for _, id := range []domain.ID{t1.ID, t2.ID} {
		row, _ := f.svc.timeEntry(f.ctx, id)
		if !row.InvoiceID.IsZero() {
			t.Fatal("void did not release time to unbilled")
		}
	}
	if row, _ := f.svc.expense(f.ctx, ex.ID); !row.InvoiceID.IsZero() {
		t.Fatal("void did not release the expense")
	}
	redraft, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil || redraft[0].SubtotalCents != inv.SubtotalCents || redraft[0].Number != "INV-2026-0002" {
		t.Fatalf("released work did not redraft: %v %+v", err, redraft)
	}
	hist, err := f.svc.History(f.ctx, f.accountant, "invoice", inv.ID)
	if err != nil || len(hist) < 5 {
		t.Fatalf("history: %v %d", err, len(hist))
	}
}

func mustErr[T any](_ T, err error) error { return err }

func TestSendFailuresWriteNothing(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Mail case", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil)
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	id := invs[0].ID
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, id); err != nil {
		t.Fatal(err)
	}
	rowsOnly := func(s string) string { return s[:strings.LastIndex(s, "|audit=")] }
	eventsBefore, _ := f.st.List(f.ctx, kindEvent)
	before := f.snapshotWithout(kindEvent)
	auditBefore := f.aud.Len()
	f.mail.fail = errors.New("smtp down")
	if _, err := f.svc.SendInvoice(f.ctx, f.attorney, id, "email"); err == nil {
		t.Fatal("mail failure must fail the send")
	}
	f.mail.fail = nil
	// Mail goes out after the commit, so a failed mail undoes the committed send: the billing
	// rows are back as they were (no sent status, no sent_to). The event stream is append-only:
	// it keeps invoice.sent and adds invoice.send_undone. The audit log keeps the send and its undo.
	if rowsOnly(f.snapshotWithout(kindEvent)) != rowsOnly(before) {
		t.Fatal("a failed mail left the send in place")
	}
	if got := f.eventCounts(id); got["invoice.sent"] != 1 || got["invoice.send_undone"] != 1 {
		t.Fatalf("event stream must keep invoice.sent and append invoice.send_undone: %v", got)
	}
	if eventsAfter, _ := f.st.List(f.ctx, kindEvent); len(eventsAfter) != len(eventsBefore)+2 {
		t.Fatalf("events %d -> %d; no event may be deleted", len(eventsBefore), len(eventsAfter))
	}
	if f.aud.Len() <= auditBefore {
		t.Fatal("the send and its undo must be audited")
	}
	hist, err := f.svc.History(f.ctx, f.attorney, "invoice", id)
	if err != nil || !strings.Contains(hist[0].Reason, "send undone") {
		t.Fatalf("undo not audited: %v %+v", err, hist)
	}
	before = f.snapshot()
	f.svc.d.Mail = nil
	wantCode(t, mustErr(f.svc.SendInvoice(f.ctx, f.attorney, id, "email")), domain.CodeConflict)
	wantCode(t, mustErr(f.svc.SendInvoice(f.ctx, f.attorney, id, "fax")), domain.CodeValidation)
	if f.snapshot() != before {
		t.Fatal("a failed send wrote rows")
	}
	inv, _ := f.svc.invoice(f.ctx, id)
	if inv.Status != StatusDraft {
		t.Fatal("status moved")
	}
	// Editing an approved draft clears the approval.
	if _, err := f.svc.EditDraft(f.ctx, f.staff, id, DraftEdit{Lines: []LineInput{{Kind: LineTime, SourceID: inv.Lines[0].SourceID}}, ClientMessage: strPtr("Thanks")}); err != nil {
		t.Fatal(err)
	}
	f.svc.d.Mail = f.mail
	wantCode(t, mustErr(f.svc.SendInvoice(f.ctx, f.attorney, id, "file")), domain.CodeConflict)
}

func strPtr(s string) *string { return &s }

func TestExactCents(t *testing.T) {
	cases := []struct {
		name string
		got  func() (domain.Cents, error)
		want domain.Cents
	}{
		{"233.331", func() (domain.Cents, error) { return timeAmount(333_33, 42) }, 233_33},
		{"0.333", func() (domain.Cents, error) { return timeAmount(1_00, 20) }, 33},
		{"0.667", func() (domain.Cents, error) { return timeAmount(2_00, 20) }, 67},
		{"half up", func() (domain.Cents, error) { return timeAmount(1, 30) }, 1},
		{"just under half", func() (domain.Cents, error) { return timeAmount(1, 29) }, 0},
		{"half away from zero", func() (domain.Cents, error) { return timeAmount(-1, 30) }, -1},
		{"11.5115", func() (domain.Cents, error) { return expenseAmount(10_01, 15) }, 11_51},
		{"11.5575", func() (domain.Cents, error) { return expenseAmount(10_05, 15) }, 11_56},
		{"no markup", func() (domain.Cents, error) { return expenseAmount(10_05, 0) }, 10_05},
		{"5.9178", func() (domain.Cents, error) { return lateInterest(900_00, 12, 20) }, 5_92},
		{"interest half", func() (domain.Cents, error) { return lateInterest(182_50, 1, 1) }, 1}, // 0.5 cent
	}
	for _, c := range cases {
		got, err := c.got()
		if err != nil || got != c.want {
			t.Fatalf("%s: %d %v want %d", c.name, got, err, c.want)
		}
	}
	if _, err := timeAmount(domain.Cents(1<<62), 4); !errors.Is(err, domain.ErrOverflow) {
		t.Fatal("overflow must fail")
	}
	if _, err := lateInterest(domain.Cents(1<<62), 100, 365); !errors.Is(err, domain.ErrOverflow) {
		t.Fatal("interest overflow must fail")
	}
	if incrementsFor(0, 6) != 1 || incrementsFor(1, 6) != 1 || incrementsFor(360, 6) != 1 || incrementsFor(361, 6) != 2 || incrementsFor(540, 6) != 2 ||
		incrementsFor(-5, 6) != 1 || incrementsFor(61, 0) != incrementsFor(61, domain.BillingTimeIncrementMinutes) {
		t.Fatal("increment rounding")
	}
	f := newFx(t)
	m, _ := f.matter("Rates", "hourly", 0, 0)
	huge := domain.Cents(1 << 62)
	date, desc, n := "2026-06-01", "x", 1000
	_, err := f.svc.CreateTime(f.ctx, f.staff, TimeInput{MatterID: &m.ID, WorkedOn: &date, Increments: &n, Description: &desc, ActivityTypeID: &f.activity, RateCents: &huge})
	wantCode(t, err, domain.CodeValidation)
	neg := domain.Cents(-1)
	_, err = f.svc.CreateTime(f.ctx, f.staff, TimeInput{MatterID: &m.ID, WorkedOn: &date, Increments: &n, Description: &desc, ActivityTypeID: &f.activity, RateCents: &neg})
	wantCode(t, err, domain.CodeValidation)
	// The firm increment is captured on the entry: a later setting change does not reprice it.
	row := f.logTime(f.staff, m.ID, date, 2, cents(100_00))
	if row.Minutes() != 12 {
		t.Fatal("minutes")
	}
}

// AC-004: an accountant reads billing and trust and cannot write anything.
func TestAC004AccountantReadOnly(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Books", "hourly", 0, 0)
	te := f.logTime(f.attorney, m.ID, "2026-06-01", 5, nil)
	ex := f.expense(m.ID, 25_00, 0)
	inv := f.sent(m.ID)
	f.deposit(m.ID, client, 500_00)
	acct := f.accountant
	ctx := f.ctx

	if _, err := f.svc.Invoices(ctx, acct, InvoiceFilter{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Invoice(ctx, acct, inv.ID); err != nil {
		t.Fatal(err)
	}
	if pdf, _, err := f.svc.InvoicePDF(ctx, acct, inv.ID); err != nil || !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatal(err)
	}
	if rows, err := f.svc.Payments(ctx, acct, PaymentFilter{}); err != nil || len(rows) != 1 {
		t.Fatal(err, len(rows))
	}
	if rows, err := f.svc.TimeEntries(ctx, acct, TimeFilter{}); err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	if _, err := f.svc.Expenses(ctx, acct, ExpenseFilter{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Worklist(ctx, acct); err != nil {
		t.Fatal(err)
	}
	for _, rep := range []func(context.Context, domain.Principal, ReportFilter) (Report, error){
		f.svc.UnbilledReport, f.svc.TimeByUserReport, f.svc.ARAgingReport, f.svc.PaymentsReport, f.svc.WriteOffsReport} {
		if _, err := rep(ctx, acct, ReportFilter{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.History(ctx, acct, "time_entry", te.ID); err != nil {
		t.Fatal(err)
	}

	before := f.snapshot()
	date, desc, n := "2026-06-02", "x", 1
	name := "Wills"
	writes := map[string]error{
		"time":         mustErr(f.svc.CreateTime(ctx, acct, TimeInput{MatterID: &m.ID, WorkedOn: &date, Increments: &n, Description: &desc, ActivityTypeID: &f.activity})),
		"time edit":    mustErr(f.svc.UpdateTime(ctx, acct, te.ID, TimeInput{Description: &desc})),
		"expense":      mustErr(f.svc.CreateExpense(ctx, acct, ExpenseInput{MatterID: &m.ID, IncurredOn: &date, Description: &desc, AmountCents: cents(1), ExpenseTypeID: &f.expType})),
		"expense edit": mustErr(f.svc.UpdateExpense(ctx, acct, ex.ID, ExpenseInput{Description: &desc})),
		"draft":        mustErr(f.svc.DraftInvoices(ctx, acct, DraftRequest{MatterID: m.ID})),
		"draft range":  mustErr(f.svc.DraftInvoices(ctx, acct, DraftRequest{FromDate: "2026-01-01", ToDate: "2026-12-31"})),
		"edit":         mustErr(f.svc.EditDraft(ctx, acct, inv.ID, DraftEdit{})),
		"approve":      mustErr(f.svc.ApproveInvoice(ctx, acct, inv.ID)),
		"send":         mustErr(f.svc.SendInvoice(ctx, acct, inv.ID, "email")),
		"patch":        mustErr(f.svc.UpdateInvoice(ctx, acct, inv.ID, InvoicePatch{InternalNotes: &desc})),
		"void":         mustErr(f.svc.VoidInvoice(ctx, acct, inv.ID, "x")),
		"write-off":    mustErr(f.svc.WriteOff(ctx, acct, inv.ID, 1, "x")),
		"payment":      mustErr(f.svc.CreatePayment(ctx, acct, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: 1, Destination: DestOperating, Method: "cash", InvoiceID: inv.ID})),
		"trust pay":    mustErr(f.svc.CreatePayment(ctx, acct, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: 1, Destination: DestTrust, TrustAccountID: f.account, Method: "cash"})),
		"apply trust":  mustErr2(f.svc.ApplyTrust(ctx, acct, inv.ID, client, f.account, 1)),
		"flat fee":     mustErr2(f.svc.ApplyFlatFee(ctx, acct, m.ID, client, f.account, 1)),
		"replenish":    mustErr(f.svc.RequestReplenishment(ctx, acct, inv.ID, client, 1, "")),
		"timer":        mustErr3(f.svc.StartTimer(ctx, acct, m.ID)),
		"fee item":     mustErr(f.svc.CreateFeeItem(ctx, acct, FeeItemInput{Name: &name, DefaultAmountCents: cents(1)})),
		"type":         mustErr(f.svc.CreateType(ctx, acct, false, TypeInput{Name: &name})),
	}
	for name, err := range writes {
		if c := code(err); c != domain.CodeForbidden && c != domain.CodeNotFound {
			t.Fatalf("%s: accountant write was not refused: %v", name, err)
		}
	}
	pays, _ := f.svc.Payments(ctx, f.attorney, PaymentFilter{})
	wantCode(t, mustErr(f.svc.ReversePayment(ctx, acct, pays[0].ID, "x")), domain.CodeForbidden)
	wantCode(t, mustErr(f.svc.UpdatePaymentStatus(ctx, acct, pays[0].ID, PayFailed)), domain.CodeForbidden)
	if f.snapshot() != before {
		t.Fatal("an accountant write changed stored rows or the audit log")
	}
}

func mustErr2[A, B any](_ A, _ B, err error) error { return err }
func mustErr3[A, B any](_ A, _ B, err error) error { return err }

// Matter walls hide billing everywhere: lists, gets, reports, worklist and drafting.
func TestWallHidesBilling(t *testing.T) {
	f := newFx(t)
	open, _ := f.matter("Open matter", "hourly", 0, 0)
	secret, client := f.matter("Walled estate", "flat", 900_00, 0)
	f.logTime(f.attorney, open.ID, "2026-06-01", 2, nil)
	hidden := f.logTime(f.attorney, secret.ID, "2026-06-01", 2, nil)
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: secret.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ps.SetWall(f.ctx, f.admin, secret.ID, []domain.ID{f.attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []domain.Principal{f.staff, f.admin, f.accountant} {
		rows, err := f.svc.TimeEntries(f.ctx, p, TimeFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.MatterID == secret.ID {
				t.Fatalf("time list leaked to %v", p.Roles)
			}
		}
		wantCode(t, mustErr(f.svc.TimeEntry(f.ctx, p, hidden.ID)), domain.CodeNotFound)
		wantCode(t, mustErr(f.svc.Invoice(f.ctx, p, invs[0].ID)), domain.CodeNotFound)
		ilist, _ := f.svc.Invoices(f.ctx, p, InvoiceFilter{})
		if len(ilist) != 0 {
			t.Fatalf("invoice list leaked to %v", p.Roles)
		}
		wl, _ := f.svc.Worklist(f.ctx, p)
		for _, r := range wl.Flat {
			if r.MatterID == secret.ID {
				t.Fatal("worklist leaked")
			}
		}
		rep, _ := f.svc.UnbilledReport(f.ctx, p, ReportFilter{})
		for _, r := range rep.Rows {
			if r["matter_id"] == secret.ID {
				t.Fatal("report leaked")
			}
		}
		wantCode(t, mustErr(f.svc.UnbilledReport(f.ctx, p, ReportFilter{MatterID: secret.ID})), domain.CodeNotFound)
	}
	wantCode(t, mustErr(f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterIDs: []domain.ID{open.ID, secret.ID}})), domain.CodeNotFound)
	ranged, err := f.svc.DraftInvoices(f.ctx, f.admin, DraftRequest{FromDate: "2026-01-01", ToDate: "2026-12-31"})
	if err != nil || len(ranged) != 1 || ranged[0].MatterID != open.ID {
		t.Fatalf("range draft: %v %+v", err, ranged)
	}
	wantCode(t, mustErr(f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: secret.ID, AmountCents: 1, Destination: DestOperating, Method: "cash"})), domain.CodeNotFound)
	if _, err := f.svc.Invoice(f.ctx, f.attorney, invs[0].ID); err != nil {
		t.Fatal("the attorney on the wall lost access")
	}
}

// AC-026: a flat-fee matter with the fee in trust shows Ready to apply; applying it posts the
// trust transfer and marks it Collected.
func TestAC026FlatFeeReadyToApply(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Simple will", "flat", 1500_00, 0)
	nofee, _ := f.matter("Fee pending", "flat", 0, 0)
	status := func(id domain.ID) FlatRow {
		wl, err := f.svc.Worklist(f.ctx, f.staff)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range wl.Flat {
			if r.MatterID == id {
				return r
			}
		}
		t.Fatalf("matter %s not on worklist", id)
		return FlatRow{}
	}
	if status(nofee.ID).Status != FlatNeedsFee || status(m.ID).Status != FlatAwaiting {
		t.Fatal("initial statuses")
	}
	wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1500_00)), domain.CodeConflict)
	f.deposit(m.ID, client, 1000_00)
	if status(m.ID).Status != FlatAwaiting {
		t.Fatal("partial funds are not ready")
	}
	f.deposit(m.ID, client, 600_00)
	row := status(m.ID)
	if row.Status != FlatReady || row.TrustCents != 1600_00 || row.ClientID != client || StatusLabel(row.Status) != "Ready to apply" {
		t.Fatalf("ready row: %+v", row)
	}
	before := f.snapshot()
	wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.staff, m.ID, client, f.account, 1500_00)), domain.CodeForbidden)
	wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1400_00)), domain.CodeValidation)
	wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 0)), domain.CodeValidation)
	if f.snapshot() != before {
		t.Fatal("a refused apply wrote rows")
	}
	posted, inv, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1500_00)
	if err != nil {
		t.Fatal(err)
	}
	if posted.EntryID.IsZero() || inv.Status != StatusPaid || inv.TotalCents != 1500_00 || !inv.HasFlatFee() {
		t.Fatalf("applied: %+v %+v", posted, inv.Invoice)
	}
	var transfer *fakeEntry
	for _, e := range f.trust.entries(f.ctx) {
		if e.ID == posted.EntryID {
			c := e
			transfer = &c
		}
	}
	if transfer == nil || transfer.Type != "transfer_to_operating" || transfer.Signed != -1500_00 || transfer.Account != f.account || !transfer.FlatFee {
		t.Fatalf("ledger entry: %+v", transfer)
	}
	if got := status(m.ID); got.Status != FlatCollected || got.TrustCents != 100_00 || StatusLabel(got.Status) != "Collected" {
		t.Fatalf("collected row: %+v", got)
	}
	pays, _ := f.svc.Payments(f.ctx, f.accountant, PaymentFilter{InvoiceID: inv.ID})
	if len(pays) != 1 || pays[0].Method != MethodInternal || pays[0].TrustEntryID != posted.EntryID {
		t.Fatalf("payment: %+v", pays)
	}
	wantCode(t, mustErr2(f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 1500_00)), domain.CodeConflict)
	if again, _ := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{FromDate: "2026-01-01", ToDate: "2026-12-31"}); len(again) != 0 {
		t.Fatal("a collected flat fee was drafted again")
	}
}

// A trust failure inside the apply rolls back the invoice and payment with it.
func TestFlatFeeTrustFailureRollsBack(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Rollback will", "flat", 500_00, 0)
	f.deposit(m.ID, client, 500_00)
	f.trust.fail = nil
	before := f.snapshot()
	orig := f.svc.d.Trust
	f.svc.d.Trust = failingPost{orig}
	if _, _, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 500_00); err == nil {
		t.Fatal("expected failure")
	}
	if f.snapshot() != before {
		t.Fatal("failed apply left rows behind")
	}
	if len(f.trust.committed) != 1 {
		t.Fatalf("only the deposit's trust effects run after commit: %d", len(f.trust.committed))
	}
}

type failingPost struct{ Trust }

func (failingPost) Post(context.Context, domain.Principal, TrustPosting) (TrustPosted, error) {
	return TrustPosted{}, errors.New("ledger unavailable")
}

func (failingPost) ApplyFlatFee(context.Context, domain.Principal, FlatFeeApplication) (TrustPosted, error) {
	return TrustPosted{}, errors.New("ledger unavailable")
}

// AC-009 (billing half): an unpaid invoice shows on the practice close screen as a warning and
// the matter still closes.
func TestAC009UnpaidInvoiceWarnsOnClose(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Closing case", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil)
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, w, _ := f.ps.PreviewClose(f.ctx, f.attorney, m.ID); len(w) != 0 {
		t.Fatalf("a draft is not an unpaid balance: %v", w)
	}
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, invs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SendInvoice(f.ctx, f.attorney, invs[0].ID, "file"); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := f.ps.PreviewClose(f.ctx, f.attorney, m.ID)
	if err != nil || len(warnings) != 1 || warnings[0].Code != practice.WarnUnpaidInvoice || !strings.Contains(warnings[0].Detail, "250.00") {
		t.Fatalf("close warning: %v %+v", err, warnings)
	}
	closed, rec, err := f.ps.CloseMatter(f.ctx, f.attorney, m.ID, practice.CloseInput{Reason: "Client moved", Acknowledge: true})
	if err != nil || closed.Status != "closed" || len(rec.Warnings) != 1 {
		t.Fatalf("close: %v %+v", err, rec)
	}
	if sum, err := f.svc.MatterSummary(f.ctx, f.accountant, m.ID); err != nil || sum.UnpaidCents != 250_00 || sum.TimeMinutes != 60 {
		t.Fatalf("summary: %v %+v", err, sum)
	}
}

func TestTimersPauseResumeStop(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Timed", "hourly", 0, 120_00)
	tm, created, err := f.svc.StartTimer(f.ctx, f.staff, m.ID)
	if err != nil || !created || tm.Status() != "running" {
		t.Fatal(err)
	}
	wantCode(t, mustErr3(f.svc.StartTimer(f.ctx, f.staff, m.ID)), domain.CodeConflict)
	f.now = f.now.Add(7 * time.Minute)
	paused, err := f.svc.PauseTimer(f.ctx, f.staff, tm.ID)
	if err != nil || paused.AccumulatedSeconds != 420 || paused.Status() != "paused" {
		t.Fatalf("pause: %v %+v", err, paused)
	}
	wantCode(t, mustErr(f.svc.PauseTimer(f.ctx, f.staff, tm.ID)), domain.CodeConflict)
	wantCode(t, mustErr(f.svc.PauseTimer(f.ctx, f.staff2, tm.ID)), domain.CodeNotFound)
	f.now = f.now.Add(time.Hour)
	again, created, err := f.svc.StartTimer(f.ctx, f.staff, m.ID)
	if err != nil || created || again.ID != tm.ID || again.AccumulatedSeconds != 420 {
		t.Fatalf("start on a paused timer resumes it: %v %+v", err, again)
	}
	f.now = f.now.Add(2 * time.Minute)
	stopped, entry, err := f.svc.StopTimer(f.ctx, f.staff, tm.ID)
	if err != nil || stopped.Status() != "stopped" || stopped.AccumulatedSeconds != 540 || stopped.TimeEntryID != entry.ID {
		t.Fatalf("stop: %v %+v", err, stopped)
	}
	// 9 minutes rounds up to two 6-minute increments at the 120.00 matter rate.
	if entry.Increments != 2 || entry.RateCents != 120_00 || entry.Source != "timer" || entry.ActivityType != string(f.activity) {
		t.Fatalf("entry: %+v", entry)
	}
	if list, _ := f.svc.Timers(f.ctx, f.staff); len(list) != 0 {
		t.Fatal("a stopped timer is not active")
	}
	wantCode(t, mustErr2(f.svc.StopTimer(f.ctx, f.staff, tm.ID)), domain.CodeConflict)
	wantCode(t, mustErr3(f.svc.StartTimer(f.ctx, f.accountant, m.ID)), domain.CodeForbidden)
}

func TestBulkRejectsWholeBatch(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Bulk", "hourly", 0, 0)
	before := f.snapshot()
	date, desc, one, zero := "2026-06-01", "Call", 1, 0
	bad := "2026-13-40"
	items := []TimeInput{
		{MatterID: &m.ID, WorkedOn: &date, Increments: &one, Description: &desc, ActivityTypeID: &f.activity},
		{MatterID: &m.ID, WorkedOn: &bad, Increments: &one, Description: &desc, ActivityTypeID: &f.activity},
	}
	wantCode(t, mustErr(f.svc.BulkTime(f.ctx, f.staff, items)), domain.CodeValidation)
	items[1] = TimeInput{MatterID: &m.ID, WorkedOn: &date, Increments: &zero, Description: &desc, ActivityTypeID: &f.activity}
	wantCode(t, mustErr(f.svc.BulkTime(f.ctx, f.staff, items)), domain.CodeValidation)
	if f.snapshot() != before {
		t.Fatal("rejected batch wrote rows")
	}
	items[1] = items[0]
	rows, err := f.svc.BulkTime(f.ctx, f.staff, items)
	if err != nil || len(rows) != 2 {
		t.Fatal(err)
	}
}

func TestApplyTrustAndReverse(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Collections", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-06-01", 20, nil) // 500.00
	inv := f.sent(m.ID)
	f.deposit(m.ID, client, 200_00)
	col, err := f.svc.CollectionsFor(f.ctx, f.staff, inv)
	if err != nil || !col.Show || col.DefaultClient != client || col.Balance != 500_00 || col.Suggested != 200_00 {
		t.Fatalf("collections: %v %+v", err, col)
	}
	wantCode(t, mustErr2(f.svc.ApplyTrust(f.ctx, f.staff, inv.ID, client, f.account, 100_00)), domain.CodeForbidden)
	wantCode(t, mustErr2(f.svc.ApplyTrust(f.ctx, f.attorney, inv.ID, client, f.account, 600_00)), domain.CodeValidation)
	// Below zero is never blocked: the trust service posts and flags.
	pay, _, err := f.svc.ApplyTrust(f.ctx, f.attorney, inv.ID, client, f.account, 300_00)
	if err != nil {
		t.Fatal(err)
	}
	if f.trust.balance(f.ctx, m.ID, client) != -100_00 {
		t.Fatal("transfer amount")
	}
	flagged := false
	for _, e := range f.trust.entries(f.ctx) {
		flagged = flagged || e.Flagged
	}
	if !flagged {
		t.Fatal("below-zero transfer should be flagged by trust")
	}
	after, _ := f.svc.invoice(f.ctx, inv.ID)
	if after.BalanceDueCents != 200_00 || after.AmountPaidCents != 300_00 {
		t.Fatalf("invoice: %+v", after.Invoice)
	}
	if _, err := f.svc.ReversePayment(f.ctx, f.attorney, pay.ID, "applied to the wrong invoice"); err != nil {
		t.Fatal(err)
	}
	if f.trust.balance(f.ctx, m.ID, client) != 200_00 {
		t.Fatal("reversal did not restore trust")
	}
	wantCode(t, mustErr(f.svc.ReversePayment(f.ctx, f.attorney, pay.ID, "again")), domain.CodeConflict)
	if back, _ := f.svc.invoice(f.ctx, inv.ID); back.BalanceDueCents != 500_00 {
		t.Fatal("reversal did not restore balance")
	}
	req, err := f.svc.RequestReplenishment(f.ctx, f.staff, inv.ID, client, 500_00, "Please replenish")
	if err != nil || req.(map[string]any)["invoice_id"] != inv.ID {
		t.Fatalf("replenish: %v %v", err, req)
	}
	wantCode(t, mustErr(f.svc.RequestReplenishment(f.ctx, f.staff, inv.ID, client, 0, "")), domain.CodeValidation)
	// A trust deposit is not applied to an invoice, and internal_transfer is reserved.
	wantCode(t, mustErr(f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: 1, Destination: DestTrust, TrustAccountID: f.account, Method: "cash", InvoiceID: inv.ID})), domain.CodeValidation)
	wantCode(t, mustErr(f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: 1, Destination: DestOperating, Method: MethodInternal})), domain.CodeValidation)
}

func TestPendingPaymentStatus(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Card", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-06-01", 4, nil) // 100.00
	inv := f.sent(m.ID)
	pay, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: m.ID, AmountCents: 100_00, Destination: DestOperating, Method: "card", Status: PayPending, InvoiceID: inv.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.invoice(f.ctx, inv.ID); got.BalanceDueCents != 100_00 {
		t.Fatal("pending payment applied")
	}
	if same, err := f.svc.UpdatePaymentStatus(f.ctx, f.staff, pay.ID, PayPending); err != nil || same.Status != PayPending {
		t.Fatal(err)
	}
	if done, err := f.svc.UpdatePaymentStatus(f.ctx, f.staff, pay.ID, PayCompleted); err != nil || done.Status != PayCompleted {
		t.Fatal(err)
	}
	if got, _ := f.svc.invoice(f.ctx, inv.ID); got.Status != StatusPaid {
		t.Fatal("completion did not pay the invoice")
	}
	wantCode(t, mustErr(f.svc.UpdatePaymentStatus(f.ctx, f.staff, pay.ID, PayFailed)), domain.CodeConflict)
}

func TestDraftEditFeeItemsAndRelease(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Mixed", "mixed", 300_00, 200_00)
	t1 := f.logTime(f.attorney, m.ID, "2026-06-01", 5, nil) // 100.00 at the 200.00 matter rate
	if t1.RateCents != 200_00 {
		t.Fatal("matter rate wins")
	}
	name := "Deed preparation"
	item, err := f.svc.CreateFeeItem(f.ctx, f.admin, FeeItemInput{Name: &name, DefaultAmountCents: cents(450_00)})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, mustErr(f.svc.CreateFeeItem(f.ctx, f.attorney, FeeItemInput{Name: &name, DefaultAmountCents: cents(1)})), domain.CodeForbidden)
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	inv := invs[0]
	if len(inv.Lines) != 2 || inv.SubtotalCents != 400_00 {
		t.Fatalf("mixed draft bills fee and time: %+v", inv.Lines)
	}
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, inv.ID); err != nil {
		t.Fatal(err)
	}
	edit := DraftEdit{
		Lines:           []LineInput{{Kind: LineFlatFee, Description: "Flat fee", AmountCents: cents(300_00)}, {Kind: LineFlatFee, FeeItemID: item.ID, AmountCents: cents(425_00)}},
		AdjustmentCents: cents(-25_00),
	}
	wantCode(t, mustErr(f.svc.EditDraft(f.ctx, f.staff, inv.ID, edit)), domain.CodeValidation)
	edit.AdjustmentReason = strPtr("Courtesy discount")
	got, err := f.svc.EditDraft(f.ctx, f.staff, inv.ID, edit)
	if err != nil {
		t.Fatal(err)
	}
	if got.Approved() || got.Lines[1].Description != name || got.Lines[1].AmountCents != 425_00 || got.TotalCents != 700_00 {
		t.Fatalf("edit: %+v", got)
	}
	if row, _ := f.svc.timeEntry(f.ctx, t1.ID); !row.InvoiceID.IsZero() {
		t.Fatal("removed time line was not released")
	}
	inactive := false
	if _, err := f.svc.UpdateFeeItem(f.ctx, f.admin, item.ID, FeeItemInput{Active: &inactive}); err != nil {
		t.Fatal(err)
	}
	// An inactive item stays on the invoice it is on, but cannot be newly added elsewhere.
	if _, err := f.svc.EditDraft(f.ctx, f.staff, inv.ID, DraftEdit{Lines: []LineInput{{Kind: LineFlatFee, FeeItemID: item.ID}}}); err != nil {
		t.Fatal(err)
	}
	other, _ := f.matter("Other", "hourly", 0, 0)
	f.logTime(f.attorney, other.ID, "2026-06-01", 1, nil)
	oinv, _ := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: other.ID})
	wantCode(t, mustErr(f.svc.EditDraft(f.ctx, f.staff, oinv[0].ID, DraftEdit{Lines: []LineInput{{Kind: LineFlatFee, FeeItemID: item.ID}}})), domain.CodeValidation)
	// A time line from another matter or one billed elsewhere is refused.
	wantCode(t, mustErr(f.svc.EditDraft(f.ctx, f.staff, oinv[0].ID, DraftEdit{Lines: []LineInput{{Kind: LineTime, SourceID: t1.ID}}})), domain.CodeValidation)
	wantCode(t, mustErr(f.svc.EditDraft(f.ctx, f.staff, inv.ID, DraftEdit{Lines: []LineInput{{Kind: LineTime, SourceID: oinv[0].Lines[0].SourceID}}})), domain.CodeValidation)
	if _, err := f.svc.EditDraft(f.ctx, f.staff, inv.ID, DraftEdit{Lines: []LineInput{{Kind: LineTime, SourceID: t1.ID}}}); err != nil {
		t.Fatal(err)
	}
	if row, _ := f.svc.timeEntry(f.ctx, t1.ID); row.InvoiceID != inv.ID {
		t.Fatal("added line did not bill the entry")
	}
	if _, err := f.svc.UpdateTime(f.ctx, f.staff, t1.ID, TimeInput{Description: strPtr("x")}); code(err) != domain.CodeConflict {
		t.Fatal("a billed entry must not change")
	}
}

func TestInvoiceNumberFormat(t *testing.T) {
	f := newFx(t)
	f.st.Put(f.ctx, kindInvoice, string(f.id()), Invoice{Invoice: domain.Invoice{Number: "INV-2026-0041"}})
	f.st.Put(f.ctx, kindInvoice, string(f.id()), Invoice{Invoice: domain.Invoice{Number: "INV-2025-0099"}})
	n, err := f.svc.nextNumber(f.ctx, "2026-06-15")
	if err != nil || n != "INV-2026-0042" {
		t.Fatal(n, err)
	}
	if n, _ := f.svc.nextNumber(f.ctx, "2027-01-02"); n != "INV-2027-0001" {
		t.Fatal(n)
	}
}

func TestClientSeesOnlySentInvoicesAndMarksViewed(t *testing.T) {
	f := newFx(t)
	m, contact := f.matter("Portal", "hourly", 0, 0)
	clientP := person(f.id(), domain.RoleClient)
	enc, _ := domain.MarshalRoles(clientP.Roles)
	f.pm.PutUser(f.ctx, domain.User{Meta: domain.Meta{ID: clientP.UserID}, Name: "Cora", Roles: enc, Active: true, ContactID: contact})
	f.logTime(f.attorney, m.ID, "2026-06-01", 2, nil)
	draft, _ := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	wantCode(t, mustErr(f.svc.Invoice(f.ctx, clientP, draft[0].ID)), domain.CodeNotFound)
	f.svc.ApproveInvoice(f.ctx, f.attorney, draft[0].ID)
	f.svc.SendInvoice(f.ctx, f.attorney, draft[0].ID, "file")
	seen, err := f.svc.Invoice(f.ctx, clientP, draft[0].ID)
	if err != nil || seen.Status != StatusViewed {
		t.Fatalf("client view: %v %s", err, seen.Status)
	}
	if stored, _ := f.svc.invoice(f.ctx, draft[0].ID); stored.ViewedAt == nil {
		t.Fatal("viewed_at not stored")
	}
	if list, _ := f.svc.Invoices(f.ctx, clientP, InvoiceFilter{}); len(list) != 1 {
		t.Fatal("client list")
	}
	wantCode(t, mustErr(f.svc.TimeEntries(f.ctx, clientP, TimeFilter{})), domain.CodeNotFound)
	wantCode(t, mustErr(f.svc.Payments(f.ctx, clientP, PaymentFilter{})), domain.CodeNotFound)
	// A client on a different matter does not see it.
	_, otherContact := f.matter("Elsewhere", "hourly", 0, 0)
	stranger := person(f.id(), domain.RoleClient)
	f.pm.PutUser(f.ctx, domain.User{Meta: domain.Meta{ID: stranger.UserID}, Name: "Stranger", Roles: enc, Active: true, ContactID: otherContact})
	wantCode(t, mustErr(f.svc.Invoice(f.ctx, stranger, draft[0].ID)), domain.CodeNotFound)
}

func TestWriteOffAndLateInterest(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Late", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-01-05", 40, nil) // 1000.00
	f.now = time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	inv := f.sent(m.ID)
	wantCode(t, mustErr(f.svc.ApplyLateInterest(f.ctx, f.attorney, inv.ID)), domain.CodeConflict) // off by default
	wantCode(t, mustErr(f.svc.WriteOff(f.ctx, f.attorney, inv.ID, 1001_00, "too much")), domain.CodeValidation)
	wantCode(t, mustErr(f.svc.WriteOff(f.ctx, f.staff, inv.ID, 1_00, "staff")), domain.CodeForbidden)
	wo, err := f.svc.WriteOff(f.ctx, f.attorney, inv.ID, 100_00, "Goodwill")
	if err != nil || wo.BalanceDueCents != 900_00 || len(wo.WriteOffs) != 1 || wo.WriteOffs[0].On != "2026-01-10" {
		t.Fatalf("write-off: %v %+v", err, wo.Invoice)
	}
	rep, _ := f.svc.WriteOffsReport(f.ctx, f.accountant, ReportFilter{FromDate: "2026-01-01", ToDate: "2026-01-31"})
	if len(rep.Rows) != 1 || rep.Rows[0]["amount_cents"] != int64(100_00) {
		t.Fatalf("write-off report: %+v", rep.Rows)
	}
	// AR aging: due 2026-02-09; on 2026-04-01 it is 51 days past due.
	aging, _ := f.svc.ARAgingReport(f.ctx, f.accountant, ReportFilter{AsOf: "2026-04-01"})
	if len(aging.Rows) != 1 || aging.Rows[0]["bucket"] != "31-60" || aging.Rows[0]["days_past_due"] != 51 || aging.Rows[0]["balance_cents"] != int64(900_00) {
		t.Fatalf("aging: %+v", aging.Rows)
	}
	// Before the write-off date the full amount was owed.
	if early, _ := f.svc.ARAgingReport(f.ctx, f.accountant, ReportFilter{AsOf: "2026-01-09"}); len(early.Rows) != 0 {
		t.Fatal("an invoice issued later is not on an earlier aging")
	}
	if _, err := f.ps.PinMarker(f.ctx, f.attorney, m.ID, practice.MarkerZeroFee, ""); err != nil {
		t.Fatal(err)
	}
	if zf, _ := f.svc.ARAgingReport(f.ctx, f.accountant, ReportFilter{AsOf: "2026-04-01"}); len(zf.Rows) != 0 {
		t.Fatal("zero-fee matters drop out of AR aging")
	}
	if od, _ := f.svc.Overdue(f.ctx, f.staff); len(od) != 0 {
		t.Fatal("zero-fee matters drop out of collections")
	}
	f.ps.RemoveMarker(f.ctx, f.attorney, m.ID, practice.MarkerZeroFee, "")
	f.now = time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC) // due 02-09 + 15 grace = 02-24; 20 days late
	if od, _ := f.svc.Overdue(f.ctx, f.staff); len(od) != 1 {
		t.Fatal("overdue list")
	}
	restore := overrideSettings(f.svc, settingOverride{"billing.late_interest_enabled": "true", "billing.late_interest_annual_percent": "12"})
	defer restore()
	got, err := f.svc.ApplyLateInterest(f.ctx, f.attorney, inv.ID)
	// 900.00 * 12% * 20 / 365 = 5.9178 -> 5.92
	if err != nil || got.InterestCents != 5_92 || got.BalanceDueCents != 905_92 {
		t.Fatalf("interest: %v %+v", err, got.Invoice)
	}
	if again, _ := f.svc.ApplyLateInterest(f.ctx, f.attorney, inv.ID); again.InterestCents != 5_92 {
		t.Fatal("interest rerun must not compound")
	}
}

func TestPDFStructure(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Smith (PDF)", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-06-01", 2, nil)
	inv := f.sent(m.ID)
	var logo bytes.Buffer
	if err := jpeg.Encode(&logo, image.NewRGBA(image.Rect(0, 0, 32, 16)), nil); err != nil {
		t.Fatal(err)
	}
	f.svc.d.Logo = func(context.Context, domain.ID) ([]byte, error) { return logo.Bytes(), nil }
	restore := overrideSettings(f.svc, settingOverride{"firm.name": "Reed & Moss LLP", "firm.logo_file_id": string(f.id())})
	defer restore()
	pdf, _, err := f.svc.InvoicePDF(f.ctx, f.staff, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"%PDF-1.4", inv.Number, "Reed & Moss LLP", "Smith \\(PDF\\)", "/DCTDecode", "/Width 32 /Height 16", "%%EOF"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Fatalf("pdf lacks %q", want)
		}
	}
	checkXref(t, pdf)
}

// checkXref verifies every xref offset points at its object header.
func checkXref(t *testing.T, pdf []byte) {
	t.Helper()
	i := bytes.LastIndex(pdf, []byte("startxref\n"))
	var at int
	if _, err := fmtSscan(string(pdf[i+10:]), &at); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(pdf[at:]), "\n")
	var count int
	fmtSscan(strings.Fields(lines[1])[1], &count)
	for n := 1; n < count; n++ {
		var off int
		fmtSscan(strings.Fields(lines[2+n])[0], &off)
		if !strings.HasPrefix(string(pdf[off:]), itoa(n)+" 0 obj") {
			t.Fatalf("xref %d points at %q", n, pdf[off:off+12])
		}
	}
}
