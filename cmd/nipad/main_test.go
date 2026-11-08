package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
)

func TestGuardSecrets(t *testing.T) {
	require.Error(t, guardSecrets(&config.Config{}))
	require.Error(t, guardSecrets(&config.Config{JWTKey: "secret", ChunkURLSigningKey: "change-me"}))
	require.NoError(t, guardSecrets(&config.Config{JWTKey: "secret", ChunkURLSigningKey: "change-me", AllowInsecureDevSecrets: true}))
	require.NoError(t, guardSecrets(&config.Config{
		JWTKey:             "0123456789abcdef0123456789abcdef",
		ChunkURLSigningKey: "fedcba9876543210fedcba9876543210",
	}))
}
