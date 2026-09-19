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

func TestSaveTree_KeepsCachedObjects(t *testing.T) {
	target := t.TempDir()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()

	firstHash := serverDomain.Hash{0x70}
	secondHash := serverDomain.Hash{0x71}
	first := []byte("first object")
	second := []byte("second object")
	require.NoError(t, lr.StoreChunks([]*serverDomain.ChunkData{
		{Hash: firstHash, Data: first},
		{Hash: secondHash, Data: second},
	}))

	require.NoError(t, lr.SaveTree(treeFixture()))

	reSave := treeFixture()
	reSave.Name = "root-v2"
	require.NoError(t, lr.SaveTree(reSave))

	require.NoError(t, lr.SaveTree(nil), "clearing the snapshot must not erase cached objects")

	for hash, data := range map[serverDomain.Hash][]byte{firstHash: first, secondHash: second} {
		got, err := lr.LoadChunk(hash)
		require.NoError(t, err)
		require.Equal(t, data, got)
	}
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

func TestSaveCommit_LoadCommit(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	require.NoError(t, lr.SaveCommit("zzz123", "beefcafe"))
	got, err := lr.LoadCommit()
	require.NoError(t, err)
	require.Equal(t, "zzz123", got.CommitID)
	require.Equal(t, "beefcafe", got.CommitHash)
}

func TestSaveCommit_UpdatesExisting(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	require.NoError(t, lr.SaveCommit("old", "oldhash"))
	require.NoError(t, lr.SaveCommit("new", "newhash"))
	got, err := lr.LoadCommit()
	require.NoError(t, err)
	require.Equal(t, "new", got.CommitID)
	require.Equal(t, "newhash", got.CommitHash)
}

func TestLoadCommit_NeverPushed(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	got, err := lr.LoadCommit()
	require.NoError(t, err)
	require.Equal(t, &domain.LocalCommit{}, got, "a clone that has never been pushed reports no pinning commit")
}

func TestSaveCommit_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	require.Error(t, lr.SaveCommit("zzz123", "beefcafe"))
	_, err := lr.LoadCommit()
	require.Error(t, err)
}

func TestMergeState_RoundTrip(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	state := &domain.MergeState{
		SourceBranch:     "feature",
		SourceCommitID:   "f1",
		SourceCommitHash: "src-hash",
		BaseCommitID:     "b1",
		BaseTreeHash:     "base-hash",
		Conflicts:        []string{"a.txt", "b.txt"},
	}
	require.NoError(t, lr.SaveMergeState(state))

	got, err := lr.LoadMergeState()
	require.NoError(t, err)
	require.Equal(t, state, got)
}

func TestMergeState_NoConflicts(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	state := &domain.MergeState{
		SourceBranch:     "feature",
		SourceCommitID:   "f1",
		SourceCommitHash: "src-hash",
		BaseCommitID:     "b1",
		BaseTreeHash:     "base-hash",
	}
	require.NoError(t, lr.SaveMergeState(state))

	got, err := lr.LoadMergeState()
	require.NoError(t, err)
	require.Empty(t, got.Conflicts)
}

func TestMergeState_OverwritesPrevious(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	require.NoError(t, lr.SaveMergeState(&domain.MergeState{SourceBranch: "old"}))
	require.NoError(t, lr.SaveMergeState(&domain.MergeState{SourceBranch: "feature", SourceCommitHash: "src-hash"}))

	got, err := lr.LoadMergeState()
	require.NoError(t, err)
	require.Equal(t, "feature", got.SourceBranch)
	require.Equal(t, "src-hash", got.SourceCommitHash)
}

func TestMergeState_LoadEmpty(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	got, err := lr.LoadMergeState()
	require.NoError(t, err)
	require.Nil(t, got, "no pending merge when nothing was saved")
}

func TestMergeState_Clear(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	require.NoError(t, lr.SaveMergeState(&domain.MergeState{SourceBranch: "feature"}))
	require.NoError(t, lr.ClearMergeState())

	got, err := lr.LoadMergeState()
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestMergeState_ClearWhenEmpty(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	defer lr.Close()

	require.NoError(t, lr.ClearMergeState())
	got, err := lr.LoadMergeState()
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestMergeState_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	require.Error(t, lr.SaveMergeState(&domain.MergeState{SourceBranch: "feature"}))
	_, err := lr.LoadMergeState()
	require.Error(t, err)
	require.Error(t, lr.ClearMergeState())
}
