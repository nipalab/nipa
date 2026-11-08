package assetdiff

import (
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nipalab/nipa/internal/preview"
	"github.com/nipalab/nipa/internal/unitymeta"
)

const maxMetaBytes = 4 << 10

type SceneResult struct {
	Engine    string        `json:"engine"`
	Added     int           `json:"added"`
	Removed   int           `json:"removed"`
	Changed   int           `json:"changed"`
	Props     int           `json:"props_changed"`
	Changes   []SceneChange `json:"changes"`
	Truncated bool          `json:"truncated,omitempty"`
	Refs      []RefChange   `json:"refs"`
}

type SceneChange struct {
	Status       string       `json:"status"`
	ID           string       `json:"id"`
	Path         string       `json:"path"`
	Type         string       `json:"type"`
	Props        []PropChange `json:"props,omitempty"`
	PropsOmitted int          `json:"props_omitted,omitempty"`
}

type PropChange struct {
	Key string `json:"key"`
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

type RefChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func compareScene(sides [2]side, opts Options) (*SceneResult, error) {
	var scenes [2]*preview.Scene
	for i, s := range sides {
		if !s.present {
			continue
		}
		sc, err := parseScene(s.ref.Path, s.data, opts)
		if err != nil {
			return nil, err
		}
		scenes[i] = sc
	}
	for i, s := range sides {
		if scenes[i] == nil && s.ref != nil {
			scenes[i] = &preview.Scene{Engine: SceneEngine(s.ref.Path)}
		}
	}
	d := preview.DiffScenes(scenes[0], scenes[1])
	out := &SceneResult{Engine: d.Engine, Added: d.Added, Removed: d.Removed, Changed: d.Changed, Changes: []SceneChange{}, Refs: []RefChange{}}
	for i, c := range d.Changes {
		out.Props += len(c.Props)
		if i >= opts.MaxChanges {
			out.Truncated = true
			continue
		}
		sc := SceneChange{Status: c.Status, ID: c.ID, Path: c.Path, Type: c.Type}
		for j, p := range c.Props {
			if j >= opts.MaxProps {
				sc.PropsOmitted = len(c.Props) - j
				break
			}
			sc.Props = append(sc.Props, PropChange{Key: p.Key, Old: p.Old, New: p.New})
		}
		out.Changes = append(out.Changes, sc)
	}
	for _, r := range d.Refs {
		out.Refs = append(out.Refs, RefChange{Path: r.Path, Status: r.Status})
	}
	return out, nil
}

func parseScene(p string, data []byte, opts Options) (*preview.Scene, error) {
	if SceneEngine(p) == "godot" {
		return preview.ParseGodotResource(data)
	}
	var resolve func(string) string
	if opts.GUIDs != nil {
		resolve = opts.GUIDs(p)
	}
	return preview.ParseUnityYAML(data, resolve)
}

var godotSceneExts = map[string]bool{".tscn": true, ".tres": true, ".godot": true, ".import": true}

// SceneEngine reports which engine a scene or asset path belongs to.
func SceneEngine(p string) string {
	if godotSceneExts[strings.ToLower(path.Ext(p))] {
		return "godot"
	}
	return "unity"
}

// UnityProject returns the clone-relative folder holding the Unity project a
// path belongs to: whatever precedes its Assets or Packages folder.
func UnityProject(p string) string {
	parts := strings.Split(path.Clean(filepath.ToSlash(p)), "/")
	for i, part := range parts {
		if part == "Assets" || part == "Packages" {
			return strings.Join(parts[:i], "/")
		}
	}
	return ""
}

// BuildGUIDIndex maps the GUIDs of the .meta files under dir's Assets and
// Packages folders to their asset paths, relative to dir.
func BuildGUIDIndex(dir string) map[string]string {
	index := map[string]string{}
	for _, top := range []string{"Assets", "Packages"} {
		_ = filepath.WalkDir(filepath.Join(dir, top), func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(p, ".meta") {
				return nil
			}
			guid := ReadGUID(p)
			if guid == "" {
				return nil
			}
			if rel, err := filepath.Rel(dir, strings.TrimSuffix(p, ".meta")); err == nil {
				index[guid] = filepath.ToSlash(rel)
			}
			return nil
		})
	}
	return index
}

// ReadGUID returns the GUID recorded in the .meta file at p, or "".
func ReadGUID(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, _ := io.ReadAll(io.LimitReader(f, maxMetaBytes))
	return unitymeta.GUID(data)
}

// WorkingGUIDs resolves Unity GUIDs through the .meta files of the clone at
// root, building one index per Unity project on first use. It suits
// Options.GUIDs for short-lived callers; long-lived ones cache and
// invalidate their own indexes.
func WorkingGUIDs(root string) func(path string) func(guid string) string {
	var mu sync.Mutex
	indexes := map[string]map[string]string{}
	return func(p string) func(string) string {
		project := UnityProject(p)
		mu.Lock()
		index, ok := indexes[project]
		if !ok {
			index = BuildGUIDIndex(filepath.Join(root, filepath.FromSlash(project)))
			indexes[project] = index
		}
		mu.Unlock()
		return func(guid string) string { return index[guid] }
	}
}
