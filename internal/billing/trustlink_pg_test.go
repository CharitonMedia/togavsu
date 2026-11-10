package billing

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/internal/trust"
)

// trustRec records the real trust service's commit events and balance-sink pushes.
type trustRec struct {
	mu     sync.Mutex
	events []trust.Event
	sunk   []domain.TrustBalance
}

func (r *trustRec) Publish(_ context.Context, ev []trust.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev...)
	return nil
}

func (r *trustRec) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Name == name {
			n++
		}
	}
	return n
}

func (r *trustRec) lastSunk(matter domain.ID) (domain.TrustBalance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.sunk) - 1; i >= 0; i-- {
		if r.sunk[i].MatterID == matter {
			return r.sunk[i], true
		}
	}
	return domain.TrustBalance{}, false
}

type realTrust struct {
	*fx
	pool *sql.DB
	tr   *trust.Service
	rec  *trustRec
}

// realTrustFx runs billing on Postgres with the merged trust package behind TrustLink, both
// on one pool, so billing's writes and trust's ledger rows share one transaction.
func realTrustFx(t *testing.T) *realTrust {
	mig, err := trust.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	pool := billingDBWith(t, mig...)
	f := newFxStore(t, NewPostgres(pool))
	aud, err := audit.New(audit.NewPostgres(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(settings.NewMemory(), aud)
	if err != nil {
		t.Fatal(err)
	}
	ids := identity.NewMemory()
	for _, p := range []domain.Principal{f.admin, f.attorney, f.staff, f.staff2, f.accountant} {
		u, err := f.pm.User(f.ctx, p.UserID)
		if err != nil {
			t.Fatal(err)
		}
		u.Email = string(p.UserID) + "@example.test"
		if err := ids.Users().Insert(f.ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	rec := &trustRec{}
	tr, err := trust.New(trust.Deps{DB: pool, Audit: aud, Settings: reg, Practice: f.pm, Users: ids.Users(), Walls: f.ps.Policy.Walls, Events: rec,
		BalanceSink: func(_ context.Context, row domain.TrustBalance) error {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.sunk = append(rec.sunk, row)
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	acct, err := tr.CreateAccount(f.ctx, f.admin, trust.AccountInput{Bank: "Synthetic Bank", Name: "IOLTA", IOLTA: true, LastFour: "4242"}, "")
	if err != nil {
		t.Fatal(err)
	}
	f.account = acct.ID
	link, err := NewTrustLink(tr, pool)
	if err != nil {
		t.Fatal(err)
	}
	f.link = link
	f.newService(audit.NewPostgres(pool))
	return &realTrust{fx: f, pool: pool, tr: tr, rec: rec}
}

func (r *realTrust) ledgerRows() int {
	r.t.Helper()
	var n int
	if err := r.pool.QueryRow(`SELECT count(*) FROM trust_ledger`).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *realTrust) flatStatus(matter domain.ID) FlatRow {
	r.t.Helper()
	wl, err := r.svc.Worklist(r.ctx, r.attorney)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, row := range wl.Flat {
		if row.MatterID == matter {
			return row
		}
	}
	r.t.Fatalf("matter %s not on the worklist", matter)
	return FlatRow{}
}

func (r *realTrust) trustFee(matter domain.ID) trust.FlatFee {
	r.t.Helper()
	st, err := r.tr.FlatFeeStatus(r.ctx, r.attorney, matter)
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

// AC-026 against the merged trust package: billing's Apply flat fee and trust's own Apply flat
// fee read and write one record, so the fee leaves trust once whichever page applies it first.
func TestRealTrustFlatFeeAppliesOnceAcrossPages(t *testing.T) {
	r := realTrustFx(t)

	// Trust page first: billing's worklist shows Collected and billing refuses a second apply.
	m1, c1 := r.matter("Trust-first will", "flat", 500_00, 0)
	r.deposit(m1.ID, c1, 800_00)
	if got := r.flatStatus(m1.ID); got.Status != FlatReady || got.TrustCents != 800_00 {
		t.Fatalf("before apply: %+v", got)
	}
	fee := int64(500_00)
	if _, err := r.tr.ApplyFlatFee(r.ctx, r.attorney, trust.ApplyFlatFeeInput{MatterID: m1.ID, ClientID: c1, TrustAccountID: r.account, AmountCents: &fee}, ""); err != nil {
		t.Fatal(err)
	}
	if got := r.flatStatus(m1.ID); got.Status != FlatCollected || got.AppliedCents != 500_00 || got.TrustCents != 300_00 {
		t.Fatalf("billing worklist after trust apply: %+v", got)
	}
	rows := r.ledgerRows()
	wantCode(t, mustErr2(r.svc.ApplyFlatFee(r.ctx, r.attorney, m1.ID, c1, r.account, 500_00)), domain.CodeConflict)
	if r.ledgerRows() != rows {
		t.Fatal("billing took the flat fee from trust a second time")
	}
	// Generate does not invoice the client for a fee already taken from trust.
	drafts, err := r.svc.DraftInvoices(r.ctx, r.staff, DraftRequest{MatterIDs: []domain.ID{m1.ID}})
	if len(drafts) != 0 {
		t.Fatalf("a fee applied from the trust page was drafted: %+v", drafts)
	}
	wantCode(t, err, domain.CodeConflict)

	// Billing first: trust's status shows Collected and trust refuses a second apply.
	m2, c2 := r.matter("Billing-first will", "flat", 400_00, 0)
	r.deposit(m2.ID, c2, 900_00)
	posted, inv, err := r.svc.ApplyFlatFee(r.ctx, r.attorney, m2.ID, c2, r.account, 400_00)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Status != StatusPaid || posted.EntryID.IsZero() {
		t.Fatalf("billing apply: %+v %s", posted, inv.Status)
	}
	if st := r.trustFee(m2.ID); st.Status != trust.FeeCollected || st.CollectedCents != 400_00 || st.TrustCents != 500_00 {
		t.Fatalf("trust status after billing apply: %+v", st)
	}
	rows = r.ledgerRows()
	fee = 400_00
	if _, err := r.tr.ApplyFlatFee(r.ctx, r.attorney, trust.ApplyFlatFeeInput{MatterID: m2.ID, ClientID: c2, TrustAccountID: r.account, AmountCents: &fee}, ""); err == nil {
		t.Fatal("trust applied a fee billing already collected")
	}
	wantCode(t, mustErr2(r.svc.ApplyFlatFee(r.ctx, r.attorney, m2.ID, c2, r.account, 400_00)), domain.CodeConflict)
	if r.ledgerRows() != rows {
		t.Fatal("a refused apply posted to trust")
	}
	bal, err := r.tr.MatterBalance(r.ctx, r.attorney, m2.ID, c2)
	if err != nil || bal != 500_00 {
		t.Fatalf("client trust after one fee: %d %v", bal, err)
	}

	// Reversing billing's payment reverses the trust entry, so both pages show the fee ready again.
	pays, err := r.svc.Payments(r.ctx, r.accountant, PaymentFilter{InvoiceID: inv.ID})
	if err != nil || len(pays) != 1 || pays[0].TrustEntryID != posted.EntryID {
		t.Fatalf("payment: %+v %v", pays, err)
	}
	if _, err := r.svc.ReversePayment(r.ctx, r.attorney, pays[0].ID, "applied in error"); err != nil {
		t.Fatal(err)
	}
	if st := r.trustFee(m2.ID); st.Status != trust.FeeReadyToApply || st.CollectedCents != 0 {
		t.Fatalf("trust status after reversal: %+v", st)
	}
	if bal, _ := r.tr.MatterBalance(r.ctx, r.attorney, m2.ID, c2); bal != 900_00 {
		t.Fatalf("trust after reversal: %d", bal)
	}
}

// A part of the fee applied from the trust page is not applied again in full from billing.
func TestRealTrustPartialTrustApplyRefusedInBilling(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Part-applied will", "flat", 600_00, 0)
	r.deposit(m.ID, c, 1000_00)
	part := int64(200_00)
	if _, err := r.tr.ApplyFlatFee(r.ctx, r.attorney, trust.ApplyFlatFeeInput{MatterID: m.ID, ClientID: c, TrustAccountID: r.account, AmountCents: &part}, ""); err != nil {
		t.Fatal(err)
	}
	if got := r.flatStatus(m.ID); got.AppliedCents != 200_00 || got.Status == FlatCollected {
		t.Fatalf("worklist: %+v", got)
	}
	rows := r.ledgerRows()
	wantCode(t, mustErr2(r.svc.ApplyFlatFee(r.ctx, r.attorney, m.ID, c, r.account, 600_00)), domain.CodeConflict)
	if r.ledgerRows() != rows {
		t.Fatal("billing posted after a partial trust apply")
	}
}

// postThenFail posts through the real trust service and then fails, as a later step of the
// billing write would.
type postThenFail struct{ Trust }

func (p postThenFail) Post(ctx context.Context, who domain.Principal, in TrustPosting) (TrustPosted, error) {
	if _, err := p.Trust.Post(ctx, who, in); err != nil {
		return TrustPosted{}, err
	}
	return TrustPosted{}, errors.New("synthetic failure after the trust post")
}

// Trust postings made through billing join billing's transaction; their balance-sink push
// and commit events run after billing commits (Committed), and never after a rollback.
func TestRealTrustEffectsRunAfterBillingCommit(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Effects estate", "hourly", 0, 0)

	// Deposit payment: the practice copy gets the new balance and the event is published.
	r.deposit(m.ID, c, 700_00)
	if row, ok := r.rec.lastSunk(m.ID); !ok || row.BalanceCents != 700_00 || row.ClientID != c {
		t.Fatalf("balance sink after deposit: %+v %v", row, ok)
	}
	if n := r.rec.count("trust.entry_posted"); n != 1 {
		t.Fatalf("entry_posted events after deposit: %d", n)
	}

	// Apply trust to an invoice.
	r.logTime(r.attorney, m.ID, "2026-06-01", 4, nil)
	inv := r.sent(m.ID)
	if _, _, err := r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 100_00); err != nil {
		t.Fatal(err)
	}
	if row, _ := r.rec.lastSunk(m.ID); row.BalanceCents != 600_00 {
		t.Fatalf("balance sink after apply trust: %+v", row)
	}
	if n := r.rec.count("trust.entry_posted"); n != 2 {
		t.Fatalf("entry_posted events after apply trust: %d", n)
	}

	// Reversal of the trust-linked payment.
	pays, err := r.svc.Payments(r.ctx, r.accountant, PaymentFilter{InvoiceID: inv.ID})
	if err != nil || len(pays) != 1 {
		t.Fatalf("payments: %+v %v", pays, err)
	}
	if _, err := r.svc.ReversePayment(r.ctx, r.attorney, pays[0].ID, "wrong invoice"); err != nil {
		t.Fatal(err)
	}
	if row, _ := r.rec.lastSunk(m.ID); row.BalanceCents != 700_00 {
		t.Fatalf("balance sink after reversal: %+v", row)
	}
	if n := r.rec.count("trust.entry_posted"); n != 3 {
		t.Fatalf("entry_posted events after reversal: %d", n)
	}

	// Replenishment from the collections panel publishes after commit.
	if _, err := r.svc.RequestReplenishment(r.ctx, r.attorney, inv.ID, c, 250_00, ""); err != nil {
		t.Fatal(err)
	}
	if n := r.rec.count("trust.replenishment_requested"); n != 1 {
		t.Fatalf("replenishment events: %d", n)
	}

	// A billing write that fails after its trust post rolls the ledger row back with it and
	// runs no trust effects.
	rows, sunk, events := r.ledgerRows(), len(r.rec.sunk), len(r.rec.events)
	r.svc.d.Trust = postThenFail{r.link}
	if _, err := r.svc.CreatePayment(r.ctx, r.staff, PaymentInput{ContactID: c, MatterID: m.ID, AmountCents: 50_00, Destination: DestTrust, TrustAccountID: r.account, Method: "cash"}); err == nil {
		t.Fatal("expected failure")
	}
	r.svc.d.Trust = r.link
	if r.ledgerRows() != rows || len(r.rec.sunk) != sunk || len(r.rec.events) != events {
		t.Fatalf("rolled-back write left trust effects: rows %d->%d sunk %d->%d events %d->%d", rows, r.ledgerRows(), sunk, len(r.rec.sunk), events, len(r.rec.events))
	}
	if bal, _ := r.tr.MatterBalance(r.ctx, r.attorney, m.ID, c); bal != 700_00 {
		t.Fatalf("trust balance after rollback: %d", bal)
	}
	if err := r.tr.VerifyBalances(r.ctx); err != nil {
		t.Fatal(err)
	}
}
