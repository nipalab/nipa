package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func binaryContent(size int) []byte {
	content := make([]byte, size)
	x := uint32(12345)
	for i := range content {
		x = x*1664525 + 1013904223
		content[i] = byte(x >> 24)
	}
	content[0] = 0x00
	return content
}

func TestPush_Run_PackedAssetUsesLargeChunks(t *testing.T) {
	root := t.TempDir()
	content := binaryContent(8 << 20)
	writeRepoFile(t, root, "asset.png", string(content))

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"asset.png"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)
	prog := &stubProgress{}

	require.NoError(t, pusher.Run(context.Background(), root, "add asset", prog))

	require.Len(t, client.pushFiles, 1)
	require.True(t, client.pushFiles[0].IsBinary)
	require.NotEmpty(t, client.pushFiles[0].ChunkHashes)
	require.Less(t, len(client.pushFiles[0].ChunkHashes), 20, "packed assets must use large chunks")

	require.Len(t, prog.starts, 1)
	require.Equal(t, int64(8<<20), prog.starts[0].bytes)
	require.Less(t, prog.starts[0].objects, 20, "the progress estimate must use the packed profile")
}

func TestPush_Run_EditableBinaryKeepsSmallChunks(t *testing.T) {
	root := t.TempDir()
	content := binaryContent(2 << 20)
	writeRepoFile(t, root, "scene.blend", string(content))
	writeRepoFile(t, root, "tex.png", string(content))

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"scene.blend", "tex.png"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)

	require.NoError(t, pusher.Run(context.Background(), root, "add"))

	require.Len(t, client.pushFiles, 2)
	blend := client.pushFiles[0]
	png := client.pushFiles[1]
	require.Greater(t, len(blend.ChunkHashes), len(png.ChunkHashes),
		"editable formats keep smaller chunks than packed assets")
}

func TestWorkingFileHash_MatchesPushedHash(t *testing.T) {
	root := t.TempDir()
	content := binaryContent(1 << 20)
	writeRepoFile(t, root, "asset.png", string(content))

	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"},
		snapshot:   &domain.Snapshot{},
		staged:     []string{"asset.png"},
	}
	client := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	pusher := newTestPush(t, local, client)
	require.NoError(t, pusher.Run(context.Background(), root, "add asset"))

	wc := newWorkingCopy(t, local, root)
	workingHash, err := wc.workingFileHash("asset.png", "")
	require.NoError(t, err)
	require.Equal(t, client.pushFiles[0].FileHash, workingHash,
		"status/diff hashing must agree with the pushed file hash")
}
