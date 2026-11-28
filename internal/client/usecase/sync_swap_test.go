package usecase

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
)

// newSwapClone is newDeltaClone over the given files.
func newSwapClone(t *testing.T, server *deltaServer, files map[string]string, sparse ...string) (*deltaClone, string) {
	t.Helper()
	base := server.commit(files)
	server.head("main", base)
	c := &deltaClone{t: t, root: t.TempDir(), lr: localrepo.NewLocalRepo(), server: server}
	t.Cleanup(func() { _ = c.lr.Close() })
	require.NoError(t, c.lr.Init(c.root))
	require.NoError(t, c.lr.SaveConfig(domain.Config{Url: deltaURL, Branch: "main", Sparse: sparse}))
	require.NoError(t, c.update(false).Run(context.Background(), c.root))
	server.calls = nil
	return c, base
}

// folders lists every folder of the working copy (.nipa excluded).
func (c *deltaClone) folders() []string {
	c.t.Helper()
	var out []string
	require.NoError(c.t, filepath.WalkDir(c.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(c.root, p)
		if !d.IsDir() || rel == "." {
			return nil
		}
		if rel == localrepo.ConfigDir {
			return filepath.SkipDir
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	}))
	sort.Strings(out)
	return out
}

// requireBase checks a refused sync left the clone at base: the stored tree,
// record and commit, and the working copy as it was.
func (c *deltaClone) requireBase(base string, working map[string]string) {
	c.t.Helper()
	require.Equal(c.t, working, c.working(), "nothing is changed in the working copy")
	synced, err := c.lr.LoadSyncedTree()
	require.NoError(c.t, err)
	require.NotNil(c.t, synced)
	require.Equal(c.t, base, synced.CommitID, "the stored tree is still the base")
	local, err := c.lr.LoadCommit()
	require.NoError(c.t, err)
	require.Equal(c.t, base, local.CommitID)
}

// foldersOf lists the folders a fresh checkout of files has.
func foldersOf(files map[string]string) []string {
	seen := map[string]bool{}
	for p := range files {
		for d := parentDir(p); d != ""; d = parentDir(d) {
			seen[d] = true
		}
	}
	var out []string
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

var swapCases = []struct {
	name     string
	from, to map[string]string
}{
	{"file to folder", map[string]string{"a/b": "b\n", "keep.txt": "k\n"}, map[string]string{"a/b/c": "c\n", "keep.txt": "k\n"}},
	{"binary file to folder of binaries", map[string]string{"a/b": "bin\x00b"}, map[string]string{"a/b/c": "bin\x00c", "a/b/d/e": "bin\x00e"}},
	{"file to folder several levels deep", map[string]string{"p": "p\n", "q/r": "r\n"}, map[string]string{"p/q/r/s.txt": "bin\x00s", "q/r": "r\n"}},
	{"folder tree to file", map[string]string{"d/e/f/g.txt": "g\n", "d/e/h.txt": "bin\x00h", "d/i.txt": "i\n", "z.txt": "z\n"}, map[string]string{"d": "d\n", "z.txt": "z\n"}},
	{"folder to binary file", map[string]string{"a/x/y": "bin\x00y", "a/x/z/w": "w\n", "a/k": "k\n"}, map[string]string{"a/x": "bin\x00x", "a/k": "k\n"}},
	{"chain in one move", map[string]string{"a/b": "b\n", "a/x/y": "y\n", "a/x/z": "bin\x00z"}, map[string]string{"a/b/c": "c\n", "a/x": "x\n"}},
}

// move brings c from its commit to target the way kind names: an update of
// the branch it is on, a switch, a tag switch or a detached update; with full
// the synced-tree record is dropped first, so the move takes the full sync.
func (c *deltaClone) move(kind string, force, full bool, target string) error {
	ctx := context.Background()
	u := c.update(force)
	clear := func() {
		if full {
			require.NoError(c.t, c.lr.ClearSyncedTree())
		}
	}
	clear()
	switch kind {
	case "update":
		c.server.head("main", target)
		return u.Run(ctx, c.root)
	case "switch":
		branch := "b" + target
		c.server.head(branch, target)
		return u.Switch(ctx, c.root, branch)
	case "tag":
		tag := "t" + target
		c.server.tags[tag] = target
		return u.SwitchTag(ctx, c.root, tag)
	case "detached":
		// Detach on the current commit first, then point the pin at the
		// target, as an interrupted tag switch leaves it.
		local, err := c.lr.LoadCommit()
		require.NoError(c.t, err)
		c.server.tags["here"] = local.CommitID
		require.NoError(c.t, u.SwitchTag(ctx, c.root, "here"))
		require.NoError(c.t, c.lr.SaveCommit(target, ""))
		clear()
		return u.Run(ctx, c.root)
	}
	c.t.Fatalf("unknown move %q", kind)
	return nil
}

// Every way to move a clone across a file that becomes a folder or a folder
// that becomes a file, both ways, by the delta and by the full sync, forced
// or not, ends where a fresh checkout of the target is: same files, rows and
// record, and no folder a fresh checkout lacks.
func TestSync_FileFolderSwaps(t *testing.T) {
	for _, tc := range swapCases {
		for _, back := range []bool{false, true} {
			from, to := tc.from, tc.to
			dir := "forth"
			if back {
				from, to, dir = to, from, "back"
			}
			for _, kind := range []string{"update", "switch", "tag", "detached"} {
				for _, sync := range []string{"delta", "full"} {
					for _, force := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/%s/%s/force=%v", tc.name, dir, kind, sync, force), func(t *testing.T) {
							recorder := recordSpans(t)
							server := newDeltaServer(t)
							c, _ := newSwapClone(t, server, from)
							require.Equal(t, foldersOf(from), c.folders())
							target := server.commit(to)
							require.NoError(t, c.move(kind, force, sync == "full", target))
							if sync == "full" {
								require.Equal(t, attrsFull("record"), syncTreeAttrs(t, recorder))
							} else {
								require.Equal(t, attrsDelta, syncTreeAttrs(t, recorder))
							}
							c.requireAt(target)
							require.Equal(t, foldersOf(to), c.folders())
						})
					}
				}
			}
		}
	}
}

