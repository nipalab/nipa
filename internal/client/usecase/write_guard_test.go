package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
)

// symlinkOrSkip links link to target, skipping the test where the platform
// refuses symlinks (Windows without the privilege).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// linkTargets are the places a symlinked folder of the working copy may
// point at: outside the clone, or into its metadata.
func linkTargets(t *testing.T, root string) map[string]string {
	return map[string]string{
		"outside":  t.TempDir(),
		"metadata": filepath.Join(root, ".nipa"),
	}
}

func requireLinkRefusal(t *testing.T, err error, verb, p, link string) {
	t.Helper()
	requireCode(t, err, 400)
	require.ErrorContains(t, err, fmt.Sprintf("refusing to %s %q: %q is a symbolic link", verb, p, link))
}

// listing names what dir holds, to compare before and after an operation.
func listing(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		// The repository lock and the write markers appear in .nipa with the
		// first operation; they are not content written through a link.
		if e.Name() != "lock" && e.Name() != "writes" {
			names = append(names, e.Name())
		}
	}
	return names
}

func countingLstat(n *atomic.Int64) func(string) (fs.FileInfo, error) {
	return func(p string) (fs.FileInfo, error) {
		n.Add(1)
		return os.Lstat(p)
	}
}

func TestDirGuard_RefusesLinkedFolders(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "real", "sub"), 0o755))
	writeRepoFile(t, root, "file", "x")
	symlinkOrSkip(t, t.TempDir(), filepath.Join(root, "out"))
	symlinkOrSkip(t, filepath.Join(root, ".nipa"), filepath.Join(root, "real", "meta"))
	symlinkOrSkip(t, filepath.Join(root, "real"), filepath.Join(root, "inside"))

	g := newDirGuard(root)
	require.NoError(t, g.check("write", "top.bin"), "a file at the root has no folder to check")
	require.NoError(t, g.check("write", "real/sub/a.bin"))
	require.NoError(t, g.check("write", "missing/deeper/a.bin"), "missing folders are created by the write")
	require.NoError(t, g.check("write", "file/below/x.txt"), "a file in the way fails the write itself")
	requireLinkRefusal(t, g.check("write", "out/a.bin"), "write", "out/a.bin", "out")
	requireLinkRefusal(t, g.check("remove", "out/new/deeper/a.bin"), "remove", "out/new/deeper/a.bin", "out")
	requireLinkRefusal(t, g.check("write", "real/meta/config"), "write", "real/meta/config", "real/meta")
	requireLinkRefusal(t, g.check("write", "inside/a.bin"), "write", "inside/a.bin", "inside")
}

func TestDirGuard_RefusesJunctionsAndReportsErrors(t *testing.T) {
	g := newDirGuard(t.TempDir())
	g.lstat = func(string) (fs.FileInfo, error) { return fakeInfo{mode: fs.ModeIrregular}, nil }
	err := g.check("write", "j/a.bin")
	requireCode(t, err, 400)
	require.ErrorContains(t, err, `"j" is a junction or reparse point`)

	g = newDirGuard(t.TempDir())
	g.lstat = func(string) (fs.FileInfo, error) { return fakeInfo{mode: fs.ModeDir | fs.ModeIrregular}, nil }
	require.NoError(t, g.check("write", "cloud/a.bin"), "a folder that is a non-link reparse point is a real folder")

	boom := errors.New("permission denied")
	g = newDirGuard(t.TempDir())
	g.lstat = func(string) (fs.FileInfo, error) { return nil, boom }
	require.ErrorIs(t, g.check("write", "d/a.bin"), boom)
}

type fakeInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (f fakeInfo) Mode() fs.FileMode { return f.mode }
func (f fakeInfo) IsDir() bool       { return f.mode.IsDir() }

func TestDirGuard_AcceptsALinkedRoot(t *testing.T) {
	real := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(real, "dir"), 0o755))
	root := filepath.Join(t.TempDir(), "clone")
	symlinkOrSkip(t, real, root)
	require.NoError(t, checkWriteInsideRoot(root, "dir/a.bin"))
	require.NoError(t, checkRemoveInsideRoot(root, "dir/a.bin"))
	requireCode(t, checkWriteInsideRoot(root, ".nipa/config"), 400)
	requireCode(t, checkRemoveInsideRoot(root, "../x"), 400)
	requireCode(t, checkTargets(root, "write", []string{"dir/a.bin", "../x"}), 400)
}

