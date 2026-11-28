package e2e

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	serverUsecase "github.com/nipalab/nipa/internal/usecase"
)

// rawPush sends a unary Push built by hand, so no client-side guard stands
// between the test and the server. Files are empty: no chunk is needed.
func (e *strategyEnv) rawPush(t *testing.T, branch string, files, removed []string) (*pb.PushResponse, error) {
	t.Helper()
	return e.rawPushOnto(t, branch, branchHeadCommit(t, e.dbConn, branch), files, removed)
}

// rawPushOnto is rawPush with the expected head given; nil is an empty branch.
func (e *strategyEnv) rawPushOnto(t *testing.T, branch string, head *serverDomain.Commit, files, removed []string) (*pb.PushResponse, error) {
	t.Helper()
	req := &pb.PushRequest{
		Context:      e.project,
		Branch:       branch,
		Message:      "raw push",
		RemovedFiles: removed,
	}
	if head != nil {
		id := head.ID.Base36()
		req.BaseCommitId = &id
	}
	emptyHash := treehash.FileHash(nil).String()
	for _, path := range files {
		req.Files = append(req.Files, &pb.PushFile{Path: path, Mode: pb.FileMode_FILE_MODE_READ_WRITE, FileHash: emptyHash})
	}
	return e.rpc.Push(e.adminCtx, req)
}

// treePaths lists the file and directory paths of a branch head and fails on
// a tree no working copy can hold: a name that is both a file and a
// directory, or used twice, in one directory.
func (e *strategyEnv) treePaths(t *testing.T, branch string) (files, dirs []string) {
	t.Helper()
	root, err := e.grpcClient.GetTreeNodeManifest(e.ctx, e2eOrgSlug, e2eProjectSlug, branch, nil)
	require.NoError(t, err)
	var walk func(node *serverDomain.TreeNode, dir string)
	walk = func(node *serverDomain.TreeNode, dir string) {
		names := map[string]string{}
		claim := func(name, kind string) {
			if other, taken := names[name]; taken {
				require.Failf(t, "corrupt tree", "%q is stored as %s and as %s in %q", name, other, kind, dir)
			}
			names[name] = kind
		}
		for _, f := range node.FileChildren {
			claim(f.Name, "a file")
			files = append(files, joinRepoPath(dir, f.Name))
		}
		for _, child := range node.TreeChildren {
			claim(child.Name, "a directory")
			dirs = append(dirs, joinRepoPath(dir, child.Name))
			walk(child, joinRepoPath(dir, child.Name))
		}
	}
	if root != nil {
		walk(root, "")
	}
	sort.Strings(files)
	sort.Strings(dirs)
	return files, dirs
}

func joinRepoPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

var clashBase = []string{
	"a/b",
	"a/x/one",
	"a/x/sub/deep",
	"a/x/two",
	"e/only",
	"rootdir/f",
	"rootfile",
}

func setupClashEnv(t *testing.T) *strategyEnv {
	t.Helper()
	env := setupStrategyEnv(t)
	_, err := env.rawPushOnto(t, "main", nil, clashBase, nil)
	require.NoError(t, err)
	files, _ := env.treePaths(t, "main")
	require.Equal(t, clashBase, files)
	return env
}

