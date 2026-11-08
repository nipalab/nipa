package godotres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(data)
}

func edit(t *testing.T, s string, pairs ...string) string {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		require.Contains(t, s, pairs[i])
		s = strings.Replace(s, pairs[i], pairs[i+1], 1)
	}
	return s
}

func merge3(t *testing.T, base, ours, theirs string) (string, []Conflict) {
	t.Helper()
	out, conflicts, err := Merge3([]byte(base), []byte(ours), []byte(theirs))
	require.NoError(t, err)
	_, err = Parse(out)
	require.NoError(t, err, "merged output must re-parse")
	return string(out), conflicts
}

func clean(t *testing.T, base, ours, theirs string) string {
	t.Helper()
	out, conflicts := merge3(t, base, ours, theirs)
	require.Empty(t, conflicts)
	assert.NotContains(t, out, "<<<<<<<")
	return out
}

func TestMerge3IdentityAndOneSided(t *testing.T) {
	for _, name := range fixtureNames(t) {
		data := fixture(t, filepath.Base(name))
		assert.Equal(t, data, clean(t, data, data, data), name)
	}
	base := fixture(t, "player_4_4.tscn")
	ours := edit(t, base, "speed = 180.0", "speed = 200.0")
	assert.Equal(t, ours, clean(t, base, ours, base))
	assert.Equal(t, ours, clean(t, base, base, ours))
}

func TestMerge3DisjointNodeEdits(t *testing.T) {
	base := fixture(t, "player_4_4.tscn")
	ours := edit(t, base, "speed = 180.0", "speed = 210.0", "zoom = Vector2(2, 2)", "zoom = Vector2(3, 3)")
	theirs := edit(t, base, "hframes = 8", "hframes = 10", "volume_db = -6.0\n", "")
	out := clean(t, base, ours, theirs)
	assert.Contains(t, out, "speed = 210.0")
	assert.Contains(t, out, "zoom = Vector2(3, 3)")
	assert.Contains(t, out, "hframes = 10")
	assert.NotContains(t, out, "volume_db")
	assert.Equal(t, edit(t, ours, "hframes = 8", "hframes = 10", "volume_db = -6.0\n", ""), out)
}

func TestMerge3BothAddNodesUnderSameParent(t *testing.T) {
	base := fixture(t, "hud_current.tscn")
	anchor := "[node name=\"Pause\""
	ours := edit(t, base, anchor, "[node name=\"Hearts\" type=\"Label\" parent=\"TopBar/Row\" unique_id=11]\nlayout_mode = 2\ntext = \"3\"\n\n"+anchor)
	theirs := edit(t, base, anchor, "[node name=\"Timer\" type=\"Label\" parent=\"TopBar/Row\" unique_id=12]\nlayout_mode = 2\ntext = \"00:00\"\n\n"+anchor)
	out := clean(t, base, ours, theirs)
	hearts, timer, pause := strings.Index(out, `name="Hearts"`), strings.Index(out, `name="Timer"`), strings.Index(out, `name="Pause"`)
	require.True(t, hearts > 0 && timer > 0)
	assert.Less(t, timer, hearts, "theirs' addition follows its predecessor Coins")
	assert.Less(t, hearts, pause)
	assert.Contains(t, out, "\n\n[node name=\"Timer\"")

	same := clean(t, base, ours, ours)
	assert.Equal(t, ours, same)
}

