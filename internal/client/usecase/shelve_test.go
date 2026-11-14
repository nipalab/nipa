package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
	"github.com/nipalab/nipa/internal/snow"
)

type shelfFixture struct {
	root string
	lr   *localrepo.LocalRepo
	wc   *WorkingCopy
	sh   *Shelve
}

func newShelfFixture(t *testing.T, files map[string]string) *shelfFixture {
	t.Helper()
	fx := &shelfFixture{root: t.TempDir(), lr: localrepo.NewLocalRepo()}
	t.Cleanup(func() { _ = fx.lr.Close() })
	require.NoError(t, fx.lr.Init(fx.root))
	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	fx.sync(t, "c1", files)
	wc, err := NewWorkingCopy(fx.lr, fx.root)
	require.NoError(t, err)
	fx.wc = wc
	fx.sh = NewShelve(fx.lr, wc, fx.root)
	return fx
}

// sync makes files the synced snapshot and working copy, like an update.
func (fx *shelfFixture) sync(t *testing.T, commit string, files map[string]string) {
	t.Helper()
	tracked := map[string]merge.File{}
	for p, content := range files {
		f, batch, err := encodeMergedFile(p, []byte(content), 2, false)
		require.NoError(t, err)
		require.NoError(t, fx.lr.StoreChunks(batch))
		tracked[p] = f
	}
	if old, err := fx.lr.Snapshot(); err == nil {
		for _, f := range old.Files {
			if _, ok := files[f.Path]; !ok {
				_ = os.Remove(filepath.Join(fx.root, filepath.FromSlash(f.Path)))
			}
		}
	}
	require.NoError(t, fx.lr.SaveTree(treeFromFiles(tracked)))
	require.NoError(t, fx.lr.SaveCommit(commit, ""))
	for p, content := range files {
		writeRepoFile(t, fx.root, p, content)
	}
}

func (fx *shelfFixture) read(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.root, filepath.FromSlash(p)))
	require.NoError(t, err)
	return string(data)
}

func (fx *shelfFixture) status(t *testing.T) *clientDomain.Status {
	t.Helper()
	st, err := fx.wc.Status(context.Background())
	require.NoError(t, err)
	return st
}

func (fx *shelfFixture) shelves(t *testing.T) []clientDomain.Shelf {
	t.Helper()
	list, err := fx.sh.List()
	require.NoError(t, err)
	return list
}

func entryPaths(s *clientDomain.Shelf) []string {
	var out []string
	for _, e := range s.Entries {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}

func TestShelve_CreateAndApplyRoundTrip(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "art/hero.png": "\x00png-v1"})
	writeRepoFile(t, fx.root, "a.txt", "a edited\n")
	require.NoError(t, os.Remove(filepath.Join(fx.root, "b.txt")))
	writeRepoFile(t, fx.root, "art/hero.png", "\x00png-v2")
	writeRepoFile(t, fx.root, "new/c.txt", "c\n")
	require.NoError(t, fx.wc.Add(ctx, []string{"new/c.txt", "art/hero.png"}))
	writeRepoFile(t, fx.root, "d.txt", "untracked\n")

	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt", "art/hero.png", "b.txt", "new/c.txt"}, entryPaths(shelf))
	require.Equal(t, "4 files on main", shelf.Name)
	require.Equal(t, "c1", shelf.BaseCommitID)
	hero, _ := shelf.Entry("art/hero.png")
	require.True(t, hero.Staged)
	require.True(t, hero.File.IsBinary)
	require.False(t, hero.BaseStored)
	a, _ := shelf.Entry("a.txt")
	require.True(t, a.BaseStored)
	require.Equal(t, clientDomain.ShelfDeleted, mustEntry(t, shelf, "b.txt").Status)
	require.Equal(t, clientDomain.ShelfAdded, mustEntry(t, shelf, "new/c.txt").Status)

	require.Equal(t, "a\n", fx.read(t, "a.txt"))
	require.Equal(t, "b\n", fx.read(t, "b.txt"))
	require.Equal(t, "\x00png-v1", fx.read(t, "art/hero.png"))
	require.NoDirExists(t, filepath.Join(fx.root, "new"))
	st := fx.status(t)
	require.Empty(t, st.Staged)
	require.Empty(t, st.Modified)
	require.Empty(t, st.Missing)
	require.Equal(t, []string{"d.txt"}, st.Untracked)

	res, err := fx.sh.Apply(ctx, shelf.ID, false)
	require.NoError(t, err)
	require.Empty(t, res.Conflicts)
	require.True(t, res.Dropped)
	require.Empty(t, fx.shelves(t))
	require.Equal(t, "a edited\n", fx.read(t, "a.txt"))
	require.NoFileExists(t, filepath.Join(fx.root, "b.txt"))
	require.Equal(t, "\x00png-v2", fx.read(t, "art/hero.png"))
	require.Equal(t, "c\n", fx.read(t, "new/c.txt"))
	st = fx.status(t)
	require.ElementsMatch(t, []string{"art/hero.png", "new/c.txt"}, st.Staged)
	require.Equal(t, []string{"a.txt"}, st.Modified)
	require.Equal(t, []string{"b.txt"}, st.Missing)
}

