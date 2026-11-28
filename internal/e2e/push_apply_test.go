package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/treedelta"
)

// manifestCountingClient is the real gRPC client, counting the manifests a
// push fetches and keeping the tree hash the server answered each push with.
type manifestCountingClient struct {
	*clientgrpc.Client
	manifests int
	treeHash  serverDomain.Hash
}

func (c *manifestCountingClient) GetTreeNodeManifest(ctx context.Context, org, project, branch string, paths []string) (*serverDomain.TreeNode, error) {
	c.manifests++
	return c.Client.GetTreeNodeManifest(ctx, org, project, branch, paths)
}

// The post-push refetch asks for the manifest with its view token.
func (c *manifestCountingClient) GetTreeManifestDelta(ctx context.Context, org, project string, target treedelta.Target, paths []string, base treedelta.Base) (*treedelta.Manifest, error) {
	c.manifests++
	return c.Client.GetTreeManifestDelta(ctx, org, project, target, paths, base)
}

func (c *manifestCountingClient) Push(ctx context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string, parent2CommitHash, baseCommitID string) (*serverDomain.PushResult, error) {
	result, err := c.Client.Push(ctx, org, project, branch, baseTreeHash, message, files, removed, parent2CommitHash, baseCommitID)
	if err == nil {
		c.treeHash = result.TreeHash
	}
	return result, err
}

// storedTree dumps every row of the local snapshot, the tree hash and the
// synced-tree record of a working copy.
func storedTree(t *testing.T, target string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(target, localrepo.ConfigDir, localrepo.DBFile))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	var out []string
	dump := func(label, query string) {
		rows, err := db.Query(query)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var row string
			require.NoError(t, rows.Scan(&row))
			out = append(out, label+" "+row)
		}
		require.NoError(t, rows.Err())
	}
	dump("node", `SELECT path || '|' || parent_path || '|' || hex(hash) || '|' || mode FROM tree_nodes ORDER BY path`)
	dump("file", `SELECT path || '|' || tree_path || '|' || hex(hash) || '|' || size_bytes || '|' || mode || '|' || is_binary || '|' || encoding || '|' || hex(chunks) FROM files ORDER BY path`)
	dump("meta", `SELECT key || '=' || value FROM meta WHERE key IN ('tree_hash', 'synced_tree', 'commit_id') ORDER BY key`)
	return out
}

// assertMatchesFreshClone proves the locally applied tree is what the server
// holds: its root is the pushed tree hash and the head's manifest hash, and
// every stored row equals a fresh clone's, which saves the manifest.
func assertMatchesFreshClone(t *testing.T, grpcClient *clientgrpc.Client, auth *clientusecase.Auth, host, branch, target string, pushed serverDomain.Hash) {
	t.Helper()
	ctx := context.Background()
	manifest, err := grpcClient.GetTreeNodeManifest(ctx, e2eOrgSlug, e2eProjectSlug, branch, nil)
	require.NoError(t, err)
	require.Equal(t, manifest.Hash, pushed, "the push response names the head's root tree")
	require.Equal(t, manifest.Hash.String(), snapshotOf(t, target).TreeHash)

	// The working copy was cloned from an empty branch, so the view token in
	// its synced-tree record came with the push responses; it must be the one
	// a fresh clone gets with the manifest.
	fresh := cloneWorktree(t, grpcClient, auth, host, branch)
	require.Equal(t, storedTree(t, fresh), storedTree(t, target))
}

// withoutView drops the view token from the synced-tree record of a
// storedTree dump.
func withoutView(rows []string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		if start := strings.Index(row, `,"view":"`); strings.HasPrefix(row, "meta synced_tree=") && start >= 0 {
			if end := strings.Index(row[start+len(`,"view":"`):], `"`); end >= 0 {
				row = row[:start] + row[start+len(`,"view":"`)+end+1:]
			}
		}
		out[i] = row
	}
	return out
}

func removeAndStage(t *testing.T, target string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		require.NoError(t, os.Remove(filepath.Join(target, filepath.FromSlash(path))))
		stagePath(t, target, path)
	}
}

func writeAndStage(t *testing.T, target, path, content string) {
	t.Helper()
	writeFile(t, target, path, content)
	stagePath(t, target, path)
}

