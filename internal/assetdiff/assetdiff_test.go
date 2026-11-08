package assetdiff

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/preview"
)

var ctx = context.Background()

type files map[string][]byte

func (f files) load(_ context.Context, ref Ref, limit int64) ([]byte, error) {
	data, ok := f[ref.Source+":"+ref.Path]
	if !ok {
		return nil, ErrMissing
	}
	if string(data) == "ERR" {
		return nil, errors.New("offline")
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

func oldRef(p string) *Ref { return &Ref{Source: "snapshot", Path: p} }
func newRef(p string) *Ref { return &Ref{Source: "working", Path: p} }

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "preview", "testdata", filepath.FromSlash(name)))
	require.NoError(t, err)
	return data
}

func pngBytes(t *testing.T, w, h int, paint func(x, y int) color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, paint(x, y))
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func wavBytes(seconds float64, amp float64) []byte {
	const rate = 8000
	n := int(seconds * rate)
	var pcm bytes.Buffer
	for i := range n {
		v := int16(amp * 32767 * math.Sin(2*math.Pi*440*float64(i)/rate))
		_ = binary.Write(&pcm, binary.LittleEndian, v)
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+pcm.Len()))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(pcm.Len()))
	b.Write(pcm.Bytes())
	return b.Bytes()
}

func TestCompareStatusAndErrors(t *testing.T) {
	f := files{
		"snapshot:a.txt": []byte("one\ntwo\n"),
		"working:a.txt":  []byte("one\nthree\nfour\n"),
		"working:b.txt":  []byte("same"),
		"snapshot:b.txt": []byte("same"),
		"working:c.bin":  []byte("x\x00y"),
		"snapshot:c.bin": []byte("x\x00"),
		"working:d.dat":  []byte("ERR"),
		"working:e.mp4":  []byte("movie"),
	}
	r, err := Compare(ctx, f.load, oldRef("a.txt"), newRef("a.txt"), Options{})
	require.NoError(t, err)
	assert.Equal(t, preview.KindText, r.Kind)
	assert.Equal(t, StatusModified, r.Status)
	assert.Equal(t, &TextResult{LinesAdded: 2, LinesRemoved: 1}, r.Text)
	assert.Equal(t, "+2 -1 lines", Headline(r))

	r, err = Compare(ctx, f.load, oldRef("b.txt"), newRef("b.txt"), Options{})
	require.NoError(t, err)
	assert.Equal(t, StatusUnchanged, r.Status)
	assert.Nil(t, r.Text)
	assert.Equal(t, "unchanged", Headline(r))

	r, err = Compare(ctx, f.load, oldRef("c.bin"), newRef("c.bin"), Options{})
	require.NoError(t, err)
	assert.Equal(t, preview.KindOpaque, r.Kind)
	assert.Equal(t, int64(2), r.OldSize)
	assert.Equal(t, int64(3), r.NewSize)
	assert.Equal(t, "2 B → 3 B", Headline(r))

	r, err = Compare(ctx, f.load, oldRef("e.mp4"), newRef("e.mp4"), Options{})
	require.NoError(t, err)
	assert.Equal(t, preview.KindVideo, r.Kind)
	assert.Equal(t, StatusAdded, r.Status)
	assert.Equal(t, "added, 5 B", Headline(r))

	r, err = Compare(ctx, f.load, oldRef("a.txt"), nil, Options{})
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, r.Status)
	assert.Equal(t, &TextResult{LinesRemoved: 2}, r.Text)

	r, err = Compare(ctx, f.load, oldRef("a.txt"), newRef("renamed.txt"), Options{})
	require.NoError(t, err)
	assert.Equal(t, "renamed.txt", r.Path)
	assert.Equal(t, "a.txt", r.OldPath)

	_, err = Compare(ctx, f.load, nil, nil, Options{})
	assert.Error(t, err)
	_, err = Compare(ctx, f.load, oldRef("none"), newRef("none"), Options{})
	assert.ErrorIs(t, err, ErrMissing)
	_, err = Compare(ctx, f.load, nil, newRef("d.dat"), Options{})
	assert.ErrorContains(t, err, "offline")
}

