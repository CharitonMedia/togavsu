package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Where narrows Store.Find by the plain columns migration 1200 keeps beside each
// document (matter, user, date, invoice). It is a pre-filter: callers still apply their
// full filter to the decoded rows. Zero fields match everything.
type Where struct {
	MatterID  domain.ID
	UserID    domain.ID
	InvoiceID domain.ID
	FromDate  string
	ToDate    string
	// Unbilled keeps rows with no invoice.
	Unbilled bool
	// Newest orders by date, newest first, then id; otherwise rows come in id order.
	Newest bool
	// After continues a batch from the cursor the previous batch ended on (Cursor).
	After string
	// Limit caps the batch; zero is no cap.
	Limit int
	// Head reads an invoice's stored header (no lines) instead of its whole body where
	// one is stored (migration 1201).
	Head bool
}

// headOf is the header document stored beside an invoice: the invoice without lines.
// Other kinds store none.
func headOf(v any) (string, error) {
	var inv Invoice
	switch r := v.(type) {
	case Invoice:
		inv = r
	case *Invoice:
		inv = *r
	default:
		return "", nil
	}
	inv.Lines = nil
	b, err := json.Marshal(inv)
	return string(b), err
}

// cols are the indexed columns of one document.
type cols struct{ matter, user, date, invoice string }

// indexOf extracts the indexed columns of a billing document. Kinds without them index
// as empty strings, which Where only matches when it filters on nothing.
func indexOf(v any) cols {
	switch r := v.(type) {
	case TimeEntry:
		return cols{string(r.MatterID), string(r.UserID), r.WorkedOn, string(r.InvoiceID)}
	case *TimeEntry:
		return indexOf(*r)
	case Expense:
		return cols{string(r.MatterID), "", r.IncurredOn, string(r.InvoiceID)}
	case *Expense:
		return indexOf(*r)
	case Invoice:
		return cols{string(r.MatterID), "", r.IssuedOn, ""}
	case *Invoice:
		return indexOf(*r)
	case Payment:
		return cols{string(r.MatterID), "", r.ReceivedOn, string(r.InvoiceID)}
	case *Payment:
		return indexOf(*r)
	case Timer:
		return cols{string(r.MatterID), string(r.UserID), "", ""}
	case *Timer:
		return indexOf(*r)
	}
	return cols{}
}

// indexRaw decodes a stored document of kind and returns its columns.
func indexRaw(kind string, raw []byte) (cols, error) {
	var v any
	switch kind {
	case kindTime:
		var r TimeEntry
		err := json.Unmarshal(raw, &r)
		v = r
		if err != nil {
			return cols{}, err
		}
	case kindExpense:
		var r Expense
		err := json.Unmarshal(raw, &r)
		v = r
		if err != nil {
			return cols{}, err
		}
	case kindInvoice:
		var r Invoice
		err := json.Unmarshal(raw, &r)
		v = r
		if err != nil {
			return cols{}, err
		}
	case kindPayment:
		var r Payment
		err := json.Unmarshal(raw, &r)
		v = r
		if err != nil {
			return cols{}, err
		}
	case kindTimer:
		var r Timer
		err := json.Unmarshal(raw, &r)
		v = r
		if err != nil {
			return cols{}, err
		}
	}
	return indexOf(v), nil
}

// indexedKinds are the kinds whose documents carry indexed columns.
var indexedKinds = []string{kindTime, kindExpense, kindInvoice, kindPayment, kindTimer}

func (w Where) match(c cols) bool {
	switch {
	case !w.MatterID.IsZero() && c.matter != string(w.MatterID),
		!w.UserID.IsZero() && c.user != string(w.UserID),
		!w.InvoiceID.IsZero() && c.invoice != string(w.InvoiceID),
		w.Unbilled && c.invoice != "",
		w.FromDate != "" && c.date < w.FromDate,
		w.ToDate != "" && c.date > w.ToDate:
		return false
	}
	return true
}

// cursorOf is the After value that continues a batch after a row with columns c and id.
func cursorOf(w Where, c cols, id string) string {
	if w.Newest {
		return c.date + "|" + id
	}
	return id
}

