package config

import (
	"strings"

	"github.com/nipalab/nipa/internal/egress"
)

// IntegrationsKey decodes INTEGRATIONS_ENCRYPTION_KEY (base64, at least 32
// bytes); nil without an error when it is unset, which leaves the issue
// tracker integration unavailable.
func (c *Config) IntegrationsKey() ([]byte, error) {
	return decodeKey("INTEGRATIONS_ENCRYPTION_KEY", c.IntegrationsEncryptionKey)
}

// IssueTrackerAllowlist splits ISSUE_TRACKER_EGRESS_ALLOWLIST and rejects
// entries that are not a hostname, an IP or a CIDR range.
func (c *Config) IssueTrackerAllowlist() ([]string, error) {
	var entries []string
	for _, entry := range strings.Split(c.IssueTrackerEgressAllowlist, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			entries = append(entries, entry)
		}
	}
	if err := egress.ValidateAllowlist(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// ValidateIntegrations fails startup on a malformed integrations key or
// issue tracker allowlist. An unset key is valid.
func (c *Config) ValidateIntegrations() error {
	if _, err := c.IntegrationsKey(); err != nil {
		return err
	}
	_, err := c.IssueTrackerAllowlist()
	return err
}