func TestCompareTooLarge(t *testing.T) {
	big := append([]byte("%YAML 1.1\n"), bytes.Repeat([]byte("a"), int(MaxSceneBytes))...)
	f := files{"working:Assets/Big.unity": big, "snapshot:Assets/Big.unity": []byte("%YAML 1.1\n")}
	r, err := Compare(ctx, f.load, oldRef("Assets/Big.unity"), newRef("Assets/Big.unity"), Options{})
	require.NoError(t, err)
	assert.Equal(t, StatusModified, r.Status)
	assert.Contains(t, r.Error, "too large")
	assert.Nil(t, r.Scene)

	long := bytes.Repeat([]byte("line\n"), int(MaxTextBytes/5)+1)
	f = files{"working:big.txt": long, "snapshot:big.txt": []byte("line\n")}
	r, err = Compare(ctx, f.load, oldRef("big.txt"), newRef("big.txt"), Options{})
	require.NoError(t, err)
	assert.Empty(t, r.Error)
	assert.Nil(t, r.Text)
	assert.Equal(t, "5.0 B → 4.0 MiB", strings.Replace(Headline(r), "5 B", "5.0 B", 1))
}

func TestCompareScene(t *testing.T) {
	f := files{
		"snapshot:Assets/Level.unity": fixture(t, "unity/Level_old.unity"),
		"working:Assets/Level.unity":  fixture(t, "unity/Level.unity"),
		"snapshot:Assets/Hero.mat":    fixture(t, "unity/Hero_old.mat"),
		"working:Assets/Hero.mat":     fixture(t, "unity/Hero.mat"),
		"working:Assets/Binary.asset": fixture(t, "unity/Binary.asset"),
		"snapshot:level.tscn":         fixture(t, "godot/level_old.tscn"),
		"working:level.tscn":          fixture(t, "godot/level.tscn"),
	}
	r, err := Compare(ctx, f.load, oldRef("Assets/Level.unity"), newRef("Assets/Level.unity"), Options{})
	require.NoError(t, err)
	require.NotNil(t, r.Scene)
	assert.Equal(t, preview.KindScene, r.Kind)
	assert.Equal(t, "unity", r.Scene.Engine)
	assert.NotEmpty(t, r.Scene.Changes)
	assert.NotEqual(t, "no structural changes", Headline(r))

	capped, err := Compare(ctx, f.load, oldRef("Assets/Level.unity"), newRef("Assets/Level.unity"), Options{MaxChanges: 1, MaxProps: 1})
	require.NoError(t, err)
	assert.Len(t, capped.Scene.Changes, 1)
	assert.Equal(t, len(r.Scene.Changes) > 1, capped.Scene.Truncated)
	assert.Equal(t, r.Scene.Props, capped.Scene.Props)
	for _, c := range capped.Scene.Changes {
		assert.LessOrEqual(t, len(c.Props), 1)
	}

	var asked []string
	opts := Options{GUIDs: func(p string) func(string) string {
		asked = append(asked, p)
		return func(guid string) string {
			if guid == "4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f" {
				return "Assets/Hero_Normal.png"
			}
			return ""
		}
	}}
	r, err = Compare(ctx, f.load, oldRef("Assets/Hero.mat"), newRef("Assets/Hero.mat"), opts)
	require.NoError(t, err)
	assert.Equal(t, []string{"Assets/Hero.mat", "Assets/Hero.mat"}, asked)
	js, _ := json.Marshal(r)
	assert.Contains(t, string(js), "Assets/Hero_Normal.png")

	r, err = Compare(ctx, f.load, nil, newRef("Assets/Binary.asset"), Options{})
	require.NoError(t, err)
	assert.Contains(t, r.Error, "Force Text")
	assert.Nil(t, r.Scene)

	r, err = Compare(ctx, f.load, oldRef("level.tscn"), newRef("level.tscn"), Options{})
	require.NoError(t, err)
	assert.Equal(t, "godot", r.Scene.Engine)
	assert.NotEmpty(t, Headline(r))

	r, err = Compare(ctx, f.load, oldRef("gone.tscn"), newRef("level.tscn"), Options{})
	require.NoError(t, err)
	assert.Equal(t, StatusAdded, r.Status)
	assert.Equal(t, "godot", r.Scene.Engine)
	assert.Zero(t, r.Scene.Removed)
	assert.Positive(t, r.Scene.Added)

	r, err = Compare(ctx, f.load, oldRef("level.tscn"), nil, Options{})
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, r.Status)
	assert.Positive(t, r.Scene.Removed)
	assert.Contains(t, Headline(r), "-")
}

