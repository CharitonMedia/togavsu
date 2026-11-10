package trust

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// posting is a validated, authorized entry waiting for the transaction.
type posting struct {
	in     PostInput
	signed domain.Cents
	matter domain.Matter
}

// Post appends one deposit, withdrawal, transfer to operating, refund or adjustment.
// A balance below zero never blocks: the entry posts and the result carries the flags.
// key is the Idempotency-Key; a replay returns the first result and emits nothing.
func (s *Service) Post(ctx context.Context, who domain.Principal, in PostInput, key string) (PostResult, error) {
	p, err := s.prepare(ctx, who, in)
	if err != nil {
		return PostResult{}, err
	}
	var res PostResult
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		if err := s.lock(ctx); err != nil {
			return err
		}
		var err error
		res, _, err = idem(ctx, s, who, "trust.post", key, in, func() (PostResult, error) {
			return s.post(ctx, who, p)
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

// PostBulk posts items in order in one transaction. Each item sees the balance the item
// before it left. Any invalid or denied item posts nothing. Below zero still posts.
func (s *Service) PostBulk(ctx context.Context, who domain.Principal, items []PostInput, key string) ([]PostResult, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("items", "at least one item is required")
	}
	if err := s.checkBulk(ctx, len(items)); err != nil {
		return nil, err
	}
	ps := make([]posting, len(items))
	for i, in := range items {
		p, err := s.prepare(ctx, who, in)
		if err != nil {
			return nil, itemErr(i, err)
		}
		ps[i] = p
	}
	var out []PostResult
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		if err := s.lock(ctx); err != nil {
			return err
		}
		var err error
		out, _, err = idem(ctx, s, who, "trust.post_bulk", key, items, func() ([]PostResult, error) {
			res := make([]PostResult, 0, len(ps))
			for i, p := range ps {
				r, err := s.post(ctx, who, p)
				if err != nil {
					return nil, itemErr(i, err)
				}
				res = append(res, r)
			}
			return res, nil
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	if !joined {
		s.Committed(ctx, out...)
	}
	return out, nil
}

func itemErr(i int, err error) error {
	var api *domain.APIError
	if errors.As(err, &api) {
		c := *api
		c.Field = fmt.Sprintf("items[%d].%s", i, strings.TrimPrefix(api.Field, "items."))
		if api.Field == "" {
			c.Field = fmt.Sprintf("items[%d]", i)
		}
		return &c
	}
	return err
}

// checkBulk applies api.bulk_batch_max. The PRD names no cap, so a registry without the
// key applies none.
// ponytail: ceiling: the API contract lists api.bulk_batch_max (starting value 500) but
// internal/settings/definitions.go does not declare it yet, so batches are uncapped.
// Upgrade path: the integration lead adds the key to the registry; this reads it with no
// code change. Trigger: the finance integration.
func (s *Service) checkBulk(ctx context.Context, n int) error {
	max, err := s.intSetting(ctx, "api.bulk_batch_max")
	if errors.Is(err, settings.ErrUnknown) {
		return nil
	}
	if err != nil {
		return err
	}
	if n > max {
		return domain.Invalid("items", fmt.Sprintf("at most %d items", max))
	}
	return nil
}

// Reverse appends the compensating entry for id. The original row never changes. A
// reversal cannot itself be reversed, and an entry is reversed at most once. Reversing a
// deposit can leave a negative balance: it still posts and flags.
func (s *Service) Reverse(ctx context.Context, who domain.Principal, id domain.ID, in ReverseInput, key string) (PostResult, error) {
	if trimmed(in.Reason) == "" {
		return PostResult{}, domain.Invalid("reason", "a reason for the reversal is required")
	}
	orig, err := s.entry(ctx, id)
	if err != nil {
		return PostResult{}, err
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionTrustWithdraw, orig.MatterID); err != nil {
		return PostResult{}, err
	}
	matter, err := s.practice.Matter(ctx, orig.MatterID)
	if err != nil {
		return PostResult{}, notFound(err, "matter")
	}
	var res PostResult
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		if err := s.lock(ctx); err != nil {
			return err
		}
		var err error
		res, _, err = idem(ctx, s, who, "trust.reverse:"+string(id), key, in, func() (PostResult, error) {
			if orig.ReversesEntryID != nil {
				return PostResult{}, domain.Conflict("a reversal cannot be reversed; post a new entry instead")
			}
			var reversed bool
			if err := s.q(ctx).QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM trust_ledger WHERE reverses_id = $1)`, string(id)).Scan(&reversed); err != nil {
				return PostResult{}, err
			}
			if reversed {
				return PostResult{}, domain.Conflict("this entry is already reversed")
			}
			back := -orig.Signed
			e, err := s.newEntry(who, orig.TrustAccountID, orig.ClientID, orig.MatterID, orig.StoredType, back, orig.Method, trimmed(in.Reason))
			if err != nil {
				return PostResult{}, err
			}
			e.ReversesEntryID = &orig.ID
			e.EntryType, e.Direction, e.AmountCents = TypeReversal, direction(int64(back)), int64(back)
			e.AdjustmentDirection = ""
			return s.commitEntry(ctx, who, e, matter)
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

// prepare validates the body and checks the caller before the transaction starts.
func (s *Service) prepare(ctx context.Context, who domain.Principal, in PostInput) (posting, error) {
	p := posting{in: in}
	for field, id := range map[string]domain.ID{"trust_account_id": in.TrustAccountID, "client_id": in.ClientID, "matter_id": in.MatterID} {
		if _, err := domain.ParseID(string(id)); err != nil {
			return p, domain.Invalid(field, "a UUID is required")
		}
	}
	if !methods[in.Method] {
		return p, domain.Invalid("method", "method must be cash, check, card, ach, internal_transfer or other")
	}
	if trimmed(in.Description) == "" {
		return p, domain.Invalid("description", "a description is required")
	}
	amount := domain.Cents(in.AmountCents)
	action := domain.ActionTrustWithdraw
	switch in.EntryType {
	case TypeDeposit:
		action = domain.ActionTrustDeposit
		p.signed = amount
	case TypeWithdrawal, TypeTransfer, TypeRefund:
		p.signed = -amount
	case TypeAdjustment:
		if in.AmountCents == int64(0) {
			return p, domain.Invalid("amount_cents", "an adjustment amount cannot be zero")
		}
		if want := direction(in.AmountCents); in.AdjustmentDirection != want {
			return p, domain.Invalid("adjustment_direction", "credit needs a positive amount and debit a negative amount")
		}
		p.signed = amount
	case TypeReversal:
		return p, domain.Invalid("entry_type", "post a reversal through the entry's reversals route")
	default:
		return p, domain.Invalid("entry_type", "unknown entry type")
	}
	if in.EntryType != TypeAdjustment {
		if in.AdjustmentDirection != "" {
			return p, domain.Invalid("adjustment_direction", "only an adjustment takes a direction")
		}
		if !positive(in.AmountCents) {
			return p, domain.Invalid("amount_cents", "the amount must be greater than zero; the entry type sets the direction")
		}
	}
	if err := s.allow(ctx, who, domain.ResTrust, action, in.MatterID); err != nil {
		return p, err
	}
	matter, err := s.practice.Matter(ctx, in.MatterID)
	if err != nil {
		return p, notFound(err, "matter")
	}
	p.matter = matter
	if !s.isClient(in.MatterID, in.ClientID) {
		return p, domain.Invalid("client_id", "the client must be a client party on the matter")
	}
	return p, nil
}

func (s *Service) isClient(matter, client domain.ID) bool {
	for _, party := range s.practice.Parties(matter, client) {
		if party.MatterID == matter && party.ContactID == client && party.Role == "client" {
			return true
		}
	}
	return false
}

func (s *Service) newEntry(who domain.Principal, account, client, matter domain.ID, typ string, signed domain.Cents, method, desc string) (Entry, error) {
	id, err := s.newID()
	if err != nil {
		return Entry{}, err
	}
	e := Entry{
		ID: id, CreatedAt: s.now(), CreatedBy: actorID(who), TrustAccountID: account, ClientID: client, MatterID: matter,
		EntryType: typ, StoredType: typ, Signed: signed, AmountCents: int64(signed), Method: method, Description: desc,
	}
	switch {
	case typ == TypeAdjustment:
		e.AdjustmentDirection = direction(int64(signed))
	case isDebit(signed):
		e.AmountCents = -int64(signed)
	}
	return e, nil
}

// post runs inside the locked transaction.
func (s *Service) post(ctx context.Context, who domain.Principal, p posting) (PostResult, error) {
	e, err := s.newEntry(who, p.in.TrustAccountID, p.in.ClientID, p.in.MatterID, p.in.EntryType, p.signed, p.in.Method, trimmed(p.in.Description))
	if err != nil {
		return PostResult{}, err
	}
	return s.commitEntry(ctx, who, e, p.matter)
}

// commitEntry appends e and applies every consequence in the same transaction: the cached
// balance, below-zero flags with their notifications and email, evergreen replenishment,
// the audit rows and the commit events.
func (s *Service) commitEntry(ctx context.Context, who domain.Principal, e Entry, matter domain.Matter) (PostResult, error) {
	acct, err := s.account(ctx, e.TrustAccountID)
	if err != nil {
		return PostResult{}, err
	}
	if !acct.Active {
		return PostResult{}, domain.Invalid("trust_account_id", "the trust account is inactive")
	}
	if err := s.insertEntry(ctx, e); err != nil {
		return PostResult{}, err
	}
	bal, err := s.balances(ctx, e.TrustAccountID, e.ClientID, e.MatterID)
	if err != nil {
		return PostResult{}, err
	}
	cached, err := s.cache(ctx, who, e.TrustAccountID, e.ClientID, e.MatterID, bal.MatterCents)
	if err != nil {
		return PostResult{}, err
	}
	field := "posted"
	if e.ReversesEntryID != nil {
		field = "reversal_of:" + string(*e.ReversesEntryID)
	}
	if err := s.record(ctx, who, "trust_entry", e.ID, field, "",
		fmt.Sprintf("%s %s account=%s client=%s matter=%s", e.EntryType, e.Signed, e.TrustAccountID, e.ClientID, e.MatterID), e.Description); err != nil {
		return PostResult{}, err
	}
	res := PostResult{Entry: e, Balances: bal, Flags: []Flag{}, cached: []domain.TrustBalance{cached}}
	res.Events = append(res.Events, Event{Name: "trust.entry_posted", RecordID: e.ID, MatterID: e.MatterID, Data: map[string]string{
		"trust_account_id": string(e.TrustAccountID), "client_id": string(e.ClientID), "entry_type": e.EntryType, "amount_cents": e.Signed.String(),
	}})
	if isDebit(e.Signed) {
		flags, events, err := s.raise(ctx, who, e, bal, matter)
		if err != nil {
			return PostResult{}, err
		}
		res.Flags = flags
		res.Events = append(res.Events, events...)
	}
	events, err := s.evergreenCheck(ctx, who, e, matter)
	if err != nil {
		return PostResult{}, err
	}
	res.Events = append(res.Events, events...)
	return res, nil
}

// Committed runs the after-commit effects of results: commit events and the balance
// sink. Post, PostBulk and Reverse call it themselves. A caller that posts inside its own
// audit.WithTx transaction calls it after that transaction commits.
func (s *Service) Committed(ctx context.Context, results ...PostResult) {
	var events []Event
	for _, r := range results {
		events = append(events, r.Events...)
		if s.sink == nil {
			continue
		}
		for _, row := range r.cached {
			if err := s.sink(ctx, row); err != nil {
				log.Printf("trust: balance sink: %v", err)
			}
		}
	}
	s.publish(ctx, false, events)
}

// raise adds the flags for a debit that left a negative balance: the matter balance in
// the account, the client's balance overall, and the account balance. The admins and the
// responsible attorney get an in-app notification and an email; the email cannot be
// turned off (PRD 8.17).
func (s *Service) raise(ctx context.Context, who domain.Principal, e Entry, bal Balances, matter domain.Matter) ([]Flag, []Event, error) {
	var flags []Flag
	var events []Event
	add := func(scope string, cents int64) error {
		id, err := s.newID()
		if err != nil {
			return err
		}
		now := s.now()
		f := Flag{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actorID(who), TrustEntryID: e.ID, Scope: scope, BalanceCents: cents, Status: "open",
			entryMatter: e.MatterID, entryClient: e.ClientID, TrustAccountID: e.TrustAccountID,
			reason: fmt.Sprintf("%s balance %s after %s", scope, domain.Cents(cents), e.EntryType)}
		if err := s.insertFlag(ctx, f); err != nil {
			return err
		}
		if err := s.record(ctx, who, "trust_flag", f.ID, "raised", "", f.reason, "below-zero trust balance"); err != nil {
			return err
		}
		switch scope {
		case ScopeMatter:
			f.MatterID, f.ClientID = e.MatterID, e.ClientID
		case ScopeClient:
			f.ClientID, f.TrustAccountID = e.ClientID, ""
		}
		flags = append(flags, f)
		events = append(events, Event{Name: "trust.flag_raised", RecordID: f.ID, MatterID: e.MatterID, Data: map[string]string{
			"matter_id": string(e.MatterID), "client_id": string(e.ClientID), "trust_account_id": string(e.TrustAccountID),
			"balance_cents": domain.Cents(cents).String(), "trust_entry_id": string(e.ID), "balance_scope": scope,
		}})
		return nil
	}
	if negative(bal.MatterCents) {
		if err := add(ScopeMatter, bal.MatterCents); err != nil {
			return nil, nil, err
		}
	}
	if negative(bal.ClientCents) {
		if err := add(ScopeClient, bal.ClientCents); err != nil {
			return nil, nil, err
		}
	}
	if negative(bal.AccountCents) {
		if err := add(ScopeAccount, bal.AccountCents); err != nil {
			return nil, nil, err
		}
	}
	if len(flags) == 0 {
		return flags, nil, nil
	}
	people, err := s.flagRecipients(ctx, matter)
	if err != nil {
		return nil, nil, err
	}
	title := fmt.Sprintf("Trust below zero: %s %s", matter.Number, matter.Name)
	link := "/trust/flags/" + string(flags[0].ID)
	for _, u := range people {
		if err := s.notify(ctx, who, u.ID, NotifyTrustFlag, title, link, matter.ID); err != nil {
			return nil, nil, err
		}
	}
	var lines []string
	for _, f := range flags {
		lines = append(lines, fmt.Sprintf("- %s balance: %s", f.Scope, domain.Cents(f.BalanceCents)))
	}
	body := fmt.Sprintf("A trust %s of %s on matter %s %s took a balance below zero. The entry posted; it was not blocked.\n\n%s\n\nReview and resolve the flag with a note: %s\n",
		e.EntryType, absCents(e.Signed), matter.Number, matter.Name, strings.Join(lines, "\n"), link)
	for _, addr := range s.flagAddresses(ctx, people) {
		if err := s.queueMail(ctx, who, MailKindTrustFlag, flags[0].ID, addr, title, body); err != nil {
			return nil, nil, err
		}
	}
	return flags, events, nil
}

func absCents(c domain.Cents) domain.Cents {
	if isDebit(c) {
		return -c
	}
	return c
}