func TestDirGuard_CostsOneLstatPerFolder(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a/b", "c"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755))
	}
	var n atomic.Int64
	g := newDirGuard(root)
	g.lstat = countingLstat(&n)
	for i := range 1000 {
		for _, dir := range []string{"a/b", "a", "c", "new/x/y"} {
			require.NoError(t, g.check("write", fmt.Sprintf("%s/f%d.bin", dir, i)))
		}
	}
	require.Equal(t, int64(4), n.Load(), "a, a/b, c and the missing new: once each")
}

func TestGuardWrites_SharesOneGuardPerScope(t *testing.T) {
	root := t.TempDir()
	require.NotSame(t, guardFor(root), guardFor(root), "without a scope nothing is remembered")
	end := guardWrites(root)
	g := guardFor(root)
	inner := guardWrites(root + string(filepath.Separator))
	require.Same(t, g, guardFor(root), "nested scopes share the guard")
	inner()
	inner()
	require.Same(t, g, guardFor(root), "closing twice releases once")
	end()
	require.NotSame(t, g, guardFor(root), "a later operation never trusts an earlier one's folders")
}

// TestSync_FolderChecksStayPerFolder runs real syncs with the guard's Lstat
// counted: the planning check and every write-time check share one scope, so
// a sync costs one Lstat per distinct folder, not per file.
func TestSync_FolderChecksStayPerFolder(t *testing.T) {
	lr, root := newDirectRepo(t)
	data := map[serverDomain.Hash][]byte{}
	treeOf := func(version string) *serverDomain.TreeNode {
		tree := &serverDomain.TreeNode{Name: "root"}
		for _, d := range []string{"d1", "d2", "d3"} {
			node := &serverDomain.TreeNode{Name: d}
			sub := &serverDomain.TreeNode{Name: "sub"}
			for i := range 40 {
				node.FileChildren = append(node.FileChildren, rawManifestFile(fmt.Sprintf("f%d.bin", i), data, version, fmt.Sprint(i)))
				sub.FileChildren = append(sub.FileChildren, textManifestFile(t, fmt.Sprintf("t%d.txt", i), version+fmt.Sprint(i), data))
			}
			node.TreeChildren = []*serverDomain.TreeNode{sub}
			tree.TreeChildren = append(tree.TreeChildren, node)
		}
		return tree
	}
	run := func(tree *serverDomain.TreeNode) int64 {
		end := guardWrites(root)
		defer end()
		var n atomic.Int64
		guardFor(root).lstat = countingLstat(&n)
		require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, tree))
		return n.Load()
	}
	require.Equal(t, int64(3), run(treeOf("one")), "a fresh clone checks the three missing top folders once")
	require.Equal(t, int64(6), run(treeOf("two")), "an update checks each of the six folders once")
	require.Equal(t, "two0", string(readRepoFile(t, root, "d2/f0.bin")))
}

func TestSync_RefusesToWriteThroughALinkedFolder(t *testing.T) {
	for kind, file := range map[string]func(t *testing.T, data map[serverDomain.Hash][]byte) *serverDomain.File{
		"direct": func(_ *testing.T, data map[serverDomain.Hash][]byte) *serverDomain.File {
			return rawManifestFile("x.bin", data, "AAAA", "BB")
		},
		"cached": func(t *testing.T, data map[serverDomain.Hash][]byte) *serverDomain.File {
			return textManifestFile(t, "x.bin", "text content\n", data)
		},
	} {
		for where := range linkTargets(t, t.TempDir()) {
			t.Run(kind+"/"+where, func(t *testing.T) {
				lr, root := newDirectRepo(t)
				target := linkTargets(t, root)[where]
				symlinkOrSkip(t, target, filepath.Join(root, "assets"))
				before := listing(t, target)
				data := map[serverDomain.Hash][]byte{}
				tree := &serverDomain.TreeNode{Name: "root",
					FileChildren: []*serverDomain.File{rawManifestFile("top.bin", data, "TOP")},
					TreeChildren: []*serverDomain.TreeNode{{Name: "assets", TreeChildren: []*serverDomain.TreeNode{{
						Name: "deep", FileChildren: []*serverDomain.File{file(t, data)},
					}}}},
				}
				client := &recordingDownloader{data: data}

				err := syncWorkingCopy(context.Background(), client, lr, root, tree, domain.ChunkScope{})
				requireLinkRefusal(t, err, "write", "assets/deep/x.bin", "assets")
				require.Empty(t, client.requested, "nothing is downloaded")
				require.NoFileExists(t, filepath.Join(root, "top.bin"), "nothing is written")
				require.NoDirExists(t, filepath.Join(target, "deep"))
				require.Equal(t, before, listing(t, target))
				requireNoTempFiles(t, root)

				err = guardedSync(lr, root, client, tree)
				requireLinkRefusal(t, err, "write", "assets/deep/x.bin", "assets")
			})
		}
	}
}

