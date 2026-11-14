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

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func shelveClone(t *testing.T) (string, *Server, *stubUpdateRunner) {
	t.Helper()
	root := newTestCloneWithContent(t, "tracked.txt", "hello\n")
	writeWorkingFile(t, root, "tracked.txt", "hello\n")
	update := &stubUpdateRunner{}
	srv := newOpServer(t, RepoOps{Update: update})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return root, srv, update
}

func runShelveCreate(t *testing.T, srv *Server, req *daemonpb.ShelveCreateRequest) []*daemonpb.OpEvent {
	t.Helper()
	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.ShelveCreate(req, stream))
	return stream.events()
}

func runShelveApply(t *testing.T, srv *Server, req *daemonpb.ShelveApplyRequest) []*daemonpb.OpEvent {
	t.Helper()
	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.ShelveApply(req, stream))
	return stream.events()
}

func TestServer_ShelveRoundTrip(t *testing.T) {
	ctx := context.Background()
	root, srv, _ := shelveClone(t)
	writeWorkingFile(t, root, "tracked.txt", "edited\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "art"), 0o755))
	writeWorkingFile(t, root, "art/hero.png", "\x00png")

	events := runShelveCreate(t, srv, &daemonpb.ShelveCreateRequest{Root: root, Name: "wip", IncludeUntracked: true})
	require.Equal(t, "shelve", events[0].GetStarted().GetPhase())
	shelf := terminalEvent(t, events).GetResult().GetShelve().GetShelf()
	require.Equal(t, "wip", shelf.GetName())
	require.Equal(t, "main", shelf.GetBranch())
	require.Len(t, shelf.GetEntries(), 2)
	require.Equal(t, "image", shelf.GetEntries()[0].GetKind())
	require.True(t, shelf.GetEntries()[0].GetUntracked())
	require.Equal(t, "modified", shelf.GetEntries()[1].GetStatus())
	require.NoFileExists(t, filepath.Join(root, "art/hero.png"))

	list, err := srv.ShelveList(ctx, &daemonpb.ShelveListRequest{Root: root})
	require.NoError(t, err)
	require.Len(t, list.GetShelves(), 1)
	require.Empty(t, list.GetApplying())

	files, err := srv.ShelveFiles(ctx, &daemonpb.ShelveFilesRequest{Root: root, Id: shelf.GetId()})
	require.NoError(t, err)
	require.Len(t, files.GetFiles(), 2)
	require.Equal(t, "added", files.GetFiles()[0].GetStatus())
	require.Equal(t, "modified", files.GetFiles()[1].GetStatus())
	require.EqualValues(t, 1, files.GetFiles()[1].GetAdditions())

	result := terminalEvent(t, runShelveApply(t, srv, &daemonpb.ShelveApplyRequest{Root: root, Id: shelf.GetId(), Keep: true})).GetResult().GetShelveApply()
	require.False(t, result.GetDropped())
	require.Empty(t, result.GetConflicts())
	data, err := os.ReadFile(filepath.Join(root, "tracked.txt"))
	require.NoError(t, err)
	require.Equal(t, "edited\n", string(data))

	_, err = srv.ShelveDrop(ctx, &daemonpb.ShelveDropRequest{Root: root, Id: shelf.GetId()})
	require.NoError(t, err)
	_, err = srv.ShelveDrop(ctx, &daemonpb.ShelveDropRequest{Root: root, Id: shelf.GetId()})
	require.Error(t, err)
	_, err = srv.ShelveFiles(ctx, &daemonpb.ShelveFilesRequest{Root: root, Id: shelf.GetId()})
	require.Error(t, err)
	_, err = srv.ShelveList(ctx, &daemonpb.ShelveListRequest{Root: t.TempDir()})
	require.Error(t, err)

	last := runShelveApply(t, srv, &daemonpb.ShelveApplyRequest{Root: root, Id: "nope"})
	require.EqualValues(t, 404, last[len(last)-1].GetFailure().GetCode())
	last = runShelveCreate(t, srv, &daemonpb.ShelveCreateRequest{Root: root, Paths: []string{"nothing.txt"}})
	require.EqualValues(t, 400, last[len(last)-1].GetFailure().GetCode())
}

func TestServer_ShelveApplyConflictsAndAbort(t *testing.T) {
	ctx := context.Background()
	root, srv, _ := shelveClone(t)
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	t.Cleanup(func() { _ = lr.Close() })
	shelf, err := lr.CreateShelf(func(func(serverDomain.Hash, []byte) error) (*clientDomain.Shelf, error) {
		return &clientDomain.Shelf{Name: "wip"}, nil
	})
	require.NoError(t, err)
	require.NoError(t, lr.SaveShelfState(&clientDomain.ShelfState{ShelfID: shelf.ID, ShelfName: "wip", Applied: []string{"tracked.txt"}, Conflicts: []string{"tracked.txt"}}))
	writeWorkingFile(t, root, "tracked.txt", "<<<<<<< ours\nA\n=======\nB\n>>>>>>> theirs\n")

	list, err := srv.ShelveList(ctx, &daemonpb.ShelveListRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, shelf.ID, list.GetApplying())
	conflicts, err := srv.Conflicts(ctx, &daemonpb.ConflictsRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, "shelf", conflicts.GetOp())
	require.Equal(t, shelf.ID, conflicts.GetTheirs().GetId())
	_, err = srv.ShelveDrop(ctx, &daemonpb.ShelveDropRequest{Root: root, Id: shelf.ID})
	require.ErrorContains(t, err, "being applied")

	result := terminalEvent(t, runShelveApply(t, srv, &daemonpb.ShelveApplyRequest{Root: root, Abort: true})).GetResult().GetShelveApply()
	require.True(t, result.GetAborted())
	require.Equal(t, "wip", result.GetName())
	data, err := os.ReadFile(filepath.Join(root, "tracked.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello\n", string(data))

	last := runShelveApply(t, srv, &daemonpb.ShelveApplyRequest{Root: root, Abort: true})
	require.Contains(t, last[len(last)-1].GetFailure().GetMessage(), "no shelf apply is waiting")
}

func TestServer_ResolveShelfConflictReadsShelf(t *testing.T) {
	root := newTestClone(t)
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveShelfState(&clientDomain.ShelfState{ShelfID: "abc", ShelfName: "wip", Conflicts: []string{"tracked.txt", "other.txt"}}))
	require.NoError(t, lr.Close())
	writeWorkingFile(t, root, "tracked.txt", "<<<<<<< ours\nA\n=======\nB\n>>>>>>> theirs\n")
	var sources []usecase.BlobSource
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(_ context.Context, _ string, req usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		sources = append(sources, req.Source)
		if req.Source == usecase.BlobSnapshot {
			missing := clientDomain.NewNotFoundError("gone")
			missing.Cause = usecase.ErrFileMissing
			return missing
		}
		return fn(diff.Entry{Path: req.Path, Mode: 2}, strings.NewReader("shelved\n"))
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	_, err = srv.ResolveConflict(context.Background(), &daemonpb.ResolveConflictRequest{Root: root, Path: "tracked.txt", Choice: "theirs"})
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, "tracked.txt"))
	require.NoError(t, err)
	require.Equal(t, "shelved\n", string(data))
	_, err = srv.ResolveConflict(context.Background(), &daemonpb.ResolveConflictRequest{Root: root, Path: "other.txt", Choice: "ours"})
	require.NoError(t, err)
	require.Equal(t, []usecase.BlobSource{usecase.BlobShelf, usecase.BlobSnapshot}, sources)
}

