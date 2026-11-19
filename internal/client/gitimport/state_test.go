package gitimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/require"

	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/gitimport"
)

func sampleBlob() *gitimport.Blob {
	return &gitimport.Blob{
		FileHash:   serverDomain.Hash{1},
		Chunks:     []serverDomain.Hash{{2}, {3}},
		ChunkSizes: []int64{10, 70000},
		Encoding:   "zstd",
		Size:       70010,
		IsBinary:   true,
	}
}

func TestState_BlobLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenState(dir, false)
	require.NoError(t, err)
	key := gitimport.BlobKey{SHA: "abc", Profile: "text"}

	_, ok, err := s.Get(key)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.Put(key, sampleBlob()))
	got, ok, err := s.Get(key)
	require.NoError(t, err)
	require.True(t, ok, "a pending blob is a hit in the same run")
	require.Equal(t, sampleBlob(), got)
	require.Equal(t, []gitimport.BlobKey{key}, s.PendingKeys())
	require.NoError(t, s.Close())

	s, err = OpenState(dir, false)
	require.NoError(t, err)
	_, ok, err = s.Get(key)
	require.NoError(t, err)
	require.False(t, ok, "a blob never uploaded is chunked again")
	require.NoError(t, s.Put(key, sampleBlob()))
	require.NoError(t, s.MarkUploaded(s.PendingKeys()))
	require.Empty(t, s.PendingKeys())
	require.NoError(t, s.MarkUploaded(nil))
	require.NoError(t, s.Close())

	s, err = OpenState(dir, false)
	require.NoError(t, err)
	got, ok, err = s.Get(key)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, sampleBlob(), got)

	empty := gitimport.BlobKey{SHA: "empty", Profile: "text"}
	require.NoError(t, s.Put(empty, &gitimport.Blob{FileHash: serverDomain.Hash{9}, Encoding: "zstd"}))
	require.NoError(t, s.MarkUploaded([]gitimport.BlobKey{empty}))
	got, ok, err = s.Get(empty)
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, got.Chunks)

	_, err = s.db.Exec(`UPDATE blobs SET chunk_sizes = ? WHERE git_sha = 'abc'`, []byte{0xff})
	require.NoError(t, err)
	_, ok, err = s.Get(key)
	require.NoError(t, err)
	require.False(t, ok, "a corrupt row is a miss")
	_, err = s.db.Exec(`UPDATE blobs SET file_hash = ? WHERE git_sha = 'abc'`, []byte{1})
	require.NoError(t, err)
	_, ok, err = s.Get(key)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, s.Close())

	s, err = OpenState(dir, true)
	require.NoError(t, err)
	_, ok, err = s.Get(empty)
	require.NoError(t, err)
	require.False(t, ok, "reset drops every row")
	require.NoError(t, s.Close())
	_, _, err = s.Get(key)
	require.Error(t, err)
}

func TestState_CheckTarget(t *testing.T) {
	s, err := OpenState(t.TempDir(), false)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	target := StateTarget{Host: "h", Org: "o", Project: "p", SourceKey: "default"}
	require.NoError(t, s.CheckTarget(target))
	require.NoError(t, s.CheckTarget(target))
	other := target
	other.SourceKey = "game"
	require.ErrorContains(t, s.CheckTarget(other), "--reset-state")

	_, err = s.db.Exec(`UPDATE meta SET value = 'x' WHERE key = 'target'`)
	require.NoError(t, err)
	require.ErrorContains(t, s.CheckTarget(target), "read import state target")
}

func TestOpenStateErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	_, err := OpenState(filepath.Join(file, "sub"), false)
	require.ErrorContains(t, err, "create import state folder")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, StateFileName), []byte("not a database at all, just text that is long enough"), 0o600))
	_, err = OpenState(dir, false)
	require.Error(t, err)
}

func TestGitDir(t *testing.T) {
	repo := newSampleRepo(t)
	src, err := gitimport.Open(repo.dir)
	require.NoError(t, err)
	dir, err := GitDir(src)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(repo.dir, ".git"), dir)

	mem, err := git.Init(memory.NewStorage(), nil)
	require.NoError(t, err)
	_, err = GitDir(gitimport.NewSource(mem))
	require.ErrorContains(t, err, "not stored on disk")
}

func TestDecodeSizes(t *testing.T) {
	sizes, err := decodeSizes(encodeSizes([]int64{0, 1, 1 << 40}))
	require.NoError(t, err)
	require.Equal(t, []int64{0, 1, 1 << 40}, sizes)
	_, err = decodeSizes([]byte{0x80})
	require.Error(t, err)
}