// The client derives the new root from the base tree and the pushed delta;
// the server derives it from its own rows. Every kind of change must give
// the same hash and the same rows, without a manifest fetch.
func TestEndToEnd_PushAppliesTreeLocally(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth := newLoggedInClient(t, host)
	counting := &manifestCountingClient{Client: grpcClient}
	pusher := clientusecase.NewPush(auth, counting, localrepo.NewLocalRepo())
	locks := clientusecase.NewFileLock(auth, grpcClient, localrepo.NewLocalRepo())

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	binary := string([]byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3, 0, 0xff, 0xfe})

	steps := []struct {
		name string
		do   func(t *testing.T)
	}{
		{"first push to an empty repository", func(t *testing.T) {
			writeAndStage(t, dir, "root.txt", "root\n")
			writeAndStage(t, dir, "src/main.go", "package main\n")
			writeAndStage(t, dir, "src/util.go", "package main // util\n")
			writeAndStage(t, dir, "src/pkg/deep/x.go", "package deep\n")
			writeAndStage(t, dir, "docs/readme.md", "# readme\n")
			writeAndStage(t, dir, "solo/chain/only.txt", "only\n")
			writeAndStage(t, dir, "assets/tex/hero.png", binary)
			writeAndStage(t, dir, "bin/run.sh", "#!/bin/sh\n")
			require.NoError(t, os.Chmod(filepath.Join(dir, "bin", "run.sh"), 0o755))
		}},
		{"modify a file", func(t *testing.T) {
			writeAndStage(t, dir, "src/main.go", "package main\n\nfunc main() {}\n")
		}},
		{"modify a root level file", func(t *testing.T) {
			writeAndStage(t, dir, "root.txt", "root v2\n")
		}},
		{"add a file in an existing directory", func(t *testing.T) {
			writeAndStage(t, dir, "src/extra.go", "package main // extra\n")
		}},
		{"add a file under new nested directories", func(t *testing.T) {
			writeAndStage(t, dir, "src/gen/v1/api/types.go", "package api\n")
			writeAndStage(t, dir, "fresh/top.txt", "top\n")
		}},
		{"remove a file", func(t *testing.T) {
			removeAndStage(t, dir, "src/util.go")
		}},
		{"remove the last file of a directory", func(t *testing.T) {
			removeAndStage(t, dir, "docs/readme.md")
		}},
		{"remove the last file of a nested chain", func(t *testing.T) {
			removeAndStage(t, dir, "solo/chain/only.txt")
		}},
		{"add and remove in one push", func(t *testing.T) {
			writeAndStage(t, dir, "docs/guide/intro.md", "# intro\n")
			writeAndStage(t, dir, "src/pkg/deep/y.go", "package deep // y\n")
			removeAndStage(t, dir, "src/pkg/deep/x.go", "fresh/top.txt", "root.txt")
		}},
		{"mode becomes executable", func(t *testing.T) {
			require.NoError(t, os.Chmod(filepath.Join(dir, "src", "main.go"), 0o755))
			stagePath(t, dir, "src/main.go")
		}},
		{"executable becomes read write", func(t *testing.T) {
			require.NoError(t, os.Chmod(filepath.Join(dir, "bin", "run.sh"), 0o644))
			stagePath(t, dir, "bin/run.sh")
		}},
		{"mode becomes read only", func(t *testing.T) {
			writeAndStage(t, dir, "src/locked.go", "package main // ro\n")
			require.NoError(t, os.Chmod(filepath.Join(dir, "src", "locked.go"), 0o444))
		}},
		{"new binary file and a changed binary", func(t *testing.T) {
			_, err := locks.Lock(ctx, dir, "assets/tex/hero.png", clientusecase.LockOptions{})
			require.NoError(t, err)
			require.NoError(t, os.Chmod(filepath.Join(dir, "assets", "tex", "hero.png"), 0o644))
			writeAndStage(t, dir, "assets/tex/hero.png", binary+"more")
			writeAndStage(t, dir, "assets/blob.dat", binary+"blob")
		}},
		{"text becomes binary", func(t *testing.T) {
			_, err := locks.Lock(ctx, dir, "src/extra.go", clientusecase.LockOptions{})
			require.NoError(t, err)
			require.NoError(t, os.Chmod(filepath.Join(dir, "src", "extra.go"), 0o644))
			writeAndStage(t, dir, "src/extra.go", binary)
		}},
		{"text content under a lockable extension", func(t *testing.T) {
			writeAndStage(t, dir, "assets/mask.psd", "not really a psd\n")
		}},
		{"empty file", func(t *testing.T) {
			writeAndStage(t, dir, "src/empty.txt", "")
		}},
		{"file named like a sibling directory", func(t *testing.T) {
			writeAndStage(t, dir, "src/pkg.txt", "pkg\n")
		}},
	}
	for i, step := range steps {
		step.do(t)
		before := counting.manifests
		require.NoError(t, pusher.Run(ctx, dir, fmt.Sprintf("step %d: %s", i, step.name)), step.name)
		require.Equal(t, before, counting.manifests, "%s: the push must not fetch the manifest", step.name)
		assertStagedEmpty(t, dir)
		assertMatchesFreshClone(t, grpcClient, auth, host, "main", dir, counting.treeHash)
	}

	// Removing everything leaves a head with an empty root.
	for _, f := range snapshotOf(t, dir).Files {
		require.NoError(t, os.Chmod(filepath.Join(dir, filepath.FromSlash(f.Path)), 0o644))
		if f.IsBinary {
			_, err := locks.Lock(ctx, dir, f.Path, clientusecase.LockOptions{})
			require.NoError(t, err)
		}
		removeAndStage(t, dir, f.Path)
	}
	before := counting.manifests
	require.NoError(t, pusher.Run(ctx, dir, "remove everything"))
	require.Equal(t, before, counting.manifests)
	require.Empty(t, snapshotOf(t, dir).Files)
	assertMatchesFreshClone(t, grpcClient, auth, host, "main", dir, counting.treeHash)

	// The record the apply wrote makes the next update a no-op.
	result, err := clientusecase.NewUpdate(auth, grpcClient, localrepo.NewLocalRepo()).RunResult(ctx, dir)
	require.NoError(t, err)
	require.True(t, result.UpToDate)
}

