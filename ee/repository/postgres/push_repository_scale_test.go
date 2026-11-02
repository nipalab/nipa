// Enterprise Edition: see ee/LICENSE.
package postgres

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

// TestApplyPushScale times one ApplyPush of NIPA_BENCH_FILES single-chunk files
// spread over ten directories. Skipped unless the variable is set; chunk setup
// is not part of the measurement.
func (s *PushRepositorySuite) TestApplyPushScale() {
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		s.T().Skip("set NIPA_BENCH_FILES=<count> to run the ApplyPush scale harness")
	}
	const dirs = 10
	ctx := context.Background()
	repo, _, projectID, branchID := s.newPushTestEnv()

	nodes := []usecase.PushNodeRow{{ID: 1, Hash: chunker.Sum([]byte("root")), Name: "root", Mode: 0o444}}
	for d := 0; d < dirs; d++ {
		name := fmt.Sprintf("d%02d", d)
		parent := int64(1)
		nodes = append(nodes, usecase.PushNodeRow{ID: int64(d + 2), Hash: chunker.Sum([]byte(name)), Name: name, Mode: 0o444, ParentID: &parent})
	}
	files := make([]usecase.PushFileRow, 0, total)
	for i := 0; i < total; i++ {
		chunkHash, size, fileHash := chunkRow(s.T(), fmt.Sprintf("scale file %06d", i))
		s.Require().NoError(repo.InsertChunkIfNotExists(ctx, chunkHash, size))
		s.Require().NoError(repo.LinkProjectChunk(ctx, projectID, chunkHash))
		files = append(files, usecase.PushFileRow{
			Name: fmt.Sprintf("f%06d.txt", i), Mode: 0o644, SizeBytes: size, Hash: fileHash,
			TreeID: int64(i%dirs + 2), ChunkHashes: []domain.Hash{chunkHash},
		})
	}

	start := time.Now()
	s.Require().NoError(repo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID:  projectID,
		BranchID:   branchID,
		CommitID:   snow.ID(777),
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, "scale"),
		UserID:     snow.ID(1),
		Message:    "scale",
		Nodes:      nodes,
		Files:      files,
	}))
	s.T().Logf("ApplyPush %d files: %s", total, time.Since(start).Round(time.Millisecond))
}
