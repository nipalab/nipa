package diff

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestLoadContentLimit(t *testing.T) {
	h1, h2, h3 := serverDomain.Hash{1}, serverDomain.Hash{2}, serverDomain.Hash{3}
	chunks := map[serverDomain.Hash][]byte{h1: []byte("0123"), h2: []byte("4567"), h3: []byte("89")}
	var loaded []serverDomain.Hash
	load := func(h serverDomain.Hash) ([]byte, error) {
		loaded = append(loaded, h)
		if data, ok := chunks[h]; ok {
			return data, nil
		}
		return nil, assertErr{}
	}
	e := Entry{Path: "f", SizeBytes: 10, ChunkHashes: []serverDomain.Hash{h1, h2, h3}}

	for _, tt := range []struct {
		limit  int64
		want   string
		chunks int
	}{
		{-1, "0123456789", 3},
		{10, "0123456789", 3},
		{20, "0123456789", 3},
		{4, "01234", 2},
		{3, "0123", 1},
		{0, "0", 1},
	} {
		loaded = nil
		got, err := LoadContentLimit(load, e, tt.limit)
		require.NoError(t, err, tt.limit)
		require.Equal(t, tt.want, string(got), tt.limit)
		require.Len(t, loaded, tt.chunks, "limit %d stops after the chunk that crosses it", tt.limit)
	}

	// A size that understates the content is still capped.
	got, err := LoadContentLimit(load, Entry{Path: "f", SizeBytes: 2, ChunkHashes: e.ChunkHashes}, 5)
	require.NoError(t, err)
	require.Equal(t, "012345", string(got))

	// A short file below the limit comes back whole.
	got, err = LoadContentLimit(load, Entry{Path: "f", SizeBytes: 20, ChunkHashes: []serverDomain.Hash{h3}}, 5)
	require.NoError(t, err)
	require.Equal(t, "89", string(got))

	_, err = LoadContentLimit(load, Entry{Path: "f", SizeBytes: 20, ChunkHashes: []serverDomain.Hash{{9}}}, 5)
	require.Error(t, err)
	_, err = LoadContentLimit(load, Entry{Path: "f", SizeBytes: 20, Encoding: "zstd", ChunkHashes: []serverDomain.Hash{h1}}, 5)
	require.Error(t, err, "a chunk that does not decode")
}

func TestTruncatedFileDiff_IsOmitted(t *testing.T) {
	f := FileDiff{
		Change: Change{Path: "big.txt", Status: Modified,
			Old: Entry{Path: "big.txt", Hash: serverDomain.Hash{1}, Mode: 2},
			New: Entry{Path: "big.txt", Hash: serverDomain.Hash{2}, Mode: 2}},
		Old:       []byte("same\nold\n"),
		New:       []byte("same\nnew\nmore\n"),
		Truncated: true,
	}
	res := FileHunksBudget(f, Options{}, DefaultBudget)
	require.True(t, res.Omitted)
	require.True(t, res.Approx)
	require.Empty(t, res.Hunks)
	require.Equal(t, 2, res.Additions)
	require.Equal(t, 1, res.Deletions)
	require.Nil(t, FileHunks(f, Options{}))

	patch := FilePatch(f, Options{})
	require.Equal(t, []string{"diff --nipa a/big.txt b/big.txt", contentTooLarge}, patch)

	// Whitespace filtering cannot judge a prefix, so the file is kept.
	f.New = []byte("same\nold \n")
	require.Len(t, FilterIgnored([]FileDiff{f}, Options{IgnoreAllSpace: true}), 1)
	f.Truncated = false
	require.Empty(t, FilterIgnored([]FileDiff{f}, Options{IgnoreAllSpace: true}))
	require.False(t, strings.Contains(strings.Join(FilePatch(f, Options{}), "\n"), contentTooLarge))
}
