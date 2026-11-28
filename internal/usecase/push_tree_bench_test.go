package usecase

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
)

// BenchmarkBuildNewTree_OneFileInWideTree measures the tree build of a
// one-file push whose two rebuilt directories (the root and one folder) each
// hold 1000 files and 1000 untouched subdirectories: the shape where the
// file/folder name check, one lookup per subdirectory, costs the most.
func BenchmarkBuildNewTree_OneFileInWideTree(b *testing.B) {
	const wide = 1000
	repo := NewMockbranchRepository(gomock.NewController(b))
	level := func(id int64) ([]*domain.File, []*domain.TreeNode) {
		files := make([]*domain.File, 0, wide)
		dirs := make([]*domain.TreeNode, 0, wide)
		for i := 0; i < wide; i++ {
			files = append(files, &domain.File{Name: fmt.Sprintf("file-%04d.txt", i), Mode: 0o644, Hash: domain.Hash{1}})
			dirs = append(dirs, &domain.TreeNode{ID: id*10000 + int64(i), Name: fmt.Sprintf("dir-%04d", i), Hash: domain.Hash{2, byte(i)}})
		}
		return files, dirs
	}
	rootFiles, rootDirs := level(1)
	subFiles, subDirs := level(2)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(1)).Return(&domain.TreeNode{ID: 1, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(1)).Return(rootFiles, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(1)).Return(rootDirs, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), rootDirs[0].ID).Return(subFiles, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), rootDirs[0].ID).Return(subDirs, nil).AnyTimes()
	head := &domain.Commit{ID: 9, TreeID: 1}
	files := []*domain.PushFile{{Path: "dir-0000/file-0500.txt", Mode: 0o644, FileHash: domain.Hash{3}}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := buildNewTree(context.Background(), repo, head, files, nil); err != nil {
			b.Fatal(err)
		}
	}
}
