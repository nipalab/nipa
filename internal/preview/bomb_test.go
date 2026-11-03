package preview

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func allocatedBy(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestPSDRLECountTableBomb(t *testing.T) {
	spec := psdSpec{version: 2, channels: psdMaxChannels, depth: 8, mode: psdModeRGB, w: 1, h: MaxPixels, compression: psdCompressRLE}
	data := spec.bytes()
	var err error
	alloc := allocatedBy(func() { _, err = DecodeImage("a.psb", data) })
	require.ErrorIs(t, err, errPSDTruncated)
	require.Less(t, alloc, uint64(64<<20))
}

func TestPSDRLESkipsExtraChannels(t *testing.T) {
	planes := [][]byte{{10, 20}, {30, 40}, {50, 60}, {1, 2}, {3, 4}, {5, 6}, {7, 8}}
	for _, version := range []int{1, 2} {
		spec := psdSpec{version: version, channels: len(planes), depth: 8, mode: psdModeRGB, w: 2, h: 1, compression: psdCompressRLE, planes: planes}
		img, err := DecodeImage("a.psd", spec.bytes())
		require.NoError(t, err)
		require.Equal(t, []uint8{10, 30, 50}, img.Pixels.Pix[0:3])
		require.Equal(t, []uint8{20, 40, 60}, img.Pixels.Pix[4:7])

		data := spec.bytes()
		_, err = DecodeImage("a.psd", data[:len(data)-len(planes)*3-(len(planes)-psdDecodedPlanes)*2])
		require.Error(t, err)
	}
}

func TestEXRBlockBytesBomb(t *testing.T) {
	chs := []exrChannel{{name: "Y", kind: exrFloat}}
	for i := range (exrMaxBlockBytes / (4096 * 4)) + 1 {
		chs = append(chs, exrChannel{name: fmt.Sprintf("c%d", i), kind: exrFloat})
	}
	data := exrFile(0, exrAttrs(1, 4096, 1, chs...), [][]byte{exrScanline(0, []byte{0, 0})})
	var err error
	alloc := allocatedBy(func() { _, err = DecodeImage("a.exr", data) })
	require.ErrorIs(t, err, ErrTooLarge)
	require.Less(t, alloc, uint64(64<<20))
}

func TestEXRRLEDoesNotPreallocateLimit(t *testing.T) {
	out := exrRLE([]byte{1, 7}, 1<<30)
	require.Equal(t, []byte{7, 7}, out)
	require.LessOrEqual(t, cap(out), 64)
}

func TestKTX2InflatesOnlySelectedSurface(t *testing.T) {
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write([]byte{1, 2, 3, 4})
	_, _ = zw.Write(make([]byte, 64<<20))
	require.NoError(t, zw.Close())
	data := ktx2Spec{vk: 37, w: 1, h: 1, faces: 1, scheme: 3, levels: [][]byte{z.Bytes()}, raw: []uint64{4 + 64<<20}}.bytes()
	var (
		img *Image
		err error
	)
	alloc := allocatedBy(func() { img, err = DecodeImage("a.ktx2", data) })
	require.NoError(t, err)
	require.Equal(t, []uint8{1, 2, 3, 4}, img.Pixels.Pix)
	require.Less(t, alloc, uint64(16<<20))
	require.False(t, errors.Is(err, ErrTooLarge))
}

func radianceSolid(w, h int, header string) []byte {
	b := []byte("#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n\n" + header + "\n")
	for range h {
		b = append(b, 2, 2, byte(w>>8), byte(w))
		for c := range 4 {
			for x := 0; x < w; {
				n := min(127, w-x)
				b = append(b, byte(128+n), byte(100+c))
				x += n
			}
		}
	}
	return b
}

func TestRadianceDecodesScanlinesInPlace(t *testing.T) {
	const w, h = 1024, 512
	data := radianceSolid(w, h, fmt.Sprintf("-Y %d +X %d", h, w))
	var (
		img *Image
		err error
	)
	alloc := allocatedBy(func() { img, err = DecodeImage("a.hdr", data) })
	require.NoError(t, err)
	require.Equal(t, w, img.Width())
	require.Equal(t, img.HDR.Pix[:4], img.HDR.Pix[len(img.HDR.Pix)-4:])
	require.Less(t, alloc, uint64(w*h*20+1<<20))
}

func TestRadianceRejectsShortDataBeforeAllocating(t *testing.T) {
	data := radianceSolid(8, 1, fmt.Sprintf("-Y %d +X %d", MaxPixels/16, 8))
	var err error
	alloc := allocatedBy(func() { _, err = DecodeImage("a.hdr", data) })
	require.ErrorIs(t, err, errHDRTruncated)
	require.Less(t, alloc, uint64(1<<20))
}