func TestMerge3RenumbersCollidingExtResources(t *testing.T) {
	base := fixture(t, "player_4_4.tscn")
	at := "\n[sub_resource type=\"CapsuleShape2D\""
	ours := edit(t, base,
		"load_steps=9", "load_steps=10",
		at, "[ext_resource type=\"Texture2D\" path=\"res://fx/dust.png\" id=\"5_n3w01\"]\n"+at,
		"[node name=\"HUD\" type=\"CanvasLayer\" parent=\".\"]\n", "[node name=\"Dust\" type=\"Sprite2D\" parent=\".\"]\ntexture = ExtResource(\"5_n3w01\")\n\n[node name=\"HUD\" type=\"CanvasLayer\" parent=\".\"]\n")
	theirs := edit(t, base,
		"load_steps=9", "load_steps=11",
		at, "[ext_resource type=\"AudioStream\" path=\"res://audio/sfx/land.wav\" id=\"5_n3w01\"]\n[ext_resource type=\"Texture2D\" path=\"res://fx/dust.png\" id=\"6_dup00\"]\n"+at,
		"bus = &\"SFX\"\n", "bus = &\"SFX\"\n\n[node name=\"LandSound\" type=\"AudioStreamPlayer2D\" parent=\".\"]\nstream = ExtResource(\"5_n3w01\")\n\n[node name=\"Shadow\" type=\"Sprite2D\" parent=\".\"]\ntexture = ExtResource(\"6_dup00\")\n")
	out := clean(t, base, ours, theirs)
	assert.Contains(t, out, `path="res://fx/dust.png" id="5_n3w01"`)
	assert.Contains(t, out, `path="res://audio/sfx/land.wav" id="5_n3w01_2"`)
	assert.Contains(t, out, "stream = ExtResource(\"5_n3w01_2\")")
	assert.Contains(t, out, "[node name=\"Shadow\" type=\"Sprite2D\" parent=\".\"]\ntexture = ExtResource(\"5_n3w01\")")
	assert.NotContains(t, out, "6_dup00")
	assert.Equal(t, 1, strings.Count(out, "res://fx/dust.png"))
	assert.Contains(t, out, "load_steps=11", "ours had 10, the merge adds one resource")
}

func TestMerge3RenumbersSubResources(t *testing.T) {
	base := "[gd_scene load_steps=1 format=3]\n\n[node name=\"R\" type=\"Node2D\"]\n"
	ours := "[gd_scene load_steps=2 format=3]\n\n[sub_resource type=\"CircleShape2D\" id=\"S_1\"]\nradius = 4.0\n\n[node name=\"R\" type=\"Node2D\"]\n\n[node name=\"A\" type=\"CollisionShape2D\" parent=\".\"]\nshape = SubResource(\"S_1\")\n"
	theirs := "[gd_scene load_steps=2 format=3]\n\n[sub_resource type=\"CircleShape2D\" id=\"S_1\"]\nradius = 9.0\n\n[node name=\"R\" type=\"Node2D\"]\n\n[node name=\"B\" type=\"CollisionShape2D\" parent=\".\"]\nshape = SubResource( \"S_1\" )\n"
	out := clean(t, base, ours, theirs)
	assert.Contains(t, out, "[sub_resource type=\"CircleShape2D\" id=\"S_1_2\"]\nradius = 9.0")
	assert.Contains(t, out, "shape = SubResource( \"S_1_2\" )")
	assert.Contains(t, out, "load_steps=3")

	same := clean(t, base, ours, ours)
	assert.Equal(t, ours, same)
}

func TestMerge3DeleteVersusModify(t *testing.T) {
	base := fixture(t, "player_4_4.tscn")
	camera := "[node name=\"Camera2D\" type=\"Camera2D\" parent=\".\"]\nzoom = Vector2(2, 2)\nposition_smoothing_enabled = true\nlimit_left = 0\nlimit_top = -2000\n\n"
	deleted := edit(t, base, camera, "")
	modified := edit(t, base, "limit_top = -2000", "limit_top = -4000")

	out, conflicts := merge3(t, base, deleted, modified)
	require.Len(t, conflicts, 1)
	assert.Equal(t, Conflict{Section: `node name="Camera2D" parent="."`, Message: "removed in ours, changed in theirs"}, conflicts[0])
	assert.NotContains(t, out, "<<<<<<<")

	_, conflicts = merge3(t, base, modified, deleted)
	require.Len(t, conflicts, 1)
	assert.Equal(t, "changed in ours, removed in theirs", conflicts[0].Message)

	out = clean(t, base, deleted, base)
	assert.NotContains(t, out, "Camera2D")
	out = clean(t, base, base, deleted)
	assert.NotContains(t, out, "Camera2D")
}