func TestSync_RefusesToRemoveThroughALinkedFolder(t *testing.T) {
	for where := range linkTargets(t, t.TempDir()) {
		t.Run(where, func(t *testing.T) {
			lr, root := newDirectRepo(t)
			data := map[serverDomain.Hash][]byte{}
			old := rawManifestFile("old.bin", data, "OLD")
			withOld := &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{Name: "assets", FileChildren: []*serverDomain.File{old}}}}
			require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, withOld))

			target := linkTargets(t, root)[where]
			require.NoError(t, os.RemoveAll(filepath.Join(root, "assets")))
			require.NoError(t, os.WriteFile(filepath.Join(target, "old.bin"), []byte("OLD"), 0o644))
			symlinkOrSkip(t, target, filepath.Join(root, "assets"))

			client := &recordingDownloader{data: data}
			without := &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{rawManifestFile("new.bin", data, "NEW")}}
			err := syncWorkingCopy(context.Background(), client, lr, root, without, domain.ChunkScope{})
			requireLinkRefusal(t, err, "remove", "assets/old.bin", "assets")
			require.Empty(t, client.requested)
			require.NoFileExists(t, filepath.Join(root, "new.bin"))
			require.FileExists(t, filepath.Join(target, "old.bin"), "the file behind the link is kept")

			// The removal itself refuses too, whoever calls it.
			snap, err := lr.Snapshot()
			require.NoError(t, err)
			err = removeLeftFiles(context.Background(), root, lr, snap.Files)
			requireLinkRefusal(t, err, "remove", "assets/old.bin", "assets")
			require.FileExists(t, filepath.Join(target, "old.bin"))
		})
	}
}

func TestSync_ReplacesALinkedFileWithoutTouchingItsTarget(t *testing.T) {
	lr, root := newDirectRepo(t)
	outside := t.TempDir()
	for _, name := range []string{"a.bin", "notes.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(outside, name), []byte("keep"), 0o444))
		symlinkOrSkip(t, filepath.Join(outside, name), filepath.Join(root, "dir", name))
	}
	data := map[serverDomain.Hash][]byte{}
	tree := &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{Name: "dir", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", data, "AAAA", "BB"),
		textManifestFile(t, "notes.txt", "hello\n", data),
	}}}}

	require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, tree))
	for name, want := range map[string]string{"a.bin": "AAAABB", "notes.txt": "hello\n"} {
		info, err := os.Lstat(filepath.Join(root, "dir", name))
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular(), "%s is a regular file now", name)
		require.Equal(t, os.FileMode(0o644), info.Mode().Perm()&0o644, "the link target's read-only mode is not inherited")
		require.Equal(t, want, string(readRepoFile(t, root, "dir/"+name)))
		require.Equal(t, "keep", string(readRepoFile(t, outside, name)))
	}
	requireNoTempFiles(t, root)
}

func TestSync_WorksThroughALinkedRoot(t *testing.T) {
	real := t.TempDir()
	root := filepath.Join(t.TempDir(), "clone")
	symlinkOrSkip(t, real, root)
	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.Init(root))
	data := map[serverDomain.Hash][]byte{}
	tree := &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{Name: "dir", FileChildren: []*serverDomain.File{
		rawManifestFile("a.bin", data, "AAAA"),
		textManifestFile(t, "n.txt", "n\n", data),
	}}}}
	require.NoError(t, syncTree(t, lr, root, &recordingDownloader{data: data}, tree))
	require.Equal(t, "AAAA", string(readRepoFile(t, real, "dir/a.bin")))
	require.Equal(t, "n\n", string(readRepoFile(t, real, "dir/n.txt")))
}

