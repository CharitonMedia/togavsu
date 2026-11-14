package billing

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Report is a ReportTable: named columns and one map per row. CSV writes the same rows.
type Report struct {
	Report  string           `json:"report"`
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	// total, when above len(Rows), is how many rows the whole report has: a screen's
	// report holds only its first rows.
	total int
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

// unbilledColumns are the unbilled report's columns.
var unbilledColumns = []string{"kind", "id", "date", "matter_id", "matter_number", "user_id", "user", "description", "hours", "rate_cents", "amount_cents"}

// UnbilledReport lists unbilled billable time and expenses, from the unbilled projection
// (migration 1261) rather than every time entry.
func (s *Service) UnbilledReport(ctx context.Context, p domain.Principal, f ReportFilter) (Report, error) {
	r := Report{Report: "unbilled-time", Columns: unbilledColumns, Rows: []map[string]any{}}
	err := s.unbilledEach(ctx, p, f, func(l *unbilledLine) error {
		r.Rows = append(r.Rows, l.row())
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	return r, nil
}

// unbilledPage is the unbilled report screen's first max rows (zero is every row), with
// the whole report's row count from the per-matter totals; it reads rows only until the
// page is full.
func (s *Service) unbilledPage(ctx context.Context, p domain.Principal, f ReportFilter, max int) (Report, error) {
	totals, err := s.UnbilledTotals(ctx, p, f)
	if err != nil {
		return Report{}, err
	}
	r := Report{Report: "unbilled-time", Columns: unbilledColumns, Rows: []map[string]any{}}
	for _, t := range totals {
		r.total += t.Items
	}
	var batch int
	if positive(int64(max)) {
		batch = max + 1
	}
	err = s.unbilledRows(ctx, p, f, batch, func(l *unbilledLine) error {
		if positive(int64(max)) && len(r.Rows) >= max {
			return errPageFull
		}
		r.Rows = append(r.Rows, l.row())
		return nil
	})
	if err != nil && !errors.Is(err, errPageFull) {
		return Report{}, err
	}
	return r, nil
}

// errPageFull stops a row walk once a screen has its page.
var errPageFull = errors.New("billing: page full")

// unbilledLine is one row of the unbilled report.
type unbilledLine struct {
	Expense      bool
	Row          *unbilledRow
	Amount       domain.Cents
	MatterNumber string
	User         string
}

func (l *unbilledLine) row() map[string]any {
	e := l.Row
	if l.Expense {
		return map[string]any{"kind": "expense", "id": e.ID, "date": e.Date, "matter_id": e.MatterID,
			"matter_number": l.MatterNumber, "user_id": "", "user": "", "description": e.Description,
			"hours": "", "rate_cents": "", "amount_cents": int64(l.Amount)}
	}
	return map[string]any{"kind": "time", "id": e.ID, "date": e.Date, "matter_id": e.MatterID,
		"matter_number": l.MatterNumber, "user_id": e.UserID, "user": l.User,
		"description": e.Description, "hours": hours(e.Minutes), "rate_cents": int64(e.RateCents), "amount_cents": int64(l.Amount)}
}

// appendJSON appends the line as the object json.Encoder writes for row(): keys sorted.
func (l *unbilledLine) appendJSON(b []byte) []byte {
	e := l.Row
	b = append(b, `{"amount_cents":`...)
	b = strconv.AppendInt(b, int64(l.Amount), 10)
	b = append(b, `,"date":`...)
	b = appendJSONString(b, e.Date)
	b = append(b, `,"description":`...)
	b = appendJSONString(b, e.Description)
	b = append(b, `,"hours":`...)
	if l.Expense {
		b = append(b, `""`...)
	} else {
		b = append(b, '"')
		b = appendHours(b, e.Minutes)
		b = append(b, '"')
	}
	b = append(b, `,"id":`...)
	b = appendJSONString(b, string(e.ID))
	if l.Expense {
		b = append(b, `,"kind":"expense","matter_id":`...)
	} else {
		b = append(b, `,"kind":"time","matter_id":`...)
	}
	b = appendJSONString(b, string(e.MatterID))
	b = append(b, `,"matter_number":`...)
	b = appendJSONString(b, l.MatterNumber)
	if l.Expense {
		return append(b, `,"rate_cents":"","user":"","user_id":""}`...)
	}
	b = append(b, `,"rate_cents":`...)
	b = strconv.AppendInt(b, int64(e.RateCents), 10)
	b = append(b, `,"user":`...)
	b = appendJSONString(b, l.User)
	b = append(b, `,"user_id":`...)
	b = appendJSONString(b, string(e.UserID))
	return append(b, '}')
}

// unbilledEach calls fn with each unbilled line p may read: time in id order, then (with
// no user filter) expenses in id order. fn must not keep the line or its row.
func (s *Service) unbilledEach(ctx context.Context, p domain.Principal, f ReportFilter, fn func(*unbilledLine) error) error {
	var stream int // no batch size: one streamed read
	return s.unbilledRows(ctx, p, f, stream, fn)
}

// unbilledRows is unbilledEach reading batch rows per query when batch is set, for a
// caller that stops early.
func (s *Service) unbilledRows(ctx context.Context, p domain.Principal, f ReportFilter, batch int, fn func(*unbilledLine) error) error {
	ok, err := s.reportGate(ctx, p, f)
	if err != nil {
		return err
	}
	names := s.lookups(ctx)
	if f.MatterID.IsZero() {
		names.allMatters()
	}
	// Each matter is checked once per report (the gate decides every matter it reaches);
	// rows come in id order, which mostly keeps a matter's rows together, so the previous
	// row's matter and user answer most rows without a map lookup.
	type seen struct {
		vis    bool
		number string
	}
	matters := map[domain.ID]seen{}
	var lastMatter, lastUser domain.ID
	var lastSeen seen
	var lastName string
	matter := func(id domain.ID) (seen, error) {
		if id == lastMatter && id != "" {
			return lastSeen, nil
		}
		m, hit := matters[id]
		if !hit {
			vis, err := ok(id)
			if err != nil {
				return seen{}, err
			}
			m = seen{vis: vis}
			if vis {
				m.number = names.matter(id)
			}
			matters[id] = m
		}
		lastMatter, lastSeen = id, m
		return m, nil
	}
	var l unbilledLine
	err = s.d.Store.Unbilled(ctx, kindTime, Where{MatterID: f.MatterID, UserID: f.UserID, FromDate: f.FromDate, ToDate: f.ToDate, Limit: batch}, func(e *unbilledRow) error {
		m, err := matter(e.MatterID)
		if err != nil || !m.vis {
			return err
		}
		amount, err := timeAmount(e.RateCents, e.Minutes)
		if err != nil {
			return err
		}
		if e.UserID != lastUser || lastUser == "" {
			lastUser, lastName = e.UserID, names.user(e.UserID)
		}
		l = unbilledLine{Row: e, Amount: amount, MatterNumber: m.number, User: lastName}
		return fn(&l)
	})
	if err != nil || !f.UserID.IsZero() {
		return err
	}
	return s.d.Store.Unbilled(ctx, kindExpense, Where{MatterID: f.MatterID, FromDate: f.FromDate, ToDate: f.ToDate, Limit: batch}, func(e *unbilledRow) error {
		m, err := matter(e.MatterID)
		if err != nil || !m.vis {
			return err
		}
		amount, err := expenseAmount(e.AmountCents, e.MarkupPercent)
		if err != nil {
			return err
		}
		l = unbilledLine{Expense: true, Row: e, Amount: amount, MatterNumber: m.number}
		return fn(&l)
	})
}

// UnbilledJSON writes the unbilled report as UnbilledReport's JSON without building a
// map per row (the report at the AC-017 seed is about 100,000 rows), streaming it to the
// writer start returns. start is called once, before the first byte, only after the
// access checks and the first read succeed. started reports whether it was called, so a
// caller can still answer an earlier error with an error response.
func (s *Service) UnbilledJSON(ctx context.Context, p domain.Principal, f ReportFilter, start func() io.Writer) (started bool, err error) {
	const chunk = 64 << 10
	b := make([]byte, 0, chunk+4096)
	var out io.Writer
	flush := func() error {
		if out == nil {
			out, started = start(), true
		}
		_, err := out.Write(b)
		b = b[:0]
		return err
	}
	b = append(b, `{"report":"unbilled-time","columns":`...)
	cols, _ := json.Marshal(unbilledColumns)
	b = append(b, cols...)
	b = append(b, `,"rows":[`...)
	first := true
	err = s.unbilledEach(ctx, p, f, func(l *unbilledLine) error {
		if !first {
			b = append(b, ',')
		}
		first = false
		b = l.appendJSON(b)
		if len(b) >= chunk {
			return flush()
		}
		return nil
	})
	if err != nil {
		return started, err
	}
	b = append(b, "]}\n"...)
	return true, flush()
}

// UnbilledSummary totals the unbilled report per matter for p, in report order (matters
// by their first time row, then matters with only expenses), from one aggregate per kind
// rather than the report's rows. Every matter is checked through the report gate.
func (s *Service) UnbilledSummary(ctx context.Context, p domain.Principal, f ReportFilter) ([]UnbilledTotal, error) {
	out, err := s.UnbilledTotals(ctx, p, f)
	names := s.lookups(ctx)
	for i := range out {
		out[i].MatterNumber = names.matter(out[i].MatterID)
	}
	return out, err
}

// UnbilledTotals is UnbilledSummary without matter numbers, for callers that show only
// some matters (MatterNumber names those) or only count.
func (s *Service) UnbilledTotals(ctx context.Context, p domain.Principal, f ReportFilter) ([]UnbilledTotal, error) {
	ok, err := s.reportGate(ctx, p, f)
	if err != nil {
		return nil, err
	}
	times, err := s.d.Store.UnbilledTotals(ctx, kindTime, Where{MatterID: f.MatterID, UserID: f.UserID, FromDate: f.FromDate, ToDate: f.ToDate})
	if err != nil {
		return nil, err
	}
	var costs []matterTotal
	if f.UserID.IsZero() {
		if costs, err = s.d.Store.UnbilledTotals(ctx, kindExpense, Where{MatterID: f.MatterID, FromDate: f.FromDate, ToDate: f.ToDate}); err != nil {
			return nil, err
		}
	}
	at := map[domain.ID]int{}
	var out []UnbilledTotal
	for _, list := range [][]matterTotal{times, costs} {
		for _, t := range list {
			vis, err := ok(t.MatterID)
			if err != nil {
				return nil, err
			}
			if !vis {
				continue
			}
			i, seen := at[t.MatterID]
			if !seen {
				i = len(out)
				at[t.MatterID] = i
				out = append(out, UnbilledTotal{MatterID: t.MatterID})
			}
			out[i].Items += t.Items
			out[i].Cents += t.Cents
		}
	}
	return out, nil
}

// UnbilledTotal is one matter's unbilled items and amount.
type UnbilledTotal struct {
	MatterID     domain.ID
	MatterNumber string
	Items        int
	Cents        domain.Cents
}

// MatterNumber is the matter's number, or "" for none; a label, not an access decision.
func (s *Service) MatterNumber(ctx context.Context, id domain.ID) string {
	return s.matterNumber(ctx, id)
}

// lookups names matters and users for one report, reading each once. They are display
// labels, not access decisions: the report gate checks every matter itself.
type lookups struct {
	s       *Service
	ctx     context.Context
	matters map[domain.ID]string
	users   map[domain.ID]string
}

func (s *Service) lookups(ctx context.Context) *lookups {
	return &lookups{s: s, ctx: ctx, matters: map[domain.ID]string{}, users: map[domain.ID]string{}}
}

// allMatters reads every matter's number in one pass, for a report that names most of
// them: one copy of the matter list instead of a lookup per matter.
func (l *lookups) allMatters() *lookups {
	ms := l.s.d.Practice.Matters()
	l.matters = make(map[domain.ID]string, len(ms))
	for i := range ms {
		l.matters[ms[i].ID] = ms[i].Number
	}
	return l
}

func (l *lookups) matter(id domain.ID) string {
	n, ok := l.matters[id]
	if !ok && !id.IsZero() {
		n = l.s.matterNumber(l.ctx, id)
		l.matters[id] = n
	}
	return n
}

func (l *lookups) user(id domain.ID) string {
	n, ok := l.users[id]
	if !ok {
		n = l.s.userName(l.ctx, id)
		l.users[id] = n
	}
	return n
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
	times, _, err := findOf[TimeEntry](ctx, s.d.Store, kindTime, Where{UserID: f.UserID, FromDate: f.FromDate, ToDate: f.ToDate})
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

// agingBuckets are the standard 30-day AR aging columns after the due date. The last one is open-ended.
var agingBuckets = []string{"1-30", "31-60", "61-90", "91+"}

func agingBucket(days int) string {
	if !positive(int64(days)) {
		return "current"
	}
	return agingBuckets[min((days-1)/30, len(agingBuckets)-1)]
}

// ARAgingReport lists each invoice with a balance on the as-of date by days past due.
// Payments received after as_of, and write-offs after it, still count as owed. Zero-fee
// matters are omitted.
func (s *Service) ARAgingReport(ctx context.Context, p domain.Principal, f ReportFilter) (Report, error) {
	r := Report{Report: "ar-aging", Columns: []string{"invoice_id", "number", "matter_id", "matter_number", "issued_on", "due_on", "days_past_due", "bucket", "balance_cents"}, Rows: []map[string]any{}}
	err := s.agingEach(ctx, p, f, func(l AgingLine) error {
		r.Rows = append(r.Rows, map[string]any{"invoice_id": l.InvoiceID, "number": l.Number, "matter_id": l.MatterID,
			"matter_number": l.MatterNumber, "issued_on": l.IssuedOn, "due_on": l.DueOn,
			"days_past_due": l.DaysPastDue, "bucket": l.Bucket, "balance_cents": int64(l.BalanceCents)})
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	return r, nil
}

// AgingLine is one AR aging row.
type AgingLine struct {
	InvoiceID, MatterID     domain.ID
	Number, MatterNumber    string
	IssuedOn, DueOn, Bucket string
	DaysPastDue             int
	BalanceCents            domain.Cents
}

// ARAgingSummary totals the AR aging report per bucket and keeps its first rows, for the
// home widget, without building a map per row.
func (s *Service) ARAgingSummary(ctx context.Context, p domain.Principal, f ReportFilter, first int) (map[string]domain.Cents, []AgingLine, error) {
	buckets := map[string]domain.Cents{}
	var top []AgingLine
	err := s.agingEach(ctx, p, f, func(l AgingLine) error {
		buckets[l.Bucket] += l.BalanceCents
		if len(top) < first {
			top = append(top, l)
		}
		return nil
	})
	return buckets, top, err
}

// agingEach calls fn with each AR aging row p may read, in invoice id order.
func (s *Service) agingEach(ctx context.Context, p domain.Principal, f ReportFilter, fn func(AgingLine) error) error {
	ok, err := s.reportGate(ctx, p, f)
	if err != nil {
		return err
	}
	asOf := f.AsOf
	if asOf == "" {
		asOf = s.today(ctx)
	}
	invs, err := s.d.Store.Aging(ctx, kindInvoice, Where{ToDate: asOf})
	if err != nil {
		return err
	}
	pays, err := s.d.Store.Aging(ctx, kindPayment, Where{})
	if err != nil {
		return err
	}
	paidOn := map[domain.ID][]agingRow{}
	for _, pay := range pays {
		paidOn[pay.InvoiceID] = append(paidOn[pay.InvoiceID], pay)
	}
	names := s.lookups(ctx).allMatters()
	for _, inv := range invs {
		if inv.Status == StatusDraft || inv.Date > asOf || s.zeroFee(inv.MatterID) {
			continue
		}
		if inv.Status == StatusVoid && inv.Closed != nil && s.firmDate(ctx, *inv.Closed) <= asOf {
			continue
		}
		if vis, err := ok(inv.MatterID); err != nil {
			return err
		} else if !vis {
			continue
		}
		owed := inv.AmountCents
		for _, pay := range paidOn[inv.ID] {
			if pay.InvoiceID != inv.ID || pay.Date > asOf || pay.Status == PayPending || pay.Status == PayFailed {
				continue
			}
			if pay.Status == PayReversed && pay.Closed != nil && s.firmDate(ctx, *pay.Closed) <= asOf {
				continue
			}
			if owed, err = owed.Sub(pay.AmountCents); err != nil {
				return err
			}
		}
		for _, wo := range inv.WriteOffs {
			if wo.On > asOf {
				continue
			}
			if owed, err = owed.Sub(wo.AmountCents); err != nil {
				return err
			}
		}
		if !positive(int64(owed)) {
			continue
		}
		days := daysBetween(inv.DueOn, asOf)
		if err := fn(AgingLine{InvoiceID: inv.ID, Number: inv.Number, MatterID: inv.MatterID, MatterNumber: names.matter(inv.MatterID),
			IssuedOn: inv.Date, DueOn: inv.DueOn, DaysPastDue: max(days, int(0)), Bucket: agingBucket(days), BalanceCents: owed}); err != nil {
			return err
		}
	}
	return nil
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
		for _, wo := range inv.WriteOffs {
			if !inRange(wo.On, f.FromDate, f.ToDate) {
				continue
			}
			if vis, err := ok(inv.MatterID); err != nil {
				return Report{}, err
			} else if !vis {
				break
			}
			r.Rows = append(r.Rows, map[string]any{"invoice_id": inv.ID, "number": inv.Number, "matter_id": inv.MatterID,
				"matter_number": s.matterNumber(ctx, inv.MatterID), "written_off_on": wo.On, "reason": wo.Reason,
				"amount_cents": int64(wo.AmountCents)})
		}
	}
	return r, nil
}
