package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

func newLinkFixture(t *testing.T, store storage.ChunkStore) (*Chunk, *MockchunkRepository) {
	t.Helper()
	repo := NewMockchunkRepository(gomock.NewController(t))
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: "key", PresignTTL: time.Hour, MaxPageSize: 10})
	return uc, repo
}

func TestChunk_Presign_OnlyLinkedChunksAreAlreadyStored(t *testing.T) {
	ctx := context.Background()
	store := newStubChunkStore()
	linkedData, foreignData := []byte("linked"), []byte("foreign")
	linked, foreign := chunker.Sum(linkedData), chunker.Sum(foreignData)
	store.data[linked] = linkedData
	store.data[foreign] = foreignData
	uc, repo := newLinkFixture(t, store)
	repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{linked, foreign}).
		Return(map[domain.Hash]bool{linked: true}, nil)

	urls, _, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{
		{Hash: linked, SizeBytes: int64(len(linkedData))},
		{Hash: foreign, SizeBytes: int64(len(foreignData))},
	}, 10, "")

	require.NoError(t, err)
	require.Len(t, urls, 2)
	require.True(t, urls[0].AlreadyStored)
	require.False(t, urls[1].AlreadyStored, "content held for another project must be uploaded again")
	require.NotEmpty(t, urls[1].URL)
}

func TestChunk_Presign_LinkLookupError(t *testing.T) {
	uc, repo := newLinkFixture(t, newStubChunkStore())
	repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(nil, errors.New("boom"))

	_, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject,
		[]ChunkRef{{Hash: chunker.Sum([]byte("x")), SizeBytes: 1}}, 10, "")
	require.EqualError(t, err, "boom")
}

func TestChunk_StoreUploaded_WritesNothingToTheDatabase(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	t.Run("a stored chunk leaves a proof", func(t *testing.T) {
		uc, _ := newLinkFixture(t, newStubChunkStore())
		require.NoError(t, uc.StoreUploaded(ctx, "acme", "game", hash, data))
		require.True(t, uc.hasUploadProof("acme", "game", hash))
		require.False(t, uc.hasUploadProof("acme", "other", hash), "proofs are per project")
	})

	t.Run("a dedup hit still leaves a proof", func(t *testing.T) {
		store := newStubChunkStore()
		store.data[hash] = data
		uc, _ := newLinkFixture(t, store)
		require.NoError(t, uc.StoreUploaded(ctx, "acme", "game", hash, data))
		require.Zero(t, store.puts)
		require.True(t, uc.hasUploadProof("acme", "game", hash))
	})

	t.Run("proofs expire", func(t *testing.T) {
		uc, _ := newLinkFixture(t, newStubChunkStore())
		now := time.Unix(1700000000, 0)
		uc.now = func() time.Time { return now }
		require.NoError(t, uc.StoreUploaded(ctx, "acme", "game", hash, data))
		now = now.Add(2 * time.Hour)
		require.False(t, uc.hasUploadProof("acme", "game", hash))
	})

	t.Run("expired proofs are pruned on later uploads", func(t *testing.T) {
		uc, _ := newLinkFixture(t, newStubChunkStore())
		now := time.Unix(1700000000, 0)
		uc.now = func() time.Time { return now }
		require.NoError(t, uc.StoreUploaded(ctx, "acme", "game", hash, data))
		now = now.Add(3 * time.Hour)
		other := []byte("another")
		require.NoError(t, uc.StoreUploaded(ctx, "acme", "game", chunker.Sum(other), other))
		require.Len(t, uc.uploaded, 1)
	})

	t.Run("no preset ttl falls back to an hour", func(t *testing.T) {
		uc := NewChunk(NewMockchunkRepository(gomock.NewController(t)), newStubChunkStore(), ChunkTransferConfig{})
		require.Equal(t, time.Hour, uc.uploadProofTTL())
	})
}

