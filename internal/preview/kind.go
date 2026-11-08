// Package preview classifies and decodes asset files into normalized data
// (RGBA images, stats) that viewers and diff tools can compare. It is pure and
// shared by the client daemon and, later, the server.
package preview

import (
	"path/filepath"
	"strings"
)

type Kind string

const (
	KindText   Kind = "text"
	KindImage  Kind = "image"
	KindModel  Kind = "model"
	KindAudio  Kind = "audio"
	KindVideo  Kind = "video"
	KindFont   Kind = "font"
	KindTable  Kind = "table"
	KindScene  Kind = "scene"
	KindOpaque Kind = "opaque"
)

var kindByExtension = map[string]Kind{
	".png": KindImage, ".jpg": KindImage, ".jpeg": KindImage, ".gif": KindImage,
	".webp": KindImage, ".bmp": KindImage, ".tga": KindImage, ".tif": KindImage,
	".tiff": KindImage, ".dds": KindImage, ".ktx": KindImage, ".ktx2": KindImage,
	".exr": KindImage, ".hdr": KindImage, ".psd": KindImage, ".psb": KindImage,

	".fbx": KindModel, ".glb": KindModel, ".gltf": KindModel, ".obj": KindModel,
	".usd": KindModel, ".usda": KindModel, ".usdc": KindModel, ".usdz": KindModel,

	".wav": KindAudio, ".ogg": KindAudio, ".mp3": KindAudio, ".flac": KindAudio,
	".aif": KindAudio, ".aiff": KindAudio,

	".mp4": KindVideo, ".mov": KindVideo, ".webm": KindVideo, ".mkv": KindVideo,
	".avi": KindVideo,

	".ttf": KindFont, ".otf": KindFont, ".woff": KindFont, ".woff2": KindFont,

	".csv": KindTable, ".tsv": KindTable,

	".unity": KindScene, ".prefab": KindScene, ".asset": KindScene, ".mat": KindScene,
	".anim": KindScene, ".controller": KindScene, ".overridecontroller": KindScene,
	".physicmaterial": KindScene,
}

// Classify picks the viewer kind for a path. Unknown extensions fall back to
// text or opaque depending on the stored binary flag, and so do
// binary-serialized scenes.
func Classify(path string, isBinary bool) Kind {
	if kind, ok := kindByExtension[strings.ToLower(filepath.Ext(path))]; ok && (kind != KindScene || !isBinary) {
		return kind
	}
	if isBinary {
		return KindOpaque
	}
	return KindText
}
