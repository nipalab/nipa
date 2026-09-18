package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func newDiffCli() *Cli {
	return newDiffCliWithManifests(nil)
}

func newDiffCliWithManifests(manifests map[string]*serverDomain.TreeNode) *Cli {
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{})
	diff := usecase.NewDiff(auth, stubDiffClient{manifests: manifests}, localrepo.NewLocalRepo())
	return NewCli(&fakeUsecaseContainer{diff: diff}, &fakeConnector{})
}

type stubDiffClient struct {
	fakeRepoInterface
	manifests   map[string]*serverDomain.TreeNode
	manifestErr error
}

func (stubDiffClient) Connect(_ context.Context, _ string) error { return nil }

func (s stubDiffClient) GetTreeNodeManifest(_ context.Context, _, _, branch, _ string) (*serverDomain.TreeNode, error) {
	if s.manifestErr != nil {
		return nil, s.manifestErr
	}
	return s.manifests[branch], nil
}

func (s stubDiffClient) GetTreeNodeManifestByCommit(_ context.Context, _, _ string, _ *snow.ID, _ *serverDomain.Hash) (*serverDomain.TreeNode, error) {
	if s.manifestErr != nil {
		return nil, s.manifestErr
	}
	return s.manifests["commit"], nil
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
	require.Equal(t, "diff --nipa a/gone.txt b/gone.txt\ncontent not available locally (run `nipa update` to fetch it)\n", out)
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

func TestSetupDiffCmd_RevisionBranch(t *testing.T) {
	root := setupRepo(t, "main")
	writeFile(t, root, "a.txt", "new\n")
	storeContent(t, root, "old\n")
	cli := newDiffCliWithManifests(map[string]*serverDomain.TreeNode{
		"main": diffTree(diffFile("a.txt", "old\n")),
	})

	out, err := runCmdInDir(t, root, cli.setupDiffCmd(), "main")
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n", out)
}

func TestSetupDiffCmd_TwoRevisions(t *testing.T) {
	root := setupRepo(t, "main")
	storeContent(t, root, "v1\n")
	storeContent(t, root, "v2\n")
	cli := newDiffCliWithManifests(map[string]*serverDomain.TreeNode{
		"v1": diffTree(diffFile("a.txt", "v1\n")),
		"v2": diffTree(diffFile("a.txt", "v2\n")),
	})

	out, err := runCmdInDir(t, root, cli.setupDiffCmd(), "v1", "v2")
	require.NoError(t, err)
	require.Equal(t, "diff --nipa a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,1 +1,1 @@\n-v1\n+v2\n", out)
}

func TestSetupDiffCmd_RevisionNotFound(t *testing.T) {
	root := setupRepo(t, "main")
	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{}, &fakeInput{})
	diff := usecase.NewDiff(auth, stubDiffClient{manifestErr: &domain.Error{Code: 404, Message: "nope"}}, localrepo.NewLocalRepo())
	cli := NewCli(&fakeUsecaseContainer{diff: diff}, &fakeConnector{})

	_, err := runCmdInDir(t, root, cli.setupDiffCmd(), "nope!!!")
	require.Error(t, err)
	require.Contains(t, err.Error(), `revision "nope!!!" not found`)
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

func TestSetupDiffCmd_Stat(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(
		diffFile("a.txt", "l1\nl2\nl3\n"),
		diffFile("gone.txt", "bye\n"),
	))
	writeFile(t, root, "a.txt", "l1\nCHANGED\nl3\n")
	storeContent(t, root, "l1\nl2\nl3\n")
	storeContent(t, root, "bye\n")
	stagePath(t, root, "new.txt")
	writeFile(t, root, "new.txt", "one\ntwo\n")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd(), "--stat")
	require.NoError(t, err)
	require.Equal(t,
		" a.txt    | 2 +-\n"+
			" gone.txt | 1 -\n"+
			" new.txt  | 2 ++\n"+
			" 3 files changed, 3 insertions(+), 2 deletions(-)\n",
		out)
}

func TestSetupDiffCmd_StatBinary(t *testing.T) {
	tree := diffTree(diffFile("img.png", "PNG-old"))
	tree.FileChildren[0].IsBinary = true
	root := setupRepoWithTree(t, "main", tree)
	writeFile(t, root, "img.png", "PNG-new!")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd(), "--stat")
	require.NoError(t, err)
	require.Equal(t,
		" img.png | Bin 7 -> 8 bytes\n"+
			" 1 file changed, 0 insertions(+), 0 deletions(-)\n",
		out)
}