func mustEntry(t *testing.T, s *clientDomain.Shelf, p string) clientDomain.ShelfEntry {
	t.Helper()
	e, ok := s.Entry(p)
	require.True(t, ok, p)
	return e
}

func TestShelve_SelectionAndUntracked(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "a\n", "docs/b.txt": "b\n"})
	writeRepoFile(t, fx.root, "a.txt", "a2\n")
	writeRepoFile(t, fx.root, "docs/b.txt", "b2\n")
	writeRepoFile(t, fx.root, "docs/new.txt", "n\n")
	writeRepoFile(t, fx.root, "loose.txt", "l\n")

	_, err := fx.sh.Create(ctx, ShelveOptions{Paths: []string{"missing.txt"}})
	require.ErrorContains(t, err, "missing.txt has no changes to shelve")
	_, err = fx.sh.Create(ctx, ShelveOptions{Paths: []string{".nipa/x"}})
	require.Error(t, err)

	shelf, err := fx.sh.Create(ctx, ShelveOptions{Name: " docs ", Paths: []string{"docs"}})
	require.NoError(t, err)
	require.Equal(t, "docs", shelf.Name)
	require.Equal(t, []string{"docs/b.txt"}, entryPaths(shelf), "untracked files under a folder need IncludeUntracked")
	require.FileExists(t, filepath.Join(fx.root, "docs/new.txt"))

	shelf, err = fx.sh.Create(ctx, ShelveOptions{Paths: []string{"loose.txt"}})
	require.NoError(t, err)
	require.Equal(t, []string{"loose.txt"}, entryPaths(shelf), "an exact untracked path is taken")
	require.True(t, mustEntry(t, shelf, "loose.txt").Untracked)
	require.NoFileExists(t, filepath.Join(fx.root, "loose.txt"))

	shelf, err = fx.sh.Create(ctx, ShelveOptions{IncludeUntracked: true})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt", "docs/new.txt"}, entryPaths(shelf))
	require.Equal(t, "2 files on main", shelf.Name)

	_, err = fx.sh.Create(ctx, ShelveOptions{})
	require.ErrorContains(t, err, "no changes to shelve")
	require.Len(t, fx.shelves(t), 3)

	res, err := fx.sh.Apply(ctx, shelf.ID, true)
	require.NoError(t, err)
	require.False(t, res.Dropped)
	require.Len(t, fx.shelves(t), 3)
	st := fx.status(t)
	require.Equal(t, []string{"docs/new.txt"}, st.Untracked, "an untracked file comes back untracked")
	require.Empty(t, st.Staged)
}

