package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	clientDiff "github.com/nipalab/nipa/internal/client/diff"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

var errTestDiff = errors.New("diff test error")

func snapshotFileWith(t *testing.T, path, content string, mode int) domain.SnapshotFile {
	t.Helper()
	chunks, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	hashes := make([]serverDomain.Hash, len(chunks))
	wrapped := make([]serverDomain.Chunk, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
		wrapped[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
	}
	return domain.SnapshotFile{
		Path:      path,
		Hash:      chunker.FileHash(hashes),
		Mode:      mode,
		SizeBytes: int64(len(content)),
		Chunks:    wrapped,
	}
}

func diffTestRepo(t *testing.T, files map[string]domain.SnapshotFile, stored map[serverDomain.Hash][]byte) (*stubLocalRepo, string) {
	t.Helper()
	root := t.TempDir()
	stub := &stubLocalRepo{
		config:       domain.Config{Url: "http://example.com/o/p", Branch: "main"},
		storedChunks: stored,
	}
	var list []domain.SnapshotFile
	for _, f := range files {
		list = append(list, f)
	}
	stub.snapshot = &domain.Snapshot{TreeHash: "abc", Files: list}
	return stub, root
}

func writeWorkingFile(t *testing.T, root, path, content string) {
	t.Helper()
	fp := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
	require.NoError(t, os.WriteFile(fp, []byte(content), 0o644))
}

func TestDiff_Run_RevisionsRejected(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	res, err := NewDiff(stub).Run(context.Background(), root, []string{"main"})
	require.Error(t, err)
	require.Nil(t, res)
	require.Contains(t, err.Error(), "not supported yet")
}

func TestDiff_Run_InitError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.initErr = errTestDiff
	_, err := NewDiff(stub).Run(context.Background(), root, nil)
	require.ErrorIs(t, err, errTestDiff)
}

func TestDiff_Run_Clean(t *testing.T) {
	f := snapshotFileWith(t, "a.txt", "hello\n", 2)
	stored := map[serverDomain.Hash][]byte{}
	for _, c := range f.Chunks {
		stored[c.Hash] = []byte("hello\n")
	}
	stub, root := diffTestRepo(t, map[string]domain.SnapshotFile{"a.txt": f}, stored)
	writeWorkingFile(t, root, "a.txt", "hello\n")

	res, err := NewDiff(stub).Run(context.Background(), root, nil)
	require.NoError(t, err)
	require.Empty(t, res.Files)
	require.Equal(t, root, stub.initTarget)
	require.Equal(t, "main (last synced)", res.Base)
	require.Equal(t, "working tree", res.Head)
}

func TestDiff_Run_Modified(t *testing.T) {
	f := snapshotFileWith(t, "a.txt", "old\n", 2)
	stored := map[serverDomain.Hash][]byte{}
	for _, c := range f.Chunks {
		stored[c.Hash] = []byte("old\n")
	}
	stub, root := diffTestRepo(t, map[string]domain.SnapshotFile{"a.txt": f}, stored)
	writeWorkingFile(t, root, "a.txt", "new\n")

	res, err := NewDiff(stub).Run(context.Background(), root, nil)
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	got := res.Files[0]
	require.Equal(t, clientDiff.Modified, got.Change.Status)
	require.Equal(t, "a.txt", got.Change.Path)
	require.False(t, got.OldUnavailable)
	require.Equal(t, []byte("old\n"), got.Old)
	require.Equal(t, []byte("new\n"), got.New)
}

func TestDiff_Run_Deleted(t *testing.T) {
	f := snapshotFileWith(t, "gone.txt", "bye\n", 2)
	stored := map[serverDomain.Hash][]byte{}
	for _, c := range f.Chunks {
		stored[c.Hash] = []byte("bye\n")
	}
	stub, root := diffTestRepo(t, map[string]domain.SnapshotFile{"gone.txt": f}, stored)

	res, err := NewDiff(stub).Run(context.Background(), root, nil)
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	got := res.Files[0]
	require.Equal(t, clientDiff.Deleted, got.Change.Status)
	require.False(t, got.OldUnavailable)
	require.Equal(t, []byte("bye\n"), got.Old)
	require.Nil(t, got.New)
}

func TestDiff_Run_OldUnavailable(t *testing.T) {
	f := snapshotFileWith(t, "gone.txt", "bye\n", 2)
	stub, root := diffTestRepo(t, map[string]domain.SnapshotFile{"gone.txt": f}, nil)
	stub.loadChunkErr = errTestDiff

	res, err := NewDiff(stub).Run(context.Background(), root, nil)
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	got := res.Files[0]
	require.Equal(t, clientDiff.Deleted, got.Change.Status)
	require.True(t, got.OldUnavailable)
	require.Nil(t, got.Old)
}

