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
	// receipt that lets confirm skip the read-back. Own-storage (BYO) projects
	// always upload direct.
	ChunkUploadModeProxy = "proxy"
)

// DefaultChunkProxyBufferMiB is the server-wide memory budget for proxied
// upload bodies when CHUNK_PROXY_BUFFER_MIB is unset or <= 0.
const DefaultChunkProxyBufferMiB = 128

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
