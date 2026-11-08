package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

func metaServer(t *testing.T, snapshot map[string]string, seen *[]string) (*Server, string) {
	t.Helper()
	return sidecarServer(t, "Assets/crate.fbx.meta", snapshot, seen)
}

func sidecarServer(t *testing.T, tracked string, snapshot map[string]string, seen *[]string) (*Server, string) {
	t.Helper()
	root := newTestCloneWithContent(t, tracked, snapshot[tracked])
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(_ context.Context, _ string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		if seen != nil {
			*seen = append(*seen, req.Path)
		}
		content, ok := snapshot[req.Path]
		if !ok {
			return errors.New("not in snapshot")
		}
		return fn(diff.Entry{Path: req.Path, SizeBytes: int64(len(content))}, strings.NewReader(content))
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

func writeNested(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func TestServer_MetaWarnings(t *testing.T) {
	var seen []string
	srv, root := metaServer(t, map[string]string{"Assets/crate.fbx.meta": "guid: 1111\n"}, &seen)
	writeNested(t, root, "ProjectSettings/ProjectVersion.txt", "m_EditorVersion: 6000.0.1f1")
	writeNested(t, root, "Assets/hero.png", "PNG")
	writeNested(t, root, "Assets/crate.fbx.meta", "guid: 2222\n")
	writeNested(t, root, "Assets/crate.fbx", "FBX")

	res, err := srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root})
	require.NoError(t, err)
	kinds := map[string]string{}
	for _, w := range res.GetWarnings() {
		kinds[w.GetPath()] = w.GetKind()
	}
	assert.Equal(t, "guid-changed", kinds["Assets/crate.fbx.meta"])
	assert.Equal(t, "missing-meta", kinds["Assets/hero.png"])
	assert.Contains(t, seen, "Assets/crate.fbx.meta")
}

func TestServer_MetaWarningsOutsideUnity(t *testing.T) {
	srv, root := metaServer(t, nil, nil)
	writeNested(t, root, "README.md", "hi")
	res, err := srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root})
	require.NoError(t, err)
	assert.NotNil(t, res.GetWarnings())
	assert.Empty(t, res.GetWarnings())
}

func TestServer_MetaWarningsErrors(t *testing.T) {
	srv := newOpServer(t, RepoOps{})
	_, err := srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: t.TempDir()})
	require.Error(t, err)

	root := newTestClone(t)
	_, err = srv.repos.watch(root)
	require.NoError(t, err)
	_, err = srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "diff runner missing")

	srv2, root2 := metaServer(t, nil, nil)
	require.NoError(t, os.RemoveAll(filepath.Join(root2, ".nipa")))
	_, err = srv2.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root2})
	require.Error(t, err)
}

func TestMetaChanges(t *testing.T) {
	changes, unity := metaChanges(&daemonpb.StatusResponse{
		Staged:    []string{"Assets/a.png", "Assets/b.png"},
		Added:     []string{"Assets/a.png"},
		Deleted:   []string{"Assets/d.png"},
		Modified:  []string{"x/Assets/m.png"},
		Missing:   []string{"Assets/g.png"},
		Untracked: []string{"Assets/u.png"},
	})
	assert.True(t, unity)
	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.Status
	}
	assert.Equal(t, map[string]string{
		"Assets/a.png": "added", "Assets/b.png": "modified", "Assets/d.png": "deleted",
		"x/Assets/m.png": "modified", "Assets/g.png": "deleted", "Assets/u.png": "untracked",
	}, got)

	_, unity = metaChanges(&daemonpb.StatusResponse{Modified: []string{"src/main.go"}})
	assert.False(t, unity)
}

func TestReadSnapshotSmall(t *testing.T) {
	run := func(entry diff.Entry, content string) ([]byte, error) {
		return readSnapshotSmall(context.Background(), &stubDiffRunner{readBlob: func(_ context.Context, _ string, _ usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
			return fn(entry, strings.NewReader(content))
		}}, "/r", "a.meta")
	}
	data, err := run(diff.Entry{SizeBytes: 3}, "abc")
	require.NoError(t, err)
	assert.Equal(t, "abc", string(data))

	_, err = run(diff.Entry{SizeBytes: maxMetaBytes + 1}, "x")
	assert.ErrorIs(t, err, errMetaTooLarge)

	_, err = run(diff.Entry{}, strings.Repeat("x", maxMetaBytes+1))
	assert.ErrorIs(t, err, errMetaTooLarge)
}

