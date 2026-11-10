package billing

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// Regression tests for the Codex checkpoint findings on PR #27.

func (f *fx) clientUser(contact domain.ID) domain.Principal {
	f.t.Helper()
	cl := person(f.id(), domain.RoleClient)
	enc, _ := domain.MarshalRoles(cl.Roles)
	if err := f.pm.PutUser(f.ctx, domain.User{Meta: domain.Meta{ID: cl.UserID}, Name: "Cora", Roles: enc, Active: true, ContactID: contact}); err != nil {
		f.t.Fatal(err)
	}
	return cl
}

// Finding 1: a client must not get the staff edit history through the service, the API or the page.
func TestClientRefusedInvoiceHistory(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, contact := f.matter("History wall", "hourly", 0, 0)
	cl := f.clientUser(contact)
	a.who[cl.UserID] = cl
	f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil)
	inv := f.sent(m.ID)
	if _, err := f.svc.UpdateInvoice(f.ctx, f.staff, inv.ID, InvoicePatch{InternalNotes: strPtr("slow payer"), DisputeNotes: strPtr("argues rate")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.WriteOff(f.ctx, f.attorney, inv.ID, 1_00, "bad debt hunch"); err != nil {
		t.Fatal(err)
	}
	secrets := []string{"slow payer", "argues rate", "bad debt hunch", "internal_notes", "approved_by"}

	// Service: refused for every entity, including the client's own sent invoice.
	wantCode(t, mustErr(f.svc.History(f.ctx, cl, "invoice", inv.ID)), domain.CodeForbidden)
	wantCode(t, mustErr(f.svc.History(f.ctx, cl, "time_entry", inv.Lines[0].SourceID)), domain.CodeForbidden)
	// Staff still read it.
	if hist, err := f.svc.History(f.ctx, f.staff, "invoice", inv.ID); err != nil || len(hist) == 0 {
		t.Fatalf("staff history: %v %d", err, len(hist))
	}

	// API.
	code, body, _ := a.do(cl, "GET", "/api/v1/invoices/"+string(inv.ID)+"/history", nil)
	if code != 403 {
		t.Fatalf("client history API: %d %s", code, body)
	}
	for _, s := range secrets {
		if strings.Contains(string(body), s) {
			t.Fatalf("history API leaked %q", s)
		}
	}
	if code, _, _ := a.do(f.staff, "GET", "/api/v1/invoices/"+string(inv.ID)+"/history", nil); code != 200 {
		t.Fatalf("staff history API: %d", code)
	}

	// Page: the client sees the allowlisted invoice, with no history card and no reasons.
	code, page := a.page(cl, "/billing/invoices/"+string(inv.ID))
	if code != 200 || !strings.Contains(page, inv.Number) {
		t.Fatalf("client page: %d", code)
	}
	for _, s := range append(secrets, "Edit history", "Void this invoice") {
		if strings.Contains(page, s) {
			t.Fatalf("client page leaked %q", s)
		}
	}
	_, page = a.page(f.attorney, "/billing/invoices/"+string(inv.ID))
	if !strings.Contains(page, "Edit history") || !strings.Contains(page, "bad debt hunch") {
		t.Fatal("staff page keeps the history and reasons")
	}
}

// Finding 3: each write-off keeps its own date, so reports split them correctly.
func TestWriteOffsKeepTheirDates(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Two write-offs", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2025-12-01", 40, nil) // 1000.00
	f.now = time.Date(2025, 12, 5, 12, 0, 0, 0, time.UTC)
	inv := f.sent(m.ID)
	f.now = time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	if _, err := f.svc.WriteOff(f.ctx, f.attorney, inv.ID, 50_00, "first"); err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	got, err := f.svc.WriteOff(f.ctx, f.attorney, inv.ID, 25_00, "second")
	if err != nil || got.WriteOffCents != 75_00 || len(got.WriteOffs) != 2 {
		t.Fatalf("write-offs: %v %+v", err, got.WriteOffs)
	}
	jan, err := f.svc.WriteOffsReport(f.ctx, f.accountant, ReportFilter{FromDate: "2026-01-01", ToDate: "2026-01-31"})
	if err != nil || len(jan.Rows) != 1 || jan.Rows[0]["amount_cents"] != int64(50_00) || jan.Rows[0]["written_off_on"] != "2026-01-05" || jan.Rows[0]["reason"] != "first" {
		t.Fatalf("January write-offs: %v %+v", err, jan.Rows)
	}
	all, _ := f.svc.WriteOffsReport(f.ctx, f.accountant, ReportFilter{FromDate: "2026-01-01", ToDate: "2026-12-31"})
	if len(all.Rows) != 2 {
		t.Fatalf("both write-offs: %+v", all.Rows)
	}
	balance := func(asOf string) any {
		r, err := f.svc.ARAgingReport(f.ctx, f.accountant, ReportFilter{AsOf: asOf})
		if err != nil || len(r.Rows) != 1 {
			t.Fatalf("aging %s: %v %+v", asOf, err, r.Rows)
		}
		return r.Rows[0]["balance_cents"]
	}
	if b := balance("2025-12-31"); b != int64(1000_00) {
		t.Fatalf("before both: %v", b)
	}
	if b := balance("2026-02-01"); b != int64(950_00) {
		t.Fatalf("between the write-offs: %v", b)
	}
	if b := balance("2026-03-05"); b != int64(925_00) {
		t.Fatalf("after both: %v", b)
	}
}

