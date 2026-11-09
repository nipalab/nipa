package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/ee/billing"
	"github.com/nipalab/nipa/ee/billing/paddle"
	"github.com/nipalab/nipa/ee/registration"
	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/usecase"
)

const quotaReconcileTimeout = 10 * time.Minute

type billingSetup struct {
	service      *billing.Service
	capabilities *registration.BillingCapabilities
}

// buildBilling validates the billing settings and wires the Paddle provider.
// With BILLING_PROVIDER=off it returns nil and nothing is gated.
func buildBilling(cfg *config.Config, dbConn *sql.DB, ledger *storageusage.Ledger) (*billingSetup, error) {
	if err := cfg.ValidateBilling(); err != nil {
		return nil, err
	}
	if cfg.BillingProviderName() == config.BillingProviderOff {
		return nil, nil
	}
	if ledger == nil {
		return nil, errors.New("billing requires the storage ledger")
	}
	environment := strings.ToLower(strings.TrimSpace(cfg.PaddleEnvironment))
	provider := paddle.New(paddle.Config{
		Environment:   environment,
		APIKey:        cfg.PaddleAPIKey,
		WebhookSecret: cfg.PaddleWebhookSecret,
		Prices: paddle.Prices{
			SeatMonthly:    cfg.PaddlePriceSeatMonthly,
			SeatYearly:     cfg.PaddlePriceSeatYearly,
			StorageMonthly: cfg.PaddlePriceStorageMonthly,
			StorageYearly:  cfg.PaddlePriceStorageYearly,
		},
	})
	service := newBillingService(cfg, dbConn, ledger).WithProvider(provider)
	slog.Info("billing enabled", "provider", provider.Name(), "environment", environment, "free_seats", cfg.BillingFreeSeats)
	return &billingSetup{
		service: service,
		capabilities: &registration.BillingCapabilities{
			Provider:    provider.Name(),
			Environment: environment,
			ClientToken: cfg.PaddleClientToken,
			Prices: registration.BillingPrices{
				Seat:    registration.IntervalPrices{Monthly: cfg.PaddlePriceSeatMonthly, Yearly: cfg.PaddlePriceSeatYearly},
				Storage: registration.IntervalPrices{Monthly: cfg.PaddlePriceStorageMonthly, Yearly: cfg.PaddlePriceStorageYearly},
			},
			FreeSeats:         cfg.BillingFreeSeats,
			FreeQuotaBytes:    cfg.SaasFreeQuotaBytes,
			SeatIncludedBytes: cfg.BillingSeatIncludedBytes,
			StoragePackBytes:  cfg.BillingStoragePackBytes,
		},
	}, nil
}

// runOptions registers the authenticated billing routes and the webhook root.
func (b *billingSetup) runOptions() []serverapp.Option {
	if b == nil {
		return nil
	}
	return []serverapp.Option{
		serverapp.WithRoutes(func(ws *restful.WebService) {
			billing.RegisterRoutes(ws, b.service)
		}),
		serverapp.WithWebService(billing.WebhookService(b.service)),
	}
}

func (b *billingSetup) capabilitiesOrNil() *registration.BillingCapabilities {
	if b == nil {
		return nil
	}
	return b.capabilities
}

func newBillingService(cfg *config.Config, dbConn *sql.DB, ledger *storageusage.Ledger) *billing.Service {
	return billing.New(postgres.NewBillingRepository(dbConn), ledger, dbtx.NewTransactor(dbConn), billing.Config{
		FreeSeats:         cfg.BillingFreeSeats,
		FreeQuotaBytes:    cfg.SaasFreeQuotaBytes,
		SeatIncludedBytes: cfg.BillingSeatIncludedBytes,
		StoragePackBytes:  cfg.BillingStoragePackBytes,
	})
}

// quotaReconciler returns the service that recomputes stored owner quotas at
// startup in SaaS mode: the billing service, or a provider-less one (free
// entitlement for everyone) when billing is off.
func quotaReconciler(cfg *config.Config, dbConn *sql.DB, ledger *storageusage.Ledger, setup *billingSetup, audit usecase.AuditRecorder) *billing.Service {
	if !cfg.SaaSMode || ledger == nil {
		return nil
	}
	if setup != nil {
		return setup.service
	}
	return newBillingService(cfg, dbConn, ledger).WithAudit(audit)
}

type quotaReconcile interface {
	ReconcileQuotas(ctx context.Context) (int, error)
}

// quotaReconcileJob runs the reconcile in the background so a slow run never
// delays readiness; shutdown cancels it between owner transactions.
type quotaReconcileJob struct {
	reconciler quotaReconcile
	cancel     context.CancelFunc
	done       chan struct{}
}

func newQuotaReconcileJob(r quotaReconcile) *quotaReconcileJob {
	return &quotaReconcileJob{reconciler: r}
}

func (j *quotaReconcileJob) Start() {
	ctx, cancel := context.WithTimeout(context.Background(), quotaReconcileTimeout)
	j.cancel = cancel
	j.done = make(chan struct{})
	go func() {
		defer close(j.done)
		defer cancel()
		reconcileQuotas(ctx, j.reconciler)
	}()
}

func (j *quotaReconcileJob) Stop(ctx context.Context) error {
	if j.cancel == nil {
		return nil
	}
	j.cancel()
	select {
	case <-j.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reconcileQuotas only logs a failure: the stored quotas stay in effect.
func reconcileQuotas(ctx context.Context, r quotaReconcile) {
	updated, err := r.ReconcileQuotas(ctx)
	if err != nil {
		slog.Warn("owner quota reconcile failed", "updated", updated, "err", err)
		return
	}
	slog.Info("owner quotas reconciled", "updated", updated)
}