func (m *Memory) Find(_ context.Context, kind string, w Where) ([][]byte, []string, error) {
	m.mu.Lock()
	type row struct {
		id string
		c  cols
		b  []byte
	}
	var rows []row
	for id, b := range m.rows[kind] {
		c := m.cols[kind][id]
		if w.match(c) {
			rows = append(rows, row{id, c, b})
		}
	}
	m.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool {
		if w.Newest {
			if rows[i].c.date != rows[j].c.date {
				return rows[i].c.date > rows[j].c.date
			}
		}
		return rows[i].id < rows[j].id
	})
	afterDate, afterID, _ := strings.Cut(w.After, "|")
	var out [][]byte
	var cursors []string
	for _, r := range rows {
		cur := cursorOf(w, r.c, r.id)
		if w.After != "" && (w.Newest && (r.c.date > afterDate || r.c.date == afterDate && r.id <= afterID) || !w.Newest && r.id <= w.After) {
			continue
		}
		if w.Limit > 0 && len(out) == w.Limit {
			break
		}
		out, cursors = append(out, r.b), append(cursors, cur)
	}
	return out, cursors, nil
}

// Find reads the documents of kind that match w through the indexes of migration 1200.
// It returns each row's cursor beside it, for the next batch's After.
func (p *Postgres) Find(ctx context.Context, kind string, w Where) ([][]byte, []string, error) {
	var q strings.Builder
	args := []any{kind}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if w.Head {
		q.WriteString(`SELECT id, on_date, CASE WHEN head <> '' THEN head ELSE body END FROM billing_records WHERE kind = $1`)
	} else {
		q.WriteString(`SELECT id, on_date, body FROM billing_records WHERE kind = $1`)
	}
	if !w.MatterID.IsZero() {
		q.WriteString(` AND matter_id = ` + arg(string(w.MatterID)))
	}
	if !w.UserID.IsZero() {
		q.WriteString(` AND user_id = ` + arg(string(w.UserID)))
	}
	if !w.InvoiceID.IsZero() {
		q.WriteString(` AND invoice_id = ` + arg(string(w.InvoiceID)))
	}
	if w.Unbilled {
		q.WriteString(` AND invoice_id = ''`)
	}
	if w.FromDate != "" {
		q.WriteString(` AND on_date >= ` + arg(w.FromDate))
	}
	if w.ToDate != "" {
		q.WriteString(` AND on_date <= ` + arg(w.ToDate))
	}
	if w.After != "" {
		if w.Newest {
			date, id, _ := strings.Cut(w.After, "|")
			d := arg(date)
			q.WriteString(` AND (on_date < ` + d + ` OR (on_date = ` + d + ` AND id > ` + arg(id) + `))`)
		} else {
			q.WriteString(` AND id > ` + arg(w.After))
		}
	}
	if w.Newest {
		q.WriteString(` ORDER BY on_date DESC, id`)
	} else {
		q.WriteString(` ORDER BY id`)
	}
	if w.Limit > 0 {
		q.WriteString(` LIMIT ` + arg(w.Limit))
	}
	rows, err := audit.Conn(ctx, p.pool).QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out [][]byte
	var cursors []string
	for rows.Next() {
		var id, date, body string
		if err := rows.Scan(&id, &date, &body); err != nil {
			return nil, nil, err
		}
		out = append(out, []byte(body))
		cursors = append(cursors, cursorOf(w, cols{date: date}, id))
	}
	return out, cursors, rows.Err()
}

