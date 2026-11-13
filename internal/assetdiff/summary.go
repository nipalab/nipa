package assetdiff

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/nipalab/nipa/internal/preview"
)

// Item is one changed file to summarize. Status, when set, overrides the
// status Compare derives from the loaded sides.
type Item struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
	Status  string `json:"status,omitempty"`
	Old     *Ref   `json:"old,omitempty"`
	New     *Ref   `json:"new,omitempty"`
}

type Summary struct {
	Path     string       `json:"path"`
	OldPath  string       `json:"old_path,omitempty"`
	Status   string       `json:"status"`
	Kind     preview.Kind `json:"kind"`
	Headline string       `json:"headline"`
	Error    string       `json:"error,omitempty"`
}

type Summaries struct {
	Files   []Summary `json:"files"`
	Omitted int       `json:"omitted"`
}

// Summarize compares each item and keeps one headline per file, sorted by
// path and capped at Options.MaxFiles. Only cancellation aborts it; other
// failures land in the entry's Error.
func Summarize(ctx context.Context, load Loader, items []Item, opts Options) (*Summaries, error) {
	opts = opts.withDefaults()
	opts.Images = false
	sorted := append([]Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	out := &Summaries{Files: []Summary{}}
	if len(sorted) > opts.MaxFiles {
		out.Omitted = len(sorted) - opts.MaxFiles
		sorted = sorted[:opts.MaxFiles]
	}
	for _, it := range sorted {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s := Summary{Path: it.Path, OldPath: it.OldPath, Status: it.Status, Kind: preview.Classify(it.Path, false)}
		res, err := Compare(ctx, load, it.Old, it.New, opts)
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, ErrMissing):
			s.Error = "file is missing on both sides"
		case err != nil:
			s.Error = err.Error()
		default:
			s.Kind = res.Kind
			if s.Status == "" {
				s.Status = res.Status
			}
			s.Error = res.Error
			s.Headline = Headline(res)
		}
		if s.Path == "" && res != nil {
			s.Path = res.Path
		}
		out.Files = append(out.Files, s)
	}
	return out, nil
}

// Headline renders the key numbers of a comparison as one short line.
func Headline(r *Result) string {
	if r.Status == StatusUnchanged {
		return "unchanged"
	}
	if r.Error != "" {
		return sizeLine(r)
	}
	var h string
	switch {
	case r.Scene != nil:
		h = sceneLine(r.Scene)
	case r.Image != nil:
		h = imageLine(r.Status, r.Image)
	case r.Model != nil:
		h = modelLine(r.Status, r.Model)
	case r.Audio != nil:
		h = audioLine(r.Status, r.Audio)
	case r.Table != nil:
		h = tableLine(r.Table)
	case r.Text != nil:
		h = fmt.Sprintf("+%s -%s lines", thousands(r.Text.LinesAdded), thousands(r.Text.LinesRemoved))
	}
	if h == "" {
		return sizeLine(r)
	}
	return h
}

func sizeLine(r *Result) string {
	switch r.Status {
	case StatusAdded:
		return "added, " + bytesLabel(r.NewSize)
	case StatusRemoved:
		return "removed, " + bytesLabel(r.OldSize)
	}
	return bytesLabel(r.OldSize) + " → " + bytesLabel(r.NewSize)
}

func sceneLine(s *SceneResult) string {
	var parts []string
	if s.Added > 0 {
		parts = append(parts, "+"+plural(s.Added, "object"))
	}
	if s.Removed > 0 {
		parts = append(parts, "-"+plural(s.Removed, "object"))
	}
	if s.Changed > 0 {
		parts = append(parts, fmt.Sprintf("%s changed (%s)", plural(s.Changed, "object"), plural(s.Props, "prop")))
	}
	var refsAdded, refsRemoved int
	for _, r := range s.Refs {
		if r.Status == "added" {
			refsAdded++
		} else {
			refsRemoved++
		}
	}
	if refsAdded > 0 {
		parts = append(parts, "+"+plural(refsAdded, "reference"))
	}
	if refsRemoved > 0 {
		parts = append(parts, "-"+plural(refsRemoved, "reference"))
	}
	if len(parts) == 0 {
		return "no structural changes"
	}
	return strings.Join(parts, ", ")
}

func imageLine(status string, im *ImageResult) string {
	dims := func(s *ImageSide) string { return fmt.Sprintf("%d×%d %s", s.Width, s.Height, s.Format) }
	switch {
	case status == StatusAdded && im.New != nil:
		return "added " + dims(im.New)
	case status == StatusRemoved && im.Old != nil:
		return "removed " + dims(im.Old)
	case im.Compare == nil:
		return ""
	case im.Compare.SizeChanged:
		return fmt.Sprintf("%d×%d → %d×%d", im.Old.Width, im.Old.Height, im.New.Width, im.New.Height)
	}
	line := fmt.Sprintf("%s pixels changed in %d×%d", percent(im.Compare.ChangedRatio), im.New.Width, im.New.Height)
	if im.Compare.FormatChanged {
		line += fmt.Sprintf(", %s → %s", im.Old.Format, im.New.Format)
	}
	return line
}

