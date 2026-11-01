package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

const linkTestKey = "key"

func newLinkFixture(t *testing.T, store storage.ChunkStore) (*Chunk, *MockchunkRepository) {
	t.Helper()
	repo := NewMockchunkRepository(gomock.NewController(t))
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour, MaxPageSize: 10})
	return uc, repo
}

func proofFor(t *testing.T, uc *Chunk, hash domain.Hash, data []byte) ChunkProof {
	t.Helper()
	challenge, err := chunkproof.NewChallenge(linkTestKey, testProject.ID, hash, uc.now().Add(time.Hour))
	require.NoError(t, err)
	return ChunkProof{Hash: hash, Challenge: challenge, Proof: chunkproof.Proof(challenge, data)}
}

func TestChunk_Presign_ChallengesEveryUnlinkedChunkAlike(t *testing.T) {
	ctx := context.Background()
	store := newStubChunkStore()
	linkedData, foreignData, freshData := []byte("linked"), []byte("foreign"), []byte("fresh")
	linked, foreign, fresh := chunker.Sum(linkedData), chunker.Sum(foreignData), chunker.Sum(freshData)
	store.data[linked] = linkedData
	store.data[foreign] = foreignData
	uc, repo := newLinkFixture(t, store)
	repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{linked, foreign, fresh}).
		Return(map[domain.Hash]bool{linked: true}, nil)

	urls, _, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{
		{Hash: linked, SizeBytes: int64(len(linkedData))},
		{Hash: foreign, SizeBytes: int64(len(foreignData))},
		{Hash: fresh, SizeBytes: int64(len(freshData))},
	}, 10, "")

	require.NoError(t, err)
	require.Len(t, urls, 3)
	require.True(t, urls[0].AlreadyStored)
	require.Empty(t, urls[0].ProofChallenge)
	for _, entry := range urls[1:] {
		require.False(t, entry.AlreadyStored, "content held for another project is not reported")
		require.NotEmpty(t, entry.URL)
		require.NoError(t, chunkproof.VerifyChallenge(linkTestKey, testProject.ID, entry.Hash, entry.ProofChallenge, uc.now()))
	}
}

func TestChunk_Presign_LinkLookupError(t *testing.T) {
	uc, repo := newLinkFixture(t, newStubChunkStore())
	repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(nil, errors.New("boom"))

	_, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject,
		[]ChunkRef{{Hash: chunker.Sum([]byte("x")), SizeBytes: 1}}, 10, "")
	require.EqualError(t, err, "boom")
}

func TestChunk_StoreUploaded_NeverTouchesTheDatabase(t *testing.T) {
	data := []byte("payload")
	hash := chunker.Sum(data)
	uc, _ := newLinkFixture(t, newStubChunkStore())

	require.NoError(t, uc.StoreUploaded(context.Background(), hash, data))
}

func TestChunk_ConfirmUploads_LinksOnlyWithAValidProof(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	setup := func(t *testing.T) (*Chunk, *MockchunkRepository) {
		store := newStubChunkStore()
		store.data[hash] = data
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).
			Return(map[domain.Hash]bool{}, nil)
		return uc, repo
	}

	t.Run("a valid proof links in one batch", func(t *testing.T) {
		uc, repo := setup(t)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{hash}).Return(nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.NoError(t, err)
		require.Empty(t, missing)
	})

	t.Run("knowing the hash is not enough", func(t *testing.T) {
		uc, _ := setup(t)
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("a proof computed without the bytes fails", func(t *testing.T) {
		uc, _ := setup(t)
		bad := proofFor(t, uc, hash, []byte("guessed content"))
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{bad})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("a challenge issued for another project fails", func(t *testing.T) {
		uc, _ := setup(t)
		challenge, err := chunkproof.NewChallenge(linkTestKey, snow.ID(999), hash, uc.now().Add(time.Hour))
		require.NoError(t, err)
		foreign := ChunkProof{Hash: hash, Challenge: challenge, Proof: chunkproof.Proof(challenge, data)}
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{foreign})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("an expired challenge fails", func(t *testing.T) {
		uc, _ := setup(t)
		challenge, err := chunkproof.NewChallenge(linkTestKey, testProject.ID, hash, uc.now().Add(-time.Hour))
		require.NoError(t, err)
		expired := ChunkProof{Hash: hash, Challenge: challenge, Proof: chunkproof.Proof(challenge, data)}
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{expired})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("an empty proof fails", func(t *testing.T) {
		uc, _ := setup(t)
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{{Hash: hash, Challenge: "x"}})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("a missing object cannot be proven", func(t *testing.T) {
		uc, repo := newLinkFixture(t, newStubChunkStore())
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{}, nil)
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("an object that does not match its key is not linked", func(t *testing.T) {
		store := newStubChunkStore()
		store.data[hash] = []byte("corrupted")
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{}, nil)
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, []byte("corrupted"))})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("read errors surface", func(t *testing.T) {
		store := &stubChunkStore{data: map[domain.Hash][]byte{hash: data}, getErr: errors.New("disk down")}
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{}, nil)
		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.Error(t, err)
	})

	t.Run("batch link error", func(t *testing.T) {
		uc, repo := setup(t)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(errors.New("boom"))
		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.EqualError(t, err, "boom")
	})

	t.Run("lookup error", func(t *testing.T) {
		uc, repo := newLinkFixture(t, newStubChunkStore())
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(nil, errors.New("boom"))
		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
		require.EqualError(t, err, "boom")
	})

	t.Run("already linked content needs no proof", func(t *testing.T) {
		store := newStubChunkStore()
		store.data[hash] = data
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).
			Return(map[domain.Hash]bool{hash: true}, nil)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
		require.NoError(t, err)
		require.Empty(t, missing)
	})
}

func TestChunk_ConfirmUploads_DirectStoreDropsObjectsThatDoNotMatchTheirKey(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	t.Run("corrupted object is deleted", func(t *testing.T) {
		store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}
		store.data[hash] = []byte("garbage")
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{}, nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, []byte("garbage"))})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
		require.Equal(t, []domain.Hash{hash}, store.deleted)
	})

	t.Run("a valid proof links the verified object", func(t *testing.T) {
		store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}
		store.data[hash] = data
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{}, nil)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{hash}).Return(nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.NoError(t, err)
		require.Empty(t, missing)
	})

	t.Run("an oversized object is deleted unread", func(t *testing.T) {
		store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}
		store.data[hash] = make([]byte, chunker.MaxChunkSize()+1)
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{}, nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
		require.Equal(t, []domain.Hash{hash}, store.deleted)
		require.Zero(t, store.gets)
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
