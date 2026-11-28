package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type revisionFixture struct {
	root   string
	lr     *localrepo.LocalRepo
	client *revisionServer
	commit string
}

// revisionServer answers directory manifests of the commits a stubDiffClient
// holds, the way the server does: 404 for a commit or a folder it lacks.
type revisionServer struct {
	*stubDiffClient
	dirs        []string
	manifestErr error
	rootErr     error
}

func (s *revisionServer) GetCommitDirManifest(_ context.Context, _, _, commitID, dir string) (*serverDomain.TreeNode, error) {
	s.dirs = append(s.dirs, dir)
	if s.manifestErr != nil {
		return nil, s.manifestErr
	}
	if dir == "" && s.rootErr != nil {
		return nil, s.rootErr
	}
	commit, ok := s.commits[commitID]
	if !ok {
		return nil, &clientDomain.Error{Code: 404, Message: "commit not found"}
	}
	node := commit.Tree
	for _, name := range strings.Split(dir, "/") {
		if name == "" {
			continue
		}
		var next *serverDomain.TreeNode
		for _, child := range node.TreeChildren {
			if child.Name == name {
				next = child
			}
		}
		if next == nil {
			return nil, &clientDomain.Error{Code: 404, Message: "path not found in commit"}
		}
		node = next
	}
	return commit.Tree, nil
}

// streamingRevisionServer also streams chunk bodies, like the gRPC client.
type streamingRevisionServer struct {
	*revisionServer
	*streamingFake
}

func (s *streamingRevisionServer) DownloadChunks(ctx context.Context, scope clientDomain.ChunkScope, hashes []serverDomain.Hash, onChunk func(serverDomain.Hash, []byte) error) error {
	return s.streamingFake.DownloadChunks(ctx, scope, hashes, onChunk)
}

func encodedTree(t *testing.T, files map[string]string, stored map[serverDomain.Hash][]byte) *serverDomain.TreeNode {
	t.Helper()
	tree := &serverDomain.TreeNode{Name: "root", Hash: serverDomain.Hash{9}}
	for name, content := range files {
		fileHash, chunks, data, encoding := testEncodedFile(t, name, content)
		for h, b := range data {
			stored[h] = b
		}
		tree.FileChildren = append(tree.FileChildren, &serverDomain.File{
			Name: name, Mode: 2, SizeBytes: int64(len(content)), IsBinary: chunker.IsBinary([]byte(content)), Encoding: encoding, Hash: fileHash, Chunks: chunks,
		})
	}
	return tree
}

// newRevisionFixture saves current as the synced snapshot and working copy,
// and serves old as commit 7.
func newRevisionFixture(t *testing.T, current, old map[string]string) *revisionFixture {
	t.Helper()
	fx := &revisionFixture{root: t.TempDir(), lr: localrepo.NewLocalRepo(), commit: snow.ID(7).Base36()}
	t.Cleanup(func() { _ = fx.lr.Close() })
	require.NoError(t, fx.lr.Init(fx.root))
	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	snapshotChunks := map[serverDomain.Hash][]byte{}
	require.NoError(t, fx.lr.SaveTree(encodedTree(t, current, snapshotChunks)))
	for name, content := range current {
		writeRepoFile(t, fx.root, name, content)
	}
	revisionChunks := map[serverDomain.Hash][]byte{}
	fx.client = &revisionServer{stubDiffClient: &stubDiffClient{
		commits:  map[string]*clientDomain.CommitDetail{fx.commit: {ID: fx.commit, Tree: blameTree(t, old, revisionChunks)}},
		download: revisionChunks,
	}}
	return fx
}

func (fx *revisionFixture) run(t *testing.T, path, commit string) error {
	t.Helper()
	return NewRevisionRestore(diffAuth(t), fx.client, fx.lr).Run(context.Background(), fx.root, path, commit)
}

func (fx *revisionFixture) read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.root, filepath.FromSlash(path)))
	require.NoError(t, err)
	return string(data)
}

func (fx *revisionFixture) status(t *testing.T) *clientDomain.Status {
	t.Helper()
	wc, err := NewWorkingCopy(fx.lr, fx.root)
	require.NoError(t, err)
	st, err := wc.Status(context.Background())
	require.NoError(t, err)
	return st
}

