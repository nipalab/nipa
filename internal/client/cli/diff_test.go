package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func newDiffCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cli := NewCli(&fakeUsecaseContainer{diff: usecase.NewDiff(localrepo.NewLocalRepo())}, &fakeConnector{})
	return cli.setupDiffCmd()
}

func setupDiffRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	target := t.TempDir()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(target))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var children []*serverDomain.File
	var pending []*serverDomain.ChunkData
	for _, name := range names {
		content := []byte(files[name])
		chunks, err := chunker.ChunkAll(content)
		require.NoError(t, err)
		hashes := make([]serverDomain.Hash, len(chunks))
		fileChunks := make([]serverDomain.Chunk, len(chunks))
		for i, c := range chunks {
			hashes[i] = c.Hash
			fileChunks[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
			pending = append(pending, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
		}
		children = append(children, &serverDomain.File{
			Name:      name,
			Mode:      2,
			SizeBytes: int64(len(content)),
			IsBinary:  chunker.IsBinary(content),
			Hash:      chunker.FileHash(hashes),
			Chunks:    fileChunks,
		})
	}
	require.NoError(t, lr.StoreChunks(pending))
	require.NoError(t, lr.SaveTree(&serverDomain.TreeNode{
		Name:         "",
		Hash:         chunker.Sum([]byte("snapshot")),
		FileChildren: children,
	}))
	require.NoError(t, lr.Close())

	for name, content := range files {
		writeFile(t, target, name, content)
	}
	return target
}

func TestDiffCmd_ModifiedPatch(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	writeFile(t, root, "a.txt", "one\nTWO\nthree\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager")
	require.NoError(t, err)
	require.Equal(t, strings.Join([]string{
		"diff --nipa a/a.txt b/a.txt",
		"--- a/a.txt",
		"+++ b/a.txt",
		"@@ -1,3 +1,3 @@",
		" one",
		"-two",
		"+TWO",
		" three",
		"",
	}, "\n"), out)
}

func TestDiffCmd_NoChanges(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "same\n"})

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager")
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestDiffCmd_AddedStaged(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "new.txt", "hello\n")
	stagePath(t, root, "new.txt")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "diff --nipa a/new.txt b/new.txt")
	require.Contains(t, out, "new file mode 100644")
	require.Contains(t, out, "+hello")
}

func TestDiffCmd_UntrackedIgnored(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "loose.txt", "hello\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager")
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestDiffCmd_Deleted(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"gone.txt": "bye\n"})
	require.NoError(t, os.Remove(filepath.Join(root, "gone.txt")))

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "deleted file mode 100644")
	require.Contains(t, out, "-bye")
	require.Contains(t, out, "+++ /dev/null")
}

func TestDiffCmd_Binary(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"img.bin": "old\x00data"})
	writeFile(t, root, "img.bin", "new\x00data")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "Binary files a/img.bin and b/img.bin differ")
}

func TestDiffCmd_Stat(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "one\ntwo\n"})
	writeFile(t, root, "a.txt", "one\nTWO\nthree\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--stat")
	require.NoError(t, err)
	require.Equal(t, strings.Join([]string{
		" a.txt | 3 ++-",
		" 1 file changed, 2 insertions(+), 1 deletion(-)",
		"",
	}, "\n"), out)
}

func TestDiffCmd_NumStat(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "one\ntwo\n"})
	writeFile(t, root, "a.txt", "one\nTWO\nthree\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--numstat")
	require.NoError(t, err)
	require.Equal(t, "2\t1\ta.txt\n", out)
}

func TestDiffCmd_ShortStat(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "one\ntwo\n"})
	writeFile(t, root, "a.txt", "one\nTWO\nthree\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--shortstat")
	require.NoError(t, err)
	require.Equal(t, " 1 file changed, 2 insertions(+), 1 deletion(-)\n", out)
}

func TestDiffCmd_NameOnlyAndStatus(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	writeFile(t, root, "a.txt", "A\n")
	writeFile(t, root, "new.txt", "n\n")
	stagePath(t, root, "new.txt")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--name-only")
	require.NoError(t, err)
	require.Equal(t, "a.txt\nnew.txt\n", out)

	out, err = runCmdInDir(t, root, newDiffCmd(t), "--name-status")
	require.NoError(t, err)
	require.Equal(t, "M\ta.txt\nA\tnew.txt\n", out)
}

