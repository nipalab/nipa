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
