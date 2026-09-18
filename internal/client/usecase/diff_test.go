package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	clientDiff "github.com/nipalab/nipa/internal/client/diff"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

var errTestDiff = errors.New("diff test error")

type stubDiffClient struct {
	connectHost    string
	connectErr     error
	manifest       *serverDomain.TreeNode
	manifestErr    error
	lastBranch     string
	commitFn       func(id *snow.ID, hash *serverDomain.Hash) (*serverDomain.TreeNode, error)
	commitIDReq    *snow.ID
	commitHashReq  *serverDomain.Hash
	downloadData   map[serverDomain.Hash][]byte
	downloadErr    error
	downloadHashes []serverDomain.Hash
}

func (s *stubDiffClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubDiffClient) GetTreeNodeManifest(_ context.Context, _, _, branch, _ string) (*serverDomain.TreeNode, error) {
	s.lastBranch = branch
	if s.manifestErr != nil {
		return nil, s.manifestErr
	}
	return s.manifest, nil
}

func (s *stubDiffClient) GetTreeNodeManifestByCommit(_ context.Context, _, _ string, id *snow.ID, hash *serverDomain.Hash) (*serverDomain.TreeNode, error) {
	s.commitIDReq = id
	s.commitHashReq = hash
	if s.commitFn != nil {
		return s.commitFn(id, hash)
	}
	return s.manifest, nil
}

func (s *stubDiffClient) DownloadChunks(_ context.Context, hashes []serverDomain.Hash, _ ...func(h serverDomain.Hash, data []byte)) (map[serverDomain.Hash][]byte, error) {
	s.downloadHashes = hashes
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	return s.downloadData, nil
}

func newTestDiff(t *testing.T, local diffLocalRepo, client diffClient) *Diff {
	t.Helper()
	auth := NewAuth(nil, &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: signTestToken(t, "secret")}}, nil)
	return NewDiff(auth, client, local)
}

// diffTreeNode builds a server tree manifest from name→content.
func diffTreeNode(t *testing.T, files map[string]string) *serverDomain.TreeNode {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var children []*serverDomain.File
	for _, name := range names {
		content := files[name]
		chunks, err := chunker.ChunkAll([]byte(content))
		require.NoError(t, err)
		var wrapped []serverDomain.Chunk
		for _, c := range chunks {
			wrapped = append(wrapped, serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))})
		}
		children = append(children, &serverDomain.File{
			Name:      name,
			Mode:      2,
			SizeBytes: int64(len(content)),
			Chunks:    wrapped,
		})
	}
	return &serverDomain.TreeNode{Name: "root", FileChildren: children}
}

// chunkHashes returns the chunk hashes of each content, for stubbing
// MissingChunks responses.
func chunkHashes(t *testing.T, contents ...string) []serverDomain.Hash {
	t.Helper()
	var out []serverDomain.Hash
	for _, content := range contents {
		chunks, err := chunker.ChunkAll([]byte(content))
		require.NoError(t, err)
		for _, c := range chunks {
			out = append(out, c.Hash)
		}
	}
	return out
}

// chunkDataMap chunks each content for download stubs.
func chunkDataMap(t *testing.T, contents ...string) map[serverDomain.Hash][]byte {
	t.Helper()
	out := make(map[serverDomain.Hash][]byte)
	for _, content := range contents {
		chunks, err := chunker.ChunkAll([]byte(content))
		require.NoError(t, err)
		for _, c := range chunks {
			out[c.Hash] = c.Data
		}
	}
	return out
}

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

func TestDiff_Run_TooManyRevs(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	res, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, []string{"a", "b", "c"})
	require.Error(t, err)
	require.Nil(t, res)
	require.Contains(t, err.Error(), "too many revisions")
}

func TestDiff_Run_InvalidURL(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.config = domain.Config{Url: "not-a-url", Branch: "main"}
	_, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, []string{"main"})
	require.Error(t, err)
}

