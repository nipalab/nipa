package localrepo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
	"github.com/nipalab/nipa/internal/treedelta"
)

// diffManifests is the delta the server sends for base -> target in an
// unfiltered view: a directory with the same hash is unchanged, a changed one
// lists every entry, files in full when they differ.
func diffManifests(base, target *domain.TreeNode) *treedelta.Dir {
	out := &treedelta.Dir{Name: target.Name}
	if base != nil && base.Hash == target.Hash {
		out.Unchanged = true
		return out
	}
	heldFiles := map[string]*domain.File{}
	heldDirs := map[string]*domain.TreeNode{}
	if base != nil {
		for _, f := range base.FileChildren {
			heldFiles[f.Name] = f
		}
		for _, d := range base.TreeChildren {
			heldDirs[d.Name] = d
		}
	}
	for _, f := range target.FileChildren {
		entry := treedelta.File{Name: f.Name}
		if old := heldFiles[f.Name]; old == nil || old.Hash != f.Hash || old.Mode != f.Mode ||
			old.IsBinary != f.IsBinary || old.Encoding != f.Encoding || old.SizeBytes != f.SizeBytes {
			entry.Changed = f
		}
		out.Files = append(out.Files, entry)
	}
	for _, d := range target.TreeChildren {
		out.Dirs = append(out.Dirs, diffManifests(heldDirs[d.Name], d))
	}
	return out
}

const (
	deltaBaseCommit = "c1"
	deltaCommit     = "c2"
)

var (
	deltaBaseView = []byte("view-of-the-base")
	deltaView     = []byte("view-of-the-target")
)

// deltaRepo holds base as the synced manifest of deltaBaseCommit, with a
// fingerprint for every file.
func deltaRepo(t *testing.T, base map[string]pushSpec, sparse ...string) *LocalRepo {
	t.Helper()
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTreeView(manifestTree(base), deltaBaseCommit, sparse, deltaBaseView))
	stats := map[string]clientDomain.StatEntry{}
	for path := range base {
		stats[path] = clientDomain.StatEntry{SizeBytes: 1}
	}
	require.NoError(t, lr.SaveStatEntries(stats))
	return lr
}

func deltaInput(base, target map[string]pushSpec, sparse ...string) clientDomain.TreeDeltaApply {
	want := manifestTree(target)
	hash := want.Hash
	return clientDomain.TreeDeltaApply{
		Delta:        &treedelta.Delta{Root: diffManifests(manifestTree(base), want), RootHash: &hash},
		BaseCommitID: deltaBaseCommit,
		CommitID:     deltaCommit,
		Sparse:       sparse,
		View:         deltaView,
	}
}

// expectChanges lists what moving from base to target does to the files.
func expectChanges(base, target map[string]pushSpec) (written, added, removed []string) {
	for path, s := range target {
		old, tracked := base[path]
		switch {
		case !tracked:
			written = append(written, path)
			added = append(added, path)
		case old != s:
			written = append(written, path)
		}
	}
	for path := range base {
		if _, kept := target[path]; !kept {
			removed = append(removed, path)
		}
	}
	sort.Strings(written)
	sort.Strings(added)
	sort.Strings(removed)
	return written, added, removed
}

func requireChanges(t *testing.T, base, target map[string]pushSpec, got *clientDomain.TreeDeltaChanges) {
	t.Helper()
	written, added, removed := expectChanges(base, target)
	var gotWritten, gotAdded, gotRemoved []string
	for _, w := range got.Written {
		gotWritten = append(gotWritten, w.Path)
		require.Equal(t, target[w.Path].manifestFile(w.Path), w.File, w.Path)
		if w.Base == nil {
			gotAdded = append(gotAdded, w.Path)
			continue
		}
		require.Equal(t, w.Path, w.Base.Path)
		require.Equal(t, base[w.Path].manifestFile(w.Path).Hash, w.Base.Hash, w.Path)
		require.Equal(t, base[w.Path].chunkHashes(), w.Base.Chunks, w.Path)
	}
	for _, f := range got.Removed {
		gotRemoved = append(gotRemoved, f.Path)
		require.Equal(t, base[f.Path].manifestFile(f.Path).Hash, f.Hash, f.Path)
		require.Equal(t, base[f.Path].chunkHashes(), f.Chunks, f.Path)
	}
	require.Equal(t, written, gotWritten, "written")
	require.Equal(t, added, gotAdded, "added")
	require.Equal(t, removed, gotRemoved, "removed")
	require.Equal(t, manifestTree(target).Hash, got.TreeHash)
	require.Equal(t, manifestTree(base).Hash.String(), got.BaseTreeHash)
}

func without(base map[string]pushSpec, paths ...string) map[string]pushSpec {
	out := copySpecs(base)
	for _, path := range paths {
		delete(out, path)
	}
	return out
}

