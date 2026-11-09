// Package assetrefs builds the reference graph of Unity (Force Text) and
// Godot text assets: which files a scene, prefab or resource points to by
// GUID, uid:// or res:// path, and which files point back. It is pure and
// reads through fs.FS, like internal/diff it must not import client code.
package assetrefs

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/godotmeta"
	"github.com/nipalab/nipa/internal/unitymeta"
)

const (
	DefaultMaxFiles     = 5000
	HardMaxFiles        = 20000
	DefaultMaxFileBytes = 8 << 20
	maxIndexBytes       = 4 << 10
	maxSidecarBytes     = 1 << 20

	guidPrefix = "guid:"
	uidScheme  = "uid://"
	resScheme  = "res://"
)

var unitySourceExts = map[string]bool{
	".unity": true, ".prefab": true, ".asset": true, ".mat": true, ".controller": true,
	".overridecontroller": true, ".anim": true, ".mask": true, ".playable": true, ".signal": true,
	".lighting": true, ".spriteatlas": true, ".terrainlayer": true,
}

var godotSourceExts = map[string]bool{".tscn": true, ".tres": true}

// Options bounds a scan. Outside reports paths a partial (sparse) clone does
// not hold; with it set, references that cannot be resolved are unknown
// rather than unresolved.
type Options struct {
	MaxFiles     int
	MaxFileBytes int64
	Outside      func(p string) bool
}

