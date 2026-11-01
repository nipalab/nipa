package ratelimit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies("127.0.0.1, 10.0.0.0/8 ,::1,")
	require.NoError(t, err)
	assert.Len(t, got, 3)

	_, err = ParseTrustedProxies("nope")
	assert.Error(t, err)

	got, err = ParseTrustedProxies("")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestClientIP(t *testing.T) {
	proxies, err := ParseTrustedProxies("127.0.0.1,::1,10.0.0.0/8")
	require.NoError(t, err)

	tests := []struct {
		name, remote, xff, want string
	}{
		{"direct peer", "203.0.113.5:4000", "", "203.0.113.5"},
		{"untrusted peer ignores xff", "203.0.113.5:4000", "1.2.3.4", "203.0.113.5"},
		{"trusted peer uses xff", "127.0.0.1:5000", "198.51.100.7", "198.51.100.7"},
		{"spoofed left hop ignored", "127.0.0.1:5000", "1.1.1.1, 198.51.100.7", "198.51.100.7"},
		{"skips trusted hops", "127.0.0.1:5000", "198.51.100.7, 10.1.2.3", "198.51.100.7"},
		{"all trusted falls back to peer", "127.0.0.1:5000", "10.0.0.1", "127.0.0.1"},
		{"garbage hop falls back to peer", "127.0.0.1:5000", "not-an-ip", "127.0.0.1"},
		{"ipv6 peer", "[::1]:5000", "2001:db8::1", "2001:db8::1"},
		{"mapped ipv4", "[::ffff:127.0.0.1]:5000", "198.51.100.7", "198.51.100.7"},
		{"bare host", "203.0.113.9", "", "203.0.113.9"},
		{"unparsable remote", "weird", "1.2.3.4", "weird"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClientIP(tt.remote, tt.xff, proxies))
		})
	}
}
