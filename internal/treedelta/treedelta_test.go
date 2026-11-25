package treedelta

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/treehash"
)

func testFile(name string, seed byte, class int) *domain.File {
	chunk := domain.Hash{seed, 1}
	return &domain.File{
		Name: name, Mode: class, SizeBytes: int64(seed),
		Hash:   treehash.FileHash([]domain.Hash{chunk}),
		Chunks: []domain.Chunk{{Hash: chunk, SizeBytes: int64(seed)}},
	}
}

// testDir builds a manifest directory as the client transport decodes it,
// with its real tree hash.
func testDir(name string, files []*domain.File, dirs ...*domain.TreeNode) *domain.TreeNode {
	node := &domain.TreeNode{Name: name, FileChildren: []*domain.File{}, TreeChildren: []*domain.TreeNode{}}
	var fileEntries []treehash.FileEntry
	for _, f := range files {
		node.FileChildren = append(node.FileChildren, f)
		fileEntries = append(fileEntries, treehash.FileEntry{Name: f.Name, Hash: f.Hash, Mode: HashMode(f.Mode)})
	}
	var treeEntries []treehash.TreeEntry
	for _, d := range dirs {
		node.TreeChildren = append(node.TreeChildren, d)
		treeEntries = append(treeEntries, treehash.TreeEntry{Name: d.Name, Hash: d.Hash})
	}
	node.Hash = treehash.TreeHash(fileEntries, treeEntries)
	return node
}

func testBase() *domain.TreeNode {
	return testDir("",
		[]*domain.File{testFile("a.txt", 1, domain.FileModeClassReadWrite), testFile("run.sh", 2, domain.FileModeClassExecutable)},
		testDir("docs", []*domain.File{testFile("guide.md", 3, domain.FileModeClassReadOnly)}),
		testDir("src", []*domain.File{testFile("main.go", 4, domain.FileModeClassReadWrite)},
			testDir("lib", []*domain.File{testFile("x.go", 5, domain.FileModeClassReadWrite)})),
	)
}

func TestApply_UnchangedRootIsTheBase(t *testing.T) {
	base := testBase()
	got, err := Apply(base, &Delta{Root: &Dir{Unchanged: true}, RootHash: &base.Hash})
	require.NoError(t, err)
	require.Same(t, base, got)

	// A client that holds nothing for an unchanged root holds an empty tree.
	got, err = Apply(nil, &Delta{Root: &Dir{Name: "root", Unchanged: true}})
	require.NoError(t, err)
	require.Equal(t, testDir("root", nil), got)
}

func TestApply_RebuildsChangedDirectories(t *testing.T) {
	base := testBase()
	newMain := testFile("main.go", 40, domain.FileModeClassExecutable)
	added := testFile("b.txt", 9, domain.FileModeClassReadWrite)
	fresh := testFile("n.txt", 10, domain.FileModeClassReadWrite)
	delta := &Delta{Root: &Dir{
		Files: []File{{Name: "a.txt"}, {Name: "b.txt", Changed: added}},
		Dirs: []*Dir{
			{Name: "docs", Unchanged: true},
			{Name: "gone", Unchanged: true},
			{Name: "hollow", Dirs: []*Dir{{Name: "inner", Unchanged: true}}},
			{Name: "new", Files: []File{{Name: "n.txt", Changed: fresh}}},
			{Name: "src", Files: []File{{Name: "main.go", Changed: newMain}}, Dirs: []*Dir{{Name: "lib", Unchanged: true}}},
		},
	}}
	want := testDir("",
		[]*domain.File{base.FileChildren[0], added},
		base.TreeChildren[0],
		testDir("new", []*domain.File{fresh}),
		testDir("src", []*domain.File{newMain}, base.TreeChildren[1].TreeChildren[0]),
	)
	delta.RootHash = &want.Hash

	before := testBase()
	got, err := Apply(base, delta)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, before, base, "the base is not modified")
	// run.sh was not listed: removed. "gone" is unchanged but the base holds
	// nothing there, and "hollow" lists only such a directory: both are left
	// out. Unchanged entries are the base's own values.
	require.Same(t, base.FileChildren[0], got.FileChildren[0])
	require.Same(t, base.TreeChildren[0], got.TreeChildren[0])
	require.Same(t, base.TreeChildren[1].TreeChildren[0], got.TreeChildren[2].TreeChildren[0])

	// Everything removed leaves the root, empty.
	got, err = Apply(base, &Delta{Root: &Dir{Name: "r"}})
	require.NoError(t, err)
	require.Equal(t, testDir("r", nil), got)
	require.Equal(t, treehash.TreeHash(nil, nil), got.Hash)

	// A directory new to an empty base.
	got, err = Apply(nil, &Delta{Root: &Dir{Dirs: []*Dir{{Name: "new", Files: []File{{Name: "n.txt", Changed: fresh}}}}}})
	require.NoError(t, err)
	require.Equal(t, testDir("", nil, testDir("new", []*domain.File{fresh})), got)
}

func TestApply_HashesModeClasses(t *testing.T) {
	for _, class := range []int{domain.FileModeClassReadOnly, domain.FileModeClassReadWrite, domain.FileModeClassExecutable, 0} {
		file := testFile("f", 1, class)
		got, err := Apply(nil, &Delta{Root: &Dir{Files: []File{{Name: "f", Changed: file}}}})
		require.NoError(t, err)
		stored := map[int]int{domain.FileModeClassReadOnly: 0o444, domain.FileModeClassExecutable: 0o755}[class]
		if stored == 0 {
			stored = 0o644
		}
		require.Equal(t, stored, HashMode(class))
		require.Equal(t, treehash.TreeHash([]treehash.FileEntry{{Name: "f", Hash: file.Hash, Mode: stored}}, nil), got.Hash)
	}
	require.True(t, File{Name: "f"}.Unchanged())
	require.False(t, File{Name: "f", Changed: &domain.File{}}.Unchanged())
}

func TestApply_Errors(t *testing.T) {
	base := testBase()
	_, err := Apply(base, nil)
	require.Error(t, err)
	_, err = Apply(base, &Delta{})
	require.Error(t, err)

	_, err = Apply(base, &Delta{Root: &Dir{Files: []File{{Name: "nope.txt"}}}})
	require.ErrorContains(t, err, `unchanged file "/nope.txt" is not in the base`)
	_, err = Apply(nil, &Delta{Root: &Dir{Dirs: []*Dir{{Name: "src", Files: []File{{Name: "main.go"}}}}}})
	require.ErrorContains(t, err, `"/src/main.go"`)
	_, err = Apply(base, &Delta{Root: &Dir{Dirs: []*Dir{nil}}})
	require.ErrorContains(t, err, "empty directory entry")

	wrong := domain.Hash{0xff}
	_, err = Apply(base, &Delta{Root: &Dir{Files: []File{{Name: "a.txt"}}}, RootHash: &wrong})
	require.ErrorIs(t, err, ErrRootHashMismatch)
	_, err = Apply(testDir("", nil), &Delta{Root: &Dir{Unchanged: true}, RootHash: &base.Hash})
	require.ErrorIs(t, err, ErrRootHashMismatch, "an unchanged root over another base than the delta was made for")
}
