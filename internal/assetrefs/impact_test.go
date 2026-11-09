package assetrefs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetdiff"
)

func mapLoader(old map[string]string) assetdiff.Loader {
	return func(_ context.Context, ref assetdiff.Ref, limit int64) ([]byte, error) {
		data, ok := old[ref.Path]
		if !ok {
			return nil, assetdiff.ErrMissing
		}
		if int64(len(data)) > limit {
			return nil, assetdiff.ErrTooLarge
		}
		return []byte(data), nil
	}
}

func item(p, status string) assetdiff.Item {
	it := assetdiff.Item{Path: p, Status: status}
	if status != assetdiff.StatusAdded {
		it.Old = &assetdiff.Ref{Source: "snapshot", Path: p}
	}
	if status != assetdiff.StatusRemoved {
		it.New = &assetdiff.Ref{Source: "working", Path: p}
	}
	return it
}

func renamed(from, to string) assetdiff.Item {
	return assetdiff.Item{Path: to, OldPath: from, Status: "renamed",
		Old: &assetdiff.Ref{Source: "snapshot", Path: from}, New: &assetdiff.Ref{Source: "working", Path: to}}
}

func impact(t *testing.T, old, now map[string]string, changes ...assetdiff.Item) *Impact {
	t.Helper()
	fsys, files := mapFS(now)
	out, err := ComputeImpact(context.Background(), Build(fsys, files, Options{}), changes, mapLoader(old))
	require.NoError(t, err)
	return out
}

func without(files map[string]string, drop ...string) map[string]string {
	out := map[string]string{}
	for k, v := range files {
		out[k] = v
	}
	for _, d := range drop {
		delete(out, d)
	}
	return out
}

