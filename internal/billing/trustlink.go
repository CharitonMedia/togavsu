package billing

import (
	"context"
	"database/sql"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/trust"
)

// TrustLink adapts the trust service to billing's Trust seam. Pool must be the pool the trust
// service and billing's Postgres store write through: a trust write made inside billing's
// transaction joins it, and its after-commit effects (balance sink push for the close warning
// and At a Glance, commit events) are left for Committed, which billing calls after its own
// commit. A write made outside a transaction on Pool commits and runs those effects itself.
type TrustLink struct {
	Svc  *trust.Service
	Pool *sql.DB
}

// NewTrustLink returns the adapter.
func NewTrustLink(svc *trust.Service, pool *sql.DB) (*TrustLink, error) {
	if svc == nil || pool == nil {
		return nil, errors.New("billing: trust link needs the trust service and its pool")
	}
	return &TrustLink{Svc: svc, Pool: pool}, nil
}

var _ Trust = (*TrustLink)(nil)

// joined reports whether ctx carries a transaction on the trust pool, so trust leaves its
// after-commit effects to the caller.
func (l *TrustLink) joined(ctx context.Context) bool { return audit.Tx(ctx, l.Pool) != nil }

// Balances lists the matter's per-client, per-account balances. Billing has authorized the
// caller on the matter before it asks; the read runs as the system so a staff member who may
// see the matter's billing gets the same funded-account choice as an attorney.
func (l *TrustLink) Balances(ctx context.Context, matter domain.ID) ([]TrustBalance, error) {
	rows, err := l.Svc.Balances(ctx, domain.SystemPrincipal(), trust.EntryFilter{MatterID: matter})
	if err != nil {
		return nil, err
	}
	var out []TrustBalance
	for _, r := range rows {
		if r.Scope != trust.ScopeMatter || r.MatterID != matter {
			continue
		}
		out = append(out, TrustBalance{ClientID: r.ClientID, AccountID: r.TrustAccountID, BalanceCents: domain.Cents(r.BalanceCents)})
	}
	return out, nil
}

// FlatFeeCollected is what the matter's flat-fee applications cover, read from the record
// trust's own Apply flat fee keeps. A matter that is not billed flat has none.
func (l *TrustLink) FlatFeeCollected(ctx context.Context, matter domain.ID) (domain.Cents, error) {
	st, err := l.Svc.FlatFeeStatus(ctx, domain.SystemPrincipal(), matter)
	if err != nil {
		return 0, err
	}
	return domain.Cents(st.CollectedCents), nil
}

func (l *TrustLink) posted(joined bool, res trust.PostResult) TrustPosted {
	return TrustPosted{EntryID: res.Entry.ID, Result: res, Pending: joined, Effects: res}
}

// Post appends one ledger entry as the caller. A below-zero balance posts and flags.
func (l *TrustLink) Post(ctx context.Context, p domain.Principal, in TrustPosting) (TrustPosted, error) {
	desc := in.Description
	if !in.InvoiceID.IsZero() {
		desc += " (invoice " + string(in.InvoiceID) + ")"
	}
	joined := l.joined(ctx)
	res, err := l.Svc.Post(ctx, p, trust.PostInput{TrustAccountID: in.AccountID, ClientID: in.ClientID, MatterID: in.MatterID,
		EntryType: in.EntryType, AmountCents: int64(in.AmountCents), Method: in.Method, Description: desc}, "")
	if err != nil {
		return TrustPosted{}, err
	}
	return l.posted(joined, res), nil
}

// ApplyFlatFee applies the fee through trust's ApplyFlatFee, so the application is the one
// trust's page and FlatFeeStatus read, and a second apply from either page is refused.
func (l *TrustLink) ApplyFlatFee(ctx context.Context, p domain.Principal, in FlatFeeApplication) (TrustPosted, error) {
	amount := int64(in.AmountCents)
	joined := l.joined(ctx)
	res, err := l.Svc.ApplyFlatFee(ctx, p, trust.ApplyFlatFeeInput{MatterID: in.MatterID, ClientID: in.ClientID, TrustAccountID: in.AccountID, AmountCents: &amount}, "")
	if err != nil {
		return TrustPosted{}, err
	}
	return l.posted(joined, res), nil
}

// Reverse appends the compensating entry; the original row never changes.
func (l *TrustLink) Reverse(ctx context.Context, p domain.Principal, entry domain.ID, reason string) (TrustPosted, error) {
	joined := l.joined(ctx)
	res, err := l.Svc.Reverse(ctx, p, entry, trust.ReverseInput{Reason: reason}, "")
	if err != nil {
		return TrustPosted{}, err
	}
	return l.posted(joined, res), nil
}

// RequestReplenishment opens a replenishment request linked to the invoice. Inside billing's
// transaction trust leaves its commit event to Committed.
func (l *TrustLink) RequestReplenishment(ctx context.Context, p domain.Principal, in ReplenishmentInput) (TrustPosted, error) {
	amount := int64(in.AmountCents)
	joined := l.joined(ctx)
	r, err := l.Svc.CreateReplenishment(ctx, p, trust.ReplenishmentInput{MatterID: in.MatterID, ClientID: in.ClientID, AmountCents: &amount,
		InvoiceID: in.InvoiceID, Message: in.Message}, "")
	if err != nil {
		return TrustPosted{}, err
	}
	ev := trust.PostResult{Events: []trust.Event{{Name: "trust.replenishment_requested", RecordID: r.ID, MatterID: r.MatterID}}}
	return TrustPosted{EntryID: r.ID, Result: r, Pending: joined, Effects: ev}, nil
}

// Committed runs the after-commit effects of results that joined billing's transaction.
func (l *TrustLink) Committed(ctx context.Context, posted ...TrustPosted) {
	var results []trust.PostResult
	for _, p := range posted {
		if r, ok := p.Effects.(trust.PostResult); ok && p.Pending {
			results = append(results, r)
		}
	}
	if len(results) > 0 {
		l.Svc.Committed(ctx, results...)
	}
}
