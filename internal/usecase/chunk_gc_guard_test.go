package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// recordingGuard records what it protected and when, relative to the store.
type recordingGuard struct {
	store     *stubChunkStore
	projects  []snow.ID
	protected [][]domain.Hash
	putsSeen  []int
	err       error
}

func (g *recordingGuard) Protect(_ context.Context, projectID snow.ID, hashes []domain.Hash) error {
	g.projects = append(g.projects, projectID)
	g.protected = append(g.protected, append([]domain.Hash(nil), hashes...))
	if g.store != nil {
		g.putsSeen = append(g.putsSeen, g.store.puts)
	}
	return g.err
}

func TestChunk_GCGuardProtectsServerUploadsBeforeTheCheck(t *testing.T) {
	ctx := context.Background()
	data := []byte("merge output")
	hash := chunker.Sum(data)
	store := newStubChunkStore()
	guard := &recordingGuard{store: store}
	uc := NewChunk(newFakeChunkRepo(), store, ChunkTransferConfig{}).WithGCGuard(guard)

	err := uc.StoreForProject(ctx, snow.ID(7), hash, data)
	require.NoError(t, err)
	require.Equal(t, [][]domain.Hash{{hash}}, guard.protected)
	require.Equal(t, []snow.ID{7}, guard.projects, "marks are cleared in the writing project only")
	require.Equal(t, []int{0}, guard.putsSeen, "protected before the content was checked or written")
}

func TestChunk_GCGuardErrorStopsServerUpload(t *testing.T) {
	data := []byte("merge output")
	store := newStubChunkStore()
	guard := &recordingGuard{store: store, err: errors.New("db down")}
	uc := NewChunk(newFakeChunkRepo(), store, ChunkTransferConfig{}).WithGCGuard(guard)

	err := uc.StoreForProject(context.Background(), snow.ID(1), chunker.Sum(data), data)
	require.Error(t, err)
	require.Zero(t, store.puts)
}

func TestChunk_GCGuardProtectsTheWholeConfirmBatch(t *testing.T) {
	ctx := context.Background()
	uc, _, store := newChunkFixture(t)
	guard := &recordingGuard{}
	uc.WithGCGuard(guard)

	present := []byte("present")
	presentHash := chunker.Sum(present)
	require.NoError(t, store.Put(ctx, testProject.ID, presentHash, present))
	absentHash := chunker.Sum([]byte("absent"))

	_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{presentHash, absentHash}, nil)
	require.NoError(t, err)
	require.Equal(t, [][]domain.Hash{{presentHash, absentHash}}, guard.protected)
	require.Equal(t, []snow.ID{testProject.ID}, guard.projects)
}

func TestChunk_GCGuardErrorStopsConfirm(t *testing.T) {
	uc, _, _ := newChunkFixture(t)
	errGuard := domain.NewErrorDatabase("db down")
	uc.WithGCGuard(&recordingGuard{err: errGuard})

	_, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{chunker.Sum([]byte("x"))}, nil)
	require.ErrorIs(t, err, errGuard)
}

func TestChunk_GCGuardProtectsRecordedChunksAtPresign(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newChunkFixture(t)
	guard := &recordingGuard{}
	uc.WithGCGuard(guard)

	data := []byte("recorded")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, testProject.ID, hash, data))
	repo.record(testProject.ID, hash, int64(len(data)))
	fresh := chunker.Sum([]byte("fresh"))

	got, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{
		{Hash: hash, SizeBytes: int64(len(data))}, {Hash: fresh, SizeBytes: 5},
	}, 10, "")
	require.NoError(t, err)
	require.True(t, got.URLs[0].AlreadyStored)
	require.Equal(t, [][]domain.Hash{{hash}}, guard.protected, "only recorded chunks are protected")
}

func TestChunk_GCGuardErrorStopsPresign(t *testing.T) {
	ctx := context.Background()
	uc, repo, _ := newChunkFixture(t)
	errGuard := domain.NewErrorDatabase("db down")
	uc.WithGCGuard(&recordingGuard{err: errGuard})
	hash := chunker.Sum([]byte("x"))
	repo.record(testProject.ID, hash, 1)

	_, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: 1}}, 10, "")
	require.ErrorIs(t, err, errGuard)
}