// Content the sync did not write and cannot keep stops a swap before anything
// changes, forced or not; once it is moved away the same sync goes through.
func TestSync_SwapRefusesToDeleteLocalContent(t *testing.T) {
	cases := []struct {
		name     string
		from, to map[string]string
		arrange  func(c *deltaClone)
		// blocker is the path the error names, clear moves it out of the way.
		blocker, reason string
		clear           func(c *deltaClone)
	}{
		{
			name: "untracked file in a folder that becomes a file",
			from: map[string]string{"a/x/y": "y\n"}, to: map[string]string{"a/x": "x\n"},
			arrange: func(c *deltaClone) {
				require.NoError(c.t, os.WriteFile(c.path("a/x/mine.txt"), []byte("mine\n"), 0o644))
			},
			blocker: "a/x/mine.txt", reason: "untracked",
			clear: func(c *deltaClone) { require.NoError(c.t, os.Remove(c.path("a/x/mine.txt"))) },
		},
		{
			name: "ignored file deep in a folder that becomes a file",
			from: map[string]string{".nipaignore": "*.tmp\n", "a/x/y/z": "z\n"}, to: map[string]string{".nipaignore": "*.tmp\n", "a/x": "x\n"},
			arrange: func(c *deltaClone) {
				require.NoError(c.t, os.WriteFile(c.path("a/x/y/build.tmp"), []byte("cache"), 0o644))
			},
			blocker: "a/x/y/build.tmp", reason: "untracked",
			clear: func(c *deltaClone) { require.NoError(c.t, os.Remove(c.path("a/x/y/build.tmp"))) },
		},
		{
			name: "edited tracked file in a folder that becomes a file",
			from: map[string]string{"a/x/y": "y\n", "a/x/z": "bin\x00z"}, to: map[string]string{"a/x": "x\n"},
			arrange: func(c *deltaClone) {
				require.NoError(c.t, os.WriteFile(c.path("a/x/z"), []byte("bin\x00mine"), 0o644))
			},
			blocker: "a/x/z", reason: "locally changed",
			clear: func(c *deltaClone) { require.NoError(c.t, os.WriteFile(c.path("a/x/z"), []byte("bin\x00z"), 0o644)) },
		},
		{
			name: "edited tracked file that becomes a folder",
			from: map[string]string{"a/b": "b\n"}, to: map[string]string{"a/b/c": "c\n"},
			arrange: func(c *deltaClone) {
				require.NoError(c.t, os.WriteFile(c.path("a/b"), []byte("mine\n"), 0o644))
			},
			blocker: "a/b", reason: "locally changed",
			clear: func(c *deltaClone) { require.NoError(c.t, os.Remove(c.path("a/b"))) },
		},
		{
			name: "untracked file where a folder is needed",
			from: map[string]string{"k": "k\n"}, to: map[string]string{"k": "k\n", "n/o/p": "p\n"},
			arrange: func(c *deltaClone) {
				require.NoError(c.t, os.WriteFile(c.path("n"), []byte("mine\n"), 0o644))
			},
			blocker: "n", reason: "untracked or kept file where a folder is needed",
			clear: func(c *deltaClone) { require.NoError(c.t, os.Remove(c.path("n"))) },
		},
		{
			name: "untracked folder with a file where a file is written",
			from: map[string]string{"k": "k\n"}, to: map[string]string{"k": "k\n", "n": "bin\x00n"},
			arrange: func(c *deltaClone) {
				require.NoError(c.t, os.MkdirAll(c.path("n/deep"), 0o755))
				require.NoError(c.t, os.WriteFile(c.path("n/deep/mine"), []byte("mine\n"), 0o644))
			},
			blocker: "n/deep/mine", reason: "untracked",
			clear: func(c *deltaClone) { require.NoError(c.t, os.RemoveAll(c.path("n"))) },
		},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, struct {
			name            string
			from, to        map[string]string
			arrange         func(c *deltaClone)
			blocker, reason string
			clear           func(c *deltaClone)
		}{
			name: "link in a folder that becomes a file",
			from: map[string]string{"a/x/y": "y\n"}, to: map[string]string{"a/x": "x\n"},
			arrange: func(c *deltaClone) {
				outside := filepath.Join(c.t.TempDir(), "outside.txt")
				require.NoError(c.t, os.WriteFile(outside, []byte("outside\n"), 0o644))
				require.NoError(c.t, os.Symlink(outside, c.path("a/x/link")))
			},
			blocker: "a/x/link", reason: "a link",
			clear: func(c *deltaClone) { require.NoError(c.t, os.Remove(c.path("a/x/link"))) },
		})
	}
	for _, tc := range cases {
		for _, sync := range []string{"delta", "full"} {
			t.Run(tc.name+"/"+sync, func(t *testing.T) {
				server := newDeltaServer(t)
				c, base := newSwapClone(t, server, tc.from)
				target := server.commit(tc.to)
				tc.arrange(c)
				before := c.working()
				foldersBefore := c.folders()
				for _, force := range []bool{false, true} {
					err := c.move("update", force, sync == "full", target)
					require.Error(t, err, "force=%v", force)
					require.ErrorContains(t, err, tc.blocker)
					require.ErrorContains(t, err, tc.reason)
					var userErr *domain.Error
					require.True(t, errors.As(err, &userErr), "a user error")
					require.Contains(t, userErr.Hint, "nothing was changed")
					require.Equal(t, before, c.working(), "nothing is changed in the working copy")
					require.Equal(t, foldersBefore, c.folders())
					local, err := c.lr.LoadCommit()
					require.NoError(t, err)
					require.Equal(t, base, local.CommitID)
					if sync == "delta" {
						c.requireBase(base, before)
					}
				}
				tc.clear(c)
				require.NoError(t, c.move("update", false, false, target))
				c.requireAt(target)
				require.Equal(t, foldersOf(tc.to), c.folders())
			})
		}
	}
}

