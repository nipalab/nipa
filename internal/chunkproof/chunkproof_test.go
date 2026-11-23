package chunkproof

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
)

func TestVerifierMatchesSum(t *testing.T) {
	for _, size := range []int{0, 1, 63, 64 << 10, 1<<20 + 17} {
		data := make([]byte, size)
		_, err := rand.Read(data)
		require.NoError(t, err)

		v := NewVerifier()
		// Feed in uneven pieces, as a streamed object read would.
		for rest := data; len(rest) > 0; {
			n := min(len(rest), 1+len(rest)/3)
			written, err := v.Write(rest[:n])
			require.NoError(t, err)
			require.Equal(t, n, written)
			rest = rest[n:]
		}
		require.Equal(t, int64(size), v.Size())
		require.Equal(t, chunker.Sum(data), v.Sum())
	}
}

func TestProtocolVersion(t *testing.T) {
	require.Equal(t, uint32(4), Protocol, "bumping the chunk protocol is a breaking change for every client")
}

func FuzzVerifierMatchesSum(f *testing.F) {
	f.Add([]byte("chunk bytes"), uint8(3))
	f.Add([]byte{}, uint8(0))
	f.Add(bytes.Repeat([]byte{0}, 4096), uint8(200))
	f.Fuzz(func(t *testing.T, data []byte, split uint8) {
		v := NewVerifier()
		cut := 0
		if len(data) > 0 {
			cut = int(split) % (len(data) + 1)
		}
		_, _ = v.Write(data[:cut])
		_, _ = v.Write(data[cut:])
		if v.Size() != int64(len(data)) {
			t.Fatalf("size %d, want %d", v.Size(), len(data))
		}
		if v.Sum() != chunker.Sum(data) {
			t.Fatal("streamed hash differs from chunker.Sum")
		}
	})
}
