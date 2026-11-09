package assetrefs

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func guid(c string) string { return strings.Repeat(c, 32) }

func meta(g string) string { return "fileFormatVersion: 2\nguid: " + g + "\n" }

func unityAsset(guids ...string) string {
	var b strings.Builder
	b.WriteString("%YAML 1.1\n%TAG !u! tag:unity3d.com,2011:\n--- !u!21 &2100000\nMaterial:\n")
	for _, g := range guids {
		b.WriteString("  - ref: {fileID: 2800000, guid: " + g + ", type: 3}\n")
	}
	return b.String()
}

func mapFS(files map[string]string) (fstest.MapFS, []string) {
	fsys := fstest.MapFS{}
	names := make([]string, 0, len(files))
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
		names = append(names, name)
	}
	sort.Strings(names)
	return fsys, names
}

func unityFiles() map[string]string {
	return map[string]string{
		"Assets/Art/hero.png":               "png",
		"Assets/Art/hero.png.meta":          meta(guid("a")),
		"Assets/Mat/hero.mat":               unityAsset(guid("a"), guid("1")),
		"Assets/Mat/hero.mat.meta":          meta(guid("b")),
		"Assets/Scripts/Hero.cs":            "class Hero {}",
		"Assets/Scripts/Hero.cs.meta":       meta(guid("1")),
		"Assets/Prefabs/Hero.prefab":        unityAsset(guid("b"), guid("c"), "0000000000000000f000000000000000"),
		"Assets/Prefabs/Hero.prefab.meta":   meta(guid("d")),
		"Assets/Scenes/Main.unity":          unityAsset(guid("d")),
		"Assets/Scenes/Main.unity.meta":     meta(guid("e")),
		"Assets/Scenes.meta":                meta(guid("2")),
		"ProjectSettings/EditorBuild.asset": unityAsset(guid("e"), guid("2")),
		"game2/Assets/X.mat":                unityAsset(guid("a")),
		"game2/Assets/Bin.asset":            "binary",
	}
}

func TestBuild_Unity(t *testing.T) {
	fsys, files := mapFS(unityFiles())
	g := Build(fsys, files, Options{})

	assert.Equal(t, 6, g.Scanned)
	assert.False(t, g.Truncated)
	assert.Equal(t, []Ref{
		{Path: "Assets/Scripts/Hero.cs", Raw: "guid:" + guid("1"), Resolved: true},
		{Path: "Assets/Art/hero.png", Raw: "guid:" + guid("a"), Resolved: true},
	}, g.Uses("Assets/Mat/hero.mat"))
	uses := g.Uses("Assets/Prefabs/Hero.prefab")
	require.Len(t, uses, 2)
	assert.True(t, uses[0].Resolved)
	assert.Equal(t, Ref{Raw: "guid:" + guid("c")}, uses[1])
	assert.Equal(t, []string{"Assets/Mat/hero.mat"}, g.UsedBy("Assets/Art/hero.png"))
	assert.Equal(t, []string{"ProjectSettings/EditorBuild.asset"}, g.UsedBy("Assets/Scenes"))
	assert.False(t, g.Uses("game2/Assets/X.mat")[0].Resolved, "GUID indexes are per project")
	assert.Empty(t, g.Uses("game2/Assets/Bin.asset"))
	assert.True(t, g.Exists("Assets/Art"))
	assert.Len(t, g.Sources(), 6)
}

func TestBuild_Caps(t *testing.T) {
	files := unityFiles()
	fsys, names := mapFS(files)
	g := Build(fsys, names, Options{MaxFiles: 2})
	assert.True(t, g.Truncated)
	assert.Equal(t, 2, g.Scanned)

	g = Build(fsys, names, Options{MaxFileBytes: 100})
	assert.True(t, g.Truncated)
	assert.Positive(t, g.Skipped)

	assert.Equal(t, HardMaxFiles, Options{MaxFiles: 1 << 30}.normalized().MaxFiles)
}

func TestBuild_UnreadableSourceTruncates(t *testing.T) {
	fsys, files := mapFS(unityFiles())
	fsys["Assets/Mat/hero.mat"] = &fstest.MapFile{Mode: fs.ModeDir}
	g := Build(fsys, files, Options{})
	assert.True(t, g.Truncated)
	assert.Equal(t, 1, g.Skipped)
	assert.Empty(t, g.UsedBy("Assets/Art/hero.png"))
}

func TestBuild_OutsideIsUnknown(t *testing.T) {
	fsys, files := mapFS(map[string]string{
		"Assets/A.mat":    unityAsset(guid("f")),
		"p/project.godot": "",
		"p/s.tscn":        "[gd_scene format=3]\n\n[ext_resource type=\"Texture2D\" path=\"res://far/t.png\" id=\"1\"]\n[ext_resource type=\"Texture2D\" path=\"res://near.png\" id=\"2\"]\n",
	})
	g := Build(fsys, files, Options{Outside: func(p string) bool { return strings.HasPrefix(p, "p/far") }})
	assert.True(t, g.Uses("Assets/A.mat")[0].Unknown)
	uses := g.Uses("p/s.tscn")
	require.Len(t, uses, 2)
	assert.True(t, uses[0].Unknown)
	assert.Equal(t, "p/far/t.png", uses[0].Path)
	assert.False(t, uses[1].Unknown)
	assert.False(t, uses[1].Resolved)
}

