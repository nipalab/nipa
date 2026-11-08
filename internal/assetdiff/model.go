package assetdiff

import (
	"slices"

	"github.com/nipalab/nipa/internal/preview"
)

type ModelResult struct {
	Old   *ModelSide  `json:"old,omitempty"`
	New   *ModelSide  `json:"new,omitempty"`
	Delta *ModelDelta `json:"delta,omitempty"`
}

type ModelSide struct {
	Format       string   `json:"format"`
	Generator    string   `json:"generator,omitempty"`
	Meshes       int      `json:"meshes"`
	Primitives   int      `json:"primitives"`
	Vertices     int      `json:"vertices"`
	Triangles    int      `json:"triangles"`
	Materials    []string `json:"materials"`
	Textures     int      `json:"textures"`
	Nodes        int      `json:"nodes"`
	Skins        int      `json:"skins"`
	Joints       int      `json:"joints"`
	Animations   []string `json:"animations"`
	MorphTargets int      `json:"morph_targets"`
	Bounds       *Bounds  `json:"bounds,omitempty"`
}

type Bounds struct {
	Min [3]float64 `json:"min"`
	Max [3]float64 `json:"max"`
}

type ModelDelta struct {
	Meshes            int      `json:"meshes"`
	Vertices          int      `json:"vertices"`
	Triangles         int      `json:"triangles"`
	Textures          int      `json:"textures"`
	Nodes             int      `json:"nodes"`
	Joints            int      `json:"joints"`
	MaterialsAdded    []string `json:"materials_added,omitempty"`
	MaterialsRemoved  []string `json:"materials_removed,omitempty"`
	AnimationsAdded   []string `json:"animations_added,omitempty"`
	AnimationsRemoved []string `json:"animations_removed,omitempty"`
	BoundsChanged     bool     `json:"bounds_changed"`
}

func compareModel(sides [2]side) (*ModelResult, error) {
	out := &ModelResult{}
	for i, s := range sides {
		if !s.present {
			continue
		}
		st, err := preview.DecodeModel(s.ref.Path, s.data)
		if err != nil {
			return nil, err
		}
		m := &ModelSide{
			Format: st.Format, Generator: st.Generator, Meshes: st.Meshes, Primitives: st.Primitives,
			Vertices: st.Vertices, Triangles: st.Triangles, Materials: orEmpty(st.Materials), Textures: st.Textures,
			Nodes: st.Nodes, Skins: st.Skins, Joints: st.Joints, Animations: orEmpty(st.Animations), MorphTargets: st.MorphTargets,
		}
		if st.HasBounds {
			m.Bounds = &Bounds{Min: st.Min, Max: st.Max}
		}
		if i == 0 {
			out.Old = m
		} else {
			out.New = m
		}
	}
	if out.Old == nil || out.New == nil {
		return out, nil
	}
	o, n := out.Old, out.New
	out.Delta = &ModelDelta{
		Meshes: n.Meshes - o.Meshes, Vertices: n.Vertices - o.Vertices, Triangles: n.Triangles - o.Triangles,
		Textures: n.Textures - o.Textures, Nodes: n.Nodes - o.Nodes, Joints: n.Joints - o.Joints,
		MaterialsAdded: missingFrom(n.Materials, o.Materials), MaterialsRemoved: missingFrom(o.Materials, n.Materials),
		AnimationsAdded: missingFrom(n.Animations, o.Animations), AnimationsRemoved: missingFrom(o.Animations, n.Animations),
		BoundsChanged: (o.Bounds == nil) != (n.Bounds == nil) || (o.Bounds != nil && *o.Bounds != *n.Bounds),
	}
	return out, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func missingFrom(a, b []string) []string {
	var out []string
	for _, v := range a {
		if !slices.Contains(b, v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
