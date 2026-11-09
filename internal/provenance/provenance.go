// Package provenance reads and writes the versioned .nipaprovenance.yml file
// that records where generated assets came from, and extracts declared
// generator metadata (PNG text chunks, C2PA labels) from asset bytes. Records
// are declarations, not proof: C2PA signatures are never verified, and
// extracted text is untrusted data. Raw prompts are never kept, only their
// SHA-256. The package is pure and must not import client packages.
package provenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zeebo/blake3"
	"go.yaml.in/yaml/v3"

	"github.com/nipalab/nipa/internal/domain"
)

const FileName = ".nipaprovenance.yml"

const (
	SourcePNGText = "png-text"
	SourceC2PA    = "c2pa"
	SourceManual  = "manual"
)

const (
	StatusRecorded = "recorded"
	StatusModified = "modified"
	StatusEdited   = "edited"
	StatusMissing  = "missing"
)

const hashPrefix = "blake3:"

var (
	contentHashPattern = regexp.MustCompile(`^blake3:[0-9a-f]{64}$`)
	sha256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Record struct {
	ContentHash       string    `yaml:"content_hash,omitempty" json:"content_hash,omitempty"`
	Tool              string    `yaml:"tool,omitempty" json:"tool,omitempty"`
	Model             string    `yaml:"model,omitempty" json:"model,omitempty"`
	PromptSHA256      string    `yaml:"prompt_sha256,omitempty" json:"prompt_sha256,omitempty"`
	Seed              *uint64   `yaml:"seed,omitempty" json:"seed,omitempty"`
	License           string    `yaml:"license,omitempty" json:"license,omitempty"`
	HumanEdited       bool      `yaml:"human_edited" json:"human_edited"`
	DigitalSourceType string    `yaml:"digital_source_type,omitempty" json:"digital_source_type,omitempty"`
	Source            string    `yaml:"source,omitempty" json:"source,omitempty"`
	C2PAVerified      *bool     `yaml:"c2pa_verified,omitempty" json:"c2pa_verified,omitempty"`
	RecordedAt        time.Time `yaml:"recorded_at,omitempty" json:"recorded_at,omitzero"`
	Note              string    `yaml:"note,omitempty" json:"note,omitempty"`
}

type File struct {
	Version int                `yaml:"version"`
	Files   map[string]*Record `yaml:"files"`
}

func New() *File {
	return &File{Version: 1, Files: map[string]*Record{}}
}

// Parse decodes and validates a provenance file. Unknown keys are errors.
func Parse(data []byte) (*File, error) {
	f := &File{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(f); err != nil && !errors.Is(err, io.EOF) {
		var te *yaml.TypeError
		if errors.As(err, &te) {
			return nil, errors.New(strings.Join(te.Errors, "; "))
		}
		return nil, err
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("unsupported version %d (expected 1)", f.Version)
	}
	files := make(map[string]*Record, len(f.Files))
	for raw, rec := range f.Files {
		p, err := NormalizePath(raw)
		if err != nil {
			return nil, err
		}
		if _, dup := files[p]; dup {
			return nil, fmt.Errorf("duplicate record for %q", p)
		}
		if rec == nil {
			rec = &Record{}
		}
		if err := rec.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		files[p] = rec
	}
	f.Files = files
	return f, nil
}

// Validate checks the field formats of one record.
func (r *Record) Validate() error {
	switch r.Source {
	case "", SourcePNGText, SourceC2PA, SourceManual:
	default:
		return fmt.Errorf("unknown source %q (expected png-text, c2pa or manual)", r.Source)
	}
	if r.ContentHash != "" && !contentHashPattern.MatchString(r.ContentHash) {
		return fmt.Errorf("invalid content_hash %q", r.ContentHash)
	}
	if r.PromptSHA256 != "" && !sha256Pattern.MatchString(r.PromptSHA256) {
		return fmt.Errorf("invalid prompt_sha256 %q", r.PromptSHA256)
	}
	if r.C2PAVerified != nil && r.Source != SourceC2PA {
		return errors.New("c2pa_verified requires source: c2pa")
	}
	return nil
}

// NormalizePath returns the repo-relative slash path of p.
func NormalizePath(p string) (string, error) {
	n, err := domain.NormalizePathPrefix(p)
	if err != nil {
		return "", err
	}
	if n == "" {
		return "", fmt.Errorf("invalid path %q", p)
	}
	return n, nil
}

// Marshal writes the file with sorted paths and a fixed field order, so
// disjoint additions merge cleanly as text.
func (f *File) Marshal() []byte {
	var buf bytes.Buffer
	buf.WriteString("version: 1\n")
	if len(f.Files) == 0 {
		buf.WriteString("files: {}\n")
		return buf.Bytes()
	}
	buf.WriteString("files:\n")
	for _, p := range f.Paths() {
		node := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: p},
			recordNode(f.Files[p]),
		}}
		var out bytes.Buffer
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(2)
		_ = enc.Encode(node)
		_ = enc.Close()
		for _, line := range strings.SplitAfter(out.String(), "\n") {
			if line != "" {
				buf.WriteString("  " + line)
			}
		}
	}
	return buf.Bytes()
}

func recordNode(r *Record) *yaml.Node {
	var n yaml.Node
	if r == nil {
		r = &Record{}
	}
	cp := *r
	cp.RecordedAt = cp.RecordedAt.UTC()
	_ = n.Encode(&cp)
	return &n
}

// Paths returns the recorded paths in sorted order.
func (f *File) Paths() []string {
	out := make([]string, 0, len(f.Files))
	for p := range f.Files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (f *File) ensure() {
	if f.Files == nil {
		f.Files = map[string]*Record{}
	}
	if f.Version == 0 {
		f.Version = 1
	}
}

func (f *File) Get(p string) *Record {
	n, err := NormalizePath(p)
	if err != nil {
		return nil
	}
	return f.Files[n]
}

func (f *File) Set(p string, r *Record) error {
	n, err := NormalizePath(p)
	if err != nil {
		return err
	}
	if r == nil {
		return errors.New("nil record")
	}
	if err := r.Validate(); err != nil {
		return err
	}
	f.ensure()
	f.Files[n] = r
	return nil
}

// Delete removes the record of p and reports whether one existed.
func (f *File) Delete(p string) bool {
	n, err := NormalizePath(p)
	if err != nil {
		return false
	}
	if _, ok := f.Files[n]; !ok {
		return false
	}
	delete(f.Files, n)
	return true
}

// Move renames the record of oldPath to newPath, replacing any record there.
func (f *File) Move(oldPath, newPath string) bool {
	o, err := NormalizePath(oldPath)
	if err != nil {
		return false
	}
	n, err := NormalizePath(newPath)
	if err != nil {
		return false
	}
	rec, ok := f.Files[o]
	if !ok {
		return false
	}
	delete(f.Files, o)
	f.Files[n] = rec
	return true
}

// HashContent returns "blake3:<hex>" of the raw bytes of r.
func HashContent(r io.Reader) (string, error) {
	h := blake3.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hashPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

func HashBytes(data []byte) string {
	sum := blake3.Sum256(data)
	return hashPrefix + hex.EncodeToString(sum[:])
}

// PromptHash is the SHA-256 hex of a prompt; the prompt itself is never kept.
func PromptHash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

// Status compares a record with the current content hash.
func Status(rec *Record, currentHash string) string {
	switch {
	case rec == nil:
		return StatusMissing
	case rec.ContentHash == "" || rec.ContentHash == currentHash:
		return StatusRecorded
	case rec.HumanEdited:
		return StatusEdited
	default:
		return StatusModified
	}
}
