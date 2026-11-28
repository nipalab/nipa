package pushbatch

import (
	"context"
	"slices"
	"strings"

	"github.com/nipalab/nipa/internal/usecase"
)

// pathChangeBatch bounds the rows of one path change insert.
var pathChangeBatch = 5000

// InsertPathChanges passes one commit's path change index rows as JSON arrays
// of [path, kind] in batches of pathChangeBatch, sorted by path and kind so
// concurrent writers of the same commit (two reads indexing it at once) take
// the primary key locks in the same order. An empty list inserts nothing.
func InsertPathChanges(ctx context.Context, changes []usecase.PathChange, insert ChunkRowInserter) error {
	rows := slices.Clone(changes)
	slices.SortFunc(rows, func(a, b usecase.PathChange) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return int(a.Kind) - int(b.Kind)
	})
	rows = slices.Compact(rows)
	for start := 0; start < len(rows); start += pathChangeBatch {
		end := min(start+pathChangeBatch, len(rows))
		batch := make([][]any, 0, end-start)
		for _, row := range rows[start:end] {
			batch = append(batch, []any{row.Path, int(row.Kind)})
		}
		if err := insertJSON(ctx, batch, insert); err != nil {
			return err
		}
	}
	return nil
}
