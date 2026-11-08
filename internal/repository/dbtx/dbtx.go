// Package dbtx routes repository queries through the transaction carried in
// the context, falling back to the database handle when there is none.
package dbtx

import (
	"context"
	"database/sql"
)

type txKey struct{}

// Transactor runs a function inside a database transaction.
type Transactor struct {
	db *sql.DB
}

func NewTransactor(db *sql.DB) *Transactor {
	return &Transactor{db: db}
}

// WithinTx begins a transaction, runs fn with a context carrying it and commits
// when fn succeeds; any error rolls the transaction back. When ctx already
// carries a transaction, fn joins it and the outer caller commits or rolls back.
func (t *Transactor) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(WithTx(ctx, tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// WithTx returns a context whose repository queries use tx.
func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// Detach returns a context whose queries run outside any transaction ctx
// carries, e.g. to persist a record that must survive the caller's rollback.
func Detach(ctx context.Context) context.Context {
	return context.WithValue(ctx, txKey{}, (*sql.Tx)(nil))
}

func txFromContext(ctx context.Context) (*sql.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	return tx, ok && tx != nil
}

// DB wraps a database handle so queries run inside the transaction carried by
// the context, when present.
type DB struct {
	db *sql.DB
}

func New(db *sql.DB) *DB {
	return &DB{db: db}
}

func (h *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx, ok := txFromContext(ctx); ok {
		return tx.ExecContext(ctx, query, args...)
	}
	return h.db.ExecContext(ctx, query, args...)
}

func (h *DB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	if tx, ok := txFromContext(ctx); ok {
		return tx.PrepareContext(ctx, query)
	}
	return h.db.PrepareContext(ctx, query)
}

func (h *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tx, ok := txFromContext(ctx); ok {
		return tx.QueryContext(ctx, query, args...)
	}
	return h.db.QueryContext(ctx, query, args...)
}

func (h *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if tx, ok := txFromContext(ctx); ok {
		return tx.QueryRowContext(ctx, query, args...)
	}
	return h.db.QueryRowContext(ctx, query, args...)
}
