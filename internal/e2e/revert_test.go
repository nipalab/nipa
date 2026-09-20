package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/merge"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/snow"
)

func loadRevertState(t *testing.T, target string) *domain.RevertState {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	state, err := lr.LoadRevertState()
	require.NoError(t, err)
	return state
}

func TestEndToEnd_Revert_HeadCommit(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	reverter := clientusecase.NewRevert(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, dir, "a.txt", "one\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "add a"))

	writeFile(t, dir, "a.txt", "two\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "change a"))
	commitID, commitHash := pinnedCommit(t, dir)

	outcome, err := reverter.Run(ctx, dir, commitID, clientusecase.RevertOptions{})
	require.NoError(t, err)
	require.True(t, outcome.Committed)
	require.Empty(t, outcome.Conflicts)

	assertFileContent(t, dir, "a.txt", "one\n")
	require.Nil(t, loadRevertState(t, dir), "a clean revert must not leave pending state")

	manifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.Equal(t, manifest.Hash.String(), snapshotOf(t, dir).TreeHash)

	head := branchHeadCommit(t, dbConn, "main")
	require.Equal(t, fmt.Sprintf("Revert \"change a\"\n\nThis reverts commit %s (%s).", commitID, commitHash), head.Message)

	parsed, err := snow.ParseBase36(commitID)
	require.NoError(t, err)
	require.Equal(t, parsed, *head.Parent1ID, "the revert commit must be a child of the reverted commit")
	require.Nil(t, head.Parent2ID, "a revert is a single-parent commit")
}

func TestEndToEnd_Revert_Range(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	reverter := clientusecase.NewRevert(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, dir, "a.txt", "one\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "first"))
	c1, _ := pinnedCommit(t, dir)

	writeFile(t, dir, "a.txt", "two\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "second"))

	writeFile(t, dir, "a.txt", "three\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "third"))
	c3, _ := pinnedCommit(t, dir)

	outcome, err := reverter.Run(ctx, dir, c1+".."+c3, clientusecase.RevertOptions{})
	require.NoError(t, err)
	require.True(t, outcome.Committed)
	require.Nil(t, loadRevertState(t, dir))

	assertFileContent(t, dir, "a.txt", "one\n")

	entries, err := grpcClient.GetCommitLog(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 5)
	require.Contains(t, entries[0].Message, `Revert "second"`)
	require.Contains(t, entries[1].Message, `Revert "third"`)
	require.Equal(t, "third", entries[2].Message)

	manifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.Equal(t, manifest.Hash.String(), snapshotOf(t, dir).TreeHash)
}

func TestEndToEnd_Revert_ConflictContinue(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	reverter := clientusecase.NewRevert(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, dir, "a.txt", "a\nb\nc\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "first"))

	writeFile(t, dir, "a.txt", "a\nX\nc\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "second"))
	commitID, _ := pinnedCommit(t, dir)

	writeFile(t, dir, "a.txt", "a\nY\nc\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "third"))

	outcome, err := reverter.Run(ctx, dir, commitID, clientusecase.RevertOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, outcome.Conflicts)
	require.NotNil(t, loadRevertState(t, dir))

	writeFile(t, dir, "a.txt", "a\nb\nc\n")
	stagePath(t, dir, "a.txt")

	outcome, err = reverter.Run(ctx, dir, "", clientusecase.RevertOptions{Continue: true})
	require.NoError(t, err)
	require.True(t, outcome.Committed)
	require.Nil(t, loadRevertState(t, dir))

	assertFileContent(t, dir, "a.txt", "a\nb\nc\n")
	manifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.Equal(t, manifest.Hash.String(), snapshotOf(t, dir).TreeHash)
}

func TestEndToEnd_Revert_Abort(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	reverter := clientusecase.NewRevert(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, dir, "a.txt", "a\nb\nc\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "first"))

	writeFile(t, dir, "a.txt", "a\nX\nc\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "second"))
	commitID, _ := pinnedCommit(t, dir)

	writeFile(t, dir, "a.txt", "a\nY\nc\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "third"))

	outcome, err := reverter.Run(ctx, dir, commitID, clientusecase.RevertOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, outcome.Conflicts)

	_, err = reverter.Run(ctx, dir, "", clientusecase.RevertOptions{Abort: true})
	require.NoError(t, err)
	require.Nil(t, loadRevertState(t, dir))

	assertFileContent(t, dir, "a.txt", "a\nY\nc\n")
	manifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.Equal(t, manifest.Hash.String(), snapshotOf(t, dir).TreeHash)
}

func TestEndToEnd_Revert_RangeKeepsUntouchedFile(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	reverter := clientusecase.NewRevert(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, dir, "a.txt", "one\n")
	writeFile(t, dir, "b.txt", "keep me\n")
	stagePath(t, dir, "a.txt")
	stagePath(t, dir, "b.txt")
	require.NoError(t, pusher.Run(ctx, dir, "seed"))
	c1, _ := pinnedCommit(t, dir)

	writeFile(t, dir, "a.txt", "two\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "second"))

	writeFile(t, dir, "a.txt", "three\n")
	stagePath(t, dir, "a.txt")
	require.NoError(t, pusher.Run(ctx, dir, "third"))
	c3, _ := pinnedCommit(t, dir)

	outcome, err := reverter.Run(ctx, dir, c1+".."+c3, clientusecase.RevertOptions{})
	require.NoError(t, err)
	require.True(t, outcome.Committed)

	assertFileContent(t, dir, "a.txt", "one\n")
	assertFileContent(t, dir, "b.txt", "keep me\n")

	manifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.Contains(t, merge.Flatten(manifest), "b.txt", "an untouched file must survive a range revert on the server")
	require.Equal(t, manifest.Hash.String(), snapshotOf(t, dir).TreeHash)
}
