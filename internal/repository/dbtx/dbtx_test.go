package dbtx

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	require.NoError(t, err)
	return db
}

func countItems(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n))
	return n
}

func TestTransactor_WithinTx_Commits(t *testing.T) {
	db := newTestDB(t)
	wrapped := New(db)

	require.NoError(t, NewTransactor(db).WithinTx(context.Background(), func(ctx context.Context) error {
		_, err := wrapped.ExecContext(ctx, `INSERT INTO items (id, name) VALUES (1, 'a')`)
		return err
	}))

	require.Equal(t, 1, countItems(t, db))
}

func TestTransactor_WithinTx_RollsBackOnError(t *testing.T) {
	db := newTestDB(t)
	wrapped := New(db)
	wantErr := errors.New("boom")

	err := NewTransactor(db).WithinTx(context.Background(), func(ctx context.Context) error {
		if _, err := wrapped.ExecContext(ctx, `INSERT INTO items (id, name) VALUES (1, 'a')`); err != nil {
			return err
		}
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)
	require.Zero(t, countItems(t, db))
}

func TestTransactor_WithinTx_BeginError(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.Close())

	err := NewTransactor(db).WithinTx(context.Background(), func(context.Context) error { return nil })
	require.Error(t, err)
}

func TestDB_QueryContext_InsideTx(t *testing.T) {
	db := newTestDB(t)
	wrapped := New(db)

	require.NoError(t, NewTransactor(db).WithinTx(context.Background(), func(ctx context.Context) error {
		if _, err := wrapped.ExecContext(ctx, `INSERT INTO items (id, name) VALUES (1, 'a')`); err != nil {
			return err
		}
		rows, err := wrapped.QueryContext(ctx, `SELECT name FROM items WHERE id = 1`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		var name string
		if !rows.Next() {
			return errors.New("no row")
		}
		if err := rows.Scan(&name); err != nil {
			return err
		}
		require.Equal(t, "a", name)
		return rows.Err()
	}))
}

func TestDB_QueryContext_OutsideTx(t *testing.T) {
	db := newTestDB(t)
	wrapped := New(db)
	_, err := wrapped.ExecContext(context.Background(), `INSERT INTO items (id, name) VALUES (1, 'a')`)
	require.NoError(t, err)

	rows, err := wrapped.QueryContext(context.Background(), `SELECT name FROM items WHERE id = 1`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next())
}

func TestDB_QueryRowContext(t *testing.T) {
	db := newTestDB(t)
	wrapped := New(db)
	_, err := wrapped.ExecContext(context.Background(), `INSERT INTO items (id, name) VALUES (1, 'a')`)
	require.NoError(t, err)

	var name string
	require.NoError(t, wrapped.QueryRowContext(context.Background(), `SELECT name FROM items WHERE id = 1`).Scan(&name))
	require.Equal(t, "a", name)

	require.NoError(t, NewTransactor(db).WithinTx(context.Background(), func(ctx context.Context) error {
		var inside string
		return wrapped.QueryRowContext(ctx, `SELECT name FROM items WHERE id = 1`).Scan(&inside)
	}))
}

func TestDB_PrepareContext(t *testing.T) {
	db := newTestDB(t)
	wrapped := New(db)

	stmt, err := wrapped.PrepareContext(context.Background(), `SELECT 1`)
	require.NoError(t, err)
	require.NoError(t, stmt.Close())

	require.NoError(t, NewTransactor(db).WithinTx(context.Background(), func(ctx context.Context) error {
		stmt, err := wrapped.PrepareContext(ctx, `SELECT 1`)
		if err != nil {
			return err
		}
		return stmt.Close()
	}))
}
