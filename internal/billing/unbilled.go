package billing

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// unbilledRow is one billable time entry or expense no invoice bills yet, with the fields
// the unbilled report shows (migration 1261 keeps them in billing_unbilled).
type unbilledRow struct {
	ID, MatterID, UserID domain.ID
	Date, Description    string
	// Minutes and RateCents are a time entry's; AmountCents and MarkupPercent an expense's.
	Minutes       int64
	RateCents     domain.Cents
	AmountCents   domain.Cents
	MarkupPercent int
	// TotalCents is the billed amount: timeAmount or expenseAmount of the fields above.
	TotalCents domain.Cents
}

// total is the row's billed amount.
func (r unbilledRow) total(expense bool) (domain.Cents, error) {
	if expense {
		return expenseAmount(r.AmountCents, r.MarkupPercent)
	}
	return timeAmount(r.RateCents, r.Minutes)
}

// matterTotal is one matter's unbilled rows of a kind: how many, their billed total, and
// the smallest row id (report order).
type matterTotal struct {
	MatterID domain.ID
	First    string
	Items    int
	Cents    domain.Cents
}

// unbilledOf is v's unbilled-report row; ok is false for any document the report leaves
// out: other kinds, non-billable work and work an invoice already bills.
func unbilledOf(v any) (unbilledRow, bool) {
	switch r := v.(type) {
	case TimeEntry:
		if !r.Billable || !r.InvoiceID.IsZero() {
			return unbilledRow{}, false
		}
		row := unbilledRow{ID: r.ID, MatterID: r.MatterID, UserID: r.UserID, Date: r.WorkedOn, Description: r.Description,
			Minutes: r.Minutes(), RateCents: r.RateCents}
		row.TotalCents, _ = row.total(false) // CreateTime refuses an amount that overflows
		return row, true
	case *TimeEntry:
		return unbilledOf(*r)
	case Expense:
		if !r.Billable || !r.InvoiceID.IsZero() {
			return unbilledRow{}, false
		}
		row := unbilledRow{ID: r.ID, MatterID: r.MatterID, Date: r.IncurredOn, Description: r.Description,
			AmountCents: r.AmountCents, MarkupPercent: r.MarkupPercent}
		row.TotalCents, _ = row.total(true)
		return row, true
	case *Expense:
		return unbilledOf(*r)
	}
	return unbilledRow{}, false
}

// matchUnbilled applies w's matter, user and date filters to an unbilled row.
func (w Where) matchUnbilled(r unbilledRow) bool {
	return (w.MatterID.IsZero() || r.MatterID == w.MatterID) && (w.UserID.IsZero() || r.UserID == w.UserID) &&
		inRange(r.Date, w.FromDate, w.ToDate)
}

// Unbilled calls fn with each unbilled row of kind matching w's matter, user and date,
// in id order. The memory store decodes every document of the kind.
func (m *Memory) Unbilled(ctx context.Context, kind string, w Where, fn func(*unbilledRow) error) error {
	raw, err := m.List(ctx, kind)
	if err != nil {
		return err
	}
	for _, b := range raw {
		v, err := decodeKind(kind, b)
		if err != nil {
			return err
		}
		if row, ok := unbilledOf(v); ok && w.matchUnbilled(row) {
			if err := fn(&row); err != nil {
				return err
			}
		}
	}
	return nil
}

// UnbilledTotals totals the unbilled rows of kind per matter, ordered by each matter's
// first row id.
func (m *Memory) UnbilledTotals(ctx context.Context, kind string, w Where) ([]matterTotal, error) {
	return totalsOf(func(fn func(*unbilledRow) error) error { return m.Unbilled(ctx, kind, w, fn) })
}

// totalsOf totals rows in id order per matter, in order of each matter's first row.
func totalsOf(each func(fn func(*unbilledRow) error) error) ([]matterTotal, error) {
	at := map[domain.ID]int{}
	var out []matterTotal
	err := each(func(r *unbilledRow) error {
		i, ok := at[r.MatterID]
		if !ok {
			i = len(out)
			at[r.MatterID] = i
			out = append(out, matterTotal{MatterID: r.MatterID, First: string(r.ID)})
		}
		out[i].Items++
		out[i].Cents += r.TotalCents
		return nil
	})
	return out, err
}

