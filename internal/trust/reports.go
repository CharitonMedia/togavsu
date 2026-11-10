package trust

import (
	"bytes"
	"context"
	"encoding/csv"
	"sort"
	"strconv"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// BalancesReport is trust balances by client: one row per client per matter per
// account, with the client's name. A walled matter is absent.
func (s *Service) BalancesReport(ctx context.Context, who domain.Principal, account domain.ID) (Report, error) {
	rows, err := s.Balances(ctx, who, EntryFilter{TrustAccountID: account})
	if err != nil {
		return Report{}, err
	}
	rep := Report{Report: "trust-balances", Columns: []string{"client_id", "client", "matter_id", "matter", "trust_account_id", "balance"}, Rows: []map[string]string{}}
	for _, r := range rows {
		if r.Scope != ScopeMatter {
			continue
		}
		rep.Rows = append(rep.Rows, map[string]string{
			"client_id": string(r.ClientID), "client": s.clientName(ctx, r.ClientID),
			"matter_id": string(r.MatterID), "matter": s.matterLabel(ctx, r.MatterID),
			"trust_account_id": string(r.TrustAccountID), "balance": domain.Cents(r.BalanceCents).String(),
		})
	}
	sort.SliceStable(rep.Rows, func(i, j int) bool { return rep.Rows[i]["client"] < rep.Rows[j]["client"] })
	return rep, nil
}

// ClientLedger is one client's entries with a running balance, optionally one matter.
func (s *Service) ClientLedger(ctx context.Context, who domain.Principal, client, matter domain.ID) (Report, error) {
	if client.IsZero() {
		return Report{}, domain.Invalid("client_id", "client_id is required")
	}
	entries, err := s.visibleEntries(ctx, who, EntryFilter{ClientID: client, MatterID: matter})
	if err != nil {
		return Report{}, err
	}
	return ledgerReport("trust-client-ledger", entries), nil
}

// AccountLedger is one account's entries with a running balance between two dates. The
// opening balance row carries the visible entries before from_date.
func (s *Service) AccountLedger(ctx context.Context, who domain.Principal, account domain.ID, from, to string) (Report, error) {
	if account.IsZero() {
		return Report{}, domain.Invalid("trust_account_id", "trust_account_id is required")
	}
	entries, err := s.visibleEntries(ctx, who, EntryFilter{TrustAccountID: account, ToDate: to})
	if err != nil {
		return Report{}, err
	}
	if from != "" && !validDate(from) {
		return Report{}, domain.Invalid("from_date", "use YYYY-MM-DD")
	}
	var opening int64
	var in []Entry
	for _, e := range entries {
		if from != "" && micro(e.CreatedAt) < dayStart(from) {
			opening += int64(e.Signed)
			continue
		}
		in = append(in, e)
	}
	rep := ledgerReport("trust-account-ledger", in)
	if from != "" {
		run := opening
		for _, r := range rep.Rows {
			v, _ := domain.ParseCents(r["amount"])
			run += int64(v)
			r["balance"] = domain.Cents(run).String()
		}
		rep.Rows = append([]map[string]string{{"date": from, "entry_type": "opening_balance", "balance": domain.Cents(opening).String()}}, rep.Rows...)
	}
	return rep, nil
}

func ledgerReport(name string, entries []Entry) Report {
	rep := Report{Report: name, Columns: []string{"date", "entry_id", "trust_account_id", "client_id", "matter_id", "entry_type", "method", "description", "amount", "balance", "reverses_entry_id"}, Rows: []map[string]string{}}
	var run int64
	for _, e := range entries {
		run += int64(e.Signed)
		rev := ""
		if e.ReversesEntryID != nil {
			rev = string(*e.ReversesEntryID)
		}
		rep.Rows = append(rep.Rows, map[string]string{
			"date": e.CreatedAt.Format("2006-01-02"), "entry_id": string(e.ID), "trust_account_id": string(e.TrustAccountID),
			"client_id": string(e.ClientID), "matter_id": string(e.MatterID), "entry_type": e.EntryType, "method": e.Method,
			"description": e.Description, "amount": e.Signed.String(), "balance": domain.Cents(run).String(), "reverses_entry_id": rev,
		})
	}
	return rep
}

// ReconciliationReport is the reconciliation history.
func (s *Service) ReconciliationReport(ctx context.Context, who domain.Principal, account domain.ID) (Report, error) {
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, ""); err != nil {
		return Report{}, err
	}
	all, err := s.reconciliations(ctx, account)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Report: "reconciliations", Columns: []string{"statement_on", "trust_account_id", "statement_balance", "ledger_total", "client_balances_sum", "statement_difference", "client_sum_difference", "reconciled_by", "reconciliation_id"}, Rows: []map[string]string{}}
	for _, r := range all {
		rep.Rows = append(rep.Rows, map[string]string{
			"statement_on": r.StatementOn, "trust_account_id": string(r.TrustAccountID),
			"statement_balance": domain.Cents(r.StatementBalanceCents).String(), "ledger_total": domain.Cents(r.LedgerTotalCents).String(),
			"client_balances_sum": domain.Cents(r.ClientBalancesSumCents).String(), "statement_difference": domain.Cents(r.StatementDifferenceCents).String(),
			"client_sum_difference": domain.Cents(r.ClientSumDifferenceCents).String(), "reconciled_by": string(r.CreatedBy), "reconciliation_id": string(r.ID),
		})
	}
	return rep, nil
}

