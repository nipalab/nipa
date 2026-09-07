package localrepo

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestInit_TargetIsFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	lr := NewLocalRepo()
	err := lr.Init(target)
	require.Error(t, err)
	require.Nil(t, lr.db)
}

func TestInit_PingFails_WhenDBPathIsDirectory(t *testing.T) {
	parent := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(parent, ConfigDir, DBFile), 0o755))

	lr := NewLocalRepo()
	err := lr.Init(parent)
	require.Error(t, err)
	require.Nil(t, lr.db)
}

func TestClose_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Close())
	require.NoError(t, lr.Close())
}

func TestClose_AfterClose(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	require.NoError(t, lr.Close())
	require.NoError(t, lr.Close())
	require.Error(t, lr.SaveTree(&serverDomain.TreeNode{Name: "root"}))
}

func TestSaveTree_BeginFails_WhenDBClosed(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	require.NoError(t, db.Close())

	lr := &LocalRepo{db: db}
	err = lr.SaveTree(treeFixture())
	require.Error(t, err)
}

func createTrigger(t *testing.T, db *sql.DB, triggerName, table, condition string) {
	t.Helper()
	createTriggerEvent(t, db, triggerName, table, "BEFORE INSERT", condition)
}

func createDeleteTrigger(t *testing.T, db *sql.DB, triggerName, table string) {
	t.Helper()
	createTriggerEvent(t, db, triggerName, table, "BEFORE DELETE", "")
}

func createTriggerEvent(t *testing.T, db *sql.DB, triggerName, table, event, condition string) {
	t.Helper()
	when := ""
	if condition != "" {
		when = "WHEN " + condition
	}
	_, err := db.Exec(fmt.Sprintf(
		`CREATE TRIGGER %s %s ON %s %s BEGIN SELECT RAISE(ABORT, 'forced failure'); END;`,
		triggerName, event, table, when))
	require.NoError(t, err)
}

func TestSaveTree_Error_ClearTrigger(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table string
	}{
		{name: "file_chunks", table: "file_chunks"},
		{name: "chunks", table: "chunks"},
		{name: "files", table: "files"},
		{name: "tree_nodes", table: "tree_nodes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := t.TempDir()
			lr := NewLocalRepo()
			require.NoError(t, lr.Init(target))
			defer lr.Close()

			require.NoError(t, lr.SaveTree(treeFixture()))

			createDeleteTrigger(t, lr.db, "fail_delete_"+tc.name, tc.table)

			require.Error(t, lr.SaveTree(&serverDomain.TreeNode{Name: "root"}))
			require.Equal(t, 2, countRows(t, lr.db, "tree_nodes"), "failed clear should roll back")
		})
	}
}

func TestSaveTree_Error_MetaSet(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	createTrigger(t, lr.db, "fail_meta", "meta", "")

	require.Error(t, lr.SaveTree(treeFixture()))
	require.Equal(t, 0, countRows(t, lr.db, "tree_nodes"))
}

func TestSaveTree_Error_TreeInsert(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	createTrigger(t, lr.db, "fail_tree_insert", "tree_nodes", "")

	require.Error(t, lr.SaveTree(treeFixture()))
	require.Equal(t, 0, countRows(t, lr.db, "tree_nodes"))
}

func TestSaveTree_Error_TreeInsertChild(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	createTrigger(t, lr.db, "fail_tree_child", "tree_nodes", "NEW.parent_tree_id IS NOT NULL")

	require.Error(t, lr.SaveTree(treeFixture()))
	require.Equal(t, 0, countRows(t, lr.db, "tree_nodes"))
}

func TestSaveTree_Error_FilePath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table string
	}{
		{name: "files", table: "files"},
		{name: "chunks", table: "chunks"},
		{name: "file_chunks", table: "file_chunks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := t.TempDir()
			lr := NewLocalRepo()
			require.NoError(t, lr.Init(target))
			defer lr.Close()

			createTrigger(t, lr.db, "fail_file_"+tc.name, tc.table, "")

			require.Error(t, lr.SaveTree(treeFixture()))
			require.Equal(t, 0, countRows(t, lr.db, "tree_nodes"))
			require.Equal(t, 0, countRows(t, lr.db, "files"))
		})
	}
}