// What a swap may delete without asking: empty folders, a tracked file that
// is already gone, read-only files and folders the tree drops.
func TestSync_SwapDeletesWhatHoldsNoContent(t *testing.T) {
	t.Run("empty folders below a folder that becomes a file", func(t *testing.T) {
		server := newDeltaServer(t)
		c, _ := newSwapClone(t, server, map[string]string{"a/x/y": "y\n"})
		require.NoError(t, os.MkdirAll(c.path("a/x/empty/deeper"), 0o755))
		target := server.commit(map[string]string{"a/x": "x\n"})
		require.NoError(t, c.move("update", false, false, target))
		c.requireAt(target)
		require.Equal(t, []string{"a"}, c.folders())
	})
	t.Run("an empty untracked folder where a file is written", func(t *testing.T) {
		// Before: the rename onto the folder failed after the download.
		server := newDeltaServer(t)
		c, _ := newSwapClone(t, server, map[string]string{"k": "k\n"})
		require.NoError(t, os.MkdirAll(c.path("n/m"), 0o755))
		target := server.commit(map[string]string{"k": "k\n", "n": "n\n"})
		require.NoError(t, c.move("update", false, false, target))
		c.requireAt(target)
	})
	t.Run("tracked files already deleted from disk", func(t *testing.T) {
		server := newDeltaServer(t)
		c, _ := newSwapClone(t, server, map[string]string{"a/b": "b\n", "a/x/y": "y\n", "a/x/z": "z\n"})
		require.NoError(t, os.Remove(c.path("a/b")))
		require.NoError(t, os.Remove(c.path("a/x/y")))
		target := server.commit(map[string]string{"a/b/c": "c\n", "a/x": "x\n"})
		require.NoError(t, c.move("update", false, false, target))
		c.requireAt(target)
	})
	t.Run("read-only files and folders", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("folder permissions are POSIX")
		}
		server := newDeltaServer(t)
		c, _ := newSwapClone(t, server, map[string]string{"a/x/y/z": "z\n", "a/x/w": "bin\x00w", "a/b": "b\n"})
		require.NoError(t, os.Chmod(c.path("a/x/w"), 0o444))
		require.NoError(t, os.Chmod(c.path("a/b"), 0o444))
		require.NoError(t, os.Chmod(c.path("a/x/y"), 0o555))
		require.NoError(t, os.Chmod(c.path("a/x"), 0o555))
		target := server.commit(map[string]string{"a/x": "x\n", "a/b/c": "c\n"})
		require.NoError(t, c.move("update", false, false, target))
		c.requireAt(target)
	})
}

