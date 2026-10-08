package e2e

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type strategyEnv struct {
	ctx        context.Context
	dbConn     *sql.DB
	host       string
	grpcClient *clientgrpc.Client
	auth       *clientusecase.Auth
	pusher     *clientusecase.Push
	merger     *clientusecase.Merge
	requests   *clientusecase.MergeRequest
	rpc        pb.NipaServiceClient
	adminCtx   context.Context
	project    *pb.ProjectContext
	mainDir    string
}

func setupStrategyEnv(t *testing.T) *strategyEnv {
	t.Helper()

	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)

	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	conn, err := grpc.NewClient(host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	return &strategyEnv{
		ctx:        ctx,
		dbConn:     dbConn,
		host:       host,
		grpcClient: grpcClient,
		auth:       auth,
		pusher:     pusher,
		merger:     clientusecase.NewMerge(auth, grpcClient, localrepo.NewLocalRepo(), pusher),
		requests:   clientusecase.NewMergeRequest(auth, grpcClient, localrepo.NewLocalRepo()),
		rpc:        pb.NewNipaServiceClient(conn),
		adminCtx:   metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+login.AccessToken),
		project:    &pb.ProjectContext{Org: e2eOrgSlug, Project: e2eProjectSlug},
		mainDir:    cloneWorktree(t, grpcClient, auth, host, "main"),
	}
}

func (e *strategyEnv) commit(t *testing.T, dir, path, content, message string) {
	t.Helper()
	writeFile(t, dir, path, content)
	stagePath(t, dir, path)
	require.NoError(t, e.pusher.Run(e.ctx, dir, message))
}

func (e *strategyEnv) branch(t *testing.T, name string) string {
	t.Helper()
	_, err := e.grpcClient.CreateBranch(e.ctx, e2eOrgSlug, e2eProjectSlug, name, "main", "", "")
	require.NoError(t, err)
	return cloneWorktree(t, e.grpcClient, e.auth, e.host, name)
}

func (e *strategyEnv) remove(t *testing.T, dir, path, message string) {
	t.Helper()
	require.NoError(t, os.Remove(filepath.Join(dir, filepath.FromSlash(path))))
	lr := localrepo.NewLocalRepo()
	defer func() { _ = lr.Close() }()
	wc, err := clientusecase.NewWorkingCopy(lr, dir)
	require.NoError(t, err)
	require.NoError(t, wc.Add(e.ctx, []string{path}))
	require.NoError(t, e.pusher.Run(e.ctx, dir, message))
}

func (e *strategyEnv) createMR(t *testing.T, source, title string) int64 {
	t.Helper()
	created, err := e.rpc.CreateMergeRequest(e.adminCtx, &pb.CreateMergeRequestRequest{
		Context: e.project, Title: title, SourceBranch: source, TargetBranch: "main",
	})
	require.NoError(t, err)
	return created.GetMergeRequest().GetNumber()
}

func (e *strategyEnv) check(t *testing.T, number int64) *pb.MergeabilityDetail {
	t.Helper()
	res, err := e.rpc.CheckMergeRequest(e.adminCtx, &pb.CheckMergeRequestRequest{
		Context: e.project, Number: number,
	})
	require.NoError(t, err)
	return res.GetMergeability()
}

func (e *strategyEnv) merge(t *testing.T, number int64, strategy string, deleteSource bool) (*pb.MergeMergeRequestResponse, error) {
	t.Helper()
	return e.rpc.MergeMergeRequest(e.adminCtx, &pb.MergeMergeRequestRequest{
		Context: e.project, Number: number, Strategy: strategy, DeleteSource: deleteSource,
	})
}

func lineage(t *testing.T, dbConn *sql.DB, head *serverDomain.Commit, n int) []*serverDomain.Commit {
	t.Helper()
	repo := sqlite.NewBranchRepository(dbConn)
	out := []*serverDomain.Commit{head}
	current := head
	for i := 1; i < n; i++ {
		require.NotNil(t, current.Parent1ID)
		next, err := repo.GetCommit(context.Background(), *current.Parent1ID)
		require.NoError(t, err)
		out = append(out, next)
		current = next
	}
	return out
}

