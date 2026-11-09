package assetrefs

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/godotmeta"
	"github.com/nipalab/nipa/internal/unitymeta"
)

const (
	ReasonRemoved     = "removed"
	ReasonRenamed     = "renamed"
	ReasonGUIDChanged = "guid_changed"
	ReasonUIDChanged  = "uid_changed"
	ReasonUnresolved  = "unresolved"
)

type BrokenRef struct {
	From   string `json:"from"`
	Raw    string `json:"raw"`
	Target string `json:"target,omitempty"`
	Reason string `json:"reason"`
}

type Impact struct {
	Broken      []BrokenRef `json:"broken"`
	NewlyUnused []string    `json:"newly_unused"`
	Scanned     int         `json:"scanned"`
	Truncated   bool        `json:"truncated,omitempty"`
}

func (i *Impact) Empty() bool {
	return i == nil || (len(i.Broken) == 0 && len(i.NewlyUnused) == 0)
}

type lostID struct {
	path   string
	target string
	reason string
}

type oldSource struct {
	path string
	refs []rawRef
}

type impactRun struct {
	ctx       context.Context
	g         *Graph
	load      assetdiff.Loader
	ids       map[string]lostID
	paths     map[string]lostID
	oldRaw    map[string]map[string]bool
	oldLost   map[string]bool
	changed   map[string]bool
	sources   []oldSource
	truncated bool
}

// ComputeImpact reports the references the changes break and the assets
// they leave unused. g is the graph of the new side; the old side of each
// change is read through load (sidecars and sources only); an unreadable old
// side marks the result truncated. A reference that
// was already unresolved before the change is not reported, and nothing is
// reported unused when the scan was truncated.
func ComputeImpact(ctx context.Context, g *Graph, changes []assetdiff.Item, load assetdiff.Loader) (*Impact, error) {
	r := &impactRun{
		ctx: ctx, g: g, load: load,
		ids: map[string]lostID{}, paths: map[string]lostID{},
		oldRaw: map[string]map[string]bool{}, oldLost: map[string]bool{}, changed: map[string]bool{},
	}
	for _, it := range changes {
		if err := r.collect(it); err != nil {
			return nil, err
		}
	}
	out := &Impact{Broken: r.broken(), NewlyUnused: []string{}, Scanned: g.Scanned, Truncated: g.Truncated || r.truncated}
	if !out.Truncated {
		out.NewlyUnused = r.unused()
	}
	return out, nil
}

// read loads p from the old side; known is false when the old content could
// not be read, as opposed to a file that did not exist.
func (r *impactRun) read(it assetdiff.Item, p string, limit int64) (data []byte, known bool, err error) {
	ref := *it.Old
	ref.Path = p
	data, err = r.load(r.ctx, ref, limit)
	switch {
	case r.ctx.Err() != nil:
		return nil, false, r.ctx.Err()
	case errors.Is(err, assetdiff.ErrMissing):
		return nil, true, nil
	case err != nil:
		r.truncated = true
		return nil, false, nil
	}
	return data, true, nil
}

func (r *impactRun) collect(it assetdiff.Item) error {
	oldPath := it.OldPath
	if oldPath == "" {
		oldPath = it.Path
	}
	gone := ""
	switch it.Status {
	case assetdiff.StatusRemoved:
		gone = ReasonRemoved
		r.paths[oldPath] = lostID{path: oldPath, reason: ReasonRemoved}
	case "renamed":
		gone = ReasonRenamed
		r.paths[oldPath] = lostID{path: oldPath, target: it.Path, reason: ReasonRenamed}
	}
	if it.New != nil {
		r.changed[it.Path] = true
	}
	if it.Old == nil {
		return nil
	}
	switch {
	case strings.HasSuffix(oldPath, ".meta"):
		return r.lostGUID(it, oldPath, strings.TrimSuffix(oldPath, ".meta"), "")
	case godotmeta.IsSidecar(oldPath):
		return r.lostUID(it, oldPath, godotmeta.AssetOf(oldPath), "")
	}
	if gone != "" {
		if unityProject(oldPath) != "" {
			if err := r.lostGUID(it, oldPath+".meta", oldPath, gone); err != nil {
				return err
			}
		}
		if sidecar, ok := godotmeta.Sidecar(oldPath); ok {
			if err := r.lostUID(it, sidecar, oldPath, gone); err != nil {
				return err
			}
		}
	}
	if !IsSource(oldPath) {
		return nil
	}
	if len(r.sources) >= r.g.opts.MaxFiles {
		r.truncated = true
		r.oldLost[it.Path] = true
		return nil
	}
	data, known, err := r.read(it, oldPath, r.g.opts.MaxFileBytes)
	if err != nil {
		return err
	}
	if !known {
		r.oldLost[it.Path] = true
		return nil
	}
	refs, self := parseRaw(oldPath, data)
	if self != "" && gone != "" && r.g.uids[self] == "" {
		r.ids[self] = lostID{path: oldPath, target: renameTarget(it, gone), reason: gone}
	}
	if it.New != nil {
		set := make(map[string]bool, len(refs))
		for _, ref := range refs {
			set[ref.raw] = true
		}
		r.oldRaw[it.Path] = set
	}
	r.sources = append(r.sources, oldSource{path: oldPath, refs: refs})
	return nil
}