func TestSaveTree_RollsBackPreviousTree_OnError(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	require.NoError(t, lr.SaveTree(treeFixture()))
	require.Equal(t, 2, countRows(t, lr.db, "tree_nodes"))

	createTrigger(t, lr.db, "fail_meta_after", "meta", "")

	require.Error(t, lr.SaveTree(&serverDomain.TreeNode{Name: "new-root"}))
	require.Equal(t, 2, countRows(t, lr.db, "tree_nodes"), "previous tree should survive a failed replace")

	var treeHash string
	require.NoError(t, lr.db.QueryRow(`SELECT value FROM meta WHERE key='tree_hash'`).Scan(&treeHash))
	require.Equal(t, treeFixture().Hash.String(), treeHash)
}

func TestAtomicWrite_Error_CreateTempFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "config")
	err := atomicWrite(path, []byte("x"), 0o644)
	require.Error(t, err)
}

func TestLoadConfig_MissingFile(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	_, err := lr.LoadConfig()
	require.Error(t, err)
}

func TestSaveConfig_Error_CreateTempFails(t *testing.T) {
	lr := NewLocalRepo()
	lr.target = filepath.Join(t.TempDir(), "missing-dir")
	err := lr.SaveConfig(domain.Config{Url: "u", Branch: "b"})
	require.Error(t, err)
}

func TestSaveTree_BinaryFileWithManyChunks(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	rootHash := serverDomain.Hash{0x11}
	fileHash := serverDomain.Hash{0x12}
	chunks := make([]serverDomain.Chunk, 5)
	for i := range chunks {
		chunks[i] = serverDomain.Chunk{Hash: serverDomain.Hash{byte(i + 1)}, SizeBytes: 4}
	}
	root := &serverDomain.TreeNode{
		Hash: rootHash,
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Hash:      fileHash,
			Name:      "bin.dat",
			Mode:      0o644,
			SizeBytes: 20,
			IsBinary:  true,
			Chunks:    chunks,
		}},
	}

	require.NoError(t, lr.SaveTree(root))

	require.Equal(t, 1, countRows(t, lr.db, "tree_nodes"))
	require.Equal(t, 1, countRows(t, lr.db, "files"))
	require.Equal(t, 5, countRows(t, lr.db, "chunks"))
	require.Equal(t, 5, countRows(t, lr.db, "file_chunks"))
}

func TestSaveTree_TreeWithFilesAtRootAndChild(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	rootHash := serverDomain.Hash{0x21}
	dirHash := serverDomain.Hash{0x22}
	rootFileHash := serverDomain.Hash{0x23}
	childFileHash := serverDomain.Hash{0x24}
	chunkHash := serverDomain.Hash{0x25}

	chunk := serverDomain.Chunk{Hash: chunkHash, SizeBytes: 3}
	root := &serverDomain.TreeNode{
		Hash: rootHash,
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Hash: rootFileHash, Name: "root.txt", SizeBytes: 3, Chunks: []serverDomain.Chunk{chunk},
		}},
		TreeChildren: []*serverDomain.TreeNode{{
			Hash: dirHash,
			Name: "sub",
			FileChildren: []*serverDomain.File{{
				Hash: childFileHash, Name: "deep.txt", SizeBytes: 3, Chunks: []serverDomain.Chunk{chunk},
			}},
		}},
	}

	require.NoError(t, lr.SaveTree(root))

	require.Equal(t, 2, countRows(t, lr.db, "tree_nodes"))
	require.Equal(t, 2, countRows(t, lr.db, "files"))
	require.Equal(t, 1, countRows(t, lr.db, "chunks"))
	require.Equal(t, 2, countRows(t, lr.db, "file_chunks"))
}