func TestRevisionRestore_CleanTextFile(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"notes.txt": "version two\n"}, map[string]string{"notes.txt": "version one\n"})

	require.NoError(t, fx.run(t, "notes.txt", fx.commit))

	require.Equal(t, "version one\n", fx.read(t, "notes.txt"))
	require.NotEmpty(t, fx.client.downloaded)
	st := fx.status(t)
	require.Equal(t, []string{"notes.txt"}, st.Modified)
	require.Empty(t, st.Staged)
}

func TestRevisionRestore_DecodesZstdText(t *testing.T) {
	old := ""
	for range 400 {
		old += "compressible line of text\n"
	}
	fx := newRevisionFixture(t, map[string]string{"big.txt": "short\n"}, map[string]string{"big.txt": old})
	tree := fx.client.commits[fx.commit].Tree
	require.Equal(t, chunker.EncodingZstd, tree.FileChildren[0].Encoding)

	require.NoError(t, fx.run(t, "big.txt", fx.commit))
	require.Equal(t, old, fx.read(t, "big.txt"))
}

func TestRevisionRestore_OverwritesLocalChanges(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "synced\n"}, map[string]string{"a.txt": "old\n"})
	writeRepoFile(t, fx.root, "a.txt", "my unsaved edit\n")

	require.NoError(t, fx.run(t, "a.txt", fx.commit))
	require.Equal(t, "old\n", fx.read(t, "a.txt"))
}

func TestRevisionRestore_BinaryAndUntrackedPath(t *testing.T) {
	bin := "\x89PNG\x00\x01\x02 old pixels"
	fx := newRevisionFixture(t, map[string]string{"keep.txt": "x\n"}, map[string]string{"hero.png": bin})

	require.NoError(t, fx.run(t, "hero.png", fx.commit))
	require.Equal(t, bin, fx.read(t, "hero.png"))
	require.Equal(t, []string{"hero.png"}, fx.status(t).Untracked)
}

func TestRevisionRestore_KeepsReadOnlyWorkingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"a.txt": "old\n"})
	full := filepath.Join(fx.root, "a.txt")
	require.NoError(t, os.Chmod(full, 0o444))

	require.NoError(t, fx.run(t, "a.txt", fx.commit))
	require.Equal(t, "old\n", fx.read(t, "a.txt"))
	info, err := os.Stat(full)
	require.NoError(t, err)
	require.Zero(t, info.Mode().Perm()&0o222)
}

func TestRevisionRestore_MissingRevisionOrPath(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"a.txt": "old\n"})

	err := fx.run(t, "a.txt", snow.ID(99).Base36())
	requireCode(t, err, 400)
	require.ErrorContains(t, err, "not found")

	err = fx.run(t, "b.txt", fx.commit)
	require.ErrorIs(t, err, ErrFileMissing)
	requireCode(t, err, 404)

	requireCode(t, fx.run(t, "a.txt", ""), 400)
	requireCode(t, fx.run(t, "a.txt", "not base36!"), 400)
	requireCode(t, fx.run(t, "../a.txt", fx.commit), 400)
	require.Equal(t, "new\n", fx.read(t, "a.txt"))
}

func TestRevisionRestore_RefusesPendingOperations(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"a.txt": "old\n"})
	require.NoError(t, fx.lr.SaveMergeState(&clientDomain.MergeState{SourceBranch: "feature"}))
	err := fx.run(t, "a.txt", fx.commit)
	requireCode(t, err, 400)
	require.ErrorContains(t, err, "merge is in progress")
	require.NoError(t, fx.lr.ClearMergeState())

	require.NoError(t, fx.lr.SaveRevertState(&clientDomain.RevertState{CurrentTreeHash: "x"}))
	err = fx.run(t, "a.txt", fx.commit)
	require.ErrorContains(t, err, "revert is in progress")
	require.Equal(t, "new\n", fx.read(t, "a.txt"))
}