func TestShelve_ApplyOnMovedBaseMergesText(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "1\n2\n3\n4\n5\n"})
	writeRepoFile(t, fx.root, "a.txt", "one\n2\n3\n4\n5\n")
	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)

	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "feature"}))
	fx.sync(t, "c2", map[string]string{"a.txt": "1\n2\n3\n4\nfive\n", "b.txt": "b\n"})

	res, err := fx.sh.Apply(ctx, shelf.ID, false)
	require.NoError(t, err)
	require.Empty(t, res.Conflicts)
	require.Equal(t, "one\n2\n3\n4\nfive\n", fx.read(t, "a.txt"))
	st := fx.status(t)
	require.Equal(t, []string{"a.txt"}, st.Modified)
	require.Empty(t, st.Staged)
}

func TestShelve_TextConflictResolveFinishes(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "base\n"})
	writeRepoFile(t, fx.root, "a.txt", "mine\n")
	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)
	fx.sync(t, "c2", map[string]string{"a.txt": "upstream\n"})

	res, err := fx.sh.Apply(ctx, shelf.ID, false)
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, res.Conflicts)
	require.False(t, res.Dropped)
	require.Contains(t, fx.read(t, "a.txt"), "<<<<<<< ours")

	st := fx.status(t)
	require.Equal(t, "shelf", st.Pending)
	require.Equal(t, []string{"a.txt"}, st.Conflicts)
	require.Empty(t, st.Staged, "conflicted files stay unstaged")

	c := NewConflicts(fx.lr, fx.wc, fx.root)
	set, err := c.List()
	require.NoError(t, err)
	require.Equal(t, "shelf", set.Op)
	require.Equal(t, shelf.ID, set.Theirs.ID)
	require.Equal(t, "c2", set.Ours.ID)
	require.Equal(t, "c1", set.Base.ID)
	base, ours, theirs := ConflictSides(set, "a.txt")
	require.Equal(t, BlobShelfBase, base.Source)
	require.Equal(t, BlobSnapshot, ours.Source)
	require.Equal(t, BlobShelf, theirs.Source)

	_, err = fx.sh.Create(ctx, ShelveOptions{})
	require.ErrorContains(t, err, "stopped on conflicts")
	_, err = fx.sh.Apply(ctx, shelf.ID, false)
	require.ErrorContains(t, err, "stopped on conflicts")
	require.ErrorContains(t, fx.sh.Drop(shelf.ID), "being applied")
	res2, err := PruneCache(fx.lr)
	require.NoError(t, err)
	require.True(t, res2.Skipped)

	require.NoError(t, c.Resolve(ctx, "a.txt", Resolution{Content: bytes.NewReader([]byte("both\n"))}))
	state, err := fx.lr.LoadShelfState()
	require.NoError(t, err)
	require.Nil(t, state, "the last resolution finishes the apply")
	require.Empty(t, fx.shelves(t), "and drops the shelf")
	require.Equal(t, "both\n", fx.read(t, "a.txt"))
}

