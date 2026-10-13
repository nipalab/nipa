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
