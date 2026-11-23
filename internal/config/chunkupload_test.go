package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseChunkUploadMode(t *testing.T) {
	for raw, want := range map[string]string{
		"":         ChunkUploadModeDirect,
		"direct":   ChunkUploadModeDirect,
		" Proxy ":  ChunkUploadModeProxy,
		"PROXY":    ChunkUploadModeProxy,
		"\tdirect": ChunkUploadModeDirect,
	} {
		got, err := ParseChunkUploadMode(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got, raw)
	}
	_, err := ParseChunkUploadMode("proxied")
	require.ErrorContains(t, err, "invalid CHUNK_UPLOAD_MODE")
}

func TestConfig_ChunkProxyBufferBytes(t *testing.T) {
	for mib, want := range map[int]int64{0: 256 << 20, -1: 256 << 20, 1: 1 << 20, 128: 128 << 20} {
		cfg := Config{ChunkProxyBufferMiB: mib}
		require.Equal(t, want, cfg.ChunkProxyBufferBytes(), mib)
	}
}

func TestConfig_ChunkBundleMaxBytes(t *testing.T) {
	for mib, want := range map[int]int64{0: 64 << 20, -3: 64 << 20, 16: 16 << 20, 128: 128 << 20} {
		cfg := Config{ChunkBundleMaxMiB: mib}
		require.Equal(t, want, cfg.ChunkBundleMaxBytes(), mib)
	}
}

func TestLoadConfig_ChunkUploadMode(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\n")
	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, ChunkUploadModeDirect, cfg.ChunkUploadMode)
	require.Equal(t, DefaultChunkProxyBufferMiB, cfg.ChunkProxyBufferMiB)
	require.Equal(t, DefaultChunkBundleMaxMiB, cfg.ChunkBundleMaxMiB)

	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\nCHUNK_UPLOAD_MODE: Proxy\nCHUNK_PROXY_BUFFER_MIB: 32\nCHUNK_BUNDLE_MAX_MIB: 24\n")
	cfg, err = LoadConfig()
	require.NoError(t, err)
	require.Equal(t, ChunkUploadModeProxy, cfg.ChunkUploadMode)
	require.Equal(t, 32, cfg.ChunkProxyBufferMiB)
	require.Equal(t, 24, cfg.ChunkBundleMaxMiB)

	t.Setenv("CHUNK_UPLOAD_MODE", "sideways")
	_, err = LoadConfig()
	require.ErrorContains(t, err, `invalid CHUNK_UPLOAD_MODE "sideways"`)
}
