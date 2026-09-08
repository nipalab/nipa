package localrepo

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestFindRepoRoot(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	lr.Close()

	withWD(t, target, func() {
		root, err := FindRepoRoot()
		require.NoError(t, err)
		require.Equal(t, target, root)
	})
}

func TestFindRepoRoot_AtChildDir(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	lr.Close()

	child := filepath.Join(target, "a", "b", "c")
	require.NoError(t, os.MkdirAll(child, 0o755))

	withWD(t, child, func() {
		root, err := FindRepoRoot()
		require.NoError(t, err)
		require.Equal(t, target, root)
	})
}

func TestFindRepoRoot_NotFound(t *testing.T) {
	withWD(t, t.TempDir(), func() {
		_, err := FindRepoRoot()
		require.Error(t, err)
		require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
	})
}

func TestFindRepoRoot_MaxDepth(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	lr.Close()

	deep := target
	for i := 0; i < MaxSearchDepth+1; i++ {
		deep = filepath.Join(deep, "d")
	}
	require.NoError(t, os.MkdirAll(deep, 0o755))

	withWD(t, deep, func() {
		_, err := FindRepoRoot()
		require.Error(t, err)
	})
}

func withWD(t *testing.T, dir string, fn func()) {
	t.Helper()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	fn()
}

func TestNewLocalRepoWithTarget(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepoWithTarget(target)
	require.NotNil(t, lr)
	require.Equal(t, target, lr.target)
}

func TestNewLocalRepoWithTarget_LoadConfig(t *testing.T) {
	target := t.TempDir()
	init := NewLocalRepo()
	require.NoError(t, init.Init(target))
	cfg := domain.Config{Url: "http://example.com/org/project", Branch: "main"}
	require.NoError(t, init.SaveConfig(cfg))
	init.Close()

	lr := NewLocalRepoWithTarget(target)
	got, err := lr.LoadConfig()
	require.NoError(t, err)
	require.Equal(t, cfg, *got)
}

func TestSaveConfig_LoadConfig(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	cfg := domain.Config{Url: "http://example.com/org/project", Branch: "main"}
	require.NoError(t, lr.SaveConfig(cfg))

	got, err := lr.LoadConfig()
	require.NoError(t, err)
	require.Equal(t, cfg, *got)
}

func TestLoadConfig_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	_, err := lr.LoadConfig()
	require.Error(t, err)
}

func TestSaveConfig_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	err := lr.SaveConfig(domain.Config{Url: "u", Branch: "b"})
	require.Error(t, err)
}

func TestInit_Idempotent(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	require.NoError(t, lr.Init(target))
	require.NoError(t, lr.Init(target))
	lr.Close()

	again := NewLocalRepo()
	require.NoError(t, again.Init(target))
	require.NoError(t, again.SaveTree(nil))
	again.Close()
}

func TestSaveTree_Flattens(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	root := treeFixture()
	require.NoError(t, lr.SaveTree(root))

	dbFile := filepath.Join(target, ConfigDir, DBFile)
	require.FileExists(t, dbFile)
	configFile := filepath.Join(target, ConfigDir, ConfigFile)

	_, err := os.Stat(filepath.Join(target, ConfigDir))
	require.NoError(t, err)
	require.NoFileExists(t, configFile)

	count := countRows(t, lr.db, "tree_nodes")
	require.Equal(t, 2, count)
	require.Equal(t, 1, countRows(t, lr.db, "files"))

	var treeHash string
	require.NoError(t, lr.db.QueryRow(`SELECT value FROM meta WHERE key='tree_hash'`).Scan(&treeHash))
	require.Equal(t, root.Hash.String(), treeHash)
}

func TestSaveTree_ReplacesPrevious(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	require.NoError(t, lr.SaveTree(treeFixture()))
	require.NoError(t, lr.SaveTree(treeFixture()))

	require.Equal(t, 2, countRows(t, lr.db, "tree_nodes"))
	require.Equal(t, 1, countRows(t, lr.db, "files"))
}

func TestSaveTree_ChunksAreCachedAcrossSaves(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	require.NoError(t, lr.SaveTree(treeFixture()))
	require.Equal(t, 1, countRows(t, lr.db, "chunks"))
	require.Equal(t, 1, countRows(t, lr.db, "file_chunks"))

	reSave := treeFixture()
	reSave.Name = "root-v2"
	require.NoError(t, lr.SaveTree(reSave))
	require.Equal(t, 1, countRows(t, lr.db, "chunks"), "re-saving a chunk with the same hash should not duplicate it")
	require.Equal(t, 1, countRows(t, lr.db, "file_chunks"), "file_chunks is snapshot-per-replace")

	otherChunk := serverDomain.Hash{0x60}
	replaced := &serverDomain.TreeNode{
		Hash: serverDomain.Hash{0x61},
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Hash:      serverDomain.Hash{0x62},
			Name:      "new.bin",
			SizeBytes: 4,
			Chunks:    []serverDomain.Chunk{{Hash: otherChunk, SizeBytes: 4}},
		}},
	}
	require.NoError(t, lr.SaveTree(replaced))
	require.Equal(t, 2, countRows(t, lr.db, "chunks"), "cache should keep the now-orphaned old chunk")
	require.Equal(t, 1, countRows(t, lr.db, "file_chunks"), "only the current file's mapping remains")
}

