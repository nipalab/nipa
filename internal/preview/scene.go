package preview

import (
	"errors"
	"sort"
)

// MaxSceneObjects and MaxSceneProps bound what a scene parser reads.
var (
	MaxSceneObjects = 200_000
	MaxSceneProps   = 2_000_000
)

var (
	errScene         = errors.New("preview: invalid scene")
	errSceneTooLarge = errors.New("preview: scene too large")
)

// Scene is an engine scene or asset flattened into objects with scalar
// properties. Parent is the ID of the parent object, empty at the root.
type Scene struct {
	Engine  string
	Objects []SceneObject
	Refs    []string
}

type SceneObject struct {
	ID, Path, Type, Parent string
	Props                  map[string]string
}

type PropChange struct {
	Key, Old, New string
}

type SceneChange struct {
	Status         string // added, removed, changed, moved
	ID, Path, Type string
	Props          []PropChange
}

type RefChange struct {
	Path   string
	Status string // added, removed
}

// SceneCompare holds the changed objects plus, in Objects, those objects and
// their ancestors (without properties) so a viewer can draw the tree.
type SceneCompare struct {
	Engine                  string
	Objects                 []SceneObject
	Changes                 []SceneChange
	Added, Removed, Changed int
	Refs                    []RefChange
}

// DiffScenes matches objects by ID; a nil side is an empty scene.
func DiffScenes(old, new *Scene) *SceneCompare {
	if old == nil {
		old = &Scene{}
	}
	if new == nil {
		new = &Scene{}
	}
	out := &SceneCompare{Engine: new.Engine, Objects: []SceneObject{}, Changes: []SceneChange{}, Refs: []RefChange{}}
	if out.Engine == "" {
		out.Engine = old.Engine
	}
	oldByID, newByID := indexObjects(old.Objects), indexObjects(new.Objects)
	for i, o := range new.Objects {
		if newByID[o.ID] != &new.Objects[i] {
			continue
		}
		prev, ok := oldByID[o.ID]
		if !ok {
			out.Changes = append(out.Changes, SceneChange{Status: "added", ID: o.ID, Path: o.Path, Type: o.Type, Props: propList(o.Props, false)})
			out.Added++
			continue
		}
		props := diffProps(prev.Props, o.Props)
		if prev.Type != o.Type {
			props = append([]PropChange{{Key: "type", Old: prev.Type, New: o.Type}}, props...)
		}
		status := "changed"
		if prev.Parent != o.Parent {
			status = "moved"
			props = append([]PropChange{{Key: "parent", Old: pathOf(oldByID, prev.Parent), New: pathOf(newByID, o.Parent)}}, props...)
		}
		if len(props) > 0 {
			out.Changes = append(out.Changes, SceneChange{Status: status, ID: o.ID, Path: o.Path, Type: o.Type, Props: props})
			out.Changed++
		}
	}
	for i, o := range old.Objects {
		if _, ok := newByID[o.ID]; !ok && oldByID[o.ID] == &old.Objects[i] {
			out.Changes = append(out.Changes, SceneChange{Status: "removed", ID: o.ID, Path: o.Path, Type: o.Type, Props: propList(o.Props, true)})
			out.Removed++
		}
	}
	sort.SliceStable(out.Changes, func(i, j int) bool {
		if out.Changes[i].Path != out.Changes[j].Path {
			return out.Changes[i].Path < out.Changes[j].Path
		}
		return out.Changes[i].ID < out.Changes[j].ID
	})
	out.Objects = treeOf(out.Changes, oldByID, newByID)
	out.Refs = diffRefs(old.Refs, new.Refs)
	return out
}

func indexObjects(objs []SceneObject) map[string]*SceneObject {
	m := make(map[string]*SceneObject, len(objs))
	for i := range objs {
		if _, dup := m[objs[i].ID]; !dup {
			m[objs[i].ID] = &objs[i]
		}
	}
	return m
}

func pathOf(objs map[string]*SceneObject, id string) string {
	if id == "" {
		return "(root)"
	}
	if o := objs[id]; o != nil {
		return o.Path
	}
	return "#" + id
}

func propList(props map[string]string, removed bool) []PropChange {
	out := make([]PropChange, 0, len(props))
	for _, k := range sortedKeys(props) {
		if removed {
			out = append(out, PropChange{Key: k, Old: props[k]})
		} else {
			out = append(out, PropChange{Key: k, New: props[k]})
		}
	}
	return out
}

func diffProps(old, new map[string]string) []PropChange {
	var out []PropChange
	keys := sortedKeys(new)
	for k := range old {
		if _, ok := new[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		o, n := old[k], new[k]
		if o != n {
			out = append(out, PropChange{Key: k, Old: o, New: n})
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func treeOf(changes []SceneChange, oldByID, newByID map[string]*SceneObject) []SceneObject {
	seen := map[string]bool{}
	out := []SceneObject{}
	add := func(id string, removed bool) {
		for id != "" && !seen[id] {
			seen[id] = true
			o := newByID[id]
			if o == nil || removed {
				o = oldByID[id]
			}
			if o == nil {
				return
			}
			out = append(out, SceneObject{ID: o.ID, Path: o.Path, Type: o.Type, Parent: o.Parent})
			id, removed = o.Parent, removed && newByID[o.Parent] == nil
		}
	}
	for _, c := range changes {
		add(c.ID, c.Status == "removed")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func diffRefs(old, new []string) []RefChange {
	in := func(list []string) map[string]bool {
		m := make(map[string]bool, len(list))
		for _, p := range list {
			m[p] = true
		}
		return m
	}
	o, n := in(old), in(new)
	out := []RefChange{}
	for _, p := range sortedSet(n) {
		if !o[p] {
			out = append(out, RefChange{Path: p, Status: "added"})
		}
	}
	for _, p := range sortedSet(o) {
		if !n[p] {
			out = append(out, RefChange{Path: p, Status: "removed"})
		}
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
