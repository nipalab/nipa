package main

import (
	"context"
	"errors"
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

func TestLedgerDefaultQuotaBytes(t *testing.T) {
	cfg := &config.Config{StorageLedgerDefaultQuotaBytes: 15, SaasFreeQuotaBytes: 50}
	require.Equal(t, int64(15), ledgerDefaultQuotaBytes(cfg))
	cfg.SaaSMode = true
	require.Equal(t, int64(50), ledgerDefaultQuotaBytes(cfg), "SaaS owners without a row get the free tier")
}

func TestQuotaReconciler(t *testing.T) {
	ledger := storageusage.NewLedger(nil, storageusage.Config{})
	require.Nil(t, quotaReconciler(&config.Config{}, nil, ledger, nil, nil), "outside SaaS mode")
	require.Nil(t, quotaReconciler(&config.Config{SaaSMode: true}, nil, nil, nil, nil), "without a ledger")
	require.NotNil(t, quotaReconciler(&config.Config{SaaSMode: true}, nil, ledger, nil, nil), "billing off builds a provider-less service")

	setup, err := buildBilling(paddleConfig(), nil, ledger)
	require.NoError(t, err)
	require.Same(t, setup.service, quotaReconciler(paddleConfig(), nil, ledger, setup, nil))
}

type stubReconcile struct {
	updated  int
	err      error
	deadline bool
}

func (s *stubReconcile) ReconcileQuotas(ctx context.Context) (int, error) {
	_, s.deadline = ctx.Deadline()
	return s.updated, s.err
}

func TestReconcileQuotas(t *testing.T) {
	ok := &stubReconcile{updated: 3}
	reconcileQuotas(context.Background(), ok)
	require.True(t, ok.deadline, "the run is bounded")

	failing := &stubReconcile{err: errors.New("db down")}
	reconcileQuotas(context.Background(), failing)
	require.True(t, failing.deadline)
}
