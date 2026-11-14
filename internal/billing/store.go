package billing

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"sort"
	"sync"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns billing's numbered SQL files (range 300-399). The core applies them with
// owner core after db.Embedded, in the same db.Apply call.
func Migrations() ([]db.Migration, error) { return db.LoadFS(migrationFS) }

// ErrNotFound is returned by Store.Get for a missing row.
var ErrNotFound = errors.New("billing: not found")

// Store keeps billing documents. Atomic groups writes so they commit or roll back together;
// reads made with the Atomic context see the writes made before them.
type Store interface {
	Atomic(ctx context.Context, fn func(ctx context.Context) error) error
	Get(ctx context.Context, kind, id string, dst any) error
	Put(ctx context.Context, kind, id string, v any) error
	Delete(ctx context.Context, kind, id string) error
	List(ctx context.Context, kind string) ([][]byte, error)
	// Find reads the rows of kind matching w's indexed columns, with a cursor per row.
	Find(ctx context.Context, kind string, w Where) ([][]byte, []string, error)
	// Unbilled calls fn with each billable, unbilled time entry or expense (kind) matching
	// w's matter, user and dates, in id order. fn must not keep the row.
	Unbilled(ctx context.Context, kind string, w Where, fn func(*unbilledRow) error) error
	// UnbilledTotals totals those rows per matter, ordered by each matter's first id.
	UnbilledTotals(ctx context.Context, kind string, w Where) ([]matterTotal, error)
	// Aging lists what AR aging reads of the invoices or payments (kind) dated on or
	// before w.ToDate, in id order.
	Aging(ctx context.Context, kind string, w Where) ([]agingRow, error)
}

// Memory is the in-process store used by tests and by a core without a database.
type Memory struct {
	mu   sync.Mutex
	rows map[string]map[string][]byte
	cols map[string]map[string]cols
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{rows: map[string]map[string][]byte{}, cols: map[string]map[string]cols{}}
}

type memTx struct{ m *Memory }

// Atomic snapshots the store and restores it when fn fails. A nested call joins the outer one.
func (m *Memory) Atomic(ctx context.Context, fn func(ctx context.Context) error) error {
	if v, ok := ctx.Value(memTx{}).(*Memory); ok && v == m {
		return fn(ctx)
	}
	m.mu.Lock()
	snap := make(map[string]map[string][]byte, len(m.rows))
	for k, rows := range m.rows {
		c := make(map[string][]byte, len(rows))
		for id, b := range rows {
			c[id] = b
		}
		snap[k] = c
	}
	csnap := make(map[string]map[string]cols, len(m.cols))
	for k, rows := range m.cols {
		c := make(map[string]cols, len(rows))
		for id, v := range rows {
			c[id] = v
		}
		csnap[k] = c
	}
	m.mu.Unlock()
	err := fn(context.WithValue(ctx, memTx{}, m))
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		m.mu.Lock()
		m.rows, m.cols = snap, csnap
		m.mu.Unlock()
	}
	return err
}

func (m *Memory) Get(_ context.Context, kind, id string, dst any) error {
	m.mu.Lock()
	b, ok := m.rows[kind][id]
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return json.Unmarshal(b, dst)
}

func (m *Memory) Put(_ context.Context, kind, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows[kind] == nil {
		m.rows[kind] = map[string][]byte{}
	}
	m.rows[kind][id] = b
	if m.cols[kind] == nil {
		m.cols[kind] = map[string]cols{}
	}
	m.cols[kind][id] = indexOf(v)
	return nil
}

func (m *Memory) Delete(_ context.Context, kind, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows[kind], id)
	delete(m.cols[kind], id)
	return nil
}

func (m *Memory) List(_ context.Context, kind string) ([][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.rows[kind]))
	for id := range m.rows[kind] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([][]byte, len(ids))
	for i, id := range ids {
		out[i] = m.rows[kind][id]
	}
	return out, nil
}

// Postgres stores documents in billing_records (migration 300). Atomic is audit.WithTx on the
// same pool, so billing rows and their audit_log rows commit in one transaction.
// ponytail: ceiling: Find narrows by indexed matter, user, date and invoice columns (migration
// 1200), but the remaining filters, report totals and List callers (payments, catalog, events)
// still decode rows in Go; one core process serializes billing writes with Service.mu. Upgrade
// path: typed tables with indexed filters, SQL aggregates and SELECT ... FOR UPDATE. Trigger: a
// second core replica, or a billing list or report over 300 ms p95 in TestReleasePerfP95.
type Postgres struct {
	pool    *sql.DB
	publish bool
	// unbilled and aging keep the report projections' rows (snapshot.go).
	unbilled projCache[unbilledRow]
	aging    projCache[agingRow]
}

// NewPostgres returns a store on pool. pool must be the pool the audit ledger uses.
func NewPostgres(pool *sql.DB) *Postgres { return &Postgres{pool: pool} }

// PublishEvents makes p also write each billing event to core.events in the same
// transaction, for the module runtime. The core composes billing this way; a billing-only
// database has no core matters for those rows to point to.
func (p *Postgres) PublishEvents() *Postgres {
	p.publish = true
	return p
}

func (p *Postgres) Atomic(ctx context.Context, fn func(ctx context.Context) error) error {
	return audit.WithTx(ctx, p.pool, fn)
}

func (p *Postgres) Get(ctx context.Context, kind, id string, dst any) error {
	var body string
	err := audit.Conn(ctx, p.pool).QueryRowContext(ctx, `SELECT body FROM billing_records WHERE kind = $1 AND id = $2`, kind, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(body), dst)
}

func (p *Postgres) Put(ctx context.Context, kind, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c := indexOf(v)
	head, err := headOf(v)
	if err != nil {
		return err
	}
	_, err = audit.Conn(ctx, p.pool).ExecContext(ctx, `INSERT INTO billing_records (kind, id, body, matter_id, user_id, on_date, invoice_id, indexed, head, status, due_on)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, $9, $10)
		ON CONFLICT (kind, id) DO UPDATE SET body = EXCLUDED.body, matter_id = EXCLUDED.matter_id, user_id = EXCLUDED.user_id,
		on_date = EXCLUDED.on_date, invoice_id = EXCLUDED.invoice_id, indexed = 1, head = EXCLUDED.head, status = EXCLUDED.status,
		due_on = EXCLUDED.due_on`, kind, id, string(b), c.matter, c.user, c.date, c.invoice, head, c.status, c.due)
	if err != nil {
		return err
	}
	if err := syncProjections(ctx, audit.Conn(ctx, p.pool), kind, id, v); err != nil {
		return err
	}
	// Billing's events also go to core.events in the same transaction, so the module
	// runtime delivers time, expense, invoice and payment writes to subscribers.
	if ev, ok := v.(domain.Event); ok && kind == kindEvent && p.publish {
		return db.PublishEvent(ctx, audit.Conn(ctx, p.pool), ev)
	}
	return nil
}

func (p *Postgres) Delete(ctx context.Context, kind, id string) error {
	_, err := audit.Conn(ctx, p.pool).ExecContext(ctx, `DELETE FROM billing_records WHERE kind = $1 AND id = $2`, kind, id)
	return err
}

func (p *Postgres) List(ctx context.Context, kind string) ([][]byte, error) {
	rows, err := audit.Conn(ctx, p.pool).QueryContext(ctx, `SELECT body FROM billing_records WHERE kind = $1 ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		out = append(out, []byte(body))
	}
	return out, rows.Err()
}

func listOf[T any](ctx context.Context, st Store, kind string) ([]T, error) {
	raw, err := st.List(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(raw))
	for _, b := range raw {
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
