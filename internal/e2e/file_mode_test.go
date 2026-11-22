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
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
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
// (0644 -> 0775 -> 0444 -> 0744) and checks every flip is a real,
// non-empty commit that leaves status clean: the tree hash includes the file
// mode class, so the changed directory gets a new hash and is read back from
// its own rows.
func TestEndToEnd_FileModeOnlyChange(t *testing.T) {
	skipWithoutUnixPerms(t)
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
		// A removed write bit on a file tracked as writable is an engine
		// plugin's lock guard, not a mode (trackedModeFromPerm/pushPerm), so
		// 0444 drops the exec bit and lands read-write.
		{0o444, e2eModeReadWrite},
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

// TestEndToEnd_FileModeKeptDirectory checks modes survive directories the
// push does not rebuild: an unchanged executable inside an unchanged directory
// keeps its mode when a sibling directory changes, and two directories with
// the same names and content but different modes stay distinct trees instead
// of sharing the rows of whichever was stored first.
func TestEndToEnd_FileModeKeptDirectory(t *testing.T) {
	skipWithoutUnixPerms(t)
	t.Parallel()
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	work := cloneWorktree(t, grpcClient, auth, host, "main")
	wc := openWorkingCopy(t, work)
	files := map[string]fs.FileMode{
		"plain/tool.sh": 0o644,
		"plain/README":  0o644,
		"tools/tool.sh": 0o755,
		"tools/README":  0o644,
		"src/main.c":    0o644,
	}
	paths := make([]string, 0, len(files))
	for path, perm := range files {
		content := "int main() { return 0; }\n"
		if path != "src/main.c" {
			content = "#!/bin/sh\necho tool\n"
		}
		writeFile(t, work, path, content)
		chmodFile(t, work, path, perm)
		paths = append(paths, path)
	}
	require.NoError(t, wc.Add(ctx, paths))
	require.NoError(t, pusher.Run(ctx, work, "add tools"))

	want := map[string]int{
		"plain/tool.sh": e2eModeReadWrite,
		"plain/README":  e2eModeReadWrite,
		"tools/tool.sh": e2eModeExecutable,
		"tools/README":  e2eModeReadWrite,
		"src/main.c":    e2eModeReadWrite,
	}
	check := func(step string) {
		t.Helper()
		tree, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
		require.NoError(t, err)
		flat := merge.Flatten(tree)
		for path, mode := range want {
			require.Equal(t, mode, flat[path].Mode, "%s: server mode of %s", step, path)
		}
		requireCleanStatus(t, wc)
		fresh := cloneWorktree(t, grpcClient, auth, host, "main")
		for path, mode := range want {
			requireMaterializedClass(t, fresh, path, mode)
		}
		requireCleanStatus(t, openWorkingCopy(t, fresh))
	}
	check("initial push")

	// Only src changes: tools/ and plain/ are carried over by hash.
	writeFile(t, work, "src/main.c", "int main() { return 1; }\n")
	require.NoError(t, wc.Add(ctx, []string{"src/main.c"}))
	require.NoError(t, pusher.Run(ctx, work, "change src"))
	check("sibling change")

	// A mode-only change in tools/, then another sibling change on top of it.
	chmodFile(t, work, "tools/README", 0o755)
	require.NoError(t, wc.Add(ctx, []string{"tools/README"}))
	require.NoError(t, pusher.Run(ctx, work, "chmod README"))
	want["tools/README"] = e2eModeExecutable
	check("mode-only change")

	writeFile(t, work, "src/main.c", "int main() { return 2; }\n")
	require.NoError(t, wc.Add(ctx, []string{"src/main.c"}))
	require.NoError(t, pusher.Run(ctx, work, "change src again"))
	check("sibling change after chmod")
}

// TestEndToEnd_TreeHashBackfillLegacyBase rewinds a stored tree to the hashes
// a server without mode-aware hashing wrote, runs the startup backfill and
// checks the head reads back with the new hash while a client still pushing
// against the old root hash (no base commit id) is accepted.
func TestEndToEnd_TreeHashBackfillLegacyBase(t *testing.T) {
	skipWithoutUnixPerms(t)
	t.Parallel()
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	work := cloneWorktree(t, grpcClient, auth, host, "main")
	wc := openWorkingCopy(t, work)
	writeFile(t, work, "bin/tool.sh", "#!/bin/sh\n")
	chmodFile(t, work, "bin/tool.sh", 0o755)
	writeFile(t, work, "a.txt", "a\n")
	require.NoError(t, wc.Add(ctx, []string{"bin/tool.sh", "a.txt"}))
	require.NoError(t, pusher.Run(ctx, work, "add tool"))

	tree, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	flat := merge.Flatten(tree)
	plain := func(name, path string) treehash.FileEntry {
		return treehash.FileEntry{Name: name, Hash: flat[path].Hash, Mode: 0o644}
	}
	oldBin := treehash.TreeHash([]treehash.FileEntry{plain("tool.sh", "bin/tool.sh")}, nil)
	oldRoot := treehash.TreeHash([]treehash.FileEntry{plain("a.txt", "a.txt")}, []treehash.TreeEntry{{Name: "bin", Hash: oldBin}})
	require.NotEqual(t, oldRoot, tree.Hash)

	head := branchHeadCommit(t, dbConn, "main")
	_, err = dbConn.ExecContext(ctx, "UPDATE tree_nodes SET hash = ? WHERE id = ?", oldRoot.Bytes(), head.TreeID)
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx, "UPDATE tree_nodes SET hash = ? WHERE parent_tree_id = ? AND name = 'bin'", oldBin.Bytes(), head.TreeID)
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx, "UPDATE tree_hash_backfill SET watermark = 0 WHERE id = 1")
	require.NoError(t, err)

	res, err := serverusecase.BackfillTreeHashes(ctx, sqlite.NewTreeHashRepository(dbConn))
	require.NoError(t, err)
	require.Equal(t, 2, res.Updated)
	after, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.Equal(t, tree.Hash, after.Hash)
	requireCleanStatus(t, wc)

	_, err = grpcClient.Push(ctx, e2eOrgSlug, e2eProjectSlug, "main", serverDomain.Hash{0x01}.String(), "stale", nil, []string{"a.txt"}, "", "")
	require.ErrorContains(t, err, "has moved")
	_, err = grpcClient.Push(ctx, e2eOrgSlug, e2eProjectSlug, "main", oldRoot.String(), "remove a.txt", nil, []string{"a.txt"}, "", "")
	require.NoError(t, err)
	final, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	finalFlat := merge.Flatten(final)
	require.NotContains(t, finalFlat, "a.txt")
	require.Equal(t, e2eModeExecutable, finalFlat["bin/tool.sh"].Mode)
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

