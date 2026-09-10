package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func runCmdInDir(t *testing.T, dir string, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	require.NoError(t, os.Chdir(dir))

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return buf.String(), err
}

func writeFile(t *testing.T, root, path, content string) {
	t.Helper()
	fp := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
	require.NoError(t, os.WriteFile(fp, []byte(content), 0o644))
}

func setupRepoWithTree(t *testing.T, branch string, tree *serverDomain.TreeNode) string {
	t.Helper()
	target := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	defer lr.Close()
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: branch}))
	require.NoError(t, lr.SaveTree(tree))
	return target
}

func stagedPathsAt(t *testing.T, root string) []string {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer lr.Close()
	paths, err := lr.ListStaged()
	require.NoError(t, err)
	return paths
}

func stagePath(t *testing.T, root, path string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer lr.Close()
	require.NoError(t, lr.StageAdd(path))
}

func chunksOf(t *testing.T, content string) []serverDomain.Chunk {
	t.Helper()
	chunks, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	out := make([]serverDomain.Chunk, len(chunks))
	for i, c := range chunks {
		out[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
	}
	return out
}