func TestShelve_ConflictKeepAndAbort(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "base\n", "hero.png": "\x00v1", "keep.txt": "k\n"})
	writeRepoFile(t, fx.root, "a.txt", "mine\n")
	writeRepoFile(t, fx.root, "hero.png", "\x00mine")
	writeRepoFile(t, fx.root, "keep.txt", "k2\n")
	writeRepoFile(t, fx.root, "added.txt", "x\n")
	require.NoError(t, fx.wc.Add(ctx, []string{"added.txt"}))
	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)
	fx.sync(t, "c2", map[string]string{"a.txt": "upstream\n", "hero.png": "\x00theirs", "keep.txt": "k\n"})

	res, err := fx.sh.Apply(ctx, shelf.ID, true)
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt", "hero.png"}, res.Conflicts)
	require.Equal(t, "\x00theirs", fx.read(t, "hero.png"), "a binary conflict keeps ours on disk")
	require.Equal(t, "k2\n", fx.read(t, "keep.txt"))
	set, err := NewConflicts(fx.lr, fx.wc, fx.root).List()
	require.NoError(t, err)
	require.Equal(t, clientDomain.ConflictBinary, set.Files[1].Kind)

	d := NewDiff(nil, nil, fx.lr)
	_, _, theirs := ConflictSides(set, "hero.png")
	require.Equal(t, "\x00mine", readBlobString(t, d, fx.root, *theirs))
	base, _, _ := ConflictSides(set, "a.txt")
	require.Equal(t, "base\n", readBlobString(t, d, fx.root, *base))

	require.NoError(t, fx.sh.Abort(ctx))
	require.Equal(t, "upstream\n", fx.read(t, "a.txt"))
	require.Equal(t, "k\n", fx.read(t, "keep.txt"))
	require.NoFileExists(t, filepath.Join(fx.root, "added.txt"))
	st := fx.status(t)
	require.Empty(t, st.Pending)
	require.Empty(t, st.Staged)
	require.Empty(t, st.Modified)
	require.Len(t, fx.shelves(t), 1, "abort keeps the shelf")
	require.ErrorContains(t, fx.sh.Abort(ctx), "no shelf apply is waiting")
}

func TestShelve_ResolveKeepsShelfWhenApplyKept(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "base\n"})
	writeRepoFile(t, fx.root, "a.txt", "mine\n")
	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)
	fx.sync(t, "c2", map[string]string{"a.txt": "upstream\n"})
	_, err = fx.sh.Apply(ctx, shelf.ID, true)
	require.NoError(t, err)
	require.NoError(t, NewConflicts(fx.lr, fx.wc, fx.root).Resolve(ctx, "a.txt", Resolution{Content: bytes.NewReader([]byte("x\n"))}))
	require.Len(t, fx.shelves(t), 1)
}

func TestShelve_ApplyRefusesLocalChanges(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	writeRepoFile(t, fx.root, "a.txt", "shelved\n")
	writeRepoFile(t, fx.root, "c.txt", "new\n")
	shelf, err := fx.sh.Create(ctx, ShelveOptions{IncludeUntracked: true})
	require.NoError(t, err)

	writeRepoFile(t, fx.root, "a.txt", "again\n")
	writeRepoFile(t, fx.root, "c.txt", "other\n")
	_, err = fx.sh.Apply(ctx, shelf.ID, false)
	require.ErrorContains(t, err, "local changes to a.txt, c.txt would be overwritten")
	require.Equal(t, "again\n", fx.read(t, "a.txt"))

	_, err = fx.sh.Apply(ctx, "nope", false)
	require.ErrorContains(t, err, "not found")
}

func TestShelve_ApplyRefusesOutsideSparse(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"art/a.txt": "a\n"})
	writeRepoFile(t, fx.root, "art/a.txt", "b\n")
	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)
	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "main", Sparse: []string{"code"}}))
	_, err = fx.sh.Apply(ctx, shelf.ID, false)
	require.ErrorContains(t, err, "outside this clone's checkout")
}

func TestShelve_RefusesPendingMergeAndRevert(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "a\n"})
	writeRepoFile(t, fx.root, "a.txt", "b\n")
	require.NoError(t, fx.lr.SaveMergeState(&clientDomain.MergeState{SourceBranch: "x"}))
	_, err := fx.sh.Create(ctx, ShelveOptions{})
	require.ErrorContains(t, err, "a merge is in progress")
	require.NoError(t, fx.lr.ClearMergeState())
	require.NoError(t, fx.lr.SaveRevertState(&clientDomain.RevertState{}))
	_, err = fx.sh.Apply(ctx, "x", false)
	require.ErrorContains(t, err, "a revert is in progress")
}