func TestSaveTree_Empty(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	require.NoError(t, lr.SaveTree(nil))

	require.Equal(t, 0, countRows(t, lr.db, "tree_nodes"))
	require.Equal(t, 0, countRows(t, lr.db, "files"))

	var treeHash string
	require.NoError(t, lr.db.QueryRow(`SELECT value FROM meta WHERE key='tree_hash'`).Scan(&treeHash))
	require.Equal(t, "", treeHash)
}

func TestSaveTree_EmptyClearsPreviousSnapshot(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	require.NoError(t, lr.SaveTree(treeFixture()))
	require.Equal(t, 2, countRows(t, lr.db, "tree_nodes"))

	require.NoError(t, lr.SaveTree(nil))
	require.Equal(t, 0, countRows(t, lr.db, "tree_nodes"))
	require.Equal(t, 0, countRows(t, lr.db, "files"))
	require.Equal(t, 0, countRows(t, lr.db, "file_chunks"))

	var treeHash string
	require.NoError(t, lr.db.QueryRow(`SELECT value FROM meta WHERE key='tree_hash'`).Scan(&treeHash))
	require.Equal(t, "", treeHash)
}

func TestSaveTree_NoDeletesWhenUnchanged(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	require.NoError(t, lr.SaveTree(treeFixture()))

	_, err := lr.db.Exec(`CREATE TRIGGER fail_any_delete
		AFTER DELETE ON files BEGIN SELECT RAISE(ABORT, 'unexpected delete'); END;`)
	require.NoError(t, err)
	_, err = lr.db.Exec(`CREATE TRIGGER fail_any_node_delete
		AFTER DELETE ON tree_nodes BEGIN SELECT RAISE(ABORT, 'unexpected delete'); END;`)
	require.NoError(t, err)

	require.NoError(t, lr.SaveTree(treeFixture()), "re-saving an unchanged tree must not touch any row")

	require.Equal(t, 2, countRows(t, lr.db, "tree_nodes"))
	require.Equal(t, 1, countRows(t, lr.db, "files"))
}

func TestSaveTree_IncrementalUpdate(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	require.NoError(t, lr.SaveTree(treeFixture()))
	require.Equal(t, 2, countRows(t, lr.db, "tree_nodes"))
	require.Equal(t, 1, countRows(t, lr.db, "files"))

	next := treeFixture()
	next.Name = "root-v2"
	next.TreeChildren = append(next.TreeChildren, &serverDomain.TreeNode{
		Hash: serverDomain.Hash{0x07},
		Name: "docs",
		FileChildren: []*serverDomain.File{{
			Hash:      serverDomain.Hash{0x08},
			Name:      "readme.txt",
			SizeBytes: 8,
			Chunks:    []serverDomain.Chunk{{Hash: serverDomain.Hash{0x09}, SizeBytes: 8}},
		}},
	})
	require.NoError(t, lr.SaveTree(next))

	require.Equal(t, 3, countRows(t, lr.db, "tree_nodes"), "assets keeps its row, docs is added")
	require.Equal(t, 2, countRows(t, lr.db, "files"))
	require.Equal(t, 2, countRows(t, lr.db, "chunks"), "chunk cache accumulates")
}

func TestSaveTree_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	err := lr.SaveTree(&serverDomain.TreeNode{Name: "root"})
	require.Error(t, err)
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
	return n
}

func treeFixture() *serverDomain.TreeNode {
	rootHash := serverDomain.Hash{0x01}
	dirHash := serverDomain.Hash{0x02}
	fileHash := serverDomain.Hash{0x03}
	chunkHash := serverDomain.Hash{0x04}
	return &serverDomain.TreeNode{
		Hash: rootHash,
		Name: "root",
		TreeChildren: []*serverDomain.TreeNode{
			{
				Hash: dirHash,
				Name: "assets",
				FileChildren: []*serverDomain.File{
					{
						Hash:      fileHash,
						Name:      "logo.png",
						Mode:      0o644,
						SizeBytes: 10,
						IsBinary:  true,
						Chunks:    []serverDomain.Chunk{{Hash: chunkHash, SizeBytes: 10}},
					},
				},
			},
		},
	}
}
