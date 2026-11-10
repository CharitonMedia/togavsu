package trust

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Queries are portable SQL with $n placeholders: no jsonb, arrays or procedures.

const entryCols = `id, created_at, created_by, trust_account_id, client_id, matter_id, entry_type, amount_cents, method, description, reverses_id`

type scanner interface{ Scan(...any) error }

func scanEntry(sc scanner) (Entry, error) {
	var e Entry
	var created, amount int64
	var rev sql.NullString
	if err := sc.Scan(&e.ID, &created, &e.CreatedBy, &e.TrustAccountID, &e.ClientID, &e.MatterID, &e.StoredType, &amount, &e.Method, &e.Description, &rev); err != nil {
		return Entry{}, err
	}
	e.CreatedAt = fromMicro(created)
	e.Signed = domain.Cents(amount)
	e.EntryType = e.StoredType
	e.AmountCents = amount
	switch {
	case rev.Valid && rev.String != "":
		id := domain.ID(rev.String)
		e.ReversesEntryID = &id
		e.EntryType = TypeReversal
		e.Direction = direction(amount)
	case e.StoredType == TypeAdjustment:
		e.AdjustmentDirection = direction(amount)
	case negative(amount):
		e.AmountCents = -amount
	}
	return e, nil
}

func direction(amount int64) string {
	if negative(amount) {
		return Debit
	}
	return Credit
}

func (s *Service) entry(ctx context.Context, id domain.ID) (Entry, error) {
	row := s.q(ctx).QueryRowContext(ctx, `SELECT `+entryCols+` FROM trust_ledger WHERE id = $1`, string(id))
	e, err := scanEntry(row)
	return e, notFound(err, "trust_entry")
}

func (s *Service) entries(ctx context.Context, f EntryFilter) ([]Entry, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, cond+" $"+strconv.Itoa(len(args)))
	}
	if !f.MatterID.IsZero() {
		add("matter_id =", string(f.MatterID))
	}
	if !f.ClientID.IsZero() {
		add("client_id =", string(f.ClientID))
	}
	if !f.TrustAccountID.IsZero() {
		add("trust_account_id =", string(f.TrustAccountID))
	}
	if f.FromDate != "" {
		add("created_at >=", dayStart(f.FromDate))
	}
	if f.ToDate != "" {
		add("created_at <", dayEnd(f.ToDate))
	}
	q := `SELECT ` + entryCols + ` FROM trust_ledger`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	rows, err := s.q(ctx).QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) insertEntry(ctx context.Context, e Entry) error {
	var rev any
	if e.ReversesEntryID != nil {
		rev = string(*e.ReversesEntryID)
	}
	_, err := s.q(ctx).ExecContext(ctx, `INSERT INTO trust_ledger (id, created_at, updated_at, created_by, trust_account_id, client_id, matter_id, entry_type, amount_cents, method, description, reverses_id)
		VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		string(e.ID), micro(e.CreatedAt), string(e.CreatedBy), string(e.TrustAccountID), string(e.ClientID), string(e.MatterID),
		e.StoredType, int64(e.Signed), e.Method, e.Description, rev)
	return err
}

// sum runs one SUM over the ledger. The ledger is the source of truth for every balance.
func (s *Service) sum(ctx context.Context, where string, args ...any) (int64, error) {
	var v sql.NullInt64
	err := s.q(ctx).QueryRowContext(ctx, `SELECT SUM(amount_cents) FROM trust_ledger WHERE `+where, args...).Scan(&v)
	return v.Int64, err
}

func (s *Service) balances(ctx context.Context, account, client, matter domain.ID) (Balances, error) {
	var b Balances
	var err error
	if b.MatterCents, err = s.sum(ctx, `trust_account_id = $1 AND client_id = $2 AND matter_id = $3`, string(account), string(client), string(matter)); err != nil {
		return b, err
	}
	if b.ClientCents, err = s.sum(ctx, `client_id = $1`, string(client)); err != nil {
		return b, err
	}
	b.AccountCents, err = s.sum(ctx, `trust_account_id = $1`, string(account))
	return b, err
}

// clientMatterCents is the client's balance on a matter across every account.
func (s *Service) clientMatterCents(ctx context.Context, client, matter domain.ID) (int64, error) {
	return s.sum(ctx, `client_id = $1 AND matter_id = $2`, string(client), string(matter))
}

// cache writes the per-matter balance row. Readers display it; the ledger stays the truth.
func (s *Service) cache(ctx context.Context, who domain.Principal, account, client, matter domain.ID, cents int64) (domain.TrustBalance, error) {
	now := s.now()
	row := domain.TrustBalance{TrustAccountID: account, ClientID: client, MatterID: matter, BalanceCents: domain.Cents(cents)}
	var id string
	var created int64
	var createdBy string
	err := s.q(ctx).QueryRowContext(ctx, `SELECT id, created_at, created_by FROM trust_balance_cache WHERE trust_account_id = $1 AND client_id = $2 AND matter_id = $3`,
		string(account), string(client), string(matter)).Scan(&id, &created, &createdBy)
	switch {
	case err == sql.ErrNoRows:
		nid, err := s.newID()
		if err != nil {
			return row, err
		}
		row.Meta = domain.Meta{ID: nid, CreatedAt: now, UpdatedAt: now, CreatedBy: actorID(who)}
		_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO trust_balance_cache (id, created_at, updated_at, created_by, trust_account_id, client_id, matter_id, balance_cents) VALUES ($1,$2,$2,$3,$4,$5,$6,$7)`,
			string(nid), micro(now), string(actorID(who)), string(account), string(client), string(matter), cents)
		return row, err
	case err != nil:
		return row, err
	}
	row.Meta = domain.Meta{ID: domain.ID(id), CreatedAt: fromMicro(created), UpdatedAt: now, CreatedBy: domain.ID(createdBy)}
	_, err = s.q(ctx).ExecContext(ctx, `UPDATE trust_balance_cache SET balance_cents = $1, updated_at = $2 WHERE id = $3`, cents, micro(now), id)
	return row, err
}

