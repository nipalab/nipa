package godotmeta

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUIDSamples(t *testing.T) {
	cases := map[string]string{
		"icon.svg.import":    "uid://cvdk8a1nd4yxb",
		"music.ogg.import":   "uid://b7t0rq3hyxm2e",
		"hero.glb.import":    "uid://dxq2l5w0j8k3n",
		"legacy.wav.import":  "",
		"player.gd.uid":      "uid://bq4m1x7c2v8kd",
		"water.gdshader.uid": "uid://c5n8r2t6y0w3h",
		"Enemy.cs.uid":       "uid://dk3j7h1g5f9s2",
	}
	for name, want := range cases {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		require.NoError(t, err)
		assert.Equal(t, want, UID(data), name)
	}
}

func TestUIDEdgeCases(t *testing.T) {
	assert.Equal(t, "uid://abc", UID([]byte("\ufeffuid://abc\r\n")))
	assert.Equal(t, "", UID(nil))
	assert.Equal(t, "", UID([]byte("uid://")))
	assert.Equal(t, "", UID([]byte("uid://bad-chars")))
	assert.Equal(t, "", UID([]byte("res://player.gd")))
	assert.Equal(t, "", UID([]byte("uid://abc\nuid://def\n")))
	assert.Equal(t, "", UID([]byte("[deps]\nuid=\"uid://abc\"\n")), "only [remap] counts")
	assert.Equal(t, "uid://abc", UID([]byte("[remap]\r\nimporter=\"texture\"\r\nuid = \"uid://abc\"\r\n[deps]\r\nuid=\"uid://zzz\"\r\n")))
	assert.Equal(t, "", UID([]byte("[remap]\nuid=\"uid://<invalid>\"\n")))
}

func TestExtensions(t *testing.T) {
	for _, ext := range []string{".png", "PNG", ".jpg", ".jpeg", ".webp", ".svg", ".bmp", ".tga", ".exr", ".hdr", ".dds", ".ktx", ".ktx2",
		".wav", ".ogg", ".mp3", ".glb", ".gltf", ".fbx", ".blend", ".obj", ".dae", ".ttf", ".otf", ".woff", ".woff2", ".fnt", ".font", ".csv"} {
		assert.True(t, Importable(ext), ext)
		assert.False(t, HasUIDFile(ext), ext)
	}
	for _, ext := range []string{".gd", ".cs", ".gdshader", ".gdshaderinc"} {
		assert.True(t, HasUIDFile(ext), ext)
		assert.False(t, Importable(ext), ext)
	}
	for _, ext := range []string{".tscn", ".tres", ".txt", ""} {
		assert.False(t, Importable(ext) || HasUIDFile(ext), ext)
	}
}

func TestSidecars(t *testing.T) {
	assert.Equal(t, []string{"art/a.png.import", "art/a.png.uid"}, Sidecars("art/a.png"))
	sc, ok := Sidecar("art/a.PNG")
	assert.True(t, ok)
	assert.Equal(t, "art/a.PNG.import", sc)
	sc, ok = Sidecar("player.gd")
	assert.True(t, ok)
	assert.Equal(t, "player.gd.uid", sc)
	_, ok = Sidecar("main.tscn")
	assert.False(t, ok)

	assert.True(t, IsSidecar("a/b.png.import"))
	assert.True(t, IsSidecar("b.gd.uid"))
	assert.False(t, IsSidecar("a/.import"))
	assert.False(t, IsSidecar("a/b.png"))
	assert.Equal(t, "a/b.png", AssetOf("a/b.png.import"))
	assert.Equal(t, "b.gd", AssetOf("b.gd.uid"))
	assert.Equal(t, "dir/c.png", AssetOf("dir/c.png"))
}
