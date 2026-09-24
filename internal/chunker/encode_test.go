package chunker

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func encodeAll(t *testing.T, data []byte, path string, isBinary bool, encoding string) (string, []EncodedChunk) {
	t.Helper()
	enc, chunks, err := EncodeBytes(data, path, isBinary, encoding)
	require.NoError(t, err)
	return enc, chunks
}

func TestEncode_TextSingleBlob(t *testing.T) {
	content := []byte("hello compressed world")
	encoding, chunks := encodeAll(t, content, "a.txt", false, "")

	require.Equal(t, EncodingZstd, encoding)
	require.Len(t, chunks, 1)
	require.Equal(t, Sum(chunks[0].Data), chunks[0].Hash)
	require.Equal(t, int64(len(chunks[0].Data)), chunks[0].SizeBytes)

	decoded, err := Decode(encoding, chunks[0].Data)
	require.NoError(t, err)
	require.Equal(t, content, decoded)
}

func TestEncode_TextIsDeterministic(t *testing.T) {
	content := bytes.Repeat([]byte("deterministic content\n"), 500)
	_, first := encodeAll(t, content, "a.txt", false, "")
	_, second := encodeAll(t, content, "a.txt", false, "")

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.Equal(t, first[0].Hash, second[0].Hash)
	require.Less(t, len(first[0].Data), len(content))
}

func TestEncode_TextLargeFallsBackToChunks(t *testing.T) {
	content := bytes.Repeat([]byte("0123456789abcdef"), (TextSingleBlobRawMax/16)+1024)
	encoding, chunks := encodeAll(t, content, "scene.unity", false, "")

	require.Equal(t, EncodingZstd, encoding)
	require.Greater(t, len(chunks), 1)

	var rebuilt []byte
	for _, c := range chunks {
		require.Equal(t, Sum(c.Data), c.Hash)
		decoded, err := Decode(encoding, c.Data)
		require.NoError(t, err)
		rebuilt = append(rebuilt, decoded...)
	}
	require.Equal(t, content, rebuilt)
}

func TestEncode_BinaryStaysRaw(t *testing.T) {
	content := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	encoding, chunks := encodeAll(t, content, "data.bin", true, "")

	require.Equal(t, EncodingRaw, encoding)
	require.Len(t, chunks, 1)
	require.Equal(t, content, chunks[0].Data)
	require.Equal(t, Sum(content), chunks[0].Hash)

	decoded, err := Decode(encoding, chunks[0].Data)
	require.NoError(t, err)
	require.Equal(t, content, decoded)
}

func TestEncode_RawEncodingKeepsTextUncompressed(t *testing.T) {
	content := []byte("legacy text stored raw")
	encoding, chunks := encodeAll(t, content, "a.txt", false, EncodingRaw)

	require.Equal(t, EncodingRaw, encoding)
	require.Len(t, chunks, 1)
	require.Equal(t, content, chunks[0].Data)
	require.Equal(t, Sum(content), chunks[0].Hash)
}

func TestEncode_EmptyTextHasNoChunks(t *testing.T) {
	encoding, chunks := encodeAll(t, nil, "empty.txt", false, "")
	require.Equal(t, EncodingZstd, encoding)
	require.Empty(t, chunks)
}

func TestNormalizeEncoding(t *testing.T) {
	encoding, ok := NormalizeEncoding("")
	require.True(t, ok)
	require.Equal(t, EncodingRaw, encoding)

	encoding, ok = NormalizeEncoding(EncodingRaw)
	require.True(t, ok)
	require.Equal(t, EncodingRaw, encoding)

	encoding, ok = NormalizeEncoding(EncodingZstd)
	require.True(t, ok)
	require.Equal(t, EncodingZstd, encoding)

	_, ok = NormalizeEncoding("bogus")
	require.False(t, ok)
}

func TestDecode_RawPassthrough(t *testing.T) {
	data := []byte("plain")
	decoded, err := Decode(EncodingRaw, data)
	require.NoError(t, err)
	require.Equal(t, data, decoded)
}

func TestDecode_InvalidZstd(t *testing.T) {
	_, err := Decode(EncodingZstd, []byte("not a zstd frame"))
	require.Error(t, err)
}
