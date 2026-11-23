package config

import (
	"fmt"
	"strings"
)

// Chunk upload modes (CHUNK_UPLOAD_MODE).
const (
	// ChunkUploadModeDirect hands clients presigned URLs of a direct-transfer
	// store (S3/R2), so upload bytes bypass the server and confirm reads every
	// chunk back.
	ChunkUploadModeDirect = "direct"
	// ChunkUploadModeProxy sends upload bytes through the server, which hashes
	// them on the way in, writes the store itself and hands back a signed
	// receipt that lets confirm skip the read-back; clients send bundles of
	// many chunks per request. Own-storage (BYO) projects
	// always upload direct.
	ChunkUploadModeProxy = "proxy"
)

// CHUNK_PACKS values.
const (
	// ChunkPacksOff stores every bundle entry as its own (loose) object.
	ChunkPacksOff = "off"
	// ChunkPacksOn writes each bundle as one pack object (S3 store in proxy
	// mode only; nipad-ee validates it at startup).
	ChunkPacksOn = "on"
)

// DefaultChunkStoreWriteConcurrency is the server-wide number of loose chunk
// writes running at once when CHUNK_STORE_WRITE_CONCURRENCY is unset or <= 0.
const DefaultChunkStoreWriteConcurrency = 64

// DefaultChunkProxyBufferMiB is the server-wide memory budget for bundle
// upload bodies when CHUNK_PROXY_BUFFER_MIB is unset or <= 0: four bundles of
// the default maximum size fit at once.
const DefaultChunkProxyBufferMiB = 256

// DefaultChunkBundleMaxMiB is the largest bundle upload body a presign signs
// when CHUNK_BUNDLE_MAX_MIB is unset or <= 0.
const DefaultChunkBundleMaxMiB = 64

// ParseChunkUploadMode normalizes CHUNK_UPLOAD_MODE; empty is direct and any
// other unknown value is an error, so a typo never silently picks a mode.
func ParseChunkUploadMode(raw string) (string, error) {
	switch mode := strings.ToLower(strings.TrimSpace(raw)); mode {
	case "", ChunkUploadModeDirect:
		return ChunkUploadModeDirect, nil
	case ChunkUploadModeProxy:
		return ChunkUploadModeProxy, nil
	default:
		return "", fmt.Errorf("invalid CHUNK_UPLOAD_MODE %q: want %q or %q", raw, ChunkUploadModeDirect, ChunkUploadModeProxy)
	}
}

// ChunkProxyBufferBytes is the proxied upload body budget in bytes.
func (c *Config) ChunkProxyBufferBytes() int64 {
	mib := c.ChunkProxyBufferMiB
	if mib <= 0 {
		mib = DefaultChunkProxyBufferMiB
	}
	return int64(mib) << 20
}

// ChunkBundleMaxBytes is the largest bundle upload body in bytes.
func (c *Config) ChunkBundleMaxBytes() int64 {
	mib := c.ChunkBundleMaxMiB
	if mib <= 0 {
		mib = DefaultChunkBundleMaxMiB
	}
	return int64(mib) << 20
}

// ParseChunkPacks normalizes CHUNK_PACKS; empty is off and any other unknown
// value is an error.
func ParseChunkPacks(raw string) (string, error) {
	switch mode := strings.ToLower(strings.TrimSpace(raw)); mode {
	case "", ChunkPacksOff, "false", "0":
		return ChunkPacksOff, nil
	case ChunkPacksOn, "true", "1":
		return ChunkPacksOn, nil
	default:
		return "", fmt.Errorf("invalid CHUNK_PACKS %q: want %q or %q", raw, ChunkPacksOn, ChunkPacksOff)
	}
}

// ChunkPacksEnabled reports whether CHUNK_PACKS is on.
func (c *Config) ChunkPacksEnabled() bool {
	packs, err := ParseChunkPacks(c.ChunkPacks)
	return err == nil && packs == ChunkPacksOn
}

// ValidateChunkPacks checks that packs can be written: they need proxied
// uploads (the server writes every bundle itself) and a server store that
// holds packs (storesPacks).
func (c *Config) ValidateChunkPacks(storesPacks bool) error {
	if !c.ChunkPacksEnabled() {
		return nil
	}
	if c.ChunkUploadMode != ChunkUploadModeProxy {
		return fmt.Errorf("CHUNK_PACKS=on requires CHUNK_UPLOAD_MODE=%s (got %q)", ChunkUploadModeProxy, c.ChunkUploadMode)
	}
	if !storesPacks {
		return fmt.Errorf("CHUNK_PACKS=on requires a chunk store that holds packs (CHUNK_STORAGE=s3)")
	}
	return nil
}
