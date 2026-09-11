package treehash

import (
	"encoding/hex"
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
		[]FileEntry{{Name: "f", Mode: 1, Hash: bb}},
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
	require.NotEqual(t, c1, CommitHash(tree, []domain.Hash{domain.Hash{0x09}}, "hi"))
}

func TestCommitHash_FirstCommit(t *testing.T) {
	tree := domain.Hash{0x01}
	c1 := CommitHash(tree, nil, "first")
	c2 := CommitHash(tree, nil, "first")
	require.Equal(t, c1, c2)
}
