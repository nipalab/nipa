// Package assetbudget parses the versioned .nipaassets.yml asset budget file
// and checks assetdiff results against it. It is pure: callers enumerate the
// changed files and decode them with internal/assetdiff.
package assetbudget

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/nipalab/nipa/internal/pathmatch"
)

// FileName is the versioned budget file expected at the repository root.
const FileName = ".nipaassets.yml"

// Size is a byte count written as an integer or a human size (8MiB, 1.5GB).
type Size int64

var sizeUnits = map[string]float64{
	"": 1, "b": 1,
	"k": 1 << 10, "kb": 1000, "kib": 1 << 10,
	"m": 1 << 20, "mb": 1000 * 1000, "mib": 1 << 20,
	"g": 1 << 30, "gb": 1000 * 1000 * 1000, "gib": 1 << 30,
}

var sizePattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]*)$`)

// ParseSize parses an integer byte count or a number with a B, KB, MB, GB
// (decimal) or KiB, MiB, GiB, K, M, G (binary) suffix, case-insensitive.
func ParseSize(s string) (Size, error) {
	m := sizePattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	unit, ok := sizeUnits[strings.ToLower(m[2])]
	if !ok {
		return 0, fmt.Errorf("invalid size unit %q", m[2])
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	v := math.Round(n * unit)
	if v >= math.MaxInt64 {
		return 0, fmt.Errorf("size %q is too large", s)
	}
	return Size(v), nil
}

func (s *Size) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: max_bytes must be a number or a size like 8MiB", n.Line)
	}
	v, err := ParseSize(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*s = v
	return nil
}

// Rule is one budget entry. Unset limits leave earlier rules' values alone.
type Rule struct {
	Name              string   `yaml:"name" json:"name,omitempty"`
	Paths             []string `yaml:"paths" json:"paths"`
	MaxBytes          *Size    `yaml:"max_bytes" json:"max_bytes,omitempty"`
	MaxTextureSize    *int     `yaml:"max_texture_size" json:"max_texture_size,omitempty"`
	PowerOfTwo        *bool    `yaml:"power_of_two" json:"power_of_two,omitempty"`
	RequireProvenance *bool    `yaml:"require_provenance" json:"require_provenance,omitempty"`
	MaxTriangles      *int     `yaml:"max_triangles" json:"max_triangles,omitempty"`
	MaxVertices       *int     `yaml:"max_vertices" json:"max_vertices,omitempty"`
	MaxAudioSeconds   *float64 `yaml:"max_audio_seconds" json:"max_audio_seconds,omitempty"`
	MaxPeakDBFS       *float64 `yaml:"max_peak_dbfs" json:"max_peak_dbfs,omitempty"`
	NamePattern       string   `yaml:"name_pattern" json:"name_pattern,omitempty"`

	patterns *pathmatch.Patterns
	nameRE   *regexp.Regexp
}

type Config struct {
	Version int    `yaml:"version" json:"version"`
	Rules   []Rule `yaml:"rules" json:"rules"`
}

// Load reads FileName from the repository root; a missing file is a nil
// config and no error.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", FileName, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return cfg, nil
}

// Parse decodes and validates a budget file. Unknown keys are errors.
func Parse(data []byte) (*Config, error) {
	cfg := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		var te *yaml.TypeError
		if errors.As(err, &te) {
			return nil, errors.New(strings.Join(te.Errors, "; "))
		}
		return nil, err
	}
	if cfg.Version != 0 && cfg.Version != 1 {
		return nil, fmt.Errorf("unsupported version %d (expected 1)", cfg.Version)
	}
	cfg.Version = 1
	lines := ruleLines(data)
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if r.Name == "" {
			r.Name = fmt.Sprintf("rules[%d]", i)
		}
		if err := r.compile(); err != nil {
			if i < len(lines) {
				return nil, fmt.Errorf("line %d: rule %q: %w", lines[i], r.Name, err)
			}
			return nil, fmt.Errorf("rule %q: %w", r.Name, err)
		}
	}
	return cfg, nil
}

func (r *Rule) compile() error {
	r.patterns = pathmatch.Compile(r.Paths)
	if r.patterns.Empty() {
		return errors.New("paths must list at least one pattern")
	}
	if r.NamePattern != "" {
		re, err := regexp.Compile(r.NamePattern)
		if err != nil {
			return fmt.Errorf("name_pattern: %w", err)
		}
		r.nameRE = re
	}
	for _, l := range []struct {
		key string
		v   *int
	}{{LimitTextureSize, r.MaxTextureSize}, {LimitTriangles, r.MaxTriangles}, {LimitVertices, r.MaxVertices}} {
		if l.v != nil && *l.v <= 0 {
			return fmt.Errorf("%s must be positive", l.key)
		}
	}
	if r.MaxBytes != nil && *r.MaxBytes <= 0 {
		return fmt.Errorf("%s must be positive", LimitBytes)
	}
	if r.MaxAudioSeconds != nil && (*r.MaxAudioSeconds <= 0 || math.IsInf(*r.MaxAudioSeconds, 0) || math.IsNaN(*r.MaxAudioSeconds)) {
		return fmt.Errorf("%s must be positive", LimitAudioSeconds)
	}
	if r.MaxPeakDBFS != nil && (math.IsInf(*r.MaxPeakDBFS, 0) || math.IsNaN(*r.MaxPeakDBFS)) {
		return fmt.Errorf("%s must be a finite number", LimitPeakDBFS)
	}
	return nil
}

// ruleLines returns the source line of every rules entry.
func ruleLines(data []byte) []int {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != "rules" {
			continue
		}
		var lines []int
		for _, item := range m.Content[i+1].Content {
			lines = append(lines, item.Line)
		}
		return lines
	}
	return nil
}
