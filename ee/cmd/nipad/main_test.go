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
		BillingFreeSeats: 3, BillingSeatIncludedBytes: 50, BillingStoragePackBytes: 100, SaasFreeQuotaBytes: 10,
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
		FreeSeats: 3, FreeQuotaBytes: 10, SeatIncludedBytes: 50, StoragePackBytes: 100,
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
	block    bool
	ran      chan struct{}
}

func (s *stubReconcile) ReconcileQuotas(ctx context.Context) (int, error) {
	_, s.deadline = ctx.Deadline()
	if s.ran != nil {
		close(s.ran)
	}
	if s.block {
		<-ctx.Done()
		return s.updated, ctx.Err()
	}
	return s.updated, s.err
}

func TestReconcileQuotas(t *testing.T) {
	reconcileQuotas(context.Background(), &stubReconcile{updated: 3})
	reconcileQuotas(context.Background(), &stubReconcile{err: errors.New("db down")})
}

func TestQuotaReconcileJob(t *testing.T) {
	require.NoError(t, newQuotaReconcileJob(&stubReconcile{}).Stop(context.Background()), "never started")

	done := &stubReconcile{updated: 1, ran: make(chan struct{})}
	job := newQuotaReconcileJob(done)
	job.Start()
	<-done.ran
	require.NoError(t, job.Stop(context.Background()))
	require.True(t, done.deadline, "the run is bounded")

	blocked := &stubReconcile{block: true, ran: make(chan struct{})}
	job = newQuotaReconcileJob(blocked)
	job.Start()
	<-blocked.ran
	require.NoError(t, job.Stop(context.Background()), "stop cancels a running reconcile")
}

func TestQuotaReconcileJob_StopDeadline(t *testing.T) {
	release := make(chan struct{})
	job := newQuotaReconcileJob(reconcileFunc(func(context.Context) (int, error) {
		<-release
		return 0, nil
	}))
	job.Start()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, job.Stop(ctx), context.Canceled)
	close(release)
}

type reconcileFunc func(context.Context) (int, error)

func (f reconcileFunc) ReconcileQuotas(ctx context.Context) (int, error) { return f(ctx) }
