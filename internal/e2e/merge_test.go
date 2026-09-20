package e2e

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

func newLoggedInClient(t *testing.T, host string) (*clientgrpc.Client, *clientusecase.Auth) {
	t.Helper()
	ctx := context.Background()
	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	require.NoError(t, grpcClient.Connect(ctx, host))

	loginResult, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(loginResult))
	return grpcClient, clientusecase.NewAuth(grpcClient, store, failPrompt{})
}

func cloneWorktree(t *testing.T, grpcClient *clientgrpc.Client, auth *clientusecase.Auth, host, branch string) string {
	t.Helper()
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), branch)
	require.NoError(t, repo.Clone(context.Background(), url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))
	if branch != "main" {
		updater := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo())
		require.NoError(t, updater.Switch(context.Background(), target, branch))
	}
	return target
}

func pinnedCommit(t *testing.T, target string) (commitID, commitHash string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	c, err := lr.LoadCommit()
	require.NoError(t, err)
	return c.CommitID, c.CommitHash
}

func branchHeadCommit(t *testing.T, dbConn *sql.DB, branchName string) *serverDomain.Commit {
	t.Helper()
	ctx := context.Background()
	org, err := sqlite.NewOrgRepository(dbConn).GetBySlug(ctx, e2eOrgSlug)
	require.NoError(t, err)
	project, err := sqlite.NewProjectRepository(dbConn).GetByOrgIDAndSlug(ctx, org.ID, e2eProjectSlug)
	require.NoError(t, err)
	branch, err := sqlite.NewBranchRepository(dbConn).GetBranchByName(ctx, project.ID, branchName)
	require.NoError(t, err)
	require.NotNil(t, branch.CommitID)
	commit, err := sqlite.NewBranchRepository(dbConn).GetCommit(ctx, *branch.CommitID)
	require.NoError(t, err)
	return commit
}

func loadMergeState(t *testing.T, target string) *domain.MergeState {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	state, err := lr.LoadMergeState()
	require.NoError(t, err)
	return state
}

func TestEndToEnd_Merge_FastForward(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	merger := clientusecase.NewMerge(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	mainDir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, mainDir, "a.txt", "base\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "seed main"))

	created, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)

	featDir := cloneWorktree(t, grpcClient, auth, host, "feature")
	writeFile(t, featDir, "a.txt", "feature content\n")
	writeFile(t, featDir, "b.txt", "added on feature\n")
	stagePath(t, featDir, "a.txt")
	stagePath(t, featDir, "b.txt")
	require.NoError(t, pusher.Run(ctx, featDir, "work on feature"))
	featID, featHash := pinnedCommit(t, featDir)
	require.NotEmpty(t, featID)

	outcome, err := merger.Run(ctx, mainDir, "feature", clientusecase.MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.FastForwarded)

	assertFileContent(t, mainDir, "a.txt", "feature content\n")
	assertFileContent(t, mainDir, "b.txt", "added on feature\n")
	require.Equal(t, "main", loadBranchConfig(t, mainDir), "fast-forward must not change the tracked branch")

	mainManifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	featureManifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "feature", nil)
	require.NoError(t, err)
	require.Equal(t, featureManifest.Hash, mainManifest.Hash, "fast-forward must move main to the feature head")
	require.Equal(t, featureManifest.Hash.String(), snapshotOf(t, mainDir).TreeHash)

	_, mainHash := pinnedCommit(t, mainDir)
	require.Equal(t, featHash, mainHash, "fast-forward must pin the feature head commit on main")

	mergeHead := branchHeadCommit(t, dbConn, "main")
	require.Nil(t, mergeHead.Parent2ID, "a fast-forward is a pointer move, not a merge commit")

	after := cloneWorktree(t, grpcClient, auth, host, "main")
	assertFileContent(t, after, "a.txt", "feature content\n")
	assertFileContent(t, after, "b.txt", "added on feature\n")
}

