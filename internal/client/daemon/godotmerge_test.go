package daemon

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/assetdiff"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

const (
	sceneBase   = "[gd_scene format=3]\n\n[node name=\"Root\" type=\"Node2D\"]\n\n[node name=\"A\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)\n\n[node name=\"B\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)\n"
	sceneOurs   = "[gd_scene format=3]\n\n[node name=\"Root\" type=\"Node2D\"]\n\n[node name=\"A\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(5, 0)\n\n[node name=\"B\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)\n"
	sceneTheirs = "[gd_scene format=3]\n\n[node name=\"Root\" type=\"Node2D\"]\n\n[node name=\"A\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(0, 0)\n\n[node name=\"B\" type=\"Sprite2D\" parent=\".\"]\nposition = Vector2(9, 0)\n"
)

func godotConflictServer(t *testing.T, versions map[string]string) (*Server, string) {
	t.Helper()
	root := newTestClone(t)
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveMergeState(&clientDomain.MergeState{
		SourceBranch:   "feature",
		SourceCommitID: "src",
		BaseCommitID:   "base",
		TargetCommitID: "tgt",
		Conflicts:      []string{"level.tscn", "theme.tres", "hero.tscn", "notes.txt"},
		ConflictBinary: []string{"hero.tscn"},
	}))
	require.NoError(t, lr.Close())
	writeWorkingFile(t, root, "level.tscn", "<<<<<<< ours\n")
	return conflictServer(t, root, versions), root
}

func TestServer_MergeGodotResourceClean(t *testing.T) {
	srv, root := godotConflictServer(t, map[string]string{
		"base:level.tscn": sceneBase,
		"tgt:level.tscn":  sceneOurs,
		"src:level.tscn":  sceneTheirs,
	})
	resp, err := srv.MergeGodotResource(context.Background(), &daemonpb.MergeGodotResourceRequest{Root: root, Path: "./level.tscn"})
	require.NoError(t, err)
	require.True(t, resp.GetClean())
	require.Empty(t, resp.GetConflicts())
	require.Contains(t, string(resp.GetMerged()), "position = Vector2(5, 0)")
	require.Contains(t, string(resp.GetMerged()), "position = Vector2(9, 0)")
	require.Equal(t, "<<<<<<< ours\n", readWorkingFile(t, root, "level.tscn"), "the working copy is left alone")
}

func TestServer_MergeGodotResourceConflicts(t *testing.T) {
	srv, root := godotConflictServer(t, map[string]string{
		"base:level.tscn": sceneBase,
		"tgt:level.tscn":  sceneOurs,
		"src:level.tscn":  strings.Replace(sceneOurs, "Vector2(5, 0)", "Vector2(7, 0)", 1),
		"tgt:theme.tres":  "[gd_resource type=\"Theme\" format=3]\n\n[resource]\na = 1\n",
	})
	ctx := context.Background()
	resp, err := srv.MergeGodotResource(ctx, &daemonpb.MergeGodotResourceRequest{Root: root, Path: "level.tscn"})
	require.NoError(t, err)
	require.False(t, resp.GetClean())
	require.Len(t, resp.GetConflicts(), 1)
	require.Equal(t, `node name="A" parent="."`, resp.GetConflicts()[0].GetSection())
	require.Equal(t, "position", resp.GetConflicts()[0].GetKey())
	require.NotContains(t, string(resp.GetMerged()), "<<<<<<<")

	resp, err = srv.MergeGodotResource(ctx, &daemonpb.MergeGodotResourceRequest{Root: root, Path: "theme.tres"})
	require.NoError(t, err)
	require.False(t, resp.GetClean())
	require.Equal(t, "theirs deleted the file: pick ours or theirs", resp.GetConflicts()[0].GetMessage())
}

func TestServer_MergeGodotResourceErrors(t *testing.T) {
	srv, root := godotConflictServer(t, map[string]string{
		"base:level.tscn": "[node name=\"A\"\n",
		"tgt:level.tscn":  sceneOurs,
		"src:level.tscn":  sceneTheirs,
	})
	ctx := context.Background()
	cases := map[string]string{
		"notes.txt":    "not a Godot text scene",
		"":             "not a Godot text scene",
		"missing.tscn": "missing.tscn has no conflict to resolve",
		"hero.tscn":    "stored as binary",
		"level.tscn":   "level.tscn cannot be merged: base: godotres: syntax error",
	}
	for p, want := range cases {
		_, err := srv.MergeGodotResource(ctx, &daemonpb.MergeGodotResourceRequest{Root: root, Path: p})
		require.ErrorContains(t, err, want, p)
		require.Equal(t, codes.InvalidArgument, status.Code(err), p)
	}

	clean := newTestClone(t)
	srv = conflictServer(t, clean, nil)
	_, err := srv.MergeGodotResource(ctx, &daemonpb.MergeGodotResourceRequest{Root: clean, Path: "level.tscn"})
	require.ErrorContains(t, err, "no merge or revert is waiting")

	_, err = srv.MergeGodotResource(ctx, &daemonpb.MergeGodotResourceRequest{Root: t.TempDir(), Path: "level.tscn"})
	require.Error(t, err)
}

func TestServer_MergeGodotResourceReadFailures(t *testing.T) {
	_, root := godotConflictServer(t, nil)
	offline := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(context.Context, string, usecase.BlobRequest, func(diff.Entry, io.Reader) error) error {
		return errors.New("offline")
	}}})
	_, err := offline.repos.watch(root)
	require.NoError(t, err)
	_, err = offline.MergeGodotResource(context.Background(), &daemonpb.MergeGodotResourceRequest{Root: root, Path: "level.tscn"})
	require.ErrorContains(t, err, "offline")

	huge := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(_ context.Context, _ string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		return fn(diff.Entry{Path: req.Path}, io.LimitReader(zeros{}, assetdiff.MaxGodotMergeBytes+2))
	}}})
	_, err = huge.repos.watch(root)
	require.NoError(t, err)
	_, err = huge.MergeGodotResource(context.Background(), &daemonpb.MergeGodotResourceRequest{Root: root, Path: "level.tscn"})
	require.ErrorContains(t, err, "too large to merge")

	bare := newOpServer(t, RepoOps{})
	_, err = bare.repos.watch(root)
	require.NoError(t, err)
	_, err = bare.MergeGodotResource(context.Background(), &daemonpb.MergeGodotResourceRequest{Root: root, Path: "level.tscn"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestServer_MergeGodotResourceRevertWithoutBase(t *testing.T) {
	root := newTestClone(t)
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveRevertState(&clientDomain.RevertState{
		Targets:         []clientDomain.CommitRef{{ID: ""}},
		CurrentCommitID: "cur",
		TheirsCommitID:  "par",
		Conflicts:       []string{"level.tscn"},
	}))
	require.NoError(t, lr.Close())
	srv := conflictServer(t, root, map[string]string{"cur:level.tscn": sceneOurs, "par:level.tscn": sceneOurs})
	resp, err := srv.MergeGodotResource(context.Background(), &daemonpb.MergeGodotResourceRequest{Root: root, Path: "level.tscn"})
	require.NoError(t, err)
	require.True(t, resp.GetClean())
	require.Equal(t, sceneOurs, string(resp.GetMerged()))
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}