// A sync stopped between freeing a path and writing it leaves the stored
// tree at the base and no record; the next update, forced or not, finishes.
func TestSync_SwapInterruptedRecovers(t *testing.T) {
	t.Run("download fails after a file that becomes a folder was removed", func(t *testing.T) {
		for _, force := range []bool{false, true} {
			server := newDeltaServer(t)
			c, base := newSwapClone(t, server, map[string]string{"a/b": "b\n", "k": "k\n"})
			target := server.commit(map[string]string{"a/b/c": "bin\x00c", "k": "k\n"})
			server.head("main", target)
			failing := &failingDownloadServer{deltaServer: server}
			err := newTestUpdate(t, c.lr, failing).WithForce(force).Run(context.Background(), c.root)
			require.ErrorContains(t, err, "network down")
			// The unchanged tracked file was removed first; the folder the
			// temp file of a/b/c was prepared in may stay.
			info, statErr := os.Lstat(c.path("a/b"))
			require.True(t, os.IsNotExist(statErr) || info.IsDir(), "a/b is no longer a file")
			local, err := c.lr.LoadCommit()
			require.NoError(t, err)
			require.Equal(t, base, local.CommitID)
			synced, err := c.lr.LoadSyncedTree()
			require.NoError(t, err)
			require.Nil(t, synced, "no record: the next update syncs fully")

			require.NoError(t, c.update(force).Run(context.Background(), c.root))
			c.requireAt(target)
		}
	})
	t.Run("killed after a folder that becomes a file was removed", func(t *testing.T) {
		server := newDeltaServer(t)
		c, _ := newSwapClone(t, server, map[string]string{"a/x/y": "y\n", "a/x/z": "z\n"})
		target := server.commit(map[string]string{"a/x": "bin\x00x"})
		require.NoError(t, c.lr.ClearSyncedTree())
		require.NoError(t, os.RemoveAll(c.path("a/x")))
		require.NoError(t, c.move("update", false, false, target))
		c.requireAt(target)
	})
	t.Run("killed after the file was written in place of a folder", func(t *testing.T) {
		for _, force := range []bool{false, true} {
			server := newDeltaServer(t)
			c, _ := newSwapClone(t, server, map[string]string{"a/x/y": "y\n", "a/x/z/w": "w\n"})
			target := server.commit(map[string]string{"a/x": "x\n"})
			require.NoError(t, c.lr.ClearSyncedTree())
			require.NoError(t, os.RemoveAll(c.path("a/x")))
			require.NoError(t, os.WriteFile(c.path("a/x"), []byte("x\n"), 0o644))
			require.NoError(t, c.move("update", force, false, target))
			c.requireAt(target)
		}
	})
}