// CSV renders the report rows in column order.
func (r Report) CSV() ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	if err := w.Write(r.Columns); err != nil {
		return nil, err
	}
	for _, row := range r.Rows {
		rec := make([]string, len(r.Columns))
		for i, c := range r.Columns {
			rec[i] = csvSafe(row[c])
		}
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

// csvSafe stops a spreadsheet from running a description as a formula. Amounts such as
// -12.00 stay numbers.
func csvSafe(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '@', '\t', '\r':
		return "'" + v
	case '-':
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return "'" + v
		}
	}
	return v
}

func (s *Service) clientName(ctx context.Context, id domain.ID) string {
	c, err := s.practice.Contact(ctx, id)
	if err != nil {
		return ""
	}
	if c.FullName != "" {
		return c.FullName
	}
	return c.GivenName + " " + c.FamilyName
}

func (s *Service) matterLabel(ctx context.Context, id domain.ID) string {
	m, err := s.practice.Matter(ctx, id)
	if err != nil {
		return ""
	}
	return m.Number + " " + m.Name
}

// TrustAlertsWidget is the open flags the caller may see (home screen, trust alerts).
func (s *Service) TrustAlertsWidget(ctx context.Context, who domain.Principal, matter domain.ID) (Widget, error) {
	flags, err := s.visibleFlags(ctx, who, FlagFilter{Status: "open", MatterID: matter})
	if err != nil {
		return Widget{}, err
	}
	q := map[string]string{"status": "open"}
	if !matter.IsZero() {
		q["matter_id"] = string(matter)
	}
	w := Widget{ID: "trust-alerts", Title: "Trust alerts", Target: Target{Path: "/trust/flags", Query: q}, PagePath: "/trust/flags", Rows: []WidgetRow{}}
	for _, f := range flags {
		w.Rows = append(w.Rows, WidgetRow{ID: string(f.ID), Title: s.matterLabel(ctx, f.entryMatter),
			Subtitle: f.Scope + " balance " + domain.Cents(f.BalanceCents).String(), TargetPath: "/trust/flags/" + string(f.ID)})
	}
	return w, nil
}

// ReplenishmentWidget is the active replenishment requests the caller may see.
func (s *Service) ReplenishmentWidget(ctx context.Context, who domain.Principal, matter domain.ID) (Widget, error) {
	repls, err := s.visibleRepls(ctx, who, matter, ReplenishActive)
	if err != nil {
		return Widget{}, err
	}
	q := map[string]string{"status": ReplenishActive}
	if !matter.IsZero() {
		q["matter_id"] = string(matter)
	}
	w := Widget{ID: "replenishment-requests", Title: "Replenishment requests", Target: Target{Path: "/trust/replenishment", Query: q}, PagePath: "/trust/replenishment", Rows: []WidgetRow{}}
	for _, r := range repls {
		w.Rows = append(w.Rows, WidgetRow{ID: string(r.ID), Title: s.matterLabel(ctx, r.MatterID),
			Subtitle: "requested " + domain.Cents(r.AmountCents).String(), TargetPath: "/trust/replenishment#" + string(r.ID)})
	}
	return w, nil
}
