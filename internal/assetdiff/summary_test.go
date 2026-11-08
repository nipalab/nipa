package assetdiff

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/preview"
)

func TestSummarize(t *testing.T) {
	f := files{
		"snapshot:b.txt": []byte("a\n"),
		"working:b.txt":  []byte("a\nb\n"),
		"working:a.obj":  []byte(objNew),
		"working:c.txt":  []byte("ERR"),
		"snapshot:z.csv": []byte("id\n1\n"),
		"working:z.csv":  []byte("id\n1\n2\n"),
	}
	items := []Item{
		{Path: "z.csv", Old: oldRef("z.csv"), New: newRef("z.csv")},
		{Path: "b.txt", Status: "modified", Old: oldRef("b.txt"), New: newRef("b.txt")},
		{Path: "a.obj", New: newRef("a.obj")},
		{Path: "c.txt", New: newRef("c.txt")},
		{Path: "d.txt", Old: oldRef("d.txt"), New: newRef("d.txt")},
		{Path: "e.txt"},
	}
	out, err := Summarize(ctx, f.load, items, Options{})
	require.NoError(t, err)
	assert.Zero(t, out.Omitted)
	require.Len(t, out.Files, 6)
	assert.Equal(t, Summary{Path: "a.obj", Status: StatusAdded, Kind: preview.KindModel, Headline: "added, 3 triangles, 1 mesh"}, out.Files[0])
	assert.Equal(t, Summary{Path: "b.txt", Status: "modified", Kind: preview.KindText, Headline: "+1 -0 lines"}, out.Files[1])
	assert.Equal(t, "offline", out.Files[2].Error)
	assert.Equal(t, "file is missing on both sides", out.Files[3].Error)
	assert.NotEmpty(t, out.Files[4].Error)
	assert.Equal(t, "+1 row", out.Files[5].Headline)

	capped, err := Summarize(ctx, f.load, items, Options{MaxFiles: 2})
	require.NoError(t, err)
	assert.Equal(t, 4, capped.Omitted)
	assert.Equal(t, out.Files[:2], capped.Files)
	assert.Equal(t, "z.csv", items[0].Path, "the caller's slice is not reordered")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = Summarize(cancelled, f.load, items, Options{})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestSummarizeCancelledDuringCompare(t *testing.T) {
	c, cancel := context.WithCancel(ctx)
	_, err := Summarize(c, func(context.Context, Ref, int64) ([]byte, error) {
		cancel()
		return nil, context.Canceled
	}, []Item{{Path: "a.txt", New: newRef("a.txt")}}, Options{})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestUnityHelpers(t *testing.T) {
	assert.Equal(t, "game", UnityProject("game/Assets/Level.unity"))
	assert.Equal(t, "", UnityProject("Packages/com.acme/Thing.asset"))
	assert.Equal(t, "", UnityProject("Level.unity"))
	for p, want := range map[string]string{
		"a/level.tscn": "godot", "a/mat.TRES": "godot", "project.godot": "godot", "icon.svg.import": "godot",
		"Assets/Level.unity": "unity", "Assets/Hero.prefab": "unity",
	} {
		assert.Equal(t, want, SceneEngine(p), p)
	}
	assert.Equal(t, "", ReadGUID("/missing/file.meta"))

	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	write("game/Assets/Hero.png.meta", "fileFormatVersion: 2\nguid: aaaa1111\n")
	write("game/Assets/Sub/Sword.fbx.meta", "guid: bbbb2222\n")
	write("game/Packages/com.acme/Thing.asset.meta", "guid: cccc3333\n")
	write("game/Assets/NoGuid.txt.meta", "fileFormatVersion: 2\n")
	write("game/Assets/Hero.png", "png")
	write("Assets/Root.mat.meta", "guid: dddd4444\n")

	assert.Equal(t, map[string]string{
		"aaaa1111": "Assets/Hero.png",
		"bbbb2222": "Assets/Sub/Sword.fbx",
		"cccc3333": "Packages/com.acme/Thing.asset",
	}, BuildGUIDIndex(filepath.Join(root, "game")))
	assert.Equal(t, "aaaa1111", ReadGUID(filepath.Join(root, "game/Assets/Hero.png.meta")))

	guids := WorkingGUIDs(root)
	assert.Equal(t, "Assets/Hero.png", guids("game/Assets/Level.unity")("aaaa1111"))
	assert.Equal(t, "", guids("game/Assets/Level.unity")("dddd4444"))
	assert.Equal(t, "Assets/Root.mat", guids("Assets/Level.unity")("dddd4444"))
}