// Account reads.

// Flags are INTEGER 0/1 columns (portable SQL); the comparison reads them as booleans.
const accountCols = `id, created_at, created_by, updated_at, bank, name, iolta = 1, last_four, active = 1`

func scanAccount(sc scanner) (Account, error) {
	var a Account
	var created, updated int64
	if err := sc.Scan(&a.ID, &created, &a.CreatedBy, &updated, &a.Bank, &a.Name, &a.IOLTA, &a.LastFour, &a.Active); err != nil {
		return Account{}, err
	}
	a.CreatedAt, a.UpdatedAt = fromMicro(created), fromMicro(updated)
	return a, nil
}

func (s *Service) account(ctx context.Context, id domain.ID) (Account, error) {
	a, err := scanAccount(s.q(ctx).QueryRowContext(ctx, `SELECT `+accountCols+` FROM trust_accounts WHERE id = $1`, string(id)))
	return a, notFound(err, "trust_account")
}

func (s *Service) accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT `+accountCols+` FROM trust_accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Flags.

const flagCols = `id, created_at, created_by, updated_at, trust_entry_id, matter_id, client_id, reason, resolved_at, resolved_by, resolution_note, scope, trust_account_id, balance_cents`

func scanFlag(sc scanner) (Flag, error) {
	var f Flag
	var created, updated, resolved int64
	var resolvedBy, account sql.NullString
	if err := sc.Scan(&f.ID, &created, &f.CreatedBy, &updated, &f.TrustEntryID, &f.entryMatter, &f.entryClient, &f.reason, &resolved, &resolvedBy, &f.ResolutionNote, &f.Scope, &account, &f.BalanceCents); err != nil {
		return Flag{}, err
	}
	f.CreatedAt, f.UpdatedAt = fromMicro(created), fromMicro(updated)
	f.Status = "open"
	if resolved != int64(0) {
		t := fromMicro(resolved)
		by := domain.ID(resolvedBy.String)
		f.Status, f.ResolvedAt, f.ResolvedBy = "resolved", &t, &by
	}
	f.entryAccount = domain.ID(account.String)
	f.expose()
	return f, nil
}

// expose sets the public ids for the flag's scope from the stored posting ids, matching
// the contract's per-scope TrustFlag shapes (additionalProperties false): a matter flag
// has matter_id and client_id, a client flag client_id, an account flag trust_account_id.
func (f *Flag) expose() {
	f.MatterID, f.ClientID, f.TrustAccountID = "", "", ""
	switch f.Scope {
	case ScopeMatter:
		f.MatterID, f.ClientID = f.entryMatter, f.entryClient
	case ScopeClient:
		f.ClientID = f.entryClient
	case ScopeAccount:
		f.TrustAccountID = f.entryAccount
	}
}

func (s *Service) flag(ctx context.Context, id domain.ID) (Flag, error) {
	f, err := scanFlag(s.q(ctx).QueryRowContext(ctx, `SELECT `+flagCols+` FROM trust_flags WHERE id = $1`, string(id)))
	return f, notFound(err, "trust_flag")
}

