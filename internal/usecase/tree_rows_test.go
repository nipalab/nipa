package usecase

import (
	"context"

	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
)

// expectTreeRows serves the batched tree reads of a mocked branch repository
// from rows keyed by node id: files (with their chunk lists, which
// ListFileMetaByTrees strips and ListChunksByFiles hands back by file id) and
// child directories. Every read may happen any number of times.
func expectTreeRows(repo *MockbranchRepository, files map[int64][]*domain.File, children map[int64][]*domain.TreeNode) {
	repo.EXPECT().ListFileMetaByTrees(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ids []int64) (map[int64][]*domain.File, error) {
		out := map[int64][]*domain.File{}
		for _, id := range ids {
			for _, file := range files[id] {
				meta := *file
				meta.Chunks = nil
				out[id] = append(out[id], &meta)
			}
		}
		return out, nil
	}).AnyTimes()
	repo.EXPECT().ListTreeChildrenByTrees(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ids []int64) (map[int64][]*domain.TreeNode, error) {
		out := map[int64][]*domain.TreeNode{}
		for _, id := range ids {
			for _, child := range children[id] {
				cp := *child
				out[id] = append(out[id], &cp)
			}
		}
		return out, nil
	}).AnyTimes()
	repo.EXPECT().ListChunksByFiles(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ids []int64) (map[int64][]domain.Chunk, error) {
		out := map[int64][]domain.Chunk{}
		for _, id := range ids {
			for _, list := range files {
				for _, file := range list {
					if file.ID == id && len(file.Chunks) > 0 {
						out[id] = file.Chunks
					}
				}
			}
		}
		return out, nil
	}).AnyTimes()
}
