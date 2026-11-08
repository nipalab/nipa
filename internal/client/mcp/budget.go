package mcp

import (
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/output"
)

const (
	defaultDiffBytes = 256 << 10
	maxDiffBytes     = 2 << 20

	defaultLogLimit = 50
	maxLogLimit     = 500
	maxMRListLimit  = 200

	fileOverhead = 128
	hunkOverhead = 64
	lineOverhead = 32
)

func diffBudget(requested int) (int, error) {
	switch {
	case requested < 0:
		return 0, toolError(domain.NewUserError("max_bytes must not be negative"))
	case requested == 0:
		return defaultDiffBytes, nil
	case requested > maxDiffBytes:
		return maxDiffBytes, nil
	default:
		return requested, nil
	}
}

func clampLimit(requested, def, limit int) int {
	if requested <= 0 {
		return def
	}
	return min(requested, limit)
}

// capDiff keeps the diff within an estimated byte budget: hunk lines are
// trimmed once the budget runs out, and the files after that are dropped and
// counted in OmittedFiles. The first file header is always kept.
func capDiff(d output.Diff, budget int) output.Diff {
	used := 0
	for i := range d.Changes {
		f := &d.Changes[i]
		if i > 0 && used >= budget {
			d.OmittedFiles = len(d.Changes) - i
			d.Changes = d.Changes[:i]
			return d
		}
		used += fileOverhead + len(f.Path) + len(f.OldPath)
		for h := range f.Hunks {
			hunk := &f.Hunks[h]
			used += hunkOverhead
			keep := len(hunk.Lines)
			for l, line := range hunk.Lines {
				used += lineOverhead + len(line.Text)
				if used > budget {
					keep = l
					break
				}
			}
			if used > budget {
				hunk.Lines = hunk.Lines[:keep]
				f.Hunks = f.Hunks[:h+1]
				if keep == 0 {
					f.Hunks = f.Hunks[:h]
				}
				f.Truncated = true
				used = budget
				break
			}
		}
	}
	return d
}

type mrDiffFile struct {
	domain.MergeRequestDiffFile
	Truncated bool `json:"truncated,omitempty"`
}

type mrDiffOutput struct {
	Files        []mrDiffFile `json:"files"`
	OmittedFiles int          `json:"omitted_files,omitempty"`
}

func capMRDiff(files []*domain.MergeRequestDiffFile, budget int) mrDiffOutput {
	present := make([]*domain.MergeRequestDiffFile, 0, len(files))
	for _, f := range files {
		if f != nil {
			present = append(present, f)
		}
	}
	files = present
	out := mrDiffOutput{Files: make([]mrDiffFile, 0, len(files))}
	used := 0
	for i, f := range files {
		if i > 0 && used >= budget {
			out.OmittedFiles = len(files) - i
			return out
		}
		file := mrDiffFile{MergeRequestDiffFile: *f}
		used += fileOverhead + len(f.Path) + len(f.OldPath)
		for l, line := range f.Patch {
			used += lineOverhead + len(line)
			if used > budget {
				file.Patch = f.Patch[:l]
				file.Truncated = true
				used = budget
				break
			}
		}
		out.Files = append(out.Files, file)
	}
	return out
}
