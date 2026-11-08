package preview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func godotFixture(t *testing.T, name string) *Scene {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "godot", name))
	require.NoError(t, err)
	s, err := ParseGodotResource(data)
	require.NoError(t, err)
	return s
}

func godotObject(s *Scene, id string) *SceneObject {
	for i := range s.Objects {
		if s.Objects[i].ID == id {
			return &s.Objects[i]
		}
	}
	return nil
}

func TestParseGodotScene(t *testing.T) {
	s := godotFixture(t, "level.tscn")
	assert.Equal(t, "godot", s.Engine)
	assert.Equal(t, []string{"res://actors/enemy.tscn", "res://art/tiles.png", "res://scripts/level.gd"}, s.Refs)

	root := godotObject(s, ".")
	require.NotNil(t, root)
	assert.Equal(t, SceneObject{ID: ".", Path: "Level", Type: "Node2D", Props: map[string]string{
		"name": "Level", "script": "res://scripts/level.gd", "metadata/_edit_lock_": "true",
	}}, *root)

	shape := godotObject(s, "Floor/Body/Shape")
	require.NotNil(t, shape)
	assert.Equal(t, "Level/Floor/Body/Shape", shape.Path)
	assert.Equal(t, "Floor/Body", shape.Parent)
	assert.Equal(t, "sub:RectangleShape2D#RectangleShape2D_ab12c", shape.Props["shape"])

	floor := godotObject(s, "Floor")
	assert.Equal(t, ".", floor.Parent)
	assert.Equal(t, `["solid", "ground"]`, floor.Props["groups"])
	assert.Equal(t, "res://art/tiles.png", floor.Props["texture"])

	enemy := godotObject(s, "Enemy")
	assert.Equal(t, "res://actors/enemy.tscn", enemy.Type)
	assert.Equal(t, "4.5", enemy.Props["speed"])

	label := godotObject(s, "Label")
	assert.Equal(t, "\"Hello \\\"world\\\"\nsecond line ; not a comment\"", label.Props["text"])

	sub := godotObject(s, "sub:RectangleShape2D#RectangleShape2D_ab12c")
	require.NotNil(t, sub)
	assert.Equal(t, "RectangleShape2D", sub.Type)
	assert.Equal(t, "Vector2(32, 48)", sub.Props["size"])

	conn := godotObject(s, `connection:connection signal="body_entered" from="Enemy" to="." method="_on_enemy_hit"`)
	require.NotNil(t, conn)
	assert.Equal(t, "body_entered Enemy → Level._on_enemy_hit", conn.Path)
	assert.Equal(t, map[string]string{"flags": "3"}, conn.Props)
	assert.Equal(t, map[string]string{"binds": `[1, "a"]`}, godotObject(s, `connection:connection signal="ready" from="." to="Floor" method="_on_ready"`).Props)

	assert.NotNil(t, godotObject(s, "editable:Enemy"))
}

func TestDiffGodotScenes(t *testing.T) {
	d := DiffScenes(godotFixture(t, "level_old.tscn"), godotFixture(t, "level.tscn"))
	assert.Equal(t, "godot", d.Engine)
	assert.Equal(t, 5, d.Added)
	assert.Equal(t, 4, d.Removed)
	assert.Equal(t, 2, d.Changed, "renumbered ext_resource ids are not changes")
	assert.Equal(t, []RefChange{{Path: "res://actors/enemy.tscn", Status: "added"}, {Path: "res://art/old_bg.png", Status: "removed"}}, d.Refs)

	byID := map[string]SceneChange{}
	for _, c := range d.Changes {
		byID[c.ID] = c
	}
	assert.Equal(t, []PropChange{{Key: "groups", Old: `["solid"]`, New: `["solid", "ground"]`}}, byID["Floor"].Props)
	assert.Equal(t, []PropChange{{Key: "size", Old: "Vector2(32, 32)", New: "Vector2(32, 48)"}}, byID["sub:RectangleShape2D#RectangleShape2D_ab12c"].Props)
	assert.Equal(t, "removed", byID["Background"].Status)
	assert.Equal(t, "added", byID["Floor/Body/Shape"].Status)
	assert.Equal(t, "removed", byID["Body/Shape"].Status)
	assert.Contains(t, d.Objects, SceneObject{ID: ".", Path: "Level", Type: "Node2D"})
}