func TestMaterializeFile_RefusesALinkedFolderAndReplacesALinkedFile(t *testing.T) {
	data := []byte("payload")
	open := func(serverDomain.Hash) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(data))), nil
	}
	root := t.TempDir()
	outside := t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(root, "out"))
	file := materializedFile{Path: "out/x.bin", Mode: 2, SizeBytes: int64(len(data)), ChunkHashes: []serverDomain.Hash{{1}}}
	requireLinkRefusal(t, materializeFile(root, file, 0, open), "write", "out/x.bin", "out")
	_, err := createSizedTemp(testWorkTree(t, root), root, file)
	requireLinkRefusal(t, err, "write", "out/x.bin", "out")
	require.Empty(t, listing(t, outside))

	require.NoError(t, os.WriteFile(filepath.Join(outside, "target"), []byte("keep"), 0o644))
	symlinkOrSkip(t, filepath.Join(outside, "target"), filepath.Join(root, "x.bin"))
	file.Path = "x.bin"
	require.NoError(t, materializeFile(root, file, 0, open))
	require.Equal(t, data, readRepoFile(t, root, "x.bin"))
	require.Equal(t, "keep", string(readRepoFile(t, outside, "target")))
}

// linkedRestore is a direct-restore clone whose tracked files sit in
// assets/: big.bin (raw, several chunks) and notes.txt (text).
func linkedRestore(t *testing.T) *directRestore {
	t.Helper()
	lr, root := newDirectRepo(t)
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	data := map[serverDomain.Hash][]byte{}
	content := randomBytes(3, 3<<20)
	file := binaryFile(t, "big.bin", content, data)
	tree := &serverDomain.TreeNode{Name: "root", TreeChildren: []*serverDomain.TreeNode{{Name: "assets", FileChildren: []*serverDomain.File{
		file,
		textManifestFile(t, "notes.txt", "notes\n", data),
	}}}}
	server := &restoreServer{streamingFake: newStreamingFake(data), tree: tree}
	require.NoError(t, syncTree(t, lr, root, server, tree))
	require.NoError(t, lr.SaveCommit("c1", ""))
	server.streamed, server.buffered = map[serverDomain.Hash]int{}, map[serverDomain.Hash]int{}
	wc, err := NewWorkingCopy(lr, root)
	require.NoError(t, err)
	wc.WithDownloader(nil, server)
	return &directRestore{lr: lr, root: root, server: server, wc: wc, content: content, file: file}
}

func TestRestore_RefusesToWriteThroughALinkedFolder(t *testing.T) {
	for _, target := range []string{"assets/big.bin", "assets/notes.txt", "assets"} {
		for where := range linkTargets(t, t.TempDir()) {
			t.Run(target+"/"+where, func(t *testing.T) {
				d := linkedRestore(t)
				// notes.txt is text: its content is in the cache. Drop the
				// cache so the refusal must come before any download.
				require.NoError(t, os.RemoveAll(filepath.Join(d.root, ".nipa", "objects")))
				require.NoError(t, os.MkdirAll(filepath.Join(d.root, ".nipa", "objects"), 0o755))
				linkTo := linkTargets(t, d.root)[where]
				require.NoError(t, os.RemoveAll(filepath.Join(d.root, "assets")))
				symlinkOrSkip(t, linkTo, filepath.Join(d.root, "assets"))
				before := listing(t, linkTo)

				err := d.wc.Restore(context.Background(), []string{target})
				requireCode(t, err, 400)
				require.ErrorContains(t, err, `"assets" is a symbolic link`)
				require.Zero(t, d.server.served(), "nothing is downloaded")
				require.Empty(t, d.server.manifestDirs)
				require.Empty(t, d.server.manifestPaths)
				require.NoFileExists(t, filepath.Join(linkTo, "big.bin"))
				require.NoFileExists(t, filepath.Join(linkTo, "notes.txt"))
				require.Equal(t, before, listing(t, linkTo))
				requireNoTempFiles(t, d.root)
			})
		}
	}
}

func TestRestore_ReplacesALinkedFile(t *testing.T) {
	d := linkedRestore(t)
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "target"), []byte("keep"), 0o644))
	for _, name := range []string{"big.bin", "notes.txt"} {
		p := filepath.Join(d.root, "assets", name)
		require.NoError(t, os.Remove(p))
		symlinkOrSkip(t, filepath.Join(outside, "target"), p)
	}

	require.NoError(t, d.wc.Restore(context.Background(), []string{"assets"}))
	require.Equal(t, d.content, readRepoFile(t, d.root, "assets/big.bin"))
	require.Equal(t, "notes\n", string(readRepoFile(t, d.root, "assets/notes.txt")))
	require.Equal(t, "keep", string(readRepoFile(t, outside, "target")))
	requireNoTempFiles(t, d.root)
}