func TestServer_MetaWarningsGUIDBudget(t *testing.T) {
	var seen []string
	srv, root := metaServer(t, map[string]string{"Assets/crate.fbx.meta": "guid: 1\n"}, &seen)
	for i := 0; i < maxGUIDChecks+2; i++ {
		writeNested(t, root, "Assets/m"+string(rune('a'+i%26))+string(rune('a'+i/26))+".meta", "guid: 2\n")
	}
	_, err := srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(seen), maxGUIDChecks)
}

func warningKinds(res *daemonpb.MetaWarningsResponse) map[string]string {
	kinds := map[string]string{}
	for _, w := range res.GetWarnings() {
		kinds[w.GetPath()] = w.GetKind()
	}
	return kinds
}

func TestServer_MetaWarningsGodot(t *testing.T) {
	var seen []string
	old := "[remap]\n\nimporter=\"texture\"\ntype=\"CompressedTexture2D\"\nuid=\"uid://cvdk8a1nd4yxb\"\n"
	srv, root := sidecarServer(t, "game/icon.png.import", map[string]string{"game/icon.png.import": old}, &seen)
	writeNested(t, root, "game/project.godot", "config_version=5\n")
	writeNested(t, root, "game/icon.png", "PNG")
	writeNested(t, root, "game/icon.png.import", strings.Replace(old, "cvdk8a1nd4yxb", "b2q9m4x7c1v0z", 1))
	writeNested(t, root, "game/player.gd", "extends Node\n")
	writeNested(t, root, "Assets/hero.png", "PNG")
	writeNested(t, root, "ProjectSettings/ProjectVersion.txt", "m_EditorVersion: 6000.0.1f1")

	res, err := srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root, Engine: "Godot"})
	require.NoError(t, err)
	kinds := warningKinds(res)
	assert.Equal(t, map[string]string{"game/icon.png.import": "uid-changed", "game/player.gd": "missing-sidecar"}, kinds)
	assert.Contains(t, seen, "game/icon.png.import")

	res, err = srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Assets/hero.png": "missing-meta"}, warningKinds(res), "unity marker at the root")

	writeNested(t, root, "project.godot", "config_version=5\n")
	res, err = srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root})
	require.NoError(t, err)
	kinds = warningKinds(res)
	assert.Equal(t, "missing-sidecar", kinds["Assets/hero.png"], "project.godot wins and the root project covers Assets/")
	assert.Equal(t, "missing-sidecar", kinds["game/player.gd"])

	res, err = srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root, Engine: "unity"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Assets/hero.png": "missing-meta"}, warningKinds(res), "an explicit engine wins")

	_, err = srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root, Engine: "unreal"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestServer_MetaWarningsWithoutMarkers(t *testing.T) {
	srv, root := sidecarServer(t, "README.md", nil, nil)
	writeNested(t, root, "unity/ProjectSettings/ProjectVersion.txt", "m_EditorVersion: 6000.0.1f1")
	writeNested(t, root, "unity/Assets/hero.png", "PNG")
	writeNested(t, root, "godot/project.godot", "config_version=5\n")
	writeNested(t, root, "godot/hero.png", "PNG")
	res, err := srv.MetaWarnings(context.Background(), &daemonpb.MetaWarningsRequest{Root: root})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"unity/Assets/hero.png": "missing-meta", "godot/hero.png": "missing-sidecar"}, warningKinds(res))
}

func TestDetectEngine(t *testing.T) {
	root := t.TempDir()
	assert.Equal(t, "", detectEngine(root))
	writeNested(t, root, "ProjectSettings/ProjectVersion.txt", "m_EditorVersion: 6000.0.1f1")
	assert.Equal(t, engineUnity, detectEngine(root))
	writeNested(t, root, "project.godot", "config_version=5\n")
	assert.Equal(t, engineGodot, detectEngine(root))

	dirs := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dirs, "project.godot"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dirs, "ProjectSettings", "ProjectVersion.txt"), 0o755))
	assert.Equal(t, "", detectEngine(dirs))
}