func modelLine(status string, m *ModelResult) string {
	one := func(s *ModelSide) string {
		return fmt.Sprintf("%s triangles, %s", thousands(s.Triangles), plural(s.Meshes, "mesh"))
	}
	switch {
	case status == StatusAdded && m.New != nil:
		return "added, " + one(m.New)
	case status == StatusRemoved && m.Old != nil:
		return "removed, " + one(m.Old)
	case m.Delta == nil:
		return ""
	}
	var parts []string
	if m.Delta.Triangles != 0 {
		parts = append(parts, "triangles "+change(m.Old.Triangles, m.New.Triangles))
	} else if m.Delta.Vertices != 0 {
		parts = append(parts, "vertices "+change(m.Old.Vertices, m.New.Vertices))
	}
	if m.Delta.Meshes != 0 {
		parts = append(parts, "meshes "+change(m.Old.Meshes, m.New.Meshes))
	}
	for _, l := range []struct {
		names []string
		sign  string
		noun  string
	}{
		{m.Delta.MaterialsAdded, "+", "material"}, {m.Delta.MaterialsRemoved, "-", "material"},
		{m.Delta.AnimationsAdded, "+", "animation"}, {m.Delta.AnimationsRemoved, "-", "animation"},
	} {
		if len(l.names) > 0 {
			parts = append(parts, l.sign+plural(len(l.names), l.noun))
		}
	}
	if m.Delta.BoundsChanged {
		parts = append(parts, "bounds changed")
	}
	if len(parts) == 0 {
		return "no geometry change"
	}
	return strings.Join(parts, ", ")
}

func audioLine(status string, a *AudioResult) string {
	one := func(s *AudioSide) string {
		return fmt.Sprintf("%.2fs %s %d Hz", s.Duration, s.Format, s.SampleRate)
	}
	switch {
	case status == StatusAdded && a.New != nil:
		return "added, " + one(a.New)
	case status == StatusRemoved && a.Old != nil:
		return "removed, " + one(a.Old)
	case a.Delta == nil:
		return ""
	}
	var parts []string
	if a.Old.Duration != a.New.Duration {
		parts = append(parts, fmt.Sprintf("duration %.2fs → %.2fs", a.Old.Duration, a.New.Duration))
	}
	if a.Delta.Loudness != nil && math.Abs(*a.Delta.Loudness) >= 0.05 {
		parts = append(parts, fmt.Sprintf("loudness %.1f → %.1f LUFS", *a.Old.Loudness, *a.New.Loudness))
	}
	if a.Delta.PeakDB != nil && math.Abs(*a.Delta.PeakDB) >= 0.05 {
		parts = append(parts, fmt.Sprintf("peak %.1f → %.1f dB", *a.Old.PeakDB, *a.New.PeakDB))
	}
	if a.New.Clipped > 0 && a.Delta.Clipped != 0 {
		parts = append(parts, plural(int(a.New.Clipped), "clipped sample"))
	}
	if a.Delta.FormatChanged {
		parts = append(parts, fmt.Sprintf("%s %d Hz → %s %d Hz", a.Old.Format, a.Old.SampleRate, a.New.Format, a.New.SampleRate))
	}
	if len(parts) == 0 {
		return "same length and levels"
	}
	return strings.Join(parts, ", ")
}

const maxSheetLines = 5

func tableLine(t *TableResult) string {
	if len(t.Sheets) == 0 {
		return countsLine(t.TableCounts)
	}
	var parts []string
	changed := 0
	for _, s := range t.Sheets {
		if s.Status == "same" {
			continue
		}
		if changed++; changed > maxSheetLines {
			continue
		}
		switch s.Status {
		case "added", "removed":
			parts = append(parts, fmt.Sprintf("sheet %q %s", s.Name, s.Status))
		default:
			parts = append(parts, fmt.Sprintf("%q: %s", s.Name, countsLine(s.TableCounts)))
		}
	}
	if changed > maxSheetLines {
		parts = append(parts, fmt.Sprintf("+%s", plural(changed-maxSheetLines, "more sheet")))
	}
	if len(parts) == 0 {
		return "no cell changes"
	}
	return strings.Join(parts, "; ")
}

func countsLine(t TableCounts) string {
	var parts []string
	if t.RowsAdded > 0 {
		parts = append(parts, "+"+plural(t.RowsAdded, "row"))
	}
	if t.RowsRemoved > 0 {
		parts = append(parts, "-"+plural(t.RowsRemoved, "row"))
	}
	if t.RowsChanged > 0 {
		parts = append(parts, fmt.Sprintf("%s changed (%s)", plural(t.RowsChanged, "row"), plural(t.CellsChanged, "cell")))
	}
	if t.RowsMoved > 0 {
		parts = append(parts, plural(t.RowsMoved, "row")+" moved")
	}
	if t.ColumnsAdded > 0 {
		parts = append(parts, "+"+plural(t.ColumnsAdded, "column"))
	}
	if t.ColumnsRemoved > 0 {
		parts = append(parts, "-"+plural(t.ColumnsRemoved, "column"))
	}
	if len(parts) == 0 {
		return "no cell changes"
	}
	return strings.Join(parts, ", ")
}

func change(a, b int) string {
	s := thousands(a) + " → " + thousands(b)
	if a > 0 {
		s += fmt.Sprintf(" (%+.0f%%)", float64(b-a)*100/float64(a))
	}
	return s
}

func percent(ratio float64) string {
	if ratio > 0 && ratio < 0.01 {
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", ratio*100)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "sh") {
		return thousands(n) + " " + noun + "es"
	}
	return thousands(n) + " " + noun + "s"
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func bytesLabel(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
