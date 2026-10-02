package db

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func testPostgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("NIPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NIPA_TEST_POSTGRES_DSN is not set")
	}
	return dsn
}

func TestMigrateUpDown(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(testPostgresDSN(t))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, MigrateDown(conn))
	require.NoError(t, MigrateUp(conn))

	var count int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE email = 'supernipa'").Scan(&count))
	require.Equal(t, 1, count)

	var exists bool
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'merge_request_reviews')").Scan(&exists))
	require.True(t, exists)

	require.NoError(t, MigrateDown(conn))
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'users')").Scan(&exists))
	require.False(t, exists)
}