func TestRevisionRestore_RefusesOutsideSparseAndFolders(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"a.txt": "old\n", "Art/b.png": "\x00b"})
	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "main", Sparse: []string{"Source"}}))
	err := fx.run(t, "a.txt", fx.commit)
	requireCode(t, err, 400)
	require.ErrorContains(t, err, "outside this clone's checkout")

	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "main", Sparse: []string{"a.txt"}}))
	require.NoError(t, fx.run(t, "a.txt", fx.commit))

	require.NoError(t, os.MkdirAll(filepath.Join(fx.root, "dir"), 0o755))
	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "http://example.com/org/project", Branch: "main"}))
	require.ErrorContains(t, fx.run(t, "dir", fx.commit), "is a folder")
}

func TestRevisionRestore_TransportErrors(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"a.txt": "old\n"})
	fx.client.connectErr = errors.New("offline")
	require.EqualError(t, fx.run(t, "a.txt", fx.commit), "offline")

	fx.client.connectErr = nil
	fx.client.downloadErr = errors.New("download failed")
	require.ErrorContains(t, fx.run(t, "a.txt", fx.commit), "download failed")

	fx.client.downloadErr = nil
	fx.client.download = map[serverDomain.Hash][]byte{}
	require.Error(t, fx.run(t, "a.txt", fx.commit))
	require.Equal(t, "new\n", fx.read(t, "a.txt"))

	require.NoError(t, fx.lr.SaveConfig(clientDomain.Config{Url: "::bad", Branch: "main"}))
	require.Error(t, fx.run(t, "a.txt", fx.commit))
}

func TestRevisionRestore_InitError(t *testing.T) {
	repo := &failingRevisionRepo{LocalRepo: localrepo.NewLocalRepo()}
	err := NewRevisionRestore(diffAuth(t), &revisionServer{stubDiffClient: &stubDiffClient{}}, repo).Run(context.Background(), filepath.Join(t.TempDir(), "file-not-dir", "x"), "a.txt", snow.ID(7).Base36())
	require.Error(t, err)
}

type failingRevisionRepo struct {
	*localrepo.LocalRepo
}

func (f *failingRevisionRepo) Init(string) error { return errors.New("init failed") }

func TestRevisionRestore_BinaryIsWrittenWithoutTheCache(t *testing.T) {
	bin := "\x89PNG\x00\x01\x02 old pixels"
	fx := newRevisionFixture(t, map[string]string{"keep.txt": "x\n"}, map[string]string{"art/deep/hero.png": bin, "art/notes.txt": "old\n"})
	repo := &pruneCountingRepo{LocalRepo: fx.lr}
	run := func(path string) error {
		return NewRevisionRestore(diffAuth(t), fx.client, repo).Run(context.Background(), fx.root, path, fx.commit)
	}

	require.NoError(t, run("art/deep/hero.png"))
	require.Equal(t, bin, fx.read(t, "art/deep/hero.png"))
	require.Equal(t, []string{"art/deep"}, fx.client.dirs, "only the file's folder is asked for, never the commit's whole tree")
	require.Empty(t, fx.client.commitLookups)
	require.Zero(t, objectCount(t, fx.root), "raw content never passes through the object cache")
	require.Zero(t, repo.prunes, "nothing was cached, so nothing is pruned")
	requireNoTempFiles(t, fx.root)

	require.NoError(t, run("art/notes.txt"))
	require.Equal(t, "old\n", fx.read(t, "art/notes.txt"))
	require.Equal(t, 1, repo.prunes, "text goes through the cache and is pruned after")
	require.Zero(t, objectCount(t, fx.root))

	require.NoError(t, run("art/notes.txt"))
	require.Equal(t, 2, repo.prunes)
}

type pruneCountingRepo struct {
	*localrepo.LocalRepo
	prunes int
}

func (p *pruneCountingRepo) PruneObjects(keep map[serverDomain.Hash]struct{}) (int, int64, error) {
	p.prunes++
	return p.LocalRepo.PruneObjects(keep)
}

