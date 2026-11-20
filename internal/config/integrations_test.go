package config

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntegrations(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

	off := Config{}
	require.NoError(t, off.ValidateIntegrations())
	decoded, err := off.IntegrationsKey()
	require.NoError(t, err)
	require.Nil(t, decoded)
	list, err := off.IssueTrackerAllowlist()
	require.NoError(t, err)
	require.Empty(t, list)

	on := Config{IntegrationsEncryptionKey: key, IssueTrackerEgressAllowlist: " jira.internal , 10.0.0.0/8,,127.0.0.1 "}
	require.NoError(t, on.ValidateIntegrations())
	decoded, err = on.IntegrationsKey()
	require.NoError(t, err)
	require.Len(t, decoded, 32)
	list, err = on.IssueTrackerAllowlist()
	require.NoError(t, err)
	require.Equal(t, []string{"jira.internal", "10.0.0.0/8", "127.0.0.1"}, list)

	short := Config{IntegrationsEncryptionKey: base64.StdEncoding.EncodeToString([]byte("short"))}
	require.ErrorContains(t, short.ValidateIntegrations(), "INTEGRATIONS_ENCRYPTION_KEY must decode to at least 32 bytes")

	badList := Config{IntegrationsEncryptionKey: key, IssueTrackerEgressAllowlist: "https://jira.example.com"}
	require.ErrorContains(t, badList.ValidateIntegrations(), "invalid egress allowlist entry")
}
