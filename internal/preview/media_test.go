package preview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

func media(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "media", name))
	require.NoError(t, err)
	return data
}

func probe(t *testing.T, name string) (*VideoStats, error) {
	data := media(t, name)
	return ProbeVideo(name, bytes.NewReader(data), int64(len(data)))
}

func TestProbeVideoMP4(t *testing.T) {
	st, err := probe(t, "clip.mp4")
	require.NoError(t, err)
	assert.Equal(t, "mp4", st.Container)
	assert.InDelta(t, 2.0, st.Duration, 1e-9)
	assert.Equal(t, 320, st.Width)
	assert.Equal(t, 240, st.Height)
	assert.Equal(t, "H.264", st.VideoCodec)
	assert.Equal(t, "avc1", st.VideoCodecID)
	assert.InDelta(t, 25.0, st.FrameRate, 1e-9)
	assert.Equal(t, "AAC", st.AudioCodec)
	assert.Equal(t, 48000, st.SampleRate)
	assert.Equal(t, 2, st.Channels)
	assert.Equal(t, 2, st.Tracks)
	assert.Equal(t, int64(len(media(t, "clip.mp4"))*8/2), st.Bitrate)
}

func TestProbeVideoMOVVersion1AndLargeSize(t *testing.T) {
	st, err := probe(t, "clip.mov")
	require.NoError(t, err)
	assert.Equal(t, "mov", st.Container)
	assert.InDelta(t, 2.0, st.Duration, 1e-9)
	assert.Equal(t, 320, st.Width)
	assert.Equal(t, "H.264", st.VideoCodec)
	assert.InDelta(t, 25.0, st.FrameRate, 1e-9)
}

func TestProbeVideoMatroska(t *testing.T) {
	st, err := probe(t, "clip.webm")
	require.NoError(t, err)
	assert.Equal(t, "webm", st.Container)
	assert.InDelta(t, 2.5, st.Duration, 1e-9)
	assert.Equal(t, 1280, st.Width)
	assert.Equal(t, 720, st.Height)
	assert.Equal(t, "VP9", st.VideoCodec)
	assert.InDelta(t, 24.0, st.FrameRate, 0.01)
	assert.Equal(t, "Opus", st.AudioCodec)
	assert.Equal(t, 44100, st.SampleRate)
	assert.Equal(t, 2, st.Channels)

	st, err = probe(t, "clip.mkv")
	require.NoError(t, err)
	assert.Equal(t, "mkv", st.Container)
	assert.Equal(t, "H.264", st.VideoCodec)
	assert.Equal(t, "AAC", st.AudioCodec)
}

func TestProbeVideoRejects(t *testing.T) {
	_, err := ProbeVideo("clip.avi", bytes.NewReader([]byte("RIFF....AVI LIST")), 16)
	assert.ErrorIs(t, err, ErrUnsupported)

	mp4 := media(t, "clip.mp4")
	idx := bytes.Index(mp4, []byte("moov"))
	_, err = ProbeVideo("x.mp4", bytes.NewReader(mp4[:idx-4]), int64(idx-4))
	assert.Error(t, err)

	mkv := media(t, "clip.webm")
	end := bytes.Index(mkv, []byte{0x18, 0x53, 0x80, 0x67})
	_, err = ProbeVideo("x.webm", bytes.NewReader(mkv[:end]), int64(end))
	assert.Error(t, err)
}

func TestProbeVideoTruncatedStillAnswers(t *testing.T) {
	for _, name := range []string{"clip.mp4", "clip.webm"} {
		data := media(t, name)
		for cut := 1; cut < len(data); cut += 97 {
			_, _ = ProbeVideo(name, bytes.NewReader(data[:cut]), int64(cut))
		}
	}
}

func TestProbeVideoBoxLimit(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 8, 'f', 't', 'y', 'p'})
	for i := 0; i < maxVideoBoxes+1; i++ {
		buf.Write([]byte{0, 0, 0, 8, 'f', 'r', 'e', 'e'})
	}
	_, err := ProbeVideo("x.mp4", bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	assert.ErrorIs(t, err, errVideoLimit)
}

func TestCodecName(t *testing.T) {
	assert.Equal(t, "H.265", codecName("hvc1"))
	assert.Equal(t, "AAC", codecName("A_AAC/MPEG4/LC"))
	assert.Equal(t, "xyz", codecName("xyz "))
}