// TestEndToEnd_PushRejectsFileFolderClash drives the server with hand-built
// pushes that would leave a name as a file and as a directory in the same
// parent. Each must be refused as a user error naming the path, before
// anything is written.
func TestEndToEnd_PushRejectsFileFolderClash(t *testing.T) {
	t.Parallel()
	env := setupClashEnv(t)

	cases := []struct {
		name    string
		files   []string
		removed []string
		want    []string
	}{
		{name: "file over an untouched folder", files: []string{"a/x"}, want: []string{`"a/x"`}},
		{name: "file over a folder that keeps a file", files: []string{"a/x"}, removed: []string{"a/x/one"}, want: []string{`"a/x"`}},
		{name: "file over a folder that keeps only a subfolder", files: []string{"a/x"}, removed: []string{"a/x/one", "a/x/two"}, want: []string{`"a/x"`}},
		{name: "file over a root folder", files: []string{"rootdir"}, want: []string{`"rootdir"`}},
		{name: "file below a tracked file", files: []string{"a/b/c"}, want: []string{`"a/b"`}},
		{name: "file deep below a tracked file", files: []string{"a/b/c/d"}, want: []string{`"a/b"`}},
		{name: "file below a root file", files: []string{"rootfile/c"}, want: []string{`"rootfile"`}},
		{name: "new file and files below it in one push", files: []string{"n/p", "n/p/q"}, want: []string{`"n/p"`}},
		{name: "new root file and files below it in one push", files: []string{"p", "p/q"}, want: []string{`"p"`}},
		{name: "both kinds in one push", files: []string{"a/x", "a/b/c", "rootfile/c"}, want: []string{`"a/x"`, `"a/b"`, `"rootfile"`}},
		{name: "replaced file and a file below it", files: []string{"a/b", "a/b/c"}, want: []string{`"a/b"`}},
	}
	before := branchHeadCommit(t, env.dbConn, "main")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.rawPush(t, "main", tc.files, tc.removed)
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err), err.Error())
			for _, want := range tc.want {
				require.Contains(t, status.Convert(err).Message(), want)
			}
			require.Contains(t, status.Convert(err).Message(), "both a file and a folder")
			require.Equal(t, before.ID, branchHeadCommit(t, env.dbConn, "main").ID, "a refused push must not move the branch")
		})
	}

	files, _ := env.treePaths(t, "main")
	require.Equal(t, clashBase, files)
	cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
}

// TestEndToEnd_PushRejectsAbsolutePath covers a path with a leading slash:
// it used to pass validation and then either vanished from the tree or made
// the same directory twice.
func TestEndToEnd_PushRejectsAbsolutePath(t *testing.T) {
	t.Parallel()
	env := setupClashEnv(t)
	for _, files := range [][]string{{"/lead/f"}, {"lead/g", "/lead/f"}, {"/top"}} {
		_, err := env.rawPush(t, "main", files, nil)
		require.Error(t, err, "%v", files)
		require.Equal(t, codes.InvalidArgument, status.Code(err), err.Error())
	}
	_, err := env.rawPush(t, "main", nil, []string{"/a/b"})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err), err.Error())
	files, _ := env.treePaths(t, "main")
	require.Equal(t, clashBase, files)
}

// TestEndToEnd_PushAllowsFileFolderSwaps covers the swaps that must keep
// working: everything a name used to be is deleted in the push that turns it
// into the other kind.
func TestEndToEnd_PushAllowsFileFolderSwaps(t *testing.T) {
	t.Parallel()
	env := setupClashEnv(t)

	// A folder with nested content becomes a file.
	_, err := env.rawPush(t, "main", []string{"a/x"}, []string{"a/x/one", "a/x/two", "a/x/sub/deep"})
	require.NoError(t, err)
	files, dirs := env.treePaths(t, "main")
	require.Equal(t, []string{"a/b", "a/x", "e/only", "rootdir/f", "rootfile"}, files)
	require.Equal(t, []string{"a", "e", "rootdir"}, dirs)

	// A file becomes a folder, nested and at the root.
	_, err = env.rawPush(t, "main", []string{"a/b/c", "a/b/d/e", "rootfile/c"}, []string{"a/b", "rootfile"})
	require.NoError(t, err)
	files, dirs = env.treePaths(t, "main")
	require.Equal(t, []string{"a/b/c", "a/b/d/e", "a/x", "e/only", "rootdir/f", "rootfile/c"}, files)
	require.Equal(t, []string{"a", "a/b", "a/b/d", "e", "rootdir", "rootfile"}, dirs)

	// A folder emptied by deletions is replaced by a file in the same push,
	// at the root and in a folder that had only a subfolder left.
	_, err = env.rawPush(t, "main", []string{"e", "a/b/d"}, []string{"e/only", "a/b/d/e"})
	require.NoError(t, err)
	files, dirs = env.treePaths(t, "main")
	require.Equal(t, []string{"a/b/c", "a/b/d", "a/x", "e", "rootdir/f", "rootfile/c"}, files)
	require.Equal(t, []string{"a", "a/b", "rootdir", "rootfile"}, dirs)

	// And back again in one push.
	_, err = env.rawPush(t, "main", []string{"e/only", "a/x/one"}, []string{"e", "a/x"})
	require.NoError(t, err)
	files, _ = env.treePaths(t, "main")
	require.Equal(t, []string{"a/b/c", "a/b/d", "a/x/one", "e/only", "rootdir/f", "rootfile/c"}, files)

	dir := cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
	assertFileContent(t, dir, "a/x/one", "")
	assertFileContent(t, dir, "e/only", "")
}

