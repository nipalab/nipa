package assetbudget

import (
	"fmt"
	"path"
	"regexp"
	"sort"

	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/preview"
)

const (
	LimitBytes        = "max_bytes"
	LimitTextureSize  = "max_texture_size"
	LimitPowerOfTwo   = "power_of_two"
	LimitTriangles    = "max_triangles"
	LimitVertices     = "max_vertices"
	LimitAudioSeconds = "max_audio_seconds"
	LimitPeakDBFS     = "max_peak_dbfs"
	LimitNamePattern  = "name_pattern"
	LimitProvenance   = "require_provenance"
)

// Violation is one limit a file breaks; Rule names the rule that set it.
type Violation struct {
	Path    string `json:"path"`
	Rule    string `json:"rule"`
	Limit   string `json:"limit"`
	Actual  any    `json:"actual"`
	Max     any    `json:"max"`
	Message string `json:"message"`
}

type setting[T any] struct {
	value T
	rule  string
	set   bool
}

func (s *setting[T]) apply(v *T, rule string) {
	if v != nil {
		s.value, s.rule, s.set = *v, rule, true
	}
}

// Limits is the merged budget of one path: every matching rule applies in
// file order, and a later rule overrides the keys it sets.
type Limits struct {
	bytes       setting[Size]
	textureSize setting[int]
	powerOfTwo  setting[bool]
	triangles   setting[int]
	vertices    setting[int]
	seconds     setting[float64]
	peak        setting[float64]
	name        setting[*regexp.Regexp]
	provenance  setting[bool]
	matched     bool
}

// For merges the limits of every rule matching path.
func (c *Config) For(p string) Limits {
	var l Limits
	if c == nil {
		return l
	}
	for i := range c.Rules {
		r := &c.Rules[i]
		if !r.patterns.Matches(p, false) {
			continue
		}
		l.matched = true
		l.bytes.apply(r.MaxBytes, r.Name)
		l.textureSize.apply(r.MaxTextureSize, r.Name)
		l.powerOfTwo.apply(r.PowerOfTwo, r.Name)
		l.triangles.apply(r.MaxTriangles, r.Name)
		l.vertices.apply(r.MaxVertices, r.Name)
		l.seconds.apply(r.MaxAudioSeconds, r.Name)
		l.peak.apply(r.MaxPeakDBFS, r.Name)
		l.provenance.apply(r.RequireProvenance, r.Name)
		if r.nameRE != nil {
			l.name.apply(&r.nameRE, r.Name)
		}
	}
	return l
}

// Matched reports whether any rule covers the path.
func (l Limits) Matched() bool { return l.matched }

// NeedsDecode reports whether checking a file of this kind needs decoded
// metadata (image, model or audio limits), beyond its size and name.
func (l Limits) NeedsDecode(kind preview.Kind) bool {
	switch kind {
	case preview.KindImage:
		return l.textureSize.set || (l.powerOfTwo.set && l.powerOfTwo.value)
	case preview.KindModel:
		return l.triangles.set || l.vertices.set
	case preview.KindAudio:
		return l.seconds.set || l.peak.set
	}
	return false
}

// Covers reports whether any rule matches path.
func (c *Config) Covers(p string) bool { return c.For(p).Matched() }

// NeedsDecode reports whether path needs a decoded assetdiff result, judging
// its kind by extension.
func (c *Config) NeedsDecode(p string) bool {
	return c.For(p).NeedsDecode(preview.Classify(p, false))
}

// CheckOptions carries the facts a check needs beyond the results.
type CheckOptions struct {
	// HasRecord reports whether a path has a provenance record; nil means
	// no file has one.
	HasRecord func(path string) bool
}

// RequiresProvenance reports whether any rule asks path for a provenance
// record.
func (c *Config) RequiresProvenance(p string) bool {
	l := c.For(p)
	return l.provenance.set && l.provenance.value
}

