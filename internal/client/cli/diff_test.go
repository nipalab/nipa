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
	cli := NewCli(&fakeUsecaseContainer{diff: usecase.NewDiff(nil, nil, localrepo.NewLocalRepo())}, &fakeConnector{})
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
}

func TestDiffCmd_ExitCodeNoChanges(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "same\n"})
	_, err := runCmdInDir(t, root, newDiffCmd(t), "--exit-code")
	require.NoError(t, err)
}

func TestDiffCmd_FlagErrors(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})

	_, err := runCmdInDir(t, root, newDiffCmd(t), "--stat", "--name-only")
	require.ErrorContains(t, err, "only one output format")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "--unified=-1")
	require.ErrorContains(t, err, "--unified must not be negative")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "--ext-diff", "--stat")
	require.ErrorContains(t, err, "--ext-diff cannot be combined")
}

func TestDiffCmd_RevisionArgErrors(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})

	_, err := runCmdInDir(t, root, newDiffCmd(t), "a", "b", "c")
	require.ErrorContains(t, err, "too many revisions")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "a..")
	require.ErrorContains(t, err, "must be <a>..<b>")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "a...")
	require.ErrorContains(t, err, "must be <a>...<b>")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "--staged", "main")
	require.ErrorContains(t, err, "--staged cannot be combined with revisions")

	_, err = runCmdInDir(t, root, newDiffCmd(t), "a", "b", "c", "--", "dir")
	require.ErrorContains(t, err, "too many revisions")
}

func TestDiffCmd_IgnoreWhitespace(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a b\nc\n"})
	writeFile(t, root, "a.txt", "a   b\nc\n")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager")
	require.NoError(t, err)
	require.NotEmpty(t, out)

	for _, flag := range []string{"-w", "-b", "--ignore-all-space", "--ignore-space-change"} {
		out, err = runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager", flag)
		require.NoError(t, err)
		require.Empty(t, out, flag)
	}
}

func TestDiffCmd_RenameDetection(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"old.txt": "same content\n"})
	require.NoError(t, os.Remove(filepath.Join(root, "old.txt")))
	writeFile(t, root, "new.txt", "same content\n")
	stagePath(t, root, "new.txt")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--name-status")
	require.NoError(t, err)
	require.Equal(t, "R100\told.txt\tnew.txt\n", out)
}

func TestDiffCmd_RenamePatch(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"old.txt": "one\ntwo\n"})
	require.NoError(t, os.Remove(filepath.Join(root, "old.txt")))
	writeFile(t, root, "new.txt", "one\ntwo\nthree\n")
	stagePath(t, root, "new.txt")

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "diff --nipa a/old.txt b/new.txt")
	require.Contains(t, out, "rename from old.txt")
	require.Contains(t, out, "rename to new.txt")
	require.Contains(t, out, "+three")
}

func TestDiffCmd_ExternalDiff(t *testing.T) {
	root := setupDiffRepo(t, map[string]string{"a.txt": "a\n"})
	writeFile(t, root, "a.txt", "b\n")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := runCmdInDir(t, root, newDiffCmd(t), "--ext-diff")
	require.ErrorContains(t, err, "no external diff command configured")

	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := filepath.Join(t.TempDir(), "tool.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$ARGS_FILE\"\n"), 0o755))
	t.Setenv("ARGS_FILE", argsFile)
	t.Setenv("NIPA_EXTERNAL_DIFF", script)

	out, err := runCmdInDir(t, root, newDiffCmd(t), "--ext-diff")
	require.NoError(t, err)
	require.Empty(t, out)
	raw, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, "a.txt\n", string(raw))

	require.NoError(t, os.Remove(argsFile))
	out, err = runCmdInDir(t, root, newDiffCmd(t), "--no-color", "--no-pager")
	require.NoError(t, err)
	require.Contains(t, out, "+b")
	_, err = os.Stat(argsFile)
	require.True(t, os.IsNotExist(err), "a configured tool only runs with --ext-diff")
}
