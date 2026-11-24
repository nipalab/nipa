package db

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
)

func TestPoolConfigNormalized(t *testing.T) {
	require.Equal(t, 32, DefaultMaxOpenConns)
	require.Equal(t, config.DefaultDatabaseMaxOpenConns, DefaultMaxOpenConns)

	tests := []struct {
		name string
		in   PoolConfig
		want PoolConfig
	}{
		{
			name: "zero value is bounded and keeps every connection idle",
			in:   PoolConfig{},
			want: PoolConfig{MaxOpenConns: DefaultMaxOpenConns, MaxIdleConns: DefaultMaxOpenConns},
		},
		{
			name: "negative values fall back",
			in:   PoolConfig{MaxOpenConns: -1, MaxIdleConns: -1, ConnMaxLifetime: -time.Second, ConnMaxIdleTime: -time.Second},
			want: PoolConfig{MaxOpenConns: DefaultMaxOpenConns, MaxIdleConns: DefaultMaxOpenConns},
		},
		{
			name: "idle follows open",
			in:   PoolConfig{MaxOpenConns: 64},
			want: PoolConfig{MaxOpenConns: 64, MaxIdleConns: 64},
		},
		{
			name: "explicit values are kept",
			in:   PoolConfig{MaxOpenConns: 64, MaxIdleConns: 8, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: time.Minute},
			want: PoolConfig{MaxOpenConns: 64, MaxIdleConns: 8, ConnMaxLifetime: time.Hour, ConnMaxIdleTime: time.Minute},
		},
		{
			name: "idle is capped at open",
			in:   PoolConfig{MaxOpenConns: 4, MaxIdleConns: 10},
			want: PoolConfig{MaxOpenConns: 4, MaxIdleConns: 4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.in.normalized())
		})
	}
}

func TestAdminPoolConfig(t *testing.T) {
	got := AdminPoolConfig().normalized()
	require.Equal(t, PoolConfig{MaxOpenConns: 4, MaxIdleConns: 4}, got)
}

// sql.Open does not connect, so the limits are observable without a server.
func TestPoolConfigApply(t *testing.T) {
	conn, err := sql.Open("pgx", "postgres://nipa:nipa@127.0.0.1:1/nipa?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	PoolConfig{}.apply(conn)
	require.Equal(t, DefaultMaxOpenConns, conn.Stats().MaxOpenConnections)

	PoolConfig{MaxOpenConns: 7}.apply(conn)
	require.Equal(t, 7, conn.Stats().MaxOpenConnections)
}

func TestOpenUnreachable(t *testing.T) {
	_, err := Open("postgres://nipa:nipa@127.0.0.1:1/nipa?sslmode=disable&connect_timeout=1", PoolConfig{})
	require.Error(t, err)
}

func (s *MigrateSuite) TestOpenAppliesPool() {
	conn, err := Open(testDSN, PoolConfig{MaxOpenConns: 3})
	s.Require().NoError(err)
	defer func() { s.Require().NoError(conn.Close()) }()
	s.Require().Equal(3, conn.Stats().MaxOpenConnections)

	// Idle follows open: three connections used at once all stay pooled.
	ctx := s.T().Context()
	held := make([]*sql.Conn, 0, 3)
	for range 3 {
		c, err := conn.Conn(ctx)
		s.Require().NoError(err)
		held = append(held, c)
	}
	for _, c := range held {
		s.Require().NoError(c.Close())
	}
	stats := conn.Stats()
	s.Require().Equal(3, stats.OpenConnections)
	s.Require().Equal(3, stats.Idle)

	def, err := Open(testDSN, PoolConfig{})
	s.Require().NoError(err)
	defer func() { s.Require().NoError(def.Close()) }()
	s.Require().Equal(DefaultMaxOpenConns, def.Stats().MaxOpenConnections)
}