func renameTarget(it assetdiff.Item, gone string) string {
	if gone == ReasonRenamed {
		return it.Path
	}
	return ""
}

func (r *impactRun) lostGUID(it assetdiff.Item, meta, asset, gone string) error {
	data, _, err := r.read(it, meta, maxSidecarBytes)
	if err != nil {
		return err
	}
	guid := strings.ToLower(unitymeta.GUID(data))
	if guid == "" || r.g.unityGUID(unityProject(asset), guid) != "" {
		return nil
	}
	reason := gone
	if reason == "" {
		reason = ReasonGUIDChanged
		if !r.g.exists(asset) {
			reason = ReasonRemoved
		}
	}
	r.ids[unityKey(asset, guidPrefix+guid)] = lostID{path: asset, target: renameTarget(it, gone), reason: reason}
	return nil
}

func (r *impactRun) lostUID(it assetdiff.Item, sidecar, asset, gone string) error {
	data, _, err := r.read(it, sidecar, maxSidecarBytes)
	if err != nil {
		return err
	}
	uid := godotmeta.UID(data)
	if uid == "" || r.g.uids[uid] != "" {
		return nil
	}
	reason := gone
	if reason == "" {
		reason = ReasonUIDChanged
		if !r.g.files[asset] {
			reason = ReasonRemoved
		}
	}
	r.ids[uid] = lostID{path: asset, target: renameTarget(it, gone), reason: reason}
	return nil
}

func (r *impactRun) broken() []BrokenRef {
	out := []BrokenRef{}
	for _, src := range r.g.Sources() {
		for _, ref := range r.g.Uses(src) {
			if ref.Resolved {
				continue
			}
			key := ref.uid
			if strings.HasPrefix(ref.Raw, guidPrefix) {
				key = unityKey(src, ref.Raw)
			}
			if l, ok := r.ids[key]; ok && key != "" {
				out = append(out, brokenFrom(src, ref.Raw, l))
				continue
			}
			if l, ok := r.paths[ref.Path]; ok && ref.Path != "" {
				out = append(out, brokenFrom(src, ref.Raw, l))
				continue
			}
			if ref.Unknown || !r.changed[src] || r.oldLost[src] || r.oldRaw[src][ref.Raw] {
				continue
			}
			out = append(out, BrokenRef{From: src, Raw: ref.Raw, Target: ref.Path, Reason: ReasonUnresolved})
		}
	}
	return out
}

func unityKey(p, raw string) string { return unityProject(p) + "|" + raw }

func brokenFrom(src, raw string, l lostID) BrokenRef {
	target := l.path
	if l.target != "" {
		target = l.target
	}
	return BrokenRef{From: src, Raw: raw, Target: target, Reason: l.reason}
}

func (r *impactRun) unused() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, src := range r.sources {
		for _, ref := range src.refs {
			t := r.oldTarget(src.path, ref)
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			if r.g.files[t] && len(r.g.UsedBy(t)) == 0 && !neverUnused(t) {
				out = append(out, t)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (r *impactRun) oldTarget(src string, ref rawRef) string {
	if guid, ok := strings.CutPrefix(ref.raw, guidPrefix); ok {
		if t := r.g.unityGUID(unityProject(src), guid); t != "" {
			return t
		}
		return r.ids[unityKey(src, ref.raw)].path
	}
	if ref.uid != "" {
		if t := r.g.uids[ref.uid]; t != "" {
			return t
		}
		if l, ok := r.ids[ref.uid]; ok {
			return l.path
		}
	}
	if ref.path != "" {
		return r.g.resPath(src, ref.path)
	}
	return ""
}

var neverUnusedExts = map[string]bool{
	".unity": true, ".tscn": true, ".cs": true, ".gd": true, ".shader": true, ".cginc": true,
	".hlsl": true, ".compute": true, ".gdshader": true, ".gdshaderinc": true,
}

var neverUnusedDirs = map[string]bool{
	"Resources": true, "StreamingAssets": true, "Editor": true, "Plugins": true, "addons": true,
}

// neverUnused reports assets that code, build settings or the engine load
// without a static reference: scenes, scripts and special folders.
func neverUnused(p string) bool {
	if neverUnusedExts[strings.ToLower(path.Ext(p))] {
		return true
	}
	for _, part := range strings.Split(path.Dir(p), "/") {
		if neverUnusedDirs[part] {
			return true
		}
	}
	return false
}
