package usecase

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
)

// fakeTreeHashRepo is an in-memory tree_nodes/files store. Writes go straight
// to the rows, like the SQL repositories inside the backfill transaction.
type fakeTreeHashRepo struct {
	nodes     map[int64]*TreeHashNode
	files     map[int64][]treehash.FileEntry
	watermark int64
	// overlap is how many ids below the watermark the next pass rescans
	// (the rows the SQL repositories find created within the overlap window).
	overlap  int64
	legacy   map[int64]domain.Hash
	rewrites map[domain.Hash]domain.Hash
	writes   int
	failOn   string
}

func newFakeTreeHashRepo() *fakeTreeHashRepo {
	return &fakeTreeHashRepo{
		nodes:    map[int64]*TreeHashNode{},
		files:    map[int64][]treehash.FileEntry{},
		legacy:   map[int64]domain.Hash{},
		rewrites: map[domain.Hash]domain.Hash{},
	}
}

func (f *fakeTreeHashRepo) fail(op string) error {
	if f.failOn == op {
		return errors.New(op + " failed")
	}
	return nil
}

func (f *fakeTreeHashRepo) add(id int64, parent *int64, name string, hash domain.Hash, files ...treehash.FileEntry) {
	f.nodes[id] = &TreeHashNode{ID: id, ParentID: parent, Name: name, Hash: hash}
	if len(files) > 0 {
		f.files[id] = files
	}
}

func (f *fakeTreeHashRepo) hasContent(id int64) bool {
	if len(f.files[id]) > 0 {
		return true
	}
	for _, n := range f.nodes {
		if n.ParentID != nil && *n.ParentID == id {
			return true
		}
	}
	return false
}

func (f *fakeTreeHashRepo) WithTreeHashBackfillLock(ctx context.Context, fn func(ctx context.Context, state TreeHashBackfillState) (int64, error)) error {
	next, err := fn(ctx, TreeHashBackfillState{Watermark: f.watermark, RescanFrom: max(f.watermark-f.overlap, 0)})
	if err != nil {
		return err
	}
	f.watermark = next
	return nil
}

func (f *fakeTreeHashRepo) MaxTreeNodeID(context.Context) (int64, error) {
	var max int64
	for id := range f.nodes {
		if id > max {
			max = id
		}
	}
	return max, f.fail("max")
}

