package billing

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// projection is a narrow table beside billing_records holding the few fields one report
// reads of each document (migrations 1261 and 1262), so the report does not decode every
// document body. Postgres.Put writes a document's row in the same transaction as the
// document; IndexRows fills rows for documents stored without Put; each table's foreign
// key drops a row with its document.
type projection struct {
	table string
	kinds []string
	// cols are the columns after kind and id.
	cols []string
	// row is the document's column values in cols order; ok is false when the document
	// has no row in this table.
	row func(v any) (vals []any, ok bool)
}

func (pr projection) has(kind string) bool {
	for _, k := range pr.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

var projections = []projection{
	{table: "billing_unbilled", kinds: []string{kindTime, kindExpense},
		cols: []string{"matter_id", "user_id", "on_date", "description", "minutes", "rate_cents", "amount_cents", "markup_percent", "total_cents"},
		row: func(v any) ([]any, bool) {
			r, ok := unbilledOf(v)
			return []any{string(r.MatterID), string(r.UserID), r.Date, r.Description, r.Minutes, int64(r.RateCents), int64(r.AmountCents),
				int64(r.MarkupPercent), int64(r.TotalCents)}, ok
		}},
	{table: "billing_aging", kinds: []string{kindInvoice, kindPayment},
		cols: []string{"matter_id", "invoice_id", "number", "status", "on_date", "due_on", "amount_cents", "closed_at", "write_offs"},
		row: func(v any) ([]any, bool) {
			r, ok := agingOf(v)
			return []any{string(r.MatterID), string(r.InvoiceID), r.Number, r.Status, r.Date, r.DueOn, int64(r.AmountCents),
				r.closedText(), r.writeOffsText()}, ok
		}},
}

// decodeKind decodes a stored document of a projected kind.
func decodeKind(kind string, raw []byte) (any, error) {
	var err error
	switch kind {
	case kindTime:
		var r TimeEntry
		err = json.Unmarshal(raw, &r)
		return r, err
	case kindExpense:
		var r Expense
		err = json.Unmarshal(raw, &r)
		return r, err
	case kindInvoice:
		var r invoiceHead
		err = json.Unmarshal(raw, &r)
		return r.Invoice, err
	case kindPayment:
		var r Payment
		err = json.Unmarshal(raw, &r)
		return r, err
	}
	return nil, nil
}

// syncProjections writes or removes the projected rows of one document.
func syncProjections(ctx context.Context, conn audit.Querier, kind, id string, v any) error {
	for _, pr := range projections {
		if !pr.has(kind) {
			continue
		}
		vals, ok := pr.row(v)
		if !ok {
			if _, err := conn.ExecContext(ctx, `DELETE FROM `+pr.table+` WHERE kind = $1 AND id = $2`, kind, id); err != nil {
				return err
			}
			continue
		}
		if err := pr.insert(ctx, conn, kind, []string{id}, [][]any{vals}, true); err != nil {
			return err
		}
	}
	return nil
}

// fillProjections replaces the projected rows of a batch of documents of kind: one
// DELETE and one INSERT per table.
func fillProjections(ctx context.Context, conn audit.Querier, kind string, ids []string, docs []any) error {
	for _, pr := range projections {
		if !pr.has(kind) || len(ids) == 0 {
			continue
		}
		in := make([]string, len(ids))
		args := []any{kind}
		for i, id := range ids {
			args = append(args, id)
			in[i] = "$" + strconv.Itoa(len(args))
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM `+pr.table+` WHERE kind = $1 AND id IN (`+strings.Join(in, ",")+`)`, args...); err != nil {
			return err
		}
		var keep []string
		var rows [][]any
		for i, d := range docs {
			if vals, ok := pr.row(d); ok {
				keep, rows = append(keep, ids[i]), append(rows, vals)
			}
		}
		if err := pr.insert(ctx, conn, kind, keep, rows, false); err != nil {
			return err
		}
	}
	return nil
}

// insert writes rows of kind; upsert replaces a row already there.
func (pr projection) insert(ctx context.Context, conn audit.Querier, kind string, ids []string, rows [][]any, upsert bool) error {
	if len(rows) == 0 {
		return nil
	}
	vals := make([]string, len(rows))
	args := []any{kind}
	for i, r := range rows {
		ph := []string{"$1"}
		args = append(args, ids[i])
		ph = append(ph, "$"+strconv.Itoa(len(args)))
		for _, v := range r {
			args = append(args, v)
			ph = append(ph, "$"+strconv.Itoa(len(args)))
		}
		vals[i] = "(" + strings.Join(ph, ",") + ")"
	}
	q := `INSERT INTO ` + pr.table + ` (kind, id, ` + strings.Join(pr.cols, ", ") + `) VALUES ` + strings.Join(vals, ",")
	if upsert {
		set := make([]string, len(pr.cols))
		for i, c := range pr.cols {
			set[i] = c + " = EXCLUDED." + c
		}
		q += ` ON CONFLICT (kind, id) DO UPDATE SET ` + strings.Join(set, ", ")
	}
	_, err := conn.ExecContext(ctx, q, args...)
	return err
}

// agingRow is what AR aging reads of one invoice or payment (migration 1262).
type agingRow struct {
	ID, MatterID, InvoiceID domain.ID
	Number, Status          string
	// Date is an invoice's issue date or a payment's received date.
	Date, DueOn string
	// AmountCents is an invoice's total or a payment's amount.
	AmountCents domain.Cents
	// Closed is an invoice's void time or a payment's reversal time.
	Closed    *time.Time
	WriteOffs []WriteOff
}

func agingOf(v any) (agingRow, bool) {
	switch r := v.(type) {
	case Invoice:
		return agingRow{ID: r.ID, MatterID: r.MatterID, Number: r.Number, Status: r.Status, Date: r.IssuedOn, DueOn: r.DueOn,
			AmountCents: r.TotalCents, Closed: r.VoidedAt, WriteOffs: r.WriteOffs}, true
	case *Invoice:
		return agingOf(*r)
	case Payment:
		return agingRow{ID: r.ID, MatterID: r.MatterID, InvoiceID: r.InvoiceID, Status: r.Status, Date: r.ReceivedOn,
			AmountCents: r.AmountCents, Closed: r.ReversedAt}, true
	case *Payment:
		return agingOf(*r)
	}
	return agingRow{}, false
}

func (r agingRow) closedText() string {
	if r.Closed == nil {
		return ""
	}
	b, _ := json.Marshal(r.Closed)
	return string(b)
}

func (r agingRow) writeOffsText() string {
	if len(r.WriteOffs) == 0 {
		return ""
	}
	b, _ := json.Marshal(r.WriteOffs)
	return string(b)
}

// Aging lists the aging rows of kind dated on or before w.ToDate (zero is every date),
// in id order. The memory store decodes every document of the kind.
func (m *Memory) Aging(ctx context.Context, kind string, w Where) ([]agingRow, error) {
	raw, err := m.List(ctx, kind)
	if err != nil {
		return nil, err
	}
	var out []agingRow
	for _, b := range raw {
		v, err := decodeKind(kind, b)
		if err != nil {
			return nil, err
		}
		if r, ok := agingOf(v); ok && (w.ToDate == "" || r.Date <= w.ToDate) {
			out = append(out, r)
		}
	}
	return out, nil
}

// Aging reads billing_aging (migration 1262), through pgx as text outside a transaction.
func (p *Postgres) Aging(ctx context.Context, kind string, w Where) ([]agingRow, error) {
	q := `SELECT id, matter_id, invoice_id, number, status, on_date, due_on, amount_cents::TEXT, closed_at, write_offs FROM billing_aging WHERE kind = $1`
	args := []any{kind}
	if w.ToDate != "" {
		q += ` AND on_date <= $2`
		args = append(args, w.ToDate)
	}
	var out []agingRow
	err := p.textRows(ctx, q+` ORDER BY id`, args, func(f []string) error {
		r := agingRow{ID: domain.ID(f[0]), MatterID: domain.ID(f[1]), InvoiceID: domain.ID(f[2]), Number: f[3], Status: f[4], Date: f[5], DueOn: f[6]}
		amount, err := strconv.ParseInt(f[7], 10, 64)
		if err != nil {
			return err
		}
		r.AmountCents = domain.Cents(amount)
		if f[8] != "" {
			if err := json.Unmarshal([]byte(f[8]), &r.Closed); err != nil {
				return err
			}
		}
		if f[9] != "" {
			if err := json.Unmarshal([]byte(f[9]), &r.WriteOffs); err != nil {
				return err
			}
		}
		out = append(out, r)
		return nil
	})
	return out, err
}

// textRows runs a query whose columns are all text and calls fn with each row's fields.
// Outside a transaction it streams through pgx, copying a chunk of rows' raw text into one
// string that the fields slice, so a row costs no allocation of its own (database/sql's
// per-value conversion dominated the AC-017 report reads); inside one it uses the
// transaction. fn may keep the strings.
func (p *Postgres) textRows(ctx context.Context, query string, args []any, fn func([]string) error) error {
	if audit.Tx(ctx, p.pool) != nil {
		rows, err := audit.Conn(ctx, p.pool).QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		f := make([]string, len(cols))
		dst := make([]any, len(cols))
		for i := range f {
			dst[i] = &f[i]
		}
		for rows.Next() {
			if err := rows.Scan(dst...); err != nil {
				return err
			}
			if err := fn(f); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	return p.native(ctx, func(pc *pgx.Conn) error {
		rows, err := pc.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		n := len(rows.FieldDescriptions())
		var arena []byte
		var ends []int
		flush := func() error {
			text := string(arena)
			f := make([]string, n)
			for at, start := 0, 0; at < len(ends); at += n {
				for i := range f {
					f[i], start = text[start:ends[at+i]], ends[at+i]
				}
				if err := fn(f); err != nil {
					return err
				}
			}
			arena, ends = arena[:0], ends[:0]
			return nil
		}
		for rows.Next() {
			for _, v := range rows.RawValues() {
				arena = append(arena, v...)
				ends = append(ends, len(arena))
			}
			if len(arena) >= textRowsChunk {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return flush()
	})
}

// textRowsChunk is how many bytes of row text textRows decodes at a time.
const textRowsChunk = 256 << 10