// UnbilledTotals totals the kept snapshot outside a transaction and aggregates the table
// inside one.
func (p *Postgres) UnbilledTotals(ctx context.Context, kind string, w Where) ([]matterTotal, error) {
	if audit.Tx(ctx, p.pool) == nil {
		if !w.MatterID.IsZero() || !w.UserID.IsZero() || w.FromDate != "" || w.ToDate != "" {
			return totalsOf(func(fn func(*unbilledRow) error) error { return p.Unbilled(ctx, kind, w, fn) })
		}
		// Unfiltered totals are the same for every caller of one snapshot; each caller's
		// report gate still decides which matters it sees.
		snap, err := p.unbilled.current(ctx, p, "billing_unbilled", p.loadUnbilled)
		if err != nil {
			return nil, err
		}
		v, err := snap.derive("totals:"+kind, func() (any, error) {
			rows := snap.kinds[kind]
			return totalsOf(func(fn func(*unbilledRow) error) error {
				for i := range rows {
					if err := fn(&rows[i]); err != nil {
						return err
					}
				}
				return nil
			})
		})
		if err != nil {
			return nil, err
		}
		return v.([]matterTotal), nil
	}
	var q strings.Builder
	args := []any{kind}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	q.WriteString(`SELECT matter_id, MIN(id), COUNT(*), COALESCE(SUM(total_cents), 0) FROM billing_unbilled WHERE kind = $1`)
	if !w.MatterID.IsZero() {
		q.WriteString(` AND matter_id = ` + arg(string(w.MatterID)))
	}
	if !w.UserID.IsZero() {
		q.WriteString(` AND user_id = ` + arg(string(w.UserID)))
	}
	if w.FromDate != "" {
		q.WriteString(` AND on_date >= ` + arg(w.FromDate))
	}
	if w.ToDate != "" {
		q.WriteString(` AND on_date <= ` + arg(w.ToDate))
	}
	// Ordered here rather than in SQL: a collation-aware sort of 20,000 ids cost Postgres
	// more than the aggregate (AC-017).
	q.WriteString(` GROUP BY matter_id`)
	rows, err := audit.Conn(ctx, p.pool).QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []matterTotal
	for rows.Next() {
		var t matterTotal
		var matter string
		var cents int64
		if err := rows.Scan(&matter, &t.First, &t.Items, &cents); err != nil {
			return nil, err
		}
		t.MatterID, t.Cents = domain.ID(matter), domain.Cents(cents)
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].First < out[j].First })
	return out, rows.Err()
}

// unbilledBatch is how many rows the transaction path reads per query, each batch
// continuing after the last id so every query is an ordered range scan of the key.
var unbilledBatch = unbilledSizes().batch

// unbilledSizes are the Unbilled read's fixed sizes: the columns a query reads, the rows
// a keyset batch reads, and the streamed rows decoded at a time. They size buffers and
// round trips; they are not firm settings.
func unbilledSizes() (s struct{ cols, batch, chunk int }) {
	s.cols, s.batch, s.chunk = 10, 5000, 1000
	return s
}

