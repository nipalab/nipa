package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultHTTPReadHeaderTimeoutSeconds = 30
	DefaultHTTPIdleTimeoutSeconds       = 300
	DefaultMaintenancePollSeconds       = 5
)

// MaintenancePollInterval is how often the stored maintenance windows and the
// MAINTENANCE_FILE override are reloaded. Values below one second become one.
func (c *Config) MaintenancePollInterval() time.Duration {
	if c.MaintenancePollSeconds < 1 {
		return time.Second
	}
	return time.Duration(c.MaintenancePollSeconds) * time.Second
}

// MaintenanceFilePath is the override file path, empty when unset.
func (c *Config) MaintenanceFilePath() string {
	return strings.TrimSpace(c.MaintenanceFile)
}

// HTTPReadHeaderTimeout bounds how long a client may take to send the request
// headers. Zero or negative keeps the default.
func (c *Config) HTTPReadHeaderTimeout() time.Duration {
	return secondsOr(c.HTTPReadHeaderTimeoutSeconds, DefaultHTTPReadHeaderTimeoutSeconds)
}

// HTTPIdleTimeout closes keep-alive HTTP/1.1 connections and HTTP/2 (h2c)
// connections without an active stream after this long. Zero or negative keeps
// the default.
func (c *Config) HTTPIdleTimeout() time.Duration {
	return secondsOr(c.HTTPIdleTimeoutSeconds, DefaultHTTPIdleTimeoutSeconds)
}

func secondsOr(v, def int) time.Duration {
	if v <= 0 {
		v = def
	}
	return time.Duration(v) * time.Second
}

// MetricsListenAddress resolves METRICS_ADDRESS: empty keeps /metrics and
// /debug/pprof on the main port, a bare port (`9090` or `:9090`) binds
// loopback, anything else is used as host:port.
func (c *Config) MetricsListenAddress() (string, error) {
	raw := strings.TrimSpace(c.MetricsAddress)
	if raw == "" {
		return "", nil
	}
	if _, err := strconv.Atoi(raw); err == nil {
		raw = ":" + raw
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("invalid METRICS_ADDRESS %q: %w", c.MetricsAddress, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return "", fmt.Errorf("invalid METRICS_ADDRESS %q: bad port", c.MetricsAddress)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

// CORSAllowedOrigins is the browser origin of PUBLIC_BASE_URL (a bare host
// means https). Empty means no cross-origin access.
func (c *Config) CORSAllowedOrigins() ([]string, error) {
	origin, err := webOrigin(c.PublicBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid PUBLIC_BASE_URL %q: %w", c.PublicBaseURL, err)
	}
	if origin == "" {
		return nil, nil
	}
	return []string{origin}, nil
}

// BillingPageURL is the absolute address of the SPA billing settings, from
// PUBLIC_BASE_URL or else EMAIL_BASE_URL; empty when neither is valid.
func (c *Config) BillingPageURL() string {
	for _, raw := range []string{c.PublicBaseURL, c.EmailBaseURL} {
		if origin, err := webOrigin(raw); err == nil && origin != "" {
			return origin + "/settings/billing"
		}
	}
	return ""
}

func webOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if (scheme != "http" && scheme != "https") || host == "" || u.User != nil {
		return "", fmt.Errorf("not an http(s) origin")
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	defaultPort := map[string]string{"http": "80", "https": "443"}[scheme]
	if port := u.Port(); port != "" && port != defaultPort {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}
