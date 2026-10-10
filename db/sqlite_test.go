package db

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithSQLiteOptions(t *testing.T) {
	require.Equal(t,
		"nipa.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate",
		WithSQLiteOptions("nipa.db"))
	require.Equal(t,
		"nipa.db?mode=rwc&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate",
		WithSQLiteOptions("nipa.db?mode=rwc"))
	require.Equal(t,
		"nipa.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate",
		WithSQLiteOptions("nipa.db?_pragma=busy_timeout(5000)"))
	require.Equal(t,
		"nipa.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(delete)&_txlock=immediate",
		WithSQLiteOptions("nipa.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(delete)"))
	require.Equal(t,
		"nipa.db?_txlock=deferred&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)",
		WithSQLiteOptions("nipa.db?_txlock=deferred"),
		"an explicit _txlock is preserved")
}
