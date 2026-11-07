package godotmeta

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sample(t *testing.T, name string) *fstest.MapFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return &fstest.MapFile{Data: data}
}

func uidData(uid string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(uid + "\n")} }

func project(root string, files map[string]*fstest.MapFile) fstest.MapFS {
	fsys := fstest.MapFS{under(root, "project.godot"): {Data: []byte("config_version=5\n")}}
	for k, v := range files {
		fsys[under(root, k)] = v
	}
	return fsys
}

func under(root, p string) string {
	if root == "" {
		return p
	}
	return root + "/" + p
}

func prefixed(root string, changes []Change) []Change {
	out := make([]Change, len(changes))
	for i, c := range changes {
		c.Path = under(root, c.Path)
		out[i] = c
	}
	return out
}

func summary(root string, ws []Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = string(w.Kind) + " " + strings.TrimPrefix(w.Path, under(root, ""))
	}
	return out
}

func TestLintNewAssets(t *testing.T) {
	for _, root := range []string{"", "game"} {
		t.Run("root="+root, func(t *testing.T) {
			fsys := project(root, map[string]*fstest.MapFile{
				"art/hero.png":               {},
				"art/hero.png.import":        sample(t, "icon.svg.import"),
				"art/box.glb":                {},
				"art/crate.glb":              {},
				"art/crate.glb.import":       sample(t, "hero.glb.import"),
				"scripts/player.gd":          {},
				"scripts/player.gd.uid":      sample(t, "player.gd.uid"),
				"scripts/enemy.gd":           {},
				"main.tscn":                  {},
				".godot/imported/x.ctex":     {},
				"raw/.gdignore":              {},
				"raw/source.png":             {},
				"audio/sfx.wav":              {},
				"audio/sfx.wav.import":       sample(t, "legacy.wav.import"),
				"addons/tool/.hidden/a.png":  {},
				"addons/tool/plugin.gd":      {},
				"addons/tool/plugin.gd.uid":  uidData("uid://p1"),
				"shaders/water.gdshader":     {},
				"shaders/water.gdshader.uid": sample(t, "water.gdshader.uid"),
			})
			ws := Lint(fsys, prefixed(root, []Change{
				{Path: "art/hero.png", Status: "added", Staged: true},
				{Path: "art/hero.png.import", Status: "untracked"},
				{Path: "art/box.glb", Status: "untracked"},
				{Path: "art/crate.glb", Status: "added", Staged: true},
				{Path: "art/crate.glb.import", Status: "added", Staged: true},
				{Path: "scripts/player.gd", Status: "added", Staged: true},
				{Path: "scripts/player.gd.uid", Status: "added", Staged: true},
				{Path: "scripts/enemy.gd", Status: "untracked"},
				{Path: "main.tscn", Status: "untracked"},
				{Path: ".godot/imported/x.ctex", Status: "untracked"},
				{Path: "raw/source.png", Status: "untracked"},
				{Path: "audio/sfx.wav", Status: "untracked"},
				{Path: "audio/sfx.wav.import", Status: "untracked"},
				{Path: "addons/tool/.hidden/a.png", Status: "untracked"},
				{Path: "addons/tool/plugin.gd", Status: "modified", Staged: true},
				{Path: "addons/tool/plugin.gd.uid", Status: "modified"},
				{Path: "shaders/water.gdshader", Status: "modified", Staged: true},
				{Path: "shaders/water.gdshader.uid", Status: "modified", Staged: true},
				{Path: "project.godot", Status: "modified"},
			}), nil)
			assert.Equal(t, []string{
				"sidecar-not-staged addons/tool/plugin.gd.uid",
				"missing-sidecar art/box.glb",
				"sidecar-not-staged art/hero.png.import",
				"missing-sidecar scripts/enemy.gd",
			}, summary(root, ws))
			assert.Equal(t, under(root, "art/box.glb.import"), ws[1].Related)
			assert.Contains(t, ws[1].Message, ".import")
			assert.Equal(t, under(root, "scripts/enemy.gd.uid"), ws[3].Related)
			assert.Contains(t, ws[3].Message, ".uid")
		})
	}
}