func TestDiff_Run_StagedNewFile(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.staged = []string{"new.txt"}
	writeWorkingFile(t, root, "new.txt", "fresh\n")
	writeWorkingFile(t, root, "untracked.txt", "noise\n")

	res, err := NewDiff(stub).Run(context.Background(), root, nil)
	require.NoError(t, err)
	require.Len(t, res.Files, 1, "staged new files are shown, unstaged untracked files are not")
	got := res.Files[0]
	require.Equal(t, clientDiff.Added, got.Change.Status)
	require.Equal(t, "new.txt", got.Change.Path)
	require.Equal(t, []byte("fresh\n"), got.New)
	require.Nil(t, got.Old)
}

func TestDiff_Run_ModeOnly(t *testing.T) {
	f := snapshotFileWith(t, "run.sh", "echo hi\n", 2)
	stored := map[serverDomain.Hash][]byte{}
	for _, c := range f.Chunks {
		stored[c.Hash] = []byte("echo hi\n")
	}
	stub, root := diffTestRepo(t, map[string]domain.SnapshotFile{"run.sh": f}, stored)
	writeWorkingFile(t, root, "run.sh", "echo hi\n")
	require.NoError(t, os.Chmod(filepath.Join(root, "run.sh"), 0o755))

	res, err := NewDiff(stub).Run(context.Background(), root, nil)
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	got := res.Files[0]
	require.Equal(t, clientDiff.Modified, got.Change.Status)
	require.Equal(t, 2, got.Change.Old.Mode)
	require.Equal(t, 3, got.Change.New.Mode)
	require.Equal(t, []byte("echo hi\n"), got.Old)
	require.Equal(t, []byte("echo hi\n"), got.New)
}

func TestDiff_Run_Binary(t *testing.T) {
	content := "GIF89a\x00binary"
	f := snapshotFileWith(t, "img.gif", content, 2)
	f.IsBinary = true
	stored := map[serverDomain.Hash][]byte{}
	for _, c := range f.Chunks {
		stored[c.Hash] = []byte(content)
	}
	stub, root := diffTestRepo(t, map[string]domain.SnapshotFile{"img.gif": f}, stored)
	writeWorkingFile(t, root, "img.gif", "GIF89a\x00changed")

	res, err := NewDiff(stub).Run(context.Background(), root, nil)
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	got := res.Files[0]
	require.Equal(t, clientDiff.Modified, got.Change.Status)
	require.True(t, got.Change.Old.IsBinary)
	require.True(t, got.Change.New.IsBinary)
}

func TestDiff_Run_SnapshotModeZero(t *testing.T) {
	f := snapshotFileWith(t, "a.txt", "hello\n", 0)
	stored := map[serverDomain.Hash][]byte{}
	for _, c := range f.Chunks {
		stored[c.Hash] = []byte("hello\n")
	}
	stub, root := diffTestRepo(t, map[string]domain.SnapshotFile{"a.txt": f}, stored)
	writeWorkingFile(t, root, "a.txt", "hello\n")

	res, err := NewDiff(stub).Run(context.Background(), root, nil)
	require.NoError(t, err)
	require.Empty(t, res.Files, "unspecified snapshot mode must not report a phantom mode change")
}

func TestDiff_Run_Errors(t *testing.T) {
	t.Run("LoadConfig", func(t *testing.T) {
		stub, root := diffTestRepo(t, nil, nil)
		stub.configLoadErr = errTestDiff
		_, err := NewDiff(stub).Run(context.Background(), root, nil)
		require.ErrorIs(t, err, errTestDiff)
	})
	t.Run("Snapshot", func(t *testing.T) {
		stub, root := diffTestRepo(t, nil, nil)
		stub.snapshotErr = errTestDiff
		_, err := NewDiff(stub).Run(context.Background(), root, nil)
		require.ErrorIs(t, err, errTestDiff)
	})
	t.Run("ListStaged", func(t *testing.T) {
		stub, root := diffTestRepo(t, nil, nil)
		stub.stagedErr = errTestDiff
		_, err := NewDiff(stub).Run(context.Background(), root, nil)
		require.ErrorIs(t, err, errTestDiff)
	})
}

func TestServerModeFromPerm(t *testing.T) {
	require.Equal(t, 3, serverModeFromPerm(0o755))
	require.Equal(t, 3, serverModeFromPerm(0o100755))
	require.Equal(t, 1, serverModeFromPerm(0o444))
	require.Equal(t, 2, serverModeFromPerm(0o644))
	require.Equal(t, 2, serverModeFromPerm(0o600))
}
