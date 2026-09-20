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

// displayPath renders the path as git does for renames.
func displayPath(c Change) string {
	if c.Status == Renamed {
		return c.Old.Path + " => " + c.Path
	}
	return c.Path
}

// NameOnly renders one changed path per line.
func NameOnly(files []FileDiff) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Change.Path)
	}
	return out
}

// NameStatus renders "<status>\t<path>" per changed file. Renames use git's
// "<status><score>\t<old>\t<new>" form.
func NameStatus(files []FileDiff) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		c := f.Change
		if c.Status == Renamed {
			out = append(out, fmt.Sprintf("R%d\t%s\t%s", c.Similarity, c.Old.Path, c.Path))
			continue
		}
		out = append(out, c.Status.String()+"\t"+c.Path)
	}
	return out
}

// Stat renders the per-file diffstat plus the summary line.
func Stat(files []FileDiff) []string {
	if len(files) == 0 {
		return nil
	}
	paths := make([]string, len(files))
	width := 0
	for i, f := range files {
		paths[i] = displayPath(f.Change)
		width = max(width, len(paths[i]))
	}
	out := make([]string, 0, len(files)+1)
	added, removed := 0, 0
	for i, f := range files {
		if f.Change.Old.IsBinary || f.Change.New.IsBinary {
			out = append(out, fmt.Sprintf(" %-*s | Bin %d -> %d bytes",
				width, paths[i], f.Change.Old.SizeBytes, f.Change.New.SizeBytes))
			continue
		}
		a, r := CountLines(f.Old, f.New)
		added += a
		removed += r
		if a+r == 0 {
			out = append(out, fmt.Sprintf(" %-*s | 0", width, paths[i]))
			continue
		}
		out = append(out, fmt.Sprintf(" %-*s | %d %s",
			width, paths[i], a+r, strings.Repeat("+", a)+strings.Repeat("-", r)))
	}
	out = append(out, fmt.Sprintf(" %d %s changed, %d %s(+), %d %s(-)",
		len(files), plural(len(files), "file", "files"),
		added, plural(added, "insertion", "insertions"),
		removed, plural(removed, "deletion", "deletions")))
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