func TestRemoveWorkingFile_RefusesALinkedFolder(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "new.txt"), []byte("x"), 0o644))
	symlinkOrSkip(t, outside, filepath.Join(root, "dir"))
	requireLinkRefusal(t, removeWorkingFile(root, "dir/new.txt"), "remove", "dir/new.txt", "dir")
	require.FileExists(t, filepath.Join(outside, "new.txt"))
	require.FileExists(t, filepath.Join(root, "dir", "new.txt"))
}

func TestConflicts_ResolveRefusesALinkedFolder(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "hero.png"), []byte("keep"), 0o644))
	symlinkOrSkip(t, outside, filepath.Join(root, "art"))
	local := &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}
	c := newConflicts(t, local, root)

	err := c.Resolve(context.Background(), "art/hero.png", Resolution{Content: strings.NewReader("theirs")})
	requireLinkRefusal(t, err, "write", "art/hero.png", "art")
	err = c.Resolve(context.Background(), "art/hero.png", Resolution{Delete: true})
	requireLinkRefusal(t, err, "remove", "art/hero.png", "art")
	require.Equal(t, "keep", string(readRepoFile(t, outside, "hero.png")))
	require.Equal(t, []string{"hero.png"}, listing(t, outside))
	require.Empty(t, local.stageAdd)
}

func TestApplyThreeWay_RefusesALinkedFolderBeforeDownloading(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "old.txt"), []byte("old"), 0o644))
	symlinkOrSkip(t, outside, filepath.Join(root, "art"))
	theirs := merge.File{Path: "art/new.txt", Mode: 2, SizeBytes: 1, ChunkHashes: []serverDomain.Hash{{1}}}
	for name, res := range map[string]*merge.Result{
		"write":  {Entries: map[string]merge.Entry{"art/new.txt": {Decision: merge.KeepTheirs, Theirs: theirs}}},
		"remove": {Entries: map[string]merge.Entry{}, Deleted: []string{"art/old.txt", "untracked.txt"}},
	} {
		t.Run(name, func(t *testing.T) {
			local := &stubLocalRepo{}
			client := &recordingDownloader{}
			base := map[string]domain.SnapshotFile{"art/old.txt": {Path: "art/old.txt"}}
			_, err := applyThreeWay(context.Background(), client, local, root, nil, base, res, domain.ChunkScope{}, true)
			requireCode(t, err, 400)
			require.ErrorContains(t, err, `"art" is a symbolic link`)
			require.Nil(t, local.missingChunksInput, "nothing is looked up or downloaded")
			require.Empty(t, client.requested)
			require.Equal(t, []string{"old.txt"}, listing(t, outside))

			_, err = applyThreeWay(context.Background(), client, local, root, nil, base, &merge.Result{Entries: map[string]merge.Entry{}}, domain.ChunkScope{}, false)
			require.NoError(t, err, "a dry run writes nothing, so it is not refused")
		})
	}
}

// testWorkTree is a work tree on root released when the test ends.
func testWorkTree(t *testing.T, root string) *workTree {
	t.Helper()
	wt := &workTree{path: root, refs: 1}
	t.Cleanup(wt.release)
	return wt
}

func TestShelve_AbortRefusesToRemoveThroughALinkedFolder(t *testing.T) {
	fx := newShelfFixture(t, map[string]string{"a.txt": "base\n"})
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "added.txt"), []byte("keep"), 0o644))
	symlinkOrSkip(t, outside, filepath.Join(fx.root, "new"))
	require.NoError(t, fx.lr.SaveShelfState(&domain.ShelfState{ShelfID: "s", Applied: []string{"new/added.txt"}}))

	requireLinkRefusal(t, fx.sh.Abort(context.Background()), "remove", "new/added.txt", "new")
	require.Equal(t, "keep", string(readRepoFile(t, outside, "added.txt")))
}

func TestGuardedRemove_KeepsAFileReplacedByALink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "target"), []byte("keep"), 0o644))
	symlinkOrSkip(t, filepath.Join(outside, "target"), filepath.Join(root, "dir", "old.bin"))
	keepHash := chunker.FileHash([]serverDomain.Hash{chunker.Sum([]byte("keep"))})

	removed, err := guardedRemove(root, domain.SnapshotFile{Path: "dir/old.bin", Hash: keepHash})
	require.NoError(t, err)
	require.False(t, removed, "a link is not content the sync wrote")
	require.Equal(t, "keep", string(readRepoFile(t, outside, "target")))

	removed, err = guardedRemove(root, domain.SnapshotFile{Path: "gone/old.bin"})
	require.NoError(t, err)
	require.True(t, removed, "a file whose folder is gone is already removed")
}