func (f *fakeTreeHashRepo) TreeHashSeeds(_ context.Context, after, upTo int64) ([]int64, error) {
	var ids []int64
	for id, files := range f.files {
		if id <= after || id > upTo {
			continue
		}
		for _, file := range files {
			if file.Mode != 0 && file.Mode != 0o644 {
				ids = append(ids, id)
				break
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, f.fail("seeds")
}

func (f *fakeTreeHashRepo) TreeHashNode(_ context.Context, id int64) (*TreeHashNode, error) {
	n, ok := f.nodes[id]
	if !ok {
		return nil, domain.NewErrorNotFound("tree node not found")
	}
	c := *n
	c.HasContent = f.hasContent(id)
	return &c, f.fail("node")
}

func (f *fakeTreeHashRepo) TreeHashFiles(_ context.Context, id int64) ([]treehash.FileEntry, error) {
	return f.files[id], f.fail("files")
}

func (f *fakeTreeHashRepo) TreeHashChildren(_ context.Context, id int64) ([]TreeHashNode, error) {
	var out []TreeHashNode
	for _, n := range f.nodes {
		if n.ParentID != nil && *n.ParentID == id {
			out = append(out, *n)
		}
	}
	return out, f.fail("children")
}

func (f *fakeTreeHashRepo) TreeHashNodesByHash(_ context.Context, hash domain.Hash) ([]TreeHashNode, error) {
	var out []TreeHashNode
	for id, n := range f.nodes {
		if n.Hash == hash {
			c := *n
			c.HasContent = f.hasContent(id)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, f.fail("byhash")
}

func (f *fakeTreeHashRepo) TreeHashDanglingReferences(_ context.Context, after, upTo int64) ([]TreeHashDanglingReference, error) {
	var out []TreeHashDanglingReference
	for id, n := range f.nodes {
		newHash, ok := f.rewrites[n.Hash]
		if id <= after || id > upTo || !ok || f.hasContent(id) {
			continue
		}
		resolves := false
		for otherID, other := range f.nodes {
			if other.Hash == n.Hash && f.hasContent(otherID) {
				resolves = true
				break
			}
		}
		if !resolves {
			out = append(out, TreeHashDanglingReference{Node: *n, NewHash: newHash})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node.ID < out[j].Node.ID })
	return out, f.fail("dangling")
}

func (f *fakeTreeHashRepo) SaveTreeHashRewrite(_ context.Context, oldHash, newHash domain.Hash) error {
	if err := f.fail("rewrite"); err != nil {
		return err
	}
	if _, ok := f.rewrites[oldHash]; !ok {
		f.rewrites[oldHash] = newHash
	}
	return nil
}

func (f *fakeTreeHashRepo) SetTreeNodeHash(_ context.Context, id int64, hash domain.Hash) error {
	if err := f.fail("set"); err != nil {
		return err
	}
	f.nodes[id].Hash = hash
	f.writes++
	return nil
}

func (f *fakeTreeHashRepo) SaveLegacyTreeHash(_ context.Context, id int64, hash domain.Hash) error {
	if err := f.fail("legacy"); err != nil {
		return err
	}
	if _, ok := f.legacy[id]; !ok {
		f.legacy[id] = hash
	}
	return nil
}

func (f *fakeTreeHashRepo) LegacyTreeHash(_ context.Context, id int64) (*domain.Hash, error) {
	h, ok := f.legacy[id]
	if !ok {
		return nil, nil
	}
	return &h, nil
}

// oldTreeHash is the pre-mode serialization (every file hashed as read-write).
func oldTreeHash(files []treehash.FileEntry, trees []treehash.TreeEntry) domain.Hash {
	plain := make([]treehash.FileEntry, len(files))
	for i, file := range files {
		plain[i] = treehash.FileEntry{Name: file.Name, Hash: file.Hash}
	}
	return treehash.TreeHash(plain, trees)
}

func i64(v int64) *int64 { return &v }

func TestBackfillTreeHashes_RecomputesStaleTrees(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTreeHashRepo()
	tool := treehash.FileEntry{Name: "tool.sh", Hash: domain.Hash{0x01}, Mode: 0o755}
	readme := treehash.FileEntry{Name: "README", Hash: domain.Hash{0x02}, Mode: 0o644}
	a1 := treehash.FileEntry{Name: "a.txt", Hash: domain.Hash{0x03}, Mode: 0o644}
	a2 := treehash.FileEntry{Name: "a.txt", Hash: domain.Hash{0x04}, Mode: 0o644}

	oldBin := oldTreeHash([]treehash.FileEntry{tool}, nil)
	oldTools := oldTreeHash([]treehash.FileEntry{readme}, []treehash.TreeEntry{{Name: "bin", Hash: oldBin}})
	oldRoot1 := oldTreeHash([]treehash.FileEntry{a1}, []treehash.TreeEntry{{Name: "tools", Hash: oldTools}})
	oldRoot2 := oldTreeHash([]treehash.FileEntry{a2}, []treehash.TreeEntry{{Name: "tools", Hash: oldTools}})

	// Commit 1 stores every directory; commit 2 changes a.txt and keeps
	// tools/ as a reference to commit 1's rows.
	repo.add(1, nil, "root", oldRoot1, a1)
	repo.add(2, i64(1), "tools", oldTools, readme)
	repo.add(3, i64(2), "bin", oldBin, tool)
	repo.add(4, nil, "root", oldRoot2, a2)
	repo.add(5, i64(4), "tools", oldTools)
	// An unrelated read-write-only tree keeps its hash.
	plainHash := treehash.TreeHash([]treehash.FileEntry{readme}, nil)
	repo.add(6, nil, "root", plainHash, readme)

	newBin := treehash.TreeHash([]treehash.FileEntry{tool}, nil)
	newTools := treehash.TreeHash([]treehash.FileEntry{readme}, []treehash.TreeEntry{{Name: "bin", Hash: newBin}})
	newRoot1 := treehash.TreeHash([]treehash.FileEntry{a1}, []treehash.TreeEntry{{Name: "tools", Hash: newTools}})
	newRoot2 := treehash.TreeHash([]treehash.FileEntry{a2}, []treehash.TreeEntry{{Name: "tools", Hash: newTools}})
	require.NotEqual(t, oldBin, newBin)

	res, err := BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, TreeHashBackfillResult{FromID: 0, UpToID: 6, Seeds: 1, Recomputed: 4, Updated: 5}, res)
	require.Equal(t, newBin, repo.nodes[3].Hash)
	require.Equal(t, newTools, repo.nodes[2].Hash)
	require.Equal(t, newTools, repo.nodes[5].Hash, "the reference follows its content node")
	require.Equal(t, newRoot1, repo.nodes[1].Hash)
	require.Equal(t, newRoot2, repo.nodes[4].Hash)
	require.Equal(t, plainHash, repo.nodes[6].Hash)
	require.Equal(t, map[int64]domain.Hash{1: oldRoot1, 4: oldRoot2}, repo.legacy)
	require.Equal(t, map[domain.Hash]domain.Hash{oldBin: newBin, oldTools: newTools, oldRoot1: newRoot1, oldRoot2: newRoot2}, repo.rewrites)
	require.Equal(t, int64(6), repo.watermark)

	// Nothing new: the pass is a no-op.
	writes := repo.writes
	res, err = BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, TreeHashBackfillResult{FromID: 6, UpToID: 6}, res)

	// A full rerun recomputes but writes nothing.
	repo.watermark = 0
	res, err = BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, 0, res.Updated)
	require.Equal(t, writes, repo.writes)
	require.Equal(t, map[int64]domain.Hash{1: oldRoot1, 4: oldRoot2}, repo.legacy, "legacy hashes are kept")
}

// Before the fix, a mode-only push stored a second content node under the same
// hash. References keep resolving to whichever content node was oldest, and
// each content node gets the hash of its own rows.
func TestBackfillTreeHashes_SharedHashKeepsResolution(t *testing.T) {
	ctx := context.Background()
	for _, execFirst := range []bool{false, true} {
		repo := newFakeTreeHashRepo()
		rw := treehash.FileEntry{Name: "lib.so", Hash: domain.Hash{0x09}, Mode: 0o644}
		ex := treehash.FileEntry{Name: "lib.so", Hash: domain.Hash{0x09}, Mode: 0o755}
		first, second := rw, ex
		if execFirst {
			first, second = ex, rw
		}
		shared := oldTreeHash([]treehash.FileEntry{rw}, nil)
		oldRoot := oldTreeHash(nil, []treehash.TreeEntry{{Name: "lib", Hash: shared}})
		repo.add(1, nil, "root", oldRoot)
		repo.add(2, i64(1), "lib", shared, first)
		repo.add(3, nil, "root", oldRoot)
		repo.add(4, i64(3), "lib", shared, second)
		repo.add(5, nil, "root", oldRoot)
		repo.add(6, i64(5), "lib", shared) // reference

		_, err := BackfillTreeHashes(ctx, repo)
		require.NoError(t, err)
		hashFirst := treehash.TreeHash([]treehash.FileEntry{first}, nil)
		hashSecond := treehash.TreeHash([]treehash.FileEntry{second}, nil)
		require.Equal(t, hashFirst, repo.nodes[2].Hash)
		require.Equal(t, hashSecond, repo.nodes[4].Hash)
		require.Equal(t, hashFirst, repo.nodes[6].Hash, "execFirst=%v: the reference keeps the content it resolved to", execFirst)
		require.Equal(t, treehash.TreeHash(nil, []treehash.TreeEntry{{Name: "lib", Hash: hashSecond}}), repo.nodes[3].Hash)
		require.Equal(t, treehash.TreeHash(nil, []treehash.TreeEntry{{Name: "lib", Hash: hashFirst}}), repo.nodes[5].Hash)
	}
}

func TestBackfillTreeHashes_IncrementalAfterWatermark(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTreeHashRepo()
	tool := treehash.FileEntry{Name: "tool.sh", Hash: domain.Hash{0x01}, Mode: 0o755}
	repo.add(1, nil, "root", treehash.TreeHash([]treehash.FileEntry{tool}, nil), tool)
	_, err := BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, 0, repo.writes)

	// A row written later in the old format (e.g. by a server that was not
	// upgraded yet) is picked up by the next pass.
	stale := oldTreeHash([]treehash.FileEntry{tool}, nil)
	repo.add(2, nil, "root", stale, tool)
	res, err := BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, TreeHashBackfillResult{FromID: 1, UpToID: 2, Seeds: 1, Recomputed: 1, Updated: 1}, res)
	require.Equal(t, repo.nodes[1].Hash, repo.nodes[2].Hash)
}

