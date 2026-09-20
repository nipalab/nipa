package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/diff"
)

func TestDiff_RenameWorkingTree(t *testing.T) {
	root := t.TempDir()
	repo := newDiffStub()
	withSnapshotFile(t, repo, "old.txt", "same content\n")
	writeRepoFile(t, root, "new.txt", "same content\n")
	repo.staged = []string{"new.txt"}

	files, err := NewDiff(nil, nil, repo).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Renamed, files[0].Change.Status)
	require.Equal(t, "old.txt", files[0].Change.Old.Path)
	require.Equal(t, "new.txt", files[0].Change.Path)
	require.Equal(t, 100, files[0].Change.Similarity)
	require.Equal(t, []byte("same content\n"), files[0].Old)
	require.Equal(t, []byte("same content\n"), files[0].New)
}

func TestDiff_RenameSimilarContent(t *testing.T) {
	root := t.TempDir()
	repo := newDiffStub()
	withSnapshotFile(t, repo, "old.txt", "one\ntwo\nthree\nfour\n")
	writeRepoFile(t, root, "new.txt", "one\ntwo\nthree\n")
	repo.staged = []string{"new.txt"}

	files, err := NewDiff(nil, nil, repo).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Renamed, files[0].Change.Status)
	require.Equal(t, 75, files[0].Change.Similarity)
}

func TestDiff_RenameBelowThreshold(t *testing.T) {
	root := t.TempDir()
	repo := newDiffStub()
	withSnapshotFile(t, repo, "old.txt", "one\ntwo\nthree\nfour\n")
	writeRepoFile(t, root, "new.txt", "one\n")
	repo.staged = []string{"new.txt"}

	files, err := NewDiff(nil, nil, repo).Run(context.Background(), root, nil, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, diff.Added, files[0].Change.Status)
	require.Equal(t, diff.Deleted, files[1].Change.Status)
}
