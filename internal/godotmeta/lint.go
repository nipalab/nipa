// Package godotmeta checks working-copy changes inside Godot projects for
// .import/.uid sidecar mistakes: assets without their sidecar, sidecars left
// without their asset, sidecars not pushed with their asset, and changed UIDs.
package godotmeta

import (
	"io/fs"
	"path"
	"sort"
	"strings"
)

const (
	maxSidecarBytes = 1 << 20
	projectFile     = "project.godot"
	ignoreFile      = ".gdignore"
)

type Kind string

const (
	MissingSidecar   Kind = "missing-sidecar"
	OrphanSidecar    Kind = "orphan-sidecar"
	SidecarNotStaged Kind = "sidecar-not-staged"
	UIDChanged       Kind = "uid-changed"
)

// Change is one entry of the working-copy status. A path can be listed twice,
// once staged and once with further unstaged edits.
type Change struct {
	Path   string
	Status string // added, modified, deleted, untracked
	Staged bool
}

type Warning struct {
	Path    string
	Kind    Kind
	Related string
	Message string
}

type state struct {
	added, modified, deleted, untracked, staged bool
}

func (s state) isNew() bool { return s.added || s.untracked }

func (s state) pending() bool { return s.isNew() || s.modified || s.deleted }

type linter struct {
	fsys       fs.FS
	oldSidecar func(string) ([]byte, error)
	states     map[string]state
	projects   map[string]bool
	ignores    map[string]bool
	seen       map[string]bool
	out        []Warning
}

// Lint inspects changes against the working copy in fsys. oldSidecar returns
// the last synced content of a sidecar; a nil func skips UID checks.
func Lint(fsys fs.FS, changes []Change, oldSidecar func(string) ([]byte, error)) []Warning {
	l := &linter{fsys: fsys, oldSidecar: oldSidecar, states: map[string]state{}, projects: map[string]bool{}, ignores: map[string]bool{}, seen: map[string]bool{}}
	for _, c := range changes {
		s := l.states[c.Path]
		switch c.Status {
		case "added":
			s.added = true
		case "modified":
			s.modified = true
		case "deleted":
			s.deleted = true
		case "untracked":
			s.untracked = true
		}
		s.staged = s.staged || c.Staged
		l.states[c.Path] = s
	}
	paths := make([]string, 0, len(l.states))
	for p := range l.states {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if root, ok := l.projectRoot(p); ok && !l.ignored(root, p) {
			l.check(p)
		}
	}
	sort.SliceStable(l.out, func(i, j int) bool { return l.out[i].Path < l.out[j].Path })
	return l.out
}

func (l *linter) projectRoot(p string) (string, bool) {
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		ok, cached := l.projects[dir]
		if !cached {
			info, err := fs.Stat(l.fsys, path.Join(dir, projectFile))
			ok = err == nil && info.Mode().IsRegular()
			l.projects[dir] = ok
		}
		if ok {
			return dir, true
		}
		if dir == "." || dir == "/" {
			return "", false
		}
	}
}

func (l *linter) ignored(root, p string) bool {
	rel := p
	if root != "." {
		rel = strings.TrimPrefix(p, root+"/")
	}
	dir := root
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ".") {
			return true
		}
		if i == len(parts)-1 {
			break
		}
		dir = path.Join(dir, part)
		skip, cached := l.ignores[dir]
		if !cached {
			skip = l.exists(path.Join(dir, ignoreFile))
			l.ignores[dir] = skip
		}
		if skip {
			return true
		}
	}
	return false
}

func (l *linter) exists(p string) bool {
	_, err := fs.Stat(l.fsys, p)
	return err == nil
}

func (l *linter) warn(p string, kind Kind, related, message string) {
	key := p + "\x00" + string(kind)
	if l.seen[key] {
		return
	}
	l.seen[key] = true
	l.out = append(l.out, Warning{Path: p, Kind: kind, Related: related, Message: message})
}

func (l *linter) check(p string) {
	if IsSidecar(p) {
		l.checkSidecar(p)
		return
	}
	s := l.states[p]
	for _, sc := range Sidecars(p) {
		ss := l.states[sc]
		present := l.exists(sc)
		switch {
		case s.deleted:
			if present && !ss.deleted {
				l.warn(sc, OrphanSidecar, p, "left behind after its asset was deleted; delete it too")
			} else if s.staged && ss.deleted && !ss.staged {
				l.warn(sc, SidecarNotStaged, p, "deletion is not staged; push it together with its asset")
			}
		case s.staged && present && ss.pending() && !ss.staged:
			l.warn(sc, SidecarNotStaged, p, "is not staged; push it together with its asset")
		}
	}
	if sc, ok := Sidecar(p); ok && s.isNew() && !l.exists(sc) {
		l.warn(p, MissingSidecar, sc, "has no "+path.Ext(sc)+" file; open the project in Godot so it gets one, then push both")
	}
}

func (l *linter) checkSidecar(p string) {
	s := l.states[p]
	asset := assetOf(p)
	switch {
	case s.deleted:
		if l.exists(asset) && !l.states[asset].deleted {
			l.warn(asset, MissingSidecar, p, "lost its "+path.Ext(p)+" file; Godot will give it a new UID and break uid:// references to it")
		} else if as := l.states[asset]; s.staged && as.deleted && !as.staged {
			l.warn(asset, SidecarNotStaged, p, "deletion is not staged; push it together with its "+path.Ext(p)+" deletion")
		}
	case s.isNew():
		if !l.exists(asset) {
			l.warn(p, OrphanSidecar, asset, "has no asset next to it; delete it or add the asset")
		}
	case s.modified && l.oldSidecar != nil:
		old, err := l.oldSidecar(p)
		if err != nil {
			return
		}
		next, err := readSmall(l.fsys, p)
		if err != nil {
			return
		}
		if before, after := UID(old), UID(next); before != "" && after != "" && before != after {
			l.warn(p, UIDChanged, asset, "UID changed from "+before+" to "+after+"; uid:// references to this asset will break")
		}
	}
}

func readSmall(fsys fs.FS, p string) ([]byte, error) {
	info, err := fs.Stat(fsys, p)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSidecarBytes {
		return nil, fs.ErrInvalid
	}
	return fs.ReadFile(fsys, p)
}