func TestEndToEnd_Merge_CleanThreeWay(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	merger := clientusecase.NewMerge(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	mainDir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, mainDir, "a.txt", "a\nb\nc\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "seed main"))

	created, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)

	featDir := cloneWorktree(t, grpcClient, auth, host, "feature")
	writeFile(t, featDir, "a.txt", "a\nX\nc\n")
	stagePath(t, featDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, featDir, "edit line 2 on feature"))

	writeFile(t, mainDir, "a.txt", "a\nb\nZ\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "edit line 3 on main"))
	mainHeadID, _ := pinnedCommit(t, mainDir)

	outcome, err := merger.Run(ctx, mainDir, "feature", clientusecase.MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.MergeCommitted)
	require.Empty(t, outcome.Conflicts)

	assertFileContent(t, mainDir, "a.txt", "a\nX\nZ\n")
	require.Nil(t, loadMergeState(t, mainDir), "a clean merge must not leave pending state")

	mergeHead := branchHeadCommit(t, dbConn, "main")
	require.NotNil(t, mergeHead.Parent2ID, "a three-way merge must record the source head as the second parent")
	featID, _ := pinnedCommit(t, featDir)
	featParsed, err := snow.ParseBase36(featID)
	require.NoError(t, err)
	require.Equal(t, featParsed, *mergeHead.Parent2ID)
	require.Equal(t, "Merge branch 'feature' into 'main'", mergeHead.Message)

	headParsed, err := snow.ParseBase36(mainHeadID)
	require.NoError(t, err)
	require.Equal(t, headParsed, *mergeHead.Parent1ID, "the merge commit's first parent must be the target head it was based on")

	after := cloneWorktree(t, grpcClient, auth, host, "main")
	assertFileContent(t, after, "a.txt", "a\nX\nZ\n")
	mainManifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.Equal(t, mainManifest.Hash.String(), snapshotOf(t, mainDir).TreeHash)
}

func TestEndToEnd_Merge_ConflictResolve(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	merger := clientusecase.NewMerge(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	mainDir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, mainDir, "a.txt", "a\nb\nc\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "seed main"))

	created, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)

	featDir := cloneWorktree(t, grpcClient, auth, host, "feature")
	writeFile(t, featDir, "a.txt", "a\nX\nc\n")
	stagePath(t, featDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, featDir, "edit line 2 on feature"))

	writeFile(t, mainDir, "a.txt", "a\nY\nc\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "edit line 2 on main"))

	outcome, err := merger.Run(ctx, mainDir, "feature", clientusecase.MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, outcome.Conflicts)

	markers := readFile(t, mainDir, "a.txt")
	require.Contains(t, markers, "<<<<<<< ours")
	require.Contains(t, markers, ">>>>>>> theirs")

	state := loadMergeState(t, mainDir)
	require.NotNil(t, state, "a conflicted merge must persist pending state")
	require.Equal(t, []string{"a.txt"}, state.Conflicts)
	require.NotEmpty(t, state.TargetTreeHash, "the resolution push must know the target head tree it is based on")

	resolve := "a\nY\nc\n"
	writeFile(t, mainDir, "a.txt", resolve)
	require.NoError(t, pusher.Run(ctx, mainDir, "resolve merge conflict"))
	require.Nil(t, loadMergeState(t, mainDir), "the push completing a merge must clear the pending state")

	assertFileContent(t, mainDir, "a.txt", resolve)
	after := cloneWorktree(t, grpcClient, auth, host, "main")
	assertFileContent(t, after, "a.txt", resolve)

	mergeHead := branchHeadCommit(t, dbConn, "main")
	featID, _ := pinnedCommit(t, featDir)
	featParsed, err := snow.ParseBase36(featID)
	require.NoError(t, err)
	require.Equal(t, featParsed, *mergeHead.Parent2ID, "the resolution push must still record the source head as second parent")
}

func TestEndToEnd_Merge_ConflictAbort(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	merger := clientusecase.NewMerge(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	mainDir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, mainDir, "a.txt", "a\nb\nc\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "seed main"))

	created, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)

	featDir := cloneWorktree(t, grpcClient, auth, host, "feature")
	writeFile(t, featDir, "a.txt", "a\nX\nc\n")
	stagePath(t, featDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, featDir, "edit line 2 on feature"))

	writeFile(t, mainDir, "a.txt", "a\nY\nc\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "edit line 2 on main"))

	outcome, err := merger.Run(ctx, mainDir, "feature", clientusecase.MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, outcome.Conflicts)
	require.NotNil(t, loadMergeState(t, mainDir))

	outcome, err = merger.Run(ctx, mainDir, "", clientusecase.MergeOptions{Abort: true})
	require.NoError(t, err)
	require.False(t, outcome.FastForwarded)
	require.False(t, outcome.MergeCommitted)

	assertFileContent(t, mainDir, "a.txt", "a\nY\nc\n")
	require.Nil(t, loadMergeState(t, mainDir), "abort must clear the pending merge state")

	mainManifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.Equal(t, mainManifest.Hash.String(), snapshotOf(t, mainDir).TreeHash, "abort must restore the snapshot to the branch head")

	mergeHead := branchHeadCommit(t, dbConn, "main")
	require.Nil(t, mergeHead.Parent2ID, "an aborted merge must not create a merge commit")
}

func readFile(t *testing.T, target, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(path)))
	require.NoError(t, err)
	return string(data)
}
