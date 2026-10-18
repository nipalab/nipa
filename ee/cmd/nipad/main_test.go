package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
)

func TestCreateStorageLedger(t *testing.T) {
	ledger, err := createStorageLedger(&config.Config{}, nil)
	require.NoError(t, err)
	require.Nil(t, ledger, "an empty STORAGE_LEDGER disables the ledger")

	ledger, err = createStorageLedger(&config.Config{StorageLedger: "cloud"}, nil)
	require.NoError(t, err)
	require.NotNil(t, ledger)

	ledger, err = createStorageLedger(&config.Config{SaaSMode: true}, nil)
	require.NoError(t, err)
	require.NotNil(t, ledger, "SAAS_MODE implies the cloud ledger so seeded quotas are enforced")

	_, err = createStorageLedger(&config.Config{StorageLedger: "bogus"}, nil)
	require.Error(t, err)
}