func TestShelve_DropAndShow(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "1\n2\n", "gone.txt": "g\n", "hero.png": "\x00v1"})
	writeRepoFile(t, fx.root, "a.txt", "1\nTWO\n")
	writeRepoFile(t, fx.root, "new.txt", "n\n")
	writeRepoFile(t, fx.root, "hero.png", "\x00v2")
	require.NoError(t, os.Remove(filepath.Join(fx.root, "gone.txt")))
	shelf, err := fx.sh.Create(ctx, ShelveOptions{IncludeUntracked: true})
	require.NoError(t, err)

	got, files, err := fx.sh.Show(shelf.ID)
	require.NoError(t, err)
	require.Equal(t, shelf.ID, got.ID)
	byPath := map[string]diff.FileDiff{}
	for _, f := range files {
		byPath[f.Change.Path] = f
	}
	require.Equal(t, diff.Modified, byPath["a.txt"].Change.Status)
	require.Equal(t, "1\n2\n", string(byPath["a.txt"].Old))
	require.Equal(t, "1\nTWO\n", string(byPath["a.txt"].New))
	require.NotEmpty(t, diff.FileHunks(byPath["a.txt"], diff.Options{}))
	require.Equal(t, diff.Added, byPath["new.txt"].Change.Status)
	require.Equal(t, diff.Deleted, byPath["gone.txt"].Change.Status)
	require.Equal(t, "g\n", string(byPath["gone.txt"].Old))
	require.True(t, byPath["hero.png"].Change.New.IsBinary)
	require.Nil(t, byPath["hero.png"].New)

	d := NewDiff(nil, nil, fx.lr)
	require.Equal(t, "n\n", readBlobString(t, d, fx.root, BlobRequest{Source: BlobShelf, Revision: shelf.ID, Path: "new.txt"}))
	err = d.ReadBlob(ctx, fx.root, BlobRequest{Source: BlobShelfBase, Revision: shelf.ID, Path: "new.txt"}, func(diff.Entry, io.Reader) error { return nil })
	require.ErrorIs(t, err, ErrFileMissing)

	_, _, err = fx.sh.Show("nope")
	require.Error(t, err)
	require.NoError(t, fx.sh.Drop(shelf.ID))
	require.Empty(t, fx.shelves(t))
	require.Error(t, fx.sh.Drop(shelf.ID))
}

func TestShelve_ShelfBaseFallsBackToBaseCommit(t *testing.T) {
	fx := newShelfFixture(t, map[string]string{"hero.png": "\x00v1"})
	writeRepoFile(t, fx.root, "hero.png", "\x00v2")
	shelf, err := fx.sh.Create(context.Background(), ShelveOptions{})
	require.NoError(t, err)
	repo := fx.lr
	entry, load, stored, err := ShelfEntryBlob(repo, shelf.ID, "hero.png", true)
	require.NoError(t, err)
	require.False(t, stored)
	require.Nil(t, load)
	require.Equal(t, int64(3), entry.SizeBytes)
	_, _, _, err = ShelfEntryBlob(repo, "nope", "hero.png", false)
	require.Error(t, err)
}

func TestShelve_RecordsLockedPaths(t *testing.T) {
	fx := newShelfFixture(t, map[string]string{"art/a.png": "\x00a", "b.txt": "b\n"})
	require.NoError(t, fx.lr.PinPaths([]string{"art"}))
	writeRepoFile(t, fx.root, "art/a.png", "\x00b")
	writeRepoFile(t, fx.root, "b.txt", "c\n")
	shelf, err := fx.sh.Create(context.Background(), ShelveOptions{})
	require.NoError(t, err)
	require.True(t, mustEntry(t, shelf, "art/a.png").Locked)
	require.False(t, mustEntry(t, shelf, "b.txt").Locked)
}

type failingShelfRepo struct {
	*localrepo.LocalRepo
}

func (failingShelfRepo) CreateShelf(func(func(serverDomain.Hash, []byte) error) (*clientDomain.Shelf, error)) (*clientDomain.Shelf, error) {
	return nil, errors.New("disk full")
}