func TestParseGodotResourceAndConfig(t *testing.T) {
	mat := godotFixture(t, "material.tres")
	require.Len(t, mat.Objects, 1)
	assert.Equal(t, "resource", mat.Objects[0].ID)
	assert.Equal(t, "StandardMaterial3D", mat.Objects[0].Path)
	assert.Equal(t, "res://art/rock_albedo.png", mat.Objects[0].Props["albedo_texture"])
	assert.Equal(t, []string{"res://art/rock_albedo.png"}, mat.Refs)

	proj := godotFixture(t, "project.godot")
	assert.Equal(t, "(global)", proj.Objects[0].Path)
	assert.Equal(t, "5", proj.Objects[0].Props["config_version"])
	app := godotObject(proj, "section:application")
	require.NotNil(t, app)
	assert.Equal(t, "[application]", app.Path)
	assert.Equal(t, `"Starfall"`, app.Props["config/name"])

	imp := godotFixture(t, "icon.svg.import")
	assert.Equal(t, `"texture"`, godotObject(imp, "section:remap").Props["importer"])
	assert.Empty(t, imp.Refs)

	plain, err := ParseGodotResource([]byte("[resource]\nx = 1\n"))
	require.NoError(t, err)
	assert.Equal(t, "Resource", plain.Objects[0].Type)
}

func TestParseGodotEdgeCases(t *testing.T) {
	s, err := ParseGodotResource([]byte(`[ext_resource type="Script" uid="uid://abc" id="1"]
[ext_resource type="Script" id="2"]
[sub_resource type="Gradient" id="g"]
[node name="Root" type="Node"]
a = ExtResource("1")
b = [ExtResource( "1" ), SubResource("g"), ExtResource("2"), ExtResource("9"), MyExtResource("1")]
c = "ExtResource(\"1\")"
[node name="X" parent="."]
[node name="X" parent="."]
[node name="Orphan" parent="Missing/Deep"]
[connection signal="s" from="." to="X" method="m"]
`))
	require.NoError(t, err)
	root := godotObject(s, ".")
	assert.Equal(t, "uid://abc", root.Props["a"])
	assert.Equal(t, `[uid://abc, sub:Gradient#g, ExtResource("2"), ExtResource("9"), MyExtResource("1")]`, root.Props["b"])
	assert.Equal(t, `"ExtResource(\"1\")"`, root.Props["c"])
	assert.Equal(t, "inherited", godotObject(s, "X").Type)
	assert.NotNil(t, godotObject(s, "X~2"), "duplicate node paths stay distinct")
	assert.Equal(t, "Root/Missing/Deep/Orphan", godotObject(s, "Missing/Deep/Orphan").Path)
	assert.Equal(t, []string{"uid://abc"}, s.Refs)
	assert.Equal(t, "s Root → X.m", s.Objects[len(s.Objects)-1].Path)

	headless, err := ParseGodotResource([]byte("[node name=\"A\" parent=\".\"]\n"))
	require.NoError(t, err)
	assert.Equal(t, "A", headless.Objects[0].Path)

	_, err = ParseGodotResource([]byte("[node name=\"a\"\n"))
	assert.ErrorIs(t, err, errScene)
	assert.ErrorContains(t, err, "line 1")

	data, err := os.ReadFile(filepath.Join("testdata", "godot", "binary.scn"))
	require.NoError(t, err)
	_, err = ParseGodotResource(data)
	assert.ErrorIs(t, err, ErrUnsupported)

	bom, err := ParseGodotResource(append([]byte{0xef, 0xbb, 0xbf}, "[resource]\n"...))
	require.NoError(t, err)
	assert.Len(t, bom.Objects, 1)
}

func TestParseGodotLimits(t *testing.T) {
	objs, props := MaxSceneObjects, MaxSceneProps
	t.Cleanup(func() { MaxSceneObjects, MaxSceneProps = objs, props })

	MaxSceneObjects = 1
	_, err := ParseGodotResource([]byte("[a]\n[b]\n"))
	assert.ErrorIs(t, err, errSceneTooLarge)

	MaxSceneObjects, MaxSceneProps = objs, 1
	_, err = ParseGodotResource([]byte("[a]\nx = 1\ny = 2\n"))
	assert.ErrorIs(t, err, errSceneTooLarge)
	_, err = ParseGodotResource([]byte("[node name=\"A\" type=\"Node\"]\nx = 1\n"))
	assert.ErrorIs(t, err, errSceneTooLarge)
}

func TestParseGodotResource_DuplicateIDs(t *testing.T) {
	var b strings.Builder
	b.WriteString("[node name=\"Root\" type=\"Node\"]\n[node name=\"X~3\" parent=\".\"]\n")
	for range 5000 {
		b.WriteString("[node name=\"X\" parent=\".\"]\n")
	}
	s, err := ParseGodotResource([]byte(b.String()))
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, o := range s.Objects {
		require.False(t, seen[o.ID], "duplicate id %q", o.ID)
		seen[o.ID] = true
	}
	assert.Len(t, s.Objects, 5002)
	assert.True(t, seen["X~2"])
	assert.True(t, seen["X~4"])
	assert.True(t, seen["X~5000"])
}