// failingDownloadServer is a deltaServer whose chunk downloads fail.
type failingDownloadServer struct{ *deltaServer }

func (s *failingDownloadServer) DownloadChunks(context.Context, domain.ChunkScope, []serverDomain.Hash, func(serverDomain.Hash, []byte) error) error {
	return errors.New("network down")
}

// Swaps at and across the edge of a sparse checkout.
func TestSync_SwapInSparseCheckout(t *testing.T) {
	cases := []struct {
		name     string
		sparse   []string
		from, to map[string]string
		want     map[string]string
	}{
		{
			name:   "folder that is the sparse path becomes a file",
			sparse: []string{"a/x"},
			from:   map[string]string{"a/x/y": "y\n", "a/z": "z\n"}, to: map[string]string{"a/x": "x\n", "a/z": "z\n"},
		},
		{
			name:   "file that is the sparse path becomes a folder",
			sparse: []string{"a/x"},
			from:   map[string]string{"a/x": "x\n", "a/z": "z\n"}, to: map[string]string{"a/x/y": "bin\x00y", "a/z": "z\n"},
		},
		{
			name:   "swap inside the sparse set, next to files outside it",
			sparse: []string{"a"},
			from:   map[string]string{"a/b": "b\n", "a/x/y": "y\n", "b/x": "x\n"}, to: map[string]string{"a/b/c": "c\n", "a/x": "x\n", "b/x/y": "y\n"},
		},
	}
	for _, tc := range cases {
		for _, back := range []bool{false, true} {
			for _, sync := range []string{"delta", "full"} {
				t.Run(fmt.Sprintf("%s/back=%v/%s", tc.name, back, sync), func(t *testing.T) {
					from, to := tc.from, tc.to
					if back {
						from, to = to, from
					}
					server := newDeltaServer(t)
					c, _ := newSwapClone(t, server, from, tc.sparse...)
					target := server.commit(to)
					require.NoError(t, c.move("update", false, sync == "full", target))
					c.requireAt(target, tc.sparse...)
				})
			}
		}
	}
}

// Removing tracked files takes the folders they leave empty with them, as a
// fresh clone has none, and keeps every folder that still holds something.
func TestSync_RemovalsPruneEmptiedFolders(t *testing.T) {
	server := newDeltaServer(t)
	c, _ := newSwapClone(t, server, map[string]string{"a/b/c/d.txt": "d\n", "a/e.txt": "e\n", "f/g.txt": "g\n", "h/i.txt": "i\n"})
	require.NoError(t, os.WriteFile(c.path("h/mine.txt"), []byte("mine\n"), 0o644))
	target := server.commit(map[string]string{"a/e.txt": "e\n"})
	require.NoError(t, c.move("update", false, false, target))
	require.Equal(t, []string{"a", "h"}, c.folders())
	require.Equal(t, map[string]string{"a/e.txt": "e\n", "h/mine.txt": "mine\n"}, c.working())
}

func TestPruneEmptiedFolders_KeepsLinksAndFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), nil, 0o644))
	pruneEmptiedFolders(root, []string{"a/b/gone.txt", "link/gone.txt", "file/gone.txt", "top.txt"})
	_, err := os.Lstat(filepath.Join(root, "a"))
	require.True(t, os.IsNotExist(err), "emptied folders go")
	info, err := os.Lstat(filepath.Join(root, "link"))
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink, "a link is not a folder to prune")
	_, err = os.Lstat(filepath.Join(root, "file"))
	require.NoError(t, err)
}

func TestIsNotDir(t *testing.T) {
	require.False(t, isNotDir(nil))
	require.True(t, isNotDir(syscall.ENOTDIR))
	require.True(t, isNotDir(&fs.PathError{Op: "open", Path: "x", Err: errors.New("not a directory")}))
	require.False(t, isNotDir(&fs.PathError{Op: "open", Path: "x", Err: fs.ErrNotExist}))
	require.False(t, isNotDir(errors.New("other")))

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "f"), nil, 0o644))
	r, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	_, err = r.OpenRoot("f")
	require.True(t, isNotDir(err), "os.Root opening a file as a folder: %v", err)
	_, err = r.Lstat("f/g")
	require.True(t, isNotDir(err), "a path below a file: %v", err)
}

