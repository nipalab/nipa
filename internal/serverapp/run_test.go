package serverapp

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
)

func TestSplitAllowlist(t *testing.T) {
	require.Nil(t, SplitAllowlist("   "))
	require.Equal(t, []string{"https://a.example"}, SplitAllowlist("https://a.example"))
	require.Equal(t, []string{"https://a.example", "https://b.example"}, SplitAllowlist("https://a.example,https://b.example"))
}

func TestIsAPIPath(t *testing.T) {
	require.True(t, isAPIPath("/auth/login"))
	require.True(t, isAPIPath("/docs"))
	require.True(t, isAPIPath("/api/v1/orgs"))
	require.False(t, isAPIPath("/repository/browse"))
}

func TestRunReturnsListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	cfg := &config.Config{
		ServerAddress: "127.0.0.1",
		ServerPort:    ln.Addr().(*net.TCPAddr).Port,
	}

	require.Error(t, Run(cfg, NewRegistry(Usecases{}), nil))
}
