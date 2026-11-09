package db

import (
	"context"
	"database/sql"
)

// Querier is the part of *sql.DB and *sql.Tx that stores use.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type txKey struct{}

type txValue struct {
	pool *sql.DB
	tx   *sql.Tx
}

// WithTx runs fn in one transaction on pool. A store that reads its connection with
// Conn(ctx, pool) joins it, so every write fn makes commits or rolls back together.
// A call inside another WithTx on the same pool joins the outer transaction.
func WithTx(ctx context.Context, pool *sql.DB, fn func(ctx context.Context) error) error {
	if Tx(ctx, pool) != nil {
		return fn(ctx)
	}
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(context.WithValue(ctx, txKey{}, txValue{pool: pool, tx: tx})); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

// Tx returns the transaction WithTx opened on pool, or nil outside one.
func Tx(ctx context.Context, pool *sql.DB) *sql.Tx {
	v, ok := ctx.Value(txKey{}).(txValue)
	if !ok || v.pool != pool {
		return nil
	}
	return v.tx
}

// Conn returns the open transaction on pool, or pool itself.
func Conn(ctx context.Context, pool *sql.DB) Querier {
	if tx := Tx(ctx, pool); tx != nil {
		return tx
	}
	return pool
}