func with(base map[string]pushSpec, more map[string]pushSpec) map[string]pushSpec {
	out := copySpecs(base)
	for path, s := range more {
		out[path] = s
	}
	return out
}

func deltaCases() map[string]map[string]pushSpec {
	return map[string]map[string]pushSpec{
		"nothing changed":      copySpecs(applyBase),
		"modify a file":        with(applyBase, map[string]pushSpec{"src/a.go": spec(40)}),
		"modify a root file":   with(applyBase, map[string]pushSpec{"root.txt": spec(41)}),
		"add a file":           with(applyBase, map[string]pushSpec{"src/c.go": spec(42)}),
		"add nested dirs":      with(applyBase, map[string]pushSpec{"new/deep/er/f.txt": spec(43), "top.txt": spec(44)}),
		"remove a file":        without(applyBase, "src/b.go"),
		"remove a directory":   without(applyBase, "src/pkg/deep/x.go", "src/pkg/deep/y.go"),
		"remove a whole chain": without(applyBase, "solo/chain/only.txt"),
		"remove a top subtree": without(applyBase, "src/a.go", "src/b.go", "src/pkg/deep/x.go", "src/pkg/deep/y.go"),
		"remove everything":    {},
		"replace everything":   {"other/place.txt": spec(45)},
		"file becomes dir":     with(without(applyBase, "root.txt"), map[string]pushSpec{"root.txt/inner.txt": spec(46)}),
		"dir becomes file":     with(without(applyBase, "src/pkg/deep/x.go", "src/pkg/deep/y.go"), map[string]pushSpec{"src/pkg": spec(47)}),
		"mode change":          with(applyBase, map[string]pushSpec{"src/a.go": {seed: 2, chunks: 1, perm: 0o755, encoding: chunker.EncodingZstd}}),
		"read-only":            with(applyBase, map[string]pushSpec{"src/a.go": {seed: 2, chunks: 1, perm: 0o444, encoding: chunker.EncodingZstd}}),
		// The tree hash covers neither the binary flag nor the encoding, so
		// such a change only shows in a directory that differs otherwise.
		"binary flag change":    with(applyBase, map[string]pushSpec{"src/n.go": spec(52), "src/a.go": {seed: 2, chunks: 1, perm: 0o644, binary: true, encoding: chunker.EncodingZstd}}),
		"encoding change":       with(applyBase, map[string]pushSpec{"src/n.go": spec(53), "src/a.go": {seed: 2, chunks: 1, perm: 0o644, encoding: chunker.EncodingRaw}}),
		"chunk count change":    with(applyBase, map[string]pushSpec{"assets/tex/hero.png": {seed: 7, chunks: 5, perm: 0o644, binary: true, encoding: chunker.EncodingRaw}}),
		"many things at once":   with(without(applyBase, "docs/readme.md", "src/b.go", "solo/chain/only.txt"), map[string]pushSpec{"src/a.go": spec(48), "src/pkg/deep/z.go": spec(49), "docs/guide/intro.md": spec(50), "bin/run.sh": spec(51)}),
		"move between siblings": with(without(applyBase, "src/pkg/deep/x.go"), map[string]pushSpec{"src/pkg/other/x.go": spec(4)}),
	}
}

// The core property: the rows a delta apply leaves are the rows
// SaveSyncedTreeView writes from the target's manifest, whether the apply
// plans by itself or follows a plan made before the record was dropped.
func TestApplyTreeDelta_EqualsSavingTheManifest(t *testing.T) {
	for name, target := range deltaCases() {
		for _, mode := range []string{"direct", "planned", "planned without record"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				lr := deltaRepo(t, applyBase)
				in := deltaInput(applyBase, target)

				var planned *clientDomain.TreeDeltaChanges
				if mode != "direct" {
					before := readFullState(t, lr)
					var err error
					planned, err = lr.PlanTreeDelta(in)
					require.NoError(t, err)
					requireChanges(t, applyBase, target, planned)
					require.Equal(t, before, readFullState(t, lr), "a plan writes nothing")
				}
				if mode == "planned without record" {
					require.NoError(t, lr.ClearSyncedTree())
				}
				changes, err := lr.ApplyTreeDelta(in, planned)
				require.NoError(t, err)
				requireChanges(t, applyBase, target, changes)

				saved := deltaRepo(t, applyBase)
				require.NoError(t, saved.SaveSyncedTreeView(manifestTree(target), deltaCommit, nil, deltaView))
				require.Equal(t, readFullState(t, saved), readFullState(t, lr))

				synced, err := lr.LoadSyncedTree()
				require.NoError(t, err)
				require.Equal(t, &clientDomain.SyncedTree{CommitID: deltaCommit, Sparse: []string{}, View: deltaView}, synced)
				hash, err := lr.SnapshotTreeHash()
				require.NoError(t, err)
				require.Equal(t, manifestTree(target).Hash.String(), hash)
			})
		}
	}
}

