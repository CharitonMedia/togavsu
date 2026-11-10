package billing

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"sort"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Report is a ReportTable: named columns and one map per row. CSV writes the same rows.
type Report struct {
	Report  string           `json:"report"`
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
}

// WriteCSV writes the report as CSV with a header row.
func (r Report) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(r.Columns); err != nil {
		return err
	}
	for _, row := range r.Rows {
		rec := make([]string, len(r.Columns))
		for i, c := range r.Columns {
			rec[i] = cell(row[c])
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return csvSafe(x)
	case domain.ID:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

// csvSafe keeps a spreadsheet from running a cell as a formula.
func csvSafe(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@' || s[0] == '\t' || s[0] == '\r') {
		return "'" + s
	}
	return s
}

// ReportFilter is the report query.
type ReportFilter struct {
	MatterID domain.ID
	UserID   domain.ID
	FromDate string
	ToDate   string
	AsOf     string
}

func (s *Service) reportGate(ctx context.Context, p domain.Principal, f ReportFilter) (func(domain.ID) (bool, error), error) {
	if err := s.readBilling(ctx, p, ""); err != nil {
		return nil, err
	}
	for _, d := range []struct{ field, v string }{{"from_date", f.FromDate}, {"to_date", f.ToDate}, {"as_of", f.AsOf}} {
		if d.v != "" {
			if _, err := parseDate(d.field, d.v); err != nil {
				return nil, err
			}
		}
	}
	if f.FromDate != "" && f.ToDate != "" && f.FromDate > f.ToDate {
		return nil, domain.Invalid("from_date", "must be on or before to_date")
	}
	if !f.MatterID.IsZero() {
		if err := s.readBilling(ctx, p, f.MatterID); err != nil {
			return nil, err
		}
	}
	seen := map[domain.ID]bool{}
	return func(m domain.ID) (bool, error) {
		if !f.MatterID.IsZero() && m != f.MatterID {
			return false, nil
		}
		if ok, cached := seen[m]; cached {
			return ok, nil
		}
		ok, err := s.visible(ctx, p, m)
		seen[m] = ok
		return ok, err
	}, nil
}

func (s *Service) matterNumber(ctx context.Context, id domain.ID) string {
	if m, err := s.d.Practice.Matter(ctx, id); err == nil {
		return m.Number
	}
	return ""
}

func inRange(date, from, to string) bool {
	return (from == "" || date >= from) && (to == "" || date <= to)
}

// UnbilledReport lists unbilled billable time and expenses.
func (s *Service) UnbilledReport(ctx context.Context, p domain.Principal, f ReportFilter) (Report, error) {
	ok, err := s.reportGate(ctx, p, f)
	if err != nil {
		return Report{}, err
	}
	r := Report{Report: "unbilled-time", Columns: []string{"kind", "id", "date", "matter_id", "matter_number", "user_id", "user", "description", "hours", "rate_cents", "amount_cents"}, Rows: []map[string]any{}}
	times, err := listOf[TimeEntry](ctx, s.d.Store, kindTime)
	if err != nil {
		return Report{}, err
	}
	for _, e := range times {
		if !e.InvoiceID.IsZero() || !e.Billable || !inRange(e.WorkedOn, f.FromDate, f.ToDate) || !f.UserID.IsZero() && e.UserID != f.UserID {
			continue
		}
		if vis, err := ok(e.MatterID); err != nil {
			return Report{}, err
		} else if !vis {
			continue
		}
		amount, err := timeAmount(e.RateCents, e.Minutes())
		if err != nil {
			return Report{}, err
		}
		r.Rows = append(r.Rows, map[string]any{"kind": "time", "id": e.ID, "date": e.WorkedOn, "matter_id": e.MatterID,
			"matter_number": s.matterNumber(ctx, e.MatterID), "user_id": e.UserID, "user": s.userName(ctx, e.UserID),
			"description": e.Description, "hours": hours(e.Minutes()), "rate_cents": int64(e.RateCents), "amount_cents": int64(amount)})
	}
	if f.UserID.IsZero() {
		costs, err := listOf[Expense](ctx, s.d.Store, kindExpense)
		if err != nil {
			return Report{}, err
		}
		for _, e := range costs {
			if !e.InvoiceID.IsZero() || !e.Billable || !inRange(e.IncurredOn, f.FromDate, f.ToDate) {
				continue
			}
			if vis, err := ok(e.MatterID); err != nil {
				return Report{}, err
			} else if !vis {
				continue
			}
			amount, err := expenseAmount(e.AmountCents, e.MarkupPercent)
			if err != nil {
				return Report{}, err
			}
			r.Rows = append(r.Rows, map[string]any{"kind": "expense", "id": e.ID, "date": e.IncurredOn, "matter_id": e.MatterID,
				"matter_number": s.matterNumber(ctx, e.MatterID), "user_id": "", "user": "", "description": e.Description,
				"hours": "", "rate_cents": "", "amount_cents": int64(amount)})
		}
	}
	return r, nil
}

// TimeByUserReport totals time per user in the date range.
func (s *Service) TimeByUserReport(ctx context.Context, p domain.Principal, f ReportFilter) (Report, error) {
	ok, err := s.reportGate(ctx, p, f)
	if err != nil {
		return Report{}, err
	}
	type tot struct {
		entries             int
		minutes, billMin    int64
		billable, billedAmt domain.Cents
	}
	by := map[domain.ID]*tot{}
	times, err := listOf[TimeEntry](ctx, s.d.Store, kindTime)
	if err != nil {
		return Report{}, err
	}
	for _, e := range times {
		if !inRange(e.WorkedOn, f.FromDate, f.ToDate) || !f.UserID.IsZero() && e.UserID != f.UserID {
			continue
		}
		if vis, err := ok(e.MatterID); err != nil {
			return Report{}, err
		} else if !vis {
			continue
		}
		t := by[e.UserID]
		if t == nil {
			t = &tot{}
			by[e.UserID] = t
		}
		t.entries++
		t.minutes += e.Minutes()
		if e.Billable {
			amount, err := timeAmount(e.RateCents, e.Minutes())
			if err != nil {
				return Report{}, err
			}
			t.billMin += e.Minutes()
			if t.billable, err = t.billable.Add(amount); err != nil {
				return Report{}, err
			}
			if !e.InvoiceID.IsZero() {
				if t.billedAmt, err = t.billedAmt.Add(amount); err != nil {
					return Report{}, err
				}
			}
		}
	}
	r := Report{Report: "time-by-user", Columns: []string{"user_id", "user", "entries", "hours", "billable_hours", "billable_cents", "billed_cents"}, Rows: []map[string]any{}}
	ids := make([]domain.ID, 0, len(by))
	for id := range by {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return s.userName(ctx, ids[i]) < s.userName(ctx, ids[j]) })
	for _, id := range ids {
		t := by[id]
		r.Rows = append(r.Rows, map[string]any{"user_id": id, "user": s.userName(ctx, id), "entries": t.entries,
			"hours": hours(t.minutes), "billable_hours": hours(t.billMin), "billable_cents": int64(t.billable), "billed_cents": int64(t.billedAmt)})
	}
	return r, nil
}