func TestShelve_FailedWriteLeavesWorkIntact(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "a\n"})
	writeRepoFile(t, fx.root, "a.txt", "precious\n")
	writeRepoFile(t, fx.root, "new.txt", "new\n")
	require.NoError(t, fx.wc.Add(ctx, []string{"new.txt"}))

	sh := NewShelve(failingShelfRepo{fx.lr}, fx.wc, fx.root)
	_, err := sh.Create(ctx, ShelveOptions{})
	require.ErrorContains(t, err, "disk full")
	require.Equal(t, "precious\n", fx.read(t, "a.txt"))
	require.Equal(t, "new\n", fx.read(t, "new.txt"))
	require.Equal(t, []string{"new.txt"}, fx.status(t).Staged)

	unreadable := filepath.Join(fx.root, "a.txt")
	require.NoError(t, os.Remove(unreadable))
	require.NoError(t, os.Mkdir(unreadable, 0o755))
	_, err = fx.sh.Create(ctx, ShelveOptions{Paths: []string{"new.txt"}})
	require.NoError(t, err, "only selected files are read")
}

func TestShelve_PushMergeRevertRefuseWhileApplyWaits(t *testing.T) {
	fx := newShelfFixture(t, map[string]string{"a.txt": "a\n"})
	require.NoError(t, fx.lr.SaveShelfState(&clientDomain.ShelfState{ShelfID: "x", ShelfName: "wip"}))
	require.ErrorContains(t, refuseShelfPending(fx.lr), `applying shelf "wip" stopped on conflicts`)
	require.NoError(t, refuseShelfPending(struct{}{}))
}

func TestOverwrittenPaths(t *testing.T) {
	files := map[string]merge.File{}
	add := func(p, content string) {
		f, _, err := encodeMergedFile(p, []byte(content), 2, false)
		require.NoError(t, err)
		files[p] = f
	}
	add("same.txt", "s")
	add("edited.txt", "e1")
	add("upstream.txt", "u1")
	snapshot := &clientDomain.Snapshot{}
	for _, f := range files {
		snapshot.Files = append(snapshot.Files, clientDomain.SnapshotFile{Path: f.Path, Hash: f.Hash, Mode: f.Mode})
	}
	add("edited.txt", "e2")
	add("upstream.txt", "u2")
	add("new.txt", "n")
	tree := treeFromFiles(files)
	st := &clientDomain.Status{
		Modified:  []string{"same.txt", "edited.txt"},
		Untracked: []string{"new.txt", "other.txt"},
		Staged:    []string{"staged.txt"},
		Deleted:   []string{"gone.txt"},
	}
	require.Equal(t, []string{"edited.txt", "new.txt"}, OverwrittenPaths(snapshot, tree, st, false))
	require.Equal(t, []string{"edited.txt", "gone.txt", "new.txt", "staged.txt"}, OverwrittenPaths(snapshot, tree, st, true))
}

func readBlobString(t *testing.T, d *Diff, root string, req BlobRequest) string {
	t.Helper()
	var out []byte
	require.NoError(t, d.ReadBlob(context.Background(), root, req, func(_ diff.Entry, r io.Reader) error {
		var err error
		out, err = io.ReadAll(r)
		return err
	}))
	return string(out)
}

