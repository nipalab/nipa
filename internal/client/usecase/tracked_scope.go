package usecase

import (
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
)

// scopedSnapshotRepo is implemented by local repositories that can read part
// of the snapshot with indexed queries: single rows, one directory's rows,
// or the tree hash alone. Callers that touch a few paths use it instead of
// Snapshot, whose cost follows the size of the repository.
type scopedSnapshotRepo interface {
	SnapshotFiles(paths []string) (map[string]domain.SnapshotFile, error)
	SnapshotFilesUnder(dir string) ([]domain.SnapshotFile, error)
	TracksUnder(dir string) (bool, error)
	SnapshotTreeHash() (string, error)
}

type snapshotLoader interface {
	Snapshot() (*domain.Snapshot, error)
}

// trackedScope answers "is this path tracked, and with which row" for the
// paths a command names, reading only the rows it is asked about. A read of
// the whole repository (the root as a target, or a repository that cannot
// read by path) loads the full snapshot once and answers from memory, which
// is what every caller did before.
type trackedScope struct {
	repo   snapshotLoader
	scoped scopedSnapshotRepo

	// all is set once the whole snapshot is loaded; order then lists its
	// paths in snapshot order.
	all   bool
	order []string
	// dirs are the directories whose files are all loaded.
	dirs   []string
	files  map[string]domain.SnapshotFile
	absent map[string]bool
	// ancestors holds every loaded path and its parent directories; it is
	// built on the first protects call.
	ancestors map[string]bool
	// err is the first error a protects lookup hit; the caller checks it
	// after using the scope as an ignore-rule exemption.
	err error
}

// newTrackedScope returns the scope of repo. A repository without by-path
// reads has its snapshot loaded right away.
func newTrackedScope(repo snapshotLoader) (*trackedScope, error) {
	s := &trackedScope{repo: repo, files: map[string]domain.SnapshotFile{}, absent: map[string]bool{}}
	s.scoped, _ = repo.(scopedSnapshotRepo)
	if s.scoped == nil {
		if err := s.loadAll(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *trackedScope) record(f domain.SnapshotFile) {
	s.files[f.Path] = f
	if s.ancestors != nil {
		addWithAncestors(s.ancestors, f.Path)
	}
}

// addWithAncestors adds p and each of its parent directories to set.
func addWithAncestors(set map[string]bool, p string) {
	if p == "" {
		return
	}
	set[p] = true
	for i := strings.LastIndexByte(p, '/'); i > 0; i = strings.LastIndexByte(p[:i], '/') {
		set[p[:i]] = true
	}
}

func (s *trackedScope) loadAll() error {
	if s.all {
		return nil
	}
	snapshot, err := s.repo.Snapshot()
	if err != nil {
		return err
	}
	s.order = make([]string, 0, len(snapshot.Files))
	for _, f := range snapshot.Files {
		s.record(f)
		s.order = append(s.order, f.Path)
	}
	s.all = true
	return nil
}

// covers reports whether every tracked file at path or below it is loaded.
func (s *trackedScope) covers(path string) bool {
	if s.all {
		return true
	}
	for _, dir := range s.dirs {
		if strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

// under loads the tracked files below the directory dir and returns their
// paths; "" is the whole repository.
func (s *trackedScope) under(dir string) ([]string, error) {
	if dir == "" || s.scoped == nil {
		if err := s.loadAll(); err != nil {
			return nil, err
		}
	}
	if s.all {
		if dir == "" {
			return s.order, nil
		}
		var paths []string
		for _, p := range s.order {
			if strings.HasPrefix(p, dir+"/") {
				paths = append(paths, p)
			}
		}
		return paths, nil
	}
	rows, err := s.scoped.SnapshotFilesUnder(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(rows))
	for _, f := range rows {
		s.record(f)
		paths = append(paths, f.Path)
	}
	s.dirs = append(s.dirs, dir)
	return paths, nil
}

// prefetch loads the rows of paths that are not known yet, in batches.
func (s *trackedScope) prefetch(paths []string) error {
	if s.all {
		return nil
	}
	var unknown []string
	for _, p := range paths {
		if _, ok := s.files[p]; ok || s.absent[p] || s.covers(p) {
			continue
		}
		unknown = append(unknown, p)
	}
	if len(unknown) == 0 {
		return nil
	}
	rows, err := s.scoped.SnapshotFiles(unknown)
	if err != nil {
		return err
	}
	for _, p := range unknown {
		if f, ok := rows[p]; ok {
			s.record(f)
		} else {
			s.absent[p] = true
		}
	}
	return nil
}

// get returns the tracked file at path; ok is false when path is untracked.
func (s *trackedScope) get(path string) (domain.SnapshotFile, bool, error) {
	if f, ok := s.files[path]; ok {
		return f, true, nil
	}
	if s.all || s.absent[path] || s.covers(path) {
		return domain.SnapshotFile{}, false, nil
	}
	if err := s.prefetch([]string{path}); err != nil {
		return domain.SnapshotFile{}, false, err
	}
	f, ok := s.files[path]
	return f, ok, nil
}

// protects reports whether rel is a tracked file or a directory that holds
// one: tracking wins over the ignore rules. A failed lookup is kept in err
// and answers true, so nothing is reported as ignored on the way to the
// error.
func (s *trackedScope) protects(rel string) bool {
	if rel == "" {
		return false
	}
	if s.ancestors == nil {
		s.ancestors = make(map[string]bool, len(s.files)*2)
		for p := range s.files {
			addWithAncestors(s.ancestors, p)
		}
	}
	if s.ancestors[rel] {
		return true
	}
	if s.all {
		return false
	}
	if _, tracked, err := s.get(rel); err != nil {
		s.err = err
		return true
	} else if tracked {
		return true
	}
	if s.covers(rel + "/") {
		// rel is a loaded directory or lies inside one: ancestors is complete.
		return false
	}
	holds, err := s.scoped.TracksUnder(rel)
	if err != nil {
		s.err = err
		return true
	}
	return holds
}

// untracked returns the paths that are not tracked, in order.
func (s *trackedScope) untracked(paths []string) ([]string, error) {
	if err := s.prefetch(paths); err != nil {
		return nil, err
	}
	var out []string
	for _, p := range paths {
		if _, ok := s.files[p]; !ok {
			out = append(out, p)
		}
	}
	return out, nil
}
