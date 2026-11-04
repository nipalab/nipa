package preview

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFloatDecoderDropsNonFinite(t *testing.T) {
	for _, width := range []int{4, 8} {
		dec := floatDecoder(width, binary.LittleEndian)
		for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			b := make([]byte, width)
			if width == 4 {
				binary.LittleEndian.PutUint32(b, math.Float32bits(float32(v)))
			} else {
				binary.LittleEndian.PutUint64(b, math.Float64bits(v))
			}
			require.Zero(t, dec(b))
		}
	}
}