// Deltas chain: each one is applied to what the previous one left.
func TestApplyTreeDelta_Successive(t *testing.T) {
	lr := deltaRepo(t, applyBase)
	current, commit := applyBase, deltaBaseCommit
	names := make([]string, 0)
	cases := deltaCases()
	for name := range cases {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		in := deltaInput(current, cases[name])
		in.BaseCommitID, in.CommitID = commit, fmt.Sprintf("s%d", i)
		changes, err := lr.ApplyTreeDelta(in, nil)
		require.NoError(t, err, name)
		requireChanges(t, current, cases[name], changes)
		current, commit = cases[name], in.CommitID
	}
	saved := newSyncedRepo(t)
	require.NoError(t, saved.SaveSyncedTreeView(manifestTree(current), commit, nil, deltaView))
	require.Equal(t, readState(t, saved), readState(t, lr))
}

// A base without any tree (an empty manifest saved with a record) takes a
// delta that lists everything in full.
func TestApplyTreeDelta_FromAnEmptyTree(t *testing.T) {
	for _, base := range []*domain.TreeNode{nil, manifestTree(nil)} {
		lr := newSyncedRepo(t)
		require.NoError(t, lr.SaveSyncedTreeView(base, deltaBaseCommit, nil, deltaBaseView))
		want := manifestTree(applyBase)
		hash := want.Hash
		in := clientDomain.TreeDeltaApply{
			Delta:        &treedelta.Delta{Root: diffManifests(nil, want), RootHash: &hash},
			BaseCommitID: deltaBaseCommit, CommitID: deltaCommit, View: deltaView,
		}
		changes, err := lr.ApplyTreeDelta(in, nil)
		require.NoError(t, err)
		require.Len(t, changes.Written, len(applyBase))
		require.Empty(t, changes.Removed)

		saved := newSyncedRepo(t)
		require.NoError(t, saved.SaveSyncedTreeView(want, deltaCommit, nil, deltaView))
		require.Equal(t, readFullState(t, saved), readFullState(t, lr))
	}
}

// An unchanged root moves the record to the new commit and nothing else,
// also when nothing is stored.
func TestApplyTreeDelta_UnchangedRoot(t *testing.T) {
	lr := deltaRepo(t, applyBase)
	before := readFullState(t, lr)
	hash := manifestTree(applyBase).Hash
	in := clientDomain.TreeDeltaApply{
		Delta:        &treedelta.Delta{Root: &treedelta.Dir{Name: "root", Unchanged: true}, RootHash: &hash},
		BaseCommitID: deltaBaseCommit, CommitID: deltaCommit, View: deltaView,
	}
	changes, err := lr.ApplyTreeDelta(in, nil)
	require.NoError(t, err)
	require.Empty(t, changes.Written)
	require.Empty(t, changes.Removed)
	require.Equal(t, hash, changes.TreeHash)
	after := readFullState(t, lr)
	require.Equal(t, before.tree, after.tree)
	require.Equal(t, before.stat, after.stat)
	require.Equal(t, `{"commit":"c2","sparse":[],"view":"dmlldy1vZi10aGUtdGFyZ2V0"}`, after.synced)

	// The root hash of an unchanged root is still checked.
	other := domain.Hash{9}
	in.BaseCommitID, in.CommitID, in.Delta.RootHash = deltaCommit, "c3", &other
	_, err = lr.ApplyTreeDelta(in, nil)
	var mismatch *clientDomain.TreeDeltaMismatchError
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, other, mismatch.Expected)
	require.Equal(t, hash, mismatch.Computed)
	require.Contains(t, mismatch.Error(), other.String())

	empty := newSyncedRepo(t)
	require.NoError(t, empty.SaveSyncedTreeView(nil, deltaBaseCommit, nil, deltaBaseView))
	in.BaseCommitID, in.CommitID, in.Delta.RootHash = deltaBaseCommit, deltaCommit, nil
	changes, err = empty.ApplyTreeDelta(in, nil)
	require.NoError(t, err)
	require.Equal(t, emptyTreeHash, changes.TreeHash)
	require.Empty(t, readState(t, empty).nodes)
}

