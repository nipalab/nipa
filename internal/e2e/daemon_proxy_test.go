package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

func proxyTreePaths(root *pb.TreeManifest) []string {
	var out []string
	if root == nil {
		return out
	}
	for _, f := range root.GetFiles() {
		out = append(out, f.GetPath())
	}
	for _, sub := range root.GetSubTrees() {
		out = append(out, proxyTreePaths(sub)...)
	}
	return out
}

func TestEndToEnd_DaemonProxyBranchesMergeRequestsLocks(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))
	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))

	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	repoUc := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())

	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repoUc.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))
	seed := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, repoUc.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, seed))

	// Main gets a binary asset so the lock surface has something to lock.
	writeFile(t, seed, "assets/logo.png", string([]byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0x02, 0x03}))
	stagePath(t, seed, "assets/logo.png")
	seedPush := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, seedPush.Run(ctx, seed, "add logo"))

	client, dctx := startDaemon(t, auth, grpcClient)
	_, err = client.WatchRepo(dctx, &daemonpb.WatchRepoRequest{Root: target})
	require.NoError(t, err)

	// Branch read and create.
	branches, err := client.ProxyBranchList(dctx, &daemonpb.ProxyBranchListRequest{
		Root:    target,
		Request: &pb.GetListBranchRequest{Limit: 100},
	})
	require.NoError(t, err)
	var names []string
	for _, b := range branches.GetResponse().GetBranches() {
		names = append(names, b.GetName())
	}
	require.Contains(t, names, "main")

	created, err := client.ProxyBranchCreate(dctx, &daemonpb.ProxyBranchCreateRequest{
		Root:    target,
		Request: &pb.CreateBranchRequest{Name: "feature"},
	})
	require.NoError(t, err)
	require.Equal(t, "feature", created.GetResponse().GetBranch().GetName())

	// The seed clone pushes work on the new branch.
	seedUpdate := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, seedUpdate.Switch(ctx, seed, "feature"))
	writeFile(t, seed, "feature.txt", "feature work\n")
	stagePath(t, seed, "feature.txt")
	require.NoError(t, seedPush.Run(ctx, seed, "feature work"))

	// Tree, log, commit and merge-base proxies.
	tree, err := client.ProxyTreeManifest(dctx, &daemonpb.ProxyTreeManifestRequest{
		Root:    target,
		Request: &pb.GetTreeManifestRequest{Branch: "main", Recursive: true},
	})
	require.NoError(t, err)
	require.Contains(t, strings.Join(proxyTreePaths(tree.GetResponse().GetRootTree()), "\n"), "logo.png")

	log, err := client.ProxyCommitLog(dctx, &daemonpb.ProxyCommitLogRequest{
		Root:    target,
		Request: &pb.GetCommitLogRequest{Branch: "main", Limit: 10},
	})
	require.NoError(t, err)
	require.NotEmpty(t, log.GetResponse().GetCommits())

	mainHead, err := grpcClient.GetBranchByName(ctx, e2eOrgSlug, e2eProjectSlug, "main")
	require.NoError(t, err)
	require.NotNil(t, mainHead.CommitID)

	commit, err := client.ProxyCommitGet(dctx, &daemonpb.ProxyCommitGetRequest{
		Root:    target,
		Request: &pb.GetCommitRequest{CommitId: mainHead.CommitID.Base36()},
	})
	require.NoError(t, err)
	require.Equal(t, mainHead.CommitID.Base36(), commit.GetResponse().GetCommit().GetCommitId())
	require.NotNil(t, commit.GetResponse().GetRootTree())

	walk, err := client.ProxyCommitWalk(dctx, &daemonpb.ProxyCommitWalkRequest{
		Root:    target,
		Request: &pb.WalkCommitsRequest{StartCommitId: mainHead.CommitID.Base36(), Limit: 10},
	})
	require.NoError(t, err)
	require.NotEmpty(t, walk.GetResponse().GetCommits())

	base, err := client.ProxyMergeBase(dctx, &daemonpb.ProxyMergeBaseRequest{
		Root:    target,
		Request: &pb.GetMergeBaseRequest{TargetBranch: "main", SourceBranch: "feature"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, base.GetResponse().GetMergeBaseCommitId())
	require.NotEmpty(t, base.GetResponse().GetTargetCommitId())
	require.NotEmpty(t, base.GetResponse().GetSourceCommitId())

	// Merge request lifecycle: create, list, reviews, close, then merge.
	createMR := func() *pb.MergeRequestDetail {
		t.Helper()
		res, err := client.ProxyMergeRequestCreate(dctx, &daemonpb.ProxyMergeRequestCreateRequest{
			Root: target,
			Request: &pb.CreateMergeRequestRequest{
				Title:        "Feature",
				Description:  "the feature branch",
				SourceBranch: "feature",
				TargetBranch: "main",
			},
		})
		require.NoError(t, err)
		return res.GetResponse().GetMergeRequest()
	}

	first := createMR()
	require.NotZero(t, first.GetNumber())
	require.Equal(t, "open", first.GetStatus())

	list, err := client.ProxyMergeRequestList(dctx, &daemonpb.ProxyMergeRequestListRequest{
		Root:    target,
		Request: &pb.ListMergeRequestsRequest{Status: "open", Limit: 10},
	})
	require.NoError(t, err)
	require.Len(t, list.GetResponse().GetMergeRequests(), 1)

	reviews, err := client.ProxyMergeRequestReviews(dctx, &daemonpb.ProxyMergeRequestReviewsRequest{
		Root:    target,
		Request: &pb.ListMergeRequestReviewsRequest{Number: first.GetNumber()},
	})
	require.NoError(t, err)
	require.Empty(t, reviews.GetResponse().GetReviews())

	state, err := client.ProxyMergeRequestReviewState(dctx, &daemonpb.ProxyMergeRequestReviewStateRequest{
		Root:    target,
		Request: &pb.GetMergeRequestReviewStateRequest{Number: first.GetNumber()},
	})
	require.NoError(t, err)
	require.EqualValues(t, 0, state.GetResponse().GetState().GetApprovals())
	require.NotEmpty(t, state.GetResponse().GetState().GetHeadCommitId())

	threads, err := client.ProxyMergeRequestThreads(dctx, &daemonpb.ProxyMergeRequestThreadsRequest{
		Root:    target,
		Request: &pb.ListMergeRequestThreadsRequest{Number: first.GetNumber()},
	})
	require.NoError(t, err)
	require.Empty(t, threads.GetResponse().GetThreads())

	closed, err := client.ProxyMergeRequestClose(dctx, &daemonpb.ProxyMergeRequestCloseRequest{
		Root:    target,
		Request: &pb.CloseMergeRequestRequest{Number: first.GetNumber()},
	})
	require.NoError(t, err)
	require.Equal(t, "closed", closed.GetResponse().GetMergeRequest().GetStatus())

	second := createMR()
	merged, err := client.ProxyMergeRequestMerge(dctx, &daemonpb.ProxyMergeRequestMergeRequest{
		Root:    target,
		Request: &pb.MergeMergeRequestRequest{Number: second.GetNumber()},
	})
	require.NoError(t, err)
	require.Equal(t, "merged", merged.GetResponse().GetMergeRequest().GetStatus())

	// File locks.
	lock, err := client.ProxyLockFile(dctx, &daemonpb.ProxyLockFileRequest{
		Root:    target,
		Request: &pb.LockFileRequest{Path: "assets/logo.png"},
	})
	require.NoError(t, err)
	require.Equal(t, "assets/logo.png", lock.GetResponse().GetLock().GetPath())
	require.True(t, lock.GetResponse().GetLock().GetGlobal())
	require.NotEmpty(t, lock.GetResponse().GetLock().GetHeldBy())

	locks, err := client.ProxyListFileLocks(dctx, &daemonpb.ProxyListFileLocksRequest{Root: target})
	require.NoError(t, err)
	require.Len(t, locks.GetResponse().GetLocks(), 1)

	_, err = client.ProxyUnlockFile(dctx, &daemonpb.ProxyUnlockFileRequest{
		Root:    target,
		Request: &pb.UnlockFileRequest{Path: "assets/logo.png"},
	})
	require.NoError(t, err)
	locks, err = client.ProxyListFileLocks(dctx, &daemonpb.ProxyListFileLocksRequest{Root: target})
	require.NoError(t, err)
	require.Empty(t, locks.GetResponse().GetLocks())

	// Branch delete cleans the feature branch up.
	_, err = client.ProxyBranchDelete(dctx, &daemonpb.ProxyBranchDeleteRequest{
		Root:    target,
		Request: &pb.DeleteBranchRequest{Name: "feature"},
	})
	require.NoError(t, err)
	branches, err = client.ProxyBranchList(dctx, &daemonpb.ProxyBranchListRequest{
		Root:    target,
		Request: &pb.GetListBranchRequest{Limit: 100},
	})
	require.NoError(t, err)
	names = names[:0]
	for _, b := range branches.GetResponse().GetBranches() {
		names = append(names, b.GetName())
	}
	require.NotContains(t, names, "feature")
}