func TestMerge3SameKeyChanges(t *testing.T) {
	base := fixture(t, "player_4_4.tscn")
	ours := edit(t, base, "speed = 180.0", "speed = 200.0")
	assert.Equal(t, ours, clean(t, base, ours, ours), "same edit on both sides is clean")

	theirs := edit(t, base, "speed = 180.0", "speed = 250.0")
	out, conflicts := merge3(t, base, ours, theirs)
	require.Equal(t, []Conflict{{Section: `node name="Player"`, Key: "speed", Message: "changed differently on both sides: ours 200.0, theirs 250.0"}}, conflicts)
	assert.Contains(t, out, "speed = 200.0")

	ours = edit(t, base, "collision_mask = 5\n", "")
	theirs = edit(t, base, "collision_mask = 5", "collision_mask = 7")
	_, conflicts = merge3(t, base, ours, theirs)
	require.Len(t, conflicts, 1)
	assert.Equal(t, "collision_mask", conflicts[0].Key)
	assert.Equal(t, "removed in ours, changed in theirs", conflicts[0].Message)
	_, conflicts = merge3(t, base, theirs, ours)
	require.Len(t, conflicts, 1)
	assert.Equal(t, "changed in ours, removed in theirs", conflicts[0].Message)

	ours = edit(t, base, "collision_mask = 5\n", "collision_mask = 5\nmotion_mode = 1\n")
	theirs = edit(t, base, "collision_mask = 5\n", "collision_mask = 5\nmotion_mode = 0\n")
	_, conflicts = merge3(t, base, ours, theirs)
	require.Len(t, conflicts, 1)
	assert.Contains(t, conflicts[0].Message, "added differently on both sides")

	ours = edit(t, base, `groups=["player", "persist"]`, `groups=["player"]`)
	theirs = edit(t, base, `groups=["player", "persist"]`, `groups=["player", "saved"]`)
	_, conflicts = merge3(t, base, ours, theirs)
	require.Len(t, conflicts, 1)
	assert.Equal(t, "groups", conflicts[0].Key)

	ours = edit(t, base, "[node name=\"JumpSound\" type=\"AudioStreamPlayer2D\" parent=\".\"]", "[node name=\"JumpSound\" type=\"AudioStreamPlayer\" parent=\".\"]")
	theirs = edit(t, base, "volume_db = -6.0", "volume_db = -3.0")
	out = clean(t, base, ours, theirs)
	assert.Contains(t, out, "[node name=\"JumpSound\" type=\"AudioStreamPlayer\" parent=\".\"]\nstream = ExtResource(\"3_jmp0a\")\nvolume_db = -3.0")

	long := strings.Repeat("x", 80)
	ours = edit(t, base, "autoplay = \"idle\"", "autoplay = \""+long+"\"")
	theirs = edit(t, base, "autoplay = \"idle\"", "autoplay = \"run\"")
	_, conflicts = merge3(t, base, ours, theirs)
	require.Len(t, conflicts, 1)
	assert.Contains(t, conflicts[0].Message, "...")
}

func TestMerge3Connections(t *testing.T) {
	base := fixture(t, "player_4_4.tscn")
	ours := edit(t, base, "[editable path=", "[connection signal=\"body_entered\" from=\"Hitbox\" to=\".\" method=\"_on_body\"]\n\n[editable path=")
	theirs := edit(t, base, "[connection signal=\"animation_finished\" from=\"AnimationPlayer\" to=\".\" method=\"_on_animation_finished\"]\n", "")
	out := clean(t, base, ours, theirs)
	assert.Contains(t, out, "_on_body")
	assert.NotContains(t, out, "_on_animation_finished")

	theirs = edit(t, base, "flags=6", "flags=2")
	out = clean(t, base, ours, theirs)
	assert.Contains(t, out, "flags=2 binds")

	hitbox := "[node name=\"Hitbox\" type=\"Area2D\" parent=\".\" groups=[\"hurtbox\"]]\ncollision_layer = 0\ncollision_mask = 8\n\n[node name=\"Shape\" type=\"CollisionShape2D\" parent=\"Hitbox\"]\nshape = SubResource(\"CapsuleShape2D_k2j1w\")\n\n"
	removed := edit(t, base, hitbox, "", "[connection signal=\"area_entered\" from=\"Hitbox\" to=\".\" method=\"_on_hitbox_area_entered\"]\n", "")
	_, conflicts := merge3(t, base, removed, ours)
	require.Len(t, conflicts, 1)
	assert.Equal(t, "from", conflicts[0].Key)
	assert.Contains(t, conflicts[0].Message, "node Hitbox was removed")
}

