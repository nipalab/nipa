package output

import (
	"strings"

	"github.com/nipalab/nipa/internal/diff"
)

type Diff struct {
	Changes []DiffFile `json:"changes"`
}

type DiffFile struct {
	Path               string     `json:"path"`
	OldPath            string     `json:"old_path,omitempty"`
	Status             string     `json:"status"`
	Binary             bool       `json:"binary,omitempty"`
	Similarity         int        `json:"similarity,omitempty"`
	Old                *DiffEntry `json:"old,omitempty"`
	New                *DiffEntry `json:"new,omitempty"`
	ContentUnavailable bool       `json:"content_unavailable,omitempty"`
	Hunks              []DiffHunk `json:"hunks"`
}

type DiffEntry struct {
	Mode      string `json:"mode"`
	SizeBytes int64  `json:"size_bytes"`
	Hash      string `json:"hash"`
	Encoding  string `json:"encoding,omitempty"`
}

type DiffHunk struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Lines    []DiffLine `json:"lines"`
}

type DiffLine struct {
	Kind      string `json:"kind"`
	Old       int    `json:"old,omitempty"`
	New       int    `json:"new,omitempty"`
	Text      string `json:"text"`
	NoNewline bool   `json:"no_newline,omitempty"`
}

func NewDiff(files []diff.FileDiff, opts diff.Options) Diff {
	out := Diff{Changes: make([]DiffFile, 0, len(files))}
	for _, f := range files {
		out.Changes = append(out.Changes, newDiffFile(f, opts))
	}
	return out
}

func newDiffFile(f diff.FileDiff, opts diff.Options) DiffFile {
	c := f.Change
	file := DiffFile{
		Path:               c.Path,
		Status:             c.Status.String(),
		Binary:             c.Old.IsBinary || c.New.IsBinary,
		Similarity:         c.Similarity,
		ContentUnavailable: f.OldUnavailable || f.NewUnavailable,
		Hunks:              []DiffHunk{},
	}
	if c.Status == diff.Renamed {
		file.OldPath = c.Old.Path
	}
	if c.Status != diff.Added {
		file.Old = newDiffEntry(c.Old)
	}
	if c.Status != diff.Deleted {
		file.New = newDiffEntry(c.New)
	}
	for _, h := range diff.FileHunks(f, opts) {
		file.Hunks = append(file.Hunks, newDiffHunk(h))
	}
	return file
}

func newDiffEntry(e diff.Entry) *DiffEntry {
	return &DiffEntry{
		Mode:      diff.ModeString(e.Mode),
		SizeBytes: e.SizeBytes,
		Hash:      e.Hash.String(),
		Encoding:  e.Encoding,
	}
}

func newDiffHunk(h diff.Hunk) DiffHunk {
	out := DiffHunk{
		OldStart: h.OldStart,
		OldLines: h.OldLines,
		NewStart: h.NewStart,
		NewLines: h.NewLines,
		Lines:    make([]DiffLine, 0, len(h.Lines)),
	}
	for _, line := range h.Lines {
		out.Lines = append(out.Lines, DiffLine{
			Kind:      lineKind(line.Kind),
			Old:       line.Old,
			New:       line.New,
			Text:      strings.TrimSuffix(line.Text, "\n"),
			NoNewline: line.NoNewline,
		})
	}
	return out
}

func lineKind(kind byte) string {
	switch kind {
	case '+':
		return "add"
	case '-':
		return "delete"
	default:
		return "context"
	}
}
