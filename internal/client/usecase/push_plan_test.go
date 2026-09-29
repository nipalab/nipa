package usecase

import (
	"context"
	"errors"
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

func TestPush_Plan_Errors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*stubLocalRepo)
		want   string
	}{
		{"init", func(l *stubLocalRepo) { l.initErr = errors.New("init failed") }, "init failed"},
		{"config", func(l *stubLocalRepo) { l.configLoadErr = errors.New("config failed") }, "config failed"},
		{"url", func(l *stubLocalRepo) { l.loadConfig.Url = "not-a-nipa-url" }, "invalid"},
		{"revert state", func(l *stubLocalRepo) { l.revertStateErr = errors.New("state failed") }, "state failed"},
		{"snapshot", func(l *stubLocalRepo) { l.snapshotErr = errors.New("snapshot failed") }, "snapshot failed"},
		{"staged", func(l *stubLocalRepo) { l.stagedErr = errors.New("staged failed") }, "staged failed"},
		{"missing chunks", func(l *stubLocalRepo) { l.missingChunksErr = errors.New("missing failed") }, "missing failed"},
		{"missing staged file", func(l *stubLocalRepo) { l.staged = []string{"gone.txt"} }, "does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeRepoFile(t, root, "a.txt", "hello")
			local := &stubLocalRepo{
				loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
				snapshot:   &domain.Snapshot{},
				staged:     []string{"a.txt"},
			}
			tc.mutate(local)
			pusher := newTestPush(t, local, &stubPushClient{})

			_, err := pusher.Plan(context.Background(), root)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestPlanCollector_DedupesChunks(t *testing.T) {
	collector := newPlanCollector()
	chunk := &serverDomain.ChunkData{Hash: serverDomain.Hash{0x01}, Data: []byte("abcd")}
	require.NoError(t, collector.add(chunk))
	require.NoError(t, collector.add(chunk))

	require.Len(t, collector.order, 1)
	require.Equal(t, int64(4), collector.sizes[serverDomain.Hash{0x01}])
}
