package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

func TestDiffWorkingFiles(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, ".nipaignore", "*.log\nbuild/\n")
	writeRepoFile(t, root, "a.txt", "a")
	writeRepoFile(t, root, "junk.log", "x")
	writeRepoFile(t, root, "kept.log", "x")
	writeRepoFile(t, root, "build/out.bin", "x")
	repo := newDiffStub()
	repo.snapshot = &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "kept.log"}}}
	repo.config = &domain.Config{Url: "http://example.com/org/project", Branch: "main", Sparse: []string{"src"}}
	d := NewDiff(nil, nil, repo)

	files, sparse, err := d.WorkingFiles(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []string{".nipaignore", "a.txt", "kept.log"}, files)
	require.Equal(t, []string{"src"}, sparse)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = d.WorkingFiles(ctx, root)
	require.ErrorIs(t, err, context.Canceled)

	boom := errors.New("boom")
	for _, set := range []func(*stubDiffRepo){
		func(s *stubDiffRepo) { s.initErr = boom },
		func(s *stubDiffRepo) { s.lockErr = boom },
		func(s *stubDiffRepo) { s.configErr = boom },
		func(s *stubDiffRepo) { s.snapshotErr = boom },
		func(s *stubDiffRepo) { s.stagedErr = boom },
	} {
		repo := newDiffStub()
		set(repo)
		_, _, err := NewDiff(nil, nil, repo).WorkingFiles(context.Background(), root)
		require.ErrorIs(t, err, boom)
	}
}