// storeClashingCommit writes, behind the usecases, the tree a push could
// store before the check: a/x as a file and as a directory holding a/x/one.
func storeClashingCommit(t *testing.T, env *strategyEnv) {
	t.Helper()
	ctx := context.Background()
	org, err := sqlite.NewOrgRepository(env.dbConn).GetBySlug(ctx, e2eOrgSlug)
	require.NoError(t, err)
	project, err := sqlite.NewProjectRepository(env.dbConn).GetByOrgIDAndSlug(ctx, org.ID, e2eProjectSlug)
	require.NoError(t, err)
	branch, err := sqlite.NewBranchRepository(env.dbConn).GetBranchByName(ctx, project.ID, "main")
	require.NoError(t, err)
	admin, err := sqlite.NewUserRepository(env.dbConn).GetByEmail(ctx, e2eSuperAdminEmail)
	require.NoError(t, err)

	empty := treehash.FileHash(nil)
	file := func(name string) treehash.FileEntry { return treehash.FileEntry{Name: name, Mode: 0o644, Hash: empty} }
	xHash := treehash.TreeHash([]treehash.FileEntry{file("one")}, nil)
	aHash := treehash.TreeHash([]treehash.FileEntry{file("x")}, []treehash.TreeEntry{{Name: "x", Hash: xHash}})
	rootHash := treehash.TreeHash([]treehash.FileEntry{file("keep")}, []treehash.TreeEntry{{Name: "a", Hash: aHash}})
	rootID, aID := int64(1), int64(2)
	node, err := snow.NewNode(0)
	require.NoError(t, err)
	req := serverUsecase.ApplyPushRequest{
		ProjectID:  project.ID,
		BranchID:   branch.ID,
		CommitID:   node.Generate(),
		CommitHash: treehash.CommitHashWithOrigin(rootHash, nil, "clash", ""),
		UserID:     admin.ID,
		Message:    "clash",
		Nodes: []serverUsecase.PushNodeRow{
			{ID: rootID, Hash: rootHash, Name: "root", Mode: 0o444},
			{ID: aID, Hash: aHash, Name: "a", Mode: 0o444, ParentID: &rootID},
			{ID: 3, Hash: xHash, Name: "x", Mode: 0o444, ParentID: &aID},
		},
		Files: []serverUsecase.PushFileRow{
			{Name: "keep", Mode: 0o644, Encoding: "raw", Hash: empty, TreeID: rootID},
			{Name: "x", Mode: 0o644, Encoding: "raw", Hash: empty, TreeID: aID},
			{Name: "one", Mode: 0o644, Encoding: "raw", Hash: empty, TreeID: 3},
		},
	}
	require.NoError(t, sqlite.NewPushRepository(env.dbConn).ApplyPush(ctx, req))
}