func TestDiff_Run_InitError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.initErr = errTestDiff
	_, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
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

	res, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
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

	res, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
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

	res, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
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

	res, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
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

	res, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
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

	res, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
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

	res, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
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

	res, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
	require.NoError(t, err)
	require.Empty(t, res.Files, "unspecified snapshot mode must not report a phantom mode change")
}

func TestDiff_Run_Errors(t *testing.T) {
	t.Run("LoadConfig", func(t *testing.T) {
		stub, root := diffTestRepo(t, nil, nil)
		stub.configLoadErr = errTestDiff
		_, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
		require.ErrorIs(t, err, errTestDiff)
	})
	t.Run("Snapshot", func(t *testing.T) {
		stub, root := diffTestRepo(t, nil, nil)
		stub.snapshotErr = errTestDiff
		_, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
		require.ErrorIs(t, err, errTestDiff)
	})
	t.Run("ListStaged", func(t *testing.T) {
		stub, root := diffTestRepo(t, nil, nil)
		stub.stagedErr = errTestDiff
		_, err := newTestDiff(t, stub, &stubDiffClient{}).Run(context.Background(), root, nil)
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

func TestDiff_Run_WorkingVsRevision_Branch(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.missingChunks = chunkHashes(t, "old\n")
	writeWorkingFile(t, root, "a.txt", "new\n")
	client := &stubDiffClient{
		manifest:     diffTreeNode(t, map[string]string{"a.txt": "old\n"}),
		downloadData: chunkDataMap(t, "old\n"),
	}

	res, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{"main"})
	require.NoError(t, err)
	require.Equal(t, "example.com", client.connectHost)
	require.Equal(t, "main", client.lastBranch)
	require.Equal(t, "main", res.Base)
	require.Equal(t, "working tree", res.Head)
	require.Len(t, res.Files, 1)
	got := res.Files[0]
	require.Equal(t, clientDiff.Modified, got.Change.Status)
	require.False(t, got.OldUnavailable)
	require.Equal(t, []byte("old\n"), got.Old)
	require.Equal(t, []byte("new\n"), got.New)
	require.Len(t, client.downloadHashes, 1)
	require.Equal(t, chunker.Sum([]byte("old\n")), client.downloadHashes[0])
}

func TestDiff_Run_WorkingVsRevision_Hash(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.missingChunks = chunkHashes(t, "old\n")
	writeWorkingFile(t, root, "a.txt", "new\n")
	client := &stubDiffClient{
		manifest:     diffTreeNode(t, map[string]string{"a.txt": "old\n"}),
		downloadData: chunkDataMap(t, "old\n"),
	}

	rev := "ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34"
	res, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{rev})
	require.NoError(t, err)
	require.Empty(t, client.lastBranch, "hash revisions must not hit the branch endpoint")
	require.Nil(t, client.commitIDReq)
	require.NotNil(t, client.commitHashReq)
	require.Equal(t, rev, client.commitHashReq.String())
	require.Len(t, res.Files, 1)
	require.Equal(t, []byte("old\n"), res.Files[0].Old)
}

func TestDiff_Run_WorkingVsRevision_IDFallback(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.missingChunks = chunkHashes(t, "old\n")
	writeWorkingFile(t, root, "a.txt", "new\n")
	id := snow.ID(99)
	client := &stubDiffClient{
		manifestErr:  &domain.Error{Code: 404, Message: `branch "l3" not found`},
		manifest:     diffTreeNode(t, map[string]string{"a.txt": "old\n"}),
		downloadData: chunkDataMap(t, "old\n"),
	}

	res, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{id.Base36()})
	require.NoError(t, err)
	require.Equal(t, id.Base36(), client.lastBranch, "branch is tried first")
	require.NotNil(t, client.commitIDReq)
	require.Equal(t, id, *client.commitIDReq)
	require.Nil(t, client.commitHashReq)
	require.Len(t, res.Files, 1)
	require.Equal(t, []byte("old\n"), res.Files[0].Old)
}

