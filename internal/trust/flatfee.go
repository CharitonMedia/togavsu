package trust

import (
	"context"
	"fmt"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// FlatFeeStatus is the trust side of the Billing / Generate flat-fee row (PRD 8.15):
// Needs fee amount, Awaiting trust funds, Ready to apply or Collected. The fee is
// collected while applications whose ledger rows are not reversed cover it.
func (s *Service) FlatFeeStatus(ctx context.Context, who domain.Principal, matterID domain.ID) (FlatFee, error) {
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, matterID); err != nil {
		return FlatFee{}, err
	}
	m, err := s.practice.Matter(ctx, matterID)
	if err != nil {
		return FlatFee{}, notFound(err, "matter")
	}
	return s.flatFee(ctx, m, "")
}

func (s *Service) primaryClient(matter domain.ID) domain.ID {
	var first domain.ID
	for _, p := range s.practice.Parties(matter, "") {
		if p.Role != "client" || p.MatterID != matter {
			continue
		}
		if p.Primary {
			return p.ContactID
		}
		if first.IsZero() {
			first = p.ContactID
		}
	}
	return first
}

func (s *Service) flatFee(ctx context.Context, m domain.Matter, client domain.ID) (FlatFee, error) {
	if client.IsZero() {
		client = s.primaryClient(m.ID)
	}
	row := FlatFee{MatterID: m.ID, MatterNumber: m.Number, MatterName: m.Name, ClientID: client, FlatFeeCents: int64(m.FlatFeeCents)}
	if m.BillingType != "flat" {
		row.Status = FeeStatusNotFlat
		return row, nil
	}
	var err error
	if !client.IsZero() {
		if row.TrustCents, err = s.clientMatterCents(ctx, client, m.ID); err != nil {
			return row, err
		}
	}
	if row.CollectedCents, err = s.collected(ctx, m.ID); err != nil {
		return row, err
	}
	switch {
	case !positive(int64(m.FlatFeeCents)):
		row.Status = FeeNeedsAmount
	case row.CollectedCents >= int64(m.FlatFeeCents):
		row.Status = FeeCollected
	case row.TrustCents >= int64(m.FlatFeeCents)-row.CollectedCents:
		row.Status = FeeReadyToApply
	default:
		row.Status = FeeAwaitingFunds
	}
	return row, nil
}

func (s *Service) collected(ctx context.Context, matter domain.ID) (int64, error) {
	var v *int64
	err := s.q(ctx).QueryRowContext(ctx, `SELECT SUM(a.amount_cents) FROM trust_flat_fee_applications a
		WHERE a.matter_id = $1 AND NOT EXISTS (SELECT 1 FROM trust_ledger r WHERE r.reverses_id = a.trust_entry_id)`, string(matter)).Scan(&v)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

// ApplyFlatFee moves the confirmed flat fee from trust to operating: one
// transfer_to_operating entry, recorded as the matter's flat-fee application, after which
// the worklist shows Collected. The amount is required; the UI prefills the fee. Only an
// attorney or admin may post it, as with any trust withdrawal. A balance below zero still
// posts and flags.
// The payment row on the invoice is billing's: billing calls this inside its own
// audit.WithTx transaction, writes the payment, and then calls Committed.
func (s *Service) ApplyFlatFee(ctx context.Context, who domain.Principal, in ApplyFlatFeeInput, key string) (PostResult, error) {
	if in.AmountCents == nil || !positive(*in.AmountCents) {
		return PostResult{}, domain.Invalid("amount_cents", "confirm the amount to apply")
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionTrustWithdraw, in.MatterID); err != nil {
		return PostResult{}, err
	}
	m, err := s.practice.Matter(ctx, in.MatterID)
	if err != nil {
		return PostResult{}, notFound(err, "matter")
	}
	if m.BillingType != "flat" {
		return PostResult{}, domain.Invalid("matter_id", "the matter is not billed as a flat fee")
	}
	client := in.ClientID
	if client.IsZero() {
		client = s.primaryClient(m.ID)
	}
	var res PostResult
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		if err := s.lock(ctx); err != nil {
			return err
		}
		var err error
		res, _, err = idem(ctx, s, who, "trust.flat_fee:"+string(m.ID), key, in, func() (PostResult, error) {
			st, err := s.flatFee(ctx, m, client)
			if err != nil {
				return PostResult{}, err
			}
			switch st.Status {
			case FeeNeedsAmount:
				return PostResult{}, domain.Invalid("matter_id", "the matter has no flat fee amount")
			case FeeCollected:
				return PostResult{}, domain.Conflict("the flat fee is already collected")
			}
			account := in.TrustAccountID
			if account.IsZero() {
				if account, err = s.fundedAccount(ctx, client, m.ID); err != nil {
					return PostResult{}, err
				}
			}
			p, err := s.prepare(ctx, who, PostInput{TrustAccountID: account, ClientID: client, MatterID: m.ID, EntryType: TypeTransfer,
				AmountCents: *in.AmountCents, Method: "internal_transfer", Description: fmt.Sprintf("Flat fee earned: %s %s", m.Number, m.Name)})
			if err != nil {
				return PostResult{}, err
			}
			r, err := s.post(ctx, who, p)
			if err != nil {
				return PostResult{}, err
			}
			id, err := s.newID()
			if err != nil {
				return PostResult{}, err
			}
			now := s.now()
			if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO trust_flat_fee_applications (id, created_at, updated_at, created_by, matter_id, trust_entry_id, amount_cents) VALUES ($1,$2,$2,$3,$4,$5,$6)`,
				string(id), micro(now), string(actorID(who)), string(m.ID), string(r.Entry.ID), *in.AmountCents); err != nil {
				return PostResult{}, err
			}
			if err := s.record(ctx, who, "matter", m.ID, "flat_fee_applied", "", domain.Cents(*in.AmountCents).String(), "trust entry "+string(r.Entry.ID)); err != nil {
				return PostResult{}, err
			}
			return r, nil
		})
		return err
	})
	if err != nil {
		return PostResult{}, err
	}
	if !joined {
		s.Committed(ctx, res)
	}
	return res, nil
}

// fundedAccount is the account holding the client's funds on the matter. Two funded
// accounts need an explicit trust_account_id.
func (s *Service) fundedAccount(ctx context.Context, client, matter domain.ID) (domain.ID, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT trust_account_id, SUM(amount_cents) FROM trust_ledger WHERE client_id = $1 AND matter_id = $2 GROUP BY trust_account_id`, string(client), string(matter))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var funded domain.ID
	for rows.Next() {
		var id domain.ID
		var v int64
		if err := rows.Scan(&id, &v); err != nil {
			return "", err
		}
		if !positive(v) {
			continue
		}
		if !funded.IsZero() {
			return "", domain.Invalid("trust_account_id", "more than one trust account holds funds; pick one")
		}
		funded = id
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if funded.IsZero() {
		return "", domain.Invalid("trust_account_id", "no trust account holds funds for this client on the matter")
	}
	return funded, nil
}
