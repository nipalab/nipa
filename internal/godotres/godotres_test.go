package godotres

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func load(t *testing.T, name string) (*File, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	f, err := Parse(data)
	require.NoError(t, err)
	return f, data
}

func TestRoundTripFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, name := range files {
		f, data := load(t, filepath.Base(name))
		assert.Equal(t, string(data), string(Write(f)), name)
	}
}

func TestParseScene(t *testing.T) {
	f, _ := load(t, "level.tscn")
	require.NotNil(t, f.Header)
	assert.Equal(t, "gd_scene", f.Header.Kind)
	assert.Equal(t, `"uid://c4k2v8d1h7xqa"`, must(f.Header.Attr("uid")))
	assert.Empty(t, f.Props)

	kinds := map[string]int{}
	for _, s := range f.Sections {
		kinds[s.Kind]++
	}
	assert.Equal(t, map[string]int{"ext_resource": 3, "sub_resource": 2, "node": 7, "connection": 2, "editable": 1}, kinds)

	curve := f.Sections[4]
	assert.Equal(t, "sub_resource id=\"Curve2D_p4th0\"", curve.Identity())
	assert.Equal(t, "{\n\"points\": PackedVector2Array(0, 0, 0, 0, 10, 20,\n0, 0, 0, 0, 100, 40),\n\"tilts\": PackedFloat32Array(0, 0)\n}", must(curve.Prop("_data")))
	assert.Equal(t, "2", must(curve.Prop("point_count")))

	floor := f.Sections[6]
	assert.Equal(t, `node name="Floor" parent="."`, floor.Identity())
	assert.Equal(t, `["solid", "ground"]`, must(floor.Attr("groups")))
	assert.Equal(t, "Floor", floor.Value("name"))

	root := f.Sections[5]
	assert.Equal(t, `node name="Level"`, root.Identity())
	assert.Equal(t, "true", must(root.Prop("metadata/_edit_lock_")))
	_, ok := root.Prop("missing")
	assert.False(t, ok)

	label := f.Sections[11]
	assert.Equal(t, "Hello \"world\"\nsecond line ; not a comment", Unquote(must(label.Prop("text"))))

	enemy := f.Sections[10]
	assert.Equal(t, `ExtResource("2_en3my")`, must(enemy.Attr("instance")))

	conn := f.Sections[12]
	assert.Equal(t, `connection signal="body_entered" from="Enemy" to="." method="_on_enemy_hit"`, conn.Identity())
	assert.Equal(t, `[1, "a"]`, must(f.Sections[13].Attr("binds")))
	assert.Equal(t, `editable path="Enemy"`, f.Sections[14].Identity())
}

func TestParseConfigFiles(t *testing.T) {
	f, _ := load(t, "project.godot")
	assert.Nil(t, f.Header)
	require.Len(t, f.Props, 1)
	assert.Equal(t, Prop{Key: "config_version", Raw: "5"}, Prop{Key: f.Props[0].Key, Raw: f.Props[0].Raw})
	require.Len(t, f.Sections, 4)
	assert.Equal(t, "application", f.Sections[0].Identity())
	assert.Equal(t, `"*res://scripts/game.gd"`, must(f.Sections[1].Prop("Game")))
	assert.Contains(t, must(f.Sections[2].Prop("jump")), `"physical_keycode":32`)

	imp, _ := load(t, "icon.svg.import")
	assert.Equal(t, "remap", imp.Sections[0].Kind)
	assert.Equal(t, "{\n\"vram_texture\": false\n}", must(imp.Sections[0].Prop("metadata")))
	assert.Equal(t, "1.0", must(imp.Sections[2].Prop("svg/scale")))

	res, _ := load(t, "material.tres")
	assert.Equal(t, "gd_resource", res.Header.Identity())
	assert.Equal(t, "resource", res.Sections[1].Identity())
}

func TestWriteEdits(t *testing.T) {
	f, err := Parse([]byte("[gd_scene format=3]\n\n[node name=\"A\" type=\"Node\"]\nx = 1 ; one\ny  =  2\n"))
	require.NoError(t, err)
	node := f.Sections[0]
	node.Props[1].Raw = "3"
	node.Props = append(node.Props, Prop{Key: "z", Raw: `"new"`})
	f.Header.Attrs = append(f.Header.Attrs, Attr{Key: "load_steps", Raw: "2"})
	f.Sections = append(f.Sections, &Section{Kind: "connection", Attrs: []Attr{{"signal", `"s"`}, {"from", `"."`}, {"to", `"."`}, {"method", `"m"`}}})
	assert.Equal(t, "[gd_scene format=3 load_steps=2]\n\n[node name=\"A\" type=\"Node\"]\nx = 1 ; one\ny  =  3\nz = \"new\"\n\n[connection signal=\"s\" from=\".\" to=\".\" method=\"m\"]\n", string(Write(f)))

	empty := &File{Sections: []*Section{{Kind: "resource", Props: []Prop{{Key: "a", Raw: "1"}}}}}
	assert.Equal(t, "[resource]\na = 1\n", string(Write(empty)))
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no value":            "a =\n",
		"no equals":           "[resource]\njust words\n",
		"empty key":           " = 1\n",
		"unterminated string": "a = \"abc\n",
		"unterminated array":  "a = [1, 2\n",
		"unbalanced":          "a = 1)\n",
		"trailing text":       "[resource] x\n",
		"open header":         "[node name=\"a\"\n",
		"header eof":          "[node",
		"nameless section":    "[]\n",
		"bad attribute":       "[node name]\n",
		"quoted key eof":      "\"key",
		"key at eof":          "key",
	}
	for name, in := range cases {
		_, err := Parse([]byte(in))
		assert.ErrorIs(t, err, ErrSyntax, name)
	}
	_, err := Parse([]byte("\n\n[resource]\nx = [\n"))
	assert.ErrorContains(t, err, "line 4")
}

func TestParseLenient(t *testing.T) {
	for _, in := range []string{"", "\n", "; only a comment", "# hash comment\n", "[a]\n\"quoted key\"=1\n", "[a] ; note\nb=1", "  [a]\n\tb = 1\n"} {
		f, err := Parse([]byte(in))
		require.NoError(t, err, in)
		assert.Equal(t, in, string(Write(f)))
	}
	f, err := Parse([]byte("[a]\n\"quoted key\"=1\n"))
	require.NoError(t, err)
	assert.Equal(t, `"quoted key"`, f.Sections[0].Props[0].Key)
	assert.Equal(t, "a", f.Sections[0].Identity())
}

func TestUnquote(t *testing.T) {
	cases := map[string]string{
		`"plain"`:            "plain",
		`&"name"`:            "name",
		`^"Path/To"`:         "Path/To",
		`"a\nb\tc\rd\"\\"`:   "a\nb\tc\rd\"\\",
		`"é\U01F600"`:        "é😀",
		`"\uZZ"`:             "uZZ",
		`"\u12"`:             "u12",
		`"x\"`:               `x\`,
		`ExtResource("1_a")`: `ExtResource("1_a")`,
		`"`:                  `"`,
		`42`:                 "42",
	}
	for in, want := range cases {
		assert.Equal(t, want, Unquote(in), in)
	}
}

func must(v string, ok bool) string {
	if !ok {
		panic("missing")
	}
	return v
}
