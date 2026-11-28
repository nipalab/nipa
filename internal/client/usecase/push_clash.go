package usecase

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
)

// stagedClash is a path a push would leave as a file and as a folder at
// once: the server refuses such a push (usecase.buildNewTree), and this
// check says so before anything is uploaded.
type stagedClash struct {
	// path is the name that would be both.
	path string
	// left are the tracked files below path that are not staged for
	// deletion, when path is staged as a file.
	left []string
	// below is a staged file under path, when path is a file (tracked and
	// not staged for deletion, or staged itself) that is in its way.
	below string
	// staged is set when the file at path is part of this push.
	staged bool
}

// stagedClashLimit caps the clashes and the files of one clash an error
// spells out.
const stagedClashLimit = 20

// stagedTreeClashes finds the paths the tracked tree would hold as a file and
// as a folder once files are written and removed are deleted. The tracked
// tree has no such path, so every clash involves a written file: one below a
// file that stays, or one where a folder keeps tracked files.
//
// It reads the tracked rows of the written files' parent folders in one
// batch, and probes once below each written file that is not tracked yet
// (one index seek; the folder is listed only when it holds something). A
// repository that tracks nothing is not probed at all.
func stagedTreeClashes(scope *trackedScope, files, removed []string) ([]stagedClash, error) {
	if len(files) == 0 {
		return nil, nil
	}
	written := make(map[string]bool, len(files))
	for _, p := range files {
		written[p] = true
	}
	gone := make(map[string]bool, len(removed))
	for _, p := range removed {
		gone[p] = true
	}

	// Folders above a written file, each with one file that needs it.
	above := map[string]string{}
	var order []string
	for _, p := range files {
		for i := strings.LastIndexByte(p, '/'); i > 0; i = strings.LastIndexByte(p[:i], '/') {
			dir := p[:i]
			if _, seen := above[dir]; seen {
				break
			}
			above[dir] = p
			order = append(order, dir)
		}
	}

	var clashes []stagedClash
	var lookup []string
	for _, dir := range order {
		switch {
		case written[dir]:
			clashes = append(clashes, stagedClash{path: dir, below: above[dir], staged: true})
		case !gone[dir]:
			lookup = append(lookup, dir)
		}
	}

	if tracksAny, err := scope.tracksUnder(""); err != nil {
		return nil, err
	} else if tracksAny {
		if err := scope.prefetch(append(lookup, files...)); err != nil {
			return nil, err
		}
		for _, dir := range lookup {
			if _, tracked := scope.files[dir]; tracked {
				clashes = append(clashes, stagedClash{path: dir, below: above[dir]})
			}
		}
		for _, p := range files {
			if _, tracked := scope.files[p]; tracked {
				// A tracked file has no tracked file below it.
				continue
			}
			holds, err := scope.tracksUnder(p)
			if err != nil {
				return nil, err
			}
			if !holds {
				continue
			}
			under, err := scope.under(p)
			if err != nil {
				return nil, err
			}
			var left []string
			for _, q := range under {
				if !gone[q] {
					left = append(left, q)
				}
			}
			if len(left) > 0 {
				sort.Strings(left)
				clashes = append(clashes, stagedClash{path: p, left: left, staged: true})
			}
		}
	}
	sort.SliceStable(clashes, func(i, j int) bool { return clashes[i].path < clashes[j].path })
	return clashes, nil
}

// tracksUnder reports whether a tracked file lies below dir ("" is the whole
// repository), with one index seek when the repository reads by path.
func (s *trackedScope) tracksUnder(dir string) (bool, error) {
	if s.all || s.scoped == nil {
		if dir == "" {
			return len(s.files) > 0, nil
		}
		for p := range s.files {
			if strings.HasPrefix(p, dir+"/") {
				return true, nil
			}
		}
		return false, nil
	}
	return s.scoped.TracksUnder(dir)
}

// stagedClashError is the refusal of a push that would make a path both a
// file and a folder. Nothing was uploaded or changed.
func stagedClashError(clashes []stagedClash) error {
	shown := clashes
	more := ""
	if len(shown) > stagedClashLimit {
		shown = shown[:stagedClashLimit]
		more = fmt.Sprintf("\n  (and %d more)", len(clashes)-stagedClashLimit)
	}
	lines := make([]string, 0, len(shown))
	for _, c := range shown {
		lines = append(lines, "  "+c.describe())
	}
	msg := "cannot push: a path cannot be both a file and a folder\n" + strings.Join(lines, "\n") + more
	first := clashes[0]
	hint := fmt.Sprintf("stage the deletion of the tracked file %q too, or unstage what is below it", first.path)
	if len(first.left) > 0 {
		hint = fmt.Sprintf("stage the deletions of the tracked files below %q too", first.path)
	} else if first.staged {
		hint = fmt.Sprintf("unstage %q or the files staged below it", first.path)
	}
	return domain.NewUserError(msg).WithHint(hint, "nipa add "+first.path)
}

func (c stagedClash) describe() string {
	switch {
	case len(c.left) > 0:
		shown := c.left
		more := ""
		if len(shown) > stagedClashLimit {
			shown = shown[:stagedClashLimit]
			more = fmt.Sprintf(" (and %d more)", len(c.left)-stagedClashLimit)
		}
		return fmt.Sprintf("%q is staged as a file, but the tracked files below it are not staged for deletion: %s%s", c.path, strings.Join(shown, ", "), more)
	case c.staged:
		return fmt.Sprintf("%q is staged as a file and %q is staged below it", c.path, c.below)
	default:
		return fmt.Sprintf("%q is staged below %q, a tracked file that is not staged for deletion", c.below, c.path)
	}
}

// checkStagedTree refuses a classified push that would leave a path as a
// file and as a folder. It runs before the first chunk is read or uploaded.
// baseByPath is what stagedBase read: the rows of the staged paths, or the
// whole snapshot of a repository that cannot read by path; either way those
// rows are not read again.
func (p *Push) checkStagedTree(staged []string, baseByPath map[string]domain.SnapshotFile, toRead []stagedFile, removed []string) error {
	if len(toRead) == 0 {
		return nil
	}
	scope := &trackedScope{repo: p.localRepo, files: make(map[string]domain.SnapshotFile, len(baseByPath)), absent: map[string]bool{}}
	scope.scoped, _ = p.localRepo.(scopedSnapshotRepo)
	if scope.scoped == nil {
		scope.order = make([]string, 0, len(baseByPath))
		for path, f := range baseByPath {
			scope.record(f)
			scope.order = append(scope.order, path)
		}
		sort.Strings(scope.order)
		scope.all = true
	} else {
		for _, path := range staged {
			if f, tracked := baseByPath[path]; tracked {
				scope.record(f)
			} else {
				scope.absent[path] = true
			}
		}
	}
	files := make([]string, 0, len(toRead))
	for _, sf := range toRead {
		files = append(files, sf.path)
	}
	clashes, err := stagedTreeClashes(scope, files, removed)
	if err != nil {
		return err
	}
	if len(clashes) > 0 {
		return stagedClashError(clashes)
	}
	return nil
}
