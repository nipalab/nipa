package preview

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

const sampleGLTF = `{
  "asset": {"version": "2.0", "generator": "Blender 4.2"},
  "scene": 0,
  "scenes": [{"nodes": [0, 3]}],
  "nodes": [
    {"translation": [10, 0, 0], "children": [1]},
    {"mesh": 0, "scale": [2, 2, 2], "rotation": [0, 0.7071067811865476, 0, 0.7071067811865476]},
    {"mesh": 1},
    {"mesh": 1, "matrix": [1,0,0,0, 0,1,0,0, 0,0,1,0, 0,-5,0,1]}
  ],
  "meshes": [
    {"primitives": [
      {"attributes": {"POSITION": 0}, "indices": 1, "targets": [{"POSITION": 0}, {"POSITION": 0}]},
      {"attributes": {"POSITION": 0}, "mode": 5},
      {"attributes": {"POSITION": 0}, "mode": 1}
    ]},
    {"primitives": [{"attributes": {"POSITION": 2}}, {"attributes": {"NORMAL": 2}}, {"attributes": {"POSITION": 99}}]}
  ],
  "accessors": [
    {"count": 8, "min": [-1, -1, -1], "max": [1, 1, 1]},
    {"count": 36},
    {"count": 6, "min": [0, 0, 0], "max": [1, 2, 3]}
  ],
  "materials": [{"name": "Skin"}, {}],
  "textures": [{}, {}],
  "images": [{"uri": "tex/albedo%20map.png"}, {"uri": "data:image/png;base64,AAAA"}, {"bufferView": 3}],
  "buffers": [{"uri": "hero.bin"}, {"uri": "https://example.com/x.bin"}, {"uri": "hero.bin"}],
  "skins": [{"joints": [1, 2]}, {"joints": [2, 3]}],
  "animations": [{"name": "Run"}, {}]
}`

func TestDecodeGLTFStats(t *testing.T) {
	s, err := DecodeModel("hero.gltf", []byte(sampleGLTF))
	require.NoError(t, err)
	require.Equal(t, "gltf", s.Format)
	require.Equal(t, "Blender 4.2", s.Generator)
	require.Equal(t, 2, s.Meshes)
	require.Equal(t, 6, s.Primitives)
	require.Equal(t, 8+8+8+6, s.Vertices)
	require.Equal(t, 12+6+2, s.Triangles)
	require.Equal(t, 2, s.MorphTargets)
	require.Equal(t, []string{"Skin", "Material 2"}, s.Materials)
	require.Equal(t, []string{"Run", "Animation 2"}, s.Animations)
	require.Equal(t, 2, s.Textures)
	require.Equal(t, 4, s.Nodes)
	require.Equal(t, 2, s.Skins)
	require.Equal(t, 3, s.Joints)
	require.Equal(t, []string{"hero.bin", "tex/albedo map.png"}, s.Dependencies)
	require.True(t, s.HasBounds)
	for i, want := range [3]float64{0, -5, -2} {
		require.InDelta(t, want, s.Min[i], 1e-9, "min %d", i)
	}
	for i, want := range [3]float64{12, 2, 3} {
		require.InDelta(t, want, s.Max[i], 1e-9, "max %d", i)
	}
}

func TestDecodeGLTFSceneFallbacks(t *testing.T) {
	noScenes := `{"asset":{"version":"2.0"},"nodes":[{"mesh":0,"children":[1]},{"mesh":0,"translation":[0,0,4]},{"children":[0]}],
	  "meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}],"accessors":[{"count":3,"min":[0,0,0],"max":[1,1,1]}]}`
	s, err := DecodeModel("a.gltf", []byte(noScenes))
	require.NoError(t, err)
	require.Equal(t, [3]float64{1, 1, 5}, s.Max)

	firstScene := `{"asset":{"version":"2.0"},"scenes":[{"nodes":[0]}],"scene":7,"nodes":[{"mesh":0,"translation":[3,0,0]}],
	  "meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}],"accessors":[{"count":3,"min":[0,0,0],"max":[1,1,1]}]}`
	s, err = DecodeModel("a.gltf", []byte(firstScene))
	require.NoError(t, err)
	require.Equal(t, [3]float64{3, 0, 0}, s.Min)

	empty := `{"asset":{"version":"2.0"},"nodes":[{"mesh":5},{"mesh":0}],"meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}],"accessors":[{"count":3}]}`
	s, err = DecodeModel("a.gltf", []byte(empty))
	require.NoError(t, err)
	require.False(t, s.HasBounds)
	require.Equal(t, 1, s.Triangles)
}

