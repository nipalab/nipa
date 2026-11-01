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
	"github.com/nipalab/nipa/internal/storage"
)

func newLinkFixture(t *testing.T, store storage.ChunkStore) (*Chunk, *MockchunkRepository) {
	t.Helper()
	repo := NewMockchunkRepository(gomock.NewController(t))
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: "key", PresignTTL: 3600, MaxPageSize: 10}).
		WithProjectResolver(stubChunkProjects{})
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

func TestChunk_StoreUploaded_Linking(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	t.Run("no resolver refuses", func(t *testing.T) {
		uc := NewChunk(NewMockchunkRepository(gomock.NewController(t)), newStubChunkStore(), ChunkTransferConfig{})
		err := uc.StoreUploaded(ctx, "acme", "game", hash, data)
		require.Error(t, err)
	})

	t.Run("resolver error", func(t *testing.T) {
		repo := NewMockchunkRepository(gomock.NewController(t))
		uc := NewChunk(repo, newStubChunkStore(), ChunkTransferConfig{}).
			WithProjectResolver(stubChunkProjects{err: errors.New("gone")})
		require.EqualError(t, uc.StoreUploaded(ctx, "acme", "game", hash, data), "gone")
	})

	t.Run("link error after a store", func(t *testing.T) {
		uc, repo := newLinkFixture(t, newStubChunkStore())
		repo.EXPECT().LinkProjectChunk(gomock.Any(), testProject.ID, hash).Return(errors.New("boom"))
		require.EqualError(t, uc.StoreUploaded(ctx, "acme", "game", hash, data), "boom")
	})

	t.Run("dedup still links the uploader", func(t *testing.T) {
		store := newStubChunkStore()
		store.data[hash] = data
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkProjectChunk(gomock.Any(), testProject.ID, hash).Return(nil)
		require.NoError(t, uc.StoreUploaded(ctx, "acme", "game", hash, data))
		require.Zero(t, store.puts)
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

	t.Run("linked content is recorded", func(t *testing.T) {
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
		repo.EXPECT().LinkProjectChunk(gomock.Any(), testProject.ID, hash).Return(nil)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)

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
		repo.EXPECT().LinkProjectChunk(gomock.Any(), testProject.ID, hash).Return(errors.New("boom"))

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
