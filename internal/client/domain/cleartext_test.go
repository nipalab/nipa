package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCleartextRemote(t *testing.T) {
	remote := []string{
		"nipa.example.com",
		"nipa.example.com:6745",
		"http://nipa.example.com",
		"HTTP://nipa.example.com:6745/org/project",
		"10.0.0.5:6745",
		"[2001:db8::1]:6745",
		"localhost.example.com",
		"128.0.0.1",
	}
	for _, address := range remote {
		require.True(t, IsCleartextRemote(address), address)
	}
	safe := []string{
		"",
		"   ",
		"https://nipa.example.com",
		"HTTPS://nipa.example.com:8443",
		"localhost",
		"LOCALHOST:6745",
		"localhost.:6745",
		"http://localhost:6745",
		"127.0.0.1:6745",
		"127.9.8.7",
		"[::1]:6745",
		"http://[::1]",
		"::1",
		"[::ffff:127.0.0.1]:6745",
	}
	for _, address := range safe {
		require.False(t, IsCleartextRemote(address), address)
	}
}
