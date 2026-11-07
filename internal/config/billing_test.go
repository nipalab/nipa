package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func validBillingConfig() Config {
	return Config{
		SaaSMode:                  true,
		BillingProvider:           "Paddle",
		PaddleEnvironment:         "sandbox",
		PaddleAPIKey:              "key",
		PaddleWebhookSecret:       "secret",
		PaddleClientToken:         "token",
		PaddlePriceSeatMonthly:    "pri_seat_m",
		PaddlePriceSeatYearly:     "pri_seat_y",
		PaddlePriceStorageMonthly: "pri_storage_m",
		PaddlePriceStorageYearly:  "pri_storage_y",
		BillingFreeSeats:          1,
		BillingSeatIncludedBytes:  10,
		BillingStoragePackBytes:   20,
	}
}

func TestValidateBilling_OffNeedsNothing(t *testing.T) {
	for _, provider := range []string{"", "off", " OFF "} {
		cfg := Config{BillingProvider: provider}
		require.Equal(t, BillingProviderOff, cfg.BillingProviderName())
		require.NoError(t, cfg.ValidateBilling())
	}
}

func TestValidateBilling_Valid(t *testing.T) {
	cfg := validBillingConfig()
	require.Equal(t, BillingProviderPaddle, cfg.BillingProviderName())
	require.NoError(t, cfg.ValidateBilling())

	cfg.StorageLedger = "cloud"
	cfg.PaddleEnvironment = "production"
	require.NoError(t, cfg.ValidateBilling())
}

func TestValidateBilling_Errors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"unknown provider", func(c *Config) { c.BillingProvider = "stripe" }, "BILLING_PROVIDER"},
		{"saas mode", func(c *Config) { c.SaaSMode = false; c.StorageLedger = "cloud" }, "SAAS_MODE"},
		{"storage ledger", func(c *Config) { c.StorageLedger = "none" }, "STORAGE_LEDGER"},
		{"environment missing", func(c *Config) { c.PaddleEnvironment = "" }, "PADDLE_ENVIRONMENT"},
		{"environment invalid", func(c *Config) { c.PaddleEnvironment = "staging" }, "PADDLE_ENVIRONMENT"},
		{"api key", func(c *Config) { c.PaddleAPIKey = " " }, "PADDLE_API_KEY"},
		{"webhook secret", func(c *Config) { c.PaddleWebhookSecret = "" }, "PADDLE_WEBHOOK_SECRET"},
		{"client token", func(c *Config) { c.PaddleClientToken = "" }, "PADDLE_CLIENT_TOKEN"},
		{"seat monthly", func(c *Config) { c.PaddlePriceSeatMonthly = "" }, "PADDLE_PRICE_SEAT_MONTHLY"},
		{"seat yearly", func(c *Config) { c.PaddlePriceSeatYearly = "" }, "PADDLE_PRICE_SEAT_YEARLY"},
		{"storage monthly", func(c *Config) { c.PaddlePriceStorageMonthly = "" }, "PADDLE_PRICE_STORAGE_MONTHLY"},
		{"storage yearly", func(c *Config) { c.PaddlePriceStorageYearly = "" }, "PADDLE_PRICE_STORAGE_YEARLY"},
		{"free seats", func(c *Config) { c.BillingFreeSeats = 0 }, "BILLING_FREE_SEATS"},
		{"seat bytes zero", func(c *Config) { c.BillingSeatIncludedBytes = 0 }, "BILLING_SEAT_INCLUDED_BYTES"},
		{"pack bytes negative", func(c *Config) { c.BillingStoragePackBytes = -1 }, "BILLING_STORAGE_PACK_BYTES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBillingConfig()
			tc.mutate(&cfg)
			err := cfg.ValidateBilling()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