func TestServer_SyncPlan(t *testing.T) {
	ctx := context.Background()
	root, srv, update := shelveClone(t)
	writeWorkingFile(t, root, "tracked.txt", "edited\n")
	var targets []usecase.SyncTarget
	update.targetFn = func(_ context.Context, _ string, target usecase.SyncTarget) (*serverDomain.TreeNode, error) {
		targets = append(targets, target)
		return &serverDomain.TreeNode{FileChildren: []*serverDomain.File{{Name: "tracked.txt", Mode: 2, Chunks: []serverDomain.Chunk{{Hash: serverDomain.Hash{7}}}}}}, nil
	}

	resp, err := srv.SyncPlan(ctx, &daemonpb.SyncPlanRequest{Root: root})
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, resp.GetOverwritten())
	_, err = srv.SyncPlan(ctx, &daemonpb.SyncPlanRequest{Root: root, Branch: "feature"})
	require.NoError(t, err)
	require.Equal(t, []usecase.SyncTarget{{}, {Branch: "feature"}}, targets)

	update.targetFn = func(context.Context, string, usecase.SyncTarget) (*serverDomain.TreeNode, error) {
		return nil, errors.New("offline")
	}
	_, err = srv.SyncPlan(ctx, &daemonpb.SyncPlanRequest{Root: root, Tag: "v1"})
	require.ErrorContains(t, err, "offline")

	bare := newOpServer(t, RepoOps{})
	_, err = bare.repos.watch(root)
	require.NoError(t, err)
	_, err = bare.SyncPlan(ctx, &daemonpb.SyncPlanRequest{Root: root})
	require.ErrorContains(t, err, "not configured")
}

func TestShelfToPB(t *testing.T) {
	pb := shelfToPB(clientDomain.Shelf{
		ID:   "x",
		Head: &clientDomain.HeadRef{Kind: clientDomain.HeadKindTag, Name: "v1"},
		Entries: []clientDomain.ShelfEntry{
			{Path: "gone.png", Status: clientDomain.ShelfDeleted, Base: &clientDomain.ShelfFile{SizeBytes: 4, IsBinary: true}},
			{Path: "x.txt", Status: clientDomain.ShelfModified},
		},
	})
	require.Equal(t, "v1", pb.GetTag())
	require.EqualValues(t, 4, pb.GetEntries()[0].GetSizeBytes())
	require.Equal(t, "image", pb.GetEntries()[0].GetKind())
	require.Equal(t, "text", pb.GetEntries()[1].GetKind())
}
