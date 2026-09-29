package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func planChunkHashes(t *testing.T, path, content string) []serverDomain.Hash {
	t.Helper()
	_, chunks, _, err := chunkFile(path, []byte(content), "")
	require.NoError(t, err)
	hashes := make([]serverDomain.Hash, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
	}
	return hashes
}

func TestPush_Plan_Added(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	hashes := planChunkHashes(t, "a.txt", "hello world")
	local := &stubLocalRepo{
		loadConfig:    &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:      &domain.Snapshot{},
		staged:        []string{"a.txt"},
		missingChunks: hashes,
	}
	pusher := newTestPush(t, local, &stubPushClient{})

	plan, err := pusher.Plan(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, "push", plan.Kind)
	require.Equal(t, []domain.PlanChange{{Path: "a.txt", Status: "A", SizeBytes: 11}}, plan.Changes)
	require.Equal(t, len(hashes), plan.UploadObjects)
	require.Greater(t, plan.UploadBytes, int64(0))
	require.False(t, local.clearedStaged, "a dry run must not clear staged files")
}

func TestPush_Plan_ModifiedAndRemoved(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "b.txt", "changed")
	hashes := planChunkHashes(t, "b.txt", "changed")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{
			{Path: "a.txt", Hash: serverDomain.Hash{0x01}, Mode: 2, SizeBytes: 3},
			{Path: "b.txt", Hash: serverDomain.Hash{0x02}, Mode: 2, SizeBytes: 3},
		}},
		staged:        []string{"a.txt", "b.txt"},
		missingChunks: hashes,
	}
	pusher := newTestPush(t, local, &stubPushClient{})

	plan, err := pusher.Plan(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []domain.PlanChange{
		{Path: "a.txt", Status: "D", SizeBytes: 3},
		{Path: "b.txt", Status: "M", SizeBytes: 7},
	}, plan.Changes)
	require.Equal(t, len(hashes), plan.UploadObjects)
}

func TestPush_Plan_UnchangedStagedIgnored(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "same")
	fileHash, _, _, err := chunkFile("a.txt", []byte("same"), "")
	require.NoError(t, err)
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{
			{Path: "a.txt", Hash: fileHash, Mode: 2, SizeBytes: 4},
		}},
		staged: []string{"a.txt"},
	}
	pusher := newTestPush(t, local, &stubPushClient{})

	plan, err := pusher.Plan(context.Background(), root)
	require.NoError(t, err)
	require.Empty(t, plan.Changes)
}

func TestPush_Plan_NothingStaged(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
	}
	pusher := newTestPush(t, local, &stubPushClient{})

	_, err := pusher.Plan(context.Background(), t.TempDir())
	require.EqualError(t, err, "nothing staged to push; run nipa add first")
}

func TestPush_Plan_RevertSequenceInProgress(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
		revertState: &domain.RevertState{Targets: []domain.CommitRef{
			{ID: "1"}, {ID: "2"},
		}},
	}
	pusher := newTestPush(t, local, &stubPushClient{})

	_, err := pusher.Plan(context.Background(), t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "revert sequence is in progress")
}
