package db

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithSQLitePragmas(t *testing.T) {
	require.Equal(t,
		"nipa.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)",
		WithSQLitePragmas("nipa.db"))
	require.Equal(t,
		"nipa.db?mode=rwc&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)",
		WithSQLitePragmas("nipa.db?mode=rwc"))
	require.Equal(t,
		"nipa.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)",
		WithSQLitePragmas("nipa.db?_pragma=busy_timeout(5000)"))
	require.Equal(t,
		"nipa.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(delete)",
		WithSQLitePragmas("nipa.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(delete)"))
}