// In a filtered or sparse view "unchanged" means "keep what you hold, if
// anything": a directory the tree does not hold stays absent, and a changed
// directory that ends up with nothing is not created (or is dropped).
func TestApplyTreeDelta_UnchangedDirectoriesTheTreeDoesNotHold(t *testing.T) {
	base := map[string]pushSpec{"a/one.txt": spec(1), "b/keep/two.txt": spec(2), "c/three.txt": spec(3)}
	lr := deltaRepo(t, base, "a", "b", "c")
	in := clientDomain.TreeDeltaApply{
		Delta: &treedelta.Delta{Root: &treedelta.Dir{Name: "root", Dirs: []*treedelta.Dir{
			{Name: "a", Files: []treedelta.File{{Name: "one.txt"}, {Name: "new.txt", Changed: spec(9).manifestFile("a/new.txt")}}},
			// Held, and now showing nothing of its own but an unseen child.
			{Name: "c", Dirs: []*treedelta.Dir{{Name: "unseen", Unchanged: true}}},
			{Name: "b", Unchanged: true},
			// Not held at all.
			{Name: "hidden", Unchanged: true},
			// New in the store, nothing of it visible.
			{Name: "ghost", Dirs: []*treedelta.Dir{{Name: "unseen", Unchanged: true}, {Name: "deeper", Dirs: []*treedelta.Dir{{Name: "x", Unchanged: true}}}}},
		}}},
		BaseCommitID: deltaBaseCommit, CommitID: deltaCommit, Sparse: []string{"c", "b", "a"}, View: deltaView,
	}
	changes, err := lr.ApplyTreeDelta(in, nil)
	require.NoError(t, err)
	require.Len(t, changes.Written, 1)
	require.Equal(t, "a/new.txt", changes.Written[0].Path)
	require.Len(t, changes.Removed, 1)
	require.Equal(t, "c/three.txt", changes.Removed[0].Path)

	target := map[string]pushSpec{"a/one.txt": spec(1), "a/new.txt": spec(9), "b/keep/two.txt": spec(2)}
	require.Equal(t, manifestTree(target).Hash, changes.TreeHash)
	saved := deltaRepo(t, base, "a", "b", "c")
	require.NoError(t, saved.SaveSyncedTreeView(manifestTree(target), deltaCommit, []string{"a", "b", "c"}, deltaView))
	require.Equal(t, readFullState(t, saved), readFullState(t, lr))
}

// A held directory with the empty-tree hash is not part of a manifest: an
// unchanged entry for it drops the row, as treedelta.Apply drops the node.
func TestApplyTreeDelta_DropsAnEmptyHeldDirectory(t *testing.T) {
	base := map[string]pushSpec{"a/one.txt": spec(1)}
	lr := deltaRepo(t, base)
	_, err := lr.db.Exec(`INSERT INTO tree_nodes (path, parent_path, hash, mode) VALUES ('/void', '', ?, 0)`, emptyTreeHash.Bytes())
	require.NoError(t, err)
	in := clientDomain.TreeDeltaApply{
		Delta: &treedelta.Delta{Root: &treedelta.Dir{Name: "root", Dirs: []*treedelta.Dir{
			{Name: "a", Files: []treedelta.File{{Name: "one.txt", Changed: spec(5).manifestFile("a/one.txt")}}},
			{Name: "void", Unchanged: true},
		}}},
		BaseCommitID: deltaBaseCommit, CommitID: deltaCommit, View: deltaView,
	}
	changes, err := lr.ApplyTreeDelta(in, nil)
	require.NoError(t, err)
	target := map[string]pushSpec{"a/one.txt": spec(5)}
	require.Equal(t, manifestTree(target).Hash, changes.TreeHash)
	saved := newSyncedRepo(t)
	require.NoError(t, saved.SaveSyncedTreeView(manifestTree(target), deltaCommit, nil, deltaView))
	require.Equal(t, readState(t, saved), readState(t, lr))
}

// A file the delta carries in full although the tree holds exactly that row
// costs no write and is not reported.
func TestApplyTreeDelta_FileAlreadyHeldAsSent(t *testing.T) {
	lr := deltaRepo(t, applyBase)
	in := deltaInput(applyBase, with(applyBase, map[string]pushSpec{"src/c.go": spec(42)}))
	for i, f := range in.Delta.Root.Dirs {
		if f.Name != "src" {
			continue
		}
		for j, file := range in.Delta.Root.Dirs[i].Files {
			if file.Name == "a.go" {
				in.Delta.Root.Dirs[i].Files[j].Changed = applyBase["src/a.go"].manifestFile("src/a.go")
			}
		}
	}
	changes, err := lr.ApplyTreeDelta(in, nil)
	require.NoError(t, err)
	require.Len(t, changes.Written, 1)
	require.Equal(t, "src/c.go", changes.Written[0].Path)
}

func requireUntouched(t *testing.T, lr *LocalRepo, before fullState) {
	t.Helper()
	require.Equal(t, before, readFullState(t, lr), "a refused delta leaves everything as it was")
}