// Finding 4: a fee-catalog item on a flat matter's invoice is not the matter's flat fee.
func TestCatalogItemIsNotTheMatterFlatFee(t *testing.T) {
	f := newFx(t)
	m, client := f.matter("Catalog flat", "flat", 0, 0)
	name, amt := "Document review", domain.Cents(75_00)
	item, err := f.svc.CreateFeeItem(f.ctx, f.admin, FeeItemInput{Name: &name, DefaultAmountCents: &amt})
	if err != nil {
		t.Fatal(err)
	}
	f.expense(m.ID, 30_00, 0)
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil || len(invs) != 1 || invs[0].HasFlatFee() {
		t.Fatalf("expense draft at Needs fee amount: %v %+v", err, invs)
	}
	edited, err := f.svc.EditDraft(f.ctx, f.staff, invs[0].ID, DraftEdit{Lines: []LineInput{
		{Kind: LineExpense, SourceID: invs[0].Lines[0].SourceID}, {Kind: LineFlatFee, FeeItemID: item.ID}}})
	if err != nil || edited.HasFlatFee() {
		t.Fatalf("a catalog item must not mark the matter fee: %v %+v", err, edited.Lines)
	}
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, edited.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SendInvoice(f.ctx, f.attorney, edited.ID, "file"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: m.ID, InvoiceID: edited.ID,
		AmountCents: 105_00, Destination: DestOperating, Method: "check", ReferenceNumber: "77"}); err != nil {
		t.Fatal(err)
	}
	fee := domain.Cents(600_00)
	if _, err := f.ps.PatchMatter(f.ctx, f.attorney, m.ID, practice.MatterPatch{FlatFeeCents: &fee}); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		wl, err := f.svc.Worklist(f.ctx, f.attorney)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range wl.Flat {
			if r.MatterID == m.ID {
				return r.Status
			}
		}
		t.Fatal("matter missing from worklist")
		return ""
	}
	if s := status(); s != FlatAwaiting {
		t.Fatalf("a paid catalog-item invoice is not Collected: %s", s)
	}
	f.deposit(m.ID, client, 600_00)
	if s := status(); s != FlatReady {
		t.Fatalf("ready: %s", s)
	}
	if _, inv, err := f.svc.ApplyFlatFee(f.ctx, f.attorney, m.ID, client, f.account, 600_00); err != nil || !inv.HasFlatFee() || inv.ID == edited.ID {
		t.Fatalf("apply the fee on its own invoice: %v %+v", err, inv.Lines)
	}
	if s := status(); s != FlatCollected {
		t.Fatalf("collected: %s", s)
	}
	// The fee is billed once, so a later draft does not bill it again.
	f.expense(m.ID, 10_00, 0)
	again, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil || len(again) != 1 || again[0].HasFlatFee() || again[0].TotalCents != 10_00 {
		t.Fatalf("redraft: %v %+v", err, again)
	}
}

// Finding 4, edit path: editing the matter-fee line's text keeps it marked; removing it releases the fee.
func TestEditKeepsMatterFeeMark(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Edit flat", "flat", 400_00, 0)
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil || !invs[0].HasFlatFee() {
		t.Fatalf("draft: %v", err)
	}
	edited, err := f.svc.EditDraft(f.ctx, f.staff, invs[0].ID, DraftEdit{Lines: []LineInput{{Kind: LineFlatFee, Description: "Estate plan, flat", AmountCents: cents(400_00)}}})
	if err != nil || !edited.HasFlatFee() {
		t.Fatalf("edited fee line stays the matter fee: %v %+v", err, edited.Lines)
	}
	adj := domain.Cents(-1_00)
	cleared, err := f.svc.EditDraft(f.ctx, f.staff, invs[0].ID, DraftEdit{Lines: []LineInput{{Kind: LineAdjustment, Description: "Credit", AmountCents: &adj}}})
	if err != nil || cleared.HasFlatFee() {
		t.Fatalf("removed fee line: %v %+v", err, cleared.Lines)
	}
	if _, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID}); err != nil {
		t.Fatalf("the released fee drafts again: %v", err)
	}
}

