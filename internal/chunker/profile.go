package chunker

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
)

// DefaultBinaryConfig chunks binary files without a more specific profile.
var DefaultBinaryConfig = Config{Min: 256 << 10, Avg: 1 << 20, Max: 4 << 20}

// PackedAssetConfig chunks write-once or already-compressed assets, where
// fewer, larger chunks beat fine-grained re-upload.
var PackedAssetConfig = Config{Min: 1 << 20, Avg: 4 << 20, Max: 16 << 20}

// editableBinaryExtensions are formats that tools re-save in place, so small
// chunks keep the re-uploaded delta tiny.
var editableBinaryExtensions = map[string]struct{}{
	".blend": {}, ".blend1": {},
	".psd": {}, ".psb": {},
	".mb": {}, ".max": {}, ".kra": {},
	".sqlite": {}, ".sqlite3": {}, ".db": {},
}

// packedAssetExtensions are formats that are written once or already
// compressed internally, so request count matters more than delta size.
var packedAssetExtensions = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {},
	".tga": {}, ".bmp": {}, ".tif": {}, ".tiff": {},
	".dds": {}, ".ktx": {}, ".ktx2": {}, ".exr": {}, ".hdr": {},
	".wav": {}, ".ogg": {}, ".mp3": {}, ".flac": {}, ".aiff": {}, ".wem": {}, ".bnk": {},
	".mp4": {}, ".mov": {}, ".avi": {}, ".webm": {}, ".mkv": {},
	".fbx": {}, ".glb": {}, ".usd": {}, ".usdc": {}, ".usdz": {},
	".pak": {}, ".zip": {}, ".7z": {}, ".rar": {}, ".gz": {}, ".unitypackage": {},
}

// ConfigForFile returns the chunking configuration for a file. Text content
// keeps the default configuration; binary files are matched by extension.
func ConfigForFile(path string, isBinary bool) Config {
	if !isBinary {
		return DefaultConfig
	}
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := editableBinaryExtensions[ext]; ok {
		return DefaultConfig
	}
	if _, ok := packedAssetExtensions[ext]; ok {
		return PackedAssetConfig
	}
	return DefaultBinaryConfig
}

// MaxChunkSize is the largest chunk size any profile can produce.
func MaxChunkSize() int64 {
	max := DefaultConfig.Max
	for _, cfg := range []Config{DefaultBinaryConfig, PackedAssetConfig} {
		if cfg.Max > max {
			max = cfg.Max
		}
	}
	return max
}

// ProbeBinary reports whether the reader's leading bytes look binary and
// restores the read position so the caller can consume the content afterwards.
func ProbeBinary(r io.ReadSeeker) (bool, error) {
	probe := make([]byte, binaryProbeSize)
	n, err := io.ReadFull(r, probe)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	return IsBinary(probe[:n]), nil
}
