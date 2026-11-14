package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
)

var shelfIDOut = regexp.MustCompile(`\(([0-9a-z]+)\)`)

func shelveCmd(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	return runCmdInDir(t, root, newStageCli().setupShelveCmd(), args...)
}

func TestShelveCmd_RoundTrip(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "original\n", "b.txt": "b\n"})
	writeFile(t, root, "a.txt", "edited\n")
	writeFile(t, root, "b.txt", "b\n")
	writeFile(t, root, "new.txt", "new\n")

	out, err := shelveCmd(t, root, "list")
	require.NoError(t, err)
	require.Equal(t, "no shelves\n", out)

	out, err = shelveCmd(t, root, "-m", "wip", "-u")
	require.NoError(t, err)
	require.Contains(t, out, `shelved 2 files as "wip"`)
	id := shelfIDOut.FindStringSubmatch(out)[1]
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "original\n", string(data))
	require.NoFileExists(t, filepath.Join(root, "new.txt"))

	out, err = shelveCmd(t, root, "list")
	require.NoError(t, err)
	require.Contains(t, out, id)
	require.Contains(t, out, "main")
	require.Contains(t, out, "wip")

	out, err = shelveCmd(t, root, "show", "-p", id)
	require.NoError(t, err)
	require.Contains(t, out, "M  a.txt\n")
	require.Contains(t, out, "A  new.txt (untracked)\n")
	require.Contains(t, out, "+edited")

	out, err = shelveCmd(t, root, "apply", id, "--keep")
	require.NoError(t, err)
	require.Equal(t, "applied shelf \"wip\"\n", out)
	data, err = os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "edited\n", string(data))

	out, err = shelveCmd(t, root, "drop", id)
	require.NoError(t, err)
	require.Contains(t, out, "dropped shelf "+id)

	_, err = shelveCmd(t, root, "a.txt")
	require.NoError(t, err)
	_, err = shelveCmd(t, root)
	require.ErrorContains(t, err, "no changes to shelve")
}

func TestShelveCmd_ApplyConflictContinueAbort(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "base\n"})
	writeFile(t, root, "a.txt", "mine\n")
	out, err := shelveCmd(t, root, "a.txt")
	require.NoError(t, err)
	require.Contains(t, out, "shelved 1 file as \"1 file on main\"")
	id := shelfIDOut.FindStringSubmatch(out)[1]

	_, err = shelveCmd(t, root, "apply")
	require.ErrorContains(t, err, "give a shelf id")
	_, err = shelveCmd(t, root, "apply", "--abort", "--continue")
	require.ErrorContains(t, err, "mutually exclusive")
	_, err = shelveCmd(t, root, "apply", "--abort")
	require.ErrorContains(t, err, "no shelf apply is waiting")

	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveShelfState(&domain.ShelfState{ShelfID: id, ShelfName: "x", Applied: []string{"a.txt"}, Conflicts: []string{"a.txt"}}))
	require.NoError(t, lr.Close())
	writeFile(t, root, "a.txt", "<<<<<<< ours\nbase\n=======\nmine\n>>>>>>> theirs\n")
	_, err = shelveCmd(t, root, "apply", "--continue")
	require.ErrorContains(t, err, "conflict markers")
	out, err = shelveCmd(t, root, "apply", "--abort")
	require.NoError(t, err)
	require.Contains(t, out, "the shelf was kept")
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "base\n", string(data))

	lr = localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveShelfState(&domain.ShelfState{ShelfID: id, ShelfName: "x", Conflicts: []string{"a.txt"}}))
	require.NoError(t, lr.Close())
	out, err = shelveCmd(t, root, "apply", "--continue")
	require.NoError(t, err)
	require.Contains(t, out, "dropped the shelf")

	_, err = shelveCmd(t, root, "show", "nope")
	require.Error(t, err)
	_, err = shelveCmd(t, root, "drop", "nope")
	require.Error(t, err)
	_, err = shelveCmd(t, root, "apply", "nope")
	require.Error(t, err)
}

func TestShelveCmd_OutsideRepo(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{}, {"list"}, {"show", "x"}, {"apply", "x"}, {"drop", "x"}} {
		_, err := shelveCmd(t, dir, args...)
		require.Error(t, err)
	}
}

func TestShelveHelpers(t *testing.T) {
	require.Equal(t, "1 file", fileCount(1))
	require.Equal(t, "tag v1", shelfWhere(domain.Shelf{Head: &domain.HeadRef{Name: "v1"}}))
}

func TestShelveCmd_Resolve(t *testing.T) {
	root := setupRepoWithStoredTree(t, "main", map[string]string{"a.txt": "base\n"})
	writeFile(t, root, "a.txt", "mine\n")
	out, err := shelveCmd(t, root, "a.txt")
	require.NoError(t, err)
	id := shelfIDOut.FindStringSubmatch(out)[1]

	_, err = shelveCmd(t, root, "resolve", "a.txt", "--ours", "--theirs")
	require.ErrorContains(t, err, "mutually exclusive")
	_, err = shelveCmd(t, root, "resolve", "a.txt")
	require.ErrorContains(t, err, "no shelf apply is waiting")

	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveShelfState(&domain.ShelfState{ShelfID: id, ShelfName: "x", Conflicts: []string{"a.txt"}, ConflictKinds: map[string]string{"a.txt": domain.ConflictBinary}, ConflictBinary: []string{"a.txt"}}))
	require.NoError(t, lr.Close())
	_, err = shelveCmd(t, root, "apply", "--continue")
	require.ErrorContains(t, err, "a.txt is still unresolved")

	out, err = shelveCmd(t, root, "resolve", "a.txt", "--theirs")
	require.NoError(t, err)
	require.Equal(t, "resolved a.txt\n", out)
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "mine\n", string(data))

	lr = localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveShelfState(&domain.ShelfState{ShelfID: id, ShelfName: "x", Keep: true, Conflicts: []string{"a.txt"}, ConflictBinary: []string{"a.txt"}}))
	require.NoError(t, lr.Close())
	_, err = shelveCmd(t, root, "resolve", "a.txt", "--ours")
	require.NoError(t, err)
	data, err = os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "base\n", string(data))
}
