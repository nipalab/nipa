package provenance

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBomb_HugeZTXt(t *testing.T) {
	payload := bytes.Repeat([]byte{'a'}, 64<<20)
	data := withChunks(tinyPNG(), zTextChunk("parameters", payload))
	require.Less(t, len(data), 1<<20)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	rec, ok, err := Extract("bomb.png", bytes.NewReader(data), Limits{})
	runtime.ReadMemStats(&after)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, rec)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20))
}

func TestBomb_HugeIDATIsSkipped(t *testing.T) {
	huge := pngChunk("IDAT", make([]byte, 32<<20))
	data := withChunks(tinyPNG(), huge, textChunk("parameters", "p\nSteps: 1, Seed: 3"))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	rec, ok, err := Extract("a.png", bytes.NewReader(data), Limits{})
	runtime.ReadMemStats(&after)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(3), *rec.Seed)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20))
}

func TestBomb_DeepCBORClaim(t *testing.T) {
	deep := bytes.Repeat([]byte{0x81}, 100000)
	deep = append(deep, 0)
	store := jumbBox("c2pa", jumbBox("urn:uuid:a", jumbBox("c2pa.claim", rawBox("cbor", deep))))
	_, ok, err := Extract("a.png", bytes.NewReader(withChunks(tinyPNG(), pngChunk("caBX", store))), Limits{})
	require.False(t, ok)
	require.ErrorContains(t, err, "nesting deeper")
}

func TestBomb_CBORDeclaredCountsDoNotPreallocate(t *testing.T) {
	data := make([]byte, 0, 1<<20)
	for range 32 {
		data = append(data, 0x9a, 0x00, 0x0f, 0x00, 0x00)
	}
	data = append(data, make([]byte, 1<<20-len(data))...)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := decodeCBOR(data, 32)
	runtime.ReadMemStats(&after)
	require.Error(t, err)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20))
}

func TestBomb_ManyCompressedTextChunks(t *testing.T) {
	chunk := zTextChunk("junk", bytes.Repeat([]byte{'a'}, 1<<20))
	chunks := make([][]byte, 0, 200)
	for range 200 {
		chunks = append(chunks, chunk)
	}
	data := withChunks(tinyPNG(), chunks...)
	require.Less(t, len(data), 1<<20)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, ok, err := Extract("many.png", bytes.NewReader(data), Limits{})
	runtime.ReadMemStats(&after)
	require.False(t, ok)
	require.ErrorContains(t, err, "extraction budget")
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(128<<20))
}
