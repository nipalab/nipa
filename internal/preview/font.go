package preview

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// MaxFontBytes bounds a font file and a WOFF's decompressed tables.
const MaxFontBytes = 32 << 20

// RuneRange is an inclusive range of code points.
type RuneRange struct {
	First rune `json:"first"`
	Last  rune `json:"last"`
}

// ScriptCoverage counts the mapped code points of one Unicode script.
type ScriptCoverage struct {
	Script string `json:"script"`
	Count  int    `json:"count"`
}

// Font is a parsed TTF/OTF (or WOFF) font. Metrics are in font units.
type Font struct {
	Format     string           `json:"format"`
	Family     string           `json:"family"`
	Style      string           `json:"style"`
	FullName   string           `json:"fullName"`
	Version    string           `json:"version"`
	Glyphs     int              `json:"glyphs"`
	UnitsPerEm int              `json:"unitsPerEm"`
	Ascent     int              `json:"ascent"`
	Descent    int              `json:"descent"`
	LineGap    int              `json:"lineGap"`
	CapHeight  int              `json:"capHeight"`
	XHeight    int              `json:"xHeight"`
	Codepoints int              `json:"codepoints"`
	Coverage   []RuneRange      `json:"coverage"`
	Scripts    []ScriptCoverage `json:"scripts"`

	face *sfnt.Font
}

// DecodeFont parses a TrueType or OpenType font (WOFF 1.0 is unwrapped
// first) into its names, metrics and code point coverage.
func DecodeFont(name string, data []byte) (*Font, error) {
	if len(data) > MaxFontBytes {
		return nil, ErrTooLarge
	}
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	switch {
	case bytes.HasPrefix(data, []byte("wOF2")):
		return nil, fmt.Errorf("%w: WOFF2 fonts", ErrUnsupported)
	case bytes.HasPrefix(data, []byte("wOFF")):
		sfntData, err := unwrapWOFF(data)
		if err != nil {
			return nil, err
		}
		data, format = sfntData, "woff"
	case bytes.HasPrefix(data, []byte("ttcf")):
		return nil, fmt.Errorf("%w: font collections", ErrUnsupported)
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("preview: parse font: %w", err)
	}
	out := &Font{Format: format, face: f, Glyphs: f.NumGlyphs(), UnitsPerEm: int(f.UnitsPerEm())}
	var buf sfnt.Buffer
	out.Family = fontName(f, &buf, sfnt.NameIDTypographicFamily, sfnt.NameIDFamily)
	out.Style = fontName(f, &buf, sfnt.NameIDTypographicSubfamily, sfnt.NameIDSubfamily)
	out.FullName = fontName(f, &buf, sfnt.NameIDFull)
	out.Version = strings.TrimSpace(strings.TrimPrefix(fontName(f, &buf, sfnt.NameIDVersion), "Version"))
	if out.UnitsPerEm > 0 {
		if m, err := f.Metrics(&buf, fixed.I(out.UnitsPerEm), font.HintingNone); err == nil {
			out.Ascent, out.Descent = m.Ascent.Round(), m.Descent.Round()
			out.LineGap = m.Height.Round() - out.Ascent - out.Descent
			out.CapHeight, out.XHeight = m.CapHeight.Round(), m.XHeight.Round()
		}
	}
	out.Coverage, out.Codepoints = coverage(f, &buf)
	out.Scripts = scriptCoverage(out.Coverage)
	return out, nil
}