func agingBucket(days int) string {
	switch {
	case days <= 0:
		return "current"
	case days <= 30:
		return "1-30"
	case days <= 60:
		return "31-60"
	case days <= 90:
		return "61-90"
	}
	return "91+"
}

// ARAgingReport lists each invoice with a balance on the as-of date by days past due.
// Payments received after as_of, and write-offs after it, still count as owed. Zero-fee
// matters are omitted.
func (s *Service) ARAgingReport(ctx context.Context, p domain.Principal, f ReportFilter) (Report, error) {
	ok, err := s.reportGate(ctx, p, f)
	if err != nil {
		return Report{}, err
	}
	asOf := f.AsOf
	if asOf == "" {
		asOf = s.today(ctx)
	}
	invs, err := listOf[Invoice](ctx, s.d.Store, kindInvoice)
	if err != nil {
		return Report{}, err
	}
	pays, err := listOf[Payment](ctx, s.d.Store, kindPayment)
	if err != nil {
		return Report{}, err
	}
	r := Report{Report: "ar-aging", Columns: []string{"invoice_id", "number", "matter_id", "matter_number", "issued_on", "due_on", "days_past_due", "bucket", "balance_cents"}, Rows: []map[string]any{}}
	for _, inv := range invs {
		if inv.Status == StatusDraft || inv.IssuedOn > asOf || s.zeroFee(inv.MatterID) {
			continue
		}
		if inv.Status == StatusVoid && inv.VoidedAt != nil && inv.VoidedAt.Format(dateLayout) <= asOf {
			continue
		}
		if vis, err := ok(inv.MatterID); err != nil {
			return Report{}, err
		} else if !vis {
			continue
		}
		owed := inv.TotalCents
		for _, pay := range pays {
			if pay.InvoiceID != inv.ID || pay.ReceivedOn > asOf || pay.Status == PayPending || pay.Status == PayFailed {
				continue
			}
			if pay.Status == PayReversed && pay.ReversedAt != nil && pay.ReversedAt.Format(dateLayout) <= asOf {
				continue
			}
			if owed, err = owed.Sub(pay.AmountCents); err != nil {
				return Report{}, err
			}
		}
		if inv.WriteOffCents > 0 && inv.WriteOffOn <= asOf {
			if owed, err = owed.Sub(inv.WriteOffCents); err != nil {
				return Report{}, err
			}
		}
		if owed <= 0 {
			continue
		}
		days := daysBetween(inv.DueOn, asOf)
		r.Rows = append(r.Rows, map[string]any{"invoice_id": inv.ID, "number": inv.Number, "matter_id": inv.MatterID,
			"matter_number": s.matterNumber(ctx, inv.MatterID), "issued_on": inv.IssuedOn, "due_on": inv.DueOn,
			"days_past_due": max(days, 0), "bucket": agingBucket(days), "balance_cents": int64(owed)})
	}
	return r, nil
}

