package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/registration"
	"github.com/nipalab/nipa/ee/storageusage"
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

func paddleConfig() *config.Config {
	return &config.Config{
		BillingProvider: "paddle", SaaSMode: true, PaddleEnvironment: " Sandbox ",
		PaddleAPIKey: "k", PaddleWebhookSecret: "s", PaddleClientToken: "t",
		PaddlePriceSeatMonthly: "a", PaddlePriceSeatYearly: "b", PaddlePriceStorageMonthly: "c", PaddlePriceStorageYearly: "d",
		BillingFreeSeats: 1, BillingSeatIncludedBytes: 1, BillingStoragePackBytes: 1,
	}
}

func TestBuildBilling(t *testing.T) {
	for _, cfg := range []*config.Config{{}, {BillingProvider: "off"}} {
		setup, err := buildBilling(cfg, nil, nil)
		require.NoError(t, err)
		require.Nil(t, setup, "billing off wires nothing")
		require.Nil(t, setup.runOptions())
		require.Nil(t, setup.capabilitiesOrNil())
	}

	_, err := buildBilling(&config.Config{BillingProvider: "paddle"}, nil, nil)
	require.ErrorContains(t, err, "SAAS_MODE", "validation runs first")

	_, err = buildBilling(paddleConfig(), nil, nil)
	require.ErrorContains(t, err, "storage ledger")

	setup, err := buildBilling(paddleConfig(), nil, storageusage.NewLedger(nil, storageusage.Config{}))
	require.NoError(t, err)
	require.NotNil(t, setup.service)
	require.Len(t, setup.runOptions(), 2, "authenticated routes and the webhook root")
	require.Equal(t, &registration.BillingCapabilities{
		Provider: "paddle", Environment: "sandbox", ClientToken: "t",
		Prices: registration.BillingPrices{
			Seat:    registration.IntervalPrices{Monthly: "a", Yearly: "b"},
			Storage: registration.IntervalPrices{Monthly: "c", Yearly: "d"},
		},
	}, setup.capabilitiesOrNil())
}
