package diff

import (
	"fmt"
	"strings"
)

// CountLines returns the number of added and removed lines between contents.
func CountLines(old, new []byte) (added, removed int) {
	for _, op := range Lines(splitLines(old), splitLines(new)) {
		switch op.Kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed
}

// NameOnly renders one changed path per line.
func NameOnly(files []FileDiff, opts Options) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Change.Path)
	}
	return opts.applyPrefix(out)
}

// NameStatus renders "<status>\t<path>" per changed file.
func NameStatus(files []FileDiff, opts Options) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Change.Status.String()+"\t"+f.Change.Path)
	}
	return opts.applyPrefix(out)
}

// NumStat renders "added\tremoved\t<path>" per changed file.
func NumStat(files []FileDiff, opts Options) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f.Change.Old.IsBinary || f.Change.New.IsBinary {
			out = append(out, "-\t-\t"+f.Change.Path)
			continue
		}
		added, removed := CountLines(f.Old, f.New)
		out = append(out, fmt.Sprintf("%d\t%d\t%s", added, removed, f.Change.Path))
	}
	return opts.applyPrefix(out)
}

// Stat renders the per-file diffstat plus the summary line.
func Stat(files []FileDiff, opts Options) []string {
	if len(files) == 0 {
		return nil
	}
	width := 0
	for _, f := range files {
		width = max(width, len(f.Change.Path))
	}
	out := make([]string, 0, len(files)+1)
	added, removed := 0, 0
	for _, f := range files {
		if f.Change.Old.IsBinary || f.Change.New.IsBinary {
			out = append(out, fmt.Sprintf(" %-*s | Bin %d -> %d bytes",
				width, f.Change.Path, f.Change.Old.SizeBytes, f.Change.New.SizeBytes))
			continue
		}
		a, r := CountLines(f.Old, f.New)
		added += a
		removed += r
		if a+r == 0 {
			out = append(out, fmt.Sprintf(" %-*s | 0", width, f.Change.Path))
			continue
		}
		out = append(out, fmt.Sprintf(" %-*s | %d %s",
			width, f.Change.Path, a+r, strings.Repeat("+", a)+strings.Repeat("-", r)))
	}
	out = append(out, fmt.Sprintf(" %d %s changed, %d %s(+), %d %s(-)",
		len(files), plural(len(files), "file", "files"),
		added, plural(added, "insertion", "insertions"),
		removed, plural(removed, "deletion", "deletions")))
	return opts.applyPrefix(out)
}

// ShortStat renders only the diffstat summary line.
func ShortStat(files []FileDiff, opts Options) []string {
	if len(files) == 0 {
		return nil
	}
	added, removed := 0, 0
	for _, f := range files {
		if f.Change.Old.IsBinary || f.Change.New.IsBinary {
			continue
		}
		a, r := CountLines(f.Old, f.New)
		added += a
		removed += r
	}
	return opts.applyPrefix([]string{fmt.Sprintf(" %d %s changed, %d %s(+), %d %s(-)",
		len(files), plural(len(files), "file", "files"),
		added, plural(added, "insertion", "insertions"),
		removed, plural(removed, "deletion", "deletions"))})
}

// Summary renders mode changes (rename/copy lines come later).
func Summary(files []FileDiff, opts Options) []string {
	var out []string
	for _, f := range files {
		if f.Change.Status == Modified && f.Change.Old.Mode != f.Change.New.Mode {
			out = append(out, fmt.Sprintf(" mode change %s => %s %s",
				modeString(f.Change.Old.Mode), modeString(f.Change.New.Mode), f.Change.Path))
		}
	}
	return opts.applyPrefix(out)
}

// Raw renders the machine-readable raw format.
func Raw(files []FileDiff, opts Options) []string {
	const zeroHash = "0000000000000000000000000000000000000000000000000000000000000000"
	out := make([]string, 0, len(files))
	for _, f := range files {
		oldHash, newHash := f.Change.Old.Hash.String(), f.Change.New.Hash.String()
		oldMode, newMode := modeString(f.Change.Old.Mode), modeString(f.Change.New.Mode)
		switch f.Change.Status {
		case Added:
			oldHash, oldMode = zeroHash, "000000"
		case Deleted:
			newHash, newMode = zeroHash, "000000"
		}
		out = append(out, fmt.Sprintf(":%s %s %s %s %s\t%s",
			oldMode, newMode, oldHash, newHash, f.Change.Status, f.Change.Path))
	}
	return opts.applyPrefix(out)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