func with(files map[string]string, kv ...string) map[string]string {
	out := without(files)
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func TestImpact_RemovedAssetWithMeta(t *testing.T) {
	old := unityFiles()
	now := without(old, "Assets/Art/hero.png", "Assets/Art/hero.png.meta")
	out := impact(t, old, now, item("Assets/Art/hero.png", assetdiff.StatusRemoved), item("Assets/Art/hero.png.meta", assetdiff.StatusRemoved))
	assert.Equal(t, []BrokenRef{{From: "Assets/Mat/hero.mat", Raw: "guid:" + guid("a"), Target: "Assets/Art/hero.png", Reason: ReasonRemoved}}, out.Broken)
	assert.Empty(t, out.NewlyUnused)
	assert.False(t, out.Empty())
}

func TestImpact_RemovedAssetKeepsOrphanMeta(t *testing.T) {
	old := unityFiles()
	now := without(old, "Assets/Art/hero.png")
	out := impact(t, old, now, item("Assets/Art/hero.png", assetdiff.StatusRemoved))
	require.Len(t, out.Broken, 1)
	assert.Equal(t, ReasonRemoved, out.Broken[0].Reason)
}

func TestImpact_GUIDChanged(t *testing.T) {
	old := unityFiles()
	now := with(old, "Assets/Art/hero.png.meta", meta(guid("9")))
	out := impact(t, old, now, item("Assets/Art/hero.png.meta", assetdiff.StatusModified))
	require.Len(t, out.Broken, 1)
	assert.Equal(t, ReasonGUIDChanged, out.Broken[0].Reason)

	now = without(old, "Assets/Art/hero.png.meta")
	out = impact(t, old, now, item("Assets/Art/hero.png.meta", assetdiff.StatusRemoved))
	require.Len(t, out.Broken, 1)
	assert.Equal(t, ReasonGUIDChanged, out.Broken[0].Reason, "the asset stays, Unity regenerates a new GUID")
}

func TestImpact_RenameWithStableGUID(t *testing.T) {
	old := unityFiles()
	now := with(without(old, "Assets/Art/hero.png", "Assets/Art/hero.png.meta"),
		"Assets/Art2/hero.png", "png", "Assets/Art2/hero.png.meta", meta(guid("a")))
	out := impact(t, old, now, renamed("Assets/Art/hero.png", "Assets/Art2/hero.png"), renamed("Assets/Art/hero.png.meta", "Assets/Art2/hero.png.meta"))
	assert.Empty(t, out.Broken)
	assert.Empty(t, out.NewlyUnused)
	assert.True(t, out.Empty())
}

func TestImpact_PreExistingUnresolvedIsQuiet(t *testing.T) {
	old := unityFiles()
	now := with(old, "Assets/Prefabs/Hero.prefab", unityAsset(guid("b"), guid("c"), "0000000000000000f000000000000000")+"  m_Name: changed\n")
	out := impact(t, old, now, item("Assets/Prefabs/Hero.prefab", assetdiff.StatusModified))
	assert.Empty(t, out.Broken)

	now = with(old, "Assets/Prefabs/Hero.prefab", unityAsset(guid("b"), guid("c"), guid("8")))
	out = impact(t, old, now, item("Assets/Prefabs/Hero.prefab", assetdiff.StatusModified))
	assert.Equal(t, []BrokenRef{{From: "Assets/Prefabs/Hero.prefab", Raw: "guid:" + guid("8"), Reason: ReasonUnresolved}}, out.Broken)

	now = with(old, "Assets/New.prefab", unityAsset(guid("7")))
	out = impact(t, old, now, item("Assets/New.prefab", assetdiff.StatusAdded))
	require.Len(t, out.Broken, 1)
	assert.Equal(t, "Assets/New.prefab", out.Broken[0].From)
}

func TestImpact_NewlyUnused(t *testing.T) {
	old := with(unityFiles(),
		"Assets/Resources/r.png", "png", "Assets/Resources/r.png.meta", meta(guid("5")),
		"Assets/Mat/hero.mat", unityAsset(guid("a"), guid("1"), guid("5"), guid("e")))
	now := with(old, "Assets/Mat/hero.mat", unityAsset())
	out := impact(t, old, now, item("Assets/Mat/hero.mat", assetdiff.StatusModified))
	assert.Equal(t, []string{"Assets/Art/hero.png"}, out.NewlyUnused, "scripts, scenes and Resources are never unused")
	assert.Empty(t, out.Broken)

	now = without(old, "Assets/Scenes/Main.unity", "Assets/Scenes/Main.unity.meta")
	out = impact(t, old, now, item("Assets/Scenes/Main.unity", assetdiff.StatusRemoved), item("Assets/Scenes/Main.unity.meta", assetdiff.StatusRemoved))
	assert.Equal(t, []string{"Assets/Prefabs/Hero.prefab"}, out.NewlyUnused)
	assert.Equal(t, []BrokenRef{{From: "Assets/Mat/hero.mat", Raw: "guid:" + guid("e"), Target: "Assets/Scenes/Main.unity", Reason: ReasonRemoved}, {From: "ProjectSettings/EditorBuild.asset", Raw: "guid:" + guid("e"), Target: "Assets/Scenes/Main.unity", Reason: ReasonRemoved}}, out.Broken)
}

func TestImpact_TruncatedSuppressesUnused(t *testing.T) {
	old := unityFiles()
	now := with(old, "Assets/Mat/hero.mat", unityAsset())
	fsys, files := mapFS(now)
	out, err := ComputeImpact(context.Background(), Build(fsys, files, Options{MaxFiles: 1}), []assetdiff.Item{item("Assets/Mat/hero.mat", assetdiff.StatusModified)}, mapLoader(old))
	require.NoError(t, err)
	assert.True(t, out.Truncated)
	assert.Empty(t, out.NewlyUnused)
	assert.NotNil(t, out.NewlyUnused)

	big := with(old, "Assets/Mat/hero.mat", unityAsset(guid("a"))+string(make([]byte, 200)))
	fsys, files = mapFS(now)
	out, err = ComputeImpact(context.Background(), Build(fsys, files, Options{MaxFileBytes: 150}), []assetdiff.Item{item("Assets/Mat/hero.mat", assetdiff.StatusModified)}, mapLoader(big))
	require.NoError(t, err)
	assert.True(t, out.Truncated)
}

func TestImpact_PartialCloneSuppressesUnused(t *testing.T) {
	old := unityFiles()
	now := with(old, "Assets/Mat/hero.mat", unityAsset())
	fsys, files := mapFS(now)
	g := Build(fsys, files, Options{Outside: func(p string) bool { return strings.HasPrefix(p, "Other/") }})
	require.True(t, g.Partial())
	out, err := ComputeImpact(context.Background(), g, []assetdiff.Item{item("Assets/Mat/hero.mat", assetdiff.StatusModified)}, mapLoader(old))
	require.NoError(t, err)
	assert.False(t, out.Truncated)
	assert.NotNil(t, out.NewlyUnused)
	assert.Empty(t, out.NewlyUnused, "a referrer outside the sparse set is not visible")
}

func TestImpact_Godot(t *testing.T) {
	old := godotFiles()

	now := with(without(old, "proj/art/a.png", "proj/art/a.png.import"), "proj/scenes/main.tscn", old["proj/scenes/main.tscn"])
	out := impact(t, old, now, item("proj/art/a.png", assetdiff.StatusRemoved), item("proj/art/a.png.import", assetdiff.StatusRemoved))
	assert.Equal(t, []BrokenRef{{From: "proj/scenes/main.tscn", Raw: "res://art/a.png", Target: "proj/art/a.png", Reason: ReasonRemoved}}, out.Broken)

	now = with(without(old, "proj/scripts/p.gd", "proj/scripts/p.gd.uid"), "proj/lib/p.gd", "extends Node", "proj/lib/p.gd.uid", "uid://scr1\n")
	out = impact(t, old, now, renamed("proj/scripts/p.gd", "proj/lib/p.gd"), renamed("proj/scripts/p.gd.uid", "proj/lib/p.gd.uid"))
	assert.Equal(t, []BrokenRef{{From: "proj/scenes/main.tscn", Raw: "res://scripts/p.gd", Target: "proj/lib/p.gd", Reason: ReasonRenamed}}, out.Broken)

	now = without(old, "proj/mat.tres")
	out = impact(t, old, now, item("proj/mat.tres", assetdiff.StatusRemoved))
	assert.Equal(t, []BrokenRef{{From: "proj/scenes/main.tscn", Raw: "uid://mat1", Target: "proj/mat.tres", Reason: ReasonRemoved}}, out.Broken)

	now = with(old, "proj/art/a.png.import", "[remap]\nuid=\"uid://other\"\n", "proj/scenes/main.tscn", godotScene("uid://main1", `uid="uid://tex1" path="res://art/b.png"`))
	out = impact(t, old, now, item("proj/art/a.png.import", assetdiff.StatusModified), item("proj/scenes/main.tscn", assetdiff.StatusModified))
	require.Len(t, out.Broken, 1)
	assert.Equal(t, ReasonUIDChanged, out.Broken[0].Reason)
	assert.Equal(t, []string{"proj/art/a.png", "proj/mat.tres"}, out.NewlyUnused, "scripts are never unused")
}

func TestImpact_LoaderError(t *testing.T) {
	fsys, files := mapFS(unityFiles())
	g := Build(fsys, files, Options{})
	boom := errors.New("boom")
	out, err := ComputeImpact(context.Background(), g, []assetdiff.Item{item("Assets/Mat/hero.mat", assetdiff.StatusModified)},
		func(context.Context, assetdiff.Ref, int64) ([]byte, error) { return nil, boom })
	require.NoError(t, err)
	assert.True(t, out.Truncated)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ComputeImpact(ctx, g, []assetdiff.Item{item("Assets/Art/hero.png.meta", assetdiff.StatusModified)}, mapLoader(unityFiles()))
	require.ErrorIs(t, err, context.Canceled)
}

func TestImpact_UnknownOldSideIsNotNewBreakage(t *testing.T) {
	old := unityFiles()
	now := with(old, "Assets/Prefabs/Hero.prefab", unityAsset(guid("b"), guid("c"))+"  m_Name: changed\n")
	changes := []assetdiff.Item{item("Assets/Prefabs/Hero.prefab", assetdiff.StatusModified)}
	fsys, files := mapFS(now)
	g := Build(fsys, files, Options{})

	failing := func(context.Context, assetdiff.Ref, int64) ([]byte, error) { return nil, errors.New("locked") }
	out, err := ComputeImpact(context.Background(), g, changes, failing)
	require.NoError(t, err)
	assert.True(t, out.Truncated)
	assert.Empty(t, out.Broken, "guid c was already unresolved; an unreadable old side must not make it new")

	g.opts.MaxFiles = 0
	out, err = ComputeImpact(context.Background(), g, changes, mapLoader(old))
	require.NoError(t, err)
	assert.True(t, out.Truncated)
	assert.Empty(t, out.Broken, "a source past the cap has no old side either")

	gone := without(old, "Assets/Prefabs/Hero.prefab")
	out = impact(t, gone, now, changes...)
	assert.Equal(t, []BrokenRef{{From: "Assets/Prefabs/Hero.prefab", Raw: "guid:" + guid("c"), Reason: ReasonUnresolved}}, out.Broken,
		"an old side that did not exist is known and empty")
}

func TestNeverUnused(t *testing.T) {
	for p, want := range map[string]bool{
		"Assets/Scenes/Main.unity":       true,
		"Assets/a.cs":                    true,
		"Assets/Resources/x.png":         true,
		"Assets/Editor/Icons/i.png":      true,
		"addons/plugin/icon.png":         true,
		"Assets/Art/x.png":               false,
		"Assets/StreamingAssets/v.mp4":   true,
		"Assets/Plugins/Android/lib.aar": true,
	} {
		assert.Equal(t, want, neverUnused(p), p)
	}
}
