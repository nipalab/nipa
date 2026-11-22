package e2e

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/merge"
)

// File mode classes as carried on the wire (pb.FileMode).
const (
	e2eModeReadOnly   = 1
	e2eModeReadWrite  = 2
	e2eModeExecutable = 3
)

func skipWithoutUnixPerms(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not tracked on Windows")
	}
}

func chmodFile(t *testing.T, root, path string, perm fs.FileMode) {
	t.Helper()
	require.NoError(t, os.Chmod(filepath.Join(root, filepath.FromSlash(path)), perm))
}

func requireCleanStatus(t *testing.T, wc *clientusecase.WorkingCopy) {
	t.Helper()
	st, err := wc.Status(context.Background(), clientusecase.StatusOptions{NoCache: true})
	require.NoError(t, err)
	require.Empty(t, st.Modified, "modified")
	require.Empty(t, st.Staged, "staged")
	require.Empty(t, st.Added, "added")
	require.Empty(t, st.Deleted, "deleted")
	require.Empty(t, st.Untracked, "untracked")
	// The cached path must agree with the uncached one.
	st, err = wc.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Modified, "modified (cached)")
}

func openWorkingCopy(t *testing.T, root string) *clientusecase.WorkingCopy {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	wc, err := clientusecase.NewWorkingCopy(lr, root)
	require.NoError(t, err)
	return wc
}

// TestEndToEnd_FileModeClassification pushes files with permissions outside
// the canonical 0644/0755/0444 and checks they land with the class the status
// path computes, so status stays clean and a fresh clone gets the same class.
func TestEndToEnd_FileModeClassification(t *testing.T) {
	skipWithoutUnixPerms(t)
	t.Parallel()
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	work := cloneWorktree(t, grpcClient, auth, host, "main")
	wc := openWorkingCopy(t, work)

	cases := []struct {
		path string
		perm fs.FileMode
		want int
	}{
		{"bin/group.sh", 0o775, e2eModeExecutable},
		{"bin/owner.sh", 0o700, e2eModeExecutable},
		{"bin/traverse.sh", 0o711, e2eModeExecutable},
		{"bin/readexec.sh", 0o555, e2eModeExecutable},
		{"docs/private.txt", 0o400, e2eModeReadOnly},
		{"docs/shared.txt", 0o666, e2eModeReadWrite},
		{"docs/plain.txt", 0o644, e2eModeReadWrite},
	}
	paths := make([]string, 0, len(cases))
	for _, c := range cases {
		writeFile(t, work, c.path, "content of "+c.path+"\n")
		chmodFile(t, work, c.path, c.perm)
		paths = append(paths, c.path)
	}
	require.NoError(t, wc.Add(ctx, paths))
	require.NoError(t, pusher.Run(ctx, work, "add files with assorted modes"))

	tree, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	files := merge.Flatten(tree)
	require.Len(t, files, len(cases), "the push must carry every file")
	for _, c := range cases {
		require.Equal(t, c.want, files[c.path].Mode, "server mode of %s (%#o)", c.path, uint32(c.perm))
	}
	requireCleanStatus(t, wc)

	fresh := cloneWorktree(t, grpcClient, auth, host, "main")
	for _, c := range cases {
		requireMaterializedClass(t, fresh, c.path, c.want)
	}
	requireCleanStatus(t, openWorkingCopy(t, fresh))
}

// TestEndToEnd_FileModeOnlyChange flips only the permission of a tracked file
// (0644 -> 0775 and back to read-only) and checks every flip is a real,
// non-empty commit that leaves status clean.
//
// Skipped until the server can store a mode-only change: treehash.TreeHash
// hashes file names and content hashes but not modes, and FileListByTree reads
// the files of the oldest tree node sharing a hash, so a chmod-only push gets
// an identical tree hash and reads back the previous modes.
func TestEndToEnd_FileModeOnlyChange(t *testing.T) {
	skipWithoutUnixPerms(t)
	t.Skip("server limitation: tree hashes ignore file modes, so a mode-only push is an empty commit")
	t.Parallel()
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	work := cloneWorktree(t, grpcClient, auth, host, "main")
	wc := openWorkingCopy(t, work)
	writeFile(t, work, "run.sh", "#!/bin/sh\necho hi\n")
	require.NoError(t, wc.Add(ctx, []string{"run.sh"}))
	require.NoError(t, pusher.Run(ctx, work, "add run.sh"))
	requireCleanStatus(t, wc)

	steps := []struct {
		perm fs.FileMode
		want int
	}{
		{0o775, e2eModeExecutable},
		{0o444, e2eModeReadOnly},
		{0o744, e2eModeExecutable},
	}
	for _, step := range steps {
		before, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
		require.NoError(t, err)
		headBefore := branchHeadCommit(t, dbConn, "main")

		chmodFile(t, work, "run.sh", step.perm)
		st, err := wc.Status(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"run.sh"}, st.Modified, "a mode change to %#o must show as modified", uint32(step.perm))

		require.NoError(t, wc.Add(ctx, []string{"run.sh"}))
		require.NoError(t, pusher.Run(ctx, work, "chmod run.sh"))

		after, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
		require.NoError(t, err)
		require.Equal(t, step.want, merge.Flatten(after)["run.sh"].Mode, "step %#o", uint32(step.perm))
		require.NotEqual(t, before.Hash, after.Hash, "a mode-only push must change the tree (no empty commit)")
		require.NotEqual(t, headBefore.ID, branchHeadCommit(t, dbConn, "main").ID)
		require.Equal(t, step.want, merge.Flatten(after)["run.sh"].Mode)
		requireCleanStatus(t, wc)

		err = pusher.Run(ctx, work, "nothing left")
		require.ErrorContains(t, err, "nothing staged to push")

		fresh := cloneWorktree(t, grpcClient, auth, host, "main")
		requireMaterializedClass(t, fresh, "run.sh", step.want)
		assertFileContent(t, fresh, "run.sh", "#!/bin/sh\necho hi\n")
	}
}

func requireMaterializedClass(t *testing.T, root, path string, want int) {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	require.NoError(t, err)
	perm := info.Mode().Perm()
	switch want {
	case e2eModeExecutable:
		require.NotZero(t, perm&0o111, "%s must be executable, got %v", path, perm)
	case e2eModeReadOnly:
		require.Zero(t, perm&0o111, "%s must not be executable, got %v", path, perm)
		require.Zero(t, perm&0o222, "%s must be read-only, got %v", path, perm)
	default:
		require.Zero(t, perm&0o111, "%s must not be executable, got %v", path, perm)
		require.NotZero(t, perm&0o200, "%s must be writable, got %v", path, perm)
	}
}
