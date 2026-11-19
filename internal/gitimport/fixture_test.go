package gitimport

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"
)

type entry struct {
	mode    filemode.FileMode
	content string
	hash    plumbing.Hash
}

func reg(s string) entry  { return entry{mode: filemode.Regular, content: s} }
func exe(s string) entry  { return entry{mode: filemode.Executable, content: s} }
func link(s string) entry { return entry{mode: filemode.Symlink, content: s} }
func gitlink(h string) entry {
	return entry{mode: filemode.Submodule, hash: plumbing.NewHash(h)}
}

type fixture struct {
	t     *testing.T
	dir   string
	repo  *git.Repository
	clock time.Time
}

func newFixture(t *testing.T, bare bool) *fixture {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, bare)
	require.NoError(t, err)
	return &fixture{t: t, dir: dir, repo: repo, clock: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (f *fixture) source() *Source {
	src, err := Open(f.dir)
	require.NoError(f.t, err)
	return src
}

func (f *fixture) store(o object.Object) plumbing.Hash {
	obj := f.repo.Storer.NewEncodedObject()
	require.NoError(f.t, o.Encode(obj))
	h, err := f.repo.Storer.SetEncodedObject(obj)
	require.NoError(f.t, err)
	return h
}

func (f *fixture) blob(content string) plumbing.Hash {
	obj := f.repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	require.NoError(f.t, err)
	_, err = w.Write([]byte(content))
	require.NoError(f.t, err)
	require.NoError(f.t, w.Close())
	h, err := f.repo.Storer.SetEncodedObject(obj)
	require.NoError(f.t, err)
	return h
}

func (f *fixture) tree(files map[string]entry) plumbing.Hash {
	direct := map[string]entry{}
	nested := map[string]map[string]entry{}
	for p, e := range files {
		if i := strings.IndexByte(p, '/'); i >= 0 {
			dir := p[:i]
			if nested[dir] == nil {
				nested[dir] = map[string]entry{}
			}
			nested[dir][p[i+1:]] = e
			continue
		}
		direct[p] = e
	}
	var entries []object.TreeEntry
	for name, e := range direct {
		h := e.hash
		if e.mode != filemode.Submodule {
			h = f.blob(e.content)
		}
		entries = append(entries, object.TreeEntry{Name: name, Mode: e.mode, Hash: h})
	}
	for name, sub := range nested {
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: f.tree(sub)})
	}
	sortKey := func(e object.TreeEntry) string {
		if e.Mode == filemode.Dir {
			return e.Name + "/"
		}
		return e.Name
	}
	sort.Slice(entries, func(i, j int) bool { return sortKey(entries[i]) < sortKey(entries[j]) })
	return f.store(&object.Tree{Entries: entries})
}

func (f *fixture) commit(files map[string]entry, msg string, parents ...plumbing.Hash) plumbing.Hash {
	f.clock = f.clock.Add(time.Minute)
	return f.commitAt(f.clock, files, msg, parents...)
}

func (f *fixture) commitAt(when time.Time, files map[string]entry, msg string, parents ...plumbing.Hash) plumbing.Hash {
	sig := object.Signature{Name: "Ada", Email: "ada@example.com", When: when}
	return f.store(&object.Commit{
		Author:       object.Signature{Name: "Ada Author", Email: "ada@example.com", When: when.Add(-time.Hour)},
		Committer:    sig,
		Message:      msg,
		TreeHash:     f.tree(files),
		ParentHashes: parents,
	})
}

func (f *fixture) ref(name string, h plumbing.Hash) {
	require.NoError(f.t, f.repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(name), h)))
}

func (f *fixture) branch(name string, h plumbing.Hash) {
	f.ref("refs/heads/"+name, h)
}

func (f *fixture) head(branch string) {
	require.NoError(f.t, f.repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.ReferenceName("refs/heads/"+branch))))
}

func (f *fixture) annotated(name, msg string, target plumbing.Hash, kind plumbing.ObjectType) plumbing.Hash {
	h := f.store(&object.Tag{
		Name:       name,
		Tagger:     object.Signature{Name: "Ada", Email: "ada@example.com", When: f.clock},
		Message:    msg,
		TargetType: kind,
		Target:     target,
	})
	f.ref("refs/tags/"+name, h)
	return h
}

func (f *fixture) origin(h plumbing.Hash) string {
	return Origin(DefaultSourceKey, h)
}

func hashes(commits []*PlannedCommit) []plumbing.Hash {
	out := make([]plumbing.Hash, 0, len(commits))
	for _, c := range commits {
		out = append(out, c.Hash)
	}
	return out
}

func warningsOf(ws []Warning, kind WarningKind) []Warning {
	var out []Warning
	for _, w := range ws {
		if w.Kind == kind {
			out = append(out, w)
		}
	}
	return out
}