// TestEndToEnd_CloneOfAClashingTreeFailsCleanly covers a commit stored
// before the check: a clone names the path and writes no working file, and
// the branch is repaired by a push that deletes one of the two.
func TestEndToEnd_CloneOfAClashingTreeFailsCleanly(t *testing.T) {
	t.Parallel()
	env := setupStrategyEnv(t)
	_, err := env.rawPushOnto(t, "main", nil, []string{"a/x/one", "keep"}, nil)
	require.NoError(t, err)
	earlier := cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
	storeClashingCommit(t, env)

	// An update of an existing clone stops before it writes: the file a/x
	// cannot replace the folder that keeps a/x/one.
	err = clientusecase.NewUpdate(env.auth, env.grpcClient, localrepo.NewLocalRepo()).Run(context.Background(), earlier)
	require.Error(t, err)
	t.Logf("update error: %v", err)
	require.ErrorContains(t, err, "a/x/one (tracked file the target commit keeps: that commit holds a path as both a file and a folder)")
	require.ErrorContains(t, err, "the target commit is damaged")
	assertFileContent(t, earlier, "a/x/one", "")

	repo := clientusecase.NewRepo(env.auth, env.grpcClient, localrepo.NewLocalRepo())
	url := "http://" + env.host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "main")
	err = repo.Clone(context.Background(), url, env.host, e2eOrgSlug, e2eProjectSlug, "", nil, target)
	require.Error(t, err)
	t.Logf("clone error: %v", err)
	require.ErrorContains(t, err, `"a/x"`)
	require.ErrorContains(t, err, "both a file and a folder")
	_, statErr := os.Stat(filepath.Join(target, "keep"))
	require.True(t, os.IsNotExist(statErr), "no working file is written")
	_, statErr = os.Stat(filepath.Join(target, "a"))
	require.True(t, os.IsNotExist(statErr), "no folder is created")

	// A push onto the damaged head that leaves the clash alone is refused;
	// one that deletes the file repairs the branch.
	_, err = env.rawPush(t, "main", []string{"a/z"}, nil)
	require.Error(t, err)
	require.Contains(t, status.Convert(err).Message(), `"a/x" already is a file and a folder`)
	_, err = env.rawPush(t, "main", []string{"other"}, nil)
	require.NoError(t, err, "a push that does not rebuild the damaged directory is not checked against it")
	_, err = env.rawPush(t, "main", nil, []string{"a/x"})
	require.NoError(t, err)
	files, _ := env.treePaths(t, "main")
	require.Equal(t, []string{"a/x/one", "keep", "other"}, files)
	cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
}

func addPaths(t *testing.T, dir string, paths ...string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	defer func() { _ = lr.Close() }()
	wc, err := clientusecase.NewWorkingCopy(lr, dir)
	require.NoError(t, err)
	require.NoError(t, wc.Add(context.Background(), paths))
}

func stagedPaths(t *testing.T, dir string) []string {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(dir))
	defer func() { _ = lr.Close() }()
	staged, err := lr.ListStaged()
	require.NoError(t, err)
	return staged
}

