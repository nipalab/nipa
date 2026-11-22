package s3

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// memChunkRepo is the smallest chunk repository confirm needs: nothing is
// linked yet and every write succeeds.
type memChunkRepo struct {
	rows  []usecase.ChunkRef
	links []domain.Hash
}

func (r *memChunkRepo) InsertChunkIfNotExists(context.Context, domain.Hash, int64) error { return nil }
func (r *memChunkRepo) InsertChunksIfNotExist(_ context.Context, rows []usecase.ChunkRef) error {
	r.rows = append(r.rows, rows...)
	return nil
}
func (r *memChunkRepo) HasChunk(context.Context, domain.Hash) (bool, error) { return false, nil }
func (r *memChunkRepo) KnownChunks(context.Context, []domain.Hash) (map[domain.Hash]struct{}, error) {
	return map[domain.Hash]struct{}{}, nil
}
func (r *memChunkRepo) LinkProjectChunk(context.Context, snow.ID, domain.Hash) error { return nil }
func (r *memChunkRepo) LinkProjectChunks(_ context.Context, _ snow.ID, hashes []domain.Hash) error {
	r.links = append(r.links, hashes...)
	return nil
}
func (r *memChunkRepo) LinkedProjectChunkSizes(context.Context, snow.ID, []domain.Hash) (map[domain.Hash]int64, error) {
	return map[domain.Hash]int64{}, nil
}

// TestStore_ConfirmReadBackDecisions runs confirm's read-back against the S3
// store: it streams one GET per verified chunk, deletes objects that do not
// match their key (also an oversized one), and reads nothing for a rejected
// challenge.
func TestStore_ConfirmReadBackDecisions(t *testing.T) {
	const key = "confirm-key"
	project := &domain.Project{ID: 7, Slug: "game"}
	org := &domain.Organization{ID: 1, Slug: "acme"}
	now := time.Now()
	proofFor := func(t *testing.T, hash domain.Hash, data []byte, projectID snow.ID) usecase.ChunkProof {
		challenge, err := chunkproof.NewChallenge(key, projectID, hash, now.Add(time.Hour))
		require.NoError(t, err)
		return usecase.ChunkProof{Hash: hash, Challenge: challenge, Proof: chunkproof.Proof(challenge, data)}
	}

	data := []byte("content uploaded directly")
	hash := chunker.Sum(data)
	oversized := bytes.Repeat([]byte{1}, int(chunker.MaxChunkSize())+1)
	oversizedHash := chunker.Sum(oversized)

	cases := []struct {
		name    string
		hash    domain.Hash
		stored  []byte
		proof   func(t *testing.T) usecase.ChunkProof
		linked  bool
		gets    int64
		deleted bool
	}{
		{name: "valid", hash: hash, stored: data, proof: func(t *testing.T) usecase.ChunkProof { return proofFor(t, hash, data, project.ID) }, linked: true, gets: 1},
		{name: "garbage", hash: hash, stored: []byte("garbage"), proof: func(t *testing.T) usecase.ChunkProof { return proofFor(t, hash, data, project.ID) }, gets: 1, deleted: true},
		{name: "oversized", hash: oversizedHash, stored: oversized, proof: func(t *testing.T) usecase.ChunkProof { return proofFor(t, oversizedHash, oversized, project.ID) }, gets: 1, deleted: true},
		{name: "not found", hash: hash, proof: func(t *testing.T) usecase.ChunkProof { return proofFor(t, hash, data, project.ID) }, gets: 1},
		{name: "other project's challenge", hash: hash, stored: data, proof: func(t *testing.T) usecase.ChunkProof { return proofFor(t, hash, data, 8) }},
		{name: "wrong proof", hash: hash, stored: data, proof: func(t *testing.T) usecase.ChunkProof {
			p := proofFor(t, hash, data, project.ID)
			p.Proof = chunkproof.Proof(p.Challenge, []byte("guess"))
			return p
		}, gets: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, fake := newTestStore(t)
			ctx := context.Background()
			if tc.stored != nil {
				fake.mu.Lock()
				fake.buckets["test-bucket"][store.key(tc.hash)] = tc.stored
				fake.mu.Unlock()
			}
			repo := &memChunkRepo{}
			uc := usecase.NewChunk(repo, store, usecase.ChunkTransferConfig{SigningKey: key, PresignTTL: time.Hour})
			fake.gets.Store(0)

			missing, err := uc.ConfirmUploads(ctx, org, project, []domain.Hash{tc.hash}, []usecase.ChunkProof{tc.proof(t)})
			require.NoError(t, err)
			require.Equal(t, tc.gets, fake.gets.Load())
			require.Zero(t, fake.heads.Load())
			if tc.linked {
				require.Empty(t, missing)
				require.Equal(t, []usecase.ChunkRef{{Hash: tc.hash, SizeBytes: int64(len(tc.stored))}}, repo.rows)
				require.Equal(t, []domain.Hash{tc.hash}, repo.links)
			} else {
				require.Equal(t, []domain.Hash{tc.hash}, missing)
				require.Empty(t, repo.links)
			}
			fake.mu.Lock()
			_, stillThere := fake.buckets["test-bucket"][store.key(tc.hash)]
			fake.mu.Unlock()
			require.Equal(t, tc.stored != nil && !tc.deleted, stillThere)
		})
	}
}
