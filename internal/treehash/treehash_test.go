package treehash

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

func TestFileHash_MatchesChunker(t *testing.T) {
	a := domain.Hash{0x01}
	b := domain.Hash{0x02}
	require.Equal(t, FileHash([]domain.Hash{a, b, b}), FileHash([]domain.Hash{a, b, b}))
	require.NotEqual(t, FileHash([]domain.Hash{a, b}), FileHash([]domain.Hash{b, a}))
}

func TestTreeHash_IndependentOfInsertionOrder(t *testing.T) {
	files := []FileEntry{
		{Name: "b.txt", Mode: 1, Hash: domain.Hash{0x02}},
		{Name: "a.txt", Mode: 1, Hash: domain.Hash{0x01}},
	}
	trees := []TreeEntry{
		{Name: "z", Hash: domain.Hash{0x0a}},
		{Name: "m", Hash: domain.Hash{0x0b}},
	}

	h1 := TreeHash(files, trees)
	h2 := TreeHash([]FileEntry{files[1], files[0]}, []TreeEntry{trees[1], trees[0]})
	require.Equal(t, h1, h2, "hash must not depend on the insertion order")
}

func TestTreeHash_ReflectsContent(t *testing.T) {
	base := []FileEntry{{Name: "a", Hash: domain.Hash{0x01}, Mode: 1}}
	require.NotEqual(t, TreeHash(base, nil), TreeHash([]FileEntry{{Name: "a", Hash: domain.Hash{0x02}, Mode: 1}}, nil))
	require.NotEqual(t, TreeHash(base, nil), TreeHash([]FileEntry{{Name: "b", Hash: domain.Hash{0x01}, Mode: 1}}, nil))
	require.NotEqual(t, TreeHash(base, nil), TreeHash(base, []TreeEntry{{Name: "s", Hash: domain.Hash{0x0f}}}))

	empty := TreeHash(nil, nil)
	require.NotEqual(t, empty, TreeHash([]FileEntry{{Name: "a", Hash: domain.Hash{0x01}, Mode: 1}}, nil))
}

func TestTreeHash_KnownInput(t *testing.T) {
	bb := domain.Hash{0xbb}
	got := TreeHash(
		[]FileEntry{{Name: "f", Mode: 0o644, Hash: bb}},
		[]TreeEntry{{Name: "d", Hash: bb}},
	)
	want := "27cf948d49a0c9e1f6641398f1412e36787ffcdb470d89b3e5e5ec0ab26f2262"
	require.Equal(t, want, hex.EncodeToString(got[:]), "hash must be stable across builds")
}

func TestCommitHash_Deterministic(t *testing.T) {
	tree := domain.Hash{0x01}
	parent := domain.Hash{0x02}
	c1 := CommitHash(tree, []domain.Hash{parent}, "hi")
	c2 := CommitHash(tree, []domain.Hash{parent}, "hi")
	require.Equal(t, c1, c2)
	require.NotEqual(t, c1, CommitHash(tree, nil, "hi"))
	require.NotEqual(t, c1, CommitHash(tree, []domain.Hash{parent}, "hi2"))
	require.NotEqual(t, c1, CommitHash(tree, []domain.Hash{{0x09}}, "hi"))
}

func TestCommitHash_FirstCommit(t *testing.T) {
	tree := domain.Hash{0x01}
	c1 := CommitHash(tree, nil, "first")
	c2 := CommitHash(tree, nil, "first")
	require.Equal(t, c1, c2)
}

func TestCommitHash_Golden(t *testing.T) {
	cases := []struct {
		tree    domain.Hash
		parents []domain.Hash
		message string
		want    string
	}{
		{domain.Hash{}, nil, "", "352bd266dae53c6e6a29244011cfa029813d0ab8434b2a2b830a487d882832ba"},
		{domain.Hash{0x01}, nil, "initial", "5b84e4fd96e0444b6d28bbabda020609d8a8ac31ef0303a9f44c5f4a3e6698c4"},
		{domain.Hash{0x02}, []domain.Hash{{0x0a}}, "second", "b1b429850a2a631dfec896c4b0fb846384b3d9943ba985a769e392b219aa4d23"},
		{domain.Hash{0x03}, []domain.Hash{{0x0a}, {0x0b}}, "Merge branch 'feature'\n\nbody", "4be0c80b81ecc77e9e2fd59d64b015ec0d9633cb3d588aa0860e0bdc2a427da6"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, CommitHash(c.tree, c.parents, c.message).String())
		require.Equal(t, c.want, CommitHashWithOrigin(c.tree, c.parents, c.message, "").String())
	}
}

