package trust

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Entry returns one ledger row. A walled matter's row is not found.
func (s *Service) Entry(ctx context.Context, who domain.Principal, id domain.ID) (Entry, error) {
	e, err := s.entry(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, e.MatterID); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Entries lists ledger rows the caller may see, in posting order.
func (s *Service) Entries(ctx context.Context, who domain.Principal, f EntryFilter) (Page[Entry], error) {
	rows, err := s.visibleEntries(ctx, who, f)
	if err != nil {
		return Page[Entry]{}, err
	}
	limit, err := s.pageLimit(ctx, f.Limit)
	if err != nil {
		return Page[Entry]{}, err
	}
	return paginate(rows, func(e Entry) domain.ID { return e.ID }, f.Cursor, limit), nil
}

// visibleEntries reads the filtered ledger and drops rows on matters the caller cannot
// see. Pages are cut after the wall filter, so a page never leaks a walled row.
// ponytail: ceiling: every matching row is read and wall-checked in Go (one check per
// matter, cached per call). Upgrade path: push the caller's visible matter ids into the
// query and page in SQL. Trigger: a ledger list reads more than 50k rows.
func (s *Service) visibleEntries(ctx context.Context, who domain.Principal, f EntryFilter) ([]Entry, error) {
	if f.FromDate != "" && !validDate(f.FromDate) {
		return nil, domain.Invalid("from_date", "use YYYY-MM-DD")
	}
	if f.ToDate != "" && !validDate(f.ToDate) {
		return nil, domain.Invalid("to_date", "use YYYY-MM-DD")
	}
	rd, err := s.reader(ctx, who)
	if err != nil {
		return nil, err
	}
	all, err := s.entries(ctx, f)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range all {
		ok, err := rd.ok(ctx, e.MatterID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// Balances returns identified balance rows computed from the ledger: one per client per
// matter per account, one per client, one per account. Rows for a walled matter are
// absent and do not count toward the client and account rows the caller sees.
func (s *Service) Balances(ctx context.Context, who domain.Principal, f EntryFilter) ([]BalanceRow, error) {
	entries, err := s.visibleEntries(ctx, who, EntryFilter{MatterID: f.MatterID, ClientID: f.ClientID, TrustAccountID: f.TrustAccountID})
	if err != nil {
		return nil, err
	}
	type key struct{ a, c, m domain.ID }
	matter := map[key]int64{}
	client := map[domain.ID]int64{}
	account := map[domain.ID]int64{}
	for _, e := range entries {
		matter[key{e.TrustAccountID, e.ClientID, e.MatterID}] += int64(e.Signed)
		client[e.ClientID] += int64(e.Signed)
		account[e.TrustAccountID] += int64(e.Signed)
	}
	var rows []BalanceRow
	for k, v := range matter {
		rows = append(rows, BalanceRow{Scope: ScopeMatter, TrustAccountID: k.a, ClientID: k.c, MatterID: k.m, BalanceCents: v})
	}
	for k, v := range client {
		rows = append(rows, BalanceRow{Scope: ScopeClient, ClientID: k, BalanceCents: v})
	}
	for k, v := range account {
		rows = append(rows, BalanceRow{Scope: ScopeAccount, TrustAccountID: k, BalanceCents: v})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Scope != b.Scope {
			return scopeRank(a.Scope) < scopeRank(b.Scope)
		}
		return string(a.TrustAccountID)+string(a.ClientID)+string(a.MatterID) < string(b.TrustAccountID)+string(b.ClientID)+string(b.MatterID)
	})
	return rows, nil
}

// scopeRank orders balance rows matter, client, account.
func scopeRank(scope string) int {
	for i, s := range []string{ScopeMatter, ScopeClient, ScopeAccount} {
		if s == scope {
			return i
		}
	}
	return len(scope)
}

// MatterBalance is the client's trust balance on a matter across every account, for the
// matter screen and the flat-fee worklist.
func (s *Service) MatterBalance(ctx context.Context, who domain.Principal, matter, client domain.ID) (int64, error) {
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, matter); err != nil {
		return 0, err
	}
	return s.clientMatterCents(ctx, client, matter)
}

// VerifyBalances checks the cache invariant: every cached per-matter balance equals the
// ledger sum for that client, matter and account, and every ledger owner has a cache row.
func (s *Service) VerifyBalances(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT l.trust_account_id, l.client_id, l.matter_id, SUM(l.amount_cents), MAX(c.balance_cents), COUNT(c.id)
		FROM trust_ledger l LEFT JOIN trust_balance_cache c
		  ON c.trust_account_id = l.trust_account_id AND c.client_id = l.client_id AND c.matter_id = l.matter_id
		GROUP BY l.trust_account_id, l.client_id, l.matter_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, c, m string
		var sum int64
		var cached *int64
		var n int
		if err := rows.Scan(&a, &c, &m, &sum, &cached, &n); err != nil {
			return err
		}
		if cached == nil || *cached != sum {
			return fmt.Errorf("trust: cached balance for account %s client %s matter %s does not match the ledger (%d)", a, c, m, sum)
		}
	}
	return rows.Err()
}

// RebuildBalances recomputes every cache row from the ledger, then pushes every row to
// the balance sink, which repairs a practice copy left wrong by a failed sink call.
func (s *Service) RebuildBalances(ctx context.Context) error {
	var pushed []domain.TrustBalance
	_, err := s.tx(ctx, func(ctx context.Context) error {
		if err := s.lock(ctx); err != nil {
			return err
		}
		rows, err := s.q(ctx).QueryContext(ctx, `SELECT trust_account_id, client_id, matter_id, SUM(amount_cents) FROM trust_ledger GROUP BY trust_account_id, client_id, matter_id`)
		if err != nil {
			return err
		}
		type owner struct {
			a, c, m domain.ID
			v       int64
		}
		var all []owner
		for rows.Next() {
			var o owner
			if err := rows.Scan(&o.a, &o.c, &o.m, &o.v); err != nil {
				rows.Close()
				return err
			}
			all = append(all, o)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, o := range all {
			row, err := s.cache(ctx, domain.SystemPrincipal(), o.a, o.c, o.m, o.v)
			if err != nil {
				return err
			}
			pushed = append(pushed, row)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.syncBalances(ctx, pushed)
}

// Accounts.

var lastFour = regexp.MustCompile(`^\d{4}$`)

// CreateAccount adds a trust bank account. Only an admin may (admin page, Trust tab).
func (s *Service) CreateAccount(ctx context.Context, who domain.Principal, in AccountInput, key string) (Account, error) {
	if err := s.allow(ctx, who, domain.ResSettings, domain.ActionWrite, ""); err != nil {
		return Account{}, err
	}
	if trimmed(in.Bank) == "" {
		return Account{}, domain.Invalid("bank", "the bank is required")
	}
	if trimmed(in.Name) == "" {
		return Account{}, domain.Invalid("name", "the account name is required")
	}
	if !lastFour.MatchString(in.LastFour) {
		return Account{}, domain.Invalid("last_four", "the last four digits are required; the full number is never stored")
	}
	var out Account
	var replay bool
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		var err error
		out, replay, err = idem(ctx, s, who, "trust.account", key, in, func() (Account, error) {
			id, err := s.newID()
			if err != nil {
				return Account{}, err
			}
			now := s.now()
			a := Account{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actorID(who), Bank: trimmed(in.Bank), Name: trimmed(in.Name), IOLTA: in.IOLTA, LastFour: in.LastFour, Active: in.Active == nil || *in.Active}
			if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO trust_accounts (id, created_at, updated_at, created_by, bank, name, iolta, last_four, active)
				VALUES ($1,$2,$2,$3,$4,$5,CASE WHEN $6 THEN 1 ELSE 0 END,$7,CASE WHEN $8 THEN 1 ELSE 0 END)`,
				string(a.ID), micro(now), string(a.CreatedBy), a.Bank, a.Name, a.IOLTA, a.LastFour, a.Active); err != nil {
				return Account{}, err
			}
			return a, s.record(ctx, who, "trust_account", a.ID, "created", "", a.Bank+" "+a.Name+" ****"+a.LastFour, "")
		})
		return err
	})
	if err != nil {
		return Account{}, err
	}
	s.publish(ctx, joined || replay, []Event{{Name: "trust_account.created", RecordID: out.ID}})
	return out, nil
}

// UpdateAccount patches an account. Only an admin may. Ledger rows are not touched.
func (s *Service) UpdateAccount(ctx context.Context, who domain.Principal, id domain.ID, p AccountPatch) (Account, error) {
	if err := s.allow(ctx, who, domain.ResSettings, domain.ActionWrite, ""); err != nil {
		return Account{}, err
	}
	var out Account
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		a, err := s.account(ctx, id)
		if err != nil {
			return err
		}
		old := a
		if p.Bank != nil {
			if trimmed(*p.Bank) == "" {
				return domain.Invalid("bank", "the bank is required")
			}
			a.Bank = trimmed(*p.Bank)
		}
		if p.Name != nil {
			if trimmed(*p.Name) == "" {
				return domain.Invalid("name", "the account name is required")
			}
			a.Name = trimmed(*p.Name)
		}
		if p.LastFour != nil {
			if !lastFour.MatchString(*p.LastFour) {
				return domain.Invalid("last_four", "four digits are required")
			}
			a.LastFour = *p.LastFour
		}
		if p.IOLTA != nil {
			a.IOLTA = *p.IOLTA
		}
		if p.Active != nil {
			a.Active = *p.Active
		}
		a.UpdatedAt = s.now()
		if _, err := s.q(ctx).ExecContext(ctx, `UPDATE trust_accounts SET bank = $1, name = $2, iolta = CASE WHEN $3 THEN 1 ELSE 0 END, last_four = $4,
			active = CASE WHEN $5 THEN 1 ELSE 0 END, updated_at = $6 WHERE id = $7`,
			a.Bank, a.Name, a.IOLTA, a.LastFour, a.Active, micro(a.UpdatedAt), string(id)); err != nil {
			return err
		}
		for _, c := range []struct{ f, o, n string }{
			{"bank", old.Bank, a.Bank}, {"name", old.Name, a.Name}, {"last_four", old.LastFour, a.LastFour},
			{"iolta", fmt.Sprint(old.IOLTA), fmt.Sprint(a.IOLTA)}, {"active", fmt.Sprint(old.Active), fmt.Sprint(a.Active)},
		} {
			if c.o != c.n {
				if err := s.record(ctx, who, "trust_account", id, c.f, c.o, c.n, ""); err != nil {
					return err
				}
			}
		}
		out = a
		return nil
	})
	if err != nil {
		return Account{}, err
	}
	s.publish(ctx, joined, []Event{{Name: "trust_account.updated", RecordID: id}})
	return out, nil
}

// Account returns one account. Every trust reader may see accounts.
func (s *Service) Account(ctx context.Context, who domain.Principal, id domain.ID) (Account, error) {
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, ""); err != nil {
		return Account{}, err
	}
	return s.account(ctx, id)
}

// Accounts lists accounts, optionally only active or inactive ones.
func (s *Service) Accounts(ctx context.Context, who domain.Principal, active *bool, cursor string, limit int) (Page[Account], error) {
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, ""); err != nil {
		return Page[Account]{}, err
	}
	limit, err := s.pageLimit(ctx, limit)
	if err != nil {
		return Page[Account]{}, err
	}
	all, err := s.accounts(ctx)
	if err != nil {
		return Page[Account]{}, err
	}
	var rows []Account
	for _, a := range all {
		if active == nil || a.Active == *active {
			rows = append(rows, a)
		}
	}
	return paginate(rows, func(a Account) domain.ID { return a.ID }, cursor, limit), nil
}

// Flags.

// Flag returns one flag the caller may see.
func (s *Service) Flag(ctx context.Context, who domain.Principal, id domain.ID) (Flag, error) {
	f, err := s.flag(ctx, id)
	if err != nil {
		return Flag{}, err
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, f.entryMatter); err != nil {
		return Flag{}, err
	}
	err = s.showFlag(ctx, &readable{s: s, who: who, seen: map[domain.ID]bool{f.entryMatter: true}}, &f)
	return f, err
}

// showFlag sets a client or account flag's balance_cents to the sum of its parts on
// matters rd may read, as of the entry that raised it. The stored overall total includes
// walled matters, so it is shown to nobody who cannot read all of them (PRD 3).
func (s *Service) showFlag(ctx context.Context, rd *readable, f *Flag) error {
	if f.Scope == ScopeMatter {
		return nil
	}
	parts, err := s.flagParts(ctx, f.ID)
	if err != nil {
		return err
	}
	f.BalanceCents, err = parts.shown(ctx, rd.ok)
	return err
}

// FlagFilter selects flags. Status is open or resolved; empty is both.
type FlagFilter struct {
	Status         string
	MatterID       domain.ID
	TrustAccountID domain.ID
	Scope          string
	Cursor         string
	Limit          int
}

// Flags lists flags the caller may see. A flag raised by an entry on a walled matter is
// absent, whatever its scope.
func (s *Service) Flags(ctx context.Context, who domain.Principal, f FlagFilter) (Page[Flag], error) {
	if f.Status != "" && f.Status != "open" && f.Status != "resolved" {
		return Page[Flag]{}, domain.Invalid("status", "status is open or resolved")
	}
	if f.Scope != "" && f.Scope != ScopeMatter && f.Scope != ScopeClient && f.Scope != ScopeAccount {
		return Page[Flag]{}, domain.Invalid("flag_scope", "flag_scope is matter, client or account")
	}
	rows, err := s.visibleFlags(ctx, who, f)
	if err != nil {
		return Page[Flag]{}, err
	}
	limit, err := s.pageLimit(ctx, f.Limit)
	if err != nil {
		return Page[Flag]{}, err
	}
	return paginate(rows, func(x Flag) domain.ID { return x.ID }, f.Cursor, limit), nil
}

func (s *Service) visibleFlags(ctx context.Context, who domain.Principal, f FlagFilter) ([]Flag, error) {
	rd, err := s.reader(ctx, who)
	if err != nil {
		return nil, err
	}
	all, err := s.flags(ctx)
	if err != nil {
		return nil, err
	}
	var rows []Flag
	for _, fl := range all {
		if (f.Status != "" && fl.Status != f.Status) || (f.Scope != "" && fl.Scope != f.Scope) ||
			(!f.MatterID.IsZero() && fl.entryMatter != f.MatterID) || (!f.TrustAccountID.IsZero() && fl.entryAccount != f.TrustAccountID) {
			continue
		}
		ok, err := rd.ok(ctx, fl.entryMatter)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if err := s.showFlag(ctx, rd, &fl); err != nil {
			return nil, err
		}
		rows = append(rows, fl)
	}
	return rows, nil
}

// ResolveFlag closes an open flag with a note. Only an attorney or admin may. The ledger
// entry that raised it stays as it is.
func (s *Service) ResolveFlag(ctx context.Context, who domain.Principal, id domain.ID, note string) (Flag, error) {
	note = trimmed(note)
	if note == "" {
		return Flag{}, domain.Invalid("note", "a resolution note is required")
	}
	f, err := s.flag(ctx, id)
	if err != nil {
		return Flag{}, err
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionTrustWithdraw, f.entryMatter); err != nil {
		return Flag{}, err
	}
	joined, err := s.tx(ctx, func(ctx context.Context) error {
		if err := s.lock(ctx); err != nil {
			return err
		}
		cur, err := s.flag(ctx, id)
		if err != nil {
			return err
		}
		if cur.Status != "open" {
			return domain.Conflict("the flag is already resolved")
		}
		now := s.now()
		if _, err := s.q(ctx).ExecContext(ctx, `UPDATE trust_flags SET resolved_at = $1, resolved_by = $2, resolution_note = $3, updated_at = $1 WHERE id = $4`,
			micro(now), string(actorID(who)), note, string(id)); err != nil {
			return err
		}
		if err := s.record(ctx, who, "trust_flag", id, "resolution_note", "", note, "resolved"); err != nil {
			return err
		}
		if f, err = s.flag(ctx, id); err != nil {
			return err
		}
		return s.showFlag(ctx, &readable{s: s, who: who, seen: map[domain.ID]bool{f.entryMatter: true}}, &f)
	})
	if err != nil {
		return Flag{}, err
	}
	s.publish(ctx, joined, []Event{{Name: "trust.flag_resolved", RecordID: id, MatterID: f.entryMatter}})
	return f, nil
}