// PaymentsReport lists completed payments received in the range.
func (s *Service) PaymentsReport(ctx context.Context, p domain.Principal, f ReportFilter) (Report, error) {
	ok, err := s.reportGate(ctx, p, f)
	if err != nil {
		return Report{}, err
	}
	pays, err := listOf[Payment](ctx, s.d.Store, kindPayment)
	if err != nil {
		return Report{}, err
	}
	r := Report{Report: "payments-received", Columns: []string{"payment_id", "received_on", "matter_id", "matter_number", "contact", "method", "destination", "reference_number", "invoice_number", "amount_cents"}, Rows: []map[string]any{}}
	for _, pay := range pays {
		if pay.Status != PayCompleted || !inRange(pay.ReceivedOn, f.FromDate, f.ToDate) {
			continue
		}
		if vis, err := ok(pay.MatterID); err != nil {
			return Report{}, err
		} else if !vis {
			continue
		}
		number := ""
		if !pay.InvoiceID.IsZero() {
			if inv, err := s.invoice(ctx, pay.InvoiceID); err == nil {
				number = inv.Number
			}
		}
		r.Rows = append(r.Rows, map[string]any{"payment_id": pay.ID, "received_on": pay.ReceivedOn, "matter_id": pay.MatterID,
			"matter_number": s.matterNumber(ctx, pay.MatterID), "contact": s.clientName(ctx, pay.ContactID), "method": pay.Method,
			"destination": pay.Destination, "reference_number": pay.ReferenceNumber, "invoice_number": number, "amount_cents": int64(pay.AmountCents)})
	}
	return r, nil
}

// WriteOffsReport lists invoice write-offs in the range.
func (s *Service) WriteOffsReport(ctx context.Context, p domain.Principal, f ReportFilter) (Report, error) {
	ok, err := s.reportGate(ctx, p, f)
	if err != nil {
		return Report{}, err
	}
	invs, err := listOf[Invoice](ctx, s.d.Store, kindInvoice)
	if err != nil {
		return Report{}, err
	}
	r := Report{Report: "write-offs", Columns: []string{"invoice_id", "number", "matter_id", "matter_number", "written_off_on", "reason", "amount_cents"}, Rows: []map[string]any{}}
	for _, inv := range invs {
		if inv.WriteOffCents == 0 || !inRange(inv.WriteOffOn, f.FromDate, f.ToDate) {
			continue
		}
		if vis, err := ok(inv.MatterID); err != nil {
			return Report{}, err
		} else if !vis {
			continue
		}
		r.Rows = append(r.Rows, map[string]any{"invoice_id": inv.ID, "number": inv.Number, "matter_id": inv.MatterID,
			"matter_number": s.matterNumber(ctx, inv.MatterID), "written_off_on": inv.WriteOffOn, "reason": inv.WriteOffReason,
			"amount_cents": int64(inv.WriteOffCents)})
	}
	return r, nil
}
