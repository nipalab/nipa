package preview

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func readGzip(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.ReadFile(filepath.Join("testdata", "bcn", name))
	require.NoError(t, err)
	r, err := gzip.NewReader(bytes.NewReader(f))
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return data
}

func refFloats(data []byte) []float32 {
	out := make([]float32, len(data)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return out
}

func near(t *testing.T, want, got, tolerance int, msg string, args ...any) {
	t.Helper()
	if d := want - got; d > tolerance || -d > tolerance {
		t.Fatalf(msg+": want %d got %d", append(args, want, got)...)
	}
}

func TestBC1To3And7MatchReference(t *testing.T) {
	for name, tc := range map[string]struct {
		size int
		fn   ldrBlock
		tol  int
	}{
		"bc1": {8, decodeBC1, 1},
		"bc2": {16, decodeBC2, 1},
		"bc3": {16, decodeBC3, 1},
		"bc7": {16, decodeBC7, 0},
	} {
		t.Run(name, func(t *testing.T) {
			blocks, ref := readGzip(t, name+".bin.gz"), readGzip(t, name+".ref.gz")
			var px [16][4]uint8
			for k := 0; k*tc.size < len(blocks); k++ {
				tc.fn(blocks[k*tc.size:(k+1)*tc.size], &px)
				for i := range 16 {
					for c := range 4 {
						near(t, int(ref[k*64+i*4+c]), int(px[i][c]), tc.tol, "block %d texel %d channel %d", k, i, c)
					}
				}
			}
		})
	}
}

func TestBC4And5MatchReference(t *testing.T) {
	for _, tc := range []struct {
		name     string
		size, ch int
		fn       ldrBlock
		signed   bool
	}{
		{"bc4", 8, 1, decodeBC4(false), false},
		{"bc4s", 8, 1, decodeBC4(true), true},
		{"bc5", 16, 2, decodeBC5(false), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocks, ref := readGzip(t, tc.name+".bin.gz"), refFloats(readGzip(t, tc.name+".ref.gz"))
			var px [16][4]uint8
			for k := 0; k*tc.size < len(blocks); k++ {
				tc.fn(blocks[k*tc.size:(k+1)*tc.size], &px)
				for i := range 16 {
					for c := range tc.ch {
						v := float64(ref[(k*16+i)*tc.ch+c])
						if tc.signed {
							v = (v + 1) / 2
						}
						near(t, int(math.Round(v*255)), int(px[i][c]), 1, "block %d texel %d channel %d", k, i, c)
					}
				}
			}
		})
	}
}

func TestBC5ReconstructsNormalZ(t *testing.T) {
	block := []byte{128, 128, 0, 0, 0, 0, 0, 0, 128, 128, 0, 0, 0, 0, 0, 0}
	var px [16][4]uint8
	decodeBC5(false)(block, &px)
	require.Equal(t, [4]uint8{128, 128, 255, 255}, px[0])
	decodeBC5(true)([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, &px)
	require.Equal(t, [4]uint8{128, 128, 255, 255}, px[5])
}

func TestBC6HMatchesReference(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signed bool
	}{{"bc6h", false}, {"bc6hs", true}} {
		t.Run(tc.name, func(t *testing.T) {
			blocks, ref := readGzip(t, tc.name+".bin.gz"), refFloats(readGzip(t, tc.name+".ref.gz"))
			fn := decodeBC6H(tc.signed)
			var px [16][3]float32
			for k := 0; k*16 < len(blocks); k++ {
				fn(blocks[k*16:(k+1)*16], &px)
				for i := range 16 {
					for c := range 3 {
						want := ref[(k*16+i)*3+c]
						if math.Float32bits(want) != math.Float32bits(px[i][c]) && !(math.IsNaN(float64(want)) && math.IsNaN(float64(px[i][c]))) {
							t.Fatalf("block %d texel %d channel %d: want %v got %v", k, i, c, want, px[i][c])
						}
					}
				}
			}
		})
	}
}