func glbFile(json string) []byte {
	pad := (4 - len(json)%4) % 4
	body := json + string(make([]byte, pad))
	b := binary.LittleEndian.AppendUint32(nil, glbMagic)
	b = binary.LittleEndian.AppendUint32(b, 2)
	b = binary.LittleEndian.AppendUint32(b, uint32(20+len(body)))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(body)))
	b = binary.LittleEndian.AppendUint32(b, glbChunkJSON)
	return append(b, body...)
}

func TestDecodeGLBAndErrors(t *testing.T) {
	s, err := DecodeModel("hero.GLB", glbFile(sampleGLTF))
	require.NoError(t, err)
	require.Equal(t, "glb", s.Format)
	require.Equal(t, 30, s.Vertices)

	bad := glbFile(sampleGLTF)
	binary.LittleEndian.PutUint32(bad[12:], 1<<30)
	for name, data := range map[string][]byte{
		"magic":   []byte("glTF but not really here"),
		"length":  bad,
		"chunk":   append(glbFile("{}")[:16], 1, 2, 3, 4),
		"json":    glbFile("{"),
		"version": glbFile(`{"asset":{"version":"1.0"}}`),
	} {
		_, err := DecodeModel("a.glb", data)
		require.Error(t, err, name)
	}
	_, err = DecodeModel("a.glb", glbFile(`{"asset":{"version":"1.0"}}`))
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = DecodeModel("a.fbx", []byte("Kaydara FBX Binary"))
	require.ErrorIs(t, err, ErrUnsupported)
	require.ErrorContains(t, err, "fbx model statistics")
}

func TestDecodeOBJ(t *testing.T) {
	obj := "# exported\nmtllib hero.mtl extra.mtl\no Hero\nv 0 0 0\nv 1 2 3\nv -1 0.5 0\nv 0 0 1\nvt 0 0\nvn 0 1 0\nusemtl Skin\nf 1 2 3\nf 1/1/1 2/1/1 3/1/1 4/1/1\nusemtl Skin\ng Hat\nusemtl Cloth\nf 1 2\n"
	s, err := DecodeModel("hero.obj", []byte(obj))
	require.NoError(t, err)
	require.Equal(t, 4, s.Vertices)
	require.Equal(t, 3, s.Triangles)
	require.Equal(t, 2, s.Meshes)
	require.Equal(t, []string{"Skin", "Cloth"}, s.Materials)
	require.Equal(t, []string{"hero.mtl", "extra.mtl"}, s.Dependencies)
	require.Equal(t, [3]float64{-1, 0, 0}, s.Min)
	require.Equal(t, [3]float64{1, 2, 3}, s.Max)

	s, err = DecodeModel("tri.obj", []byte("v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n"))
	require.NoError(t, err)
	require.Equal(t, 1, s.Meshes)
	s, err = DecodeModel("none.obj", nil)
	require.NoError(t, err)
	require.Equal(t, 0, s.Meshes)

	for _, bad := range []string{"v 1 2\n", "v 1 x 3\n"} {
		_, err := DecodeModel("a.obj", []byte(bad))
		require.Error(t, err)
	}
	_, err = DecodeModel("a.obj", append([]byte("v "), make([]byte, 17<<20)...))
	require.Error(t, err)
}

func TestMaterialLibraryDependencies(t *testing.T) {
	mtl := "newmtl Skin\nKd 1 1 1\nmap_Kd -s 1 1 1 textures/skin.png\nmap_Bump skin_n.png\nbump skin_n.png\nnorm n.png\ndisp\nmap_d data:x\n"
	require.Equal(t, []string{"textures/skin.png", "skin_n.png", "n.png"}, MaterialLibraryDependencies([]byte(mtl)))
	require.Empty(t, MaterialLibraryDependencies(nil))
}
