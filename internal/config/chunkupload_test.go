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

func TestParseChunkPacks(t *testing.T) {
	for raw, want := range map[string]string{
		"": ChunkPacksOff, "off": ChunkPacksOff, " OFF ": ChunkPacksOff, "false": ChunkPacksOff, "0": ChunkPacksOff,
		"on": ChunkPacksOn, "On": ChunkPacksOn, "true": ChunkPacksOn, "1": ChunkPacksOn,
	} {
		got, err := ParseChunkPacks(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got, raw)
	}
	_, err := ParseChunkPacks("maybe")
	require.ErrorContains(t, err, "invalid CHUNK_PACKS")
}

func TestConfig_ValidateChunkPacks(t *testing.T) {
	off := Config{ChunkPacks: ChunkPacksOff}
	require.False(t, off.ChunkPacksEnabled())
	require.NoError(t, off.ValidateChunkPacks(false))

	direct := Config{ChunkPacks: ChunkPacksOn, ChunkUploadMode: ChunkUploadModeDirect}
	require.True(t, direct.ChunkPacksEnabled())
	require.ErrorContains(t, direct.ValidateChunkPacks(true), "CHUNK_UPLOAD_MODE=proxy")

	proxy := Config{ChunkPacks: ChunkPacksOn, ChunkUploadMode: ChunkUploadModeProxy}
	require.ErrorContains(t, proxy.ValidateChunkPacks(false), "holds packs")
	require.NoError(t, proxy.ValidateChunkPacks(true))

	bad := Config{ChunkPacks: "sometimes"}
	require.False(t, bad.ChunkPacksEnabled())
}

func TestLoadConfig_ChunkPacks(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\n")
	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, ChunkPacksOff, cfg.ChunkPacks)
	require.Equal(t, DefaultChunkStoreWriteConcurrency, cfg.ChunkStoreWriteConcurrency)

	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\nCHUNK_PACKS: \"ON\"\nCHUNK_STORE_WRITE_CONCURRENCY: 8\n")
	cfg, err = LoadConfig()
	require.NoError(t, err)
	require.Equal(t, ChunkPacksOn, cfg.ChunkPacks)
	require.Equal(t, 8, cfg.ChunkStoreWriteConcurrency)

	t.Setenv("CHUNK_PACKS", "sometimes")
	_, err = LoadConfig()
	require.ErrorContains(t, err, `invalid CHUNK_PACKS "sometimes"`)
}
