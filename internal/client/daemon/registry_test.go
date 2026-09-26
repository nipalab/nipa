package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// newTestClone builds a minimal real clone: config plus a snapshot tracking
// tracked.txt, so status runs against the actual localrepo/SQLite stack.
func newTestClone(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(clientDomain.Config{
		Url:    "https://nipa.example.com/default/default",
		Branch: "main",
	}))
	require.NoError(t, lr.SaveTree(&serverDomain.TreeNode{
		Hash: serverDomain.Hash{0x01},
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Hash:      serverDomain.Hash{0x03},
			Name:      "tracked.txt",
			Mode:      0o644,
			SizeBytes: 5,
			Chunks:    []serverDomain.Chunk{{Hash: serverDomain.Hash{0x04}, SizeBytes: 5}},
		}},
	}))
	require.NoError(t, lr.Close())
	return root
}

func TestRegistry_WatchResolvesSubdirectory(t *testing.T) {
	reg := newRegistry(Runners{})
	defer reg.closeAll()
	root := newTestClone(t)
	sub := filepath.Join(root, "assets", "textures")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	rp, err := reg.watch(sub)
	require.NoError(t, err)
	require.Equal(t, root, rp.root)
	require.Equal(t, "main", rp.cfg.Branch)
}

func TestRegistry_WatchIsIdempotent(t *testing.T) {
	reg := newRegistry(Runners{})
	defer reg.closeAll()
	root := newTestClone(t)

	first, err := reg.watch(root)
	require.NoError(t, err)
	second, err := reg.watch(root)
	require.NoError(t, err)
	require.Same(t, first, second)
}

func TestRegistry_WatchRejectsNonRepo(t *testing.T) {
	reg := newRegistry(Runners{})
	defer reg.closeAll()

	_, err := reg.watch(t.TempDir())
	require.ErrorContains(t, err, "not a nipa working copy")

	_, err = reg.watch("")
	require.ErrorContains(t, err, "required")
}

func TestRegistry_RefRequiresWatch(t *testing.T) {
	reg := newRegistry(Runners{})
	defer reg.closeAll()

	_, _, err := reg.ref(newTestClone(t))
	require.ErrorContains(t, err, "not watched")
}

func TestRegistry_UnwatchWaitsForRefs(t *testing.T) {
	reg := newRegistry(Runners{})
	defer reg.closeAll()
	root := newTestClone(t)
	_, err := reg.watch(root)
	require.NoError(t, err)

	_, release, err := reg.ref(root)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- reg.unwatch(root) }()
	select {
	case <-done:
		t.Fatal("unwatch returned while a ref was held")
	case <-time.After(50 * time.Millisecond):
	}

	release()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("unwatch did not finish after the ref was released")
	}

	require.Empty(t, reg.list())
	_, _, err = reg.ref(root)
	require.ErrorContains(t, err, "not watched")
}

func TestRegistry_UnwatchUnknownIsNoOp(t *testing.T) {
	reg := newRegistry(Runners{})
	defer reg.closeAll()
	require.NoError(t, reg.unwatch(newTestClone(t)))
}

func TestRegistry_ListSorted(t *testing.T) {
	reg := newRegistry(Runners{})
	defer reg.closeAll()
	rootA := newTestClone(t)
	rootB := newTestClone(t)
	_, err := reg.watch(rootB)
	require.NoError(t, err)
	_, err = reg.watch(rootA)
	require.NoError(t, err)

	repos := reg.list()
	require.Len(t, repos, 2)
	require.Less(t, repos[0].root, repos[1].root)
}
