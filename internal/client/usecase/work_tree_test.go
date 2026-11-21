package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// linkStyles are the two ways a swapped-in link can leave the clone: by an
// absolute target, or by a relative one climbing out of the root.
var linkStyles = []string{"absolute", "relative"}

// swapFolder replaces the folder rel of root, which the write guard already
// verified, with a link to outside, as a racing local process could. The
// folder's files are moved to outside first, so whatever the operation would
// write or delete there has a twin behind the link.
func swapFolder(t *testing.T, root, rel, outside, style string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.NoError(t, os.Rename(filepath.Join(dir, e.Name()), filepath.Join(outside, e.Name())))
	}
	require.NoError(t, os.Remove(dir))
	target := outside
	if style == "relative" {
		target, err = filepath.Rel(filepath.Dir(dir), outside)
		require.NoError(t, err)
	}
	symlinkOrSkip(t, target, dir)
}

func requireEscapeRefusal(t *testing.T, err error, verb string) {
	t.Helper()
	requireCode(t, err, 400)
	require.ErrorContains(t, err, "refusing to "+verb)
	require.ErrorContains(t, err, "is a link that leads out of the working copy")
}

// requireUntouched asserts outside holds exactly names with their content
// unchanged ("keep"): nothing was written, created or removed there.
func requireUntouched(t *testing.T, outside string, names ...string) {
	t.Helper()
	require.ElementsMatch(t, names, listing(t, outside))
	for _, n := range names {
		require.Equal(t, "keep", string(readRepoFile(t, outside, n)))
	}
}

// verifiedThenSwapped is a working copy whose art/ folder holds hero.png; a
// guard scope verified art/ and then art/ was swapped for a link. The scope
// stays open until the test ends, as it would for the rest of an operation.
func verifiedThenSwapped(t *testing.T, style string) (root, outside string) {
	t.Helper()
	root = t.TempDir()
	outside = t.TempDir()
	writeRepoFile(t, root, "art/hero.png", "keep")
	t.Cleanup(guardWrites(root))
	require.NoError(t, checkTargets(root, "write", []string{"art/hero.png", "art/new.bin"}))
	require.NoError(t, checkTargets(root, "remove", []string{"art/hero.png"}))
	swapFolder(t, root, "art", outside, style)
	return root, outside
}

func TestWorkTree_RefusesAFolderSwappedAfterTheGuard(t *testing.T) {
	data := []byte("payload")
	open := func(serverDomain.Hash) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
	file := materializedFile{Path: "art/new.bin", Mode: 2, SizeBytes: int64(len(data)), ChunkHashes: []serverDomain.Hash{chunker.Sum(data)}, ChunkSizes: []int64{int64(len(data))}}
	keepHash := chunker.FileHash([]serverDomain.Hash{chunker.Sum([]byte("keep"))})
	for _, style := range linkStyles {
		for name, run := range map[string]func(root string) (string, error){
			"materialize": func(root string) (string, error) {
				return "write", materializeFile(root, file, 0, open)
			},
			"direct": func(root string) (string, error) {
				plan, err := newDirectPlan(root, []materializeJob{{file: file}})
				require.Nil(t, plan)
				return "write", err
			},
			"overwrite": func(root string) (string, error) {
				return "write", materializeFile(root, materializedFile{Path: "art/hero.png", Mode: 2, SizeBytes: int64(len(data)), ChunkHashes: file.ChunkHashes}, 0, open)
			},
			"remove left file": func(root string) (string, error) {
				_, err := guardedRemoveCached(root, domain.SnapshotFile{Path: "art/hero.png", Hash: keepHash}, nil)
				return "remove", err
			},
			"remove left file by fingerprint": func(root string) (string, error) {
				stat := map[string]domain.StatEntry{"art/hero.png": {Hash: keepHash}}
				_, err := guardedRemoveCached(root, domain.SnapshotFile{Path: "art/hero.png", Hash: keepHash}, stat)
				return "remove", err
			},
			"remove shelved file": func(root string) (string, error) {
				return "remove", removeWorkingFile(root, "art/hero.png")
			},
			"resolve write": func(root string) (string, error) {
				c := newConflicts(t, &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}, root)
				return "write", c.Resolve(context.Background(), "art/hero.png", Resolution{Content: strings.NewReader("theirs")})
			},
			"resolve delete": func(root string) (string, error) {
				c := newConflicts(t, &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}, root)
				return "remove", c.Resolve(context.Background(), "art/hero.png", Resolution{Delete: true})
			},
		} {
			t.Run(style+"/"+name, func(t *testing.T) {
				root, outside := verifiedThenSwapped(t, style)
				verb, err := run(root)
				requireEscapeRefusal(t, err, verb)
				requireUntouched(t, outside, "hero.png")
			})
		}
	}
}