func TestUpdate_TargetTree(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "a\n"})
	branchTree := &serverDomain.TreeNode{Name: "branch"}
	commitTree := &serverDomain.TreeNode{Name: "commit"}
	client := &stubUpdateClient{manifest: branchTree, commitManifest: commitTree, tagInfo: &clientDomain.Tag{Name: "v1", CommitID: "t1"}}
	u := newTestUpdate(t, fx.lr, client)

	tree, err := u.TargetTree(ctx, fx.root, SyncTarget{})
	require.NoError(t, err)
	require.Same(t, branchTree, tree)
	require.Equal(t, "main", client.branch)

	tree, err = u.TargetTree(ctx, fx.root, SyncTarget{Branch: "feature"})
	require.NoError(t, err)
	require.Same(t, branchTree, tree)
	require.Equal(t, "feature", client.branch)

	tree, err = u.TargetTree(ctx, fx.root, SyncTarget{Tag: "v1"})
	require.NoError(t, err)
	require.Same(t, commitTree, tree)
	require.Equal(t, "t1", client.commitTreeLookup)

	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "main", Head: &clientDomain.HeadRef{Kind: clientDomain.HeadKindTag, Name: "v1"}}))
	_, err = u.TargetTree(ctx, fx.root, SyncTarget{})
	require.NoError(t, err)
	require.Equal(t, "c1", client.commitTreeLookup)
	require.NoError(t, fx.lr.SaveCommit("", ""))
	_, err = u.TargetTree(ctx, fx.root, SyncTarget{})
	require.ErrorContains(t, err, "no commit checked out")

	client.tagErr = errors.New("no tag")
	_, err = u.TargetTree(ctx, fx.root, SyncTarget{Tag: "v2"})
	require.ErrorContains(t, err, "no tag")
	client.connectErr = errors.New("offline")
	_, err = u.TargetTree(ctx, fx.root, SyncTarget{})
	require.ErrorContains(t, err, "offline")
}

func TestShelve_ShelfBaseReadsBaseCommit(t *testing.T) {
	ctx := context.Background()
	commit := snow.ID(7).Base36()
	fx := newShelfFixture(t, map[string]string{"hero.png": "\x00v1"})
	require.NoError(t, fx.lr.SaveCommit(commit, ""))
	writeRepoFile(t, fx.root, "hero.png", "\x00v2")
	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)

	revisionChunks := map[serverDomain.Hash][]byte{}
	client := &stubDiffClient{
		commits:  map[string]*clientDomain.CommitDetail{commit: {ID: commit, Tree: encodedTree(t, map[string]string{"hero.png": "\x00v1"}, revisionChunks)}},
		download: revisionChunks,
	}
	d := NewDiff(diffAuth(t), client, fx.lr)
	require.Equal(t, "\x00v1", readBlobString(t, d, fx.root, BlobRequest{Source: BlobShelfBase, Revision: shelf.ID, Path: "hero.png"}))

	require.NoError(t, fx.sh.Drop(shelf.ID))
	err = d.ReadBlob(ctx, fx.root, BlobRequest{Source: BlobShelfBase, Revision: shelf.ID, Path: "hero.png"}, func(diff.Entry, io.Reader) error { return nil })
	require.ErrorContains(t, err, "not found")
}

func TestConflictSides_Commits(t *testing.T) {
	set := &clientDomain.ConflictSet{Op: "merge", Base: clientDomain.ConflictRevision{ID: "b"}, Ours: clientDomain.ConflictRevision{ID: "o"}}
	base, ours, theirs := ConflictSides(set, "a.txt")
	require.Equal(t, &BlobRequest{Source: BlobRevision, Revision: "b", Path: "a.txt"}, base)
	require.Equal(t, "o", ours.Revision)
	require.Nil(t, theirs)
}

func TestNoDownload(t *testing.T) {
	err := noDownload{}.DownloadChunks(context.Background(), clientDomain.ChunkScope{}, nil, nil)
	require.ErrorContains(t, err, "not cached locally")
}

func TestShelve_ReadShelfBlobNeedsShelfRepo(t *testing.T) {
	root := t.TempDir()
	d := NewDiff(nil, nil, &stubLocalRepo{})
	err := d.ReadBlob(context.Background(), root, BlobRequest{Source: BlobShelf, Revision: "x", Path: "a"}, func(diff.Entry, io.Reader) error { return nil })
	require.ErrorContains(t, err, "not supported")
}

