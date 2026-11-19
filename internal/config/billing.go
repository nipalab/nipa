package config

import (
	"fmt"
	"strings"
)

const (
	BillingProviderOff    = "off"
	BillingProviderPaddle = "paddle"
)

// BillingProviderName normalizes BILLING_PROVIDER; empty means off.
func (c *Config) BillingProviderName() string {
	name := strings.ToLower(strings.TrimSpace(c.BillingProvider))
	if name == "" {
		return BillingProviderOff
	}
	return name
}

// ValidateBilling checks the billing settings. With the provider off nothing
// is required; otherwise every key must be set and nothing falls back to a
// built-in commercial value.
func (c *Config) ValidateBilling() error {
	provider := c.BillingProviderName()
	switch provider {
	case BillingProviderOff:
		return nil
	case BillingProviderPaddle:
	default:
		return fmt.Errorf("unknown BILLING_PROVIDER %q (use off or paddle)", c.BillingProvider)
	}
	if !c.SaaSMode {
		return fmt.Errorf("BILLING_PROVIDER=%s requires SAAS_MODE=1", provider)
	}
	if !c.storageLedgerEnabled() {
		return fmt.Errorf("BILLING_PROVIDER=%s requires the storage ledger (STORAGE_LEDGER=cloud)", provider)
	}
	switch env := strings.ToLower(strings.TrimSpace(c.PaddleEnvironment)); env {
	case "sandbox", "production":
	case "":
		return fmt.Errorf("PADDLE_ENVIRONMENT is required")
	default:
		return fmt.Errorf("PADDLE_ENVIRONMENT must be sandbox or production, got %q", c.PaddleEnvironment)
	}
	for _, setting := range []struct{ key, value string }{
		{"PADDLE_API_KEY", c.PaddleAPIKey},
		{"PADDLE_WEBHOOK_SECRET", c.PaddleWebhookSecret},
		{"PADDLE_CLIENT_TOKEN", c.PaddleClientToken},
		{"PADDLE_PRICE_SEAT_MONTHLY", c.PaddlePriceSeatMonthly},
		{"PADDLE_PRICE_SEAT_YEARLY", c.PaddlePriceSeatYearly},
		{"PADDLE_PRICE_STORAGE_MONTHLY", c.PaddlePriceStorageMonthly},
		{"PADDLE_PRICE_STORAGE_YEARLY", c.PaddlePriceStorageYearly},
	} {
		if strings.TrimSpace(setting.value) == "" {
			return fmt.Errorf("%s is required", setting.key)
		}
	}
	if err := c.validateOwnStoragePrices(); err != nil {
		return err
	}
	if c.BillingFreeSeats < 1 {
		return fmt.Errorf("BILLING_FREE_SEATS must be at least 1 (the owner is a seat)")
	}
	for _, setting := range []struct {
		key   string
		value int64
	}{
		{"BILLING_SEAT_INCLUDED_BYTES", c.BillingSeatIncludedBytes},
		{"BILLING_STORAGE_PACK_BYTES", c.BillingStoragePackBytes},
	} {
		if setting.value <= 0 {
			return fmt.Errorf("%s must be greater than 0 (0 means unlimited in the ledger)", setting.key)
		}
	}
	return nil
}

// OwnStorageOffered reports whether the Own storage add-on has prices.
func (c *Config) OwnStorageOffered() bool {
	return strings.TrimSpace(c.PaddlePriceOwnStorageMonthly) != "" && strings.TrimSpace(c.PaddlePriceOwnStorageYearly) != ""
}

func (c *Config) validateOwnStoragePrices() error {
	monthly := strings.TrimSpace(c.PaddlePriceOwnStorageMonthly)
	yearly := strings.TrimSpace(c.PaddlePriceOwnStorageYearly)
	if monthly == "" && yearly == "" {
		return nil
	}
	if monthly == "" || yearly == "" {
		return fmt.Errorf("set both PADDLE_PRICE_OWN_STORAGE_MONTHLY and PADDLE_PRICE_OWN_STORAGE_YEARLY or neither")
	}
	seen := map[string]bool{}
	for _, id := range []string{c.PaddlePriceSeatMonthly, c.PaddlePriceSeatYearly, c.PaddlePriceStorageMonthly, c.PaddlePriceStorageYearly, monthly, yearly} {
		id = strings.TrimSpace(id)
		if seen[id] {
			return fmt.Errorf("PADDLE_PRICE_* ids must be distinct, %q is used twice", id)
		}
		seen[id] = true
	}
	return nil
}

func (c *Config) storageLedgerEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(c.StorageLedger)) {
	case "cloud":
		return true
	case "":
		return c.SaaSMode
	default:
		return false
	}
}
