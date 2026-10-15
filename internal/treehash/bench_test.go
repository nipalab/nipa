package treehash

import (
	"fmt"
	"testing"

	"github.com/nipalab/nipa/internal/domain"
)

func BenchmarkTreeHash(b *testing.B) {
	files := make([]FileEntry, 10000)
	for i := range files {
		files[i] = FileEntry{Name: fmt.Sprintf("asset_%06d.bin", i), Mode: 0o644}
		files[i].Hash[0] = byte(i)
		files[i].Hash[1] = byte(i >> 8)
	}
	trees := make([]TreeEntry, 100)
	for i := range trees {
		trees[i] = TreeEntry{Name: fmt.Sprintf("dir_%03d", i)}
		trees[i].Hash[0] = byte(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = TreeHash(files, trees)
	}
}

func BenchmarkCommitHash(b *testing.B) {
	var tree domain.Hash
	parents := []domain.Hash{{1}, {2}}
	message := "asset import: replace hero textures and rebuild the material library"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CommitHash(tree, parents, message)
	}
}
