package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/desktop/internal/connector"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/daemon"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func seedClone(t *testing.T, rel, content string) string {
	t.Helper()
	root := t.TempDir()
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(clientDomain.Config{Url: "https://nipa.test/org/game", Branch: "main"}))
	data := []byte(content)
	_, chunks, err := chunker.EncodeBytes(data, rel, false, chunker.EncodingRaw)
	require.NoError(t, err)
	hashes := make([]serverDomain.Hash, 0, len(chunks))
	stored := make([]*serverDomain.ChunkData, 0, len(chunks))
	refs := make([]serverDomain.Chunk, 0, len(chunks))
	for _, c := range chunks {
		hashes = append(hashes, c.Hash)
		stored = append(stored, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
		refs = append(refs, serverDomain.Chunk{Hash: c.Hash, SizeBytes: c.SizeBytes})
	}
	require.NoError(t, lr.StoreChunks(stored))
	require.NoError(t, lr.SaveTree(&serverDomain.TreeNode{
		Hash: serverDomain.Hash{0x01},
		Name: "root",
		FileChildren: []*serverDomain.File{{
			Name: rel, Hash: chunker.FileHash(hashes), SizeBytes: int64(len(data)), Chunks: refs,
		}},
	}))
	require.NoError(t, lr.Close())
	require.NoError(t, os.WriteFile(filepath.Join(root, rel), data, 0o644))
	return root
}

func TestChangesWithRealDaemon(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "daemon.json")
	srv, err := daemon.NewServer(daemon.Options{EndpointPath: endpoint, Runners: daemon.Runners{New: func(string) daemon.RepoOps {
		return daemon.RepoOps{Diff: daemon.SerialDiff(usecase.NewDiff(nil, nil, localrepo.NewLocalRepo()))}
	}}})
	require.NoError(t, err)
	serveCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(serveCtx) }()
	defer func() { cancel(); <-done }()
	<-srv.Ready()

	root := seedClone(t, "notes.txt", "one\ntwo\n")
	rec := &recorder{}
	svc := New(ConnectorDialer(connector.Options{EndpointPath: endpoint}), &memStore{}, nil)
	svc.Events = rec.emit
	defer func() { _ = svc.ServiceShutdown() }()

	changes, err := svc.Changes(ctx, root)
	require.NoError(t, err)
	require.Empty(t, changes.Staged)
	require.Empty(t, changes.Unstaged)

	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("one\nthree\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "new.txt"), []byte("hello\n"), 0o644))
	require.Eventually(t, func() bool { return len(rec.snapshot()) > 0 }, 5*time.Second, 10*time.Millisecond, "watcher changes reach the frontend")

	changes, err = svc.Changes(ctx, root)
	require.NoError(t, err)
	require.Equal(t, []FileChange{
		{Path: "new.txt", Status: "untracked", Kind: "text"},
		{Path: "notes.txt", Status: "modified", Kind: "text"},
	}, changes.Unstaged)

	diff, err := svc.FileDiff(ctx, root, "notes.txt")
	require.NoError(t, err)
	require.Equal(t, "modified", diff.Status)
	require.EqualValues(t, 1, diff.Additions)
	require.EqualValues(t, 1, diff.Deletions)
	var kinds []string
	for _, l := range diff.Hunks[0].Lines {
		kinds = append(kinds, l.Kind)
	}
	require.Equal(t, []string{"context", "removed", "added"}, kinds)

	diff, err = svc.FileDiff(ctx, root, "new.txt")
	require.NoError(t, err)
	require.True(t, diff.Untracked)
	require.Equal(t, "hello", diff.Hunks[0].Lines[0].Text)

	changes, err = svc.Stage(ctx, root, []string{"new.txt"})
	require.NoError(t, err)
	require.Equal(t, []FileChange{{Path: "new.txt", Status: "added", Kind: "text"}}, changes.Staged)

	changes, err = svc.Discard(ctx, root, []string{"notes.txt"})
	require.NoError(t, err)
	require.Empty(t, changes.Unstaged)
	data, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	require.NoError(t, err)
	require.Equal(t, "one\ntwo\n", string(data))

	changes, err = svc.Unstage(ctx, root, []string{"new.txt"})
	require.NoError(t, err)
	require.Empty(t, changes.Staged)

	_, err = svc.Push(ctx, root, "m")
	require.Error(t, err, "a daemon without runners cannot push")
}
