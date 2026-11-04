package unitymeta

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func meta(guid string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("fileFormatVersion: 2\nguid: " + guid + "\nTextureImporter:\n  guid: ignored\n")}
}

func project(files map[string]*fstest.MapFile) fstest.MapFS {
	fsys := fstest.MapFS{"game/ProjectSettings/ProjectVersion.txt": {Data: []byte("m_EditorVersion: 6000.0.1f1")}}
	for k, v := range files {
		fsys[k] = v
	}
	return fsys
}

func summary(ws []Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = string(w.Kind) + " " + w.Path
	}
	return out
}

func TestGUID(t *testing.T) {
	assert.Equal(t, "0123abcd", GUID(meta("0123abcd").Data))
	assert.Equal(t, "", GUID([]byte("fileFormatVersion: 2\n  guid: nested\n")))
}

func TestLintNewAssets(t *testing.T) {
	fsys := project(map[string]*fstest.MapFile{
		"game/Assets/Art.meta":                 meta("a1"),
		"game/Assets/Art/hero.png":             {},
		"game/Assets/Art/hero.png.meta":        meta("b2"),
		"game/Assets/Art/Props/box.fbx":        {},
		"game/Assets/Art/Props/crate.fbx":      {},
		"game/Assets/Art/Props/crate.fbx.meta": meta("c3"),
		"game/Assets/Art/notes.txt~":           {},
		"game/Assets/.hidden/x.png":            {},
		"game/Assets/Art/scratch.tmp":          {},
		"game/Assets/Audio/sfx.wav":            {},
		"game/Assets/Audio/sfx.wav.meta":       meta("d4"),
		"tools/Assets/readme.md":               {},
	})
	ws := Lint(fsys, []Change{
		{Path: "game/Assets/Art/hero.png", Status: "added", Staged: true},
		{Path: "game/Assets/Art/hero.png.meta", Status: "untracked"},
		{Path: "game/Assets/Art/Props/box.fbx", Status: "untracked"},
		{Path: "game/Assets/Art/Props/crate.fbx", Status: "added", Staged: true},
		{Path: "game/Assets/Art/Props/crate.fbx.meta", Status: "added", Staged: true},
		{Path: "game/Assets/Art/notes.txt~", Status: "untracked"},
		{Path: "game/Assets/.hidden/x.png", Status: "untracked"},
		{Path: "game/Assets/Art/scratch.tmp", Status: "untracked"},
		{Path: "game/Assets/Audio/sfx.wav", Status: "untracked"},
		{Path: "game/Assets/Audio/sfx.wav.meta", Status: "untracked"},
		{Path: "tools/Assets/readme.md", Status: "untracked"},
		{Path: "game/ProjectSettings/ProjectVersion.txt", Status: "modified"},
	}, nil)
	assert.Equal(t, []string{
		"missing-meta game/Assets/Art/Props",
		"missing-meta game/Assets/Art/Props/box.fbx",
		"meta-not-staged game/Assets/Art/hero.png.meta",
		"missing-meta game/Assets/Audio",
	}, summary(ws))
	assert.Equal(t, "game/Assets/Art/Props.meta", ws[0].Related)
	assert.Equal(t, "game/Assets/Art/hero.png", ws[2].Related)
}

func TestLintDeletions(t *testing.T) {
	fsys := project(map[string]*fstest.MapFile{
		"game/Assets/old.png.meta":   meta("a1"),
		"game/Assets/kept.png":       {},
		"game/Assets/gone.png.meta":  meta("b2"),
		"game/Assets/stray.mat.meta": meta("c3"),
	})
	ws := Lint(fsys, []Change{
		{Path: "game/Assets/old.png", Status: "deleted", Staged: true},
		{Path: "game/Assets/kept.png.meta", Status: "deleted"},
		{Path: "game/Assets/gone.png", Status: "deleted"},
		{Path: "game/Assets/gone.png.meta", Status: "deleted"},
		{Path: "game/Assets/stray.mat.meta", Status: "untracked"},
		{Path: "game/Assets/both.png", Status: "deleted"},
		{Path: "game/Assets/both.png.meta", Status: "deleted"},
	}, nil)
	assert.Equal(t, []string{
		"missing-meta game/Assets/kept.png",
		"orphan-meta game/Assets/old.png.meta",
		"orphan-meta game/Assets/stray.mat.meta",
	}, summary(ws))
}

func TestLintGUIDChanges(t *testing.T) {
	fsys := project(map[string]*fstest.MapFile{
		"game/Assets/a.png.meta": meta("ffff"),
		"game/Assets/b.png.meta": meta("1111"),
		"game/Assets/c.png.meta": meta("2222"),
		"game/Assets/d.png.meta": {Data: []byte(strings.Repeat("x", maxMetaBytes+1))},
	})
	old := map[string]string{"game/Assets/a.png.meta": "aaaa", "game/Assets/b.png.meta": "1111"}
	read := func(p string) ([]byte, error) {
		if g, ok := old[p]; ok {
			return meta(g).Data, nil
		}
		return nil, errors.New("not synced")
	}
	changes := []Change{
		{Path: "game/Assets/a.png.meta", Status: "modified"},
		{Path: "game/Assets/b.png.meta", Status: "modified", Staged: true},
		{Path: "game/Assets/c.png.meta", Status: "modified"},
		{Path: "game/Assets/d.png.meta", Status: "modified"},
		{Path: "game/Assets/e.png.meta", Status: "modified"},
	}
	old["game/Assets/d.png.meta"] = "dddd"
	old["game/Assets/e.png.meta"] = "eeee"
	ws := Lint(fsys, changes, read)
	require.Equal(t, []string{"guid-changed game/Assets/a.png.meta"}, summary(ws))
	assert.Contains(t, ws[0].Message, "aaaa to ffff")
	assert.Empty(t, Lint(fsys, changes, nil))
}

func TestLintOutsideUnity(t *testing.T) {
	fsys := fstest.MapFS{"Assets/x.png": {}, "ProjectSettings": {Data: []byte("file, not a folder")}}
	assert.Empty(t, Lint(fsys, []Change{{Path: "Assets/x.png", Status: "untracked"}, {Path: "README.md", Status: "modified"}}, nil))
}

func TestLintStagedAssetDeletionWithUnstagedMeta(t *testing.T) {
	fsys := project(map[string]*fstest.MapFile{})
	ws := Lint(fsys, []Change{
		{Path: "game/Assets/old.png", Status: "deleted", Staged: true},
		{Path: "game/Assets/old.png.meta", Status: "deleted"},
	}, nil)
	assert.Equal(t, []string{"meta-not-staged game/Assets/old.png.meta"}, summary(ws))
}

func TestLintStagedMetaDeletionWithUnstagedAsset(t *testing.T) {
	fsys := project(map[string]*fstest.MapFile{})
	ws := Lint(fsys, []Change{
		{Path: "game/Assets/old.png", Status: "deleted"},
		{Path: "game/Assets/old.png.meta", Status: "deleted", Staged: true},
	}, nil)
	assert.Equal(t, []string{"meta-not-staged game/Assets/old.png"}, summary(ws))
}