func TestEndToEnd_MergeRequestSquashStrategy(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "a.txt", "a\nb\nc\n", "seed main")

	featureDir := env.branch(t, "feature")
	env.commit(t, featureDir, "a.txt", "a\nX\nc\n", "feature edit")

	env.commit(t, env.mainDir, "a.txt", "a\nb\nZ\n", "main edit")
	env.commit(t, env.mainDir, "main.txt", "main\n", "main add")

	number := env.createMR(t, "feature", "Squash it")
	info := env.check(t, number)
	require.Equal(t, "behind_target", info.GetStatus())
	require.Empty(t, info.GetBlockedBy())

	mainHeadBefore := branchHeadCommit(t, env.dbConn, "main")
	res, err := env.merge(t, number, "squash", true)
	require.NoError(t, err)
	require.Equal(t, "merged", res.GetMergeRequest().GetStatus())
	require.NotEmpty(t, res.GetMergeRequest().GetMergeCommitId())

	head := branchHeadCommit(t, env.dbConn, "main")
	require.Equal(t, "Squash it", head.Message)
	require.Nil(t, head.Parent2ID, "squash must author a single-parent commit")
	require.Equal(t, mainHeadBefore.ID, *head.Parent1ID)
	require.Equal(t, snow.ID(1), head.UserID)

	after := cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
	assertFileContent(t, after, "a.txt", "a\nX\nZ\n")
	assertFileContent(t, after, "main.txt", "main\n")

	_, err = env.rpc.GetBranchByName(env.adminCtx, &pb.GetBranchByNameRequest{Context: env.project, Name: "feature"})
	require.Equal(t, codes.NotFound, status.Code(err), "delete_source must remove the merged branch")
}

func TestEndToEnd_MergeRequestMergeCommitStrategy(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "a.txt", "a\nb\nc\n", "seed main")

	featureDir := env.branch(t, "feature")
	env.commit(t, featureDir, "feature.txt", "feature\n", "feature work")
	featureHead := branchHeadCommit(t, env.dbConn, "feature")

	env.commit(t, env.mainDir, "main.txt", "main\n", "main work")
	mainHeadBefore := branchHeadCommit(t, env.dbConn, "main")

	number := env.createMR(t, "feature", "Merge it")
	res, err := env.merge(t, number, "merge", false)
	require.NoError(t, err)
	require.Equal(t, "merged", res.GetMergeRequest().GetStatus())

	head := branchHeadCommit(t, env.dbConn, "main")
	require.Equal(t, mainHeadBefore.ID, *head.Parent1ID)
	require.NotNil(t, head.Parent2ID)
	require.Equal(t, featureHead.ID, *head.Parent2ID, "the merge commit records the source head")
	require.Contains(t, head.Message, "feature")

	after := cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
	assertFileContent(t, after, "a.txt", "a\nb\nc\n")
	assertFileContent(t, after, "feature.txt", "feature\n")
	assertFileContent(t, after, "main.txt", "main\n")
}

func TestEndToEnd_MergeRequestRebaseStrategy(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "a.txt", "a\n", "seed main")

	featureDir := env.branch(t, "feature")
	env.commit(t, featureDir, "f1.txt", "one\n", "feature one")
	env.commit(t, featureDir, "f2.txt", "two\n", "feature two")

	env.commit(t, env.mainDir, "main.txt", "main\n", "main work")
	mainHeadBefore := branchHeadCommit(t, env.dbConn, "main")

	number := env.createMR(t, "feature", "Rebase it")
	res, err := env.merge(t, number, "rebase", false)
	require.NoError(t, err)
	require.Equal(t, "merged", res.GetMergeRequest().GetStatus())

	head := branchHeadCommit(t, env.dbConn, "main")
	chain := lineage(t, env.dbConn, head, 3)
	require.Equal(t, "feature two", chain[0].Message)
	require.Equal(t, "feature one", chain[1].Message)
	require.Equal(t, mainHeadBefore.ID, chain[2].ID, "the rebased chain must start at the previous target head")
	require.Nil(t, chain[0].Parent2ID)
	require.Nil(t, chain[1].Parent2ID)

	after := cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
	assertFileContent(t, after, "f1.txt", "one\n")
	assertFileContent(t, after, "f2.txt", "two\n")
	assertFileContent(t, after, "main.txt", "main\n")
}