// swapRepo swaps a folder of the working copy for a link the first time the
// operation looks up missing chunks: after its planning check verified the
// folder, before it writes or removes anything there.
type swapRepo struct {
	*localrepo.LocalRepo
	once sync.Once
	swap func()
}

func (r *swapRepo) MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error) {
	r.once.Do(r.swap)
	return r.LocalRepo.MissingChunks(hashes)
}

func TestSync_RefusesAFolderSwappedMidOperation(t *testing.T) {
	for _, style := range linkStyles {
		for name, next := range map[string]func(t *testing.T, data map[serverDomain.Hash][]byte, old *serverDomain.File) (*serverDomain.TreeNode, string){
			"direct write": func(_ *testing.T, data map[serverDomain.Hash][]byte, old *serverDomain.File) (*serverDomain.TreeNode, string) {
				return &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{Name: "assets",
					FileChildren: []*serverDomain.File{old, rawManifestFile("new.bin", data, "NEW", "DATA")},
				}}}, "write"
			},
			"cached write": func(t *testing.T, data map[serverDomain.Hash][]byte, old *serverDomain.File) (*serverDomain.TreeNode, string) {
				return &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{Name: "assets",
					FileChildren: []*serverDomain.File{old, textManifestFile(t, "new.txt", "text\n", data)},
				}}}, "write"
			},
			"removal": func(_ *testing.T, data map[serverDomain.Hash][]byte, _ *serverDomain.File) (*serverDomain.TreeNode, string) {
				return &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{rawManifestFile("top.bin", data, "TOP")}}, "remove"
			},
		} {
			t.Run(style+"/"+name, func(t *testing.T) {
				lr, root := newDirectRepo(t)
				data := map[serverDomain.Hash][]byte{}
				old := rawManifestFile("old.bin", data, "keep")
				first := &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{Name: "assets", FileChildren: []*serverDomain.File{old}}}}
				require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, first))
				tree, verb := next(t, data, old)

				outside := t.TempDir()
				repo := &swapRepo{LocalRepo: lr, swap: func() { swapFolder(t, root, "assets", outside, style) }}
				err := syncWorkingCopy(context.Background(), &recordingDownloader{data: data}, repo, root, tree, domain.ChunkScope{})
				requireEscapeRefusal(t, err, verb)
				requireUntouched(t, outside, "old.bin")
				requireNoTempFiles(t, root)
			})
		}
	}
}

func TestRestore_RefusesAFolderSwappedMidOperation(t *testing.T) {
	for _, style := range linkStyles {
		for _, target := range []string{"assets/big.bin", "assets/notes.txt"} {
			t.Run(style+"/"+target, func(t *testing.T) {
				d := linkedRestore(t)
				writeRepoFile(t, d.root, "assets/big.bin", "keep")
				writeRepoFile(t, d.root, "assets/notes.txt", "keep")
				outside := t.TempDir()
				repo := &swapRepo{LocalRepo: d.lr, swap: func() { swapFolder(t, d.root, "assets", outside, style) }}
				wc, err := NewWorkingCopy(repo, d.root)
				require.NoError(t, err)
				wc.WithDownloader(nil, d.server)

				err = wc.Restore(context.Background(), []string{target})
				requireEscapeRefusal(t, err, "write")
				requireUntouched(t, outside, "big.bin", "notes.txt")
				requireNoTempFiles(t, d.root)
			})
		}
	}
}

