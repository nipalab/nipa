package db

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

// OpenSQLite opens a SQLite database with the pragmas the server relies on.
// WAL lets readers run while the single writer commits, and the busy timeout
// absorbs the remaining short lock overlaps instead of failing with
// SQLITE_BUSY. Both are applied per connection through the DSN.
func OpenSQLite(dsn string) (*sql.DB, error) {
	return sql.Open("sqlite", WithSQLitePragmas(dsn))
}

// WithSQLitePragmas appends the missing server pragmas to a DSN, preserving
// any pragmas the caller configured explicitly.
func WithSQLitePragmas(dsn string) string {
	var missing []string
	if !strings.Contains(dsn, "busy_timeout") {
		missing = append(missing, "_pragma=busy_timeout(10000)")
	}
	if !strings.Contains(dsn, "journal_mode") {
		missing = append(missing, "_pragma=journal_mode(WAL)")
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