func TestShelve_Continue(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "base\n", "hero.png": "\x00v1"})
	writeRepoFile(t, fx.root, "a.txt", "mine\n")
	writeRepoFile(t, fx.root, "hero.png", "\x00mine")
	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)
	_, err = fx.sh.Continue()
	require.ErrorContains(t, err, "no shelf apply is waiting")
	fx.sync(t, "c2", map[string]string{"a.txt": "upstream\n", "hero.png": "\x00theirs"})
	_, err = fx.sh.Apply(ctx, shelf.ID, false)
	require.NoError(t, err)

	_, err = fx.sh.Continue()
	require.ErrorContains(t, err, "still has conflict markers")
	writeRepoFile(t, fx.root, "a.txt", "fixed\n")
	_, err = fx.sh.Continue()
	require.ErrorContains(t, err, "hero.png is still unresolved", "a binary conflict needs an explicit side")
	require.Len(t, fx.shelves(t), 1)
	require.NoError(t, fx.sh.Resolve(ctx, "hero.png", ShelfTakeTheirs))
	require.Equal(t, "\x00mine", fx.read(t, "hero.png"))
	state, err := fx.sh.Continue()
	require.NoError(t, err)
	require.Equal(t, shelf.ID, state.ShelfID)
	require.Empty(t, fx.shelves(t))
	st := fx.status(t)
	require.Empty(t, st.Pending)
	require.Empty(t, st.Staged)
	require.ElementsMatch(t, []string{"a.txt", "hero.png"}, st.Modified)
}

func TestShelve_ResolveSides(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, map[string]string{"a.txt": "base\n", "hero.png": "\x00v1", "gone.txt": "g\n", "kept.txt": "k\n"})
	require.ErrorContains(t, fx.sh.Resolve(ctx, "a.txt", ShelfTakeOurs), "no shelf apply is waiting")
	writeRepoFile(t, fx.root, "a.txt", "mine\n")
	writeRepoFile(t, fx.root, "hero.png", "\x00mine")
	writeRepoFile(t, fx.root, "kept.txt", "shelved k\n")
	require.NoError(t, os.Remove(filepath.Join(fx.root, "gone.txt")))
	shelf, err := fx.sh.Create(ctx, ShelveOptions{})
	require.NoError(t, err)
	fx.sync(t, "c2", map[string]string{"a.txt": "upstream\n", "hero.png": "\x00theirs", "gone.txt": "upstream g\n"})

	res, err := fx.sh.Apply(ctx, shelf.ID, true)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a.txt", "gone.txt", "hero.png", "kept.txt"}, res.Conflicts)

	require.ErrorContains(t, fx.sh.Resolve(ctx, "a.txt", "sideways"), "unknown side")
	require.Error(t, fx.sh.Resolve(ctx, "../x", ShelfTakeOurs))
	require.ErrorContains(t, fx.sh.Resolve(ctx, "a.txt", ShelfKeepWorking), "conflict markers")
	_, err = fx.sh.Continue()
	require.ErrorContains(t, err, "a.txt still has conflict markers")
	writeRepoFile(t, fx.root, "a.txt", "edited by hand\n")
	_, err = fx.sh.Continue()
	require.ErrorContains(t, err, "gone.txt is still unresolved", "a modify/delete conflict needs an explicit side")

	require.NoError(t, fx.sh.Resolve(ctx, "hero.png", ShelfTakeOurs))
	require.Equal(t, "\x00theirs", fx.read(t, "hero.png"))
	require.NoError(t, fx.sh.Resolve(ctx, "gone.txt", ShelfTakeTheirs))
	require.NoFileExists(t, filepath.Join(fx.root, "gone.txt"), "the shelf deleted it")
	require.NoError(t, fx.sh.Resolve(ctx, "kept.txt", ShelfTakeOurs))
	require.NoFileExists(t, filepath.Join(fx.root, "kept.txt"), "the branch no longer has it")
	require.NoError(t, fx.sh.Resolve(ctx, "a.txt", ShelfTakeTheirs))
	require.Equal(t, "mine\n", fx.read(t, "a.txt"))

	require.Empty(t, fx.status(t).Pending, "the last resolution finishes the apply")
	require.Len(t, fx.shelves(t), 1, "kept by the apply")
}