func TestDiff_Run_WorkingVsRevision_UnknownRev(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	client := &stubDiffClient{
		manifestErr: &domain.Error{Code: 404, Message: "nope"},
	}

	_, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{"nope!!!"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `revision "nope!!!" not found`)
}

func TestDiff_Run_WorkingVsRevision_BranchError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	client := &stubDiffClient{manifestErr: errTestDiff}

	_, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{"main"})
	require.ErrorIs(t, err, errTestDiff)
}

func TestDiff_Run_WorkingVsRevision_EmptyBranch(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.staged = []string{"new.txt"}
	writeWorkingFile(t, root, "new.txt", "fresh\n")
	client := &stubDiffClient{manifest: nil}

	res, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{"empty"})
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	require.Equal(t, clientDiff.Added, res.Files[0].Change.Status)
	require.Equal(t, []byte("fresh\n"), res.Files[0].New)
}

func TestDiff_Run_WorkingVsRevision_ConnectError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	client := &stubDiffClient{connectErr: errTestDiff}

	_, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{"main"})
	require.ErrorIs(t, err, errTestDiff)
}

func TestDiff_Run_RevisionDiff_TwoCommits(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.missingChunks = chunkHashes(t, "v1\n", "v2\n", "new\n")
	idA := snow.ID(11)
	idB := snow.ID(22)
	trees := map[snow.ID]*serverDomain.TreeNode{
		idA: diffTreeNode(t, map[string]string{"a.txt": "v1\n"}),
		idB: diffTreeNode(t, map[string]string{"a.txt": "v2\n", "b.txt": "new\n"}),
	}
	client := &stubDiffClient{
		manifestErr: &domain.Error{Code: 404, Message: "not a branch"},
		commitFn: func(id *snow.ID, _ *serverDomain.Hash) (*serverDomain.TreeNode, error) {
			return trees[*id], nil
		},
		downloadData: chunkDataMap(t, "v1\n", "v2\n", "new\n"),
	}

	res, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{idA.Base36(), idB.Base36()})
	require.NoError(t, err)
	require.Equal(t, idA.Base36(), res.Base)
	require.Equal(t, idB.Base36(), res.Head)
	require.Len(t, res.Files, 2)

	modified := res.Files[0]
	require.Equal(t, "a.txt", modified.Change.Path)
	require.Equal(t, clientDiff.Modified, modified.Change.Status)
	require.Equal(t, []byte("v1\n"), modified.Old)
	require.Equal(t, []byte("v2\n"), modified.New)

	added := res.Files[1]
	require.Equal(t, "b.txt", added.Change.Path)
	require.Equal(t, clientDiff.Added, added.Change.Status)
	require.Equal(t, []byte("new\n"), added.New)

	require.Len(t, client.downloadHashes, 3, "only changed text chunks are downloaded")
}

func TestDiff_Run_RevisionDiff_BinarySkipped(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	idA := snow.ID(11)
	idB := snow.ID(22)
	treeA := diffTreeNode(t, map[string]string{"img.png": "PNG-old"})
	treeA.FileChildren[0].IsBinary = true
	treeB := diffTreeNode(t, map[string]string{"img.png": "PNG-new"})
	treeB.FileChildren[0].IsBinary = true
	trees := map[snow.ID]*serverDomain.TreeNode{idA: treeA, idB: treeB}
	client := &stubDiffClient{
		manifestErr: &domain.Error{Code: 404, Message: "not a branch"},
		commitFn: func(id *snow.ID, _ *serverDomain.Hash) (*serverDomain.TreeNode, error) {
			return trees[*id], nil
		},
		downloadData: map[serverDomain.Hash][]byte{},
	}

	res, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{idA.Base36(), idB.Base36()})
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	require.Equal(t, clientDiff.Modified, res.Files[0].Change.Status)
	require.Empty(t, client.downloadHashes, "binary content is never downloaded")
}