func TestRevisionRestore_StreamsBinaryAndCopiesCachedChunks(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"keep.txt": "x\n"}, nil)
	data := map[serverDomain.Hash][]byte{}
	file := rawManifestFile("big.bin", data, "HEAD", "BODY", "HEAD")
	fx.client.commits[fx.commit].Tree = &serverDomain.TreeNode{Name: "root", FileChildren: []*serverDomain.File{file}}
	body := chunker.Sum([]byte("BODY"))
	require.NoError(t, fx.lr.StoreChunk(body, []byte("BODY")))
	server := &streamingRevisionServer{revisionServer: fx.client, streamingFake: newStreamingFake(data)}

	require.NoError(t, NewRevisionRestore(diffAuth(t), server, fx.lr).Run(context.Background(), fx.root, "big.bin", fx.commit))

	require.Equal(t, "HEADBODYHEAD", fx.read(t, "big.bin"))
	require.Equal(t, map[serverDomain.Hash]int{chunker.Sum([]byte("HEAD")): 1}, server.streamed, "the cached chunk is copied, the other streamed once")
	require.Empty(t, server.buffered)
	requireNoTempFiles(t, fx.root)
}

func TestRevisionRestore_DirectFailureLeavesTheFile(t *testing.T) {
	bin := "\x00 old pixels"
	fx := newRevisionFixture(t, map[string]string{"hero.png": "\x00 new pixels"}, map[string]string{"hero.png": bin})
	fx.client.download = map[serverDomain.Hash][]byte{}
	err := fx.run(t, "hero.png", fx.commit)
	require.ErrorContains(t, err, "did not return chunk")
	require.Equal(t, "\x00 new pixels", fx.read(t, "hero.png"))
	requireNoTempFiles(t, fx.root)

	fx.client.downloadErr = errors.New("download failed")
	require.ErrorContains(t, fx.run(t, "hero.png", fx.commit), "download failed")
	requireNoTempFiles(t, fx.root)
}

func TestRevisionRestore_RefusesUnsafePathsBeforeAnyRequest(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"a.txt": "old\n"})
	for _, p := range []string{".nipa/config", "sub/.NIPA/config", "sub/.nipa./x", "a/b\x00c"} {
		requireCode(t, fx.run(t, p, fx.commit), 400)
	}
	require.Empty(t, fx.client.connectHost)
	require.Empty(t, fx.client.dirs)
}

func TestRevisionRestore_RefusesToWriteThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges")
	}
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"out/secret.txt": "old\n", "meta/x.txt": "old\n"})
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(fx.root, "out")))
	require.NoError(t, os.Symlink(filepath.Join(fx.root, ".nipa"), filepath.Join(fx.root, "meta")))

	err := fx.run(t, "out/secret.txt", fx.commit)
	requireLinkRefusal(t, err, "write", "out/secret.txt", "out")
	require.NoFileExists(t, filepath.Join(outside, "secret.txt"))
	err = fx.run(t, "out/new/deeper/secret.txt", fx.commit)
	requireLinkRefusal(t, err, "write", "out/new/deeper/secret.txt", "out")

	requireLinkRefusal(t, fx.run(t, "meta/x.txt", fx.commit), "write", "meta/x.txt", "meta")
	require.NoFileExists(t, filepath.Join(fx.root, ".nipa", "x.txt"))
	require.Empty(t, fx.client.dirs)
	require.Empty(t, listing(t, outside))
	requireNoTempFiles(t, fx.root)
}

func TestRevisionRestore_RefusesALinkPointingInsideTheClone(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"alias/a.txt": "old\n"})
	symlinkOrSkip(t, fx.root, filepath.Join(fx.root, "alias"))
	requireLinkRefusal(t, fx.run(t, "alias/a.txt", fx.commit), "write", "alias/a.txt", "alias")
	require.Equal(t, "new\n", fx.read(t, "a.txt"))
}

func TestCheckWriteInsideRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, checkWriteInsideRoot(root, "a/b/c.txt"))
	requireCode(t, checkWriteInsideRoot(root, "a/../../x.txt"), 400)

	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), nil, 0o644))
	require.NoError(t, checkWriteInsideRoot(root, "file/below/x.txt"), "a file in the way is left to the write, which fails on it")
}