func TestDecodeFont(t *testing.T) {
	f, err := DecodeFont("Go-Regular.ttf", goregular.TTF)
	require.NoError(t, err)
	assert.Equal(t, "ttf", f.Format)
	assert.Equal(t, "Go", f.Family)
	assert.Equal(t, "Regular", f.Style)
	assert.NotEmpty(t, f.Version)
	assert.Equal(t, 2048, f.UnitsPerEm)
	assert.Positive(t, f.Ascent)
	assert.Positive(t, f.Descent)
	assert.Positive(t, f.CapHeight)
	assert.Positive(t, f.XHeight)
	assert.Positive(t, f.Glyphs)
	assert.Greater(t, f.Codepoints, 500)
	assert.Equal(t, RuneRange{First: 0x20, Last: 0x7E}, f.Coverage[0])
	assert.Equal(t, "Latin", f.Scripts[0].Script)

	sheet, err := RenderFontSheet(f, FontSheetOptions{MaxCells: 32, Columns: 8})
	require.NoError(t, err)
	assert.Positive(t, sheet.Rect.Dx())
	inked := false
	for i := 3; i < len(sheet.Pix); i += 4 {
		if sheet.Pix[i] != 0 {
			inked = true
			break
		}
	}
	assert.True(t, inked)

	_, err = RenderFontSheet(&Font{}, FontSheetOptions{})
	assert.Error(t, err)
}

func TestDecodeFontWOFF(t *testing.T) {
	f, err := DecodeFont("regular.woff", media(t, "regular.woff"))
	require.NoError(t, err)
	assert.Equal(t, "woff", f.Format)
	assert.Equal(t, "Go", f.Family)

	ref, err := DecodeFont("r.ttf", goregular.TTF)
	require.NoError(t, err)
	assert.Equal(t, ref.Coverage, f.Coverage)
}

func TestDecodeFontRejects(t *testing.T) {
	_, err := DecodeFont("a.woff2", []byte("wOF2xxxx"))
	assert.ErrorIs(t, err, ErrUnsupported)
	_, err = DecodeFont("a.ttc", []byte("ttcfxxxx"))
	assert.ErrorIs(t, err, ErrUnsupported)
	_, err = DecodeFont("a.ttf", []byte("garbage"))
	assert.Error(t, err)
	_, err = DecodeFont("a.woff", []byte("wOFF"))
	assert.Error(t, err)
	_, err = DecodeFont("a.ttf", make([]byte, MaxFontBytes+1))
	assert.ErrorIs(t, err, ErrTooLarge)

	woff := bytes.Clone(media(t, "regular.woff"))
	binary.BigEndian.PutUint32(woff[44+8:], 1<<30)
	_, err = DecodeFont("a.woff", woff)
	assert.Error(t, err)
}

func TestCompareFonts(t *testing.T) {
	reg, err := DecodeFont("r.ttf", goregular.TTF)
	require.NoError(t, err)
	mono, err := DecodeFont("m.ttf", gomono.TTF)
	require.NoError(t, err)
	bold, err := DecodeFont("b.ttf", gobold.TTF)
	require.NoError(t, err)

	same := CompareFonts(reg, reg)
	assert.Empty(t, same.Added)
	assert.Empty(t, same.Removed)
	assert.Empty(t, same.Changes)

	d := CompareFonts(reg, bold)
	names := map[string]MetricChange{}
	for _, c := range d.Changes {
		names[c.Name] = c
	}
	assert.Equal(t, "Regular", names["Style"].Old)
	assert.Equal(t, "Bold", names["Style"].New)

	d = CompareFonts(reg, mono)
	assert.Equal(t, reg.Codepoints+d.AddedCount-d.RemovedCount, mono.Codepoints)

	assert.Empty(t, CompareFonts(nil, reg).Changes)
}

func TestSubtractRanges(t *testing.T) {
	a := []RuneRange{{1, 10}, {20, 30}}
	b := []RuneRange{{3, 4}, {8, 22}, {29, 40}}
	got, n := subtractRanges(a, b)
	assert.Equal(t, []RuneRange{{1, 2}, {5, 7}, {23, 28}}, got)
	assert.Equal(t, 11, n)
	got, n = subtractRanges(a, nil)
	assert.Equal(t, a, got)
	assert.Equal(t, 21, n)
}