func TestApplyTreeDelta_RootHashMismatchRollsBack(t *testing.T) {
	target := with(without(applyBase, "docs/readme.md"), map[string]pushSpec{"src/a.go": spec(40), "new/f.txt": spec(41)})
	lr := deltaRepo(t, applyBase)
	before := readFullState(t, lr)
	in := deltaInput(applyBase, target)
	wrong := domain.Hash{0xde, 0xad}
	in.Delta.RootHash = &wrong

	for _, call := range []func() error{
		func() error { _, err := lr.PlanTreeDelta(in); return err },
		func() error { _, err := lr.ApplyTreeDelta(in, nil); return err },
	} {
		err := call()
		var mismatch *clientDomain.TreeDeltaMismatchError
		require.ErrorAs(t, err, &mismatch)
		require.Equal(t, wrong, mismatch.Expected)
		require.Equal(t, manifestTree(target).Hash, mismatch.Computed)
		requireUntouched(t, lr, before)
	}

	// Without a root hash there is nothing to verify the result against.
	in.Delta.RootHash = nil
	_, err := lr.ApplyTreeDelta(in, nil)
	require.NoError(t, err)
}

// A delta computed against another tree than the stored one: the stored tree
// misses a file the delta keeps, or leads to another root.
func TestApplyTreeDelta_WrongBase(t *testing.T) {
	target := with(applyBase, map[string]pushSpec{"src/c.go": spec(42)})
	in := deltaInput(applyBase, target)

	missing := deltaRepo(t, without(applyBase, "src/b.go"))
	before := readFullState(t, missing)
	_, err := missing.PlanTreeDelta(in)
	require.ErrorIs(t, err, clientDomain.ErrTreeDeltaBase)
	require.ErrorContains(t, err, `"/src/b.go"`)
	_, err = missing.ApplyTreeDelta(in, nil)
	require.ErrorIs(t, err, clientDomain.ErrTreeDeltaBase)
	requireUntouched(t, missing, before)

	// A file differs in a directory the delta lists: the root gives it away.
	differs := deltaRepo(t, with(applyBase, map[string]pushSpec{"src/b.go": spec(77)}))
	before = readFullState(t, differs)
	_, err = differs.ApplyTreeDelta(in, nil)
	var mismatch *clientDomain.TreeDeltaMismatchError
	require.ErrorAs(t, err, &mismatch)
	requireUntouched(t, differs, before)
}

func TestApplyTreeDelta_RefusesAnUnknownBase(t *testing.T) {
	target := with(applyBase, map[string]pushSpec{"src/c.go": spec(42)})
	cases := map[string]struct {
		setup func(t *testing.T, lr *LocalRepo)
		edit  func(in *clientDomain.TreeDeltaApply)
	}{
		"no record": {setup: func(t *testing.T, lr *LocalRepo) { require.NoError(t, lr.ClearSyncedTree()) }},
		"tree saved without a record": {setup: func(t *testing.T, lr *LocalRepo) {
			require.NoError(t, lr.SaveTree(manifestTree(applyBase)))
		}},
		"record of another commit":     {edit: func(in *clientDomain.TreeDeltaApply) { in.BaseCommitID = "other" }},
		"record of another sparse set": {edit: func(in *clientDomain.TreeDeltaApply) { in.Sparse = []string{"src"} }},
		"unreadable record": {setup: func(t *testing.T, lr *LocalRepo) {
			require.NoError(t, sqlcLocalrepo.New(lr.db).MetaSet(t.Context(), sqlcLocalrepo.MetaSetParams{Key: syncedTreeKey, Value: "{"}))
		}},
		"no base commit":   {edit: func(in *clientDomain.TreeDeltaApply) { in.BaseCommitID = "" }},
		"no target commit": {edit: func(in *clientDomain.TreeDeltaApply) { in.CommitID = "" }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lr := deltaRepo(t, applyBase)
			if tc.setup != nil {
				tc.setup(t, lr)
			}
			in := deltaInput(applyBase, target)
			if tc.edit != nil {
				tc.edit(&in)
			}
			before := readFullState(t, lr)
			_, err := lr.PlanTreeDelta(in)
			require.ErrorIs(t, err, clientDomain.ErrTreeDeltaBase)
			_, err = lr.ApplyTreeDelta(in, nil)
			require.ErrorIs(t, err, clientDomain.ErrTreeDeltaBase)
			requireUntouched(t, lr, before)
		})
	}
}

