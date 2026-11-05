package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

func conflictedClone(t *testing.T) string {
	t.Helper()
	root := newTestClone(t)
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveMergeState(&clientDomain.MergeState{
		SourceBranch:   "feature",
		SourceCommitID: "src",
		BaseCommitID:   "base",
		TargetCommitID: "tgt",
		Conflicts:      []string{"tracked.txt", "art/hero.png", "new.txt"},
		ConflictKinds: map[string]string{
			"tracked.txt":  clientDomain.ConflictText,
			"art/hero.png": clientDomain.ConflictBinary,
			"new.txt":      clientDomain.ConflictDeleteModify,
		},
		ConflictBinary: []string{"art/hero.png"},
	}))
	require.NoError(t, lr.Close())
	writeWorkingFile(t, root, "tracked.txt", "<<<<<<< ours\nA\n=======\nB\n>>>>>>> theirs\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "art"), 0o755))
	writeWorkingFile(t, root, "art/hero.png", "ours-png")
	return root
}

func conflictServer(t *testing.T, root string, versions map[string]string) *Server {
	t.Helper()
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(_ context.Context, _ string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		data, ok := versions[req.Revision+":"+req.Path]
		if !ok {
			missing := clientDomain.NewNotFoundError(req.Path + " not found")
			missing.Cause = usecase.ErrFileMissing
			return missing
		}
		return fn(diff.Entry{Path: req.Path, Mode: 3}, strings.NewReader(data))
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv
}

func TestServer_ConflictsDescribesPendingMerge(t *testing.T) {
	root := conflictedClone(t)
	srv := conflictServer(t, root, nil)

	resp, err := srv.Conflicts(context.Background(), &daemonpb.ConflictsRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, "merge", resp.GetOp())
	require.Equal(t, "base", resp.GetBase().GetId())
	require.Equal(t, "main", resp.GetOurs().GetLabel())
	require.Equal(t, "feature", resp.GetTheirs().GetLabel())
	require.Len(t, resp.GetFiles(), 3)
	require.Equal(t, "text", resp.GetFiles()[0].GetViewer())
	require.Equal(t, "image", resp.GetFiles()[1].GetViewer())
	require.True(t, resp.GetFiles()[1].GetBinary())
	require.Equal(t, "delete_modify", resp.GetFiles()[2].GetKind())
}

func TestServer_ConflictsNothingPending(t *testing.T) {
	root := newTestClone(t)
	srv := conflictServer(t, root, nil)
	resp, err := srv.Conflicts(context.Background(), &daemonpb.ConflictsRequest{Root: root})
	require.NoError(t, err)
	require.Empty(t, resp.GetOp())

	_, err = srv.Conflicts(context.Background(), &daemonpb.ConflictsRequest{Root: t.TempDir()})
	require.Error(t, err)

	_, err = srv.ResolveConflict(context.Background(), &daemonpb.ResolveConflictRequest{Root: root, Path: "tracked.txt", Choice: "ours"})
	require.ErrorContains(t, err, "no merge or revert is waiting")
}

func TestServer_ResolveConflictChoices(t *testing.T) {
	root := conflictedClone(t)
	srv := conflictServer(t, root, map[string]string{
		"src:art/hero.png": "theirs-png",
		"tgt:tracked.txt":  "ours text\n",
		"src:new.txt":      "theirs new\n",
	})
	ctx := context.Background()

	_, err := srv.ResolveConflict(ctx, &daemonpb.ResolveConflictRequest{Root: root, Path: "tracked.txt", Choice: "working"})
	require.ErrorContains(t, err, "conflict markers")

	st, err := srv.ResolveConflict(ctx, &daemonpb.ResolveConflictRequest{Root: root, Path: "tracked.txt", Choice: "content", Content: []byte("A\nB\n")})
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, st.GetResolved())
	require.Equal(t, "A\nB\n", readWorkingFile(t, root, "tracked.txt"))

	_, err = srv.ResolveConflict(ctx, &daemonpb.ResolveConflictRequest{Root: root, Path: "tracked.txt", Choice: "ours"})
	require.NoError(t, err)
	require.Equal(t, "ours text\n", readWorkingFile(t, root, "tracked.txt"))

	_, err = srv.ResolveConflict(ctx, &daemonpb.ResolveConflictRequest{Root: root, Path: "art/hero.png", Choice: "theirs"})
	require.NoError(t, err)
	require.Equal(t, "theirs-png", readWorkingFile(t, root, "art/hero.png"))

	st, err = srv.ResolveConflict(ctx, &daemonpb.ResolveConflictRequest{Root: root, Path: "new.txt", Choice: "theirs"})
	require.NoError(t, err)
	require.Equal(t, "theirs new\n", readWorkingFile(t, root, "new.txt"))
	require.Contains(t, st.GetStaged(), "new.txt")
	require.ElementsMatch(t, []string{"tracked.txt", "art/hero.png", "new.txt"}, st.GetResolved())

	_, err = srv.ResolveConflict(ctx, &daemonpb.ResolveConflictRequest{Root: root, Path: "new.txt", Choice: "ours"})
	require.NoError(t, err, "the side without the file deletes it")
	_, err = os.Stat(filepath.Join(root, "new.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = srv.ResolveConflict(ctx, &daemonpb.ResolveConflictRequest{Root: root, Path: "art/hero.png", Choice: "delete"})
	require.NoError(t, err)

	_, err = srv.ResolveConflict(ctx, &daemonpb.ResolveConflictRequest{Root: root, Path: "art/hero.png", Choice: "mine"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestServer_ResolveConflictKeepsFileOnTransferNotFound(t *testing.T) {
	root := conflictedClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(context.Context, string, usecase.BlobRequest, func(diff.Entry, io.Reader) error) error {
		return clientDomain.NewNotFoundError("chunk not found on the server")
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	_, err = srv.ResolveConflict(context.Background(), &daemonpb.ResolveConflictRequest{Root: root, Path: "art/hero.png", Choice: "theirs"})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, "ours-png", readWorkingFile(t, root, "art/hero.png"))
}

func TestServer_ResolveConflictReadFailures(t *testing.T) {
	root := conflictedClone(t)
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(context.Context, string, usecase.BlobRequest, func(diff.Entry, io.Reader) error) error {
		return errors.New("offline")
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	_, err = srv.ResolveConflict(context.Background(), &daemonpb.ResolveConflictRequest{Root: root, Path: "tracked.txt", Choice: "theirs"})
	require.ErrorContains(t, err, "offline")

	bare := newOpServer(t, RepoOps{})
	_, err = bare.repos.watch(root)
	require.NoError(t, err)
	_, err = bare.ResolveConflict(context.Background(), &daemonpb.ResolveConflictRequest{Root: root, Path: "tracked.txt", Choice: "theirs"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestServer_ResolveConflictTakesDeleteWhenSideHasNoCommit(t *testing.T) {
	root := newTestClone(t)
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveRevertState(&clientDomain.RevertState{
		Targets:   []clientDomain.CommitRef{{ID: "c1"}},
		Conflicts: []string{"tracked.txt"},
	}))
	require.NoError(t, lr.Close())
	writeWorkingFile(t, root, "tracked.txt", "kept")
	srv := conflictServer(t, root, nil)

	resp, err := srv.Conflicts(context.Background(), &daemonpb.ConflictsRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, "revert", resp.GetOp())

	st, err := srv.ResolveConflict(context.Background(), &daemonpb.ResolveConflictRequest{Root: root, Path: "tracked.txt", Choice: "theirs"})
	require.NoError(t, err)
	require.Contains(t, st.GetDeleted(), "tracked.txt")
	require.Equal(t, "revert", st.GetPending())
}

func readWorkingFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}