func TestDecodeThumbnailBlend(t *testing.T) {
	for _, name := range []string{"scene.blend", "scene_be32.blend", "scene_v5.blend", "scene_gz.blend", "scene_zstd.blend"} {
		t.Run(name, func(t *testing.T) {
			img, err := DecodeThumbnail(name, media(t, name))
			require.NoError(t, err)
			assert.Equal(t, "Blender thumbnail", img.Format)
			assert.Equal(t, 8, img.Width())
			assert.Equal(t, 6, img.Height())
			assert.Equal(t, [4]uint8{30, 0, 200, 255}, [4]uint8(img.Pixels.Pix[4:8]))
			assert.Equal(t, uint8(40), img.Pixels.Pix[img.Pixels.Stride+1])
		})
	}
	_, err := DecodeThumbnail("empty.blend", media(t, "empty.blend"))
	assert.ErrorIs(t, err, ErrNoThumbnail)
	_, err = DecodeThumbnail("x.blend", []byte("not blender"))
	assert.Error(t, err)
	_, err = DecodeThumbnail("x.blend", []byte{0x1f, 0x8b, 0})
	assert.Error(t, err)
	_, err = DecodeThumbnail("x.blend", []byte("BLENDER17-02v0500"))
	assert.ErrorIs(t, err, ErrUnsupported)

	data := bytes.Clone(media(t, "scene.blend"))
	i := bytes.Index(data, []byte("TEST"))
	binary.LittleEndian.PutUint32(data[i+24:], 100000)
	_, err = DecodeThumbnail("x.blend", data)
	assert.Error(t, err)
	_, err = DecodeThumbnail("x.blend", media(t, "scene.blend")[:i+40])
	assert.Error(t, err)
}

func TestDecodeThumbnailUnreal(t *testing.T) {
	img, err := DecodeThumbnail("mesh.uasset", media(t, "mesh.uasset"))
	require.NoError(t, err)
	assert.Equal(t, "Unreal thumbnail (PNG)", img.Format)
	assert.Equal(t, 8, img.Width())

	img, err = DecodeThumbnail("level.umap", media(t, "level.umap"))
	require.NoError(t, err)
	assert.Equal(t, "Unreal thumbnail (JPEG)", img.Format)
	assert.Equal(t, 6, img.Height())

	_, err = DecodeThumbnail("x.uasset", []byte{0xC1, 0x83, 0x2A, 0x9E, 0, 0, 0, 0})
	assert.ErrorIs(t, err, ErrNoThumbnail)
	_, err = DecodeThumbnail("x.uasset", []byte("nope"))
	assert.Error(t, err)
	_, err = DecodeThumbnail("x.png", nil)
	assert.ErrorIs(t, err, ErrUnsupported)
	assert.True(t, HasEmbeddedThumbnail("A/B.UASSET"))
	assert.False(t, HasEmbeddedThumbnail("a.png"))
}

func FuzzProbeVideo(f *testing.F) {
	addFixtureSeeds(f, "media/clip.*")
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, name := range []string{"f.mp4", "f.webm"} {
			st, err := ProbeVideo(name, bytes.NewReader(data), int64(len(data)))
			if err == nil && st == nil {
				t.Fatal("nil stats without error")
			}
		}
	})
}

func FuzzDecodeFont(f *testing.F) {
	f.Add(goregular.TTF)
	addFixtureSeeds(f, "media/*.woff")
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, name := range []string{"f.ttf", "f.woff"} {
			font, err := DecodeFont(name, data)
			if err == nil {
				_, _ = RenderFontSheet(font, FontSheetOptions{MaxCells: 8})
			}
		}
	})
}

func FuzzDecodeThumbnail(f *testing.F) {
	addFixtureSeeds(f, "media/*.blend")
	addFixtureSeeds(f, "media/*.uasset")
	addFixtureSeeds(f, "media/*.umap")
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, name := range []string{"f.blend", "f.uasset"} {
			img, err := DecodeThumbnail(name, data)
			if err == nil && (img.Width() <= 0 || img.Height() <= 0) {
				t.Fatal("empty thumbnail")
			}
			if err != nil && img != nil {
				t.Fatal("image with error")
			}
			_ = errors.Is(err, ErrNoThumbnail)
		}
	})
}