// Between plan and apply the sync drops the record; anything else that
// happened to the tree in between is noticed.
func TestApplyTreeDelta_PlannedAgainstAnotherTree(t *testing.T) {
	target := with(applyBase, map[string]pushSpec{"src/c.go": spec(42)})
	in := deltaInput(applyBase, target)
	in.Delta.RootHash = nil

	t.Run("the stored tree was replaced", func(t *testing.T) {
		lr := deltaRepo(t, applyBase)
		planned, err := lr.PlanTreeDelta(in)
		require.NoError(t, err)
		require.NoError(t, lr.SaveTree(manifestTree(without(applyBase, "docs/readme.md"))))
		before := readFullState(t, lr)
		_, err = lr.ApplyTreeDelta(in, planned)
		require.ErrorIs(t, err, clientDomain.ErrTreeDeltaBase)
		requireUntouched(t, lr, before)
	})
	t.Run("the stored tree was replaced and recorded as the base", func(t *testing.T) {
		lr := deltaRepo(t, applyBase)
		planned, err := lr.PlanTreeDelta(in)
		require.NoError(t, err)
		require.NoError(t, lr.SaveSyncedTreeView(manifestTree(without(applyBase, "docs/readme.md")), deltaBaseCommit, nil, deltaBaseView))
		before := readFullState(t, lr)
		_, err = lr.ApplyTreeDelta(in, planned)
		require.ErrorIs(t, err, clientDomain.ErrTreeDeltaBase)
		requireUntouched(t, lr, before)
	})
	t.Run("the plan leads elsewhere", func(t *testing.T) {
		lr := deltaRepo(t, applyBase)
		planned, err := lr.PlanTreeDelta(in)
		require.NoError(t, err)
		planned.TreeHash = domain.Hash{1, 2, 3}
		require.NoError(t, lr.ClearSyncedTree())
		before := readFullState(t, lr)
		_, err = lr.ApplyTreeDelta(in, planned)
		var mismatch *clientDomain.TreeDeltaMismatchError
		require.ErrorAs(t, err, &mismatch)
		require.Equal(t, domain.Hash{1, 2, 3}, mismatch.Expected)
		requireUntouched(t, lr, before)
	})
	t.Run("the record names another commit", func(t *testing.T) {
		lr := deltaRepo(t, applyBase)
		planned, err := lr.PlanTreeDelta(in)
		require.NoError(t, err)
		require.NoError(t, lr.SaveSyncedTreeView(manifestTree(applyBase), "elsewhere", nil, deltaBaseView))
		_, err = lr.ApplyTreeDelta(in, planned)
		require.ErrorIs(t, err, clientDomain.ErrTreeDeltaBase)
	})
}

func TestApplyTreeDelta_RejectsMalformedInput(t *testing.T) {
	file := func(name string) treedelta.File {
		return treedelta.File{Name: name, Changed: spec(1).manifestFile(name)}
	}
	cases := map[string]*treedelta.Delta{
		"no delta":            nil,
		"no root":             {},
		"nil directory":       {Root: &treedelta.Dir{Dirs: []*treedelta.Dir{nil}}},
		"unnamed directory":   {Root: &treedelta.Dir{Dirs: []*treedelta.Dir{{}}}},
		"directory with path": {Root: &treedelta.Dir{Dirs: []*treedelta.Dir{{Name: "a/b"}}}},
		"directory twice":     {Root: &treedelta.Dir{Dirs: []*treedelta.Dir{{Name: "src", Unchanged: true}, {Name: "src", Unchanged: true}}}},
		"unnamed file":        {Root: &treedelta.Dir{Files: []treedelta.File{file("")}}},
		"file with path":      {Root: &treedelta.Dir{Files: []treedelta.File{file("a/b")}}},
		"file twice":          {Root: &treedelta.Dir{Files: []treedelta.File{file("x"), file("x")}}},
		"kept file twice":     {Root: &treedelta.Dir{Files: []treedelta.File{{Name: "root.txt"}, {Name: "root.txt"}}}},
		"bad nested entry":    {Root: &treedelta.Dir{Dirs: []*treedelta.Dir{{Name: "src", Dirs: []*treedelta.Dir{nil}}}}},
	}
	for name, delta := range cases {
		t.Run(name, func(t *testing.T) {
			lr := deltaRepo(t, applyBase)
			before := readFullState(t, lr)
			in := clientDomain.TreeDeltaApply{Delta: delta, BaseCommitID: deltaBaseCommit, CommitID: deltaCommit}
			_, err := lr.PlanTreeDelta(in)
			require.Error(t, err)
			require.False(t, errors.Is(err, clientDomain.ErrTreeDeltaBase) && !strings.Contains(name, "kept"), err)
			_, err = lr.ApplyTreeDelta(in, nil)
			require.Error(t, err)
			requireUntouched(t, lr, before)
		})
	}
}

func TestApplyTreeDelta_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	_, err := lr.PlanTreeDelta(clientDomain.TreeDeltaApply{})
	require.ErrorContains(t, err, "not initialized")
	_, err = lr.ApplyTreeDelta(clientDomain.TreeDeltaApply{}, nil)
	require.ErrorContains(t, err, "not initialized")
}

