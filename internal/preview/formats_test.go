package preview

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

func decodeFixture(t *testing.T, name string, sel Select) *Image {
	t.Helper()
	img, err := DecodeImageAt(name, fixture(t, name), sel)
	require.NoError(t, err)
	return img
}

func requirePixelsNear(t *testing.T, want, got *image.NRGBA, channels, tolerance int) {
	t.Helper()
	require.Equal(t, want.Rect.Size(), got.Rect.Size())
	for y := range want.Rect.Dy() {
		for x := range want.Rect.Dx() {
			a, b := want.Pix[y*want.Stride+x*4:], got.Pix[y*got.Stride+x*4:]
			if channels == 4 && a[3] == 0 && b[3] == 0 {
				continue
			}
			for c := range channels {
				near(t, int(a[c]), int(b[c]), tolerance, "pixel %d,%d channel %d", x, y, c)
			}
		}
	}
}

func TestDDSMatchesPillow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		encoding string
		channels int
		levels   int
	}{
		{"dds_dxt1", "DXT1", 4, 3},
		{"dds_dxt5", "DXT5", 4, 3},
		{"dds_rgba", "32-bit RGB", 4, 3},
		{"dds_bc7", "BC7_UNORM", 4, 1},
		{"dds_l8", "8-bit luminance", 4, 1},
		{"dds_bc5", "BC5", 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := decodeFixture(t, tc.name+".dds", Select{})
			ref := decodeFixture(t, tc.name+".ref.png", Select{})
			require.Equal(t, "dds", img.Format)
			require.Equal(t, tc.encoding, img.Encoding)
			require.Equal(t, tc.levels, img.Levels)
			requirePixelsNear(t, ref.Pixels, img.Pixels, tc.channels, 1)
		})
	}
}

func TestDDSMipLevels(t *testing.T) {
	img := decodeFixture(t, "dds_dxt1.dds", Select{Level: 1})
	require.Equal(t, 1, img.Level)
	require.Equal(t, image.Pt(8, 4), img.Pixels.Rect.Size())
	img = decodeFixture(t, "dds_rgba.dds", Select{Level: 99, Face: 3})
	require.Equal(t, 2, img.Level)
	require.Equal(t, 0, img.Face)
	require.Equal(t, image.Pt(4, 2), img.Pixels.Rect.Size())
}

func TestCubeFaces(t *testing.T) {
	for _, name := range []string{"dds_cube.dds", "ktx2_cube.ktx2"} {
		img := decodeFixture(t, name, Select{Face: 2})
		require.True(t, img.Cube, name)
		require.Equal(t, 6, img.Faces)
		require.Equal(t, 2, img.Face)
		require.Equal(t, []uint8{0, 0, 255, 255}, img.Pixels.Pix[:4], name)
	}
	img := decodeFixture(t, "dds_cube.dds", Select{Level: 1, Face: 5})
	require.Equal(t, []uint8{127, 0, 127, 255}, img.Pixels.Pix[:4])
	require.Equal(t, 2, img.Levels)
}

var floatPixels = []float32{2, 0.5, 0, 1, 0.25, 4, 1, 0.5, 0, 0, 8, 1, 1, 1, 1, 0}

func TestFloatTextures(t *testing.T) {
	for _, name := range []string{"dds_rgba16f.dds", "dds_d3d113.dds", "ktx2_rgba16f.ktx2"} {
		img := decodeFixture(t, name, Select{})
		require.NotNil(t, img.HDR, name)
		require.Equal(t, floatPixels, img.HDR.Pix, name)
		require.Equal(t, 16, img.BitDepth)
		require.True(t, img.HasAlpha)
		require.Equal(t, []uint8{255, 188, 0, 255}, img.Pixels.Pix[:4])
	}
}

func TestKTX2(t *testing.T) {
	tex := decodeFixture(t, "tex.png", Select{})
	for _, name := range []string{"ktx2_rgba8.ktx2", "ktx2_zstd.ktx2", "ktx2_zlib.ktx2"} {
		img := decodeFixture(t, name, Select{})
		require.Equal(t, "ktx2", img.Format)
		require.Equal(t, 2, img.Levels)
		requirePixelsNear(t, tex.Pixels, img.Pixels, 4, 0)
		mip := decodeFixture(t, name, Select{Level: 1})
		requirePixelsNear(t, decodeFixture(t, "ktx2_mip1.ref.png", Select{}).Pixels, mip.Pixels, 4, 0)
	}
	bc7 := decodeFixture(t, "ktx2_bc7.ktx2", Select{})
	require.Equal(t, "BC7_UNORM", bc7.Encoding)
	requirePixelsNear(t, decodeFixture(t, "dds_bc7.ref.png", Select{}).Pixels, bc7.Pixels, 4, 0)
	_, err := DecodeImage("a.ktx2", fixture(t, "ktx2_basis.ktx2"))
	require.ErrorIs(t, err, ErrUnsupported)
}

