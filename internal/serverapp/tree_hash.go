package serverapp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nipalab/nipa/internal/usecase"
)

// BackfillTreeHashes runs the tree hash backfill (usecase.BackfillTreeHashes)
// after the schema migration and before the server starts serving. The first
// run after the upgrade recomputes every stored tree hash that changed when
// file modes became part of it; later runs only look at newer rows plus the
// overlap window before the previous run (usecase.TreeHashBackfillOverlap),
// and repair references an older server wrote with a rewritten hash.
func BackfillTreeHashes(ctx context.Context, repo usecase.TreeHashRepository) error {
	started := time.Now()
	result, err := usecase.BackfillTreeHashes(ctx, repo)
	if err != nil {
		return fmt.Errorf("tree hash backfill: %w", err)
	}
	if result.UpToID == result.FromID {
		return nil
	}
	slog.Info("tree hash backfill completed",
		"from_tree_id", result.FromID,
		"to_tree_id", result.UpToID,
		"seeds", result.Seeds,
		"repaired_references", result.Repaired,
		"recomputed", result.Recomputed,
		"updated", result.Updated,
		"duration", time.Since(started).String())
	return nil
}
