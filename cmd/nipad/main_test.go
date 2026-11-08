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

func TestGuardSecretsDevGeneratesEphemeralKeys(t *testing.T) {
	cfg := &config.Config{AllowInsecureDevSecrets: true}
	require.NoError(t, guardSecrets(cfg))
	require.GreaterOrEqual(t, len(cfg.JWTKey), 32)
	require.GreaterOrEqual(t, len(cfg.ChunkURLSigningKey), 32)
	require.NotEqual(t, cfg.JWTKey, cfg.ChunkURLSigningKey)

	cfg = &config.Config{AllowInsecureDevSecrets: true, JWTKey: "secret"}
	require.NoError(t, guardSecrets(cfg))
	require.Equal(t, "secret", cfg.JWTKey)
}