func TestBackfillTreeHashes_Errors(t *testing.T) {
	ctx := context.Background()
	for _, op := range []string{"max", "seeds", "dangling", "node", "files", "children", "byhash", "set", "legacy", "rewrite"} {
		repo := newFakeTreeHashRepo()
		tool := treehash.FileEntry{Name: "tool.sh", Hash: domain.Hash{0x01}, Mode: 0o755}
		repo.add(1, nil, "root", oldTreeHash(nil, []treehash.TreeEntry{{Name: "bin", Hash: oldTreeHash([]treehash.FileEntry{tool}, nil)}}))
		repo.add(2, i64(1), "bin", oldTreeHash([]treehash.FileEntry{tool}, nil), tool)
		repo.failOn = op
		_, err := BackfillTreeHashes(ctx, repo)
		require.ErrorContains(t, err, op+" failed")
		require.Equal(t, int64(0), repo.watermark, "a failed pass keeps the watermark")
	}
}

func TestPush_LegacyBaseTreeHash(t *testing.T) {
	newHead := domain.Hash{0x02}
	legacy := domain.Hash{0x01}
	setup := func(t *testing.T, lookup legacyTreeHashLookup) (*Push, context.Context) {
		uc, perm, repo, pushRepo, ctx := newPushFixture(t)
		uc.WithLegacyTreeHashes(lookup)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", CommitID: idPtr(snow.ID(9))}, nil)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(9)).
			Return(&domain.Commit{ID: 9, TreeID: 100, Hash: domain.Hash{5}}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
			Return(&domain.TreeNode{ID: 100, Hash: newHead, Name: "root"}, nil).AnyTimes()
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
		pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		return uc, ctx
	}
	ch, fh := chunkAndFileHash(t, "hello")
	files := func() []*domain.PushFile {
		return []*domain.PushFile{{Path: "a.txt", Mode: 0o644, SizeBytes: 5, FileHash: fh, ChunkHashes: []domain.Hash{ch}}}
	}

	t.Run("accepted", func(t *testing.T) {
		repo := newFakeTreeHashRepo()
		repo.legacy[100] = legacy
		uc, ctx := setup(t, repo)
		_, err := uc.Push(ctx, snow.ID(1), "main", legacy.String(), "msg", files(), nil, "", "")
		require.NoError(t, err)
	})
	t.Run("other hash refused", func(t *testing.T) {
		repo := newFakeTreeHashRepo()
		repo.legacy[100] = legacy
		uc, ctx := setup(t, repo)
		_, err := uc.Push(ctx, snow.ID(1), "main", domain.Hash{0x07}.String(), "msg", files(), nil, "", "")
		require.True(t, domain.IsErrorConflict(err))
	})
	t.Run("no legacy row", func(t *testing.T) {
		uc, ctx := setup(t, newFakeTreeHashRepo())
		_, err := uc.Push(ctx, snow.ID(1), "main", legacy.String(), "msg", files(), nil, "", "")
		require.True(t, domain.IsErrorConflict(err))
	})
	t.Run("lookup error", func(t *testing.T) {
		uc, ctx := setup(t, failingLegacyLookup{})
		_, err := uc.Push(ctx, snow.ID(1), "main", legacy.String(), "msg", files(), nil, "", "")
		require.ErrorContains(t, err, "lookup failed")
	})
}

type failingLegacyLookup struct{}

func (failingLegacyLookup) LegacyTreeHash(context.Context, int64) (*domain.Hash, error) {
	return nil, errors.New("lookup failed")
}

// rollingDeployFixture is a backfilled store (tools/bin holds an executable)
// plus the hashes an older server still has in mind for it.
type rollingDeployFixture struct {
	repo                       *fakeTreeHashRepo
	tool, a2                   treehash.FileEntry
	oldBin, oldRoot2           domain.Hash
	newBin, newRoot1, newRoot2 domain.Hash
}

func newRollingDeployFixture(t *testing.T) rollingDeployFixture {
	t.Helper()
	repo := newFakeTreeHashRepo()
	tool := treehash.FileEntry{Name: "tool.sh", Hash: domain.Hash{0x01}, Mode: 0o755}
	a1 := treehash.FileEntry{Name: "a.txt", Hash: domain.Hash{0x03}, Mode: 0o644}
	a2 := treehash.FileEntry{Name: "a.txt", Hash: domain.Hash{0x04}, Mode: 0o644}
	oldBin := oldTreeHash([]treehash.FileEntry{tool}, nil)
	repo.add(1, nil, "root", oldTreeHash([]treehash.FileEntry{a1}, []treehash.TreeEntry{{Name: "bin", Hash: oldBin}}), a1)
	repo.add(2, i64(1), "bin", oldBin, tool)
	_, err := BackfillTreeHashes(context.Background(), repo)
	require.NoError(t, err)
	newBin := treehash.TreeHash([]treehash.FileEntry{tool}, nil)
	require.Equal(t, newBin, repo.nodes[2].Hash)
	return rollingDeployFixture{
		repo: repo, tool: tool, a2: a2,
		oldBin:   oldBin,
		oldRoot2: oldTreeHash([]treehash.FileEntry{a2}, []treehash.TreeEntry{{Name: "bin", Hash: oldBin}}),
		newBin:   newBin,
		newRoot1: treehash.TreeHash([]treehash.FileEntry{a1}, []treehash.TreeEntry{{Name: "bin", Hash: newBin}}),
		newRoot2: treehash.TreeHash([]treehash.FileEntry{a2}, []treehash.TreeEntry{{Name: "bin", Hash: newBin}}),
	}
}

// An older server that read bin/'s hash before the pass rewrote it keeps bin/
// by reference under the old hash, which no content node carries any more. The
// next pass repoints it through tree_hash_rewrites and fixes its root.
func TestBackfillTreeHashes_RepairsDanglingReference(t *testing.T) {
	ctx := context.Background()
	f := newRollingDeployFixture(t)
	repo := f.repo
	require.Equal(t, f.newBin, repo.rewrites[f.oldBin])

	repo.add(3, nil, "root", f.oldRoot2, f.a2)
	repo.add(4, i64(3), "bin", f.oldBin) // reference with the pre-backfill hash
	res, err := BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, TreeHashBackfillResult{FromID: 2, UpToID: 4, Repaired: 1, Recomputed: 1, Updated: 2}, res)
	require.Equal(t, f.newBin, repo.nodes[4].Hash, "the reference resolves to bin/'s content again")
	require.Equal(t, f.newRoot2, repo.nodes[3].Hash)
	require.Equal(t, f.oldRoot2, repo.legacy[3], "an old client can still push against the root it got")

	// Idempotent: rerunning everything changes nothing.
	writes := repo.writes
	repo.watermark = 0
	res, err = BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Zero(t, res.Repaired)
	require.Zero(t, res.Updated)
	require.Equal(t, writes, repo.writes)
}