func godotScene(uid string, exts ...string) string {
	var b strings.Builder
	b.WriteString("[gd_scene load_steps=3 format=3")
	if uid != "" {
		b.WriteString(` uid="` + uid + `"`)
	}
	b.WriteString("]\n\n")
	for i, e := range exts {
		b.WriteString("[ext_resource type=\"Resource\" " + e + " id=\"" + string(rune('1'+i)) + "\"]\n")
	}
	b.WriteString("\n[node name=\"Root\" type=\"Node\"]\n")
	return b.String()
}

func godotFiles() map[string]string {
	return map[string]string{
		"proj/project.godot":     "config_version=5\n",
		"proj/scenes/main.tscn":  godotScene("uid://main1", `uid="uid://tex1" path="res://art/a.png"`, `path="res://scripts/p.gd"`, `uid="uid://mat1"`, `path="res://gone.png"`),
		"proj/art/a.png":         "png",
		"proj/art/a.png.import":  "[remap]\n\nimporter=\"texture\"\nuid=\"uid://tex1\"\n",
		"proj/scripts/p.gd":      "extends Node",
		"proj/scripts/p.gd.uid":  "uid://scr1\n",
		"proj/mat.tres":          "[gd_resource type=\"Material\" format=3 uid=\"uid://mat1\"]\n\n[resource]\n",
		"proj/sub/project.godot": "",
		"proj/sub/x.tscn":        godotScene("", `path="res://y.png"`),
		"proj/sub/y.png":         "png",
		"proj/bin.tres":          "RSRC binary",
		"proj/broken.tscn":       "[gd_scene\n",
		"proj/orphan.png.import": "[remap]\nuid=\"uid://orph\"\n",
	}
}

func TestBuild_Godot(t *testing.T) {
	fsys, files := mapFS(godotFiles())
	g := Build(fsys, files, Options{})
	uses := g.Uses("proj/scenes/main.tscn")
	require.Len(t, uses, 4)
	byRaw := map[string]Ref{}
	for _, u := range uses {
		byRaw[u.Raw] = u
	}
	assert.Equal(t, "proj/art/a.png", byRaw["res://art/a.png"].Path)
	assert.True(t, byRaw["res://art/a.png"].Resolved)
	assert.True(t, byRaw["res://scripts/p.gd"].Resolved)
	assert.Equal(t, "proj/mat.tres", byRaw["uid://mat1"].Path)
	assert.False(t, byRaw["res://gone.png"].Resolved)
	assert.Equal(t, "proj/gone.png", byRaw["res://gone.png"].Path)
	assert.Equal(t, []string{"proj/sub/y.png"}, []string{g.Uses("proj/sub/x.tscn")[0].Path})
	assert.True(t, g.Uses("proj/sub/x.tscn")[0].Resolved)
	assert.Empty(t, g.Uses("proj/bin.tres"))
	assert.Empty(t, g.Uses("proj/broken.tscn"))
	assert.Equal(t, "proj/scenes/main.tscn", g.uids["uid://main1"])
	assert.Empty(t, g.uids["uid://orph"])
}

func TestBuild_GodotStalePathWithValidUID(t *testing.T) {
	fsys, files := mapFS(map[string]string{
		"project.godot":    "",
		"main.tscn":        godotScene("", `uid="uid://tex1" path="res://old/a.png"`),
		"new/a.png":        "png",
		"new/a.png.import": "[remap]\nuid=\"uid://tex1\"\n",
	})
	g := Build(fsys, files, Options{})
	ref := g.Uses("main.tscn")[0]
	assert.True(t, ref.Resolved)
	assert.Equal(t, "new/a.png", ref.Path)
}

func TestRelevant(t *testing.T) {
	for p, want := range map[string]bool{
		"Assets/a.png":      true,
		"a.png.meta":        true,
		"x/main.tscn":       true,
		"x/a.png":           true,
		"x/a.png.import":    true,
		"x/p.gd":            true,
		"docs/readme.txt":   false,
		"ProjectSettings/x": true,
		"src/main.go":       false,
	} {
		assert.Equal(t, want, Relevant(p), p)
	}
}

func TestParseRefs(t *testing.T) {
	assert.Equal(t, []string{"guid:" + guid("a")}, ParseRefs("a.prefab", []byte("\ufeff"+unityAsset(guid("A"), guid("a")))))
	assert.Empty(t, ParseRefs("a.prefab", []byte("binary")))
	assert.Empty(t, ParseRefs("a.png", []byte(unityAsset(guid("a")))))
	assert.Equal(t, []string{"res://a.png", "uid://m"}, ParseRefs("s.tscn", []byte(godotScene("", `path="res://a.png"`, `uid="uid://m"`, `path="res://a.png"`, `path="user://x"`))))
}

func FuzzUnityRefs(f *testing.F) {
	f.Add([]byte(unityAsset(guid("a"), guid("b"))))
	f.Add([]byte("%YAML\nguid: 0000000000000000f000000000000000"))
	f.Add([]byte("guid:"))
	f.Fuzz(func(t *testing.T, data []byte) {
		refs := unityRefs(data)
		for i, r := range refs {
			if !strings.HasPrefix(r.raw, guidPrefix) || len(r.raw) != len(guidPrefix)+32 {
				t.Fatalf("bad ref %q", r.raw)
			}
			if i > 0 && refs[i-1].raw >= r.raw {
				t.Fatalf("refs not sorted and unique: %v", refs)
			}
		}
		_ = ParseRefs("x.tscn", data)
	})
}