func TestDiffCmd_Raw(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "a.txt", "b\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--raw")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(out, ":100644 100644 "))
	require.True(t, strings.HasSuffix(out, " M\ta.txt\n"))
}

func TestDiffCmd_PathFilter(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n", "dir/b.txt": "b\n"})
	writeFile(t, root, "a.txt", "A\n")
	writeFile(t, root, "dir/b.txt", "B\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--name-only", "--", "dir")
	require.NoError(t, err)
	require.Equal(t, "dir/b.txt\n", out)
}

func TestDiffCmd_Staged(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	writeFile(t, root, "a.txt", "A\n")
	writeFile(t, root, "b.txt", "B\n")
	stagePath(t, root, "a.txt")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--name-only", "--staged")
	require.NoError(t, err)
	require.Equal(t, "a.txt\n", out)

	out, err = runCmdInDir(t, root, newDiffCmd(t), "--name-only", "--cached")
	require.NoError(t, err)
	require.Equal(t, "a.txt\n", out)
}

func TestDiffCmd_DiffFilter(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "a.txt", "A\n")
	writeFile(t, root, "new.txt", "n\n")
	stagePath(t, root, "new.txt")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--name-status", "--diff-filter=A")
	require.NoError(t, err)
	require.Equal(t, "A\tnew.txt\n", out)
}

func TestDiffCmd_Reverse(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "a.txt", "b\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--name-status", "--reverse")
	require.NoError(t, err)
	require.Equal(t, "M\ta.txt\n", out)

	out, err = runCmdInDir(t, root, newDiffCmd(t), "--reverse", "-U0")
	require.NoError(t, err)
	require.Contains(t, out, "-b")
	require.Contains(t, out, "+a")
}

func TestDiffCmd_UnifiedZero(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	writeFile(t, root, "a.txt", "one\nTWO\nthree\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "-U0")
	require.NoError(t, err)
	require.Contains(t, out, "@@ -2,1 +2,1 @@")
	require.NotContains(t, out, " one")
}

func TestDiffCmd_ExitCode(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "a.txt", "b\n")

	_, err := runCmdInDir(t, root, newDiffCmd(t), "--exit-code")
	require.ErrorIs(t, err, ErrExitCode)

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--exit-code", "--name-only")
	require.ErrorIs(t, err, ErrExitCode)
	require.Equal(t, "a.txt\n", out)

	_, err = runCmdInDir(t, root, newDiffCmd(t), "--quiet")
	require.ErrorIs(t, err, ErrExitCode)
}

func TestDiffCmd_ExitCodeNoChanges(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "same\n"})
	_, err := runCmdInDir(t, root, newDiffCmd(t), "--quiet")
	require.NoError(t, err)
}

func TestDiffCmd_OutputFile(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "a.txt", "b\n")

	_, err := runCmdInDir(t, root, newDiffCmd(t), "--output", "patch.diff")
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(root, "patch.diff"))
	require.NoError(t, err)
	require.Contains(t, string(content), "diff --nipa a/a.txt b/a.txt")
	require.Contains(t, string(content), "+b")
}

func TestDiffCmd_FlagErrors(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})

	_, err := runCmdInDir(t, root, newDiffCmd(t), "--stat", "--name-only")
	require.ErrorContains(t, err, "only one output format")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "--patch", "--stat")
	require.ErrorContains(t, err, "--patch cannot be combined")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "--color", "sometimes")
	require.ErrorContains(t, err, "--color must be")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "--diff-filter", "X")
	require.ErrorContains(t, err, "unsupported --diff-filter")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "--unified=-1")
	require.ErrorContains(t, err, "--unified must not be negative")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "--output-indicator-new", "ab")
	require.ErrorContains(t, err, "single character")
}

func TestDiffCmd_PrefixIndicators(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "a.txt", "b\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t),
		"--no-prefix", "--output-indicator-old", "<", "--output-indicator-new", ">")
	require.NoError(t, err)
	require.Contains(t, out, "diff --nipa a.txt a.txt")
	require.Contains(t, out, "--- a.txt")
	require.Contains(t, out, "+++ a.txt")
	require.Contains(t, out, "<a")
	require.Contains(t, out, ">b")
}