func (o Options) normalized() Options {
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if o.MaxFiles > HardMaxFiles {
		o.MaxFiles = HardMaxFiles
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	return o
}

// Ref is one dependency of a source file. Raw is the reference as written
// (guid:<x>, res://..., uid://...), Path the repo path it maps to (also set
// for an unresolved res:// path).
type Ref struct {
	Path     string `json:"path,omitempty"`
	Raw      string `json:"raw"`
	Resolved bool   `json:"resolved"`
	Unknown  bool   `json:"unknown,omitempty"`
	uid      string
}

type rawRef struct {
	raw  string
	path string
	uid  string
}

type Graph struct {
	Scanned   int
	Skipped   int
	Truncated bool

	opts   Options
	files  map[string]bool
	dirs   map[string]bool
	godot  []string
	unity  map[string]map[string]string
	uids   map[string]string
	edges  map[string][]Ref
	usedBy map[string][]string
}

// IsSource reports whether p is a file the graph parses for references.
func IsSource(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return unitySourceExts[ext] || godotSourceExts[ext]
}

func isUnitySource(p string) bool { return unitySourceExts[strings.ToLower(path.Ext(p))] }

func isGodotSource(p string) bool { return godotSourceExts[strings.ToLower(path.Ext(p))] }

// Relevant reports whether a change to p can affect the reference graph:
// sources, sidecars, Unity project files and Godot-imported assets.
func Relevant(p string) bool {
	ext := path.Ext(p)
	switch {
	case IsSource(p), strings.HasSuffix(p, ".meta"), godotmeta.IsSidecar(p):
		return true
	case godotmeta.Importable(ext), godotmeta.HasUIDFile(ext):
		return true
	}
	return unityProject(p) != ""
}

// Build parses the source files among files (repo-relative slash paths read
// through fsys) and resolves their references against the indexes built
// from the same file set.
func Build(fsys fs.FS, files []string, opts Options) *Graph {
	g := &Graph{
		opts: opts.normalized(), files: map[string]bool{}, dirs: map[string]bool{},
		unity: map[string]map[string]string{}, uids: map[string]string{},
		edges: map[string][]Ref{}, usedBy: map[string][]string{},
	}
	var sources, metas, sidecars []string
	for _, f := range files {
		g.files[f] = true
		for d := path.Dir(f); d != "." && d != "/" && !g.dirs[d]; d = path.Dir(d) {
			g.dirs[d] = true
		}
		switch {
		case IsSource(f):
			sources = append(sources, f)
		case strings.HasSuffix(f, ".meta"):
			metas = append(metas, f)
		case godotmeta.IsSidecar(f):
			sidecars = append(sidecars, f)
		case path.Base(f) == "project.godot":
			g.godot = append(g.godot, path.Dir(f))
		}
	}
	sort.Strings(sources)
	if len(sources) > g.opts.MaxFiles {
		sources, g.Truncated = sources[:g.opts.MaxFiles], true
	}
	parsed := map[string][]rawRef{}
	hasUnity := false
	for _, src := range sources {
		data, err := readCapped(fsys, src, g.opts.MaxFileBytes)
		if err != nil {
			g.Skipped++
			g.Truncated = true
			continue
		}
		g.Scanned++
		refs, self := parseRaw(src, data)
		if self != "" {
			g.uids[self] = src
		}
		if isUnitySource(src) {
			hasUnity = true
		}
		parsed[src] = refs
	}
	if hasUnity {
		g.indexUnity(fsys, metas)
	}
	g.indexGodot(fsys, sidecars)
	for _, src := range sources {
		refs, ok := parsed[src]
		if !ok {
			continue
		}
		out := make([]Ref, 0, len(refs))
		for _, r := range refs {
			ref := g.resolve(src, r)
			out = append(out, ref)
			if ref.Resolved {
				g.usedBy[ref.Path] = append(g.usedBy[ref.Path], src)
			}
		}
		g.edges[src] = out
	}
	for p, users := range g.usedBy {
		g.usedBy[p] = dedupSorted(users)
	}
	return g
}

func (g *Graph) exists(p string) bool { return g.files[p] || g.dirs[p] }

// Partial reports whether the scanned file set is a sparse subset of the
// repository, so UsedBy can miss referrers outside it.
func (g *Graph) Partial() bool { return g.opts.Outside != nil }

// Exists reports whether p is a file or directory of the scanned file set.
func (g *Graph) Exists(p string) bool { return g.exists(p) }

func (g *Graph) indexUnity(fsys fs.FS, metas []string) {
	for _, m := range metas {
		asset := strings.TrimSuffix(m, ".meta")
		if !g.exists(asset) {
			continue
		}
		data, err := readCapped(fsys, m, maxIndexBytes)
		if err != nil && !errors.Is(err, errTooLarge) {
			continue
		}
		guid := strings.ToLower(unitymeta.GUID(data))
		if guid == "" {
			continue
		}
		project := unityProject(asset)
		idx := g.unity[project]
		if idx == nil {
			idx = map[string]string{}
			g.unity[project] = idx
		}
		idx[guid] = asset
	}
}

func (g *Graph) indexGodot(fsys fs.FS, sidecars []string) {
	for _, s := range sidecars {
		asset := godotmeta.AssetOf(s)
		if !g.files[asset] {
			continue
		}
		data, err := readCapped(fsys, s, maxSidecarBytes)
		if err != nil {
			continue
		}
		if uid := godotmeta.UID(data); uid != "" {
			g.uids[uid] = asset
		}
	}
}

func (g *Graph) resolve(src string, r rawRef) Ref {
	ref := Ref{Raw: r.raw, uid: r.uid}
	if guid, ok := strings.CutPrefix(r.raw, guidPrefix); ok {
		if target := g.unity[unityProject(src)][guid]; target != "" {
			ref.Path, ref.Resolved = target, true
			return ref
		}
		ref.Unknown = g.opts.Outside != nil
		return ref
	}
	if r.uid != "" {
		if target := g.uids[r.uid]; target != "" {
			ref.Path, ref.Resolved = target, true
			return ref
		}
	}
	if r.path != "" {
		ref.Path = g.resPath(src, r.path)
		switch {
		case g.exists(ref.Path):
			ref.Resolved = true
		case g.opts.Outside != nil && g.opts.Outside(ref.Path):
			ref.Unknown = true
		}
		return ref
	}
	ref.Unknown = g.opts.Outside != nil
	return ref
}

// resPath maps res://x to x below the nearest folder holding project.godot.
func (g *Graph) resPath(src, res string) string {
	rel := strings.TrimPrefix(res, resScheme)
	root := ""
	for _, dir := range g.godot {
		if (dir == "." || dir == path.Dir(src) || strings.HasPrefix(src, dir+"/")) && len(dir) >= len(root) {
			root = dir
		}
	}
	if root == "" || root == "." {
		return path.Clean(rel)
	}
	return path.Join(root, rel)
}

// Uses returns the dependencies of p, sorted by raw reference.
func (g *Graph) Uses(p string) []Ref { return g.edges[p] }

// UsedBy returns the sources referencing p, sorted.
func (g *Graph) UsedBy(p string) []string { return g.usedBy[p] }

// Sources lists the parsed source files, sorted.
func (g *Graph) Sources() []string {
	out := make([]string, 0, len(g.edges))
	for p := range g.edges {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (g *Graph) unityGUID(project, guid string) string { return g.unity[project][guid] }

var errTooLarge = errors.New("assetrefs: file too large")

func readCapped(fsys fs.FS, p string, limit int64) ([]byte, error) {
	f, err := fsys.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return data[:limit], errTooLarge
	}
	return data, nil
}

// unityProject returns the folder in front of a path's Assets, Packages or
// ProjectSettings segment, "." for a project at the repo root, or "" when
// the path is not inside a Unity project.
func unityProject(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts[:len(parts)-1] {
		if part == "Assets" || part == "Packages" || part == "ProjectSettings" {
			if i == 0 {
				return "."
			}
			return strings.Join(parts[:i], "/")
		}
	}
	return ""
}

func dedupSorted(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}
