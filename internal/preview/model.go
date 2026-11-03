package preview

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// ModelStats summarizes a 3D model for review: what it contains and where it
// sits in space. Counts describe the asset's meshes, not their instances.
type ModelStats struct {
	Format       string
	Generator    string
	Meshes       int
	Primitives   int
	Vertices     int
	Triangles    int
	Materials    []string
	Textures     int
	Nodes        int
	Skins        int
	Joints       int
	Animations   []string
	MorphTargets int
	HasBounds    bool
	Min, Max     [3]float64
	// Dependencies are the files the model loads next to itself (glTF buffers
	// and images, OBJ material libraries), relative to the model's folder.
	Dependencies []string
}

var errModel = errors.New("preview: invalid model")

// DecodeModel reads model statistics from glTF (JSON), GLB and OBJ files.
// Other model formats return ErrUnsupported; viewers can still render them.
func DecodeModel(name string, data []byte) (*ModelStats, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".glb":
		return decodeGLB(data)
	case ".gltf":
		return decodeGLTF(data, "gltf")
	case ".obj":
		return decodeOBJ(data)
	}
	return nil, fmt.Errorf("%w: %s model statistics", ErrUnsupported, strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."))
}

const (
	glbMagic     = 0x46546c67
	glbChunkJSON = 0x4e4f534a
)

func decodeGLB(data []byte) (*ModelStats, error) {
	if len(data) < 20 || binary.LittleEndian.Uint32(data) != glbMagic || binary.LittleEndian.Uint32(data[4:]) != 2 {
		return nil, errModel
	}
	size := int(binary.LittleEndian.Uint32(data[12:]))
	if binary.LittleEndian.Uint32(data[16:]) != glbChunkJSON || size > len(data)-20 {
		return nil, errModel
	}
	return decodeGLTF(bytes.TrimRight(data[20:20+size], "\x00 "), "glb")
}

type gltfAccessor struct {
	Count int       `json:"count"`
	Min   []float64 `json:"min"`
	Max   []float64 `json:"max"`
}

type gltfPrimitive struct {
	Attributes map[string]int   `json:"attributes"`
	Indices    *int             `json:"indices"`
	Mode       *int             `json:"mode"`
	Targets    []map[string]int `json:"targets"`
}

type gltfNode struct {
	Children    []int     `json:"children"`
	Mesh        *int      `json:"mesh"`
	Matrix      []float64 `json:"matrix"`
	Translation []float64 `json:"translation"`
	Rotation    []float64 `json:"rotation"`
	Scale       []float64 `json:"scale"`
}

type gltfDoc struct {
	Asset struct {
		Generator string `json:"generator"`
		Version   string `json:"version"`
	} `json:"asset"`
	Accessors []gltfAccessor `json:"accessors"`
	Meshes    []struct {
		Primitives []gltfPrimitive `json:"primitives"`
	} `json:"meshes"`
	Materials []struct {
		Name string `json:"name"`
	} `json:"materials"`
	Textures []json.RawMessage `json:"textures"`
	Images   []struct {
		URI string `json:"uri"`
	} `json:"images"`
	Buffers []struct {
		URI string `json:"uri"`
	} `json:"buffers"`
	Nodes  []gltfNode `json:"nodes"`
	Scenes []struct {
		Nodes []int `json:"nodes"`
	} `json:"scenes"`
	Scene *int `json:"scene"`
	Skins []struct {
		Joints []int `json:"joints"`
	} `json:"skins"`
	Animations []struct {
		Name string `json:"name"`
	} `json:"animations"`
}

const (
	gltfTriangles     = 4
	gltfTriangleStrip = 5
	gltfTriangleFan   = 6
)

