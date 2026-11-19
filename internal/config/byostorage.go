package config

import "errors"

// BYOStorageEnabled reports whether bring-your-own storage servers are on:
// only when BYO_STORAGE_ENCRYPTION_KEY is set.
func (c *Config) BYOStorageEnabled() bool {
	key, err := c.BYOStorageKey()
	return err == nil && key != nil
}

// BYOStorageKey decodes BYO_STORAGE_ENCRYPTION_KEY (base64, at least 32
// bytes); nil without an error when it is unset.
func (c *Config) BYOStorageKey() ([]byte, error) {
	return decodeKey("BYO_STORAGE_ENCRYPTION_KEY", c.BYOStorageEncryptionKey)
}

// ValidateBYOStorage checks the bring-your-own storage settings: nothing is
// required while the key is unset; with it the key must decode and the
// server must run in SaaS mode with the storage ledger.
func (c *Config) ValidateBYOStorage() error {
	key, err := c.BYOStorageKey()
	if err != nil || key == nil {
		return err
	}
	if !c.SaaSMode {
		return errors.New("BYO_STORAGE_ENCRYPTION_KEY requires SAAS_MODE=1")
	}
	if !c.storageLedgerEnabled() {
		return errors.New("BYO_STORAGE_ENCRYPTION_KEY requires the storage ledger (STORAGE_LEDGER=cloud)")
	}
	return nil
}
