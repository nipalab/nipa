package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/registration"
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
)

func TestBuildBYOStorage(t *testing.T) {
	setup, err := buildBYOStorage(&config.Config{}, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Nil(t, setup, "no key wires nothing")
	require.False(t, setup.enabled())
	require.Nil(t, setup.runOptions(serverapp.Usecases{}))
	deps := serverapp.UsecaseDeps{}
	setup.wire(&deps)
	require.Nil(t, deps.ChunkStores)
	require.Nil(t, deps.ProjectStorage)

	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	_, err = buildBYOStorage(&config.Config{BYOStorageEncryptionKey: key}, nil, nil, nil, nil)
	require.ErrorContains(t, err, "SAAS_MODE")

	cfg := &config.Config{BYOStorageEncryptionKey: key, SaaSMode: true, BYOStorageAllowPrivate: true}
	_, err = buildBYOStorage(cfg, nil, nil, nil, nil)
	require.ErrorContains(t, err, "storage ledger")

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	setup, err = buildBYOStorage(cfg, nil, node, nil, storageusage.NewLedger(nil, storageusage.Config{}))
	require.NoError(t, err)
	require.True(t, setup.enabled())
	setup.wire(&deps)
	require.Same(t, setup.resolver, deps.ChunkStores)
	require.Same(t, setup.service, deps.ProjectStorage)
	require.Len(t, setup.runOptions(serverapp.Usecases{}), 1)
	setup.entitle(nil)
}

func TestOwnStorageEntitlement(t *testing.T) {
	require.Nil(t, ownStorageEntitlement(nil), "billing off keeps own storage open")
	var nilSetup *byoStorageSetup
	nilSetup.entitle(&billingSetup{})

	ledger := storageusage.NewLedger(nil, storageusage.Config{})
	cfg := paddleConfig()
	billingOn, err := buildBilling(cfg, nil, ledger)
	require.NoError(t, err)
	require.False(t, billingOn.ownStorage)
	require.Same(t, billingOn.service, ownStorageEntitlement(billingOn))

	cfg.PaddlePriceOwnStorageMonthly, cfg.PaddlePriceOwnStorageYearly = "e", "f"
	billingOn, err = buildBilling(cfg, nil, ledger)
	require.NoError(t, err)
	require.True(t, billingOn.ownStorage)
	require.Equal(t, &registration.IntervalPrices{Monthly: "e", Yearly: "f"}, billingOn.capabilities.Prices.OwnStorage)
	require.Same(t, billingOn.service, ownStorageEntitlement(billingOn))

	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	byo, err := buildBYOStorage(&config.Config{BYOStorageEncryptionKey: key, SaaSMode: true}, nil, node, nil, ledger)
	require.NoError(t, err)
	byo.entitle(billingOn)
}
