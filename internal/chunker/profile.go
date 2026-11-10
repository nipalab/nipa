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
	".scn": {}, ".res": {},
	".uasset": {}, ".umap": {},
	".c4d": {}, ".hip": {}, ".hiplc": {}, ".hipnc": {},
	".spp": {}, ".xcf": {}, ".clip": {}, ".aep": {},
	".upk": {}, ".udk": {}, ".flax": {},
	".rbxl": {}, ".rbxm": {},
	".rxdata": {}, ".rvdata": {}, ".rvdata2": {},
	".lxo": {}, ".3b": {}, ".tbscene": {},
	".afphoto": {}, ".afdesign": {}, ".aseprite": {}, ".ase": {},
	".spine": {}, ".cmo3": {}, ".can3": {},
}

// binaryOnlyProfiles cover extensions that are often text, so they stay out
// of the lockable lists, but get a specific profile when the content is binary.
var binaryOnlyProfiles = map[string]Config{
	".unity": DefaultConfig, ".prefab": DefaultConfig, ".asset": DefaultConfig,
	".mat": DefaultConfig, ".anim": DefaultConfig, ".controller": DefaultConfig,
	".overridecontroller": DefaultConfig, ".lighting": DefaultConfig,
	".terrainlayer": DefaultConfig, ".cubemap": DefaultConfig,
	".mixer": DefaultConfig, ".playable": DefaultConfig,
	".ply": PackedAssetConfig, ".stl": PackedAssetConfig,
}

// packedAssetExtensions are formats that are written once or already
// compressed internally, so request count matters more than delta size.
var packedAssetExtensions = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {},
	".tga": {}, ".bmp": {}, ".tif": {}, ".tiff": {},
	".dds": {}, ".ktx": {}, ".ktx2": {}, ".exr": {}, ".hdr": {},
	".avif": {}, ".heic": {}, ".jxl": {}, ".astc": {}, ".basis": {},
	".wav": {}, ".ogg": {}, ".mp3": {}, ".flac": {}, ".aif": {}, ".aiff": {}, ".aifc": {}, ".wem": {}, ".bnk": {},
	".m4a": {}, ".aac": {}, ".opus": {}, ".wma": {}, ".fsb": {}, ".bank": {},
	".mp4": {}, ".mov": {}, ".avi": {}, ".webm": {}, ".mkv": {},
	".m4v": {}, ".mpg": {}, ".bik": {}, ".bk2": {},
	".fbx": {}, ".glb": {}, ".usd": {}, ".usdc": {}, ".usdz": {},
	".abc": {}, ".vdb": {}, ".3ds": {}, ".sbsar": {},
	".pvr": {}, ".crn": {}, ".tx": {}, ".vtf": {}, ".ctex": {}, ".stex": {},
	".usm": {}, ".ogv": {}, ".wmv": {}, ".xma": {}, ".at9": {}, ".caf": {},
	".bgeo": {}, ".cgf": {}, ".chr": {}, ".mdl": {}, ".bsp": {},
	".skel": {}, ".moc3": {}, ".xnb": {},
	".ttf": {}, ".otf": {}, ".ttc": {}, ".woff": {}, ".woff2": {},
	".pak": {}, ".pck": {}, ".uexp": {}, ".ubulk": {}, ".ucas": {}, ".utoc": {},
	".vpk": {}, ".pk3": {}, ".wad": {}, ".rgss3a": {}, ".unity3d": {},
	".zip": {}, ".7z": {}, ".rar": {}, ".gz": {}, ".unitypackage": {},
	".tar": {}, ".xz": {}, ".bz2": {}, ".zst": {}, ".lz4": {},
	".apk": {}, ".aab": {}, ".ipa": {}, ".xapk": {}, ".appx": {}, ".msix": {},
	".ztl": {}, ".procreate": {}, ".als": {}, ".flp": {},
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
	if cfg, ok := binaryOnlyProfiles[ext]; ok {
		return cfg
	}
	return DefaultBinaryConfig
}

// IsLockablePath reports whether a path looks like a binary asset that needs
// an exclusive lock before it can change. It matches by extension only; files
// already tracked in a manifest should use their stored is_binary flag instead.
func IsLockablePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := editableBinaryExtensions[ext]; ok {
		return true
	}
	_, ok := packedAssetExtensions[ext]
	return ok
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
