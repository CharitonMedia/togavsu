package trust

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Reconcile stores one reconciliation: the statement balance the caller entered, the
// ledger total for the account through the statement date, the sum of the client
// balances in that account on that date, and both differences (PRD 8.4). Reconciliation
// sees every matter: it is about the bank account, so a matter wall does not hide rows
// from the totals. Staff, attorneys and admins may reconcile.
func (s *Service) Reconcile(ctx context.Context, who domain.Principal, in ReconcileInput, key string) (Reconciliation, error) {
	out, err := s.ReconcileBulk(ctx, who, []ReconcileInput{in}, key)
	if err != nil {
		return Reconciliation{}, unwrapItem(err)
	}
	return out[0], nil
}

// unwrapItem turns a one-item batch error back into a plain field error.
func unwrapItem(err error) error {
	api, ok := err.(*domain.APIError)
	if !ok {
		return err
	}
	field, cut := strings.CutPrefix(api.Field, "items[0].")
	if !cut {
		field, cut = strings.CutPrefix(api.Field, "items[0]")
	}
	if !cut {
		return err
	}
	c := *api
	c.Field = field
	return &c
}

// ReconcileBulk validates every item, then stores them in one transaction.
func (s *Service) ReconcileBulk(ctx context.Context, who domain.Principal, items []ReconcileInput, key string) ([]Reconciliation, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("items", "at least one item is required")
	}
	if err := s.checkBulk(ctx, len(items)); err != nil {
		return nil, err
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionTrustDeposit, ""); err != nil {
		return nil, err
	}
	for i, in := range items {
		if !validDate(in.StatementOn) {
			return nil, itemErr(i, domain.Invalid("statement_on", "the statement date is required as YYYY-MM-DD"))
		}
		if in.StatementBalanceCents == nil {
			return nil, itemErr(i, domain.Invalid("statement_balance_cents", "the statement balance is required"))
		}
	}
	var out []Reconciliation
	var events []Event
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		if err := s.lock(ctx); err != nil {
			return err
		}
		var err error
		out, _, err = idem(ctx, s, who, "trust.reconcile", key, items, func() ([]Reconciliation, error) {
			var res []Reconciliation
			for i, in := range items {
				if _, err := s.account(ctx, in.TrustAccountID); err != nil {
					return nil, itemErr(i, err)
				}
				r, err := s.reconcileOne(ctx, who, in)
				if err != nil {
					return nil, itemErr(i, err)
				}
				events = append(events, Event{Name: "trust.reconciled", RecordID: r.ID, Data: map[string]string{
					"trust_account_id": string(r.TrustAccountID), "statement_difference_cents": domain.Cents(r.StatementDifferenceCents).String(),
				}})
				res = append(res, r)
			}
			return res, nil
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish(ctx, joined, events)
	return out, nil
}

func (s *Service) reconcileOne(ctx context.Context, who domain.Principal, in ReconcileInput) (Reconciliation, error) {
	end := dayEnd(in.StatementOn)
	ledger, err := s.sum(ctx, `trust_account_id = $1 AND created_at < $2`, string(in.TrustAccountID), end)
	if err != nil {
		return Reconciliation{}, err
	}
	// The client side: each client's balance in this account on the statement date,
	// summed client by client.
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT client_id, SUM(amount_cents) FROM trust_ledger WHERE trust_account_id = $1 AND created_at < $2 GROUP BY client_id`,
		string(in.TrustAccountID), end)
	if err != nil {
		return Reconciliation{}, err
	}
	var clients int64
	for rows.Next() {
		var id string
		var v int64
		if err := rows.Scan(&id, &v); err != nil {
			rows.Close()
			return Reconciliation{}, err
		}
		clients += v
	}
	if err := rows.Close(); err != nil {
		return Reconciliation{}, err
	}
	id, err := s.newID()
	if err != nil {
		return Reconciliation{}, err
	}
	now := s.now()
	r := Reconciliation{
		ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actorID(who), TrustAccountID: in.TrustAccountID, StatementOn: in.StatementOn,
		StatementBalanceCents: *in.StatementBalanceCents, LedgerTotalCents: ledger, ClientBalancesSumCents: clients,
		StatementDifferenceCents: *in.StatementBalanceCents - ledger, ClientSumDifferenceCents: ledger - clients,
	}
	if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO trust_reconciliations (id, created_at, updated_at, created_by, trust_account_id, statement_on, statement_balance_cents, ledger_total_cents, client_balance_cents, difference_cents)
		VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8,$9)`,
		string(r.ID), micro(now), string(r.CreatedBy), string(r.TrustAccountID), r.StatementOn, r.StatementBalanceCents, r.LedgerTotalCents, r.ClientBalancesSumCents, r.StatementDifferenceCents); err != nil {
		return Reconciliation{}, err
	}
	return r, s.record(ctx, who, "trust_reconciliation", r.ID, "difference_cents", "", domain.Cents(r.StatementDifferenceCents).String(),
		fmt.Sprintf("statement %s on %s; ledger %s; clients %s", domain.Cents(r.StatementBalanceCents), r.StatementOn, domain.Cents(ledger), domain.Cents(clients)))
}