// The synced snapshot tracks a/b as a file and the older commit holds it as a
// folder: both commits are sound, so the tracked file is only in the way and
// the refusal must not call the target commit damaged.
func TestRevisionRestore_RefusesATrackedFileWhereAFolderIsNeeded(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a/b": "file now\n"}, map[string]string{"a/b/c": "old\n"})
	_, tracked, err := fx.lr.SnapshotFile("a/b")
	require.NoError(t, err)
	require.True(t, tracked, "a/b is tracked in the synced snapshot")

	err = fx.run(t, "a/b/c", fx.commit)
	requireCode(t, err, 400)
	require.ErrorContains(t, err, "a/b (untracked or kept file where a folder is needed)")
	require.NotContains(t, err.Error(), "the target commit is damaged")
	require.NotContains(t, err.Error(), damagedTargetReason)

	require.Equal(t, "file now\n", fx.read(t, "a/b"))
	require.Empty(t, fx.client.dirs, "refused before any request")
	require.Empty(t, fx.status(t).Modified)
	requireNoTempFiles(t, fx.root)
}

func TestRevisionRestore_RefusesPendingShelfApply(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"a.txt": "old\n"})
	require.NoError(t, fx.lr.SaveShelfState(&clientDomain.ShelfState{ShelfID: "s1", ShelfName: "wip"}))
	err := fx.run(t, "a.txt", fx.commit)
	requireCode(t, err, 400)
	require.ErrorContains(t, err, "stopped on conflicts")
	require.Equal(t, "new\n", fx.read(t, "a.txt"))
}

func TestRevisionRestore_SweepsTempFilesOfAKilledWrite(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"a.txt": "old\n"})
	leftover := leaveTemp(t, fx.root, "dir/.nipa-materialize-999", time.Minute)
	leaveDeadWriteMarker(t, fx.root)

	require.NoError(t, fx.run(t, "a.txt", fx.commit))
	require.NoFileExists(t, leftover)
	require.Equal(t, "old\n", fx.read(t, "a.txt"))
}

func TestRevisionRestore_TellsAMissingFolderFromAMissingCommit(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"art/a.txt": "old\n"})

	err := fx.run(t, "gone/a.txt", fx.commit)
	require.ErrorIs(t, err, ErrFileMissing)
	require.Equal(t, []string{"gone", ""}, fx.client.dirs, "the commit's root tells the two apart")

	err = fx.run(t, "art/a.txt", snow.ID(99).Base36())
	requireCode(t, err, 400)
	require.ErrorContains(t, err, "not found")

	fx.client.rootErr = errors.New("root unavailable")
	require.EqualError(t, fx.run(t, "gone/a.txt", fx.commit), "root unavailable")

	fx.client.manifestErr = errors.New("manifest unavailable")
	require.EqualError(t, fx.run(t, "art/a.txt", fx.commit), "manifest unavailable")
}

type brokenRevisionRepo struct {
	*localrepo.LocalRepo
	missingErr, snapshotErr error
}

func (b *brokenRevisionRepo) MissingChunks(h []serverDomain.Hash) ([]serverDomain.Hash, error) {
	if b.missingErr != nil {
		return nil, b.missingErr
	}
	return b.LocalRepo.MissingChunks(h)
}

func (b *brokenRevisionRepo) SnapshotFile(p string) (clientDomain.SnapshotFile, bool, error) {
	if b.snapshotErr != nil {
		return clientDomain.SnapshotFile{}, false, b.snapshotErr
	}
	return b.LocalRepo.SnapshotFile(p)
}

func TestRevisionRestore_LocalRepoErrors(t *testing.T) {
	fx := newRevisionFixture(t, map[string]string{"a.txt": "new\n"}, map[string]string{"a.txt": "old\n", "hero.png": "\x00 old"})
	repo := &brokenRevisionRepo{LocalRepo: fx.lr, missingErr: errors.New("cache unreadable")}
	run := func(path string) error {
		return NewRevisionRestore(diffAuth(t), fx.client, repo).Run(context.Background(), fx.root, path, fx.commit)
	}
	require.EqualError(t, run("a.txt"), "cache unreadable")
	require.EqualError(t, run("hero.png"), "cache unreadable")
	requireNoTempFiles(t, fx.root)

	repo.missingErr, repo.snapshotErr = nil, errors.New("snapshot unreadable")
	require.EqualError(t, run("a.txt"), "snapshot unreadable")
	require.Equal(t, "new\n", fx.read(t, "a.txt"))
}