func TestEndToEnd_MergeRequestRebaseAfterSourceMergedTarget(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "a.txt", "a\n", "seed main")

	featureDir := env.branch(t, "feature")
	env.commit(t, featureDir, "s1.txt", "s1\n", "add s1")

	env.commit(t, env.mainDir, "main.txt", "main\n", "add main")
	_, err := env.merger.Run(env.ctx, featureDir, "main", clientusecase.MergeOptions{})
	require.NoError(t, err, "feature must be able to merge main locally")

	env.commit(t, env.mainDir, "main2.txt", "main2\n", "add main2")
	mainHeadBefore := branchHeadCommit(t, env.dbConn, "main")

	number := env.createMR(t, "feature", "Rebase after catch-up")
	info := env.check(t, number)
	require.Equal(t, "behind_target", info.GetStatus())

	res, err := env.merge(t, number, "rebase", false)
	require.NoError(t, err)
	require.Equal(t, "merged", res.GetMergeRequest().GetStatus())

	head := branchHeadCommit(t, env.dbConn, "main")
	chain := lineage(t, env.dbConn, head, 3)
	require.Contains(t, chain[0].Message, "Merge branch", "the source merge commit is replayed")
	require.Equal(t, "add s1", chain[1].Message)
	require.Equal(t, mainHeadBefore.ID, chain[2].ID,
		"the rebase must stop at the merge base history, not replay it")

	after := cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
	assertFileContent(t, after, "s1.txt", "s1\n")
	assertFileContent(t, after, "main.txt", "main\n")
	assertFileContent(t, after, "main2.txt", "main2\n")
	assertFileContent(t, after, "a.txt", "a\n")
}

func TestEndToEnd_MergeRequestBehindTargetStaysReviewGated(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "a.txt", "a\n", "seed main")

	featureDir := env.branch(t, "feature")
	env.commit(t, featureDir, "feature.txt", "feature\n", "feature work")
	env.commit(t, env.mainDir, "main.txt", "main\n", "main work")

	one := int64(1)
	_, err := env.rpc.SetBranchProtection(env.adminCtx, &pb.SetBranchProtectionRequest{
		Context: env.project, Name: "main", IsProtected: true, RequiredApprovals: &one,
	})
	require.NoError(t, err)

	number := env.createMR(t, "feature", "Gated rebase")
	info := env.check(t, number)
	require.Equal(t, "behind_target", info.GetStatus())
	require.Equal(t, "insufficient_approvals", info.GetBlockedBy(),
		"a diverged source must still report the review policy block")

	for _, strategy := range []string{"ff", "merge", "squash", "rebase"} {
		_, err := env.merge(t, number, strategy, false)
		require.Equal(t, codes.FailedPrecondition, status.Code(err), "strategy %q must stay blocked", strategy)
	}
}

func TestEndToEnd_MergeRequestAncestorSourceIsUpToDate(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "a.txt", "a\n", "seed main")

	_ = env.branch(t, "feature")
	env.commit(t, env.mainDir, "main.txt", "main\n", "main work")
	mainHeadBefore := branchHeadCommit(t, env.dbConn, "main")

	number := env.createMR(t, "feature", "Nothing to merge")
	info := env.check(t, number)
	require.Equal(t, "up_to_date", info.GetStatus(),
		"a source contained in the target has nothing to merge")

	for _, strategy := range []string{"ff", "merge", "squash", "rebase"} {
		_, err := env.merge(t, number, strategy, false)
		require.Equal(t, codes.FailedPrecondition, status.Code(err), "strategy %q must not land anything", strategy)
		require.Contains(t, status.Convert(err).Message(), "up to date")
	}

	head := branchHeadCommit(t, env.dbConn, "main")
	require.Equal(t, mainHeadBefore.ID, head.ID, "a refused merge must not move the target")
}

