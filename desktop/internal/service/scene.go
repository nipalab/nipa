package service

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/preview"
	"github.com/nipalab/nipa/internal/unitymeta"
)

const (
	maxSceneBytes = 16 << 20
	maxMetaBytes  = 4 << 10
)

type SceneObject struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Type   string `json:"type"`
	Parent string `json:"parent"`
}

type PropChange struct {
	Key string `json:"key"`
	Old string `json:"old"`
	New string `json:"new"`
}

type SceneChange struct {
	Status string       `json:"status"` // added, removed, changed, moved
	ID     string       `json:"id"`
	Path   string       `json:"path"`
	Type   string       `json:"type"`
	Props  []PropChange `json:"props"`
}

type RefChange struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added, removed
}

// SceneCompare is a structural diff of an engine scene or asset: Objects
// holds the changed objects and their ancestors for the tree. Error is set,
// and the lists are empty, when a side cannot be parsed.
type SceneCompare struct {
	Engine  string        `json:"engine"`
	Objects []SceneObject `json:"objects"`
	Changes []SceneChange `json:"changes"`
	Added   int           `json:"added"`
	Removed int           `json:"removed"`
	Changed int           `json:"changed"`
	Refs    []RefChange   `json:"refs"`
	Error   string        `json:"error,omitempty"`
}

type guidKey struct{ root, project string }

// guidIndex caches GUID → path maps; gen counts invalidations per root so a
// build that raced with one is not stored.
type guidIndex struct {
	mu    sync.Mutex
	cache map[guidKey]map[string]string
	gen   map[string]uint64
	build func(dir string) map[string]string
}

// SceneDiff compares two versions of a Unity YAML scene, prefab or asset
// (resolving asset GUIDs through the working copy's .meta files) or of a
// Godot text resource, object by object.
func (s *Service) SceneDiff(ctx context.Context, root string, old, new *ImageRef) (*SceneCompare, error) {
	if old == nil && new == nil {
		return nil, errors.New("nothing to compare")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	var scenes [2]*preview.Scene
	for i, ref := range []*ImageRef{old, new} {
		if ref == nil {
			continue
		}
		sc, msg, err := s.loadScene(ctx, client, root, ref)
		if err != nil {
			return nil, err
		}
		if msg != "" {
			return &SceneCompare{Engine: sceneEngine(ref.Path), Objects: []SceneObject{}, Changes: []SceneChange{}, Refs: []RefChange{}, Error: msg}, nil
		}
		scenes[i] = sc
	}
	d := preview.DiffScenes(scenes[0], scenes[1])
	out := &SceneCompare{
		Engine: d.Engine, Objects: make([]SceneObject, len(d.Objects)), Changes: make([]SceneChange, len(d.Changes)),
		Added: d.Added, Removed: d.Removed, Changed: d.Changed, Refs: make([]RefChange, len(d.Refs)),
	}
	for i, o := range d.Objects {
		out.Objects[i] = SceneObject{ID: o.ID, Path: o.Path, Type: o.Type, Parent: o.Parent}
	}
	for i, c := range d.Changes {
		props := make([]PropChange, len(c.Props))
		for j, p := range c.Props {
			props[j] = PropChange{Key: p.Key, Old: p.Old, New: p.New}
		}
		out.Changes[i] = SceneChange{Status: c.Status, ID: c.ID, Path: c.Path, Type: c.Type, Props: props}
	}
	for i, r := range d.Refs {
		out.Refs[i] = RefChange{Path: r.Path, Status: r.Status}
	}
	return out, nil
}

func (s *Service) loadScene(ctx context.Context, client daemonpb.NipaDaemonClient, root string, ref *ImageRef) (*preview.Scene, string, error) {
	_, data, err := s.readBlob(ctx, client, root, blobRef(ref), maxSceneBytes)
	if errors.Is(err, errBlobTooLarge) {
		return nil, "the file is too large to compare as a scene", nil
	}
	engine := sceneEngine(ref.Path)
	if errors.Is(err, errBlobMissing) {
		return &preview.Scene{Engine: engine}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var sc *preview.Scene
	if engine == "godot" {
		sc, err = preview.ParseGodotResource(data)
	} else {
		index := s.guidIndex(root, unityProject(ref.Path))
		sc, err = preview.ParseUnityYAML(data, func(guid string) string { return index[guid] })
	}
	if err != nil {
		return nil, strings.TrimPrefix(err.Error(), "preview: "), nil
	}
	return sc, "", nil
}

var godotSceneExts = map[string]bool{".tscn": true, ".tres": true, ".godot": true, ".import": true}

func sceneEngine(p string) string {
	if godotSceneExts[strings.ToLower(path.Ext(p))] {
		return "godot"
	}
	return "unity"
}

// unityProject returns the clone-relative folder holding the Unity project a
// path belongs to: whatever precedes its Assets or Packages folder.
func unityProject(p string) string {
	parts := strings.Split(path.Clean(filepath.ToSlash(p)), "/")
	for i, part := range parts {
		if part == "Assets" || part == "Packages" {
			return strings.Join(parts[:i], "/")
		}
	}
	return ""
}

func (s *Service) guidIndex(root, project string) map[string]string {
	key := guidKey{root, project}
	s.guids.mu.Lock()
	index, ok := s.guids.cache[key]
	gen := s.guids.gen[root]
	build := s.guids.build
	s.guids.mu.Unlock()
	if ok {
		return index
	}
	if build == nil {
		build = buildGUIDIndex
	}
	index = build(filepath.Join(root, filepath.FromSlash(project)))
	s.guids.mu.Lock()
	defer s.guids.mu.Unlock()
	if s.guids.gen[root] != gen {
		return index
	}
	if s.guids.cache == nil {
		s.guids.cache = map[guidKey]map[string]string{}
	}
	s.guids.cache[key] = index
	return index
}

// invalidateGUIDs drops the cached indexes of root when a change may have
// touched a .meta file; no paths means anything may have changed.
func (s *Service) invalidateGUIDs(root string, paths []string) {
	touched := len(paths) == 0
	for _, p := range paths {
		if strings.HasSuffix(p, ".meta") {
			touched = true
			break
		}
	}
	if !touched {
		return
	}
	s.guids.mu.Lock()
	defer s.guids.mu.Unlock()
	if s.guids.gen == nil {
		s.guids.gen = map[string]uint64{}
	}
	s.guids.gen[root]++
	for key := range s.guids.cache {
		if key.root == root {
			delete(s.guids.cache, key)
		}
	}
}

func buildGUIDIndex(dir string) map[string]string {
	index := map[string]string{}
	for _, top := range []string{"Assets", "Packages"} {
		_ = filepath.WalkDir(filepath.Join(dir, top), func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(p, ".meta") {
				return nil
			}
			guid := readGUID(p)
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

func readGUID(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, maxMetaBytes))
	return unitymeta.GUID(data)
}