func TestApplyTreeDelta_FailsOnBrokenDatabase(t *testing.T) {
	target := with(without(applyBase, "docs/readme.md", "src/b.go", "src/pkg/deep/x.go", "src/pkg/deep/y.go", "solo/chain/only.txt"),
		map[string]pushSpec{"src/a.go": spec(95), "fresh/dir/f.txt": spec(96), "src/pkg": spec(97)})
	in := deltaInput(applyBase, target)

	// Each statement an apply runs fails once, in turn; none may leave a
	// partial tree behind.
	failed := 0
	for _, table := range []string{"meta", "tree_nodes", "files", "stat_cache"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			t.Run(table+" "+op, func(t *testing.T) {
				lr := deltaRepo(t, applyBase)
				before := readFullState(t, lr)
				_, err := lr.db.Exec(fmt.Sprintf(
					`CREATE TRIGGER fail_write BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'write refused'); END`, op, table))
				require.NoError(t, err)

				_, err = lr.ApplyTreeDelta(in, nil)
				if err == nil {
					// The apply does not run this kind of statement on this table.
					return
				}
				failed++
				require.ErrorContains(t, err, "write refused")
				requireUntouched(t, lr, before)
			})
		}
	}
	require.GreaterOrEqual(t, failed, 7, "the case must exercise every kind of write")

	closed := deltaRepo(t, applyBase)
	require.NoError(t, closed.db.Close())
	_, err := closed.PlanTreeDelta(in)
	require.Error(t, err)
	_, err = closed.ApplyTreeDelta(in, nil)
	require.Error(t, err)

	for _, table := range []string{"meta", "tree_nodes", "files"} {
		t.Run("read "+table, func(t *testing.T) {
			lr := deltaRepo(t, applyBase)
			_, err := lr.db.Exec(`ALTER TABLE ` + table + ` RENAME TO gone`)
			require.NoError(t, err)
			_, err = lr.PlanTreeDelta(in)
			require.Error(t, err)
			_, err = lr.ApplyTreeDelta(in, nil)
			require.Error(t, err)
		})
	}

	// An unchanged root still reads the root row.
	lr := deltaRepo(t, applyBase)
	_, err = lr.db.Exec(`ALTER TABLE tree_nodes RENAME TO gone`)
	require.NoError(t, err)
	_, err = lr.PlanTreeDelta(clientDomain.TreeDeltaApply{
		Delta:        &treedelta.Delta{Root: &treedelta.Dir{Unchanged: true}},
		BaseCommitID: deltaBaseCommit, CommitID: deltaCommit,
	})
	require.Error(t, err)
}

// A stored chunk list that cannot be read fails the plan, for a row the
// delta replaces as for a row that leaves with its directory.
func TestApplyTreeDelta_UnreadableStoredRow(t *testing.T) {
	for name, tc := range map[string]struct {
		path   string
		target map[string]pushSpec
	}{
		"replaced row":          {"/src/a.go", with(applyBase, map[string]pushSpec{"src/a.go": spec(40)})},
		"row below a subtree":   {"/src/pkg/deep/x.go", without(applyBase, "src/pkg/deep/x.go", "src/pkg/deep/y.go")},
		"row of a removed file": {"/docs/readme.md", with(without(applyBase, "docs/readme.md"), map[string]pushSpec{"docs/other.md": spec(41)})},
	} {
		t.Run(name, func(t *testing.T) {
			lr := deltaRepo(t, applyBase)
			in := deltaInput(applyBase, tc.target)
			_, err := lr.db.Exec(`UPDATE files SET chunks = X'0102' WHERE path = ?`, tc.path)
			require.NoError(t, err)
			before := readFullState(t, lr)
			_, err = lr.ApplyTreeDelta(in, nil)
			require.Error(t, err)
			requireUntouched(t, lr, before)
		})
	}
}

// More replaced rows than one by-path query binds.
func TestApplyTreeDelta_ManyFilesInOneDirectory(t *testing.T) {
	base := map[string]pushSpec{}
	target := map[string]pushSpec{}
	for i := 0; i < pathBatch+120; i++ {
		path := fmt.Sprintf("big/f%04d.txt", i)
		base[path] = spec(byte(i % 50))
		if i%3 != 0 {
			target[path] = spec(byte(100 + i%50))
		}
	}
	lr := deltaRepo(t, base)
	changes, err := lr.ApplyTreeDelta(deltaInput(base, target), nil)
	require.NoError(t, err)
	requireChanges(t, base, target, changes)
	saved := deltaRepo(t, base)
	require.NoError(t, saved.SaveSyncedTreeView(manifestTree(target), deltaCommit, nil, deltaView))
	require.Equal(t, readFullState(t, saved), readFullState(t, lr))
}