func fontName(f *sfnt.Font, buf *sfnt.Buffer, ids ...sfnt.NameID) string {
	for _, id := range ids {
		if s, err := f.Name(buf, id); err == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func coverage(f *sfnt.Font, buf *sfnt.Buffer) ([]RuneRange, int) {
	var ranges []RuneRange
	count := 0
	for r := rune(0x20); r <= unicode.MaxRune; r++ {
		switch r {
		case 0x7F:
			r = 0xA0
		case 0xD800:
			r = 0xE000
		}
		if gi, err := f.GlyphIndex(buf, r); err != nil || gi == 0 {
			continue
		}
		count++
		if n := len(ranges); n > 0 && ranges[n-1].Last == r-1 {
			ranges[n-1].Last = r
		} else {
			ranges = append(ranges, RuneRange{First: r, Last: r})
		}
	}
	return ranges, count
}

func scriptCoverage(ranges []RuneRange) []ScriptCoverage {
	counts := map[string]int{}
	last := ""
	for _, rg := range ranges {
		for r := rg.First; r <= rg.Last; r++ {
			if last == "" || !inScript(last, r) {
				last = scriptOf(r)
			}
			counts[last]++
		}
	}
	out := make([]ScriptCoverage, 0, len(counts))
	for s, n := range counts {
		out = append(out, ScriptCoverage{Script: s, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Script < out[j].Script
	})
	return out
}

var scriptOrder = func() []string {
	names := make([]string, 0, len(unicode.Scripts))
	for name := range unicode.Scripts {
		if name != "Common" && name != "Inherited" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}()

func inScript(name string, r rune) bool {
	table, ok := unicode.Scripts[name]
	return ok && unicode.Is(table, r)
}

func scriptOf(r rune) string {
	switch {
	case unicode.Is(unicode.Common, r):
		return "Common"
	case unicode.Is(unicode.Inherited, r):
		return "Inherited"
	}
	for _, name := range scriptOrder {
		if unicode.Is(unicode.Scripts[name], r) {
			return name
		}
	}
	return "Unassigned"
}

type woffTable struct {
	tag, offset, compLen, origLen, checksum uint32
}

func unwrapWOFF(data []byte) ([]byte, error) {
	bad := errors.New("preview: malformed WOFF font")
	if len(data) < 44 {
		return nil, bad
	}
	flavor := binary.BigEndian.Uint32(data[4:8])
	numTables := int(binary.BigEndian.Uint16(data[12:14]))
	total := binary.BigEndian.Uint32(data[16:20])
	if numTables == 0 || numTables > 1024 || total > MaxFontBytes || 44+numTables*20 > len(data) {
		return nil, bad
	}
	tables := make([]woffTable, numTables)
	for i := range tables {
		e := data[44+i*20:]
		tables[i] = woffTable{
			tag: binary.BigEndian.Uint32(e[0:4]), offset: binary.BigEndian.Uint32(e[4:8]),
			compLen: binary.BigEndian.Uint32(e[8:12]), origLen: binary.BigEndian.Uint32(e[12:16]),
			checksum: binary.BigEndian.Uint32(e[16:20]),
		}
	}
	headerLen := 12 + 16*numTables
	out := make([]byte, headerLen, max(headerLen, int(total)))
	binary.BigEndian.PutUint32(out[0:4], flavor)
	binary.BigEndian.PutUint16(out[4:6], uint16(numTables))
	for i, t := range tables {
		if uint64(t.offset)+uint64(t.compLen) > uint64(len(data)) || t.compLen > t.origLen || len(out)+int(t.origLen) > MaxFontBytes {
			return nil, bad
		}
		raw := data[t.offset : t.offset+t.compLen]
		var table []byte
		if t.compLen < t.origLen {
			zr, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, bad
			}
			table, err = io.ReadAll(io.LimitReader(zr, int64(t.origLen)+1))
			_ = zr.Close()
			if err != nil || len(table) != int(t.origLen) {
				return nil, bad
			}
		} else {
			table = raw
		}
		rec := out[12+16*i:]
		binary.BigEndian.PutUint32(rec[0:4], t.tag)
		binary.BigEndian.PutUint32(rec[4:8], t.checksum)
		binary.BigEndian.PutUint32(rec[8:12], uint32(len(out)))
		binary.BigEndian.PutUint32(rec[12:16], t.origLen)
		out = append(out, table...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
	}
	return out, nil
}

// FontSheetOptions sizes the rendered glyph sheet.
type FontSheetOptions struct {
	Sample   string
	Size     float64
	Columns  int
	MaxCells int
}

const defaultFontSample = "The quick brown fox jumps over the lazy dog 0123456789"

// RenderFontSheet draws a sample line and a grid of the first covered glyphs
// in black on a transparent background.
func RenderFontSheet(f *Font, opts FontSheetOptions) (*image.RGBA, error) {
	if f == nil || f.face == nil {
		return nil, errors.New("preview: no font to render")
	}
	if opts.Sample == "" {
		opts.Sample = defaultFontSample
	}
	if opts.Size <= 0 {
		opts.Size = 28
	}
	if opts.Columns <= 0 {
		opts.Columns = 16
	}
	if opts.MaxCells <= 0 {
		opts.MaxCells = 256
	}
	face, err := opentype.NewFace(f.face, &opentype.FaceOptions{Size: opts.Size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("preview: font face: %w", err)
	}
	defer func() { _ = face.Close() }()
	small, err := opentype.NewFace(f.face, &opentype.FaceOptions{Size: opts.Size * 0.75, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("preview: font face: %w", err)
	}
	defer func() { _ = small.Close() }()

	glyphs := sheetRunes(f.Coverage, opts.MaxCells)
	cell := int(opts.Size * 1.6)
	pad := cell / 4
	line := face.Metrics().Height.Ceil()
	sampleW := font.MeasureString(small, opts.Sample).Ceil()
	width := max(opts.Columns*cell, sampleW) + 2*pad
	rows := (len(glyphs) + opts.Columns - 1) / opts.Columns
	height := pad*3 + line + rows*cell
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	ink := image.NewUniform(color.Black)

	d := font.Drawer{Dst: img, Src: ink, Face: small}
	d.Dot = fixed.P(pad, pad+small.Metrics().Ascent.Ceil())
	d.DrawString(opts.Sample)

	d.Face = face
	top := pad*2 + line
	ascent := face.Metrics().Ascent.Ceil()
	for i, r := range glyphs {
		x := pad + (i%opts.Columns)*cell
		y := top + (i/opts.Columns)*cell
		adv, ok := face.GlyphAdvance(r)
		if !ok {
			continue
		}
		d.Dot = fixed.P(x+(cell-adv.Ceil())/2, y+(cell-line)/2+ascent)
		d.DrawString(string(r))
	}
	return img, nil
}

func sheetRunes(ranges []RuneRange, limit int) []rune {
	var out []rune
	for _, rg := range ranges {
		for r := rg.First; r <= rg.Last && len(out) < limit; r++ {
			if unicode.IsGraphic(r) && !unicode.IsSpace(r) {
				out = append(out, r)
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

// MetricChange is one font property that differs between two versions.
type MetricChange struct {
	Name string `json:"name"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// FontDiff lists the code points a new font version gained or lost and the
// names and metrics that changed.
type FontDiff struct {
	Added        []RuneRange    `json:"added"`
	Removed      []RuneRange    `json:"removed"`
	AddedCount   int            `json:"addedCount"`
	RemovedCount int            `json:"removedCount"`
	Changes      []MetricChange `json:"changes"`
}

// CompareFonts diffs the coverage, names and metrics of two fonts.
func CompareFonts(old, new *Font) FontDiff {
	out := FontDiff{Added: []RuneRange{}, Removed: []RuneRange{}, Changes: []MetricChange{}}
	if old == nil || new == nil {
		return out
	}
	out.Added, out.AddedCount = subtractRanges(new.Coverage, old.Coverage)
	out.Removed, out.RemovedCount = subtractRanges(old.Coverage, new.Coverage)
	for _, c := range []struct {
		name     string
		old, new any
	}{
		{"Family", old.Family, new.Family}, {"Style", old.Style, new.Style}, {"Version", old.Version, new.Version},
		{"Glyphs", old.Glyphs, new.Glyphs}, {"Units per em", old.UnitsPerEm, new.UnitsPerEm},
		{"Ascent", old.Ascent, new.Ascent}, {"Descent", old.Descent, new.Descent}, {"Line gap", old.LineGap, new.LineGap},
		{"Cap height", old.CapHeight, new.CapHeight}, {"x-height", old.XHeight, new.XHeight},
	} {
		a, b := fmt.Sprint(c.old), fmt.Sprint(c.new)
		if a != b {
			out.Changes = append(out.Changes, MetricChange{Name: c.name, Old: a, New: b})
		}
	}
	return out
}

func subtractRanges(a, b []RuneRange) ([]RuneRange, int) {
	out := []RuneRange{}
	count := 0
	j := 0
	for _, rg := range a {
		cur := rg.First
		for j < len(b) && b[j].Last < cur {
			j++
		}
		for k := j; k < len(b) && b[k].First <= rg.Last && cur <= rg.Last; k++ {
			if b[k].First > cur {
				out = append(out, RuneRange{First: cur, Last: b[k].First - 1})
				count += int(b[k].First - cur)
			}
			cur = max(cur, b[k].Last+1)
		}
		if cur <= rg.Last {
			out = append(out, RuneRange{First: cur, Last: rg.Last})
			count += int(rg.Last - cur + 1)
		}
	}
	return out, count
}
