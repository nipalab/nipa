package diff

import (
	"errors"
	"testing"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/stretchr/testify/require"
)

func hashOf(data string) serverDomain.Hash {
	return chunker.Sum([]byte(data))
}

func testEntry(path string, mode int, data string) Entry {
	h := hashOf(data)
	return Entry{
		Path:        path,
		Mode:        mode,
		SizeBytes:   int64(len(data)),
		Hash:        h,
		ChunkHashes: []serverDomain.Hash{h},
		ChunkSizes:  []int64{int64(len(data))},
	}
}

func TestStatus_String(t *testing.T) {
	require.Equal(t, "A", Added.String())
	require.Equal(t, "M", Modified.String())
	require.Equal(t, "D", Deleted.String())
	require.Equal(t, "?", Status(0).String())
}

func TestCompare_Mixed(t *testing.T) {
	oldMap := map[string]Entry{
		"same.txt":     testEntry("same.txt", 2, "same"),
		"changed.txt":  testEntry("changed.txt", 2, "old content"),
		"removed.txt":  testEntry("removed.txt", 2, "gone"),
		"modeonly.txt": testEntry("modeonly.txt", 2, "content"),
	}
	newMap := map[string]Entry{
		"same.txt":     testEntry("same.txt", 2, "same"),
		"changed.txt":  testEntry("changed.txt", 2, "new content"),
		"added.txt":    testEntry("added.txt", 2, "fresh"),
		"modeonly.txt": testEntry("modeonly.txt", 3, "content"),
	}
	changes := Compare(oldMap, newMap)
	require.Len(t, changes, 4)
	// sorted by path
	require.Equal(t, "added.txt", changes[0].Path)
	require.Equal(t, Added, changes[0].Status)
	require.Equal(t, newMap["added.txt"], changes[0].New)
	require.Equal(t, "changed.txt", changes[1].Path)
	require.Equal(t, Modified, changes[1].Status)
	require.Equal(t, oldMap["changed.txt"], changes[1].Old)
	require.Equal(t, newMap["changed.txt"], changes[1].New)
	require.Equal(t, "modeonly.txt", changes[2].Path)
	require.Equal(t, Modified, changes[2].Status)
	require.Equal(t, "removed.txt", changes[3].Path)
	require.Equal(t, Deleted, changes[3].Status)
	require.Equal(t, oldMap["removed.txt"], changes[3].Old)
}

func TestCompare_Empty(t *testing.T) {
	require.Empty(t, Compare(nil, nil))
	require.Empty(t, Compare(map[string]Entry{}, map[string]Entry{}))
	changes := Compare(nil, map[string]Entry{"a.txt": testEntry("a.txt", 2, "x")})
	require.Len(t, changes, 1)
	require.Equal(t, Added, changes[0].Status)
}

func TestFromTree(t *testing.T) {
	fileHash := func(data string) serverDomain.Hash {
		return chunker.FileHash([]serverDomain.Hash{hashOf(data)})
	}
	root := &serverDomain.TreeNode{
		Name: "root",
		FileChildren: []*serverDomain.File{
			{Name: "top.txt", Mode: 2, SizeBytes: 3, Chunks: []serverDomain.Chunk{{Hash: hashOf("top"), SizeBytes: 3}}},
		},
		TreeChildren: []*serverDomain.TreeNode{
			{
				Name: "sub",
				FileChildren: []*serverDomain.File{
					{Name: "inner.txt", Mode: 3, IsBinary: true, SizeBytes: 5, Chunks: []serverDomain.Chunk{{Hash: hashOf("inner"), SizeBytes: 5}}},
				},
			},
			nil,
		},
	}
	got := FromTree(root)
	require.Len(t, got, 2)
	top := got["top.txt"]
	require.Equal(t, 2, top.Mode)
	require.Equal(t, int64(3), top.SizeBytes)
	require.Equal(t, fileHash("top"), top.Hash)
	require.Equal(t, []serverDomain.Hash{hashOf("top")}, top.ChunkHashes)
	require.Equal(t, []int64{3}, top.ChunkSizes)
	inner := got["sub/inner.txt"]
	require.Equal(t, 3, inner.Mode)
	require.True(t, inner.IsBinary)
	require.Equal(t, fileHash("inner"), inner.Hash)
}

func TestFromTree_Nil(t *testing.T) {
	require.Empty(t, FromTree(nil))
}

func TestLoadContent(t *testing.T) {
	e := testEntry("f.txt", 2, "hello")
	e.ChunkHashes = []serverDomain.Hash{hashOf("hel"), hashOf("lo")}
	got, err := LoadContent(func(h serverDomain.Hash) ([]byte, error) {
		if h == hashOf("hel") {
			return []byte("hel"), nil
		}
		return []byte("lo"), nil
	}, e)
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), got)
}

func TestLoadContent_Empty(t *testing.T) {
	got, err := LoadContent(func(serverDomain.Hash) ([]byte, error) {
		return nil, errors.New("must not be called")
	}, Entry{Path: "empty.txt"})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestLoadContent_Error(t *testing.T) {
	wantErr := errors.New("chunk content not stored")
	_, err := LoadContent(func(serverDomain.Hash) ([]byte, error) {
		return nil, wantErr
	}, testEntry("f.txt", 2, "x"))
	require.ErrorIs(t, err, wantErr)
	require.Contains(t, err.Error(), "f.txt")
}
