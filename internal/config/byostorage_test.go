package config

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBYOStorage(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

	off := Config{}
	require.False(t, off.BYOStorageEnabled())
	require.NoError(t, off.ValidateBYOStorage())

	on := Config{BYOStorageEncryptionKey: key, SaaSMode: true}
	require.True(t, on.BYOStorageEnabled())
	require.NoError(t, on.ValidateBYOStorage())
	decoded, err := on.BYOStorageKey()
	require.NoError(t, err)
	require.Len(t, decoded, 32)

	noSaaS := Config{BYOStorageEncryptionKey: key}
	require.ErrorContains(t, noSaaS.ValidateBYOStorage(), "SAAS_MODE")

	noLedger := Config{BYOStorageEncryptionKey: key, SaaSMode: true, StorageLedger: "none"}
	require.ErrorContains(t, noLedger.ValidateBYOStorage(), "STORAGE_LEDGER")

	short := Config{BYOStorageEncryptionKey: base64.StdEncoding.EncodeToString([]byte("short")), SaaSMode: true}
	require.False(t, short.BYOStorageEnabled())
	require.ErrorContains(t, short.ValidateBYOStorage(), "BYO_STORAGE_ENCRYPTION_KEY must decode to at least 32 bytes")

	invalid := Config{BYOStorageEncryptionKey: "!!!", SaaSMode: true}
	require.ErrorContains(t, invalid.ValidateBYOStorage(), "BYO_STORAGE_ENCRYPTION_KEY must be base64")
}
