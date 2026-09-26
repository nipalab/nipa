package e2e

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

func writeBinaryFile(t *testing.T, target, path string) {
	t.Helper()
	full := filepath.Join(target, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0x02, 0x03}, 0o644))
}

func projectIDFromDB(t *testing.T, dbConn *sql.DB) snow.ID {
	t.Helper()
	ctx := context.Background()
	org, err := sqlite.NewOrgRepository(dbConn).GetBySlug(ctx, e2eOrgSlug)
	require.NoError(t, err)
	project, err := sqlite.NewProjectRepository(dbConn).GetByOrgIDAndSlug(ctx, org.ID, e2eProjectSlug)
	require.NoError(t, err)
	return project.ID
}

func seedOtherUserLock(t *testing.T, dbConn *sql.DB, path string) {
	t.Helper()
	ctx := context.Background()
	_, err := dbConn.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password) VALUES (9001, 'bob', 'bob@example.com', 'x')`)
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx,
		`INSERT INTO file_locks (id, project_id, branch_id, path, held_by) VALUES (9100, ?, NULL, ?, 9001)`,
		projectIDFromDB(t, dbConn).Int64(), path)
	require.NoError(t, err)
}

func TestEndToEnd_FileLockPushLifecycle(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth := newLoggedInClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	locks := clientusecase.NewFileLock(auth, grpcClient, localrepo.NewLocalRepo())

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeBinaryFile(t, dir, "art/tex.png")
	stagePath(t, dir, "art/tex.png")

	err := pusher.Run(ctx, dir, "unlocked binary")
	require.Error(t, err, "a binary push without a lock must be rejected")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Contains(t, domErr.Message, "requires a lock")

	lock, err := locks.Lock(ctx, dir, "art/tex.png", "")
	require.NoError(t, err)
	require.True(t, lock.Global, "the default branch gets a project-global lock")

	require.NoError(t, pusher.Run(ctx, dir, "locked binary"))

	list, err := locks.List(ctx, dir)
	require.NoError(t, err)
	require.Empty(t, list, "landing a push releases the owner's exact-path lock")
}

func TestEndToEnd_FileLockConflictAndBranchScope(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	locks := clientusecase.NewFileLock(auth, grpcClient, localrepo.NewLocalRepo())

	mainDir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, mainDir, "readme.txt", "base\n")
	stagePath(t, mainDir, "readme.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "seed main"))

	seedOtherUserLock(t, dbConn, "tex.png")

	writeBinaryFile(t, mainDir, "tex.png")
	stagePath(t, mainDir, "tex.png")

	err := pusher.Run(ctx, mainDir, "blocked by bob")
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Contains(t, domErr.Message, "bob")

	_, err = locks.Lock(ctx, mainDir, "tex.png", "")
	require.Error(t, err, "the mainline lock is already held by another user")

	_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "artwork", "main", "", "")
	require.NoError(t, err)
	artDir := cloneWorktree(t, grpcClient, auth, host, "artwork")

	devLock, err := locks.Lock(ctx, artDir, "tex.png", "")
	require.NoError(t, err, "a development branch has its own lock scope")
	require.False(t, devLock.Global)
	require.Equal(t, "artwork", devLock.Branch)

	writeBinaryFile(t, artDir, "tex.png")
	stagePath(t, artDir, "tex.png")
	require.NoError(t, pusher.Run(ctx, artDir, "dev branch edit"))

	list, err := locks.List(ctx, artDir)
	require.NoError(t, err)
	require.Len(t, list, 1, "only bob's mainline lock remains; the branch lock landed")
	require.Equal(t, "bob", list[0].HeldByName)

	dirDir := cloneWorktree(t, grpcClient, auth, host, "main")
	dirLock, err := locks.Lock(ctx, dirDir, "assets/", "")
	require.NoError(t, err)
	require.Equal(t, "assets", dirLock.Path)
	writeBinaryFile(t, dirDir, "assets/orc.png")
	stagePath(t, dirDir, "assets/orc.png")
	require.NoError(t, pusher.Run(ctx, dirDir, "directory lock covers new binaries"))

	_, err = locks.Lock(ctx, artDir, "wip.png", "")
	require.NoError(t, err)
	require.NoError(t, grpcClient.DeleteBranch(ctx, e2eOrgSlug, e2eProjectSlug, "artwork"))

	list, err = locks.List(ctx, mainDir)
	require.NoError(t, err)
	require.Len(t, list, 2, "deleting the branch releases its scoped locks")
	for _, lock := range list {
		require.NotEqual(t, "wip.png", lock.Path)
	}
}

func TestEndToEnd_FileLockMergeRequest(t *testing.T) {
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	locks := clientusecase.NewFileLock(auth, grpcClient, localrepo.NewLocalRepo())
	requests := clientusecase.NewMergeRequest(auth, grpcClient, localrepo.NewLocalRepo())

	mainDir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, mainDir, "a.txt", "base\n")
	stagePath(t, mainDir, "a.txt")
	require.NoError(t, pusher.Run(ctx, mainDir, "seed main"))

	_, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "hero", "main", "", "")
	require.NoError(t, err)
	heroDir := cloneWorktree(t, grpcClient, auth, host, "hero")

	writeBinaryFile(t, heroDir, "hero.png")
	stagePath(t, heroDir, "hero.png")
	_, err = locks.Lock(ctx, heroDir, "hero.png", "")
	require.NoError(t, err)
	require.NoError(t, pusher.Run(ctx, heroDir, "add hero art"))

	seedOtherUserLock(t, dbConn, "blocked.png")
	writeBinaryFile(t, heroDir, "blocked.png")
	stagePath(t, heroDir, "blocked.png")
	_, err = locks.Lock(ctx, heroDir, "blocked.png", "")
	require.NoError(t, err)
	require.NoError(t, pusher.Run(ctx, heroDir, "add blocked art"))

	_, err = requests.Create(ctx, heroDir, clientusecase.CreateMergeRequestOptions{Title: "Hero art"})
	require.Error(t, err, "a binary path locked on the target scope must block the request")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Contains(t, domErr.Message, "bob")

	list, err := locks.List(ctx, mainDir)
	require.NoError(t, err)
	require.Len(t, list, 1, "a failed request must not leave partial locks behind")

	require.NoError(t, locks.Unlock(ctx, mainDir, "blocked.png", ""))

	mr, err := requests.Create(ctx, heroDir, clientusecase.CreateMergeRequestOptions{Title: "Hero art"})
	require.NoError(t, err)

	list, err = locks.List(ctx, mainDir)
	require.NoError(t, err)
	require.Len(t, list, 2, "the request owns both binary paths on the mainline scope")
	for _, lock := range list {
		require.True(t, lock.Global)
		require.NotNil(t, lock.MergeRequestNumber)
		require.Equal(t, mr.Number, *lock.MergeRequestNumber)
	}

	merged, info, err := requests.Merge(ctx, mainDir, strconv.FormatInt(mr.Number, 10))
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestMerged, merged.Status)
	require.Equal(t, "mergeable", info.Status)

	list, err = locks.List(ctx, mainDir)
	require.NoError(t, err)
	require.Empty(t, list, "merging the request releases its locks")
}