// Check evaluates the new side of every result against the budget, sorted
// by path and limit. Removed files are skipped, and results without decoded
// metadata (too large, unparseable) are only checked for size and name.
func Check(cfg *Config, results []*assetdiff.Result) []Violation {
	return CheckWith(cfg, results, CheckOptions{})
}

// CheckWith is Check with provenance records for require_provenance.
func CheckWith(cfg *Config, results []*assetdiff.Result, opts CheckOptions) []Violation {
	out := []Violation{}
	if cfg == nil {
		return out
	}
	for _, res := range results {
		if res == nil || res.Status == assetdiff.StatusRemoved {
			continue
		}
		l := cfg.For(res.Path)
		out = append(out, l.check(res)...)
		if l.provenance.set && l.provenance.value && (opts.HasRecord == nil || !opts.HasRecord(res.Path)) {
			out = append(out, Violation{Path: res.Path, Rule: l.provenance.rule, Limit: LimitProvenance, Actual: false, Max: true, Message: "no provenance record"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Limit < out[j].Limit
	})
	return out
}

func (l Limits) check(res *assetdiff.Result) []Violation {
	var out []Violation
	add := func(rule, limit string, actual, max any, format string, args ...any) {
		out = append(out, Violation{Path: res.Path, Rule: rule, Limit: limit, Actual: actual, Max: max, Message: fmt.Sprintf(format, args...)})
	}
	if l.bytes.set && res.NewSize > int64(l.bytes.value) {
		add(l.bytes.rule, LimitBytes, res.NewSize, int64(l.bytes.value), "%s exceeds %s", humanSize(res.NewSize), humanSize(int64(l.bytes.value)))
	}
	if l.name.set {
		if base := path.Base(res.Path); !l.name.value.MatchString(base) {
			add(l.name.rule, LimitNamePattern, base, l.name.value.String(), "name %q does not match %s", base, l.name.value.String())
		}
	}
	if res.Image != nil && res.Image.New != nil {
		img := res.Image.New
		if longest := max(img.Width, img.Height); l.textureSize.set && longest > l.textureSize.value {
			add(l.textureSize.rule, LimitTextureSize, longest, l.textureSize.value, "%dx%d exceeds %dpx", img.Width, img.Height, l.textureSize.value)
		}
		if l.powerOfTwo.set && l.powerOfTwo.value && (!powerOfTwo(img.Width) || !powerOfTwo(img.Height)) {
			add(l.powerOfTwo.rule, LimitPowerOfTwo, fmt.Sprintf("%dx%d", img.Width, img.Height), true, "%dx%d is not a power of two", img.Width, img.Height)
		}
	}
	if res.Model != nil && res.Model.New != nil {
		m := res.Model.New
		if l.triangles.set && m.Triangles > l.triangles.value {
			add(l.triangles.rule, LimitTriangles, m.Triangles, l.triangles.value, "%d triangles exceed %d", m.Triangles, l.triangles.value)
		}
		if l.vertices.set && m.Vertices > l.vertices.value {
			add(l.vertices.rule, LimitVertices, m.Vertices, l.vertices.value, "%d vertices exceed %d", m.Vertices, l.vertices.value)
		}
	}
	if res.Audio != nil && res.Audio.New != nil {
		a := res.Audio.New
		if l.seconds.set && a.Duration > l.seconds.value {
			add(l.seconds.rule, LimitAudioSeconds, a.Duration, l.seconds.value, "%.2fs exceeds %gs", a.Duration, l.seconds.value)
		}
		if l.peak.set && a.PeakDB != nil && *a.PeakDB > l.peak.value {
			add(l.peak.rule, LimitPeakDBFS, *a.PeakDB, l.peak.value, "peak %.2f dBFS exceeds %g dBFS", *a.PeakDB, l.peak.value)
		}
	}
	return out
}

func powerOfTwo(n int) bool {
	return n > 0 && n&(n-1) == 0
}

func humanSize(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
