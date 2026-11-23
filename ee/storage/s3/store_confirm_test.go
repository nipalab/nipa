package s3

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// memChunkRepo is the smallest chunk repository confirm needs: no chunk is
// recorded yet and every write succeeds.
type memChunkRepo struct {
	rows []usecase.ChunkRow
}

func (r *memChunkRepo) InsertChunkRows(_ context.Context, _ snow.ID, rows []usecase.ChunkRow) error {
	r.rows = append(r.rows, rows...)
	return nil
}

func (r *memChunkRepo) ProjectChunkRecords(context.Context, snow.ID, []domain.Hash) (map[domain.Hash]usecase.ChunkRecord, error) {
	return map[domain.Hash]usecase.ChunkRecord{}, nil
}

func (r *memChunkRepo) RelocateChunks(context.Context, snow.ID, []usecase.ChunkRelocation) error {
	return nil
}

func (r *memChunkRepo) InsertChunkPack(context.Context, usecase.ChunkPack) error { return nil }

func (r *memChunkRepo) SetChunkPackState(context.Context, snow.ID, usecase.ChunkPackState) error {
	return nil
}

// TestStore_ConfirmReadBackDecisions runs confirm's read-back against the S3
// store: it streams one GET per verified chunk from the project's own key,
// checks the plain BLAKE3 hash, and deletes objects that do not match their
// key (also an oversized one).
func TestStore_ConfirmReadBackDecisions(t *testing.T) {
	const key = "confirm-key"
	project := &domain.Project{ID: testProject, Slug: "game"}
	org := &domain.Organization{ID: 1, Slug: "acme"}

	data := []byte("content uploaded directly")
	hash := chunker.Sum(data)
	oversized := bytes.Repeat([]byte{1}, int(chunker.MaxChunkSize())+1)
	oversizedHash := chunker.Sum(oversized)

	cases := []struct {
		name     string
		hash     domain.Hash
		stored   []byte
		project  snow.ID
		recorded bool
		deleted  bool
	}{
		{name: "valid", hash: hash, stored: data, project: testProject, recorded: true},
		{name: "garbage", hash: hash, stored: []byte("garbage"), project: testProject, deleted: true},
		{name: "oversized", hash: oversizedHash, stored: oversized, project: testProject, deleted: true},
		{name: "not found", hash: hash},
		{name: "another project's object", hash: hash, stored: data, project: 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, fake := newTestStore(t)
			ctx := context.Background()
			if tc.stored != nil {
				fake.mu.Lock()
				fake.buckets["test-bucket"][store.key(tc.project, tc.hash)] = tc.stored
				fake.mu.Unlock()
			}
			repo := &memChunkRepo{}
			uc := usecase.NewChunk(repo, store, usecase.ChunkTransferConfig{SigningKey: key, PresignTTL: time.Hour})
			fake.gets.Store(0)

			missing, err := uc.ConfirmUploads(ctx, org, project, []domain.Hash{tc.hash}, nil)
			require.NoError(t, err)
			require.Equal(t, int64(1), fake.gets.Load())
			require.Zero(t, fake.heads.Load())
			if tc.recorded {
				require.Empty(t, missing)
				require.Equal(t, []usecase.ChunkRow{{Hash: tc.hash, SizeBytes: int64(len(tc.stored))}}, repo.rows)
			} else {
				require.Equal(t, []domain.Hash{tc.hash}, missing)
				require.Empty(t, repo.rows)
			}
			if tc.stored != nil {
				fake.mu.Lock()
				_, stillThere := fake.buckets["test-bucket"][store.key(tc.project, tc.hash)]
				fake.mu.Unlock()
				require.Equal(t, !tc.deleted, stillThere)
			}
		})
	}
}
