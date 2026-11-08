package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type stubCacheRepo struct {
	initErr     error
	snapshot    *domain.Snapshot
	snapshotErr error
	merge       *domain.MergeState
	mergeErr    error
	revert      *domain.RevertState
	revertErr   error
	pruneFiles  int
	pruneBytes  int64
	pruneErr    error
	pruned      bool
	keep        map[serverDomain.Hash]struct{}
}

func (s *stubCacheRepo) Init(string) error { return s.initErr }
func (s *stubCacheRepo) Snapshot() (*domain.Snapshot, error) {
	return s.snapshot, s.snapshotErr
}
func (s *stubCacheRepo) LoadMergeState() (*domain.MergeState, error) { return s.merge, s.mergeErr }
func (s *stubCacheRepo) LoadRevertState() (*domain.RevertState, error) {
	return s.revert, s.revertErr
}
func (s *stubCacheRepo) PruneObjects(keep map[serverDomain.Hash]struct{}) (int, int64, error) {
	s.pruned = true
	s.keep = keep
	return s.pruneFiles, s.pruneBytes, s.pruneErr
}

func TestPruneCache_KeepsEverySnapshotChunk(t *testing.T) {
	a, b, c := serverDomain.Hash{1}, serverDomain.Hash{2}, serverDomain.Hash{3}
	repo := &stubCacheRepo{
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{
			{Path: "a", Chunks: []serverDomain.Hash{a, b}},
			{Path: "b", Chunks: []serverDomain.Hash{b, c}},
		}},
		pruneFiles: 2,
		pruneBytes: 10,
	}
	res, err := pruneCache(repo)
	require.NoError(t, err)
	require.Equal(t, PruneResult{Objects: 2, Bytes: 10}, res)
	require.Equal(t, map[serverDomain.Hash]struct{}{a: {}, b: {}, c: {}}, repo.keep)
}

func TestPruneCache_SkippedWhilePending(t *testing.T) {
	for name, repo := range map[string]*stubCacheRepo{
		"merge":  {merge: &domain.MergeState{SourceBranch: "f"}},
		"revert": {revert: &domain.RevertState{}},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := pruneCache(repo)
			require.NoError(t, err)
			require.True(t, res.Skipped)
			require.False(t, repo.pruned)
		})
	}
}

func TestPruneCache_Errors(t *testing.T) {
	boom := errors.New("boom")
	for name, repo := range map[string]*stubCacheRepo{
		"merge":    {mergeErr: boom},
		"revert":   {revertErr: boom},
		"snapshot": {snapshotErr: boom},
		"prune":    {pruneErr: boom},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pruneCache(repo)
			require.ErrorIs(t, err, boom)
		})
	}
}

func TestPruneCache_NilSnapshot(t *testing.T) {
	repo := &stubCacheRepo{}
	_, err := pruneCache(repo)
	require.NoError(t, err)
	require.True(t, repo.pruned)
	require.Empty(t, repo.keep)
}

func TestGC_Run(t *testing.T) {
	repo := &stubCacheRepo{snapshot: &domain.Snapshot{}, pruneFiles: 1, pruneBytes: 4}
	res, err := NewGC(repo).Run("root")
	require.NoError(t, err)
	require.Equal(t, PruneResult{Objects: 1, Bytes: 4}, res)

	_, err = NewGC(&stubCacheRepo{initErr: errors.New("init")}).Run("root")
	require.EqualError(t, err, "init")
}

func TestPruneAfterSuccess(t *testing.T) {
	failed := &stubCacheRepo{}
	pruneAfterSuccess(failed, errors.New("op failed"))
	require.False(t, failed.pruned)

	pruneAfterSuccess(&stubLocalRepo{}, nil)

	broken := &stubCacheRepo{pruneErr: errors.New("disk")}
	pruneAfterSuccess(broken, nil)
	require.True(t, broken.pruned)
}

func TestUpdate_Run_PrunesReplacedObjects(t *testing.T) {
	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))

	manifest := func(content string) (*serverDomain.TreeNode, []serverDomain.Chunk, map[serverDomain.Hash][]byte) {
		fileHash, chunks, stored, encoding := testEncodedFile(t, "a.txt", content)
		return &serverDomain.TreeNode{
			Name: "root",
			Hash: serverDomain.Hash{byte(len(content))},
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      2,
				SizeBytes: int64(len(content)),
				Encoding:  encoding,
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		}, chunks, stored
	}

	oldTree, oldChunks, oldStored := manifest("first version")
	client := &stubUpdateClient{manifest: oldTree, downloadData: oldStored}
	require.NoError(t, newTestUpdate(t, lr, client).Run(context.Background(), root))
	_, err := lr.LoadChunk(oldChunks[0].Hash)
	require.NoError(t, err)

	newTree, newChunks, newStored := manifest("second version, longer")
	client = &stubUpdateClient{manifest: newTree, downloadData: newStored}
	require.NoError(t, newTestUpdate(t, lr, client).Run(context.Background(), root))

	_, err = lr.LoadChunk(oldChunks[0].Hash)
	require.Error(t, err, "the replaced version must be pruned")
	_, err = lr.LoadChunk(newChunks[0].Hash)
	require.NoError(t, err)
	require.Equal(t, "second version, longer", string(readRepoFile(t, root, "a.txt")))
}

func TestUpdate_Run_KeepsObjectsDuringPendingMerge(t *testing.T) {
	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	orphan := []byte("still needed by the merge")
	orphanHash := serverDomain.Hash{9}
	require.NoError(t, lr.StoreChunk(orphanHash, orphan))
	require.NoError(t, lr.SaveMergeState(&domain.MergeState{SourceBranch: "feature"}))

	client := &stubUpdateClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	require.NoError(t, newTestUpdate(t, lr, client).Run(context.Background(), root))

	_, err := lr.LoadChunk(orphanHash)
	require.NoError(t, err)
}
