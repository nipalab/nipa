package db

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

// OpenSQLite opens a SQLite database with the connection options the server
// relies on. WAL lets readers run while the single writer commits, the busy
// timeout absorbs short lock overlaps instead of failing with SQLITE_BUSY, and
// immediate transactions take the write lock at BEGIN so a transaction that
// read first can never fail upgrading mid-way with SQLITE_BUSY_SNAPSHOT. All
// are applied per connection through the DSN.
func OpenSQLite(dsn string) (*sql.DB, error) {
	return sql.Open("sqlite", WithSQLiteOptions(dsn))
}

// WithSQLiteOptions appends the missing server options to a DSN, preserving
// any the caller configured explicitly.
func WithSQLiteOptions(dsn string) string {
	var missing []string
	if !strings.Contains(dsn, "busy_timeout") {
		missing = append(missing, "_pragma=busy_timeout(10000)")
	}
	if !strings.Contains(dsn, "journal_mode") {
		missing = append(missing, "_pragma=journal_mode(WAL)")
	}
	if !strings.Contains(dsn, "_txlock") {
		missing = append(missing, "_txlock=immediate")
	}
	if len(missing) == 0 {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + strings.Join(missing, "&")
}
