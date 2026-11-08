package serverapp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
)

func TestSSHAudience(t *testing.T) {
	require.Equal(t, "nipa.example.com", sshAudience(&config.Config{PublicBaseURL: "https://nipa.example.com"}))
	require.Equal(t, "127.0.0.1:8080", sshAudience(&config.Config{EmailBaseURL: "http://127.0.0.1:8080"}))
	require.Empty(t, sshAudience(&config.Config{}))
	require.Empty(t, sshAudience(&config.Config{PublicBaseURL: "ftp://bad"}))
}
