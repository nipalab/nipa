package e2e

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

// TestEndToEnd_DaemonFullFlow drives one clone through the whole daemon
// surface: watch, status, stage, lock, push, update, the merge-request proxy
// and gracefully shutting the daemon down.
func TestEndToEnd_DaemonFullFlow(t *testing.T) {
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

	// Seed main with a text and a binary file before the target clone exists.
	seed := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, repoUc.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, seed))
	writeFile(t, seed, "a.txt", "hello\n")
	writeFile(t, seed, "assets/logo.png", string([]byte{0x89, 'P', 'N', 'G', 0x00, 0x01}))
	stagePath(t, seed, "a.txt")
	stagePath(t, seed, "assets/logo.png")
	seedPush := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, seedPush.Run(ctx, seed, "seed main"))

	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repoUc.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))

	client, dctx := startDaemon(t, auth, grpcClient)
	_, err = client.WatchRepo(dctx, &daemonpb.WatchRepoRequest{Root: target})
	require.NoError(t, err)

	// Status: edit both tracked files and add an untracked one.
	writeFile(t, target, "a.txt", "hello from the target\n")
	writeFile(t, target, "assets/logo.png", string([]byte{0x89, 'P', 'N', 'G', 0x00, 0x02}))
	writeFile(t, target, "local.txt", "draft\n")
	st, err := client.Status(dctx, &daemonpb.StatusRequest{Root: target})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt", "assets/logo.png"}, st.GetModified())
	require.Equal(t, []string{"local.txt"}, st.GetUntracked())

	// Stage everything and lock the binary.
	st, err = client.Stage(dctx, &daemonpb.StageRequest{
		Root: target,
		Add:  []string{"a.txt", "assets/logo.png", "local.txt"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt", "assets/logo.png", "local.txt"}, st.GetStaged())

	lock, err := client.ProxyLockFile(dctx, &daemonpb.ProxyLockFileRequest{
		Root:    target,
		Request: &pb.LockFileRequest{Path: "assets/logo.png"},
	})
	require.NoError(t, err)
	require.True(t, lock.GetResponse().GetLock().GetGlobal())

	// Push through the daemon while holding the binary lock.
	pushStream, err := client.Push(dctx, &daemonpb.PushRequest{Root: target, Message: "full flow push"})
	require.NoError(t, err)
	push := terminalResult(t, collectOpEvents(t, pushStream)).GetPush()
	require.NotEmpty(t, push.GetCommitId())

	head, err := grpcClient.GetBranchByName(ctx, e2eOrgSlug, e2eProjectSlug, "main")
	require.NoError(t, err)
	require.NotNil(t, head.CommitID)
	require.Equal(t, head.CommitID.Base36(), push.GetCommitId())

	// Update through the daemon settles on the same head.
	updateStream, err := client.Update(dctx, &daemonpb.UpdateRequest{Root: target})
	require.NoError(t, err)
	sync := terminalResult(t, collectOpEvents(t, updateStream)).GetSync()
	require.Equal(t, push.GetCommitId(), sync.GetCommitId())

	// The landed push releases the pusher's own exact-path lock.
	locks, err := client.ProxyListFileLocks(dctx, &daemonpb.ProxyListFileLocksRequest{Root: target})
	require.NoError(t, err)
	require.Empty(t, locks.GetResponse().GetLocks())

	// Run the merge-request lifecycle through the proxy.
	_, err = client.ProxyBranchCreate(dctx, &daemonpb.ProxyBranchCreateRequest{
		Root:    target,
		Request: &pb.CreateBranchRequest{Name: "feature"},
	})
	require.NoError(t, err)
	seedUpdate := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, seedUpdate.Switch(ctx, seed, "feature"))
	writeFile(t, seed, "feature.txt", "feature work\n")
	stagePath(t, seed, "feature.txt")
	require.NoError(t, seedPush.Run(ctx, seed, "feature work"))

	mr, err := client.ProxyMergeRequestCreate(dctx, &daemonpb.ProxyMergeRequestCreateRequest{
		Root: target,
		Request: &pb.CreateMergeRequestRequest{
			Title:        "Full flow",
			SourceBranch: "feature",
			TargetBranch: "main",
		},
	})
	require.NoError(t, err)
	number := mr.GetResponse().GetMergeRequest().GetNumber()
	require.NotZero(t, number)

	merged, err := client.ProxyMergeRequestMerge(dctx, &daemonpb.ProxyMergeRequestMergeRequest{
		Root:    target,
		Request: &pb.MergeMergeRequestRequest{Number: number},
	})
	require.NoError(t, err)
	require.Equal(t, "merged", merged.GetResponse().GetMergeRequest().GetStatus())

	// Graceful shutdown: the daemon stops answering and cleans up.
	_, err = client.Shutdown(dctx, &daemonpb.ShutdownRequest{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		pingCtx, cancel := context.WithTimeout(dctx, time.Second)
		defer cancel()
		_, err := client.Ping(pingCtx, &daemonpb.PingRequest{})
		return err != nil
	}, 5*time.Second, 50*time.Millisecond, "the daemon must stop answering after shutdown")
}