// Reconciliation returns one stored reconciliation.
func (s *Service) Reconciliation(ctx context.Context, who domain.Principal, id domain.ID) (Reconciliation, error) {
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, ""); err != nil {
		return Reconciliation{}, err
	}
	return s.reconciliation(ctx, id)
}

// Reconciliations lists stored reconciliations, oldest first.
func (s *Service) Reconciliations(ctx context.Context, who domain.Principal, account domain.ID, cursor string, limit int) (Page[Reconciliation], error) {
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, ""); err != nil {
		return Page[Reconciliation]{}, err
	}
	limit, err := s.pageLimit(ctx, limit)
	if err != nil {
		return Page[Reconciliation]{}, err
	}
	all, err := s.reconciliations(ctx, account)
	if err != nil {
		return Page[Reconciliation]{}, err
	}
	return paginate(all, func(r Reconciliation) domain.ID { return r.ID }, cursor, limit), nil
}

// RemindReconciliations notifies the admins once per account per month when, on or after
// the trust.reconciliation_reminder_day, last month has no reconciliation. The job runner
// calls it daily. It returns how many reminders it sent.
func (s *Service) RemindReconciliations(ctx context.Context, now time.Time) (int, error) {
	day, err := s.intSetting(ctx, "trust.reconciliation_reminder_day")
	if err != nil {
		return 0, err
	}
	now = now.UTC()
	if now.Day() < day {
		return 0, nil
	}
	lastMonth := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	sys := domain.SystemPrincipal()
	admins, err := s.flagRecipients(ctx, domain.Matter{})
	if err != nil {
		return 0, err
	}
	var sent int
	_, err = s.tx(ctx, func(ctx context.Context) error {
		accts, err := s.accounts(ctx)
		if err != nil {
			return err
		}
		recs, err := s.reconciliations(ctx, "")
		if err != nil {
			return err
		}
		for _, a := range accts {
			if !a.Active {
				continue
			}
			done := false
			for _, r := range recs {
				// Dates are YYYY-MM-DD, so text order is date order.
				if r.TrustAccountID == a.ID && r.StatementOn >= lastMonth {
					done = true
				}
			}
			if done {
				continue
			}
			link := "/trust/reconciliation?trust_account_id=" + string(a.ID) + "&month=" + lastMonth
			var reminded bool
			if err := s.q(ctx).QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM notifications WHERE kind = $1 AND link = $2)`, NotifyReconcile, link).Scan(&reminded); err != nil {
				return err
			}
			if reminded {
				continue
			}
			for _, u := range admins {
				if err := s.notify(ctx, sys, u.user.ID, NotifyReconcile, fmt.Sprintf("Reconcile %s ****%s for %s", a.Name, a.LastFour, lastMonth), link, ""); err != nil {
					return err
				}
				sent++
			}
		}
		return nil
	})
	return sent, err
}