func TestSceneLine(t *testing.T) {
	assert.Equal(t, "no structural changes", sceneLine(&SceneResult{}))
	assert.Equal(t, "+1 object, -2 objects, 3 objects changed (1 prop), +1 reference, -2 references", sceneLine(&SceneResult{
		Added: 1, Removed: 2, Changed: 3, Props: 1,
		Refs: []RefChange{{Status: "added"}, {Status: "removed"}, {Status: "removed"}},
	}))
}

func TestCompareImage(t *testing.T) {
	gray := func(int, int) color.NRGBA { return color.NRGBA{100, 100, 100, 255} }
	patched := func(x, y int) color.NRGBA {
		if x < 2 && y < 1 {
			return color.NRGBA{255, 0, 0, 255}
		}
		return gray(x, y)
	}
	f := files{
		"snapshot:t.png":  pngBytes(t, 4, 4, gray),
		"working:t.png":   pngBytes(t, 4, 4, patched),
		"working:big.png": pngBytes(t, 600, 300, gray),
		"working:bad.png": []byte("not a png"),
	}
	r, err := Compare(ctx, f.load, oldRef("t.png"), newRef("t.png"), Options{})
	require.NoError(t, err)
	require.NotNil(t, r.Image.Compare)
	assert.Equal(t, 2, r.Image.Compare.ChangedPixels)
	assert.Equal(t, &Rect{X: 0, Y: 0, W: 2, H: 1}, r.Image.Compare.Bounds)
	assert.Nil(t, r.Image.New.ThumbnailPNG)
	assert.Equal(t, "12% pixels changed in 4×4", Headline(r))

	r, err = Compare(ctx, f.load, oldRef("t.png"), newRef("t.png"), Options{Threshold: 255})
	require.NoError(t, err)
	assert.Nil(t, r.Image.Compare.Bounds)
	assert.Equal(t, "0% pixels changed in 4×4", Headline(r))

	r, err = Compare(ctx, f.load, oldRef("t.png"), newRef("big.png"), Options{Images: true, ThumbSize: 64})
	require.NoError(t, err)
	assert.True(t, r.Image.Compare.SizeChanged)
	assert.Equal(t, "4×4 → 600×300", Headline(r))
	thumb, err := png.Decode(bytes.NewReader(r.Image.New.ThumbnailPNG))
	require.NoError(t, err)
	assert.Equal(t, image.Pt(64, 32), thumb.Bounds().Size())
	js, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(js), `"thumbnail_png":"`)

	r, err = Compare(ctx, f.load, nil, newRef("big.png"), Options{})
	require.NoError(t, err)
	assert.Equal(t, "added 600×300 png", Headline(r))
	assert.Nil(t, r.Image.Compare)
	r, err = Compare(ctx, f.load, oldRef("t.png"), nil, Options{})
	require.NoError(t, err)
	assert.Equal(t, "removed 4×4 png", Headline(r))

	r, err = Compare(ctx, f.load, oldRef("t.png"), newRef("bad.png"), Options{})
	require.NoError(t, err)
	assert.NotEmpty(t, r.Error)
	assert.False(t, strings.HasPrefix(r.Error, "preview:"))
	assert.Nil(t, r.Image)
}

func TestImageLine(t *testing.T) {
	assert.Equal(t, "", imageLine(StatusModified, &ImageResult{}))
	im := &ImageResult{
		Old:     &ImageSide{Width: 2, Height: 2, Format: "png"},
		New:     &ImageSide{Width: 2, Height: 2, Format: "jpeg"},
		Compare: &ImageCompare{FormatChanged: true, ChangedRatio: 0.001},
	}
	assert.Equal(t, "<1% pixels changed in 2×2, png → jpeg", imageLine(StatusModified, im))
}

const objOld = `mtllib a.mtl
v 0 0 0
v 1 0 0
v 0 1 0
v 1 1 0
usemtl Skin
f 1 2 3
`

const objNew = `mtllib a.mtl
v 0 0 0
v 1 0 0
v 0 1 0
v 1 1 0
v 2 2 2
usemtl Cloth
f 1 2 3
f 2 4 3
f 3 4 5
`