// A push that took its ids before the previous pass read MAX(id) but committed
// after it lands at or below the watermark. The overlap window rescans it.
func TestBackfillTreeHashes_RescansOverlapWindow(t *testing.T) {
	ctx := context.Background()
	f := newRollingDeployFixture(t)
	repo := f.repo
	// Ids 3 and 4 were taken by the push before the pass that stored
	// watermark 6 (ids 5 and 6 committed in time).
	repo.add(5, nil, "root", treehash.TreeHash(nil, nil))
	repo.add(6, nil, "root", treehash.TreeHash(nil, nil))
	res, err := BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, int64(6), repo.watermark)
	require.Zero(t, res.Updated)

	staleBin := oldTreeHash([]treehash.FileEntry{f.tool}, nil)
	repo.add(3, nil, "root", oldTreeHash([]treehash.FileEntry{f.a2}, []treehash.TreeEntry{{Name: "bin", Hash: staleBin}}), f.a2)
	repo.add(4, i64(3), "bin", staleBin, f.tool)

	// Without the overlap the late rows are never looked at.
	res, err = BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, TreeHashBackfillResult{FromID: 6, UpToID: 6}, res)
	require.Equal(t, staleBin, repo.nodes[4].Hash)

	repo.overlap = 4
	res, err = BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, TreeHashBackfillResult{FromID: 2, UpToID: 6, Seeds: 1, Recomputed: 2, Updated: 2}, res)
	require.Equal(t, f.newBin, repo.nodes[4].Hash)
	require.Equal(t, f.newRoot2, repo.nodes[3].Hash)
	require.Equal(t, int64(6), repo.watermark, "the watermark never moves back")

	// Idempotent: the window is rescanned on the next pass but nothing changes.
	writes := repo.writes
	res, err = BackfillTreeHashes(ctx, repo)
	require.NoError(t, err)
	require.Equal(t, 1, res.Seeds)
	require.Zero(t, res.Updated)
	require.Equal(t, writes, repo.writes)
}

// A dangling reference whose rewrite target equals its hash is left alone.
func TestBackfillTreeHashes_RepairSkipsUnchanged(t *testing.T) {
	b := newTreeHashBackfill(newFakeTreeHashRepo())
	h := domain.Hash{0x05}
	require.Zero(t, b.repair([]TreeHashDanglingReference{{Node: TreeHashNode{ID: 1, Hash: h}, NewHash: h}}))
	require.Equal(t, 1, b.repair([]TreeHashDanglingReference{{Node: TreeHashNode{ID: 2, Hash: h}, NewHash: domain.Hash{0x06}}}))
	require.Equal(t, 0, b.queue.Len(), "a reference without parent queues nothing")
}