func TestSetupDiffCmd_StatModeOnly(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(diffFile("run.sh", "echo hi\n")))
	writeFile(t, root, "run.sh", "echo hi\n")
	storeContent(t, root, "echo hi\n")
	require.NoError(t, os.Chmod(filepath.Join(root, "run.sh"), 0o755))
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd(), "--stat")
	require.NoError(t, err)
	require.Equal(t,
		" run.sh | 0\n"+
			" 1 file changed, 0 insertions(+), 0 deletions(-)\n",
		out)
}

func TestSetupDiffCmd_NameOnly(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(
		diffFile("b.txt", "x\n"),
		diffFile("a.txt", "y\n"),
	))
	writeFile(t, root, "a.txt", "changed\n")
	storeContent(t, root, "y\n")
	storeContent(t, root, "x\n")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd(), "--name-only")
	require.NoError(t, err)
	require.Equal(t, "a.txt\nb.txt\n", out)
}

func TestSetupDiffCmd_NameStatus(t *testing.T) {
	root := setupRepoWithTree(t, "main", diffTree(
		diffFile("b.txt", "x\n"),
		diffFile("a.txt", "y\n"),
	))
	writeFile(t, root, "a.txt", "changed\n")
	storeContent(t, root, "y\n")
	storeContent(t, root, "x\n")
	stagePath(t, root, "new.txt")
	writeFile(t, root, "new.txt", "fresh\n")
	cli := newDiffCli()

	out, err := runCmdInDir(t, root, cli.setupDiffCmd(), "--name-status")
	require.NoError(t, err)
	require.Equal(t, "M\ta.txt\nD\tb.txt\nA\tnew.txt\n", out)
}

func TestSetupDiffCmd_OutputModeConflict(t *testing.T) {
	root := setupRepo(t, "main")
	cli := newDiffCli()

	_, err := runCmdInDir(t, root, cli.setupDiffCmd(), "--stat", "--name-only")
	require.ErrorContains(t, err, "only one of --stat, --name-only, --name-status may be given")

	_, err = runCmdInDir(t, root, cli.setupDiffCmd(), "--stat", "--name-status")
	require.ErrorContains(t, err, "only one of --stat, --name-only, --name-status may be given")
}

func TestColorizeDiffLine(t *testing.T) {
	require.Equal(t, "\x1b[32m+added\x1b[m", colorizeDiffLine("+added", false))
	require.Equal(t, "\x1b[31m-removed\x1b[m", colorizeDiffLine("-removed", false))
	require.Equal(t, "\x1b[36m@@ -1 +1 @@\x1b[m", colorizeDiffLine("@@ -1 +1 @@", false))
	require.Equal(t, "\x1b[1mdiff --nipa a/f b/f\x1b[m", colorizeDiffLine("diff --nipa a/f b/f", false))
	require.Equal(t, "\x1b[1m--- a/f\x1b[m", colorizeDiffLine("--- a/f", false))
	require.Equal(t, "\x1b[1m+++ b/f\x1b[m", colorizeDiffLine("+++ b/f", false))
	require.Equal(t, " context", colorizeDiffLine(" context", false))
	require.Equal(t, `\ No newline at end of file`, colorizeDiffLine(`\ No newline at end of file`, false))
}

func TestColorizeStatLine(t *testing.T) {
	require.Equal(t, " f | 3 \x1b[32m++\x1b[m\x1b[31m-\x1b[m", colorizeDiffLine(" f | 3 ++-", true))
	require.Equal(t, " f | 2 \x1b[32m++\x1b[m", colorizeDiffLine(" f | 2 ++", true))
	require.Equal(t, " f | 1 \x1b[31m-\x1b[m", colorizeDiffLine(" f | 1 -", true))
	require.Equal(t, " f | 0", colorizeDiffLine(" f | 0", true))
	require.Equal(t, " f | Bin 1 -> 2 bytes", colorizeDiffLine(" f | Bin 1 -> 2 bytes", true))
	require.Equal(t, "no separator", colorizeDiffLine("no separator", true))
	require.Equal(t, " f | 3 ", colorizeDiffLine(" f | 3 ", true))
}

func TestUseColor(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	defer f.Close()
	require.False(t, useColor(f, false), "regular files are not terminals")
	require.False(t, useColor(f, true))

	t.Setenv("NO_COLOR", "1")
	require.False(t, useColor(f, false))
}

func TestPlural(t *testing.T) {
	require.Equal(t, "file changed", plural(1, "file changed", "files changed"))
	require.Equal(t, "files changed", plural(2, "file changed", "files changed"))
	require.Equal(t, "insertions(+)", plural(0, "insertion(+)", "insertions(+)"))
}