// failStore fails every Put of one kind, to break a write after its rows are staged.
type failStore struct {
	Store
	kind string
}

func (s failStore) Put(ctx context.Context, kind, id string, v any) error {
	if kind == s.kind {
		return errors.New("disk full")
	}
	return s.Store.Put(ctx, kind, id, v)
}

// Finding 5: mail goes out only after the commit, so a failed commit emails nobody.
func TestSendMailsOnlyAfterCommit(t *testing.T) { sendAfterCommitCase(t, newFx(t)) }

func TestPostgresSendMailsOnlyAfterCommit(t *testing.T) {
	f, _ := pgFx(t)
	sendAfterCommitCase(t, f)
}

func sendAfterCommitCase(t *testing.T, f *fx) {
	m, _ := f.matter("Commit first", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil)
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	id := invs[0].ID
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, id); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	real := f.svc.d.Store
	f.svc.d.Store = failStore{Store: real, kind: kindEvent} // the event Put is the last step before commit
	if _, err := f.svc.SendInvoice(f.ctx, f.attorney, id, "email"); err == nil {
		t.Fatal("the failed commit must fail the send")
	}
	f.svc.d.Store = real
	if len(f.mail.sent) != 0 {
		t.Fatal("the client was emailed although the send did not commit")
	}
	if f.snapshot() != before {
		t.Fatal("a failed commit wrote rows")
	}
	inv, err := f.svc.SendInvoice(f.ctx, f.attorney, id, "email")
	if err != nil || inv.Status != StatusSent || len(f.mail.sent) != 1 || len(inv.SentTo) != 1 {
		t.Fatalf("retry sends once: %v %d %+v", err, len(f.mail.sent), inv.SentTo)
	}
	// A failed mail on a resend of a sent invoice keeps it sent and leaves no extra event.
	events := func() int { rows, _ := f.st.List(f.ctx, kindEvent); return len(rows) }
	n := events()
	f.mail.fail = errors.New("smtp down")
	if _, err := f.svc.SendInvoice(f.ctx, f.attorney, id, "email"); err == nil {
		t.Fatal("mail failure must fail the resend")
	}
	f.mail.fail = nil
	after, _ := f.svc.invoice(f.ctx, id)
	if after.Status != StatusSent || events() != n {
		t.Fatalf("resend failure: %s, events %d -> %d", after.Status, n, events())
	}
}

// Finding 6: a stopped timer saves a draft entry that is not invoiced until reviewed, and a stop
// with no active activity type is refused.
func TestTimerEntryNeedsReviewBeforeBilling(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Timer review", "hourly", 0, 0)
	tm, _, err := f.svc.StartTimer(f.ctx, f.staff, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(20 * time.Minute)
	off := false
	if _, err := f.svc.UpdateType(f.ctx, f.admin, false, f.activity, TypeInput{Active: &off}); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot()
	wantCode(t, mustErr2(f.svc.StopTimer(f.ctx, f.staff, tm.ID)), domain.CodeConflict)
	if f.snapshot() != before {
		t.Fatal("a refused stop wrote rows")
	}
	if running, _ := f.svc.Timer(f.ctx, f.staff, tm.ID); running.Status() != "running" {
		t.Fatal("the timer keeps running")
	}
	on := true
	if _, err := f.svc.UpdateType(f.ctx, f.admin, false, f.activity, TypeInput{Active: &on}); err != nil {
		t.Fatal(err)
	}
	_, entry, err := f.svc.StopTimer(f.ctx, f.staff, tm.ID)
	if err != nil || !entry.NeedsReview || entry.ActivityType != string(f.activity) || entry.Description != "" {
		t.Fatalf("stop: %v %+v", err, entry)
	}
	a := newAPI(t, f)
	c := loadContract(t)
	code, body, _ := a.do(f.staff, "GET", "/api/v1/time-entries/"+string(entry.ID), nil)
	if code != 200 {
		t.Fatalf("get entry: %d", code)
	}
	c.check(t, "TimeEntry", decodeMap(t, body))
	wantCode(t, mustErr(f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})), domain.CodeConflict)
	if wl, _ := f.svc.Worklist(f.ctx, f.staff); len(wl.Hourly) != 0 {
		t.Fatal("an unreviewed timer entry is not ready to bill")
	}
	// Saving it without a description does not review it.
	billable := true
	if e, err := f.svc.UpdateTime(f.ctx, f.staff, entry.ID, TimeInput{Billable: &billable}); err != nil || !e.NeedsReview {
		t.Fatalf("no description: %v %+v", err, e)
	}
	// The time page opens the draft for review and saves it.
	if _, page := a.page(f.staff, "/billing/time"); !strings.Contains(page, "/billing/time/"+string(entry.ID)+"/review") || !strings.Contains(page, "needs review") {
		t.Fatal("the time page must open the timer draft for review")
	}
	review := "/billing/time/" + string(entry.ID) + "/review"
	if code, _ := a.form(f.staff, review, url.Values{"activity_type_id": {string(f.activity)}, "description": {" "}}, true); code != 303 {
		t.Fatalf("blank review: %d", code)
	}
	if e, _ := f.svc.timeEntry(f.ctx, entry.ID); !e.NeedsReview {
		t.Fatal("a blank description is not a review")
	}
	if code, _ := a.form(f.accountant, review, url.Values{"activity_type_id": {string(f.activity)}, "description": {"x"}}, true); code == 303 {
		if e, _ := f.svc.timeEntry(f.ctx, entry.ID); !e.NeedsReview {
			t.Fatal("an accountant cannot review")
		}
	}
	code, _ = a.form(f.staff, review, url.Values{"activity_type_id": {string(f.activity)}, "description": {"Call with client about the inventory"}}, true)
	reviewed, err := f.svc.timeEntry(f.ctx, entry.ID)
	if code != 303 || err != nil || reviewed.NeedsReview || reviewed.Description != "Call with client about the inventory" {
		t.Fatalf("review: %d %v %+v", code, err, reviewed)
	}
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil || len(invs) != 1 || invs[0].Lines[0].SourceID != entry.ID {
		t.Fatalf("reviewed entry drafts: %v %+v", err, invs)
	}
}