func TestMerge3BrokenReferences(t *testing.T) {
	base := fixture(t, "player_4_4.tscn")
	hud := "[node name=\"HUD\" type=\"CanvasLayer\" parent=\".\"]\n\n[node name=\"HealthBar\" parent=\"HUD\" instance=ExtResource(\"4_hb4r1\")]\noffset_left = 16.0\noffset_top = 16.0\nmax_health = 5\n\n"
	ours := edit(t, base, hud, "", "[connection signal=\"value_changed\" from=\"HUD/HealthBar\" to=\".\" method=\"_on_health_changed\" flags=6 binds=[\"hud\"]]\n", "", "\n[editable path=\"HUD/HealthBar\"]\n", "")
	theirs := edit(t, base, "[node name=\"Hitbox\"", "[node name=\"Armor\" type=\"Label\" parent=\"HUD\"]\n\n[node name=\"Hitbox\"")
	_, conflicts := merge3(t, base, ours, theirs)
	require.Len(t, conflicts, 1)
	assert.Equal(t, Conflict{Section: `node name="Armor" parent="HUD"`, Key: "parent", Message: "parent node HUD was removed"}, conflicts[0])

	anim := "[sub_resource type=\"Animation\" id=\"Animation_run01\"]"
	start := strings.Index(base, anim)
	end := strings.Index(base, "[sub_resource type=\"AnimationLibrary\"")
	ours = edit(t, base, base[start:end], "", "&\"run\": SubResource(\"Animation_run01\")", "&\"walk\": SubResource(\"Animation_idle0\")", ",\n&\"walk\"", ",\n&\"walk\"")
	theirs = edit(t, base, "[node name=\"Hitbox\"", "[node name=\"Ghost\" type=\"AnimationPlayer\" parent=\".\"]\nlibraries = {\n&\"\": SubResource(\"Animation_run01\")\n}\n\n[node name=\"Hitbox\"")
	_, conflicts = merge3(t, base, ours, theirs)
	require.Len(t, conflicts, 1)
	assert.Equal(t, "libraries", conflicts[0].Key)
	assert.Contains(t, conflicts[0].Message, "sub_resource \"Animation_run01\"")

	instanced := edit(t, base, "[node name=\"Hitbox\"", "[node name=\"Fill\" type=\"ColorRect\" parent=\"HUD/HealthBar/Bar\"]\n\n[node name=\"Hitbox\"")
	clean(t, base, base, instanced)
}

func TestMerge3TwoRoots(t *testing.T) {
	base := "[gd_scene format=3]\n\n[node name=\"Main\" type=\"Node\"]\n"
	ours := "[gd_scene format=3]\n\n[node name=\"Game\" type=\"Node\"]\n"
	theirs := "[gd_scene format=3]\n\n[node name=\"Main\" type=\"Node\"]\n\n[node name=\"Child\" type=\"Node\" parent=\".\"]\n"
	_, conflicts := merge3(t, base, ours, theirs)
	require.Len(t, conflicts, 1, "the child lost its anchor and would load before the root")
	assert.Equal(t, Conflict{Section: `node name="Child" parent="."`, Key: "parent", Message: "the merge puts parent node . after this node"}, conflicts[0])

	theirs = "[gd_scene format=3]\n\n[node name=\"World\" type=\"Node\"]\n"
	_, conflicts = merge3(t, base, ours, theirs)
	require.Len(t, conflicts, 1)
	assert.Equal(t, "the merge leaves two root nodes", conflicts[0].Message)
}

