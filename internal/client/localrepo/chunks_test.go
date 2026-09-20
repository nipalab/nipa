package localrepo

import (
	"testing"

	"github.com/stretchr/testify/require"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestEncodeDecodeChunkHashes(t *testing.T) {
	hashes := []serverDomain.Hash{{0x01}, {0xff}}
	encoded := encodeChunkHashes(hashes)
	require.Len(t, encoded, 2*chunkHashSize)

	decoded, err := decodeChunkHashes(encoded)
	require.NoError(t, err)
	require.Equal(t, hashes, decoded)

	empty, err := decodeChunkHashes(nil)
	require.NoError(t, err)
	require.Empty(t, empty)

	_, err = decodeChunkHashes([]byte{0x01, 0x02})
	require.Error(t, err)
}

func TestSaveTree_StoresChunkHashes(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.SaveTree(treeFixture()))

	snapshot, err := lr.Snapshot()
	require.NoError(t, err)
	require.Len(t, snapshot.Files, 1)
	require.Equal(t, "assets/logo.png", snapshot.Files[0].Path)
	require.Equal(t, []serverDomain.Hash{{0x04}}, snapshot.Files[0].Chunks)
}
