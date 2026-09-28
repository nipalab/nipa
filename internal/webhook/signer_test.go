package webhook

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSign(t *testing.T) {
	// Vector generated with:
	// printf '%s' '{"event":"push"}' | openssl dgst -sha256 -hmac 'test-secret'
	require.Equal(t,
		"sha256=548a95b095dc4ad2e3392a07dbe4100099c13ba9a3bd5a86dbc50eddf639b947",
		Sign("test-secret", []byte(`{"event":"push"}`)),
	)
}

func TestVerify(t *testing.T) {
	body := []byte(`{"event":"push"}`)
	signature := Sign("test-secret", body)

	require.True(t, Verify("test-secret", body, signature))
	require.False(t, Verify("other-secret", body, signature))
	require.False(t, Verify("test-secret", []byte(`{"event":"branch.created"}`), signature))
	require.False(t, Verify("test-secret", body, "sha1=548a95b0"))
	require.False(t, Verify("test-secret", body, "sha256=not-hex"))
	require.False(t, Verify("test-secret", body, "548a95b0"))
	require.False(t, Verify("test-secret", body, ""))
}