func TestMerge3HeaderAndResource(t *testing.T) {
	base := fixture(t, "theme_current.tres")
	ours := edit(t, base, "default_font_size = 16", "default_font_size = 18")
	theirs := edit(t, base, `format=3 uid="uid://c0x4mc8b2w6ty"`, `format=3 uid="uid://c0x4mc8b2w6tz"`, "Label/constants/line_spacing = 2\n", "Label/constants/line_spacing = 2\nLabel/font_sizes/font_size = 14\n")
	out := clean(t, base, ours, theirs)
	assert.Contains(t, out, `uid="uid://c0x4mc8b2w6tz"`)
	assert.Contains(t, out, "default_font_size = 18")
	assert.True(t, strings.HasSuffix(out, "Label/font_sizes/font_size = 14\n"))

	ours = edit(t, base, `format=3 uid="uid://c0x4mc8b2w6ty"`, `format=3 uid="uid://aaaa"`)
	theirs = edit(t, base, `format=3 uid="uid://c0x4mc8b2w6ty"`, `format=3 uid="uid://bbbb"`)
	_, conflicts := merge3(t, base, ours, theirs)
	require.Len(t, conflicts, 1)
	assert.Equal(t, Conflict{Section: "gd_resource", Key: "uid", Message: `changed differently on both sides: ours "uid://aaaa", theirs "uid://bbbb"`}, conflicts[0])

	scene := fixture(t, "player_4_4.tscn")
	upgraded := edit(t, scene, "load_steps=9 ", "")
	out = clean(t, scene, upgraded, edit(t, scene, "speed = 180.0", "speed = 1.0"))
	assert.True(t, strings.HasPrefix(out, "[gd_scene format=3"))
	out = clean(t, scene, edit(t, scene, "speed = 180.0", "speed = 1.0"), upgraded)
	assert.True(t, strings.HasPrefix(out, "[gd_scene format=3"))
}

func TestMerge3HeaderPresence(t *testing.T) {
	plain := "[node name=\"A\" type=\"Node\"]\n"
	scene := "[gd_scene format=3]\n\n" + plain
	resource := "[gd_resource type=\"Theme\" format=3]\n\n" + plain
	assert.Equal(t, scene, clean(t, plain, plain, scene))
	assert.Equal(t, scene, clean(t, plain, scene, plain))
	_, conflicts := merge3(t, plain, scene, resource)
	require.Len(t, conflicts, 1)
	assert.Equal(t, "header", conflicts[0].Section)
}

func TestMerge3ConfigFiles(t *testing.T) {
	base := fixture(t, "project.godot")
	ours := edit(t, base, "config_version=5", "config_version=6")
	theirs := edit(t, base, "config/name=", "config/description=\"x\"\nconfig/name=")
	out := clean(t, base, ours, theirs)
	assert.Contains(t, out, "config_version=6")
	assert.Contains(t, out, "config/description=\"x\"")

	_, conflicts := merge3(t, base, ours, edit(t, base, "config_version=5", "config_version=4"))
	require.Len(t, conflicts, 1)
	assert.Equal(t, Conflict{Key: "config_version", Message: "changed differently on both sides: ours 6, theirs 4"}, conflicts[0])
}

func TestMerge3AppendsAfterUnterminatedLine(t *testing.T) {
	base := "[resource]\na = 1"
	ours := "[resource]\na = 2"
	theirs := "[resource]\na = 1\nb = 3\n\n[sub]\nc = 4\n"
	out := clean(t, base, ours, theirs)
	assert.Equal(t, "[resource]\na = 2\nb = 3\n\n[sub]\nc = 4\n", out)
}

func TestMerge3KeepsBOMAndRejectsSyntax(t *testing.T) {
	data := "\xef\xbb\xbf[gd_scene format=3]\n\n[node name=\"A\" type=\"Node\"]\n"
	out, conflicts, err := Merge3([]byte(data), []byte(data), []byte(data))
	require.NoError(t, err)
	assert.Empty(t, conflicts)
	assert.Equal(t, data, string(out))

	for i, side := range []string{"base", "ours", "theirs"} {
		in := [][]byte{[]byte(data), []byte(data), []byte(data)}
		in[i] = []byte("[node name=\"A\"\n")
		_, _, err := Merge3(in[0], in[1], in[2])
		assert.ErrorIs(t, err, ErrSyntax)
		assert.ErrorContains(t, err, side+": ")
	}
}

func TestMerge3DuplicateKeysPairInOrder(t *testing.T) {
	base := "[a]\nx = 1\nx = 2\n"
	ours := "[a]\nx = 1\nx = 3\n"
	theirs := "[a]\nx = 0\nx = 2\n"
	assert.Equal(t, "[a]\nx = 0\nx = 3\n", clean(t, base, ours, theirs))
}

