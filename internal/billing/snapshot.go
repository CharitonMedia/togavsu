package billing

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
)

// snapshot is a report projection's rows per kind, read at one version token (migration
// 1265). Rows are shared between requests and never changed in place.
type snapshot[T any] struct {
	token string
	kinds map[string][]T
	// derived holds values computed from the rows (per-matter totals), at most once
	// per snapshot in practice.
	derived sync.Map
}

// derive returns key's value from the snapshot, computing it with fn the first time.
func (s *snapshot[T]) derive(key string, fn func() (any, error)) (any, error) {
	if v, ok := s.derived.Load(key); ok {
		return v, nil
	}
	v, err := fn()
	if err != nil {
		return nil, err
	}
	v, _ = s.derived.LoadOrStore(key, v)
	return v, nil
}

// projCache keeps one projection's latest snapshot. It holds row data only: every report
// still checks each matter it shows through the access gate.
// ponytail: ceiling: one core process holds the rows (about 100,000 unbilled rows at the
// AC-017 seed, some 25 MB) and any write to the table makes the next report reread all
// of it. Upgrade path: apply each committed change to the snapshot instead of rereading.
// Trigger: unbilled or aging reports reloading on most requests, or a projection over a
// few hundred thousand rows.
type projCache[T any] struct {
	mu   sync.Mutex
	snap *snapshot[T]
}

// rows returns the projection's rows of kind, current as of the version token now
// committed: it reads the token and, when it has moved, rereads every kind's rows with
// load in one repeatable-read transaction, so the rows always match the token stored
// with them. Concurrent callers share one reload.
func (c *projCache[T]) rows(ctx context.Context, p *Postgres, name, kind string, load func(ctx context.Context, tx pgx.Tx) (map[string][]T, error)) ([]T, error) {
	snap, err := c.current(ctx, p, name, load)
	if err != nil {
		return nil, err
	}
	return snap.kinds[kind], nil
}

// current is the snapshot for the committed version token, reloaded when it moved.
func (c *projCache[T]) current(ctx context.Context, p *Postgres, name string, load func(ctx context.Context, tx pgx.Tx) (map[string][]T, error)) (*snapshot[T], error) {
	var token string
	if err := p.pool.QueryRowContext(ctx, `SELECT token FROM billing_projection_versions WHERE name = $1`, name).Scan(&token); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil && c.snap.token == token {
		return c.snap, nil
	}
	var snap *snapshot[T]
	err := p.native(ctx, func(pc *pgx.Conn) error {
		tx, err := pc.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var at string
		if err := tx.QueryRow(ctx, `SELECT token FROM billing_projection_versions WHERE name = $1`, name).Scan(&at); err != nil {
			return err
		}
		kinds, err := load(ctx, tx)
		if err != nil {
			return err
		}
		snap = &snapshot[T]{token: at, kinds: kinds}
		return tx.Commit(ctx)
	})
	if err != nil {
		return nil, err
	}
	c.snap = snap
	return snap, nil
}
