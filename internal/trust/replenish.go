package trust

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Evergreen returns the matter's evergreen minimum: its own row, else the
// trust.evergreen_minimum_cents default. ok is false when the matter has none.
func (s *Service) evergreen(ctx context.Context, matter domain.ID) (int64, bool, error) {
	var v int64
	err := s.q(ctx).QueryRowContext(ctx, `SELECT minimum_cents FROM trust_evergreen WHERE matter_id = $1`, string(matter)).Scan(&v)
	switch {
	case err == nil:
		return v, positive(v), nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, err
	}
	raw := s.setting(ctx, "trust.evergreen_minimum_cents")
	if raw == "" {
		return 0, false, nil
	}
	// A cents setting is stored as a decimal amount, such as "500.00".
	c, err := domain.ParseCents(raw)
	if err != nil {
		return 0, false, nil
	}
	return int64(c), positive(int64(c)), nil
}

// EvergreenMinimum returns the matter's minimum and whether it has one.
func (s *Service) EvergreenMinimum(ctx context.Context, who domain.Principal, matter domain.ID) (int64, bool, error) {
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, matter); err != nil {
		return 0, false, err
	}
	return s.evergreen(ctx, matter)
}

// SetEvergreenMinimum sets the matter's evergreen minimum. Zero turns it off for this
// matter. Staff, attorneys and admins may set it; the change is audited.
func (s *Service) SetEvergreenMinimum(ctx context.Context, who domain.Principal, matter domain.ID, cents int64) error {
	if negative(cents) {
		return domain.Invalid("minimum_cents", "the minimum cannot be negative")
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionTrustDeposit, matter); err != nil {
		return err
	}
	if _, err := s.practice.Matter(ctx, matter); err != nil {
		return notFound(err, "matter")
	}
	_, err := s.tx(ctx, func(ctx context.Context) error {
		if err := s.lock(ctx); err != nil {
			return err
		}
		old, had, err := s.evergreen(ctx, matter)
		if err != nil {
			return err
		}
		if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO trust_evergreen (matter_id, created_at, updated_at, created_by, minimum_cents) VALUES ($1,$2,$2,$3,$4)
			ON CONFLICT (matter_id) DO UPDATE SET minimum_cents = excluded.minimum_cents, updated_at = excluded.updated_at`,
			string(matter), micro(s.now()), string(actorID(who)), cents); err != nil {
			return err
		}
		oldText := ""
		if had {
			oldText = domain.Cents(old).String()
		}
		return s.record(ctx, who, "matter", matter, "trust_evergreen_minimum", oldText, domain.Cents(cents).String(), "")
	})
	return err
}

// evergreenCheck runs after every entry. A debit that leaves the client's matter balance
// under the evergreen minimum opens one request (when none is active) and notifies the
// responsible attorney. A balance back at or above the minimum satisfies open evergreen
// requests.
func (s *Service) evergreenCheck(ctx context.Context, who domain.Principal, e Entry, matter domain.Matter) ([]Event, error) {
	minimum, ok, err := s.evergreen(ctx, e.MatterID)
	if err != nil || !ok {
		return nil, err
	}
	bal, err := s.clientMatterCents(ctx, e.ClientID, e.MatterID)
	if err != nil {
		return nil, err
	}
	active, err := s.replenishments(ctx, e.MatterID, ReplenishActive)
	if err != nil {
		return nil, err
	}
	var evergreen []Replenishment
	for _, r := range active {
		if r.ClientID == e.ClientID && r.ThresholdCents != nil {
			evergreen = append(evergreen, r)
		}
	}
	var events []Event
	if bal >= minimum {
		for _, r := range evergreen {
			if err := s.setReplStatus(ctx, who, r, ReplenishSatisfied); err != nil {
				return nil, err
			}
			events = append(events, Event{Name: "trust.replenishment_satisfied", RecordID: r.ID, MatterID: r.MatterID})
		}
		return events, nil
	}
	if !isDebit(e.Signed) || len(evergreen) > 0 {
		return nil, nil
	}
	threshold := minimum
	r, err := s.newRepl(who, e.MatterID, e.ClientID, minimum-bal, &threshold, nil,
		fmt.Sprintf("The trust balance is %s, under the evergreen minimum of %s. Please deposit %s.", domain.Cents(bal), domain.Cents(minimum), domain.Cents(minimum-bal)))
	if err != nil {
		return nil, err
	}
	if err := s.storeRepl(ctx, who, r, matter); err != nil {
		return nil, err
	}
	return []Event{{Name: "trust.replenishment_requested", RecordID: r.ID, MatterID: r.MatterID}}, nil
}

func (s *Service) newRepl(who domain.Principal, matter, client domain.ID, amount int64, threshold *int64, invoice *domain.ID, msg string) (Replenishment, error) {
	id, err := s.newID()
	if err != nil {
		return Replenishment{}, err
	}
	now := s.now()
	return Replenishment{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actorID(who), MatterID: matter, ClientID: client,
		AmountCents: amount, ThresholdCents: threshold, InvoiceID: invoice, Message: msg, Status: ReplenishActive}, nil
}

func (s *Service) storeRepl(ctx context.Context, who domain.Principal, r Replenishment, matter domain.Matter) error {
	if err := s.insertRepl(ctx, r); err != nil {
		return err
	}
	if err := s.record(ctx, who, "trust_replenishment", r.ID, "created", "", domain.Cents(r.AmountCents).String(), r.Message); err != nil {
		return err
	}
	// Only an active responsible attorney who can read the matter is told; the request
	// itself still opens and shows on the replenishment widget.
	u, ok, err := s.activeUser(ctx, matter.ResponsibleAttorneyID)
	if err != nil || !ok {
		return err
	}
	if ok, err = s.canSee(ctx, u, matter.ID); err != nil || !ok {
		return err
	}
	return s.notify(ctx, who, matter.ResponsibleAttorneyID, NotifyReplenish,
		fmt.Sprintf("Replenishment requested: %s %s", matter.Number, matter.Name), "/trust/replenishment#"+string(r.ID), matter.ID)
}

func (s *Service) setReplStatus(ctx context.Context, who domain.Principal, r Replenishment, status string) error {
	if _, err := s.q(ctx).ExecContext(ctx, `UPDATE trust_replenishment_requests SET status = $1, updated_at = $2 WHERE id = $3`, status, micro(s.now()), string(r.ID)); err != nil {
		return err
	}
	return s.record(ctx, who, "trust_replenishment", r.ID, "status", r.Status, status, "")
}

// CreateReplenishment opens a request by hand, for example from an invoice's
// collections panel. The amount is required.
func (s *Service) CreateReplenishment(ctx context.Context, who domain.Principal, in ReplenishmentInput, key string) (Replenishment, error) {
	out, err := s.CreateReplenishments(ctx, who, []ReplenishmentInput{in}, key)
	if err != nil {
		return Replenishment{}, unwrapItem(err)
	}
	return out[0], nil
}

// CreateReplenishments validates every item, then writes them in one transaction.
func (s *Service) CreateReplenishments(ctx context.Context, who domain.Principal, items []ReplenishmentInput, key string) ([]Replenishment, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("items", "at least one item is required")
	}
	if err := s.checkBulk(ctx, len(items)); err != nil {
		return nil, err
	}
	matters := make([]domain.Matter, len(items))
	for i, in := range items {
		if in.AmountCents == nil || !positive(*in.AmountCents) {
			return nil, itemErr(i, domain.Invalid("amount_requested_cents", "an amount greater than zero is required"))
		}
		if err := s.allow(ctx, who, domain.ResTrust, domain.ActionTrustDeposit, in.MatterID); err != nil {
			return nil, itemErr(i, err)
		}
		m, err := s.practice.Matter(ctx, in.MatterID)
		if err != nil {
			return nil, itemErr(i, notFound(err, "matter"))
		}
		if !s.isClient(in.MatterID, in.ClientID) {
			return nil, itemErr(i, domain.Invalid("client_id", "the client must be a client party on the matter"))
		}
		matters[i] = m
	}
	var out []Replenishment
	var events []Event
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		var err error
		out, _, err = idem(ctx, s, who, "trust.replenishment", key, items, func() ([]Replenishment, error) {
			var res []Replenishment
			for i, in := range items {
				var invoice *domain.ID
				if !in.InvoiceID.IsZero() {
					v := in.InvoiceID
					invoice = &v
				}
				msg := trimmed(in.Message)
				if msg == "" {
					msg = "Please replenish the trust balance by " + domain.Cents(*in.AmountCents).String() + "."
				}
				r, err := s.newRepl(who, in.MatterID, in.ClientID, *in.AmountCents, nil, invoice, msg)
				if err != nil {
					return nil, err
				}
				if err := s.storeRepl(ctx, who, r, matters[i]); err != nil {
					return nil, err
				}
				events = append(events, Event{Name: "trust.replenishment_requested", RecordID: r.ID, MatterID: r.MatterID})
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

// CancelReplenishment and SatisfyReplenishment close an active request.
func (s *Service) CancelReplenishment(ctx context.Context, who domain.Principal, id domain.ID) (Replenishment, error) {
	return s.closeRepl(ctx, who, id, ReplenishCancelled, "trust.replenishment_cancelled")
}

// SatisfyReplenishment marks an active request satisfied.
func (s *Service) SatisfyReplenishment(ctx context.Context, who domain.Principal, id domain.ID) (Replenishment, error) {
	return s.closeRepl(ctx, who, id, ReplenishSatisfied, "trust.replenishment_satisfied")
}

func (s *Service) closeRepl(ctx context.Context, who domain.Principal, id domain.ID, status, event string) (Replenishment, error) {
	r, err := s.replenishment(ctx, id)
	if err != nil {
		return Replenishment{}, err
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionTrustDeposit, r.MatterID); err != nil {
		return Replenishment{}, err
	}
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		if err := s.lock(ctx); err != nil {
			return err
		}
		cur, err := s.replenishment(ctx, id)
		if err != nil {
			return err
		}
		if cur.Status != ReplenishActive {
			return domain.Conflict("the request is already " + cur.Status)
		}
		if err := s.setReplStatus(ctx, who, cur, status); err != nil {
			return err
		}
		r = cur
		r.Status, r.UpdatedAt = status, s.now()
		return nil
	})
	if err != nil {
		return Replenishment{}, err
	}
	s.publish(ctx, joined, []Event{{Name: event, RecordID: r.ID, MatterID: r.MatterID}})
	return r, nil
}

// Replenishment returns one request the caller may see.
func (s *Service) Replenishment(ctx context.Context, who domain.Principal, id domain.ID) (Replenishment, error) {
	r, err := s.replenishment(ctx, id)
	if err != nil {
		return Replenishment{}, err
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, r.MatterID); err != nil {
		return Replenishment{}, err
	}
	return r, nil
}

// Replenishments lists requests the caller may see. A walled matter is absent.
func (s *Service) Replenishments(ctx context.Context, who domain.Principal, matter domain.ID, status, cursor string, limit int) (Page[Replenishment], error) {
	rows, err := s.visibleRepls(ctx, who, matter, status)
	if err != nil {
		return Page[Replenishment]{}, err
	}
	if limit, err = s.pageLimit(ctx, limit); err != nil {
		return Page[Replenishment]{}, err
	}
	return paginate(rows, func(r Replenishment) domain.ID { return r.ID }, cursor, limit), nil
}

func (s *Service) visibleRepls(ctx context.Context, who domain.Principal, matter domain.ID, status string) ([]Replenishment, error) {
	rd, err := s.reader(ctx, who)
	if err != nil {
		return nil, err
	}
	all, err := s.replenishments(ctx, matter, status)
	if err != nil {
		return nil, err
	}
	var rows []Replenishment
	for _, r := range all {
		if ok, err := rd.ok(ctx, r.MatterID); err != nil {
			return nil, err
		} else if ok {
			rows = append(rows, r)
		}
	}
	return rows, nil
}
