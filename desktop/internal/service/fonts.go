package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image/png"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/preview"
)

const maxFontRanges = 400

type CodeRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

type ScriptCount struct {
	Script string `json:"script"`
	Count  int    `json:"count"`
}

type FontInfo struct {
	Family     string        `json:"family"`
	Style      string        `json:"style"`
	FullName   string        `json:"fullName"`
	Version    string        `json:"version"`
	Glyphs     int           `json:"glyphs"`
	UnitsPerEm int           `json:"unitsPerEm"`
	Ascent     int           `json:"ascent"`
	Descent    int           `json:"descent"`
	LineGap    int           `json:"lineGap"`
	CapHeight  int           `json:"capHeight"`
	XHeight    int           `json:"xHeight"`
	Codepoints int           `json:"codepoints"`
	Scripts    []ScriptCount `json:"scripts"`
	Ranges     []CodeRange   `json:"ranges"`
	// RangesTruncated is set when Ranges lists only the first ranges.
	RangesTruncated bool `json:"rangesTruncated,omitempty"`
}

// FontSide is one version of a font: names, metrics, coverage and a PNG
// glyph sheet (black on transparent, as a data URL).
type FontSide struct {
	Path     string    `json:"path"`
	Format   string    `json:"format"`
	Size     int64     `json:"size"`
	Info     *FontInfo `json:"info"`
	Sheet    string    `json:"sheet"`
	Error    string    `json:"error,omitempty"`
	TooLarge bool      `json:"tooLarge,omitempty"`
}

type MetricChange struct {
	Name string `json:"name"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

type FontChanges struct {
	Added        []CodeRange    `json:"added"`
	Removed      []CodeRange    `json:"removed"`
	AddedCount   int            `json:"addedCount"`
	RemovedCount int            `json:"removedCount"`
	Metrics      []MetricChange `json:"metrics"`
	Truncated    bool           `json:"truncated,omitempty"`
}

type FontCompare struct {
	Old     *FontSide    `json:"old"`
	New     *FontSide    `json:"new"`
	Changes *FontChanges `json:"changes"`
}

// FontDiff decodes the old and/or new version of a font and, when both
// decode, lists the code points and metrics that changed.
func (s *Service) FontDiff(ctx context.Context, root string, old, new *ImageRef) (*FontCompare, error) {
	if old == nil && new == nil {
		return nil, errors.New("nothing to compare")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	out := &FontCompare{}
	var fonts [2]*preview.Font
	for i, side := range []struct {
		ref  *ImageRef
		dest **FontSide
	}{{old, &out.Old}, {new, &out.New}} {
		if side.ref == nil {
			continue
		}
		f, decoded, err := s.loadFont(ctx, client, root, side.ref)
		if err != nil {
			return nil, err
		}
		*side.dest, fonts[i] = f, decoded
	}
	if fonts[0] != nil && fonts[1] != nil {
		d := preview.CompareFonts(fonts[0], fonts[1])
		added, cutA := codeRanges(d.Added)
		removed, cutR := codeRanges(d.Removed)
		out.Changes = &FontChanges{Added: added, Removed: removed, AddedCount: d.AddedCount, RemovedCount: d.RemovedCount, Metrics: []MetricChange{}, Truncated: cutA || cutR}
		for _, c := range d.Changes {
			out.Changes.Metrics = append(out.Changes.Metrics, MetricChange(c))
		}
	}
	return out, nil
}

func (s *Service) loadFont(ctx context.Context, client daemonpb.NipaDaemonClient, root string, ref *ImageRef) (*FontSide, *preview.Font, error) {
	side := &FontSide{Path: ref.Path, Format: modelFormat(ref.Path)}
	info, data, err := s.readBlob(ctx, client, root, blobRef(ref), preview.MaxFontBytes)
	if errors.Is(err, errBlobTooLarge) {
		side.Size, side.TooLarge, side.Error = info.GetSizeBytes(), true, "the font is too large to read"
		return side, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	side.Size = int64(len(data))
	f, err := preview.DecodeFont(ref.Path, data)
	if err != nil {
		side.Error = strings.TrimPrefix(err.Error(), "preview: ")
		return side, nil, nil
	}
	side.Format = f.Format
	ranges, cut := codeRanges(f.Coverage)
	side.Info = &FontInfo{
		Family: f.Family, Style: f.Style, FullName: f.FullName, Version: f.Version, Glyphs: f.Glyphs,
		UnitsPerEm: f.UnitsPerEm, Ascent: f.Ascent, Descent: f.Descent, LineGap: f.LineGap,
		CapHeight: f.CapHeight, XHeight: f.XHeight, Codepoints: f.Codepoints, Scripts: []ScriptCount{},
		Ranges: ranges, RangesTruncated: cut,
	}
	for _, sc := range f.Scripts {
		side.Info.Scripts = append(side.Info.Scripts, ScriptCount(sc))
	}
	if img, err := preview.RenderFontSheet(f, preview.FontSheetOptions{}); err == nil {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err == nil {
			side.Sheet = "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		}
	}
	return side, f, nil
}

func codeRanges(in []preview.RuneRange) ([]CodeRange, bool) {
	out := make([]CodeRange, 0, min(len(in), maxFontRanges))
	for i, r := range in {
		if i == maxFontRanges {
			return out, true
		}
		out = append(out, CodeRange{First: int(r.First), Last: int(r.Last)})
	}
	return out, false
}