func decodeGLTF(data []byte, format string) (*ModelStats, error) {
	var doc gltfDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%w: %v", errModel, err)
	}
	if !strings.HasPrefix(doc.Asset.Version, "2") {
		return nil, fmt.Errorf("%w: glTF %q", ErrUnsupported, doc.Asset.Version)
	}
	s := &ModelStats{
		Format: format, Generator: doc.Asset.Generator, Meshes: len(doc.Meshes), Textures: len(doc.Textures),
		Nodes: len(doc.Nodes), Skins: len(doc.Skins), Materials: []string{}, Animations: []string{}, Dependencies: []string{},
	}
	for i, m := range doc.Materials {
		s.Materials = append(s.Materials, nameOr(m.Name, "Material", i))
	}
	for i, a := range doc.Animations {
		s.Animations = append(s.Animations, nameOr(a.Name, "Animation", i))
	}
	joints := map[int]bool{}
	for _, skin := range doc.Skins {
		for _, j := range skin.Joints {
			joints[j] = true
		}
	}
	s.Joints = len(joints)
	accessor := func(i int) (gltfAccessor, bool) {
		if i < 0 || i >= len(doc.Accessors) {
			return gltfAccessor{}, false
		}
		return doc.Accessors[i], true
	}
	for _, mesh := range doc.Meshes {
		for _, p := range mesh.Primitives {
			s.Primitives++
			s.MorphTargets += len(p.Targets)
			pos, ok := accessor(p.Attributes["POSITION"])
			if _, has := p.Attributes["POSITION"]; !has || !ok {
				continue
			}
			s.Vertices += max(pos.Count, 0)
			n := max(pos.Count, 0)
			if p.Indices != nil {
				if idx, ok := accessor(*p.Indices); ok {
					n = max(idx.Count, 0)
				}
			}
			mode := gltfTriangles
			if p.Mode != nil {
				mode = *p.Mode
			}
			switch mode {
			case gltfTriangles:
				s.Triangles += n / 3
			case gltfTriangleStrip, gltfTriangleFan:
				s.Triangles += max(n-2, 0)
			}
		}
	}
	for _, b := range doc.Buffers {
		s.addDependency(b.URI)
	}
	for _, img := range doc.Images {
		s.addDependency(img.URI)
	}
	gltfBounds(&doc, s)
	return s, nil
}

func nameOr(name, kind string, i int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("%s %d", kind, i+1)
}

func (s *ModelStats) addDependency(uri string) {
	if uri == "" || strings.HasPrefix(uri, "data:") || strings.Contains(uri, "://") {
		return
	}
	if decoded, err := url.PathUnescape(uri); err == nil {
		uri = decoded
	}
	for _, d := range s.Dependencies {
		if d == uri {
			return
		}
	}
	s.Dependencies = append(s.Dependencies, uri)
}

func (s *ModelStats) include(p [3]float64) {
	if !s.HasBounds {
		s.Min, s.Max, s.HasBounds = p, p, true
		return
	}
	for i := range 3 {
		s.Min[i] = math.Min(s.Min[i], p[i])
		s.Max[i] = math.Max(s.Max[i], p[i])
	}
}

type mat4 [16]float64

var identity = mat4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}

func (a mat4) mul(b mat4) mat4 {
	var out mat4
	for col := range 4 {
		for row := range 4 {
			var v float64
			for k := range 4 {
				v += a[k*4+row] * b[col*4+k]
			}
			out[col*4+row] = v
		}
	}
	return out
}

func (a mat4) apply(p [3]float64) [3]float64 {
	var out [3]float64
	for row := range 3 {
		out[row] = a[row]*p[0] + a[4+row]*p[1] + a[8+row]*p[2] + a[12+row]
	}
	return out
}

// local builds the node transform from its column-major matrix or its
// translation, rotation (quaternion) and scale.
func (n gltfNode) local() mat4 {
	if len(n.Matrix) == 16 {
		var m mat4
		copy(m[:], n.Matrix)
		return m
	}
	t, r, sc := [3]float64{}, [4]float64{0, 0, 0, 1}, [3]float64{1, 1, 1}
	copy(t[:], n.Translation)
	copy(r[:], n.Rotation)
	copy(sc[:], n.Scale)
	x, y, z, w := r[0], r[1], r[2], r[3]
	return mat4{
		(1 - 2*(y*y+z*z)) * sc[0], 2 * (x*y + z*w) * sc[0], 2 * (x*z - y*w) * sc[0], 0,
		2 * (x*y - z*w) * sc[1], (1 - 2*(x*x+z*z)) * sc[1], 2 * (y*z + x*w) * sc[1], 0,
		2 * (x*z + y*w) * sc[2], 2 * (y*z - x*w) * sc[2], (1 - 2*(x*x+y*y)) * sc[2], 0,
		t[0], t[1], t[2], 1,
	}
}