func TestDiff_Run_RevisionDiff_MissingChunksError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.missingChunksErr = errTestDiff
	idA := snow.ID(11)
	idB := snow.ID(22)
	trees := map[snow.ID]*serverDomain.TreeNode{
		idA: diffTreeNode(t, map[string]string{"a.txt": "v1\n"}),
		idB: diffTreeNode(t, map[string]string{"a.txt": "v2\n"}),
	}
	client := &stubDiffClient{
		manifestErr: &domain.Error{Code: 404, Message: "not a branch"},
		commitFn: func(id *snow.ID, _ *serverDomain.Hash) (*serverDomain.TreeNode, error) {
			return trees[*id], nil
		},
		downloadData: chunkDataMap(t, "v1\n", "v2\n"),
	}

	_, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{idA.Base36(), idB.Base36()})
	require.ErrorIs(t, err, errTestDiff)
}

func TestDiff_Run_RevisionDiff_DownloadError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.missingChunks = chunkHashes(t, "v1\n", "v2\n")
	idA := snow.ID(11)
	idB := snow.ID(22)
	trees := map[snow.ID]*serverDomain.TreeNode{
		idA: diffTreeNode(t, map[string]string{"a.txt": "v1\n"}),
		idB: diffTreeNode(t, map[string]string{"a.txt": "v2\n"}),
	}
	client := &stubDiffClient{
		manifestErr: &domain.Error{Code: 404, Message: "not a branch"},
		commitFn: func(id *snow.ID, _ *serverDomain.Hash) (*serverDomain.TreeNode, error) {
			return trees[*id], nil
		},
		downloadErr: errTestDiff,
	}

	_, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{idA.Base36(), idB.Base36()})
	require.ErrorIs(t, err, errTestDiff)
}

func TestDiff_Run_RevisionDiff_ResolveError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	client := &stubDiffClient{manifestErr: errTestDiff}

	_, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{"main", "dev"})
	require.ErrorIs(t, err, errTestDiff)
}

func TestIsNotFoundError(t *testing.T) {
	require.True(t, isNotFoundError(&domain.Error{Code: 404, Message: "x"}))
	require.False(t, isNotFoundError(&domain.Error{Code: 400, Message: "x"}))
	require.False(t, isNotFoundError(errTestDiff))
	require.False(t, isNotFoundError(nil))
}

func TestDiff_Run_WorkingVsRevision_StagedError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.stagedErr = errTestDiff
	client := &stubDiffClient{manifest: diffTreeNode(t, map[string]string{"a.txt": "old\n"})}

	_, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{"main"})
	require.ErrorIs(t, err, errTestDiff)
}

func TestDiff_Run_WorkingVsRevision_EnsureContentError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.missingChunksErr = errTestDiff
	writeWorkingFile(t, root, "a.txt", "new\n")
	client := &stubDiffClient{manifest: diffTreeNode(t, map[string]string{"a.txt": "old\n"})}

	_, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{"main"})
	require.ErrorIs(t, err, errTestDiff)
}

func TestDiff_Run_RevisionDiff_SecondRevError(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	idA := snow.ID(11)
	idB := snow.ID(22)
	client := &stubDiffClient{
		manifestErr: &domain.Error{Code: 404, Message: "not a branch"},
		commitFn: func(id *snow.ID, _ *serverDomain.Hash) (*serverDomain.TreeNode, error) {
			if *id == idB {
				return nil, errTestDiff
			}
			return diffTreeNode(t, map[string]string{"a.txt": "v1\n"}), nil
		},
	}

	_, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{idA.Base36(), idB.Base36()})
	require.ErrorIs(t, err, errTestDiff)
}

