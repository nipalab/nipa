package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func newDiffCli() *Cli {
	return NewCli(&fakeUsecaseContainer{diff: usecase.NewDiff(localrepo.NewLocalRepo())}, &fakeConnector{})
}

func storeContent(t *testing.T, root, content string) {
	t.Helper()
	chunks, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer lr.Close()
	for _, c := range chunks {
		require.NoError(t, lr.StoreChunk(c.Hash, c.Data))
	}
}

func diffTree(files ...*serverDomain.File) *serverDomain.TreeNode {
	return &serverDomain.TreeNode{Hash: serverDomain.Hash{0x01}, Name: "root", FileChildren: files}
}

func diffFile(name, content string) *serverDomain.File {
	return &serverDomain.File{Name: name, Mode: 2, SizeBytes: int64(len(content)), Chunks: chunksOfFor(content)}
}

func chunksOfFor(content string) []serverDomain.Chunk {
	chunks, err := chunker.ChunkAll([]byte(content))
	if err != nil {
		panic(err)
	}
	out := make([]serverDomain.Chunk, len(chunks))
	for i, c := range chunks {
		out[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
	}
	return out
}

func TestSetupDiffCmd_Empty(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(diffFile("a.txt", "hello\n")))
	writeFile(t, root, "a.txt", "hello\n")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd(), "--no-pager")
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestSetupDiffCmd_Modified(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(diffFile("a.txt", "old\n")))
	writeFile(t, root, "a.txt", "new\n")
	storeContent(t, root, "old\n")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd())
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n", out)
}

func TestSetupDiffCmd_StagedNew(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "new.txt", "fresh\n")
	stagePath(t, root, "new.txt")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd())
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/new.txt b/new.txt\nnew file mode 2\n--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1,1 @@\n+fresh\n", out)
}

func TestSetupDiffCmd_UntrackedExcluded(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "noise.txt", "noise\n")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd())
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestSetupDiffCmd_DeletedWithContent(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(diffFile("gone.txt", "bye\n")))
	storeContent(t, root, "bye\n")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd())
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/gone.txt b/gone.txt\ndeleted file mode 2\n--- a/gone.txt\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-bye\n", out)
}

func TestSetupDiffCmd_DeletedUnavailable(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(diffFile("gone.txt", "bye\n")))
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd())
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/gone.txt b/gone.txt\nold content not available locally (run `nipa update` to fetch it)\n", out)
}

func TestSetupDiffCmd_ModeOnly(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(diffFile("run.sh", "echo hi\n")))
	writeFile(t, root, "run.sh", "echo hi\n")
	storeContent(t, root, "echo hi\n")
	require.NoError(t, os.Chmod(filepath.Join(root, "run.sh"), 0o755))
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd())
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/run.sh b/run.sh\nold mode 2\nnew mode 3\n", out)
}

func TestSetupDiffCmd_UnifiedFlag(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(diffFile("f.txt", "l1\nl2\nl3\n")))
	writeFile(t, root, "f.txt", "l1\nl2\nchanged\n")
	storeContent(t, root, "l1\nl2\nl3\n")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd(), "-U", "1")
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -2,2 +2,2 @@\n l2\n-l3\n+changed\n", out)
}

func TestSetupDiffCmd_RevisionsRejected(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newDiffCli()

	_, err := runCmdInDir(t, root, cli.setupDiffCmd(), "main")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported yet")
}

func TestSetupDiffCmd_TooManyArgs(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newDiffCli()

	_, err := runCmdInDir(t, root, cli.setupDiffCmd(), "a", "b", "c")
	require.Error(t, err)
}

func TestSetupDiffCmd_NotARepo(t *testing.T) {
	cli := newDiffCli()

	_, err := runCmdInDir(t, t.TempDir(), cli.setupDiffCmd())
	require.Error(t, err)
	require.Equal(t, "not a nipa repository (or any of the parent directories)", err.Error())
}

func TestDiffFooter(t *testing.T) {
	require.Equal(t, "nipa diff: no changes", diffFooter(2)(newLogViewport(nil, 10)))
	vp := newLogViewport([]string{"l1", "l2", "l3"}, 2)
	require.Equal(t, "nipa diff: 2 files, lines 1-2 of 3 (q to quit)", diffFooter(2)(vp))
}

type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

func TestSetupDiffCmd_WriteError(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(diffFile("a.txt", "old\n")))
	writeFile(t, root, "a.txt", "new\n")
	storeContent(t, root, "old\n")
	cli := newDiffCli()

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWD)) }()
	require.NoError(t, os.Chdir(root))

	cmd := cli.setupDiffCmd()
	cmd.SetOut(errWriter{err: errors.New("write failed")})
	cmd.SetArgs([]string{})
	require.ErrorContains(t, cmd.Execute(), "write failed")
}
