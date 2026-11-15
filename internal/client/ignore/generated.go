package ignore

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Generated is a folder whose new files look like build output or installed
// dependencies rather than project content.
type Generated struct {
	Dir     string `json:"dir"`
	Files   int    `json:"files"`
	Pattern string `json:"pattern"`
}

var generatedAnywhere = map[string]bool{
	"node_modules":     true,
	".next":            true,
	".nuxt":            true,
	".svelte-kit":      true,
	".turbo":           true,
	".parcel-cache":    true,
	"__pycache__":      true,
	".pytest_cache":    true,
	".gradle":          true,
	".godot":           true,
	".vs":              true,
	"DerivedDataCache": true,
}

var unityGenerated = map[string]bool{"Library": true, "Temp": true, "Logs": true, "obj": true, "UserSettings": true}

var unrealGenerated = map[string]bool{"Intermediate": true, "Saved": true, "Binaries": true}

// DetectGenerated groups paths under the outermost generated folder they sit
// in: dependency and cache folders anywhere, Unity's Library/Temp/Logs/obj/
// UserSettings next to ProjectSettings, and Unreal's Intermediate/Saved/
// Binaries next to a .uproject file. Results are sorted by file count.
func DetectGenerated(root string, paths []string) []Generated {
	byDir := map[string]*Generated{}
	engine := engineProbe{root: root, unity: map[string]bool{}, unreal: map[string]bool{}}
	for _, p := range paths {
		segments := strings.Split(strings.Trim(filepath.ToSlash(p), "/"), "/")
		for i, name := range segments[:len(segments)-1] {
			parent := strings.Join(segments[:i], "/")
			pattern := ""
			switch {
			case generatedAnywhere[name]:
				pattern = name + "/"
			case unityGenerated[name] && engine.isUnity(parent),
				unrealGenerated[name] && engine.isUnreal(parent):
				pattern = "/" + path.Join(parent, name) + "/"
			default:
				continue
			}
			dir := path.Join(parent, name)
			g := byDir[dir]
			if g == nil {
				g = &Generated{Dir: dir, Pattern: pattern}
				byDir[dir] = g
			}
			g.Files++
			break
		}
	}
	if len(byDir) == 0 {
		return nil
	}
	out := make([]Generated, 0, len(byDir))
	for _, g := range byDir {
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b Generated) int {
		if a.Files != b.Files {
			return b.Files - a.Files
		}
		return strings.Compare(a.Dir, b.Dir)
	})
	return out
}

type engineProbe struct {
	root          string
	unity, unreal map[string]bool
}

func (e engineProbe) isUnity(dir string) bool {
	known, ok := e.unity[dir]
	if !ok {
		info, err := os.Stat(filepath.Join(e.root, filepath.FromSlash(dir), "ProjectSettings"))
		known = err == nil && info.IsDir()
		e.unity[dir] = known
	}
	return known
}

func (e engineProbe) isUnreal(dir string) bool {
	known, ok := e.unreal[dir]
	if !ok {
		matches, _ := filepath.Glob(filepath.Join(e.root, filepath.FromSlash(dir), "*.uproject"))
		known = len(matches) > 0
		e.unreal[dir] = known
	}
	return known
}
