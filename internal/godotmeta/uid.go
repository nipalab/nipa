package godotmeta

import (
	"path"
	"strings"
)

const (
	importExt = ".import"
	uidExt    = ".uid"
	uidScheme = "uid://"
)

var importable = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "webp": true, "svg": true, "bmp": true, "tga": true,
	"exr": true, "hdr": true, "dds": true, "ktx": true, "ktx2": true,
	"wav": true, "ogg": true, "mp3": true,
	"glb": true, "gltf": true, "fbx": true, "blend": true, "obj": true, "dae": true,
	"ttf": true, "otf": true, "woff": true, "woff2": true, "fnt": true, "font": true,
	"csv": true,
}

var uidFile = map[string]bool{"gd": true, "cs": true, "gdshader": true, "gdshaderinc": true}

func normExt(ext string) string {
	return strings.ToLower(strings.TrimPrefix(ext, "."))
}

// Importable reports whether Godot imports files with this extension and
// keeps their UID in a .import sidecar.
func Importable(ext string) bool { return importable[normExt(ext)] }

// HasUIDFile reports whether Godot 4.4+ keeps the UID of files with this
// extension in a .uid sidecar.
func HasUIDFile(ext string) bool { return uidFile[normExt(ext)] }

// Sidecars returns both possible sidecars of an asset.
func Sidecars(p string) []string { return []string{p + importExt, p + uidExt} }

// Sidecar returns the sidecar Godot writes for an asset, if any.
func Sidecar(p string) (string, bool) {
	ext := path.Ext(p)
	switch {
	case Importable(ext):
		return p + importExt, true
	case HasUIDFile(ext):
		return p + uidExt, true
	}
	return "", false
}

// IsSidecar reports whether p is a .import or .uid sidecar of another file.
func IsSidecar(p string) bool {
	base := path.Base(p)
	for _, ext := range []string{importExt, uidExt} {
		if strings.HasSuffix(base, ext) && len(base) > len(ext) {
			return true
		}
	}
	return false
}

// AssetOf returns the asset a .import or .uid sidecar belongs to.
func AssetOf(sidecar string) string {
	if s, ok := strings.CutSuffix(sidecar, importExt); ok {
		return s
	}
	return strings.TrimSuffix(sidecar, uidExt)
}

// UID returns the uid:// identifier recorded in a .import file's [remap]
// section or in the single line of a .uid file, or "".
func UID(content []byte) string {
	text := strings.TrimPrefix(string(content), "\ufeff")
	if trimmed := strings.TrimSpace(text); !strings.ContainsAny(trimmed, "\r\n") {
		return validUID(trimmed)
	}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if section != "remap" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "uid" {
			return validUID(strings.Trim(strings.TrimSpace(value), `"`))
		}
	}
	return ""
}

func validUID(s string) string {
	id, ok := strings.CutPrefix(s, uidScheme)
	if !ok || id == "" {
		return ""
	}
	if strings.ContainsFunc(id, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	}) {
		return ""
	}
	return s
}