func TestChunk_ConfirmUploads_RequiresLink(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	t.Run("unlinked local content is reported missing", func(t *testing.T) {
		store := newStubChunkStore()
		store.data[hash] = data
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{hash}).
			Return(map[domain.Hash]bool{}, nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("content uploaded through this project is linked in one batch", func(t *testing.T) {
		store := newStubChunkStore()
		uc, repo := newLinkFixture(t, store)
		require.NoError(t, uc.StoreUploaded(ctx, "acme", "game", hash, data))
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).
			Return(map[domain.Hash]bool{}, nil)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{hash}).Return(nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash})
		require.NoError(t, err)
		require.Empty(t, missing)
		require.False(t, uc.hasUploadProof("acme", "game", hash), "the proof is consumed")
	})

	t.Run("batch link error", func(t *testing.T) {
		store := newStubChunkStore()
		uc, repo := newLinkFixture(t, store)
		require.NoError(t, uc.StoreUploaded(ctx, "acme", "game", hash, data))
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).
			Return(map[domain.Hash]bool{}, nil)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(errors.New("boom"))

		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash})
		require.EqualError(t, err, "boom")
	})

	t.Run("a proof from another project does not link", func(t *testing.T) {
		store := newStubChunkStore()
		uc, repo := newLinkFixture(t, store)
		require.NoError(t, uc.StoreUploaded(ctx, "acme", "elsewhere", hash, data))
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).
			Return(map[domain.Hash]bool{}, nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("already linked content is recorded", func(t *testing.T) {
		store := newStubChunkStore()
		store.data[hash] = data
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).
			Return(map[domain.Hash]bool{hash: true}, nil)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash})
		require.NoError(t, err)
		require.Empty(t, missing)
	})

	t.Run("lookup error", func(t *testing.T) {
		uc, repo := newLinkFixture(t, newStubChunkStore())
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(nil, errors.New("boom"))
		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash})
		require.EqualError(t, err, "boom")
	})

	t.Run("direct store links after verifying content", func(t *testing.T) {
		store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}
		store.data[hash] = data
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).
			Return(map[domain.Hash]bool{}, nil)
		repo.EXPECT().HasChunk(gomock.Any(), hash).Return(true, nil)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{hash}).Return(nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash})
		require.NoError(t, err)
		require.Empty(t, missing)
		require.Equal(t, 1, store.gets, "unlinked content is always read back and verified")
	})

	t.Run("direct store link error", func(t *testing.T) {
		store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}
		store.data[hash] = data
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).
			Return(map[domain.Hash]bool{}, nil)
		repo.EXPECT().HasChunk(gomock.Any(), hash).Return(true, nil)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(errors.New("boom"))

		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash})
		require.EqualError(t, err, "boom")
	})
}

func TestChunk_AttributeProjectChunks_LinksWithoutLedger(t *testing.T) {
	ctx := context.Background()
	hash := chunker.Sum([]byte("merged"))

	t.Run("links", func(t *testing.T) {
		uc, repo := newLinkFixture(t, newStubChunkStore())
		repo.EXPECT().LinkProjectChunk(gomock.Any(), snow.ID(7), hash).Return(nil)
		require.NoError(t, uc.AttributeProjectChunks(ctx, snow.ID(7), []LedgerChunk{{Hash: hash, SizeBytes: 6}}))
	})

	t.Run("link error", func(t *testing.T) {
		uc, repo := newLinkFixture(t, newStubChunkStore())
		repo.EXPECT().LinkProjectChunk(gomock.Any(), snow.ID(7), hash).Return(errors.New("boom"))
		require.EqualError(t, uc.AttributeProjectChunks(ctx, snow.ID(7), []LedgerChunk{{Hash: hash}}), "boom")
	})
}

func TestChunk_UploadForProject(t *testing.T) {
	ctx := context.Background()
	data := []byte("seeded")
	hash := chunker.Sum(data)

	t.Run("stores and links", func(t *testing.T) {
		uc, repo := newLinkFixture(t, newStubChunkStore())
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunk(gomock.Any(), snow.ID(7), hash).Return(nil)
		isNew, err := uc.UploadForProject(ctx, snow.ID(7), hash, data)
		require.NoError(t, err)
		require.True(t, isNew)
	})

	t.Run("upload error", func(t *testing.T) {
		uc, _ := newLinkFixture(t, newStubChunkStore())
		_, err := uc.UploadForProject(ctx, snow.ID(7), hash, []byte("different"))
		require.Error(t, err)
	})

	t.Run("link error", func(t *testing.T) {
		uc, repo := newLinkFixture(t, newStubChunkStore())
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunk(gomock.Any(), snow.ID(7), hash).Return(errors.New("boom"))
		_, err := uc.UploadForProject(ctx, snow.ID(7), hash, data)
		require.EqualError(t, err, "boom")
	})
}
