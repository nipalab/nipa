package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestPush_Run_CachesTextAndPinnedRawOnlyAndUnpinsExactPaths(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "notes.txt", "hello world")
	writeRepoFile(t, root, "loose.bin", "\x00loose binary\x01")
	writeRepoFile(t, root, "keep/hero.bin", "\x00pinned binary\x02")

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{
			{Path: "gone.bin", Encoding: "raw"},
		}},
		staged: []string{"notes.txt", "loose.bin", "keep/hero.bin", "gone.bin"},
		pins:   []string{"keep", "loose.bin.meta"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}

	require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "assets"))

	byPath := map[string]*serverDomain.PushFile{}
	for _, f := range client.pushFiles {
		byPath[f.Path] = f
	}
	require.Len(t, client.uploadedChunks, 3, "every chunk is still uploaded from memory")
	cached := func(path string) bool {
		for _, h := range byPath[path].ChunkHashes {
			if _, ok := local.storedChunks[h]; !ok {
				return false
			}
		}
		return true
	}
	require.True(t, cached("notes.txt"))
	require.True(t, cached("keep/hero.bin"))
	require.False(t, cached("loose.bin"), "raw content of an unpinned file is not cached")
	require.ElementsMatch(t, []string{"gone.bin", "notes.txt", "loose.bin", "keep/hero.bin"}, local.unpinned)
	require.NotContains(t, local.unpinned, "keep", "directory pins span the editing pass")
}

func TestPush_Run_PinListFailureStopsBeforeUpload(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
		pinsErr:    errors.New("pins"),
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	require.EqualError(t, newTestPush(t, local, client).Run(context.Background(), root, "msg"), "pins")
	require.Empty(t, client.uploadedChunks)
}

func TestPush_Run_UnpinFailureOnlyWarns(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"a.txt"},
		unpinErr:   errors.New("db"),
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "msg"))
}
