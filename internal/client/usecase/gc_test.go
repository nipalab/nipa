package usecase

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	pins        []string
	pinsErr     error
}

func (s *stubCacheRepo) ListPins() ([]string, error) { return s.pins, s.pinsErr }

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

func TestPruneCache_KeepsTextAndPinnedRawChunks(t *testing.T) {
	a, b, c, d, e := serverDomain.Hash{1}, serverDomain.Hash{2}, serverDomain.Hash{3}, serverDomain.Hash{4}, serverDomain.Hash{5}
	repo := &stubCacheRepo{
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{
			{Path: "notes.txt", Encoding: "zstd", Chunks: []serverDomain.Hash{a}},
			{Path: "art/hero.png", Encoding: "raw", Chunks: []serverDomain.Hash{b, c}},
			{Path: "levels/one.bin", Encoding: "raw", Chunks: []serverDomain.Hash{d}},
			{Path: "artwork/x.png", Encoding: "raw", Chunks: []serverDomain.Hash{e}},
			{Path: "legacy.bin", Chunks: []serverDomain.Hash{e}},
		}},
		pins:       []string{"art", "levels/one.bin"},
		pruneFiles: 2,
		pruneBytes: 10,
	}
	res, err := pruneCache(repo)
	require.NoError(t, err)
	require.Equal(t, PruneResult{Objects: 2, Bytes: 10}, res)
	require.Equal(t, map[serverDomain.Hash]struct{}{a: {}, b: {}, c: {}, d: {}}, repo.keep)
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
			require.Equal(t, "a merge or revert is in progress", res.SkipReason)
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
		"pins":     {pinsErr: boom},
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

type stubLockingCacheRepo struct {
	stubCacheRepo
	busy       bool
	lockErr    error
	released   int
	sharedTook int
}

func (s *stubLockingCacheRepo) LockShared() (func(), error) {
	s.sharedTook++
	return func() { s.released++ }, s.lockErr
}

func (s *stubLockingCacheRepo) TryLockExclusive() (func(), bool, error) {
	if s.lockErr != nil || s.busy {
		return nil, false, s.lockErr
	}
	return func() { s.released++ }, true, nil
}

func TestPruneCache_ExclusiveLock(t *testing.T) {
	busy := &stubLockingCacheRepo{busy: true}
	res, err := pruneCache(busy)
	require.NoError(t, err)
	require.Equal(t, PruneResult{Skipped: true, SkipReason: "repository busy"}, res)
	require.False(t, busy.pruned)

	failing := &stubLockingCacheRepo{lockErr: errors.New("lock")}
	_, err = pruneCache(failing)
	require.EqualError(t, err, "lock")

	free := &stubLockingCacheRepo{}
	_, err = pruneCache(free)
	require.NoError(t, err)
	require.True(t, free.pruned)
	require.Equal(t, 1, free.released)
}

func TestLockShared(t *testing.T) {
	unlock, err := lockShared(struct{}{})
	require.NoError(t, err)
	unlock()

	repo := &stubLockingCacheRepo{}
	unlock, err = lockShared(repo)
	require.NoError(t, err)
	unlock()
	require.Equal(t, 1, repo.sharedTook)
	require.Equal(t, 1, repo.released)
}

type pausingUpdateClient struct {
	*stubUpdateClient
	stored  chan struct{}
	proceed chan struct{}
}

func (c *pausingUpdateClient) DownloadChunks(ctx context.Context, scope domain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	if err := c.stubUpdateClient.DownloadChunks(ctx, scope, hashes, onChunk); err != nil {
		return err
	}
	close(c.stored)
	<-c.proceed
	return nil
}

func TestGC_SkippedWhileUpdateKeepsDownloadedChunks(t *testing.T) {
	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: "http://example.com/org/project", Branch: "main"}))

	content := "downloaded while gc runs"
	fileHash, chunks, stored, encoding := testEncodedFile(t, "a.txt", content)
	client := &pausingUpdateClient{
		stubUpdateClient: &stubUpdateClient{
			manifest: &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{{
				Name: "a.txt", Mode: 2, SizeBytes: int64(len(content)), Encoding: encoding, Hash: fileHash, Chunks: chunks,
			}}},
			downloadData: stored,
		},
		stored:  make(chan struct{}),
		proceed: make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() { done <- newTestUpdate(t, lr, client).Run(context.Background(), root) }()
	<-client.stored

	gcRepo := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = gcRepo.Close() })
	res, err := NewGC(gcRepo).Run(root)
	require.NoError(t, err)
	require.Equal(t, PruneResult{Skipped: true, SkipReason: "repository busy"}, res)

	close(client.proceed)
	require.NoError(t, <-done)
	require.Equal(t, content, string(readRepoFile(t, root, "a.txt")))
	_, err = gcRepo.LoadChunk(chunks[0].Hash)
	require.NoError(t, err)

	res, err = NewGC(gcRepo).Run(root)
	require.NoError(t, err)
	require.False(t, res.Skipped)
}

const lockHelperEnv = "NIPA_TEST_LOCK_HELPER_ROOT"

func TestHelperHoldSharedLock(t *testing.T) {
	root := os.Getenv(lockHelperEnv)
	if root == "" {
		t.Skip("helper process only")
	}
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	unlock, err := lr.LockShared()
	require.NoError(t, err)
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
	unlock()
	_ = lr.Close()
}

func TestGC_SkippedWhileAnotherProcessHoldsLock(t *testing.T) {
	root := t.TempDir()
	lr := localrepo.NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.Init(root))
	orphan := serverDomain.Hash{7}
	require.NoError(t, lr.StoreChunk(orphan, []byte("orphan")))

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldSharedLock$")
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+root)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	scanner := bufio.NewScanner(stdout)
	locked := false
	for scanner.Scan() {
		if scanner.Text() == "locked" {
			locked = true
			break
		}
	}
	require.True(t, locked, "helper never took the lock")

	res, err := NewGC(lr).Run(root)
	require.NoError(t, err)
	require.Equal(t, PruneResult{Skipped: true, SkipReason: "repository busy"}, res)
	_, err = lr.LoadChunk(orphan)
	require.NoError(t, err)

	require.NoError(t, stdin.Close())
	_, _ = io.Copy(io.Discard, stdout)
	require.NoError(t, cmd.Wait())

	res, err = NewGC(lr).Run(root)
	require.NoError(t, err)
	require.Equal(t, PruneResult{Objects: 1, Bytes: 6}, res)
}