func TestWorkTree_OpensOncePerScopeAndClosesWithTheLastHolder(t *testing.T) {
	root := t.TempDir()
	end := guardWrites(root)
	wt := acquireWorkTree(root)
	require.Same(t, wt, acquireWorkTree(root), "writes in a scope share its work tree")
	r, err := wt.open(false)
	require.NoError(t, err)
	again, err := wt.open(false)
	require.NoError(t, err)
	require.Same(t, r, again, "the root is opened once")
	wt.release()
	end()
	_, done, err := wt.dirOf("write", filepath.Join("still", "open", "f"), true)
	require.NoError(t, err, "a holder keeps the root open after the scope ends")
	done()
	wt.release()

	_, err = wt.open(false)
	require.ErrorIs(t, err, os.ErrClosed)
	_, _, err = wt.dirOf("write", filepath.Join("x", "f"), true)
	require.ErrorIs(t, err, os.ErrClosed)
	_, err = wt.openFile("x", os.O_RDONLY, 0)
	require.ErrorIs(t, err, os.ErrClosed)
	require.ErrorIs(t, wt.chmod("x", 0o644), os.ErrClosed)
	require.ErrorIs(t, wt.remove("x"), os.ErrClosed)
	require.ErrorIs(t, wt.replace("x", "y"), os.ErrClosed)
	require.False(t, wt.isReadOnly("x"))
	require.NotSame(t, wt, acquireWorkTree(root), "a new operation gets a new work tree")
}

func TestWorkTree_CreatesMissingFoldersOnlyToWrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new", "clone")
	wt := testWorkTree(t, root)
	_, _, err := wt.dirOf("remove", filepath.Join("a", "f"), false)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, done, err := wt.dirOf("write", "f", true)
	require.NoError(t, err)
	done()
	require.DirExists(t, root)
	_, _, err = wt.dirOf("remove", filepath.Join("a", "b", "f"), false)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoDirExists(t, filepath.Join(root, "a"))
	_, done, err = wt.dirOf("write", filepath.Join("a", "b", "f"), true)
	require.NoError(t, err)
	done()
	require.DirExists(t, filepath.Join(root, "a", "b"))

	writeRepoFile(t, root, "file", "x")
	_, _, err = wt.dirOf("write", filepath.Join("file", "sub", "f"), true)
	require.Error(t, err)
	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, nil, 0o644))
	_, _, err = testWorkTree(t, filepath.Join(blocked, "clone")).dirOf("write", filepath.Join("a", "f"), true)
	require.Error(t, err)
}

func TestCreateTempIn_NamesMatchTheSweep(t *testing.T) {
	root := t.TempDir()
	r, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	f, name, err := createTempIn(r, materializeTempPrefix)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.True(t, isMaterializeTemp(name))
	info, err := os.Stat(filepath.Join(root, name))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Zero(t, info.Mode().Perm()&0o077, "a temp file is private until its final mode is set")
	}
	require.NoError(t, r.Close())
	_, _, err = createTempIn(r, materializeTempPrefix)
	require.Error(t, err)
}

func TestRefuseEscape_KeepsOtherErrors(t *testing.T) {
	require.NoError(t, refuseEscape("write", "x", nil))
	plain := errors.New("disk full")
	require.Same(t, plain, refuseEscape("write", "x", plain))
	require.False(t, isRootEscape(&os.PathError{Op: "open", Path: "x", Err: os.ErrNotExist}))

	root := t.TempDir()
	r, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	_, err = r.Open("../x")
	require.True(t, isRootEscape(err), "os.Root still reports an escape as %q: %v", rootEscapeMessage, err)
	requireEscapeRefusal(t, refuseEscape("remove", filepath.Join("a", "b"), err), "remove")
	require.ErrorContains(t, refuseEscape("remove", filepath.Join("a", "b"), err), `"a/b"`)
}