func TestCompareModel(t *testing.T) {
	f := files{
		"snapshot:m.obj": []byte(objOld),
		"working:m.obj":  []byte(objNew),
		"working:m.fbx":  []byte("fbx"),
	}
	r, err := Compare(ctx, f.load, oldRef("m.obj"), newRef("m.obj"), Options{})
	require.NoError(t, err)
	require.NotNil(t, r.Model.Delta)
	assert.Equal(t, 2, r.Model.Delta.Triangles)
	assert.Equal(t, []string{"Cloth"}, r.Model.Delta.MaterialsAdded)
	assert.Equal(t, []string{"Skin"}, r.Model.Delta.MaterialsRemoved)
	assert.True(t, r.Model.Delta.BoundsChanged)
	assert.Equal(t, "triangles 1 → 3 (+200%), +1 material, -1 material, bounds changed", Headline(r))

	r, err = Compare(ctx, f.load, nil, newRef("m.obj"), Options{})
	require.NoError(t, err)
	assert.Equal(t, "added, 3 triangles, 1 mesh", Headline(r))
	r, err = Compare(ctx, f.load, oldRef("m.obj"), nil, Options{})
	require.NoError(t, err)
	assert.Equal(t, "removed, 1 triangles, 1 mesh", Headline(r))

	r, err = Compare(ctx, f.load, nil, newRef("m.fbx"), Options{})
	require.NoError(t, err)
	assert.Contains(t, r.Error, "unsupported")
	assert.Equal(t, "added, 3 B", Headline(r))
}

func TestModelLine(t *testing.T) {
	side := &ModelSide{Vertices: 3, Triangles: 1, Meshes: 1}
	more := &ModelSide{Vertices: 4, Triangles: 1, Meshes: 2}
	assert.Equal(t, "", modelLine(StatusModified, &ModelResult{}))
	assert.Equal(t, "no geometry change", modelLine(StatusModified, &ModelResult{Old: side, New: side, Delta: &ModelDelta{}}))
	assert.Equal(t, "vertices 3 → 4 (+33%), meshes 1 → 2 (+100%), +1 animation, -1 animation", modelLine(StatusModified, &ModelResult{
		Old: side, New: more,
		Delta: &ModelDelta{Vertices: 1, Meshes: 1, AnimationsAdded: []string{"run"}, AnimationsRemoved: []string{"walk"}},
	}))
	assert.Equal(t, []string{}, orEmpty(nil))
	assert.Equal(t, "0 → 5", change(0, 5))
}

func TestCompareAudio(t *testing.T) {
	f := files{
		"snapshot:a.wav": wavBytes(0.5, 0.25),
		"working:a.wav":  wavBytes(1, 1),
		"working:s.wav":  wavBytes(0.5, 0),
		"working:x.wav":  []byte("RIFFjunk"),
		"working:t.ogg":  fixture(t, "audio/tone.ogg"),
	}
	r, err := Compare(ctx, f.load, oldRef("a.wav"), newRef("a.wav"), Options{})
	require.NoError(t, err)
	require.Empty(t, r.Error)
	require.NotNil(t, r.Audio.Delta)
	assert.InDelta(t, 0.5, r.Audio.Delta.Duration, 0.001)
	assert.Positive(t, *r.Audio.Delta.PeakDB)
	assert.False(t, r.Audio.Delta.FormatChanged)
	h := Headline(r)
	assert.Contains(t, h, "duration 0.50s → 1.00s")
	assert.Contains(t, h, "peak")

	r, err = Compare(ctx, f.load, oldRef("a.wav"), newRef("t.ogg"), Options{})
	require.NoError(t, err)
	assert.True(t, r.Audio.Delta.FormatChanged)
	assert.Contains(t, Headline(r), "wav 8000 Hz → ")

	r, err = Compare(ctx, f.load, nil, newRef("s.wav"), Options{})
	require.NoError(t, err)
	assert.Nil(t, r.Audio.New.PeakDB)
	assert.Equal(t, "added, 0.50s wav 8000 Hz", Headline(r))
	r, err = Compare(ctx, f.load, oldRef("a.wav"), nil, Options{})
	require.NoError(t, err)
	assert.Equal(t, "removed, 0.50s wav 8000 Hz", Headline(r))

	r, err = Compare(ctx, f.load, oldRef("a.wav"), newRef("x.wav"), Options{})
	require.NoError(t, err)
	assert.NotEmpty(t, r.Error)
}