// Unbilled reads billing_unbilled (migration 1261) by matter, user and date, in id order.
// Outside a transaction it filters the kept snapshot (snapshot.go; fn must not change
// the shared row); inside a caller's transaction it reads keyset batches of the table, so
// the transaction sees its own writes.
func (p *Postgres) Unbilled(ctx context.Context, kind string, w Where, fn func(*unbilledRow) error) error {
	var q strings.Builder
	args := []any{kind}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	q.WriteString(`SELECT id, matter_id, user_id, on_date, description, minutes, rate_cents, amount_cents, markup_percent,
		total_cents FROM billing_unbilled WHERE kind = $1`)
	if !w.MatterID.IsZero() {
		q.WriteString(` AND matter_id = ` + arg(string(w.MatterID)))
	}
	if !w.UserID.IsZero() {
		q.WriteString(` AND user_id = ` + arg(string(w.UserID)))
	}
	if w.FromDate != "" {
		q.WriteString(` AND on_date >= ` + arg(w.FromDate))
	}
	if w.ToDate != "" {
		q.WriteString(` AND on_date <= ` + arg(w.ToDate))
	}
	if audit.Tx(ctx, p.pool) == nil {
		rows, err := p.unbilled.rows(ctx, p, "billing_unbilled", kind, p.loadUnbilled)
		if err != nil {
			return err
		}
		for i := range rows {
			if w.matchUnbilled(rows[i]) {
				if err := fn(&rows[i]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// Keyset batches, w.Limit rows each when set.
	batch := unbilledBatch
	if positive(int64(w.Limit)) {
		batch = w.Limit
	}
	after := arg("")
	query, last := q.String()+` AND id > `+after+` ORDER BY id LIMIT `+strconv.Itoa(batch), len(args)-1
	buf := make([]unbilledRow, 0, batch)
	for {
		rows, err := audit.Conn(ctx, p.pool).QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		buf = buf[:0]
		for rows.Next() {
			var r unbilledRow
			var id, matter, user string
			var minutes, rate, amount, markup, total int64
			if err = rows.Scan(&id, &matter, &user, &r.Date, &r.Description, &minutes, &rate, &amount, &markup, &total); err != nil {
				break
			}
			r.ID, r.MatterID, r.UserID, r.Minutes = domain.ID(id), domain.ID(matter), domain.ID(user), minutes
			r.RateCents, r.AmountCents, r.MarkupPercent, r.TotalCents = domain.Cents(rate), domain.Cents(amount), int(markup), domain.Cents(total)
			buf = append(buf, r)
		}
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			return err
		}
		for i := range buf {
			if err := fn(&buf[i]); err != nil {
				return err
			}
		}
		if len(buf) < batch {
			return nil
		}
		args[last] = string(buf[len(buf)-1].ID)
	}
}

// loadUnbilled reads every unbilled row of both kinds for the snapshot: one in-order scan
// of the primary key per kind (never a sort of the whole set), decoded from raw values.
func (p *Postgres) loadUnbilled(ctx context.Context, tx pgx.Tx) (map[string][]unbilledRow, error) {
	for _, set := range []string{`SET LOCAL enable_sort = off`, `SET LOCAL jit = off`} {
		if _, err := tx.Exec(ctx, set); err != nil {
			return nil, err
		}
	}
	out := map[string][]unbilledRow{}
	for _, kind := range []string{kindTime, kindExpense} {
		rows, err := tx.Query(ctx, `SELECT id, matter_id, user_id, on_date, description, minutes, rate_cents, amount_cents, markup_percent,
			total_cents FROM billing_unbilled WHERE kind = $1 ORDER BY id`, kind)
		if err != nil {
			return nil, err
		}
		list := []unbilledRow{}
		err = streamUnbilled(rows, func(r *unbilledRow) error {
			list = append(list, *r)
			return nil
		})
		rows.Close()
		if err != nil {
			return nil, err
		}
		out[kind] = list
	}
	return out, nil
}

// streamUnbilled decodes streamed rows a chunk at a time from their raw wire values: the
// chunk's text is copied into one arena and each row's text fields are slices of a single
// string, so a chunk of 1,000 rows is one allocation rather than 5,000; the BIGINT
// columns are read as pgx returns them, binary or text.
func streamUnbilled(rows pgx.Rows, fn func(*unbilledRow) error) error {
	const texts = 5 // id, matter_id, user_id, on_date, description; then five BIGINTs
	sizes := unbilledSizes()
	fields := rows.FieldDescriptions()
	if len(fields) != sizes.cols {
		return fmt.Errorf("billing_unbilled: %d columns", len(fields))
	}
	var arena []byte
	var ends []int
	buf := make([]unbilledRow, 0, sizes.chunk)
	flush := func() error {
		text := string(arena)
		start, at := 0, 0
		for i := range buf {
			var f [texts]string
			for k := range f {
				f[k], start = text[start:ends[at+k]], ends[at+k]
			}
			at += texts
			r := &buf[i]
			r.ID, r.MatterID, r.UserID, r.Date, r.Description = domain.ID(f[0]), domain.ID(f[1]), domain.ID(f[2]), f[3], f[4]
		}
		for i := range buf {
			if err := fn(&buf[i]); err != nil {
				return err
			}
		}
		arena, ends, buf = arena[:0], ends[:0], buf[:0]
		return nil
	}
	for rows.Next() {
		raw := rows.RawValues()
		for _, v := range raw[:texts] {
			arena = append(arena, v...)
			ends = append(ends, len(arena))
		}
		var n [5]int64
		for k := range n {
			v, err := wireInt(fields[texts+k].Format, raw[texts+k])
			if err != nil {
				return err
			}
			n[k] = v
		}
		buf = append(buf, unbilledRow{Minutes: n[0], RateCents: domain.Cents(n[1]), AmountCents: domain.Cents(n[2]), MarkupPercent: int(n[3]),
			TotalCents: domain.Cents(n[4])})
		if len(buf) == sizes.chunk {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return flush()
}

// wireInt reads a BIGINT wire value in pgx's binary (8 bytes, big-endian) or text format.
func wireInt(format int16, b []byte) (int64, error) {
	if format == pgx.BinaryFormatCode {
		if len(b) != 8 {
			return 0, fmt.Errorf("billing_unbilled: %d-byte integer", len(b))
		}
		return int64(binary.BigEndian.Uint64(b)), nil
	}
	return strconv.ParseInt(string(b), 10, 64)
}

// native runs fn on one pooled connection through pgx's own interface.
func (p *Postgres) native(ctx context.Context, fn func(*pgx.Conn) error) error {
	conn, err := p.pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error { return fn(dc.(*stdlib.Conn).Conn()) })
}
