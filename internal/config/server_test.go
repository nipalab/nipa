package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig_ServerHardeningDefaults(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\n")
	t.Chdir(dir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, DefaultHTTPReadHeaderTimeoutSeconds, cfg.HTTPReadHeaderTimeoutSeconds)
	require.Equal(t, DefaultHTTPIdleTimeoutSeconds, cfg.HTTPIdleTimeoutSeconds)
	require.Equal(t, 30*time.Second, cfg.HTTPReadHeaderTimeout())
	require.Equal(t, 5*time.Minute, cfg.HTTPIdleTimeout())
	require.Empty(t, cfg.MetricsAddress)
	addr, err := cfg.MetricsListenAddress()
	require.NoError(t, err)
	require.Empty(t, addr)
}

func TestLoadConfig_ServerHardeningFromYAMLAndEnv(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\nHTTP_READ_HEADER_TIMEOUT_SECONDS: 5\nHTTP_IDLE_TIMEOUT_SECONDS: 90\n")
	t.Chdir(dir)
	t.Setenv("METRICS_ADDRESS", "9101")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, cfg.HTTPReadHeaderTimeout())
	require.Equal(t, 90*time.Second, cfg.HTTPIdleTimeout())
	addr, err := cfg.MetricsListenAddress()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9101", addr, "per-color env files set the metrics port without a config.yaml key")
}

func TestHTTPTimeoutsFallBackToDefaults(t *testing.T) {
	cfg := &Config{HTTPReadHeaderTimeoutSeconds: 0, HTTPIdleTimeoutSeconds: -1}
	require.Equal(t, 30*time.Second, cfg.HTTPReadHeaderTimeout())
	require.Equal(t, 300*time.Second, cfg.HTTPIdleTimeout())
}

func TestMetricsListenAddress(t *testing.T) {
	cases := []struct{ in, want, err string }{
		{"", "", ""},
		{"  ", "", ""},
		{"9090", "127.0.0.1:9090", ""},
		{":9090", "127.0.0.1:9090", ""},
		{"0.0.0.0:9090", "0.0.0.0:9090", ""},
		{"10.0.0.5:9100", "10.0.0.5:9100", ""},
		{"[::1]:9090", "[::1]:9090", ""},
		{"localhost:9090", "localhost:9090", ""},
		{"metrics", "", "invalid METRICS_ADDRESS"},
		{"host:port", "", "bad port"},
		{"host:70000", "", "bad port"},
	}
	for _, c := range cases {
		got, err := (&Config{MetricsAddress: c.in}).MetricsListenAddress()
		if c.err != "" {
			require.ErrorContains(t, err, c.err, c.in)
			continue
		}
		require.NoError(t, err, c.in)
		require.Equal(t, c.want, got, c.in)
	}
}

func TestCORSAllowedOrigins(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  bool
	}{
		{"", nil, false},
		{"https://nipalab.com", []string{"https://nipalab.com"}, false},
		{"https://NipaLab.com/some/path/", []string{"https://nipalab.com"}, false},
		{"https://nipalab.com:443", []string{"https://nipalab.com"}, false},
		{"http://nipa.local:80", []string{"http://nipa.local"}, false},
		{"http://localhost:6745", []string{"http://localhost:6745"}, false},
		{"https://nipa.example.com:8443", []string{"https://nipa.example.com:8443"}, false},
		{"nipa.example.com", []string{"https://nipa.example.com"}, false},
		{"https://[::1]:8443", []string{"https://[::1]:8443"}, false},
		{"ftp://nipa.example.com", nil, true},
		{"https://user@nipa.example.com", nil, true},
		{"https://", nil, true},
		{"https://%zz", nil, true},
	}
	for _, c := range cases {
		got, err := (&Config{PublicBaseURL: c.in}).CORSAllowedOrigins()
		if c.err {
			require.Error(t, err, c.in)
			continue
		}
		require.NoError(t, err, c.in)
		require.Equal(t, c.want, got, c.in)
	}
}