// Finding 2: the AC-030 rewrite keeps the fixed rules' behaviour.
func TestFixedRulesAfterSettingsRewrite(t *testing.T) {
	for days, want := range map[int]string{-3: "current", 0: "current", 1: "1-30", 30: "1-30", 31: "31-60", 60: "31-60", 61: "61-90", 90: "61-90", 91: "91+", 4000: "91+"} {
		if got := agingBucket(days); got != want {
			t.Fatalf("agingBucket(%d) = %s, want %s", days, got, want)
		}
	}
	if exactlyOne() || exactlyOne(false, false) || !exactlyOne(false, true, false) || exactlyOne(true, true) || exactlyOne(true, false, true) {
		t.Fatal("exactlyOne")
	}
	if onlyOne(0) || !onlyOne(1) || onlyOne(2) {
		t.Fatal("onlyOne")
	}
	if addDays("2026-01-31", 30) != "2026-03-02" || addDays("2028-02-28", 1) != "2028-02-29" || addDays("bad", 3) != "bad" {
		t.Fatal("addDays")
	}
	if daysBetween("2026-02-09", "2026-04-01") != 51 || daysBetween("bad", "2026-04-01") != 0 || daysBetween("2026-04-01", "2026-03-31") != -1 {
		t.Fatal("daysBetween")
	}
	gray := new(bytes.Buffer)
	if err := jpeg.Encode(gray, image.NewGray(image.Rect(0, 0, 8, 4)), nil); err != nil {
		t.Fatal(err)
	}
	if info, err := parseJPEG(gray.Bytes()); err != nil || info.space != "DeviceGray" || info.width != 8 || info.height != 4 {
		t.Fatalf("gray jpeg: %v %+v", err, info)
	}
	if _, err := parseJPEG([]byte("\x89PNG\r\n\x1a\n")); err == nil {
		t.Fatal("a PNG is not drawn as a JPEG")
	}
	l, err := loadLayout()
	if err != nil || l.Top <= l.LinesBottom || l.Right <= l.Margin || l.WrapLines <= 0 || l.Line <= 0 {
		t.Fatalf("pdf layout: %v %+v", err, l)
	}
	if latin1("café\u0007(x)世") != "caf\xe9\\(x\\)?" {
		t.Fatalf("latin1: %q", latin1("café\u0007(x)世"))
	}
}

// CSV keeps large integer cents exact (JSON numbers are not rounded through float64).
func TestCSVKeepsExactCents(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, _ := f.matter("Big rate", "hourly", 0, 0)
	rate := domain.Cents(90_071_992_547_409_93) // above 2^53
	f.logTime(f.attorney, m.ID, "2026-06-01", 1, &rate)
	_, csv, _ := a.do(f.staff, "GET", "/api/v1/time-entries?format=csv", nil)
	if !strings.Contains(string(csv), "9007199254740993") {
		t.Fatalf("csv lost precision: %s", csv)
	}
}
