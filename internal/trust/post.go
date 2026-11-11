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

// checkBulk applies api.bulk_batch_max (registry default 500). A registry without the
// key, as in package tests, applies none.
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
	if in.ReversesEntryID != nil {
		return p, domain.Invalid("reverses_entry_id", "reverses_entry_id must be null; post a reversal through /api/v1/trust-ledger/{trust_entry_id}/reversals")
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
	// The caller is authorized on the posting matter; the client and account totals they
	// get count only the matters they may read, as Balances does (PRD 3: walls apply on
	// every surface). The overall totals still decide the flags.
	rd := &readable{s: s, who: who, seen: map[domain.ID]bool{e.MatterID: true}}
	shown, err := bal.visible(ctx, rd.ok)
	if err != nil {
		return PostResult{}, err
	}
	res := PostResult{Entry: e, Balances: shown, Flags: []Flag{}, cached: []domain.TrustBalance{cached}}
	res.Events = append(res.Events, Event{Name: "trust.entry_posted", RecordID: e.ID, MatterID: e.MatterID, Data: map[string]string{
		"trust_account_id": string(e.TrustAccountID), "client_id": string(e.ClientID), "entry_type": e.EntryType, "amount_cents": e.Signed.String(),
	}})
	if isDebit(e.Signed) {
		flags, events, err := s.raise(ctx, who, e, bal, matter, rd.ok)
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
	var rows []domain.TrustBalance
	for _, r := range results {
		events = append(events, r.Events...)
		rows = append(rows, r.cached...)
	}
	if err := s.syncBalances(ctx, rows); err != nil {
		log.Printf("trust: balance sink: %v (RebuildBalances repushes every row)", err)
	}
	s.publish(ctx, false, events)
}

// syncBalances pushes the current cache row for each changed owner to the balance sink.
// It does not push the snapshot a post computed: two posts on one matter may run their
// after-commit effects in either order, and an older snapshot pushed last would leave the
// practice copy (close warning, At a Glance) stale. Each push holds the trust write lock
// and re-reads the committed cache row, so the last push always carries the newest value,
// in this process or another.
func (s *Service) syncBalances(ctx context.Context, rows []domain.TrustBalance) error {
	if s.sink == nil || len(rows) == 0 {
		return nil
	}
	type owner struct{ account, client, matter domain.ID }
	seen := map[owner]bool{}
	var firstErr error
	for _, row := range rows {
		k := owner{row.TrustAccountID, row.ClientID, row.MatterID}
		if seen[k] {
			continue
		}
		seen[k] = true
		// The sink gets the caller's context without the lock transaction: it writes the
		// practice store, not trust tables.
		outer := ctx
		_, err := s.tx(ctx, func(ctx context.Context) error {
			if err := s.lock(ctx); err != nil {
				return err
			}
			cur, ok, err := s.cacheRow(ctx, k.account, k.client, k.matter)
			if err != nil || !ok {
				return err
			}
			return s.sink(outer, cur)
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// raise adds the flags for a debit that left a negative balance: the matter balance in
// the account, the client's balance overall, and the account balance. The admins and the
// responsible attorney get an in-app notification and an email; the email cannot be
// turned off (PRD 8.17).
//
// The overall client and account totals decide the flag and are stored on it, with the
// per-matter parts behind them. Every reader is shown only the parts on matters they may
// read: the caller through callerSees, each email address through addrSees, and the
// commit event through unwalled. A walled matter's amount never shows through a client or
// account total.
func (s *Service) raise(ctx context.Context, who domain.Principal, e Entry, bal snapshot, matter domain.Matter, callerSees func(context.Context, domain.ID) (bool, error)) ([]Flag, []Event, error) {
	type raised struct {
		flag  Flag
		parts scopeParts // nil for a matter flag
	}
	var all []raised
	var flags []Flag
	var events []Event
	add := func(scope string, cents int64, parts scopeParts) error {
		id, err := s.newID()
		if err != nil {
			return err
		}
		now := s.now()
		reason := fmt.Sprintf("%s balance %s after %s", scope, domain.Cents(cents), e.EntryType)
		if parts != nil {
			// The audit row and the stored reason must not carry a walled-inclusive total.
			reason = fmt.Sprintf("%s balance below zero after %s", scope, e.EntryType)
		}
		f := Flag{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actorID(who), TrustEntryID: e.ID, Scope: scope, BalanceCents: cents, Status: "open",
			entryMatter: e.MatterID, entryClient: e.ClientID, entryAccount: e.TrustAccountID, reason: reason}
		if err := s.insertFlag(ctx, f); err != nil {
			return err
		}
		if err := s.insertFlagParts(ctx, f.ID, parts); err != nil {
			return err
		}
		if err := s.record(ctx, who, "trust_flag", f.ID, "raised", "", f.reason, "below-zero trust balance"); err != nil {
			return err
		}
		event := cents
		if parts != nil {
			if f.BalanceCents, err = parts.shown(ctx, callerSees); err != nil {
				return err
			}
			if event, err = parts.shown(ctx, s.unwalled); err != nil {
				return err
			}
		}
		f.expose()
		flags = append(flags, f)
		all = append(all, raised{flag: f, parts: parts})
		events = append(events, Event{Name: "trust.flag_raised", RecordID: f.ID, MatterID: e.MatterID, Data: map[string]string{
			"matter_id": string(e.MatterID), "client_id": string(e.ClientID), "trust_account_id": string(e.TrustAccountID),
			"balance_cents": domain.Cents(event).String(), "trust_entry_id": string(e.ID), "balance_scope": scope,
		}})
		return nil
	}
	if negative(bal.MatterCents) {
		if err := add(ScopeMatter, bal.MatterCents, nil); err != nil {
			return nil, nil, err
		}
	}
	if negative(bal.ClientCents) {
		if err := add(ScopeClient, bal.ClientCents, bal.client); err != nil {
			return nil, nil, err
		}
	}
	if negative(bal.AccountCents) {
		if err := add(ScopeAccount, bal.AccountCents, bal.account); err != nil {
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
	for _, r := range people {
		if !r.sees {
			continue
		}
		if err := s.notify(ctx, who, r.user.ID, NotifyTrustFlag, title, link, matter.ID); err != nil {
			return nil, nil, err
		}
	}
	for _, t := range s.flagAddresses(ctx, people) {
		sees := s.addrSees(t)
		full, err := sees(ctx, matter.ID)
		if err != nil {
			return nil, nil, err
		}
		subject, text := walledFlagSubject, walledFlagBody
		if full {
			var lines []string
			for _, r := range all {
				cents := r.flag.BalanceCents
				if r.parts != nil {
					if cents, err = r.parts.shown(ctx, sees); err != nil {
						return nil, nil, err
					}
				}
				lines = append(lines, fmt.Sprintf("- %s balance: %s", r.flag.Scope, domain.Cents(cents)))
			}
			subject = title
			text = fmt.Sprintf("A trust %s of %s on matter %s %s took a balance below zero. The entry posted; it was not blocked.\n\n%s\n\nReview and resolve the flag with a note: %s\n",
				e.EntryType, absCents(e.Signed), matter.Number, matter.Name, strings.Join(lines, "\n"), link)
		}
		if err := s.queueMail(ctx, who, MailKindTrustFlag, flags[0].ID, t.addr, subject, text); err != nil {
			return nil, nil, err
		}
	}
	return flags, events, nil
}

// The below-zero email cannot be turned off, but a recipient outside the matter's wall
// gets this notice with no matter number, name, amount, balance or flag link.
const (
	walledFlagSubject = "Trust below zero on a walled matter"
	walledFlagBody    = "A trust entry took a balance below zero on a matter that is walled from you. The entry posted; it was not blocked.\n\nA user on the matter's wall must review and resolve the flag with a note.\n"
)

func absCents(c domain.Cents) domain.Cents {
	if isDebit(c) {
		return -c
	}
	return c
}