// TestEndToEnd_ClientFileFolderSwap is the user's scenario through the real
// client: a tracked folder is replaced by a file and only that path is added.
// add stages the deletions below it, so the push is a complete swap; a staged
// list without them is refused locally before anything is uploaded.
func TestEndToEnd_ClientFileFolderSwap(t *testing.T) {
	t.Parallel()
	env := setupStrategyEnv(t)
	ctx := context.Background()
	dir := env.mainDir
	writeFile(t, dir, "a/x/one", "1\n")
	writeFile(t, dir, "a/x/sub/two", "2\n")
	writeFile(t, dir, "a/b", "b\n")
	addPaths(t, dir, ".")
	require.NoError(t, env.pusher.Run(ctx, dir, "seed"))
	seeded := branchHeadCommit(t, env.dbConn, "main")

	require.NoError(t, os.RemoveAll(filepath.Join(dir, "a", "x")))
	writeFile(t, dir, "a/x", "a file now\n")

	// Staged without the deletions (a client that does not stage them): the
	// push is refused locally and nothing changes.
	stagePaths(t, dir, []string{"a/x"})
	err := env.pusher.Run(ctx, dir, "swap")
	require.ErrorContains(t, err, `"a/x" is staged as a file, but the tracked files below it are not staged for deletion: a/x/one, a/x/sub/two`)
	require.Equal(t, seeded.ID, branchHeadCommit(t, env.dbConn, "main").ID)
	require.Equal(t, []string{"a/x"}, stagedPaths(t, dir))
	commitID, _ := pinnedCommit(t, dir)
	require.Equal(t, seeded.ID.Base36(), commitID)

	// nipa add a/x stages the file and what it displaced.
	addPaths(t, dir, "a/x")
	require.Equal(t, []string{"a/x", "a/x/one", "a/x/sub/two"}, stagedPaths(t, dir))
	require.NoError(t, env.pusher.Run(ctx, dir, "swap"))
	assertStagedEmpty(t, dir)
	files, dirs := env.treePaths(t, "main")
	require.Equal(t, []string{"a/b", "a/x"}, files)
	require.Equal(t, []string{"a"}, dirs)

	// The other direction: a tracked file becomes a folder and only a file
	// below it is added.
	require.NoError(t, os.Remove(filepath.Join(dir, "a", "b")))
	writeFile(t, dir, "a/b/c", "c\n")
	addPaths(t, dir, "a/b/c")
	require.Equal(t, []string{"a/b", "a/b/c"}, stagedPaths(t, dir))
	require.NoError(t, env.pusher.Run(ctx, dir, "swap back"))
	files, _ = env.treePaths(t, "main")
	require.Equal(t, []string{"a/b/c", "a/x"}, files)

	fresh := cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
	assertFileContent(t, fresh, "a/x", "a file now\n")
	assertFileContent(t, fresh, "a/b/c", "c\n")
}

// TestEndToEnd_MergeRequestFileFolderClash merges a branch that turned a
// folder into a file into a target that added a file to that folder. The
// three-way merge sees no conflict (no path changed on both sides); the tree
// builder does, for every strategy that builds a tree.
func TestEndToEnd_MergeRequestFileFolderClash(t *testing.T) {
	t.Parallel()
	env := setupStrategyEnv(t)
	ctx := context.Background()
	env.commit(t, env.mainDir, "m/x/one", "1\n", "seed main")

	featureDir := env.branch(t, "feature")
	require.NoError(t, os.RemoveAll(filepath.Join(featureDir, "m", "x")))
	writeFile(t, featureDir, "m/x", "file\n")
	addPaths(t, featureDir, "m/x")
	require.NoError(t, env.pusher.Run(ctx, featureDir, "folder becomes a file"))
	env.commit(t, env.mainDir, "m/x/two", "2\n", "main adds to the folder")
	before := branchHeadCommit(t, env.dbConn, "main")

	number := env.createMR(t, "feature", "File/folder clash")
	for _, strategy := range []string{"merge", "squash", "rebase"} {
		_, err := env.merge(t, number, strategy, false)
		require.Equal(t, codes.FailedPrecondition, status.Code(err), "%s: %v", strategy, err)
		require.Contains(t, status.Convert(err).Message(), "merge conflicts in m/x", strategy)
		require.Contains(t, status.Convert(err).Message(), "one branch has the path as a file, the other as a folder", strategy)
		require.Equal(t, before.ID, branchHeadCommit(t, env.dbConn, "main").ID, "%s must not land", strategy)
	}
	files, _ := env.treePaths(t, "main")
	require.Equal(t, []string{"m/x/one", "m/x/two"}, files)
	cloneWorktree(t, env.grpcClient, env.auth, env.host, "main")
}
