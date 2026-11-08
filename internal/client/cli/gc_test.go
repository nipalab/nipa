package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
)

func TestSetupGCCmd_RemovesUnreferencedObjects(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "tracked"})
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	orphan := []byte("orphan bytes")
	require.NoError(t, lr.StoreChunk(chunker.Sum(orphan), orphan))
	require.NoError(t, lr.Close())

	out, err := runCmdInDir(t, root, newStageCli().setupGCCmd())
	require.NoError(t, err)
	require.Equal(t, "removed 1 object(s), freed 12 B\n", out)

	out, err = runCmdInDir(t, root, newStageCli().setupGCCmd())
	require.NoError(t, err)
	require.Equal(t, "removed 0 object(s), freed 0 B\n", out)
}

func TestSetupGCCmd_SkippedDuringMerge(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "tracked"})
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveMergeState(&domain.MergeState{SourceBranch: "feature"}))
	require.NoError(t, lr.Close())

	out, err := runCmdInDir(t, root, newStageCli().setupGCCmd())
	require.NoError(t, err)
	require.Equal(t, "skipped: a merge or revert is in progress\n", out)
}

func TestSetupGCCmd_NotARepo(t *testing.T) {
	_, err := runCmdInDir(t, t.TempDir(), newStageCli().setupGCCmd())
	require.Error(t, err)
}
