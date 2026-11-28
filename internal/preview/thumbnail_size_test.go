package preview

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTextureThumbnailLevel(t *testing.T) {
	tex := &texture{width: 4096, height: 1024, levels: 13}
	for _, tc := range []struct {
		sel  Select
		size int
		want int
	}{
		{Select{}, 128, 5},  // 128x32
		{Select{}, 129, 4},  // 256x64 still covers 129
		{Select{}, 4096, 0}, // level 0 is the only one that covers it
		{Select{}, 9000, 0}, // never below the selected level
		{Select{Level: 7}, 128, 7},
		{Select{Level: 2}, 1, 12}, // the last level is 1x1
		{Select{Level: 99}, 128, 12},
	} {
		require.Equal(t, tc.want, tex.thumbnailLevel(tc.sel, tc.size), "%+v size %d", tc.sel, tc.size)
	}
	single := &texture{width: 4096, height: 4096, levels: 1}
	require.Equal(t, 0, single.thumbnailLevel(Select{}, 16))
}

func TestDecodeImageForSizePicksSmallestCoveringMip(t *testing.T) {
	data := fixture(t, "dds_dxt1.dds") // 16x8, 3 levels
	full, err := DecodeImageAt("a.dds", data, Select{})
	require.NoError(t, err)

	img, err := DecodeImageForSize("a.DDS", data, Select{}, 8)
	require.NoError(t, err)
	require.Equal(t, image.Pt(8, 4), img.Pixels.Rect.Size(), "level 1 covers 8")
	require.Equal(t, 0, img.Level, "reports the selected level")
	w, h := img.SurfaceSize()
	require.Equal(t, [2]int{16, 8}, [2]int{w, h})
	require.Equal(t, full.Levels, img.Levels)
	require.Equal(t, full.Format, img.Format)
	require.Equal(t, full.Encoding, img.Encoding)

	same, err := DecodeImageForSize("a.dds", data, Select{}, 16)
	require.NoError(t, err)
	require.Equal(t, full.Pixels.Rect, same.Pixels.Rect)
	require.Zero(t, same.SurfaceWidth, "level 0 decoded: no stand-in size")
	w, h = same.SurfaceSize()
	require.Equal(t, [2]int{16, 8}, [2]int{w, h})

	lvl, err := DecodeImageForSize("a.dds", data, Select{Level: 1}, 2)
	require.NoError(t, err)
	require.Equal(t, image.Pt(4, 2), lvl.Pixels.Rect.Size())
	require.Equal(t, 1, lvl.Level)
	w, h = lvl.SurfaceSize()
	require.Equal(t, [2]int{8, 4}, [2]int{w, h})
}

func TestDecodeImageForSizeKTX2AndCube(t *testing.T) {
	for _, name := range []string{"ktx2_rgba8.ktx2", "ktx2_zstd.ktx2"} {
		data := fixture(t, name)
		mip := decodeFixture(t, name, Select{Level: 1})
		img, err := DecodeImageForSize(name, data, Select{}, 1)
		require.NoError(t, err)
		require.Equal(t, "ktx2", img.Format)
		require.Equal(t, 0, img.Level)
		require.Equal(t, mip.Pixels.Pix, img.Pixels.Pix, name)
	}
	cube, err := DecodeImageForSize("c.dds", fixture(t, "dds_cube.dds"), Select{Face: 5}, 1)
	require.NoError(t, err)
	require.Equal(t, 5, cube.Face)
	require.Equal(t, []uint8{127, 0, 127, 255}, cube.Pixels.Pix[:4], "face 5 of the smaller mip")
}

func TestDecodeImageForSizeOtherFormatsAndErrors(t *testing.T) {
	png := fixture(t, "ref.png")
	img, err := DecodeImageForSize("ref.png", png, Select{}, 1)
	require.NoError(t, err)
	full := decodeFixture(t, "ref.png", Select{})
	require.Equal(t, full.Pixels.Rect, img.Pixels.Rect)

	dds := fixture(t, "dds_dxt1.dds")
	img, err = DecodeImageForSize("a.dds", dds, Select{}, 0)
	require.NoError(t, err)
	require.Equal(t, image.Pt(16, 8), img.Pixels.Rect.Size(), "no size decodes the selected level")

	_, err = DecodeImageForSize("a.dds", []byte("nope"), Select{}, 8)
	require.ErrorIs(t, err, errDDSHeader)
	_, err = DecodeImageForSize("a.ktx2", fixture(t, "ktx2_basis.ktx2"), Select{}, 8)
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = DecodeImageForSize("a.dds", dds[:ddsHeaderSize+8], Select{}, 1)
	require.Error(t, err, "a truncated mip fails like a full decode")
}
