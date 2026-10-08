package merge

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

func chunkedFile(t *testing.T, name string, content []byte, mode int) *domain.File {
	t.Helper()
	chunks, err := chunker.ChunkAll(content)
	require.NoError(t, err)
	domainChunks := make([]domain.Chunk, len(chunks))
	for i, c := range chunks {
		domainChunks[i] = domain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
	}
	return &domain.File{
		Name:      name,
		Mode:      mode,
		SizeBytes: int64(len(content)),
		Chunks:    domainChunks,
	}
}

func TestFlatten_NilRoot(t *testing.T) {
	require.Empty(t, Flatten(nil))
}

func TestFlatten_RootFile(t *testing.T) {
	content := []byte("hello world, this is file content")
	chunked := chunkedFile(t, "readme.txt", content, 0o644)
	hashes := make([]domain.Hash, len(chunked.Chunks))
	expectedSizes := make([]int64, len(chunked.Chunks))
	for i, c := range chunked.Chunks {
		hashes[i] = c.Hash
		expectedSizes[i] = c.SizeBytes
	}

	root := &domain.TreeNode{Name: "root", Mode: 0o040000, FileChildren: []*domain.File{chunked}}
	got := Flatten(root)

	require.Len(t, got, 1)
	f := got["readme.txt"]
	require.Equal(t, "readme.txt", f.Path)
	require.Equal(t, 0o644, f.Mode)
	require.Equal(t, int64(len(content)), f.SizeBytes)
	require.Equal(t, chunker.FileHash(hashes), f.Hash)
	require.Equal(t, hashes, f.ChunkHashes)
	require.Equal(t, expectedSizes, f.ChunkSizes)
}

func nodeWith(name string, files ...*domain.File) *domain.TreeNode {
	return &domain.TreeNode{Name: name, Mode: 0o040000, FileChildren: files}
}

func TestFlatten_NestedTrees(t *testing.T) {
	content := []byte("sprite data")
	chunked := chunkedFile(t, "sprite.png", content, 0o644)
	root := &domain.TreeNode{
		Name:         "root",
		Mode:         0o040000,
		TreeChildren: []*domain.TreeNode{nodeWith("assets", chunked)},
	}

	got := Flatten(root)

	require.Len(t, got, 1)
	f := got["assets/sprite.png"]
	require.Equal(t, "assets/sprite.png", f.Path)
	require.Equal(t, chunked.SizeBytes, f.SizeBytes)
}

func TestFlatten_NilChildNode(t *testing.T) {
	content := []byte("x")
	chunked := chunkedFile(t, "a.txt", content, 0o644)
	root := &domain.TreeNode{
		Name:         "root",
		Mode:         0o040000,
		FileChildren: []*domain.File{chunked},
		TreeChildren: []*domain.TreeNode{nil},
	}

	got := Flatten(root)

	require.Len(t, got, 1)
	require.Contains(t, got, "a.txt")
}

func TestFlatten_EmptyChunks(t *testing.T) {
	empty := &domain.File{Name: "empty.bin", Mode: 0o644, SizeBytes: 0}
	root := &domain.TreeNode{Name: "root", Mode: 0o040000, FileChildren: []*domain.File{empty}}

	got := Flatten(root)

	f := got["empty.bin"]
	require.Empty(t, f.ChunkHashes)
	require.Equal(t, chunker.FileHash(nil), f.Hash)
}