func (s *Service) flags(ctx context.Context) ([]Flag, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT `+flagCols+` FROM trust_flags ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Flag
	for rows.Next() {
		f, err := scanFlag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Service) insertFlag(ctx context.Context, f Flag) error {
	var account any
	if !f.entryAccount.IsZero() {
		account = string(f.entryAccount)
	}
	_, err := s.q(ctx).ExecContext(ctx, `INSERT INTO trust_flags (id, created_at, updated_at, created_by, trust_entry_id, matter_id, client_id, reason, resolved_at, resolved_by, resolution_note, scope, trust_account_id, balance_cents)
		VALUES ($1,$2,$2,$3,$4,$5,$6,$7,0,NULL,'',$8,$9,$10)`,
		string(f.ID), micro(f.CreatedAt), string(f.CreatedBy), string(f.TrustEntryID), string(f.entryMatter), string(f.entryClient), f.reason, f.Scope, account, f.BalanceCents)
	return err
}

// Replenishment requests.

const replCols = `id, created_at, created_by, updated_at, matter_id, client_id, amount_cents, threshold_cents, message, status, invoice_id`

func scanRepl(sc scanner) (Replenishment, error) {
	var r Replenishment
	var created, updated, threshold int64
	var invoice sql.NullString
	if err := sc.Scan(&r.ID, &created, &r.CreatedBy, &updated, &r.MatterID, &r.ClientID, &r.AmountCents, &threshold, &r.Message, &r.Status, &invoice); err != nil {
		return Replenishment{}, err
	}
	r.CreatedAt, r.UpdatedAt = fromMicro(created), fromMicro(updated)
	// threshold_cents is NOT NULL in version 1; -1 stores "no threshold" (a collections request).
	if !negative(threshold) {
		r.ThresholdCents = &threshold
	}
	if invoice.Valid && invoice.String != "" {
		id := domain.ID(invoice.String)
		r.InvoiceID = &id
	}
	return r, nil
}

func (s *Service) replenishment(ctx context.Context, id domain.ID) (Replenishment, error) {
	r, err := scanRepl(s.q(ctx).QueryRowContext(ctx, `SELECT `+replCols+` FROM trust_replenishment_requests WHERE id = $1`, string(id)))
	return r, notFound(err, "trust_replenishment")
}

func (s *Service) replenishments(ctx context.Context, matter domain.ID, status string) ([]Replenishment, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT `+replCols+` FROM trust_replenishment_requests
		WHERE ($1 = '' OR matter_id = $1) AND ($2 = '' OR status = $2) ORDER BY id`, string(matter), status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Replenishment
	for rows.Next() {
		r, err := scanRepl(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) insertRepl(ctx context.Context, r Replenishment) error {
	threshold := int64(-1)
	if r.ThresholdCents != nil {
		threshold = *r.ThresholdCents
	}
	var invoice any
	if r.InvoiceID != nil {
		invoice = string(*r.InvoiceID)
	}
	_, err := s.q(ctx).ExecContext(ctx, `INSERT INTO trust_replenishment_requests (id, created_at, updated_at, created_by, matter_id, client_id, amount_cents, threshold_cents, message, status, invoice_id)
		VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		string(r.ID), micro(r.CreatedAt), string(r.CreatedBy), string(r.MatterID), string(r.ClientID), r.AmountCents, threshold, r.Message, r.Status, invoice)
	return err
}

// Reconciliations.

const reconCols = `id, created_at, created_by, updated_at, trust_account_id, statement_on, statement_balance_cents, ledger_total_cents, client_balance_cents, difference_cents`

func scanRecon(sc scanner) (Reconciliation, error) {
	var r Reconciliation
	var created, updated int64
	if err := sc.Scan(&r.ID, &created, &r.CreatedBy, &updated, &r.TrustAccountID, &r.StatementOn, &r.StatementBalanceCents, &r.LedgerTotalCents, &r.ClientBalancesSumCents, &r.StatementDifferenceCents); err != nil {
		return Reconciliation{}, err
	}
	r.CreatedAt, r.UpdatedAt = fromMicro(created), fromMicro(updated)
	r.ClientSumDifferenceCents = r.LedgerTotalCents - r.ClientBalancesSumCents
	return r, nil
}

func (s *Service) reconciliation(ctx context.Context, id domain.ID) (Reconciliation, error) {
	r, err := scanRecon(s.q(ctx).QueryRowContext(ctx, `SELECT `+reconCols+` FROM trust_reconciliations WHERE id = $1`, string(id)))
	return r, notFound(err, "trust_reconciliation")
}

func (s *Service) reconciliations(ctx context.Context, account domain.ID) ([]Reconciliation, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT `+reconCols+` FROM trust_reconciliations WHERE ($1 = '' OR trust_account_id = $1) ORDER BY id`, string(account))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reconciliation
	for rows.Next() {
		r, err := scanRecon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