func TestDiff_Run_WorkingVsRevision_Subdir(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.config = domain.Config{Url: "http://example.com/o/p/sub/dir", Branch: "main"}
	stub.missingChunks = chunkHashes(t, "old\n")
	writeWorkingFile(t, root, "a.txt", "new\n")
	client := &stubDiffClient{
		manifest: &serverDomain.TreeNode{
			Name: "root",
			TreeChildren: []*serverDomain.TreeNode{
				{
					Name: "sub",
					TreeChildren: []*serverDomain.TreeNode{
						{
							Name:         "dir",
							FileChildren: []*serverDomain.File{{Name: "a.txt", Mode: 2, SizeBytes: 4, Chunks: chunksOfRemote(t, "old\n")}},
						},
					},
				},
				{
					Name:         "other",
					FileChildren: []*serverDomain.File{{Name: "b.txt", Mode: 2, SizeBytes: 1, Chunks: chunksOfRemote(t, "x")}},
				},
			},
		},
		downloadData: chunkDataMap(t, "old\n"),
	}

	res, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{"main"})
	require.NoError(t, err)
	require.Len(t, res.Files, 1, "only the subpath file is compared; outside files are dropped")
	require.Equal(t, "a.txt", res.Files[0].Change.Path)
	require.Equal(t, []byte("old\n"), res.Files[0].Old)
	require.Equal(t, []byte("new\n"), res.Files[0].New)
}

func chunksOfRemote(t *testing.T, content string) []serverDomain.Chunk {
	t.Helper()
	chunks, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	var out []serverDomain.Chunk
	for _, c := range chunks {
		out = append(out, serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))})
	}
	return out
}

func TestDiff_Run_RevisionDiff_Subdir(t *testing.T) {
	stub, root := diffTestRepo(t, nil, nil)
	stub.config = domain.Config{Url: "http://example.com/o/p/sub", Branch: "main"}
	stub.missingChunks = chunkHashes(t, "v1\n", "v2\n")
	idA := snow.ID(11)
	idB := snow.ID(22)
	trees := map[snow.ID]*serverDomain.TreeNode{
		idA: {
			Name: "root",
			TreeChildren: []*serverDomain.TreeNode{{
				Name:         "sub",
				FileChildren: []*serverDomain.File{{Name: "a.txt", Mode: 2, SizeBytes: 3, Chunks: chunksOfRemote(t, "v1\n")}},
			}},
		},
		idB: {
			Name: "root",
			TreeChildren: []*serverDomain.TreeNode{{
				Name:         "sub",
				FileChildren: []*serverDomain.File{{Name: "a.txt", Mode: 2, SizeBytes: 3, Chunks: chunksOfRemote(t, "v2\n")}},
			}},
		},
	}
	client := &stubDiffClient{
		manifestErr: &domain.Error{Code: 404, Message: "not a branch"},
		commitFn: func(id *snow.ID, _ *serverDomain.Hash) (*serverDomain.TreeNode, error) {
			return trees[*id], nil
		},
		downloadData: chunkDataMap(t, "v1\n", "v2\n"),
	}

	res, err := newTestDiff(t, stub, client).Run(context.Background(), root, []string{idA.Base36(), idB.Base36()})
	require.NoError(t, err)
	require.Len(t, res.Files, 1)
	require.Equal(t, "a.txt", res.Files[0].Change.Path)
	require.Equal(t, []byte("v1\n"), res.Files[0].Old)
	require.Equal(t, []byte("v2\n"), res.Files[0].New)
}

func TestScopeEntries(t *testing.T) {
	m := map[string]clientDiff.Entry{
		"sub/a.txt":   {Path: "sub/a.txt"},
		"sub/d/b.txt": {Path: "sub/d/b.txt"},
		"other/c.txt": {Path: "other/c.txt"},
	}
	require.Equal(t, m, scopeEntries(m, ""))
	require.Equal(t, m, scopeEntries(m, "/"))

	got := scopeEntries(m, "sub")
	require.Len(t, got, 2)
	require.Equal(t, "a.txt", got["a.txt"].Path)
	require.Equal(t, "d/b.txt", got["d/b.txt"].Path)

	got = scopeEntries(m, "/sub/")
	require.Len(t, got, 2)

	require.Empty(t, scopeEntries(m, "missing"))
	require.Empty(t, scopeEntries(nil, "sub"))
}