func TestEndToEnd_MergeRequestStrategyConflicts(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "a.txt", "a\nb\nc\n", "seed main")

	featureDir := env.branch(t, "feature")
	env.commit(t, featureDir, "a.txt", "a\nX\nc\n", "feature edit")
	env.commit(t, env.mainDir, "a.txt", "a\nY\nc\n", "main edit")
	mainHeadBefore := branchHeadCommit(t, env.dbConn, "main")

	number := env.createMR(t, "feature", "Conflicting")
	_, err := env.merge(t, number, "merge", false)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "a.txt")
	require.Equal(t, mainHeadBefore.ID, branchHeadCommit(t, env.dbConn, "main").ID,
		"a conflicting merge must not land")

	second := env.branch(t, "rebase-conflict")
	env.commit(t, second, "a.txt", "a\nZ\nc\n", "second edit")
	env.commit(t, env.mainDir, "a.txt", "a\nW\nc\n", "main edit two")
	mainHeadBefore = branchHeadCommit(t, env.dbConn, "main")

	secondNumber := env.createMR(t, "rebase-conflict", "Conflicting rebase")
	_, err = env.merge(t, secondNumber, "rebase", false)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "a.txt")

	head := branchHeadCommit(t, env.dbConn, "main")
	require.Equal(t, mainHeadBefore.ID, head.ID, "conflicting merges must not land")
}

func TestEndToEnd_MergeRequestAddAddConflict(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "a.txt", "a\n", "seed main")

	featureDir := env.branch(t, "feature")
	env.commit(t, featureDir, "b.txt", "feature\n", "feature add")
	env.commit(t, env.mainDir, "b.txt", "main\n", "main add")
	mainHeadBefore := branchHeadCommit(t, env.dbConn, "main")

	number := env.createMR(t, "feature", "Add/add")
	_, err := env.merge(t, number, "merge", false)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "b.txt")
	require.Equal(t, mainHeadBefore.ID, branchHeadCommit(t, env.dbConn, "main").ID)
}

func TestEndToEnd_MergeRequestMergeDeletesRemovedFiles(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "keep.txt", "keep\n", "seed keep")
	env.commit(t, env.mainDir, "gone.txt", "gone\n", "seed gone")

	featureDir := env.branch(t, "feature")
	env.remove(t, featureDir, "gone.txt", "feature delete")
	env.commit(t, env.mainDir, "main.txt", "main\n", "main work")

	number := env.createMR(t, "feature", "Delete via merge")
	res, err := env.merge(t, number, "merge", false)
	require.NoError(t, err)
	require.Equal(t, "merged", res.GetMergeRequest().GetStatus())

	after := cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
	assertFileContent(t, after, "keep.txt", "keep\n")
	assertFileContent(t, after, "main.txt", "main\n")
	require.NoFileExists(t, filepath.Join(after, "gone.txt"))
}

func TestEndToEnd_MergeRequestMergeStrategyCopiesStoredChunks(t *testing.T) {
	env := setupStrategyEnv(t)
	env.commit(t, env.mainDir, "pack.bin", "\x00\x01\x02\x03packed", "seed main")

	featureDir := env.branch(t, "feature")
	env.commit(t, featureDir, "other.bin", "\x04\x05\x06\x07other", "feature binary")

	env.commit(t, env.mainDir, "main.txt", "main\n", "main work")
	number := env.createMR(t, "feature", "Binary merge")

	res, err := env.merge(t, number, "merge", false)
	require.NoError(t, err)
	require.Equal(t, "merged", res.GetMergeRequest().GetStatus())

	after := cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
	assertFileContent(t, after, "pack.bin", "\x00\x01\x02\x03packed")
	assertFileContent(t, after, "other.bin", "\x04\x05\x06\x07other")
	assertFileContent(t, after, "main.txt", "main\n")
	require.Equal(t, "main", loadBranchConfig(t, after))
}