// guardedRemove keeps a folder that took a tracked file's place and treats
// a path below a file as gone.
func TestGuardedRemove_FolderInPlaceAndFileAbove(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0o755))
	removed, err := guardedRemove(root, domain.SnapshotFile{Path: "a/b"})
	require.NoError(t, err)
	require.False(t, removed)
	_, err = os.Stat(filepath.Join(root, "a", "b"))
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(root, "f"), nil, 0o644))
	removed, err = guardedRemove(root, domain.SnapshotFile{Path: "f/g/h"})
	require.NoError(t, err)
	require.True(t, removed)
}

func TestSwapConflictError_ListsAtMostTwenty(t *testing.T) {
	var blockers []swapBlocker
	for i := 0; i < 25; i++ {
		blockers = append(blockers, swapBlocker{fmt.Sprintf("p%02d", i), "not tracked"})
	}
	err := swapConflictError(blockers)
	require.ErrorContains(t, err, "25 path(s)")
	require.ErrorContains(t, err, "p19 (not tracked)")
	require.NotContains(t, err.Error(), "p20")
	require.ErrorContains(t, err, "... and 5 more")
}

// threeWaySide encodes contents as merge files, caching their chunks in lr.
func threeWaySide(t *testing.T, lr *localrepo.LocalRepo, contents map[string]string) map[string]merge.File {
	t.Helper()
	out := make(map[string]merge.File, len(contents))
	for p, content := range contents {
		f, batch, err := encodeMergedFile(p, []byte(content), 2, false)
		require.NoError(t, err)
		require.NoError(t, lr.StoreChunks(batch))
		out[p] = f
	}
	return out
}

// applySwapThreeWay applies the three-way result of base/ours/theirs to a
// working copy holding ours (merge, revert and shelve apply share it).
func applySwapThreeWay(t *testing.T, base, ours, theirs map[string]string, arrange func(root string)) (*threeWayResult, string, error) {
	t.Helper()
	lr, root := newDirectRepo(t)
	b, o, th := threeWaySide(t, lr, base), threeWaySide(t, lr, ours), threeWaySide(t, lr, theirs)
	baseByPath := map[string]domain.SnapshotFile{}
	for p, f := range o {
		writeRepoFile(t, root, p, ours[p])
		baseByPath[p] = domain.SnapshotFile{Path: p, Hash: f.Hash, Mode: f.Mode, Encoding: f.Encoding, Chunks: f.ChunkHashes}
	}
	if arrange != nil {
		arrange(root)
	}
	res, err := applyThreeWay(context.Background(), noDownload{}, lr, root, o, baseByPath, merge.ThreeWay(b, o, th), domain.ChunkScope{}, true)
	return res, root, err
}

func TestApplyThreeWay_Swaps(t *testing.T) {
	for _, tc := range swapCases {
		for _, back := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/back=%v", tc.name, back), func(t *testing.T) {
				from, to := tc.from, tc.to
				if back {
					from, to = to, from
				}
				res, root, err := applySwapThreeWay(t, from, from, to, nil)
				require.NoError(t, err)
				c := &deltaClone{t: t, root: root}
				require.Equal(t, to, c.working())
				require.Equal(t, foldersOf(to), c.folders())
				var gone []string
				for p := range from {
					if _, kept := to[p]; !kept {
						gone = append(gone, p)
					}
				}
				sort.Strings(gone)
				require.Equal(t, gone, res.Deleted, "every removed path is staged as deleted")
				require.Empty(t, res.Conflicted)
			})
		}
	}
}

func TestApplyThreeWay_SwapRefusesToDeleteContent(t *testing.T) {
	t.Run("ours edited the file theirs turned into a folder", func(t *testing.T) {
		// The merge keeps ours (modify/delete conflict) and takes theirs below
		// it; both cannot be on disk, so nothing is written.
		_, root, err := applySwapThreeWay(t,
			map[string]string{"a/b": "b\n"},
			map[string]string{"a/b": "mine\n"},
			map[string]string{"a/b/c": "c\n"}, nil)
		require.ErrorContains(t, err, "a/b (untracked or kept file where a folder is needed)")
		c := &deltaClone{t: t, root: root}
		require.Equal(t, map[string]string{"a/b": "mine\n"}, c.working())
	})
	t.Run("untracked file in the folder theirs turned into a file", func(t *testing.T) {
		_, root, err := applySwapThreeWay(t,
			map[string]string{"a/x/y": "y\n"},
			map[string]string{"a/x/y": "y\n"},
			map[string]string{"a/x": "x\n"},
			func(root string) { writeRepoFile(t, root, "a/x/mine", "mine") })
		require.ErrorContains(t, err, "a/x/mine")
		c := &deltaClone{t: t, root: root}
		require.Equal(t, map[string]string{"a/x/y": "y\n", "a/x/mine": "mine"}, c.working())
	})
}

