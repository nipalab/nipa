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

func TestChunk_Presign_NeverOffersToWriteStoredContent(t *testing.T) {
	ctx := context.Background()
	store := &stubDirectChunkStore{
		stubChunkStore: newStubChunkStore(),
		uploadTarget:   storage.UploadTarget{URL: "https://s3.example.com/put", Headers: map[string]string{"If-None-Match": "*"}},
	}
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
	require.Empty(t, urls[0].URL)

	require.False(t, urls[1].AlreadyStored, "content held for another project is not reported as stored")
	require.Empty(t, urls[1].URL, "stored content gets no upload target, it is claimed with a proof")
	require.NoError(t, chunkproof.VerifyChallenge(linkTestKey, testProject.ID, foreign, urls[1].ProofChallenge, uc.now()))

	require.Equal(t, "https://s3.example.com/put", urls[2].URL, "only absent content gets a create-only target")
	require.Equal(t, map[string]string{"If-None-Match": "*"}, urls[2].Headers)
	require.NoError(t, chunkproof.VerifyChallenge(linkTestKey, testProject.ID, fresh, urls[2].ProofChallenge, uc.now()))
}

func TestChunk_Presign_LinkedButMissingObjectIsUploadedAgain(t *testing.T) {
	data := []byte("lost object")
	hash := chunker.Sum(data)
	store := &stubDirectChunkStore{
		stubChunkStore: newStubChunkStore(),
		uploadTarget:   storage.UploadTarget{URL: "https://s3.example.com/put"},
	}
	uc, repo := newLinkFixture(t, store)
	repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{hash: true}, nil)

	urls, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")
	require.NoError(t, err)
	require.False(t, urls[0].AlreadyStored)
	require.NotEmpty(t, urls[0].URL)
}

func TestChunk_Presign_ExistsCheckError(t *testing.T) {
	store := &stubChunkStore{data: map[domain.Hash][]byte{}, existsErr: errors.New("boom")}
	uc, repo := newLinkFixture(t, store)
	repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{}, nil)

	_, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject,
		[]ChunkRef{{Hash: chunker.Sum([]byte("x")), SizeBytes: 1}}, 10, "")
	require.Error(t, err)
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

func TestChunk_ConfirmUploads_DirectStoreVerifiesWhatClientsWrote(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	setup := func(t *testing.T) (*Chunk, *MockchunkRepository, *stubDirectChunkStore) {
		store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{}, nil)
		return uc, repo, store
	}

	t.Run("a direct upload with a valid proof is linked", func(t *testing.T) {
		uc, repo, store := setup(t)
		store.data[hash] = data
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
		repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{hash}).Return(nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.NoError(t, err)
		require.Empty(t, missing)
		require.Empty(t, store.deleted)
	})

	t.Run("garbage on the key is deleted so the real content can be uploaded", func(t *testing.T) {
		uc, _, store := setup(t)
		store.data[hash] = []byte("same size, other bytes")

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, []byte("same size, other bytes"))})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
		require.Equal(t, []domain.Hash{hash}, store.deleted)
		require.Empty(t, store.data)
	})

	t.Run("a failed delete of garbage surfaces", func(t *testing.T) {
		uc, _, store := setup(t)
		store.data[hash] = []byte("garbage")
		store.deleteErr = errors.New("delete failed")
		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, []byte("garbage"))})
		require.Error(t, err)
	})

	t.Run("a wrong proof over valid content links nothing and deletes nothing", func(t *testing.T) {
		uc, _, store := setup(t)
		store.data[hash] = data
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, []byte("guess"))})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
		require.Empty(t, store.deleted)
		require.Equal(t, data, store.data[hash])
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

func TestChunk_ConfirmUploads_LinkedChunksAreVerifiedWhenProven(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	setup := func(t *testing.T, stored []byte) (*Chunk, *MockchunkRepository, *stubDirectChunkStore) {
		store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}
		if stored != nil {
			store.data[hash] = stored
		}
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{hash: true}, nil)
		return uc, repo, store
	}

	t.Run("garbage that replaced a lost object is deleted, not confirmed", func(t *testing.T) {
		uc, _, store := setup(t, []byte("same size, other bytes"))

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing, "the owner's re-upload got a 412 over garbage, so it must be retried")
		require.Equal(t, []domain.Hash{hash}, store.deleted)
	})

	t.Run("a proven linked chunk is confirmed without being linked again", func(t *testing.T) {
		uc, repo, store := setup(t, data)
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)

		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.NoError(t, err)
		require.Empty(t, missing)
		require.Empty(t, store.deleted)
	})

	t.Run("a linked chunk whose object is gone is missing", func(t *testing.T) {
		uc, _, _ := setup(t, nil)
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.NoError(t, err)
		require.Equal(t, []domain.Hash{hash}, missing)
	})

	t.Run("a read error while verifying surfaces", func(t *testing.T) {
		store := &stubChunkStore{data: map[domain.Hash][]byte{hash: data}, getErr: errors.New("disk down")}
		uc, repo := newLinkFixture(t, store)
		repo.EXPECT().LinkedProjectChunks(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]bool{hash: true}, nil)
		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
		require.Error(t, err)
	})
}