func TestLintDeletions(t *testing.T) {
	fsys := project("game", map[string]*fstest.MapFile{
		"old.png.import":   sample(t, "icon.svg.import"),
		"kept.png":         {},
		"gone.ogg.import":  sample(t, "music.ogg.import"),
		"stray.mp3.import": sample(t, "music.ogg.import"),
		"lost.gd.uid":      uidData("uid://lost"),
	})
	ws := Lint(fsys, prefixed("game", []Change{
		{Path: "old.png", Status: "deleted", Staged: true},
		{Path: "kept.png.import", Status: "deleted"},
		{Path: "gone.ogg", Status: "deleted"},
		{Path: "gone.ogg.import", Status: "deleted"},
		{Path: "stray.mp3.import", Status: "untracked"},
		{Path: "both.png", Status: "deleted"},
		{Path: "both.png.import", Status: "deleted"},
		{Path: "lost.gd", Status: "deleted"},
	}), nil)
	assert.Equal(t, []string{
		"missing-sidecar kept.png",
		"orphan-sidecar lost.gd.uid",
		"orphan-sidecar old.png.import",
		"orphan-sidecar stray.mp3.import",
	}, summary("game", ws))
}

func TestLintStagedDeletions(t *testing.T) {
	fsys := project("", nil)
	ws := Lint(fsys, []Change{
		{Path: "old.png", Status: "deleted", Staged: true},
		{Path: "old.png.import", Status: "deleted"},
		{Path: "a.gd", Status: "deleted"},
		{Path: "a.gd.uid", Status: "deleted", Staged: true},
	}, nil)
	assert.Equal(t, []string{
		"sidecar-not-staged a.gd",
		"sidecar-not-staged old.png.import",
	}, summary("", ws))
}

func TestLintUIDChanges(t *testing.T) {
	fsys := project("", map[string]*fstest.MapFile{
		"a.png.import":   sample(t, "icon.svg.import"),
		"b.png.import":   sample(t, "music.ogg.import"),
		"c.gd.uid":       {Data: []byte("uid://cnew\r\n")},
		"d.gd.uid":       {Data: []byte(strings.Repeat("x", maxSidecarBytes+1))},
		"e.gd.uid":       uidData("uid://e"),
		"f.wav.import":   sample(t, "legacy.wav.import"),
		"g.gd.uid":       uidData("uid://same"),
		"h.png.import":   sample(t, "icon.svg.import"),
		"stray/x.gd.uid": uidData("uid://x"),
	})
	old := map[string]string{
		"a.png.import": "[remap]\nuid=\"uid://old\"\n",
		"b.png.import": "[remap]\nuid=\"uid://b7t0rq3hyxm2e\"\n",
		"c.gd.uid":     "uid://cold\n",
		"d.gd.uid":     "uid://d\n",
		"f.wav.import": "[remap]\nuid=\"uid://f\"\n",
		"g.gd.uid":     "uid://same",
	}
	read := func(p string) ([]byte, error) {
		if v, ok := old[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("not synced")
	}
	changes := []Change{
		{Path: "a.png.import", Status: "modified"},
		{Path: "b.png.import", Status: "modified", Staged: true},
		{Path: "c.gd.uid", Status: "modified"},
		{Path: "d.gd.uid", Status: "modified"},
		{Path: "e.gd.uid", Status: "modified"},
		{Path: "f.wav.import", Status: "modified"},
		{Path: "g.gd.uid", Status: "modified"},
		{Path: "missing.gd.uid", Status: "modified"},
	}
	old["missing.gd.uid"] = "uid://m"
	ws := Lint(fsys, changes, read)
	require.Equal(t, []string{"uid-changed a.png.import", "uid-changed c.gd.uid"}, summary("", ws))
	assert.Contains(t, ws[0].Message, "uid://old to uid://cvdk8a1nd4yxb")
	assert.Equal(t, "c.gd", ws[1].Related)
	assert.Empty(t, Lint(fsys, changes, nil))
}

func TestLintOutsideGodot(t *testing.T) {
	fsys := fstest.MapFS{"a.png": {}, "project.godot/x": {}, "other/project.godot": {}}
	assert.Empty(t, Lint(fsys, []Change{{Path: "a.png", Status: "untracked"}, {Path: "README.md", Status: "modified"}}, nil))
	assert.Empty(t, Lint(fsys, []Change{{Path: "/abs/a.png", Status: "untracked"}}, nil))
}

func TestLintNestedProjects(t *testing.T) {
	fsys := fstest.MapFS{
		"project.godot":       {},
		"tools/project.godot": {},
		"tools/raw/.gdignore": {},
		"tools/a.png":         {},
		"tools/raw/b.png":     {},
		"c.png":               {},
	}
	ws := Lint(fsys, []Change{
		{Path: "tools/a.png", Status: "untracked"},
		{Path: "tools/raw/b.png", Status: "untracked"},
		{Path: "c.png", Status: "untracked"},
	}, nil)
	assert.Equal(t, []string{"missing-sidecar c.png", "missing-sidecar tools/a.png"}, summary("", ws))
}
