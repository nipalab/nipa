package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// recordingGuard records what it protected and when, relative to the store.
type recordingGuard struct {
	store     *stubChunkStore
	protected [][]domain.Hash
	putsSeen  []int
	err       error
}

func (g *recordingGuard) Protect(_ context.Context, hashes []domain.Hash) error {
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
	repo := NewMockchunkRepository(gomock.NewController(t))
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
	guard := &recordingGuard{store: store}
	uc := NewChunk(repo, store, ChunkTransferConfig{}).WithGCGuard(guard)

	_, err := uc.StoreForProject(ctx, snow.ID(1), hash, data)
	require.NoError(t, err)
	require.Equal(t, [][]domain.Hash{{hash}}, guard.protected)
	require.Equal(t, []int{0}, guard.putsSeen, "protected before the content was checked or written")
}

func TestChunk_GCGuardErrorStopsServerUpload(t *testing.T) {
	data := []byte("merge output")
	store := newStubChunkStore()
	guard := &recordingGuard{store: store, err: errors.New("db down")}
	uc := NewChunk(NewMockchunkRepository(gomock.NewController(t)), store, ChunkTransferConfig{}).WithGCGuard(guard)

	_, err := uc.StoreForProject(context.Background(), snow.ID(1), chunker.Sum(data), data)
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
	require.NoError(t, store.Put(ctx, presentHash, present))
	absentHash := chunker.Sum([]byte("absent"))

	_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{presentHash, absentHash}, nil)
	require.NoError(t, err)
	require.Equal(t, [][]domain.Hash{{presentHash, absentHash}}, guard.protected)
}

func TestChunk_GCGuardErrorStopsConfirm(t *testing.T) {
	uc, _, _ := newChunkFixture(t)
	errGuard := domain.NewErrorDatabase("db down")
	uc.WithGCGuard(&recordingGuard{err: errGuard})

	_, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{chunker.Sum([]byte("x"))}, nil)
	require.ErrorIs(t, err, errGuard)
}
