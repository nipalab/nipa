package chunker

import (
	"bytes"
	"encoding/hex"
	"io"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
)

func TestSumVectors(t *testing.T) {
	empty := Sum(nil)
	require.Equal(t,
		"af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262",
		hex.EncodeToString(empty[:]),
		"blake3 of empty input")
	abc := Sum([]byte("abc"))
	require.Equal(t,
		"6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85",
		hex.EncodeToString(abc[:]),
		"blake3 of abc")
}

func TestFileHash(t *testing.T) {
	h1 := domain.Hash{0x01}
	h2 := domain.Hash{0x02}
	h3 := domain.Hash{0x03}

	got := FileHash([]domain.Hash{h1, h2, h3})
	want := Sum(append(append(h1[:], h2[:]...), h3[:]...))
	require.Equal(t, want, got)

	require.NotEqual(t, FileHash([]domain.Hash{h1, h2}), FileHash([]domain.Hash{h2, h1}), "chunk order must matter")
	require.Equal(t, FileHash([]domain.Hash{h1, h2}), FileHash([]domain.Hash{h1, h2}), "same input, same output")
}

func TestChunkAllReconstruction(t *testing.T) {
	sizes := []int{0, 1, 15, 16, 17, 4095, 4096, 16384, 65536, 65537, 1 << 17, 1<<20 + 1234}
	seeds := []int64{1, 42, 1337}
	for _, size := range sizes {
		for _, seed := range seeds {
			data := randData(size, seed)
			chunks, err := ChunkAll(data)
			require.NoError(t, err)
			if size == 0 {
				require.Empty(t, chunks, "empty input must produce no chunks")
				continue
			}
			joined := joinChunks(chunks)
			require.Equal(t, data, joined, "chunks must reassemble to the original input (size %d seed %d)", size, seed)
			assertChunkBounds(t, chunks)
		}
	}
}

func TestChunkAllLowEntropy(t *testing.T) {
	for _, data := range [][]byte{
		make([]byte, 1<<20),
		bytes.Repeat([]byte("a"), 1<<20),
		bytes.Repeat([]byte("abcdef"), 1<<18),
	} {
		chunks, err := ChunkAll(data)
		require.NoError(t, err)
		require.NotEmpty(t, chunks)
		require.Equal(t, data, joinChunks(chunks))
		assertChunkBounds(t, chunks)
	}
}

func TestChunkStreamingMatchesInMemory(t *testing.T) {
	sizes := []int{1, 17, 1 << 17, 1 << 20}
	widths := []int{1, 7, 65536}
	for _, size := range sizes {
		data := randData(size, 7)
		chunks, err := ChunkAll(data)
		require.NoError(t, err)
		for _, width := range widths {
			r := &tinyReader{data: data, width: width}
			s, err := NewSplitter(r)
			require.NoError(t, err)
			var streamed []Chunk
			for {
				c, err := s.Next()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				streamed = append(streamed, *c)
			}
			require.Len(t, streamed, len(chunks), "same chunk count via %d-byte reads (size %d)", width, size)
			for i := range streamed {
				require.Equal(t, chunks[i].Hash, streamed[i].Hash, "chunk %d hash mismatch (size %d, read width %d)", i, size, width)
				require.Equal(t, chunks[i].Data, streamed[i].Data)
			}
		}
	}
}

func TestDeterminism(t *testing.T) {
	data := randData(1<<18, 99)
	a, err := ChunkAll(data)
	require.NoError(t, err)
	b, err := ChunkAll(data)
	require.NoError(t, err)
	require.Equal(t, a, b)
}

func TestCustomConfig(t *testing.T) {
	cfg := Config{Avg: 1 << 20}
	sanitized := Config{Min: cfg.Avg / 4, Avg: cfg.Avg, Max: cfg.Avg * 4}
	data := randData(5<<20, 3)
	chunks, err := ChunkAllWith(cfg, data)
	require.NoError(t, err)
	require.Equal(t, data, joinChunks(chunks))
	assertChunkBoundsWith(t, chunks, sanitized)
}

func TestConfigValidation(t *testing.T) {
	cases := []Config{
		{Min: 1, Avg: 1, Max: 1},
		{Min: 64, Avg: 32, Max: 256},
		{Min: 64, Avg: 128, Max: 128},
		{Min: 64, Avg: 100, Max: 400},
		{Min: 8, Avg: 64, Max: 256},
	}
	for _, cfg := range cases {
		_, err := NewSplitter(nil, cfg)
		require.Error(t, err, "config %+v must be rejected", cfg)
	}
	valid, err := NewSplitter(nil)
	require.NoError(t, err)
	require.NotNil(t, valid)
}

func TestSplitterNoConfig(t *testing.T) {
	s, err := NewSplitter(bytes.NewReader([]byte("hello world")))
	require.NoError(t, err)
	c, err := s.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("hello world"), c.Data)
	require.Equal(t, Sum([]byte("hello world")), c.Hash)
	_, err = s.Next()
	require.Equal(t, io.EOF, err)
}

func TestIsBinary(t *testing.T) {
	require.False(t, IsBinary(nil))
	require.False(t, IsBinary([]byte("plain text file\nwith no nul bytes\n")))
	require.False(t, IsBinary(bytes.Repeat([]byte("x"), binaryProbeSize+100)))
	require.True(t, IsBinary([]byte{0x00}))
	require.True(t, IsBinary(append([]byte("PNG header "), 0x00)))
	probe := bytes.Repeat([]byte("x"), binaryProbeSize+100)
	probe[binaryProbeSize+50] = 0x00
	require.False(t, IsBinary(probe), "nuls past the probe window are ignored")
}

func assertChunkBounds(t *testing.T, chunks []Chunk) {
	assertChunkBoundsWith(t, chunks, DefaultConfig)
}

func assertChunkBoundsWith(t *testing.T, chunks []Chunk, cfg Config) {
	t.Helper()
	if len(chunks) < 2 {
		return
	}
	for i, c := range chunks[:len(chunks)-1] {
		size := int64(len(c.Data))
		require.GreaterOrEqual(t, size, cfg.Min, "chunk %d shorter than min", i)
		require.LessOrEqual(t, size, cfg.Max, "chunk %d longer than max", i)
	}
}

func joinChunks(chunks []Chunk) []byte {
	var w bytes.Buffer
	for _, c := range chunks {
		w.Write(c.Data)
	}
	return w.Bytes()
}

func randData(n int, seed int64) []byte {
	data := make([]byte, n)
	r := rand.New(rand.NewSource(seed))
	_, _ = r.Read(data)
	return data
}

type tinyReader struct {
	data  []byte
	width int
	pos   int
}

func (r *tinyReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := len(r.data) - r.pos
	if n > r.width {
		n = r.width
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}
