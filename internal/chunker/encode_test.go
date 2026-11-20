package chunker

import (
	"bytes"
	"math/rand"
	"runtime"
	"testing"

	"github.com/klauspost/compress/zstd"
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

func TestDecodeAppend(t *testing.T) {
	out, err := DecodeAppend([]byte("ab"), EncodingRaw, []byte("cd"))
	require.NoError(t, err)
	require.Equal(t, "abcd", string(out))

	_, chunks, err := EncodeBytes([]byte("compressed text payload"), "a.txt", false, "")
	require.NoError(t, err)
	out, err = DecodeAppend([]byte("x:"), EncodingZstd, chunks[0].Data)
	require.NoError(t, err)
	require.Equal(t, "x:compressed text payload", string(out))

	_, err = DecodeAppend(nil, EncodingZstd, []byte("not a zstd frame"))
	require.Error(t, err)
}

// zstdBomb compresses 1 GiB of zeros without holding it in memory. With
// knownSize the frame header declares the size, otherwise it is unknown.
func zstdBomb(t *testing.T, knownSize bool) []byte {
	t.Helper()
	const total = 1 << 30
	var out bytes.Buffer
	w, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	require.NoError(t, err)
	size := int64(-1)
	if knownSize {
		size = total
	}
	w.ResetContentSize(&out, size)
	zeros := make([]byte, 1<<20)
	for range total / len(zeros) {
		_, err = w.Write(zeros)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return out.Bytes()
}

func TestDecodeRejectsZstdBomb(t *testing.T) {
	for name, known := range map[string]bool{"frame content size": true, "unknown content size": false} {
		t.Run(name, func(t *testing.T) {
			bomb := zstdBomb(t, known)
			require.Less(t, len(bomb), 1<<20)
			// Warm the shared decoder so its one-time setup is not measured.
			_, err := Decode(EncodingZstd, zstdEncoder.EncodeAll([]byte("warm up"), nil))
			require.NoError(t, err)

			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, err = Decode(EncodingZstd, bomb)
			runtime.ReadMemStats(&after)
			require.ErrorIs(t, err, zstd.ErrDecoderSizeExceeded)
			require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(64<<20), "a 1 GiB bomb must not be decoded in full")

			_, err = DecodeAppend([]byte("kept"), EncodingZstd, bomb)
			require.ErrorIs(t, err, zstd.ErrDecoderSizeExceeded)
		})
	}
}

func TestDecodeRejectsJustOverCap(t *testing.T) {
	over := zstdEncoder.EncodeAll(make([]byte, MaxDecodedChunkSize+1), nil)
	_, err := Decode(EncodingZstd, over)
	require.ErrorIs(t, err, zstd.ErrDecoderSizeExceeded)
	at, err := Decode(EncodingZstd, zstdEncoder.EncodeAll(make([]byte, MaxDecodedChunkSize), nil))
	require.NoError(t, err)
	require.Len(t, at, MaxDecodedChunkSize)
}

func TestDecodeAppendInPlaceAndEmpty(t *testing.T) {
	data := zstdEncoder.EncodeAll([]byte("payload"), nil)
	dst := make([]byte, 3, 64)
	copy(dst, "pre")
	out, err := DecodeAppend(dst, EncodingZstd, data)
	require.NoError(t, err)
	require.Equal(t, "prepayload", string(out))
	require.Same(t, &dst[0], &out[0], "decoded into the spare capacity")

	out, err = DecodeAppend([]byte("pre"), EncodingZstd, zstdEncoder.EncodeAll(nil, nil))
	require.NoError(t, err)
	require.Equal(t, "pre", string(out))
}

func TestDecodeAcceptsLargestEncodedText(t *testing.T) {
	text := bytes.Repeat([]byte("line of text for the largest single blob\n"), TextSingleBlobRawMax/41+1)[:TextSingleBlobRawMax]
	encoding, chunks, err := EncodeBytes(text, "big.txt", false, "")
	require.NoError(t, err)
	require.Equal(t, EncodingZstd, encoding)
	require.Len(t, chunks, 1, "the whole file is one blob")
	require.EqualValues(t, TextSingleBlobRawMax, chunks[0].RawSize)

	// A large dst prefix does not count against the per-chunk cap.
	prefix := bytes.Repeat([]byte{'p'}, MaxDecodedChunkSize)
	out, err := DecodeAppend(prefix, EncodingZstd, chunks[0].Data)
	require.NoError(t, err)
	require.Equal(t, text, out[len(prefix):])

	decoded, err := Decode(EncodingZstd, chunks[0].Data)
	require.NoError(t, err)
	require.Equal(t, text, decoded)
}

func TestDecodeAcceptsLargeChunkedText(t *testing.T) {
	// Above the single-blob size, text is chunked and every chunk decodes on
	// its own within the cap.
	rng := rand.New(rand.NewSource(1))
	text := make([]byte, TextSingleBlobRawMax+(3<<20))
	for i := range text {
		text[i] = byte('a' + rng.Intn(26))
	}
	encoding, chunks, err := EncodeBytes(text, "big.txt", false, "")
	require.NoError(t, err)
	require.Equal(t, EncodingZstd, encoding)
	require.Greater(t, len(chunks), 1)
	var out []byte
	for _, c := range chunks {
		require.LessOrEqual(t, c.RawSize, TextLargeConfig.Max)
		out, err = DecodeAppend(out, encoding, c.Data)
		require.NoError(t, err)
	}
	require.Equal(t, text, out)
}