// IndexRows fills the indexed columns of documents written before migration 1200, in
// batches, so an upgraded database answers Find without a long first request. The core
// calls it once at start; a database with nothing to fill costs one query per kind.
func (p *Postgres) IndexRows(ctx context.Context) error {
	for _, kind := range indexedKinds {
		for {
			rows, err := p.pool.QueryContext(ctx, `SELECT id, body FROM billing_records WHERE kind = $1 AND (indexed = 0 OR kind = 'invoice' AND head = '')
				ORDER BY id LIMIT 2000`, kind)
			if err != nil {
				return err
			}
			type fill struct {
				id   string
				c    cols
				head string
			}
			var batch []fill
			for rows.Next() {
				var id, body string
				if err := rows.Scan(&id, &body); err != nil {
					rows.Close()
					return err
				}
				c, err := indexRaw(kind, []byte(body))
				if err != nil {
					rows.Close()
					return err
				}
				head, err := headRaw(kind, []byte(body))
				if err != nil {
					rows.Close()
					return err
				}
				batch = append(batch, fill{id, c, head})
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if len(batch) == 0 {
				break
			}
			err = audit.WithTx(ctx, p.pool, func(ctx context.Context) error {
				tx := audit.Conn(ctx, p.pool)
				// One UPDATE ... FROM (VALUES ...) per 400 rows; SQLite 3.33 has the same form.
				for start := 0; start < len(batch); start += 400 {
					part := batch[start:min(start+400, len(batch))]
					var vals strings.Builder
					args := []any{kind}
					for i, f := range part {
						if i > 0 {
							vals.WriteString(",")
						}
						n := len(args)
						fmt.Fprintf(&vals, "($%d,$%d,$%d,$%d,$%d,$%d)", n+1, n+2, n+3, n+4, n+5, n+6)
						args = append(args, f.id, f.c.matter, f.c.user, f.c.date, f.c.invoice, f.head)
					}
					if _, err := tx.ExecContext(ctx, `UPDATE billing_records SET matter_id = v.matter_id, user_id = v.user_id, on_date = v.on_date,
						invoice_id = v.invoice_id, head = v.head, indexed = 1
						FROM (VALUES `+vals.String()+`) AS v (id, matter_id, user_id, on_date, invoice_id, head)
						WHERE billing_records.kind = $1 AND billing_records.id = v.id`, args...); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// findOf decodes Find's rows as T.
func findOf[T any](ctx context.Context, st Store, kind string, w Where) ([]T, []string, error) {
	raw, cursors, err := st.Find(ctx, kind, w)
	if err != nil {
		return nil, nil, err
	}
	out := make([]T, 0, len(raw))
	for _, b := range raw {
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, nil, err
		}
		out = append(out, v)
	}
	return out, cursors, nil
}

// findBatch is how many rows scanFind reads per query when it stops early.
var findBatch = 500

// scanFind walks Find's rows in batches until keep has accepted max rows (zero is every
// row). keep returns whether it took the row.
func scanFind[T any](ctx context.Context, st Store, kind string, w Where, max int, keep func(T) (bool, error)) error {
	batch := w
	if max > 0 {
		batch.Limit = findBatch
	}
	taken := 0
	for {
		rows, cursors, err := findOf[T](ctx, st, kind, batch)
		if err != nil {
			return err
		}
		for _, r := range rows {
			ok, err := keep(r)
			if err != nil {
				return err
			}
			if ok {
				taken++
				if max > 0 && taken >= max {
					return nil
				}
			}
		}
		if batch.Limit == 0 || len(rows) < batch.Limit {
			return nil
		}
		batch.After = cursors[len(cursors)-1]
	}
}

// skipJSON reads past a JSON value without keeping it.
type skipJSON struct{}

func (*skipJSON) UnmarshalJSON([]byte) error { return nil }

// invoiceHead decodes an invoice without its lines, for lists and reports that read
// only the header and write-offs.
type invoiceHead struct {
	Invoice
	Lines *skipJSON `json:"lines"`
}

// invoiceHeads lists the invoices matching w with their Lines left empty.
func invoiceHeads(ctx context.Context, st Store, w Where) ([]Invoice, error) {
	w.Head = true
	rows, _, err := findOf[invoiceHead](ctx, st, kindInvoice, w)
	if err != nil {
		return nil, err
	}
	out := make([]Invoice, len(rows))
	for i, r := range rows {
		out[i] = r.Invoice
	}
	return out, nil
}

// headRaw is headOf for a stored document of kind.
func headRaw(kind string, raw []byte) (string, error) {
	if kind != kindInvoice {
		return "", nil
	}
	var inv Invoice
	if err := json.Unmarshal(raw, &inv); err != nil {
		return "", err
	}
	return headOf(inv)
}
