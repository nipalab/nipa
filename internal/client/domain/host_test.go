package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseHost(t *testing.T) {
	cases := map[string]string{
		"nipa.example.com":                     "nipa.example.com",
		"http://nipa.example.com":              "nipa.example.com",
		"https://nipa.example.com":             "nipa.example.com",
		"nipa.example.com:8080":                "nipa.example.com:8080",
		"http://nipa.example.com/org/project":  "nipa.example.com",
		"https://nipa.example.com/org/project": "nipa.example.com",
	}
	for raw, want := range cases {
		got, err := ParseHost(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got, raw)
	}
}

func TestParseHost_Empty(t *testing.T) {
	_, err := ParseHost("  ")
	require.ErrorContains(t, err, "host cannot be empty")
}

func TestParseHost_Invalid(t *testing.T) {
	_, err := ParseHost("http://")
	require.ErrorContains(t, err, "invalid host")
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		address string
		host    string
	}{
		{name: "bare host", raw: "nipa.example.com", address: "nipa.example.com", host: "nipa.example.com"},
		{name: "bare host with port", raw: "nipa.example.com:6745", address: "nipa.example.com:6745", host: "nipa.example.com:6745"},
		{name: "http url", raw: "http://nipa.example.com/org/project", address: "http://nipa.example.com", host: "nipa.example.com"},
		{name: "https url", raw: "https://nipa.example.com/org/project", address: "https://nipa.example.com", host: "nipa.example.com"},
		{name: "https with port", raw: "https://nipa.example.com:8443", address: "https://nipa.example.com:8443", host: "nipa.example.com:8443"},
		{name: "uppercase scheme", raw: "HTTPS://nipa.example.com/org/project", address: "https://nipa.example.com", host: "nipa.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEndpoint(tt.raw)
			require.NoError(t, err)
			require.Equal(t, tt.address, got.Address)
			require.Equal(t, tt.host, got.Host)
		})
	}
}

func TestParseEndpoint_Errors(t *testing.T) {
	_, err := ParseEndpoint("  ")
	require.ErrorContains(t, err, "host cannot be empty")

	_, err = ParseEndpoint("ftp://nipa.example.com/org/project")
	require.ErrorContains(t, err, "invalid URL scheme")
}

func TestHostForAddress(t *testing.T) {
	require.Equal(t, "nipa.example.com", HostForAddress("https://nipa.example.com/org/project"))
	require.Equal(t, "nipa.example.com:6745", HostForAddress("http://nipa.example.com:6745"))
	require.Equal(t, "nipa.example.com", HostForAddress("nipa.example.com"))
	require.Empty(t, HostForAddress("  "))
}
