package billing

import (
	"context"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/trust"
)

// Checkpoint 8 (AC-026, PRD 8.15): billing's flat-fee checks read trust's record before
// trust's lock is held. A partial apply on the trust page that commits between those checks
// and billing's trust call must not let the fee leave trust past its amount.

// racingTrust commits a trust-page apply just before forwarding billing's ApplyFlatFee, in
// its own transaction, the way a second user on the trust page would.
type racingTrust struct {
	Trust
	race func()
}

func (r *racingTrust) ApplyFlatFee(ctx context.Context, p domain.Principal, in FlatFeeApplication) (TrustPosted, error) {
	if r.race != nil {
		race := r.race
		r.race = nil
		race()
	}
	return r.Trust.ApplyFlatFee(ctx, p, in)
}

// raceTrustPage installs racingTrust so the next billing apply is preceded by a committed
// trust-page apply of cents. The event hook is unbound for that commit (billing holds its
// write lock, as a delayed or lost event would leave it), then bound again.
func (r *realTrust) raceTrustPage(matter, client domain.ID, cents int64) {
	r.t.Helper()
	orig := r.svc.d.Trust
	r.svc.d.Trust = &racingTrust{Trust: orig, race: func() {
		r.hook.Bind(nil)
		defer r.hook.Bind(r.svc)
		if _, err := r.trustApply(matter, client, cents); err != nil {
			r.t.Errorf("trust-page apply: %v", err)
		}
	}}
	r.t.Cleanup(func() { r.svc.d.Trust = orig })
}

func (r *realTrust) afterRace(m domain.Matter, c domain.ID, inv Invoice, rows int) {
	r.t.Helper()
	if st := r.trustFee(m.ID); st.CollectedCents != 300_00 || st.Status == trust.FeeCollected {
		r.t.Fatalf("trust took more than the trust-page part: %+v", st)
	}
	if r.balance(m.ID, c) != 700_00 {
		r.t.Fatalf("client trust after refused apply: %d", r.balance(m.ID, c))
	}
	if r.ledgerRows() != rows+1 {
		r.t.Fatalf("ledger rows: %d, want only the trust-page apply after %d", r.ledgerRows(), rows)
	}
	pays, err := r.svc.Payments(r.ctx, r.accountant, PaymentFilter{InvoiceID: inv.ID})
	if err != nil || len(pays) != 0 {
		r.t.Fatalf("a refused apply left billing payments: %+v %v", pays, err)
	}
	// The trust-page part is then credited, and the rest applies from billing once.
	if err := r.svc.SyncFlatFee(r.ctx, m.ID); err != nil {
		r.t.Fatal(err)
	}
	if got := r.inv(inv.ID); got.BalanceDueCents != 200_00 {
		r.t.Fatalf("after sync the invoice owes %s, want 200.00", money(got.BalanceDueCents))
	}
	if _, _, err := r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 200_00); err != nil {
		r.t.Fatal(err)
	}
	if got := r.inv(inv.ID); got.Status != StatusPaid || got.BalanceDueCents != 0 {
		r.t.Fatalf("invoice: %s %s", got.Status, money(got.BalanceDueCents))
	}
	if st := r.trustFee(m.ID); st.CollectedCents != 500_00 || st.Status != trust.FeeCollected {
		r.t.Fatalf("trust: %+v", st)
	}
	if r.balance(m.ID, c) != 500_00 {
		r.t.Fatalf("the fee left trust %d past its amount", 500_00-r.balance(m.ID, c))
	}
}

func TestRealTrustRaceApplyTrustRefusesFeePastAmount(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Race apply-trust will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	inv := r.sent(m.ID)
	rows := r.ledgerRows()
	r.raceTrustPage(m.ID, c, 300_00)
	wantCode(t, mustErr2(r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 500_00)), domain.CodeConflict)
	r.afterRace(m, c, inv, rows)
}

func TestRealTrustRaceApplyFlatFeeRefusesFeePastAmount(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Race flat-fee will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	inv := r.sent(m.ID)
	rows := r.ledgerRows()
	r.raceTrustPage(m.ID, c, 300_00)
	wantCode(t, mustErr2(r.svc.ApplyFlatFee(r.ctx, r.attorney, m.ID, c, r.account, 500_00)), domain.CodeConflict)
	r.afterRace(m, c, inv, rows)
}

// A race whose total stays within the fee still fails closed, because billing's decision was
// made on a stale record; nothing posts and the retry settles the rest exactly.
func TestRealTrustRacePartialWithinFeeFailsClosed(t *testing.T) {
	r := realTrustFx(t)
	m, c := r.matter("Race partial will", "flat", 500_00, 0)
	r.deposit(m.ID, c, 1000_00)
	inv := r.sent(m.ID)
	rows := r.ledgerRows()
	r.raceTrustPage(m.ID, c, 300_00)
	wantCode(t, mustErr2(r.svc.ApplyTrust(r.ctx, r.attorney, inv.ID, c, r.account, 100_00)), domain.CodeConflict)
	r.afterRace(m, c, inv, rows)
}