func TestEXR(t *testing.T) {
	base := decodeFixture(t, "exr_none.exr", Select{})
	tex := decodeFixture(t, "tex.png", Select{}).Pixels
	for i, v := range tex.Pix {
		if tex.Pix[i|3] != 0 || i%4 == 3 {
			require.InDelta(t, float64(v)/255, base.HDR.Pix[i], 0.002, "sample %d", i)
		}
	}
	require.Equal(t, 16, base.BitDepth)
	for _, name := range []string{"exr_rle.exr", "exr_zips.exr", "exr_zip.exr", "exr_pxr24.exr"} {
		img := decodeFixture(t, name, Select{})
		require.Equal(t, base.HDR.Pix, img.HDR.Pix, name)
	}
	require.Equal(t, "PXR24", decodeFixture(t, "exr_pxr24.exr", Select{}).Encoding)
	_, err := DecodeImage("a.exr", fixture(t, "exr_piz.exr"))
	require.ErrorIs(t, err, ErrUnsupported)
	require.ErrorContains(t, err, "PIZ")

	tiled := decodeFixture(t, "exr_tiled.exr", Select{})
	require.Equal(t, 32, tiled.BitDepth)
	at := func(img *Image, x, y int) []float32 { i := (y*img.HDR.Width + x) * 4; return img.HDR.Pix[i : i+4] }
	require.Equal(t, []float32{2, 4, 3, 1}, at(tiled, 4, 2))
	require.Equal(t, []float32{1.5, 2, 3, 1}, at(tiled, 3, 1))
	luma := decodeFixture(t, "exr_luma.exr", Select{})
	require.Equal(t, []float32{1, 1, 1, 1}, at(luma, 1, 1))
	depth := decodeFixture(t, "exr_depth.exr", Select{})
	require.Equal(t, []float32{9, 9, 9, 1}, at(depth, 1, 0))
}

func TestRadiance(t *testing.T) {
	img := decodeFixture(t, "hdr_rle.hdr", Select{})
	require.Equal(t, "hdr", img.Format)
	requirePixelsNear(t, decodeFixture(t, "tex_rgb.png", Select{}).Pixels, img.Pixels, 3, 16)
	flat := decodeFixture(t, "hdr_flat.hdr", Select{})
	require.Equal(t, []float32{0, 0, 0, 1}, flat.HDR.Pix[:4])
	require.InDelta(t, 255.5/256, flat.HDR.Pix[4], 1e-6)
	require.Equal(t, flat.HDR.Pix[8:12], flat.HDR.Pix[12:16])
	require.InDelta(t, 128.5/128, flat.HDR.Pix[8], 1e-6)
}

func TestPSDFixtures(t *testing.T) {
	tex := decodeFixture(t, "tex.png", Select{})
	layered := decodeFixture(t, "psd_layers.psd", Select{})
	require.Equal(t, "psd", layered.Format)
	requirePixelsNear(t, tex.Pixels, layered.Pixels, 4, 0)
	require.Len(t, layered.Layers, 2)
	require.Equal(t, "Spark", layered.Layers[0].Name)
	require.Equal(t, "Hero", layered.Layers[1].Name)
	require.NotEqual(t, layered.Layers[0].Hash, layered.Layers[1].Hash)

	deep := decodeFixture(t, "psd_16.psd", Select{})
	require.Equal(t, 16, deep.BitDepth)
	requirePixelsNear(t, tex.Pixels, deep.Pixels, 4, 1)
	psb := decodeFixture(t, "psd_raw.psb", Select{})
	require.Equal(t, "psb", psb.Format)
	rgb := decodeFixture(t, "tex_rgb.png", Select{})
	requirePixelsNear(t, rgb.Pixels, psb.Pixels, 3, 0)
	cmyk := decodeFixture(t, "psd_cmyk.psd", Select{})
	require.Equal(t, "CMYK", cmyk.Encoding)
	requirePixelsNear(t, rgb.Pixels, cmyk.Pixels, 3, 2)
	gray := decodeFixture(t, "psd_gray.psd", Select{})
	require.Equal(t, "Grayscale", gray.Encoding)
	require.Equal(t, gray.Pixels.Pix[0], gray.Pixels.Pix[1])
}