func TestCommitHashWithOrigin(t *testing.T) {
	tree := domain.Hash{0x01}
	parents := []domain.Hash{{0x0a}}
	native := CommitHash(tree, parents, "WIP")
	a := CommitHashWithOrigin(tree, parents, "WIP", "git:"+strings.Repeat("a", 40))
	b := CommitHashWithOrigin(tree, parents, "WIP", "git:"+strings.Repeat("b", 40))
	require.NotEqual(t, native, a)
	require.NotEqual(t, a, b)
	require.Equal(t, a, CommitHashWithOrigin(tree, parents, "WIP", "git:"+strings.Repeat("a", 40)))
	require.NotEqual(t, CommitHashWithOrigin(tree, parents, "WIPg", "it:x"), CommitHashWithOrigin(tree, parents, "WIP", "git:x"))
}

// legacyTreeHash is the serialization before modes were hashed: every file had
// kind 0. Read-write files must still hash exactly like this.
func legacyTreeHash(files []FileEntry, trees []TreeEntry) domain.Hash {
	plain := make([]FileEntry, len(files))
	for i, f := range files {
		plain[i] = FileEntry{Name: f.Name, Hash: f.Hash, Mode: 0o644}
	}
	return TreeHash(plain, trees)
}

func TestTreeHash_ModeChangesHash(t *testing.T) {
	file := func(mode int) []FileEntry { return []FileEntry{{Name: "lib.so", Hash: domain.Hash{0x01}, Mode: mode}} }
	rw := TreeHash(file(0o644), nil)
	exec := TreeHash(file(0o755), nil)
	ro := TreeHash(file(0o444), nil)
	require.NotEqual(t, rw, exec)
	require.NotEqual(t, rw, ro)
	require.NotEqual(t, exec, ro)
	// A mode change deep in a tree reaches the root through the child hash.
	require.NotEqual(t,
		TreeHash(nil, []TreeEntry{{Name: "bin", Hash: rw}}),
		TreeHash(nil, []TreeEntry{{Name: "bin", Hash: exec}}))
}

func TestTreeHash_ModeNormalization(t *testing.T) {
	hashOf := func(mode int) domain.Hash {
		return TreeHash([]FileEntry{{Name: "f", Hash: domain.Hash{0x07}, Mode: mode}}, nil)
	}
	groups := [][]int{
		{0, 0o644, 0o664, 0o600, 644},
		{0o755, 0o775, 0o744, 0o700, 755, 0o111},
		{0o444, 0o400, 444},
	}
	for _, group := range groups {
		for _, mode := range group[1:] {
			require.Equal(t, hashOf(group[0]), hashOf(mode), "mode %#o must hash like %#o", mode, group[0])
		}
	}
}

func TestTreeHash_ReadWriteMatchesLegacyFormat(t *testing.T) {
	files := []FileEntry{
		{Name: "a.txt", Hash: domain.Hash{0x01}, Mode: 0o644},
		{Name: "b.txt", Hash: domain.Hash{0x02}, Mode: 0},
	}
	trees := []TreeEntry{{Name: "sub", Hash: domain.Hash{0x03}}}
	require.Equal(t, legacyTreeHash(files, trees), TreeHash(files, trees))

	files[1].Mode = 0o755
	require.NotEqual(t, legacyTreeHash(files, trees), TreeHash(files, trees))
}

func TestTreeHash_ModeGolden(t *testing.T) {
	bb := domain.Hash{0xbb}
	cases := map[int]string{
		0o644: "27cf948d49a0c9e1f6641398f1412e36787ffcdb470d89b3e5e5ec0ab26f2262",
		0o755: "f80331353fd830d303b98834dca6ff6ca6b597914b560aac3d2c1acb99c76a75",
		0o444: "43d6bb9715a318c3b0a79256faef1c2d84e62e8b6003818da39a2a18d3de3b34",
	}
	for mode, want := range cases {
		got := TreeHash([]FileEntry{{Name: "f", Mode: mode, Hash: bb}}, []TreeEntry{{Name: "d", Hash: bb}})
		require.Equal(t, want, got.String(), "mode %#o", mode)
	}
}
