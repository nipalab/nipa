package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

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
	client *stubDiffClient
	commit string
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
	fx.client = &stubDiffClient{
		commits:  map[string]*clientDomain.CommitDetail{fx.commit: {ID: fx.commit, Tree: encodedTree(t, old, revisionChunks)}},
		download: revisionChunks,
	}
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
	err := NewRevisionRestore(diffAuth(t), &stubDiffClient{}, repo).Run(context.Background(), filepath.Join(t.TempDir(), "file-not-dir", "x"), "a.txt", snow.ID(7).Base36())
	require.Error(t, err)
}

type failingRevisionRepo struct {
	*localrepo.LocalRepo
}

func (f *failingRevisionRepo) Init(string) error { return errors.New("init failed") }
