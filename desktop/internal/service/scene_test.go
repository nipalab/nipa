package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

const oldLevel = `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!1 &100
GameObject:
  m_Component:
  - component: {fileID: 101}
  - component: {fileID: 102}
  m_Name: Player
--- !u!4 &101
Transform:
  m_GameObject: {fileID: 100}
  m_LocalPosition: {x: 0, y: 1, z: 0}
  m_Father: {fileID: 0}
--- !u!114 &102
MonoBehaviour:
  m_GameObject: {fileID: 100}
  m_Script: {fileID: 11500000, guid: 5c1a0f3b2d4e4f6a8b9c0d1e2f3a4b5c, type: 3}
  speed: 5
`

const newLevel = `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!1 &100
GameObject:
  m_Component:
  - component: {fileID: 101}
  - component: {fileID: 102}
  m_Name: Player
--- !u!4 &101
Transform:
  m_GameObject: {fileID: 100}
  m_LocalPosition: {x: 2, y: 1, z: 0}
  m_Father: {fileID: 0}
--- !u!114 &102
MonoBehaviour:
  m_GameObject: {fileID: 100}
  m_Script: {fileID: 11500000, guid: 5c1a0f3b2d4e4f6a8b9c0d1e2f3a4b5c, type: 3}
  speed: 7
  weapon: {fileID: 11400000, guid: aa11bb22cc33dd44ee55ff6677889900, type: 2}
`

func writeMeta(t *testing.T, dir, rel, guid string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel)+".meta")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("fileFormatVersion: 2\nguid: "+guid+"\n"), 0o644))
}

func TestService_SceneDiff(t *testing.T) {
	root := t.TempDir()
	writeMeta(t, root, "game/Assets/Scripts/PlayerController.cs", "5c1a0f3b2d4e4f6a8b9c0d1e2f3a4b5c")
	writeMeta(t, root, "game/Assets/Scripts", "dddddddddddddddddddddddddddddddd")
	writeMeta(t, root, "game/Packages/com.acme.tools/Runtime/Tool.cs", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	require.NoError(t, os.WriteFile(filepath.Join(root, "game/Assets/broken.meta"), []byte("nothing"), 0o644))

	svc, d := newModels(t, map[string]string{
		"game/Assets/Level.unity":     newLevel,
		"game/Assets/old/Level.unity": oldLevel,
		"game/Assets/Bad.unity":       "%YAML 1.1\n--- !u!x &1\n",
		"game/Assets/Binary.asset":    "UnityFS\x00",
	})
	out, err := svc.SceneDiff(ctx, root, &ImageRef{Source: "snapshot", Path: "game/Assets/old/Level.unity"}, &ImageRef{Source: "working", Path: "game/Assets/Level.unity"})
	require.NoError(t, err)
	assert.Equal(t, "unity", out.Engine)
	assert.Equal(t, 0, out.Added)
	assert.Equal(t, 2, out.Changed)
	require.Len(t, out.Changes, 2)
	assert.Equal(t, SceneChange{Status: "changed", ID: "102", Path: "Player > PlayerController", Type: "PlayerController", Props: []PropChange{
		{Key: "speed", Old: "5", New: "7"},
		{Key: "weapon", New: "guid:aa11bb22cc33dd44ee55ff6677889900"},
	}}, out.Changes[0])
	assert.Equal(t, []RefChange{{Path: "guid:aa11bb22cc33dd44ee55ff6677889900", Status: "added"}}, out.Refs)
	assert.Contains(t, out.Objects, SceneObject{ID: "100", Path: "Player", Type: "GameObject"})

	index := svc.guidIndex(root, "game")
	assert.Equal(t, "Packages/com.acme.tools/Runtime/Tool.cs", index["eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"])
	assert.Equal(t, "Assets/Scripts", index["dddddddddddddddddddddddddddddddd"])
	assert.Len(t, index, 3)

	writeMeta(t, root, "game/Assets/Data/Sword.asset", "aa11bb22cc33dd44ee55ff6677889900")
	out, err = svc.SceneDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "game/Assets/Level.unity"})
	require.NoError(t, err)
	assert.Equal(t, 3, out.Added)
	assert.Contains(t, out.Refs, RefChange{Path: "guid:aa11bb22cc33dd44ee55ff6677889900", Status: "added"}, "index is cached")

	d.changes <- &daemonpb.RepoChange{Paths: []string{"game/Assets/Data/Sword.asset.meta"}}
	assert.Eventually(t, func() bool {
		out, err := svc.SceneDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "game/Assets/Level.unity"})
		require.NoError(t, err)
		for _, r := range out.Refs {
			if r.Path == "Assets/Data/Sword.asset" {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)

	out, err = svc.SceneDiff(ctx, root, &ImageRef{Source: "snapshot", Path: "game/Assets/gone.unity"}, &ImageRef{Source: "working", Path: "game/Assets/Level.unity"})
	require.NoError(t, err)
	assert.Equal(t, 3, out.Added)

	out, err = svc.SceneDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "game/Assets/Bad.unity"})
	require.NoError(t, err)
	assert.Contains(t, out.Error, "invalid scene")
	assert.Empty(t, out.Changes)

	out, err = svc.SceneDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "game/Assets/Binary.asset"})
	require.NoError(t, err)
	assert.Contains(t, out.Error, "Force Text")

	d.sizes["game/Assets/Level.unity"] = maxSceneBytes + 1
	out, err = svc.SceneDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "game/Assets/Level.unity"})
	require.NoError(t, err)
	assert.Contains(t, out.Error, "too large")

	_, err = svc.SceneDiff(ctx, root, nil, nil)
	assert.Error(t, err)

	d.err = status.Error(codes.Internal, "boom")
	_, err = svc.SceneDiff(ctx, root, nil, &ImageRef{Source: "working", Path: "game/Assets/Level.unity"})
	assert.ErrorContains(t, err, "boom")
}

func TestUnityProjectAndInvalidate(t *testing.T) {
	assert.Equal(t, "game", unityProject("game/Assets/Level.unity"))
	assert.Equal(t, "", unityProject("Packages/com.acme/Thing.asset"))
	assert.Equal(t, "", unityProject("Level.unity"))

	svc := New(nil, &memStore{}, nil)
	svc.guidIndex("/missing", "")
	svc.guidIndex("/other", "")
	svc.invalidateGUIDs("/missing", []string{"a.cs"})
	assert.Len(t, svc.guids.cache, 2)
	svc.invalidateGUIDs("/missing", nil)
	assert.Len(t, svc.guids.cache, 1)
	assert.Equal(t, "", readGUID("/missing/file.meta"))
}

func TestGUIDIndexDropsBuildRacingInvalidation(t *testing.T) {
	svc := New(nil, &memStore{}, nil)
	builds := 0
	svc.guids.build = func(string) map[string]string {
		builds++
		if builds == 1 {
			svc.invalidateGUIDs("/r", []string{"Assets/New.png.meta"})
		}
		return map[string]string{"g": "stale"}
	}
	assert.Equal(t, "stale", svc.guidIndex("/r", "")["g"])
	assert.Empty(t, svc.guids.cache, "a build that raced an invalidation is not cached")
	svc.guidIndex("/r", "")
	svc.guidIndex("/r", "")
	assert.Equal(t, 2, builds)
}
