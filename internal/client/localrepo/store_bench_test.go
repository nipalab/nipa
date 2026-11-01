package localrepo

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

const (
	benchDirs         = 100
	benchFilesPerDir  = 100
	benchChangedFiles = 5
)

func benchHash(a, b uint32) serverDomain.Hash {
	var h serverDomain.Hash
	binary.BigEndian.PutUint32(h[:4], a)
	binary.BigEndian.PutUint32(h[4:8], b)
	return h
}

func benchTree(version uint32) *serverDomain.TreeNode {
	root := &serverDomain.TreeNode{Hash: benchHash(0xffffffff, version), Name: "root"}
	for d := 0; d < benchDirs; d++ {
		dir := &serverDomain.TreeNode{Hash: benchHash(uint32(d), 0), Name: fmt.Sprintf("d%03d", d)}
		for f := 0; f < benchFilesPerDir; f++ {
			fileVersion := uint32(0)
			if d == 0 && f < benchChangedFiles {
				fileVersion = version
			}
			dir.FileChildren = append(dir.FileChildren, &serverDomain.File{
				Hash:      benchHash(uint32(d*benchFilesPerDir+f), fileVersion),
				Name:      fmt.Sprintf("f%03d.txt", f),
				Mode:      0o644,
				SizeBytes: 64,
				Chunks:    []serverDomain.Chunk{{Hash: benchHash(uint32(d*benchFilesPerDir+f), fileVersion+1), SizeBytes: 64}},
			})
		}
		if d == 0 {
			dir.Hash = benchHash(0, version)
		}
		root.TreeChildren = append(root.TreeChildren, dir)
	}
	return root
}

// BenchmarkSaveTree_TenKFiles_FiveChanges measures the cost of saving a new
// tree that differs from the stored one by a handful of files.
func BenchmarkSaveTree_TenKFiles_FiveChanges(b *testing.B) {
	lr := NewLocalRepo()
	require.NoError(b, lr.Init(b.TempDir()))
	b.Cleanup(func() { _ = lr.Close() })
	require.NoError(b, lr.SaveTree(benchTree(0)))

	b.ResetTimer()
	for i := 1; i <= b.N; i++ {
		tree := benchTree(uint32(i))
		b.StartTimer()
		require.NoError(b, lr.SaveTree(tree))
		b.StopTimer()
	}
}