// gltfBounds unions the POSITION min/max boxes of every mesh instance in the
// default scene, transformed to world space; without scenes, every root node
// is used.
func gltfBounds(doc *gltfDoc, s *ModelStats) {
	var roots []int
	switch {
	case doc.Scene != nil && *doc.Scene >= 0 && *doc.Scene < len(doc.Scenes):
		roots = doc.Scenes[*doc.Scene].Nodes
	case len(doc.Scenes) > 0:
		roots = doc.Scenes[0].Nodes
	default:
		child := map[int]bool{}
		for _, n := range doc.Nodes {
			for _, c := range n.Children {
				child[c] = true
			}
		}
		for i := range doc.Nodes {
			if !child[i] {
				roots = append(roots, i)
			}
		}
	}
	visited := map[int]bool{}
	var walk func(i int, parent mat4)
	walk = func(i int, parent mat4) {
		if i < 0 || i >= len(doc.Nodes) || visited[i] {
			return
		}
		visited[i] = true
		n := doc.Nodes[i]
		world := parent.mul(n.local())
		if n.Mesh != nil && *n.Mesh >= 0 && *n.Mesh < len(doc.Meshes) {
			for _, p := range doc.Meshes[*n.Mesh].Primitives {
				idx, ok := p.Attributes["POSITION"]
				if !ok || idx < 0 || idx >= len(doc.Accessors) {
					continue
				}
				a := doc.Accessors[idx]
				if len(a.Min) != 3 || len(a.Max) != 3 {
					continue
				}
				for corner := range 8 {
					var c [3]float64
					for axis := range 3 {
						c[axis] = a.Min[axis]
						if corner&(1<<axis) != 0 {
							c[axis] = a.Max[axis]
						}
					}
					s.include(world.apply(c))
				}
			}
		}
		for _, c := range n.Children {
			walk(c, world)
		}
	}
	for _, r := range roots {
		walk(r, identity)
	}
}

func decodeOBJ(data []byte) (*ModelStats, error) {
	s := &ModelStats{Format: "obj", Materials: []string{}, Animations: []string{}, Dependencies: []string{}}
	materials := map[string]bool{}
	objects := 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		switch key {
		case "v":
			f := strings.Fields(rest)
			if len(f) < 3 {
				return nil, fmt.Errorf("%w: vertex %q", errModel, line)
			}
			var p [3]float64
			for i := range 3 {
				v, err := strconv.ParseFloat(f[i], 64)
				if err != nil {
					return nil, fmt.Errorf("%w: vertex %q", errModel, line)
				}
				p[i] = v
			}
			s.Vertices++
			s.include(p)
		case "f":
			if n := len(strings.Fields(rest)); n >= 3 {
				s.Triangles += n - 2
			}
		case "o", "g":
			objects++
		case "usemtl":
			if rest != "" && !materials[rest] {
				materials[rest] = true
				s.Materials = append(s.Materials, rest)
			}
		case "mtllib":
			for _, lib := range strings.Fields(rest) {
				s.addDependency(lib)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", errModel, err)
	}
	s.Meshes = max(objects, min(s.Vertices, 1))
	s.Primitives = s.Meshes
	return s, nil
}

// MaterialLibraryDependencies lists the texture files an OBJ material library
// (.mtl) references; texture options before the file name are skipped.
func MaterialLibraryDependencies(data []byte) []string {
	s := &ModelStats{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		if strings.HasPrefix(key, "map_") || key == "bump" || key == "disp" || key == "decal" || key == "norm" || key == "refl" {
			s.addDependency(fields[len(fields)-1])
		}
	}
	return s.Dependencies
}