// A merge of two sound commits can still hold a path as a file and as a
// folder: ours added the file dir, theirs the folder dir/x, and merge.ThreeWay
// keeps both. The tracked file in the way is refused as content to move, not
// blamed on a damaged target commit: a three-way result is not a commit.
func TestApplyThreeWay_TrackedFileBlockingIsNotADamagedTarget(t *testing.T) {
	fx := newShelfFixture(t, map[string]string{"dir": "mine\n"})
	snap, err := fx.lr.Snapshot()
	require.NoError(t, err)
	baseByPath := map[string]domain.SnapshotFile{}
	for _, f := range snap.Files {
		baseByPath[f.Path] = f
	}
	ours := threeWaySide(t, fx.lr, map[string]string{"dir": "mine\n"})
	theirs := threeWaySide(t, fx.lr, map[string]string{"dir/x": "x\n"})
	result := merge.ThreeWay(map[string]merge.File{}, ours, theirs)

	_, err = applyThreeWay(context.Background(), noDownload{}, fx.lr, fx.root, ours, baseByPath, result, domain.ChunkScope{}, true)
	require.ErrorContains(t, err, "dir (untracked or kept file where a folder is needed)")
	require.NotContains(t, err.Error(), "the target commit is damaged")
	c := &deltaClone{t: t, root: fx.root}
	require.Equal(t, map[string]string{"dir": "mine\n"}, c.working())
	require.Empty(t, c.folders())
}

// Restoring a tracked file where the user put a folder, or below a folder
// the user replaced by a file: content in the way is refused, an empty
// folder gives way.
func TestRestore_LocalSwaps(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a/b": "b\n", "a/x/y": "y\n"})
	require.NoError(t, os.Remove(filepath.Join(fx.root, "a", "b")))
	writeRepoFile(t, fx.root, "a/b/mine", "mine")
	require.NoError(t, os.RemoveAll(filepath.Join(fx.root, "a", "x")))
	writeRepoFile(t, fx.root, "a/x", "my file")

	err := fx.wc.Restore(ctx, []string{"a/b"})
	require.ErrorContains(t, err, "a/b/mine")
	require.Equal(t, "mine", fx.read(t, "a/b/mine"))
	err = fx.wc.Restore(ctx, []string{"a/x/y"})
	require.ErrorContains(t, err, "a/x (untracked or kept file where a folder is needed)")
	require.Equal(t, "my file", fx.read(t, "a/x"))

	require.NoError(t, os.Remove(filepath.Join(fx.root, "a", "b", "mine")))
	require.NoError(t, os.Remove(filepath.Join(fx.root, "a", "x")))
	require.NoError(t, fx.wc.Restore(ctx, []string{"a"}))
	require.Equal(t, "b\n", fx.read(t, "a/b"))
	require.Equal(t, "y\n", fx.read(t, "a/x/y"))
}

// Shelving a local swap restores the tracked side and clears the new one in
// an order that works; applying the shelf brings the swap back.
func TestShelve_LocalSwapRoundTrip(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a/b": "b\n", "a/x/y": "y\n", "k": "k\n"})
	require.NoError(t, os.Remove(filepath.Join(fx.root, "a", "b")))
	writeRepoFile(t, fx.root, "a/b/c", "c\n")
	require.NoError(t, os.RemoveAll(filepath.Join(fx.root, "a", "x")))
	writeRepoFile(t, fx.root, "a/x", "x\n")
	swapped := map[string]string{"a/b/c": "c\n", "a/x": "x\n", "k": "k\n"}
	c := &deltaClone{t: t, root: fx.root}
	require.Equal(t, swapped, c.working())

	shelf, err := fx.sh.Create(ctx, ShelveOptions{IncludeUntracked: true})
	require.NoError(t, err)
	require.Equal(t, []string{"a/b", "a/b/c", "a/x", "a/x/y"}, entryPaths(shelf))
	require.Equal(t, map[string]string{"a/b": "b\n", "a/x/y": "y\n", "k": "k\n"}, c.working())

	_, err = fx.sh.Apply(ctx, shelf.ID, false)
	require.NoError(t, err)
	require.Equal(t, swapped, c.working())
}

