package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
)

func TestNewTermsSource(t *testing.T) {
	fixed := newTermsSource(&config.Config{SaasTermsVersion: "2026-11-10"})
	require.Empty(t, fixed.URL())
	require.Equal(t, "2026-11-10", fixed.Version())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"2027-01-01"}`))
	}))
	defer server.Close()
	live := newTermsSource(&config.Config{SaasTermsURL: server.URL, SaasTermsVersion: "2026-11-10"})
	require.Equal(t, "2027-01-01", live.Version())

	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	fallback := newTermsSource(&config.Config{SaasTermsURL: down.URL, SaasTermsVersion: "2026-11-10"})
	require.Equal(t, "2026-11-10", fallback.Version())
}
