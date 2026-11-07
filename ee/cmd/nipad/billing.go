package main

import (
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/ee/billing"
	"github.com/nipalab/nipa/ee/billing/paddle"
	"github.com/nipalab/nipa/ee/registration"
	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/serverapp"
)

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
	service := billing.New(postgres.NewBillingRepository(dbConn), ledger, dbtx.NewTransactor(dbConn), billing.Config{
		FreeSeats:         cfg.BillingFreeSeats,
		FreeQuotaBytes:    cfg.SaasFreeQuotaBytes,
		SeatIncludedBytes: cfg.BillingSeatIncludedBytes,
		StoragePackBytes:  cfg.BillingStoragePackBytes,
	}).WithProvider(provider)
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