// A swap made in the working copy shows in status, stages with add and
// pushes: a tracked file below a path that is a file now, or a tracked file
// that is a folder now, reads as deleted.
func TestLocalSwap_StatusAddAndPushClassification(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a/b": "b\n", "a/x/y": "y\n", "k": "k\n"})
	require.NoError(t, os.Remove(filepath.Join(fx.root, "a", "b")))
	writeRepoFile(t, fx.root, "a/b/c", "c\n")
	require.NoError(t, os.RemoveAll(filepath.Join(fx.root, "a", "x")))
	writeRepoFile(t, fx.root, "a/x", "x\n")

	st := fx.status(t)
	require.Equal(t, []string{"a/b", "a/x/y"}, st.Missing)
	require.Equal(t, []string{"a/b/c", "a/x"}, st.Untracked)

	require.NoError(t, fx.wc.Add(ctx, []string{"."}))
	st = fx.status(t)
	require.ElementsMatch(t, []string{"a/b/c", "a/x"}, st.Staged)
	require.ElementsMatch(t, []string{"a/b", "a/x/y"}, st.Deleted)

	snap, err := fx.lr.Snapshot()
	require.NoError(t, err)
	baseByPath := map[string]domain.SnapshotFile{}
	for _, f := range snap.Files {
		baseByPath[f.Path] = f
	}
	staged, err := fx.lr.ListStaged()
	require.NoError(t, err)
	toRead, removed, err := classifyStaged(fx.root, staged, baseByPath)
	require.NoError(t, err)
	var read []string
	for _, f := range toRead {
		read = append(read, f.path)
	}
	require.ElementsMatch(t, []string{"a/b/c", "a/x"}, read)
	require.ElementsMatch(t, []string{"a/b", "a/x/y"}, removed)

	// A folder at a staged path the base does not hold is still refused.
	require.NoError(t, os.MkdirAll(filepath.Join(fx.root, "n"), 0o755))
	_, _, err = classifyStaged(fx.root, []string{"n"}, baseByPath)
	require.ErrorContains(t, err, "is not a regular file")
}

func TestTrackedFileChanged(t *testing.T) {
	root := t.TempDir()
	hash, _, _, encoding := testEncodedFile(t, "f", "content\n")
	f := domain.SnapshotFile{Path: "f", Hash: hash, Encoding: encoding}
	require.False(t, trackedFileChanged(root, f, nil), "a missing file holds nothing")

	writeRepoFile(t, root, "f", "content\n")
	require.False(t, trackedFileChanged(root, f, nil))
	info, err := os.Stat(filepath.Join(root, "f"))
	require.NoError(t, err)
	fresh := map[string]domain.StatEntry{"f": {Hash: serverDomain.Hash{1}, SizeBytes: info.Size(), MtimeNS: info.ModTime().UnixNano(), CachedAt: info.ModTime().UnixNano() + 1}}
	require.True(t, trackedFileChanged(root, f, fresh), "a fresh fingerprint answers without reading")

	writeRepoFile(t, root, "f", "edited\n")
	require.True(t, trackedFileChanged(root, f, nil))

	require.NoError(t, os.Remove(filepath.Join(root, "f")))
	require.NoError(t, os.Mkdir(filepath.Join(root, "f"), 0o755))
	require.True(t, trackedFileChanged(root, f, nil), "a folder in its place is not its content")
}

// A file that changed between the check and the removal is kept and stops
// the sync.
func TestSwapPlan_RemoveKeepsAFileChangedSinceTheCheck(t *testing.T) {
	root := t.TempDir()
	hash, _, _, encoding := testEncodedFile(t, "a/b", "b\n")
	writeRepoFile(t, root, "a/b", "changed\n")
	plan := &swapPlan{blocking: []domain.SnapshotFile{{Path: "a/b", Hash: hash, Encoding: encoding}}}
	err := plan.removeBlocking(context.Background(), root)
	require.ErrorContains(t, err, "a/b (changed while the sync ran)")
	require.Equal(t, "changed\n", string(readRepoFile(t, root, "a/b")))

	// A folder planned for clearing that disappeared since stops it too.
	plan = &swapPlan{folders: []string{"gone"}}
	require.Error(t, plan.clearFolders(context.Background(), root))
}
