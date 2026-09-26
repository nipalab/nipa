package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type stubFileLockPermission struct{}

func (stubFileLockPermission) HasProjectAccess(context.Context, snow.ID, domain.Permission) bool {
	return true
}
func (stubFileLockPermission) HasPathAccess(context.Context, snow.ID, string, domain.Permission) bool {
	return true
}
func (stubFileLockPermission) CompileFilter(context.Context, snow.ID, domain.Permission) (*usecase.PathFilter, error) {
	return usecase.AllowAllFilter(), nil
}
func (stubFileLockPermission) AdminHasProject(context.Context, snow.ID) bool { return true }

func newFileLockTestServer(t *testing.T) (*nipaServer, context.Context) {
	t.Helper()
	dbConn, err := database.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dbConn.Close()) })
	dbConn.SetMaxOpenConns(1)
	require.NoError(t, database.MigrateUp(dbConn, "sqlite3"))

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	lockUc := usecase.NewFileLock(
		sqlite.NewFileLockRepository(dbConn),
		sqlite.NewBranchRepository(dbConn),
		stubFileLockPermission{},
		node,
	)
	common := usecase.NewCommon(sqlite.NewOrgRepository(dbConn), sqlite.NewProjectRepository(dbConn))
	srv := New(&mockUsecaseContainer{common: common, fileLock: lockUc})
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(1), IsAdmin: true})
	return srv, ctx
}

func TestFileLockHandlers_Flow(t *testing.T) {
	srv, ctx := newFileLockTestServer(t)
	project := &pb.ProjectContext{Org: "default", Project: "default"}

	lockResp, err := srv.LockFile(ctx, &pb.LockFileRequest{Context: project, Path: "assets/orc.png", Branch: "main"})
	require.NoError(t, err)
	lock := lockResp.GetLock()
	require.Equal(t, "assets/orc.png", lock.GetPath())
	require.True(t, lock.GetGlobal())
	require.Equal(t, "main", lock.GetBranch())
	require.NotEmpty(t, lock.GetId())
	require.Nil(t, lock.MergeRequestNumber)
	require.False(t, lock.GetAcquiredAt().AsTime().IsZero())

	listResp, err := srv.ListFileLocks(ctx, &pb.ListFileLocksRequest{Context: project})
	require.NoError(t, err)
	require.Len(t, listResp.GetLocks(), 1)
	require.Equal(t, "assets/orc.png", listResp.GetLocks()[0].GetPath())
	require.Equal(t, "Super Admin", listResp.GetLocks()[0].GetHeldByName())

	_, err = srv.UnlockFile(ctx, &pb.UnlockFileRequest{Context: project, Path: "assets/orc.png", Branch: "main"})
	require.NoError(t, err)

	listResp, err = srv.ListFileLocks(ctx, &pb.ListFileLocksRequest{Context: project})
	require.NoError(t, err)
	require.Empty(t, listResp.GetLocks())
}

func TestFileLockHandlers_Errors(t *testing.T) {
	srv, ctx := newFileLockTestServer(t)
	project := &pb.ProjectContext{Org: "default", Project: "default"}

	_, err := srv.LockFile(ctx, &pb.LockFileRequest{Context: project, Path: "../escape"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.LockFile(ctx, &pb.LockFileRequest{Context: project, Path: "a.png", Branch: "ghost"})
	require.Equal(t, codes.NotFound, status.Code(err))

	_, err = srv.LockFile(ctx, &pb.LockFileRequest{
		Context: &pb.ProjectContext{Org: "default", Project: "ghost"}, Path: "a.png",
	})
	require.Equal(t, codes.NotFound, status.Code(err))

	_, err = srv.UnlockFile(ctx, &pb.UnlockFileRequest{Context: project, Path: "missing.png"})
	require.Equal(t, codes.NotFound, status.Code(err))

	_, err = srv.ListFileLocks(ctx, &pb.ListFileLocksRequest{
		Context: &pb.ProjectContext{Org: "default", Project: "ghost"},
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}