// A stored tree that disagrees with the server's is caught by the root hash
// and repaired by the manifest fetch; the push itself succeeds.
func TestEndToEnd_PushFallsBackOnTreeMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth := newLoggedInClient(t, host)
	counting := &manifestCountingClient{Client: grpcClient}
	pusher := clientusecase.NewPush(auth, counting, localrepo.NewLocalRepo())

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeAndStage(t, dir, "src/a.txt", "a\n")
	writeAndStage(t, dir, "docs/b.txt", "b\n")
	require.NoError(t, pusher.Run(ctx, dir, "seed"))
	require.Zero(t, counting.manifests)

	db, err := sql.Open("sqlite", filepath.Join(dir, localrepo.ConfigDir, localrepo.DBFile))
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE tree_nodes SET hash = zeroblob(32) WHERE path = '/docs'`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	writeAndStage(t, dir, "src/a.txt", "a v2\n")
	require.NoError(t, pusher.Run(ctx, dir, "push over a stale directory hash"))
	require.Equal(t, 1, counting.manifests, "a mismatching root falls back to the manifest")
	assertStagedEmpty(t, dir)
	assertMatchesFreshClone(t, grpcClient, auth, host, "main", dir, counting.treeHash)

	// The fallback left a tree the next push applies locally again.
	writeAndStage(t, dir, "docs/b.txt", "b v2\n")
	require.NoError(t, pusher.Run(ctx, dir, "push after the repair"))
	require.Equal(t, 1, counting.manifests)
	assertMatchesFreshClone(t, grpcClient, auth, host, "main", dir, counting.treeHash)
}

// A sparse checkout holds part of the tree, so it always saves the manifest.
func TestEndToEnd_SparsePushFetchesManifest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth := newLoggedInClient(t, host)
	counting := &manifestCountingClient{Client: grpcClient}
	pusher := clientusecase.NewPush(auth, counting, localrepo.NewLocalRepo())

	seed := cloneWorktree(t, grpcClient, auth, host, "main")
	writeAndStage(t, seed, "src/a.txt", "a\n")
	writeAndStage(t, seed, "docs/b.txt", "b\n")
	require.NoError(t, pusher.Run(ctx, seed, "seed"))
	require.Zero(t, counting.manifests)

	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	sparse := filepath.Join(t.TempDir(), "sparse")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", []string{"src"}, sparse))

	writeAndStage(t, sparse, "src/a.txt", "a from sparse\n")
	require.NoError(t, pusher.Run(ctx, sparse, "sparse push"))
	require.Equal(t, 1, counting.manifests)
	snap := snapshotOf(t, sparse)
	require.Len(t, snap.Files, 1, "the sparse snapshot still holds only the sparse set")
	require.Equal(t, "src/a.txt", snap.Files[0].Path)

	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(sparse))
	defer func() { _ = lr.Close() }()
	record, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	commitID, _ := pinnedCommit(t, sparse)
	require.Len(t, record.View, 32, "the refetched manifest's view token is recorded")
	record.View = nil
	require.Equal(t, &domain.SyncedTree{CommitID: commitID, Sparse: []string{"src"}}, record)
}

// A clean revert commits through pushStaged on top of a synced head: the
// stored tree is the base, so it applies locally.
func TestEndToEnd_RevertAppliesTreeLocally(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth := newLoggedInClient(t, host)
	counting := &manifestCountingClient{Client: grpcClient}
	pusher := clientusecase.NewPush(auth, counting, localrepo.NewLocalRepo())
	reverter := clientusecase.NewRevert(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeAndStage(t, dir, "a.txt", "one\n")
	writeAndStage(t, dir, "keep/k.txt", "keep\n")
	require.NoError(t, pusher.Run(ctx, dir, "add a"))
	writeAndStage(t, dir, "a.txt", "two\n")
	writeAndStage(t, dir, "added/new.txt", "new\n")
	require.NoError(t, pusher.Run(ctx, dir, "change a"))
	second, _ := pinnedCommit(t, dir)
	writeAndStage(t, dir, "keep/k.txt", "keep v2\n")
	require.NoError(t, pusher.Run(ctx, dir, "change k"))
	third, _ := pinnedCommit(t, dir)
	require.Zero(t, counting.manifests)

	outcome, err := reverter.Run(ctx, dir, third, clientusecase.RevertOptions{})
	require.NoError(t, err)
	require.True(t, outcome.Committed)
	require.Zero(t, counting.manifests, "a clean revert applies its commit locally")
	assertFileContent(t, dir, "keep/k.txt", "keep\n")
	assertMatchesFreshClone(t, grpcClient, auth, host, "main", dir, counting.treeHash)

	// Reverting a commit that added a directory removes it again.
	outcome, err = reverter.Run(ctx, dir, second, clientusecase.RevertOptions{})
	require.NoError(t, err)
	require.True(t, outcome.Committed)
	require.Zero(t, counting.manifests)
	assertFileContent(t, dir, "a.txt", "one\n")
	assertMatchesFreshClone(t, grpcClient, auth, host, "main", dir, counting.treeHash)
}

// A clean three-way merge pushes the merge result as a delta on the target
// head the stored tree holds, so it applies locally; resolving a conflict
// pushes over a tree the merge built locally, which falls back.
func TestEndToEnd_MergePushTreeSync(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth := newLoggedInClient(t, host)
	counting := &manifestCountingClient{Client: grpcClient}
	pusher := clientusecase.NewPush(auth, counting, localrepo.NewLocalRepo())
	merger := clientusecase.NewMerge(auth, grpcClient, localrepo.NewLocalRepo(), pusher)

	mainDir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeAndStage(t, mainDir, "a.txt", "a\nb\nc\n")
	writeAndStage(t, mainDir, "conflict.txt", "base\n")
	require.NoError(t, pusher.Run(ctx, mainDir, "seed main"))

	_, err := grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	featDir := cloneWorktree(t, grpcClient, auth, host, "feature")
	writeAndStage(t, featDir, "a.txt", "a\nX\nc\n")
	writeAndStage(t, featDir, "feature/only.txt", "from feature\n")
	require.NoError(t, pusher.Run(ctx, featDir, "feature work"))

	writeAndStage(t, mainDir, "a.txt", "a\nb\nZ\n")
	require.NoError(t, pusher.Run(ctx, mainDir, "main work"))
	require.Zero(t, counting.manifests)

	outcome, err := merger.Run(ctx, mainDir, "feature", clientusecase.MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.MergeCommitted)
	require.Zero(t, counting.manifests, "a clean merge commit applies locally")
	assertFileContent(t, mainDir, "a.txt", "a\nX\nZ\n")
	assertFileContent(t, mainDir, "feature/only.txt", "from feature\n")
	assertMatchesFreshClone(t, grpcClient, auth, host, "main", mainDir, counting.treeHash)

	// Now a conflicting pair of changes.
	writeAndStage(t, featDir, "conflict.txt", "feature side\n")
	require.NoError(t, pusher.Run(ctx, featDir, "feature conflict"))
	writeAndStage(t, mainDir, "conflict.txt", "main side\n")
	require.NoError(t, pusher.Run(ctx, mainDir, "main conflict"))
	require.Zero(t, counting.manifests)

	outcome, err = merger.Run(ctx, mainDir, "feature", clientusecase.MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"conflict.txt"}, outcome.Conflicts)

	writeFile(t, mainDir, "conflict.txt", "resolved\n")
	require.NoError(t, pusher.Run(ctx, mainDir, "resolve"))
	require.Equal(t, 1, counting.manifests, "the stored tree was built by the merge, not synced: fetch the manifest")
	assertStagedEmpty(t, mainDir)
	assertMatchesFreshClone(t, grpcClient, auth, host, "main", mainDir, counting.treeHash)
}
