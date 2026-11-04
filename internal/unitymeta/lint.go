// Package unitymeta checks working-copy changes inside Unity projects for
// .meta sidecar mistakes: assets without a .meta, .meta files left without
// their asset, .meta files not pushed with their asset, and changed GUIDs.
package unitymeta

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

const maxMetaBytes = 1 << 20

type Kind string

const (
	MissingMeta   Kind = "missing-meta"
	OrphanMeta    Kind = "orphan-meta"
	MetaNotStaged Kind = "meta-not-staged"
	GUIDChanged   Kind = "guid-changed"
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

var guidLine = regexp.MustCompile(`(?m)^guid:[ \t]*([0-9A-Za-z]+)`)

// GUID returns the asset GUID recorded in a .meta file, or "".
func GUID(meta []byte) string {
	if m := guidLine.FindSubmatch(meta); m != nil {
		return string(m[1])
	}
	return ""
}

type linter struct {
	fsys     fs.FS
	oldMeta  func(string) ([]byte, error)
	states   map[string]state
	projects map[string]bool
	seen     map[string]bool
	out      []Warning
}

// Lint inspects changes against the working copy in fsys. oldMeta returns
// the last synced content of a .meta file; a nil func skips GUID checks.
func Lint(fsys fs.FS, changes []Change, oldMeta func(string) ([]byte, error)) []Warning {
	l := &linter{fsys: fsys, oldMeta: oldMeta, states: map[string]state{}, projects: map[string]bool{}, seen: map[string]bool{}}
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
		if assets, ok := l.assetsRoot(p); ok && !ignored(strings.TrimPrefix(p, assets+"/")) {
			l.check(p, assets)
		}
	}
	sort.SliceStable(l.out, func(i, j int) bool { return l.out[i].Path < l.out[j].Path })
	return l.out
}

func (l *linter) assetsRoot(p string) (string, bool) {
	parts := strings.Split(p, "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] != "Assets" {
			continue
		}
		project := strings.Join(parts[:i], "/")
		ok, cached := l.projects[project]
		if !cached {
			info, err := fs.Stat(l.fsys, path.Join(project, "ProjectSettings"))
			ok = err == nil && info.IsDir()
			l.projects[project] = ok
		}
		if ok {
			return path.Join(project, "Assets"), true
		}
	}
	return "", false
}

func ignored(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, "~") || strings.EqualFold(part, "cvs") || strings.EqualFold(path.Ext(part), ".tmp") {
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

func (l *linter) check(p, assets string) {
	s := l.states[p]
	if strings.HasSuffix(p, ".meta") {
		l.checkMeta(p, s)
		return
	}
	meta := p + ".meta"
	switch {
	case s.deleted:
		if l.exists(meta) && !l.states[meta].deleted {
			l.warn(meta, OrphanMeta, p, "left behind after its asset was deleted; delete it too")
		}
	case s.isNew():
		if !l.exists(meta) {
			l.warn(p, MissingMeta, meta, "has no .meta file; open the project in Unity so it gets one, then push both")
		} else if s.staged && !l.states[meta].staged && l.states[meta].isNew() {
			l.warn(meta, MetaNotStaged, p, "is not staged; push it together with its asset")
		}
		for dir := path.Dir(p); dir != assets && strings.HasPrefix(dir, assets+"/"); dir = path.Dir(dir) {
			if !l.exists(dir + ".meta") {
				l.warn(dir, MissingMeta, dir+".meta", "folder has no .meta file; Unity gives it a new GUID on every machine")
			}
		}
	}
}

func (l *linter) checkMeta(p string, s state) {
	asset := strings.TrimSuffix(p, ".meta")
	switch {
	case s.deleted:
		if l.exists(asset) && !l.states[asset].deleted {
			l.warn(asset, MissingMeta, p, "lost its .meta file; Unity will give it a new GUID and break references to it")
		}
	case s.isNew():
		if !l.exists(asset) {
			l.warn(p, OrphanMeta, asset, "has no asset next to it; delete it or add the asset")
		}
	case s.modified && l.oldMeta != nil:
		old, err := l.oldMeta(p)
		if err != nil {
			return
		}
		next, err := readSmall(l.fsys, p)
		if err != nil {
			return
		}
		if before, after := GUID(old), GUID(next); before != "" && after != "" && before != after {
			l.warn(p, GUIDChanged, asset, "GUID changed from "+before+" to "+after+"; references to this asset will break")
		}
	}
}

func readSmall(fsys fs.FS, p string) ([]byte, error) {
	info, err := fs.Stat(fsys, p)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxMetaBytes {
		return nil, fs.ErrInvalid
	}
	return fs.ReadFile(fsys, p)
}