func TestAudioLine(t *testing.T) {
	v := func(f float64) *float64 { return &f }
	assert.Equal(t, "", audioLine(StatusModified, &AudioResult{}))
	same := &AudioSide{Duration: 1}
	assert.Equal(t, "same length and levels", audioLine(StatusModified, &AudioResult{Old: same, New: same, Delta: &AudioDelta{}}))
	assert.Equal(t, "loudness -20.0 → -14.0 LUFS, 7 clipped samples", audioLine(StatusModified, &AudioResult{
		Old:   &AudioSide{Duration: 1, Loudness: v(-20)},
		New:   &AudioSide{Duration: 1, Loudness: v(-14), Clipped: 7},
		Delta: &AudioDelta{Loudness: v(6), Clipped: 7},
	}))
	assert.Nil(t, sub(nil, v(1)))
	assert.Nil(t, finite(math.NaN()))
}

func TestCompareTable(t *testing.T) {
	f := files{
		"snapshot:d.csv":  []byte("id,name,hp\n1,orc,10\n2,elf,8\n3,imp,2\n"),
		"working:d.csv":   []byte("id,name,hp,armor\n1,orc,12,1\n2,elf,8,0\n4,dwarf,9,3\n"),
		"working:bad.csv": []byte("\xc3\x28,x\n"),
	}
	r, err := Compare(ctx, f.load, oldRef("d.csv"), newRef("d.csv"), Options{})
	require.NoError(t, err)
	tb := r.Table
	require.NotNil(t, tb)
	assert.Equal(t, 1, tb.ColumnsAdded)
	assert.Equal(t, "id", tb.KeyColumn)
	assert.Equal(t, "added", tb.Columns[3].Status)
	assert.Equal(t, 1, tb.RowsAdded)
	assert.Equal(t, 1, tb.RowsRemoved)
	assert.False(t, tb.Truncated)
	assert.Equal(t, "+1 row, -1 row, 1 row changed (1 cell), +1 column", Headline(r))

	r, err = Compare(ctx, f.load, oldRef("d.csv"), newRef("d.csv"), Options{MaxRows: 1})
	require.NoError(t, err)
	assert.Len(t, r.Table.Rows, 1)
	assert.True(t, r.Table.Truncated)

	r, err = Compare(ctx, f.load, oldRef("d.csv"), nil, Options{})
	require.NoError(t, err)
	assert.Equal(t, 3, r.Table.RowsRemoved)
	assert.Equal(t, "removed", r.Table.Columns[0].Status)

	r, err = Compare(ctx, f.load, oldRef("d.csv"), newRef("bad.csv"), Options{})
	require.NoError(t, err)
	assert.NotEmpty(t, r.Error)
}

func TestTableLine(t *testing.T) {
	assert.Equal(t, "no cell changes", tableLine(&TableResult{}))
	assert.Equal(t, "2 rows moved, -1 column", tableLine(&TableResult{RowsMoved: 2, ColumnsRemoved: 1}))
	r, err := compareTable([2]side{
		{ref: oldRef("r.csv"), data: []byte("id,name\n1,a\n"), present: true},
		{ref: newRef("r.csv"), data: []byte("key,title\n1,a\n"), present: true},
	}, Options{}.withDefaults())
	require.NoError(t, err)
	assert.Equal(t, "renamed", r.Columns[1].Status)
	assert.Equal(t, "name", r.Columns[1].OldName)
}

func TestFormatting(t *testing.T) {
	assert.Equal(t, "0", thousands(0))
	assert.Equal(t, "12,040", thousands(12040))
	assert.Equal(t, "-1,234,567", thousands(-1234567))
	assert.Equal(t, "2 meshes", plural(2, "mesh"))
	assert.Equal(t, "1.5 KiB", bytesLabel(1536))
	assert.Equal(t, "3.0 GiB", bytesLabel(3<<30))
	assert.Equal(t, "51%", percent(0.51))
	assert.Equal(t, "1 B → 2 B", sizeLine(&Result{Status: StatusModified, OldSize: 1, NewSize: 2}))
	assert.Equal(t, "removed, 1 B", sizeLine(&Result{Status: StatusRemoved, OldSize: 1}))
	assert.Equal(t, "1 B → 2 B", Headline(&Result{Status: StatusModified, OldSize: 1, NewSize: 2, Image: &ImageResult{}}))
}