// A small change in a large tree costs reads and writes in proportion to the
// changed directories, never to the size of the tree.
func TestApplyTreeDelta_CostFollowsTheChange(t *testing.T) {
	const dirs, perDir = 60, 80
	files := make(map[string]pushSpec, dirs*perDir)
	for d := 0; d < dirs; d++ {
		for f := 0; f < perDir; f++ {
			files[fmt.Sprintf("d%03d/sub/f%03d.txt", d, f)] = pushSpec{seed: byte(d), chunks: 1, perm: 0o644, encoding: chunker.EncodingZstd}
		}
	}
	target := with(without(files, "d007/sub/f001.txt"), map[string]pushSpec{
		"d007/sub/f000.txt":              spec(201),
		"d007/sub/deeper/still/leaf.txt": spec(202),
	})
	lr := deltaRepo(t, files)
	in := deltaInput(files, target)

	// Reads: the plan asks only about the changed directories and the rows
	// it replaces or removes.
	counter := &countingDB{DBTX: lr.db}
	plan, err := planTreeDelta(context.Background(), sqlcLocalrepo.New(counter), in, nil)
	require.NoError(t, err)
	changed := []string{"", "/d007", "/d007/sub", "/d007/sub/deeper", "/d007/sub/deeper/still"}
	// The record and tree hash, then node + files + children of the three
	// directories that exist (a new directory reads its missing row only),
	// then one by-path read of the two replaced rows.
	require.Len(t, counter.queries, 2+3*3+2+1, "%v", counter.queries)
	require.Len(t, plan.rows.nodeUpserts, len(changed))
	require.Len(t, plan.rows.fileUpserts, 2)
	require.Equal(t, []string{"/d007/sub/f001.txt"}, plan.rows.fileDeletes)
	require.Empty(t, plan.rows.nodeDeletes)
	require.Empty(t, plan.subtrees)

	// Writes: two file rows, one delete and one row per changed directory.
	for _, table := range []string{"tree_nodes", "files"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			_, err := lr.db.Exec(fmt.Sprintf(
				`CREATE TRIGGER log_%s_%s AFTER %s ON %s BEGIN INSERT INTO meta (key, value) VALUES ('w' || hex(randomblob(8)), '%s'); END`,
				table, op, op, table, table))
			require.NoError(t, err)
		}
	}
	_, err = lr.ApplyTreeDelta(in, nil)
	require.NoError(t, err)
	var written int
	require.NoError(t, lr.db.QueryRow(`SELECT COUNT(*) FROM meta WHERE key LIKE 'w%'`).Scan(&written))
	require.Equal(t, 3+len(changed), written, "%d files are stored", len(files))

	_, err = lr.db.Exec(`DELETE FROM meta WHERE key LIKE 'w%'`)
	require.NoError(t, err)
	saved := deltaRepo(t, files)
	require.NoError(t, saved.SaveSyncedTreeView(manifestTree(target), deltaCommit, nil, deltaView))
	require.Equal(t, readFullState(t, saved), readFullState(t, lr))
}

func TestSaveSyncedTreeView_RoundTrip(t *testing.T) {
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTreeView(syncedTestTree("a.txt"), "c1", []string{"b", "a"}, []byte{1, 2, 3}))
	require.Equal(t, `{"commit":"c1","sparse":["a","b"],"view":"AQID"}`, rawSyncedValue(t, lr))
	synced, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, &clientDomain.SyncedTree{CommitID: "c1", Sparse: []string{"a", "b"}, View: []byte{1, 2, 3}}, synced)

	// A manifest saved without a token drops the one stored before.
	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c2", nil))
	require.Equal(t, `{"commit":"c2","sparse":[]}`, rawSyncedValue(t, lr))
	synced, err = lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Nil(t, synced.View)

	// No commit, no record, whatever the token.
	require.NoError(t, lr.SaveSyncedTreeView(syncedTestTree("a.txt"), "", nil, []byte{1}))
	synced, err = lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Nil(t, synced)
}

// A push applied locally keeps the view token of the tree it was applied to:
// the token names the view, not the commit.
func TestApplyPush_KeepsTheViewToken(t *testing.T) {
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTreeView(manifestTree(applyBase), "c1", nil, []byte("token")))
	tc := pushCase{base: applyBase, pushed: map[string]pushSpec{"src/a.go": spec(90)}}
	require.NoError(t, lr.ApplyPush(tc.apply("c1", "c2", manifestTree(tc.after()).Hash)))
	synced, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, &clientDomain.SyncedTree{CommitID: "c2", Sparse: []string{}, View: []byte("token")}, synced)
}