func TestFreshIDAndRewriteRefs(t *testing.T) {
	assert.Equal(t, "8", freshID("3", map[string]bool{"3": true, "7": true, `"x"`: true}))
	assert.Equal(t, "abc_3", freshID("abc", map[string]bool{"abc": true, "abc_2": true}))
	assert.Equal(t, `"a_2"`, freshID(`"a"`, map[string]bool{`"a"`: true}))

	ren := func(_, id string) string { return id + "!" }
	assert.Equal(t, `[ExtResource("1"!), SubResource( 2! ), ExtResource()]`, rewriteRefs(`[ExtResource("1"), SubResource( 2 ), ExtResource()]`, ren))
	assert.Equal(t, `ExtResource("1"`, rewriteRefs(`ExtResource("1"`, ren))
	assert.Equal(t, `Vector2(1, 2)`, rewriteRefs(`Vector2(1, 2)`, ren))
}

func TestMerge3RenumbersIntegerIDs(t *testing.T) {
	base := "[gd_scene load_steps=2 format=2]\n\n[ext_resource path=\"res://a.gd\" type=\"Script\" id=1]\n\n[node name=\"R\" type=\"Node\"]\nscript = ExtResource( 1 )\n"
	ours := strings.Replace(base, "\n\n[node", "\n[ext_resource path=\"res://b.png\" type=\"Texture\" id=2]\n\n[node", 1)
	theirs := strings.Replace(base, "\n\n[node", "\n[ext_resource path=\"res://c.png\" type=\"Texture\" id=2]\n\n[node", 1)
	theirs = strings.Replace(theirs, "script = ExtResource( 1 )", "script = ExtResource( 1 )\ntexture = ExtResource( 2 )", 1)
	out := clean(t, base, ours, theirs)
	assert.Contains(t, out, "[ext_resource path=\"res://c.png\" type=\"Texture\" id=3]")
	assert.Contains(t, out, "texture = ExtResource( 3 )")
	assert.Contains(t, out, "load_steps=3")
}

func TestMerge3LargeScene(t *testing.T) {
	base := fixture(t, "large_world.tscn")
	ours := edit(t, base, "[node name=\"Crate1\" parent=\"Props\" instance=ExtResource(\"2_cr4te\")]\ntransform = Transform3D(1, 0, 0, 0, 1, 0, 0, 0, 1, -76, 0.5, -60)", "[node name=\"Crate1\" parent=\"Props\" instance=ExtResource(\"2_cr4te\")]\ntransform = Transform3D(1, 0, 0, 0, 1, 0, 0, 0, 1, -70, 0.5, -60)")
	theirs := edit(t, base, "[node name=\"Crate1199\"", "[node name=\"Crate1200\" parent=\"Props\" instance=ExtResource(\"2_cr4te\")]\n\n[node name=\"Crate1199\"")
	out := clean(t, base, ours, theirs)
	assert.Contains(t, out, "-70, 0.5, -60")
	assert.Contains(t, out, "Crate1200")
	assert.Equal(t, len(ours)+len("[node name=\"Crate1200\" parent=\"Props\" instance=ExtResource(\"2_cr4te\")]\n\n"), len(out))
}

func TestMerge3OrderKeptFromSource(t *testing.T) {
	odd := "[gd_scene format=3]\n\n[node name=\"R\" type=\"Node\"]\nshape = SubResource(\"S\")\n\n[sub_resource type=\"BoxShape3D\" id=\"S\"]\n"
	assert.Equal(t, odd, clean(t, odd, odd, odd))
	edited := strings.Replace(odd, "[node name=\"R\" type=\"Node\"]", "[node name=\"R\" type=\"Node\"]\nvisible = false", 1)
	assert.Equal(t, edited, clean(t, odd, odd, edited))

	base := "[gd_scene format=3]\n\n[sub_resource type=\"BoxShape3D\" id=\"A\"]\n\n[node name=\"R\" type=\"Node\"]\n"
	ours := "[gd_scene format=3]\n\n[sub_resource type=\"BoxShape3D\" id=\"B\"]\n\n[node name=\"R\" type=\"Node\"]\n"
	theirs := "[gd_scene format=3]\n\n[sub_resource type=\"BoxShape3D\" id=\"A\"]\n\n[node name=\"R\" type=\"Node\"]\n\n[node name=\"C\" type=\"Node\" parent=\".\"]\nshape = SubResource(\"N\")\n\n[sub_resource type=\"BoxShape3D\" id=\"N\"]\n"
	_, conflicts := merge3(t, base, ours, theirs)
	assert.Empty(t, conflicts, "theirs already used N before defining it")
}