// TestEndToEnd_TreeHashBackfillRollingDeploy simulates a push applied by a
// server that predates mode-aware hashes, committed after the backfill: it
// keeps bin/ by reference under the hash bin/ had before the pass rewrote it,
// so no content node resolves the reference and the head manifest loses
// bin/tool.sh. The next backfill pass repoints the reference and a fresh
// clone gets the executable back.
func TestEndToEnd_TreeHashBackfillRollingDeploy(t *testing.T) {
	skipWithoutUnixPerms(t)
	t.Parallel()
	ctx := context.Background()
	dbConn := openTestDB(t)
	host := startTestServer(t, dbConn)
	grpcClient, auth := newLoggedInClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	work := cloneWorktree(t, grpcClient, auth, host, "main")
	wc := openWorkingCopy(t, work)
	writeFile(t, work, "bin/tool.sh", "#!/bin/sh\n")
	chmodFile(t, work, "bin/tool.sh", 0o755)
	writeFile(t, work, "a.txt", "a\n")
	require.NoError(t, wc.Add(ctx, []string{"bin/tool.sh", "a.txt"}))
	require.NoError(t, pusher.Run(ctx, work, "add tool"))

	tree, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	flat := merge.Flatten(tree)
	oldBin := treehash.TreeHash([]treehash.FileEntry{{Name: "tool.sh", Hash: flat["bin/tool.sh"].Hash, Mode: 0o644}}, nil)
	oldRoot := treehash.TreeHash([]treehash.FileEntry{{Name: "a.txt", Hash: flat["a.txt"].Hash, Mode: 0o644}}, []treehash.TreeEntry{{Name: "bin", Hash: oldBin}})

	// Rewind to the old format and backfill, as the upgrade does.
	head := branchHeadCommit(t, dbConn, "main")
	_, err = dbConn.ExecContext(ctx, "UPDATE tree_nodes SET hash = ? WHERE id = ?", oldRoot.Bytes(), head.TreeID)
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx, "UPDATE tree_nodes SET hash = ? WHERE parent_tree_id = ? AND name = 'bin'", oldBin.Bytes(), head.TreeID)
	require.NoError(t, err)
	_, err = dbConn.ExecContext(ctx, "UPDATE tree_hash_backfill SET watermark = 0 WHERE id = 1")
	require.NoError(t, err)
	trees := sqlite.NewTreeHashRepository(dbConn)
	_, err = serverusecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)

	// The old server removes a.txt and keeps bin/ under its old hash.
	org, err := sqlite.NewOrgRepository(dbConn).GetBySlug(ctx, e2eOrgSlug)
	require.NoError(t, err)
	project, err := sqlite.NewProjectRepository(dbConn).GetByOrgIDAndSlug(ctx, org.ID, e2eProjectSlug)
	require.NoError(t, err)
	branch, err := sqlite.NewBranchRepository(dbConn).GetBranchByName(ctx, project.ID, "main")
	require.NoError(t, err)
	oldPodRoot := treehash.TreeHash(nil, []treehash.TreeEntry{{Name: "bin", Hash: oldBin}})
	commitID := snow.ID(987654321)
	require.NoError(t, sqlite.NewPushRepository(dbConn).ApplyPush(ctx, serverusecase.ApplyPushRequest{
		ProjectID: project.ID, BranchID: branch.ID, CommitID: commitID, ParentID: &head.ID,
		CommitHash: treehash.CommitHash(oldPodRoot, []serverDomain.Hash{head.Hash}, "remove a.txt"),
		UserID:     head.UserID, Message: "remove a.txt",
		Nodes: []serverusecase.PushNodeRow{
			{ID: 1, Hash: oldPodRoot, Name: "root", Mode: 0o444},
			{ID: 2, Hash: oldBin, Name: "bin", Mode: 0o444, ParentID: &[]int64{1}[0]},
		},
	}))
	broken, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	require.NotContains(t, merge.Flatten(broken), "bin/tool.sh", "the dangling reference hides bin/")

	res, err := serverusecase.BackfillTreeHashes(ctx, trees)
	require.NoError(t, err)
	require.Equal(t, 1, res.Repaired)
	fixed, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, "main", nil)
	require.NoError(t, err)
	fixedFlat := merge.Flatten(fixed)
	require.NotContains(t, fixedFlat, "a.txt")
	require.Equal(t, e2eModeExecutable, fixedFlat["bin/tool.sh"].Mode)
	fresh := cloneWorktree(t, grpcClient, auth, host, "main")
	requireMaterializedClass(t, fresh, "bin/tool.sh", e2eModeExecutable)
	requireCleanStatus(t, openWorkingCopy(t, fresh))
}
