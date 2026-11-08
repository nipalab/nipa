package assetdiff

import (
	"context"
	"errors"

	"github.com/nipalab/nipa/internal/godotres"
)

const MaxGodotMergeBytes int64 = 64 << 20

type GodotConflict struct {
	Section string `json:"section,omitempty"`
	Key     string `json:"key,omitempty"`
	Message string `json:"message"`
}

type GodotMerge struct {
	Merged    []byte          `json:"merged,omitempty"`
	Clean     bool            `json:"clean"`
	Conflicts []GodotConflict `json:"conflicts,omitempty"`
}

// MergeError wraps a godotres parse or merge failure.
type MergeError struct{ Err error }

func (e *MergeError) Error() string { return e.Err.Error() }
func (e *MergeError) Unwrap() error { return e.Err }

// MergeGodot three-way merges a Godot text resource with godotres.Merge3. A
// nil or missing base is empty; a missing ours or theirs is a deletion the
// caller has to settle by picking a side.
func MergeGodot(ctx context.Context, load Loader, base, ours, theirs *Ref) (*GodotMerge, error) {
	var sides [3][]byte
	var present [3]bool
	for i, ref := range []*Ref{base, ours, theirs} {
		if ref == nil {
			continue
		}
		data, err := load(ctx, *ref, MaxGodotMergeBytes)
		if errors.Is(err, ErrMissing) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sides[i], present[i] = data, true
	}
	for i, label := range []string{"ours", "theirs"} {
		if !present[i+1] {
			return &GodotMerge{Conflicts: []GodotConflict{{Message: label + " deleted the file: pick ours or theirs"}}}, nil
		}
	}
	merged, conflicts, err := godotres.Merge3(sides[0], sides[1], sides[2])
	if err != nil {
		return nil, &MergeError{Err: err}
	}
	out := &GodotMerge{Merged: merged, Clean: len(conflicts) == 0}
	for _, c := range conflicts {
		out.Conflicts = append(out.Conflicts, GodotConflict{Section: c.Section, Key: c.Key, Message: c.Message})
	}
	return out, nil
}
